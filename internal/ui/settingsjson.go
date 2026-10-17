package ui

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/config"
	"tuicode/internal/lsp"
)

// isSettingsJSON reports whether the editor shows a VS Code or tuicode settings.json.
func (v *EditorView) isSettingsJSON() bool {
	p := v.buf().Path
	if filepath.Base(p) != "settings.json" {
		return false
	}
	switch filepath.Base(filepath.Dir(p)) {
	case ".vscode", ".tuicode", "User":
		return true
	}
	return false
}

// jsonContext describes where the cursor sits in a JSON document.
type jsonContext struct {
	inKey     bool       // inside (or starting) an object key
	keyStart  buffer.Pos // first rune of the key text (after the quote, if any)
	quoted    bool
	inValue   bool   // right after `"key":`
	key       string // the key whose value is being typed
	path      []string
	valStart  buffer.Pos
	valQuoted bool
}

// scanJSONContext tokenizes the document up to p to find the key or value position at p.
func scanJSONContext(b *buffer.Buffer, p buffer.Pos) jsonContext {
	type frame struct {
		object    bool
		expectKey bool
		key       string
	}
	var stack []frame
	var ctx jsonContext
	inString, inLineComment, inBlockComment, escaped := false, false, false, false
	var strStart buffer.Pos
	var str strings.Builder
	lastKey := ""
	afterColon := false
	for ln := 0; ln <= p.Line && ln < b.LineCount(); ln++ {
		line := b.Line(ln)
		end := len(line)
		if ln == p.Line {
			end = min(p.Col, len(line))
		}
		inLineComment = false
		for i := 0; i < end; i++ {
			r := line[i]
			switch {
			case inLineComment:
				continue
			case inBlockComment:
				if r == '*' && i+1 < len(line) && line[i+1] == '/' {
					inBlockComment = false
					i++
				}
				continue
			case inString:
				if escaped {
					escaped = false
				} else if r == '\\' {
					escaped = true
				} else if r == '"' {
					inString = false
					if len(stack) > 0 && stack[len(stack)-1].object && stack[len(stack)-1].expectKey {
						lastKey = str.String()
					}
				} else {
					str.WriteRune(r)
				}
				continue
			}
			switch r {
			case '/':
				if i+1 < len(line) && line[i+1] == '/' {
					inLineComment = true
				} else if i+1 < len(line) && line[i+1] == '*' {
					inBlockComment = true
					i++
				}
			case '"':
				inString = true
				strStart = buffer.Pos{Line: ln, Col: i + 1}
				str.Reset()
			case '{', '[':
				if len(stack) > 0 {
					stack[len(stack)-1].key = lastKey
				}
				stack = append(stack, frame{object: r == '{', expectKey: r == '{'})
				afterColon = false
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				afterColon = false
			case ':':
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].expectKey = false
					afterColon = true
				}
			case ',':
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].expectKey = true
				}
				afterColon = false
			case ' ', '\t':
			default:
				afterColon = false
			}
		}
	}
	if inLineComment || inBlockComment || len(stack) == 0 || !stack[len(stack)-1].object {
		return ctx
	}
	for _, f := range stack[:len(stack)-1] {
		ctx.path = append(ctx.path, f.key)
	}
	top := stack[len(stack)-1]
	line := b.Line(p.Line)
	switch {
	case inString && top.expectKey:
		ctx.inKey, ctx.quoted, ctx.keyStart = true, true, strStart
	case !inString && top.expectKey:
		s := p.Col
		for s > 0 && (isWordRune(line[s-1]) || line[s-1] == '.') {
			s--
		}
		ctx.inKey, ctx.keyStart = true, buffer.Pos{Line: p.Line, Col: s}
	case !top.expectKey && inString && afterColon:
		ctx.inValue, ctx.key, ctx.valQuoted, ctx.valStart = true, lastKey, true, strStart
	case !top.expectKey && !inString:
		s := p.Col
		for s > 0 && isWordRune(line[s-1]) {
			s--
		}
		t := s
		for t > 0 && (line[t-1] == ' ' || line[t-1] == '\t') {
			t--
		}
		if t > 0 && line[t-1] == ':' {
			ctx.inValue, ctx.key, ctx.valStart = true, lastKey, buffer.Pos{Line: p.Line, Col: s}
		}
	}
	return ctx
}

// settingsCompletions suggests setting keys and boolean values inside settings.json.
func (a *App) settingsCompletions(v *EditorView) ([]*citem, buffer.Pos, bool) {
	if !v.isSettingsJSON() {
		return nil, buffer.Pos{}, false
	}
	b := v.buf()
	cur := v.Cursor()
	ctx := scanJSONContext(b, cur)
	depthOK := len(ctx.path) == 0 || len(ctx.path) == 1 && strings.HasPrefix(ctx.path[0], "[")
	if !depthOK {
		return nil, buffer.Pos{}, false
	}
	line := b.Line(cur.Line)
	if ctx.inKey {
		end := cur
		if ctx.quoted {
			// replace through the closing quote, and an existing colon after it
			e := cur.Col
			for e < len(line) && line[e] != '"' {
				e++
			}
			if e < len(line) {
				e++
			}
			end.Col = e
		}
		start := ctx.keyStart
		replace := buffer.Range{Start: start, End: end}
		if ctx.quoted {
			replace.Start.Col--
		}
		keys := make([]string, 0, len(config.Defaults))
		for k := range config.Defaults {
			if strings.HasPrefix(k, "[") {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		items := make([]*citem, 0, len(keys))
		for _, k := range keys {
			def := config.Defaults[k]
			raw, _ := json.Marshal(def)
			val := string(raw)
			var snippetVal string
			switch d := def.(type) {
			case string:
				snippetVal = `"${1:` + snippetEscape(d) + `}"`
			case bool, float64, int:
				snippetVal = "${1:" + val + "}"
			default:
				snippetVal = "${1:" + snippetEscape(val) + "}"
			}
			body := `"` + k + `": ` + snippetVal
			r := replace
			ci := &citem{
				item:     &lsp.CompletionItem{Label: k, InsertText: body, InsertTextFormat: 2, Kind: lsp.CIKProperty},
				label:    k,
				filter:   k,
				sortText: k,
				kind:     lsp.CIKProperty,
				detail:   val,
				resolved: true,
				docs:     "Default: `" + val + "`",
				rng:      &r,
			}
			items = append(items, ci)
		}
		return items, start, true
	}
	if ctx.inValue {
		if _, isBool := config.Defaults[ctx.key].(bool); isBool && !ctx.valQuoted {
			end := cur
			for end.Col < len(line) && isWordRune(line[end.Col]) {
				end.Col++
			}
			r := buffer.Range{Start: ctx.valStart, End: end}
			var items []*citem
			for _, s := range []string{"true", "false"} {
				rr := r
				items = append(items, &citem{label: s, filter: s, sortText: s, kind: lsp.CIKValue, resolved: true, rng: &rr})
			}
			return items, ctx.valStart, true
		}
	}
	return nil, buffer.Pos{}, false
}

func snippetEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "$", `\$`, "}", `\}`).Replace(s)
}

// settingsJSONAfterType keeps setting suggestions open while a key is typed. It reports whether it handled ch.
func (a *App) settingsJSONAfterType(v *EditorView, ch rune) bool {
	if !v.isSettingsJSON() || !(isWordRune(ch) || ch == '.' || ch == '"' || ch == ' ' || ch == ':') {
		return false
	}
	if _, _, ok := a.settingsCompletions(v); !ok {
		return false
	}
	if v.completion != nil && v.completion.active && ch != '"' && ch != ':' {
		a.refilterCompletion(v)
	} else {
		a.triggerCompletion(v, false, "")
	}
	return true
}
