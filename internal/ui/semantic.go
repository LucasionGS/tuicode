package ui

import (
	"encoding/json"
	"time"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
	"tuicode/internal/syntax"
)

// semanticKind maps LSP semantic token types (and modifiers) to theme kinds.
func semanticKind(tokenType string, mods []string) (syntax.Kind, bool) {
	has := func(m string) bool {
		for _, x := range mods {
			if x == m {
				return true
			}
		}
		return false
	}
	switch tokenType {
	case "namespace":
		return syntax.KNamespace, true
	case "type", "class", "enum", "interface", "struct", "typeParameter", "typeAlias":
		return syntax.KType, true
	case "parameter":
		return syntax.KParameter, true
	case "variable":
		if has("readonly") || has("static") && has("readonly") {
			return syntax.KConstant, true
		}
		if has("defaultLibrary") {
			return syntax.KConstant, true
		}
		return syntax.KVariable, true
	case "property":
		if has("readonly") && has("static") {
			return syntax.KConstant, true
		}
		return syntax.KProperty, true
	case "enumMember":
		return syntax.KConstant, true
	case "function", "method":
		if has("defaultLibrary") {
			return syntax.KBuiltin, true
		}
		return syntax.KFunction, true
	case "macro":
		return syntax.KPreproc, true
	case "keyword":
		return syntax.KKeyword, true
	case "modifier":
		return syntax.KStorage, true
	case "comment":
		return syntax.KComment, true
	case "string":
		return syntax.KString, true
	case "number":
		return syntax.KNumber, true
	case "regexp":
		return syntax.KRegex, true
	case "operator":
		return syntax.KOperator, true
	case "decorator":
		return syntax.KDecorator, true
	case "label":
		return syntax.KLabel, true
	}
	return 0, false
}

// scheduleSemanticTokens requests semantic tokens shortly after edits.
func (a *App) scheduleSemanticTokens(d *Document) {
	if a.lsp == nil || d.Buf.Path == "" {
		return
	}
	if d.semTimer != nil {
		d.semTimer.Stop()
	}
	d.semTimer = time.AfterFunc(250*time.Millisecond, func() {
		a.Post(func() { a.requestSemanticTokens(d) })
	})
}

func (a *App) requestSemanticTokens(d *Document) {
	if a.lsp == nil {
		return
	}
	if v, ok := a.settings.Get("editor.semanticHighlighting.enabled", d.Lang.ID).(bool); ok && !v {
		return
	}
	c := a.lsp.Client(d.Buf)
	if c == nil || !c.Supports("semanticTokensProvider.full") {
		return
	}
	if d.semVersion == d.Buf.Version() && d.semSpans != nil {
		return
	}
	legend, _ := c.Cap("semanticTokensProvider.legend").(map[string]any)
	types := toStrings(legend["tokenTypes"])
	mods := toStrings(legend["tokenModifiers"])
	version := d.Buf.Version()
	enc := c.PositionEncoding
	a.lsp.Request(d.Buf, "textDocument/semanticTokens/full", map[string]any{"textDocument": map[string]any{"uri": lsp.PathToURI(d.Buf.Path)}}, func(raw json.RawMessage, err error) {
		if err != nil || d.Buf.Version() != version {
			return
		}
		var res struct {
			Data []int `json:"data"`
		}
		if json.Unmarshal(raw, &res) != nil {
			return
		}
		spans := make([][]syntax.Span, d.Buf.LineCount())
		line, char := 0, 0
		for i := 0; i+4 < len(res.Data); i += 5 {
			dl, dc, length, tt, tm := res.Data[i], res.Data[i+1], res.Data[i+2], res.Data[i+3], res.Data[i+4]
			if dl > 0 {
				line += dl
				char = dc
			} else {
				char += dc
			}
			if line >= len(spans) || tt >= len(types) {
				continue
			}
			var active []string
			for bit := 0; bit < len(mods); bit++ {
				if tm&(1<<bit) != 0 {
					active = append(active, mods[bit])
				}
			}
			kind, ok := semanticKind(types[tt], active)
			if !ok {
				continue
			}
			start := lsp.FromLSPPos(d.Buf, lsp.Position{Line: line, Character: char}, enc)
			end := lsp.FromLSPPos(d.Buf, lsp.Position{Line: line, Character: char + length}, enc)
			if end.Line != start.Line {
				end = buffer.Pos{Line: start.Line, Col: d.Buf.LineLen(start.Line)}
			}
			spans[line] = append(spans[line], syntax.Span{Start: start.Col, End: end.Col, Kind: kind})
		}
		d.semSpans = spans
		d.semVersion = version
	})
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// semanticAt returns the semantic token kind covering col, if any.
func (d *Document) semanticAt(line, col int) (syntax.Kind, bool) {
	if line >= len(d.semSpans) {
		return 0, false
	}
	for _, s := range d.semSpans[line] {
		if col >= s.Start && col < s.End {
			return s.Kind, true
		}
	}
	return 0, false
}
