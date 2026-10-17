package ui

import (
	"strings"

	"tuicode/internal/buffer"
)

// tagLangs are the languages with HTML-like tags.
var tagLangs = map[string]bool{"html": true, "xml": true, "php": true, "vue": true, "svelte": true, "javascriptreact": true, "typescriptreact": true}

func isJSXLang(lang string) bool { return lang == "javascriptreact" || lang == "typescriptreact" }

func isTagNameRune(r rune) bool {
	return isWordRune(r) || r == '-' || r == ':' || r == '.'
}

// autoClosingTagsEnabled mirrors html.autoClosingTags and its JavaScript/TypeScript counterparts.
func (v *EditorView) autoClosingTagsEnabled() bool {
	switch v.lang() {
	case "javascriptreact":
		return v.app.settings.Bool("javascript.autoClosingTags", v.lang())
	case "typescriptreact":
		return v.app.settings.Bool("typescript.autoClosingTags", v.lang())
	}
	return tagLangs[v.lang()] && v.app.settings.Bool("html.autoClosingTags", v.lang())
}

// autoCloseTag inserts a closing tag after typing `>` of an opening tag, or completes `</`.
func (v *EditorView) autoCloseTag(ch rune) {
	if (ch != '>' && ch != '/') || !v.autoClosingTagsEnabled() {
		return
	}
	b := v.buf()
	edits := make([]buffer.Edit, 0, len(v.sels))
	for _, s := range v.sels {
		p := s.Active
		if !s.Empty() || p.Col == 0 {
			return
		}
		line := b.Line(p.Line)
		var insert string
		if ch == '>' {
			name := v.openTagBefore(p)
			if name == "" {
				return
			}
			closing := "</" + name + ">"
			if strings.HasPrefix(string(line[p.Col:]), closing) {
				return
			}
			insert = closing
		} else {
			if p.Col < 2 || line[p.Col-2] != '<' || isJSXLang(v.lang()) && p.Col >= 3 && isWordRune(line[p.Col-3]) {
				return
			}
			name := v.unclosedTag(buffer.Pos{Line: p.Line, Col: p.Col - 2})
			if name == "" {
				return
			}
			insert = name
			if p.Col >= len(line) || line[p.Col] != '>' {
				insert += ">"
			}
		}
		edits = append(edits, buffer.Edit{Range: buffer.Range{Start: p, End: p}, Text: insert})
	}
	if len(edits) == 0 {
		return
	}
	stay := ch == '>'
	v.applyEdits(edits, "type", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			if stay {
				out[i] = buffer.Caret(r.Start)
			} else {
				out[i] = buffer.Caret(r.End)
				if !strings.HasSuffix(edits[i].Text, ">") {
					out[i] = buffer.Caret(buffer.Pos{Line: r.End.Line, Col: r.End.Col + 1})
				}
			}
		}
		return out
	})
}

// openTagBefore returns the tag name when the `>` just before p ends an opening tag.
func (v *EditorView) openTagBefore(p buffer.Pos) string {
	line := v.buf().Line(p.Line)
	gt := p.Col - 1
	if gt < 0 || line[gt] != '>' {
		return ""
	}
	if gt > 0 && line[gt-1] == '/' {
		return ""
	}
	if v.inStringOrComment(buffer.Pos{Line: p.Line, Col: gt}) {
		return ""
	}
	// find the `<` that starts this tag, skipping quoted attribute values and JSX expressions
	lt := -1
	quote := rune(0)
	braces := 0
	for i := gt - 1; i >= 0; i-- {
		r := line[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '}':
			braces++
		case r == '{':
			braces--
		case r == '<' && braces == 0:
			lt = i
		case r == '>' && braces == 0:
			return ""
		}
		if lt >= 0 {
			break
		}
	}
	if lt < 0 || quote != 0 || braces != 0 {
		return ""
	}
	if isJSXLang(v.lang()) && lt > 0 && (isWordRune(line[lt-1]) || line[lt-1] == ')' || line[lt-1] == ']') {
		return "" // generic type arguments, not a tag
	}
	e := lt + 1
	for e < gt && isTagNameRune(line[e]) {
		e++
	}
	name := string(line[lt+1 : e])
	if name == "" || !isLetter(rune(name[0])) {
		return ""
	}
	if e < gt && line[e] != ' ' && line[e] != '\t' {
		return ""
	}
	if !isJSXLang(v.lang()) && v.lang() != "xml" && voidTags[strings.ToLower(name)] {
		return ""
	}
	return name
}

func isLetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

// tagToken is one tag found by scanTags.
type tagToken struct {
	name    string
	closing bool
	self    bool         // self-closing or void
	nameAt  buffer.Range // range of the name
}

// scanTags finds tags in lines [from, to), skipping comments.
func (v *EditorView) scanTags(from, to int) []tagToken {
	b := v.buf()
	if from < 0 {
		from = 0
	}
	if to > b.LineCount() {
		to = b.LineCount()
	}
	html := !isJSXLang(v.lang()) && v.lang() != "xml"
	var out []tagToken
	inComment := false
	for ln := from; ln < to; ln++ {
		line := b.Line(ln)
		for i := 0; i < len(line); i++ {
			if inComment {
				if line[i] == '-' && strings.HasPrefix(string(line[i:]), "-->") {
					inComment = false
					i += 2
				}
				continue
			}
			if line[i] != '<' {
				continue
			}
			if strings.HasPrefix(string(line[i:]), "<!--") {
				inComment = true
				i += 3
				continue
			}
			j := i + 1
			closing := j < len(line) && line[j] == '/'
			if closing {
				j++
			}
			s := j
			for j < len(line) && isTagNameRune(line[j]) {
				j++
			}
			// an empty name (`<>`, `</>`) stays linked while the name is retyped
			if j == s && (j >= len(line) || line[j] != '>') || j > s && !isLetter(line[s]) {
				continue
			}
			t := tagToken{name: string(line[s:j]), closing: closing, nameAt: buffer.Range{Start: buffer.Pos{Line: ln, Col: s}, End: buffer.Pos{Line: ln, Col: j}}}
			// find the end of the tag on this line to detect `/>`
			k := j
			quote := rune(0)
			for k < len(line) {
				r := line[k]
				if quote != 0 {
					if r == quote {
						quote = 0
					}
				} else if r == '"' || r == '\'' {
					quote = r
				} else if r == '>' {
					break
				}
				k++
			}
			if k < len(line) && k > j && line[k-1] == '/' {
				t.self = true
			}
			if html && !closing && voidTags[strings.ToLower(t.name)] {
				t.self = true
			}
			out = append(out, t)
			i = j - 1
		}
	}
	return out
}

// unclosedTag returns the innermost tag opened before p that is still open.
func (v *EditorView) unclosedTag(p buffer.Pos) string {
	tags := v.scanTags(p.Line-3000, p.Line+1)
	var stack []string
	for _, t := range tags {
		if !t.nameAt.Start.Less(p) {
			break
		}
		if t.self || t.name == "" {
			continue
		}
		if !t.closing {
			stack = append(stack, t.name)
			continue
		}
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i] == t.name {
				stack = stack[:i]
				break
			}
		}
	}
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1]
}

// linkedTagRanges returns the name range of the tag at p and the name range of its partner.
func (v *EditorView) linkedTagRanges(p buffer.Pos) (self, other buffer.Range, ok bool) {
	if !tagLangs[v.lang()] {
		return
	}
	const window = 3000
	tags := v.scanTags(p.Line-window, p.Line+window)
	idx := -1
	for i, t := range tags {
		if !p.Less(t.nameAt.Start) && !t.nameAt.End.Less(p) {
			idx = i
			break
		}
	}
	if idx < 0 || tags[idx].self {
		return
	}
	t := tags[idx]
	depth := 0
	if !t.closing {
		for i := idx + 1; i < len(tags); i++ {
			u := tags[i]
			if u.self || u.name != t.name {
				continue
			}
			if !u.closing {
				depth++
			} else if depth == 0 {
				return t.nameAt, u.nameAt, true
			} else {
				depth--
			}
		}
		return
	}
	for i := idx - 1; i >= 0; i-- {
		u := tags[i]
		if u.self || u.name != t.name {
			continue
		}
		if u.closing {
			depth++
		} else if depth == 0 {
			return t.nameAt, u.nameAt, true
		} else {
			depth--
		}
	}
	return
}

// withLinkedEditing mirrors an edit inside a tag name to the matching tag when editor.linkedEditing is on.
// op is "type", "deleteLeft" or "deleteRight".
func (v *EditorView) withLinkedEditing(op, text string, fn func()) {
	if len(v.sels) != 1 || !tagLangs[v.lang()] || !v.app.settings.Bool("editor.linkedEditing", v.lang()) {
		fn()
		return
	}
	if op == "type" {
		for _, r := range text {
			if !isTagNameRune(r) {
				fn()
				return
			}
		}
	}
	s := v.sels[0]
	r := s.Range()
	self, other, ok := v.linkedTagRanges(r.Start)
	if !ok || r.Start.Line != r.End.Line || self.End.Less(r.End) {
		fn()
		return
	}
	switch {
	case op == "deleteLeft" && s.Empty() && r.Start.Col == self.Start.Col,
		op == "deleteRight" && s.Empty() && r.End.Col == self.End.Col:
		fn()
		return
	}
	mirror := func(p buffer.Pos) buffer.Pos {
		return buffer.Pos{Line: other.Start.Line, Col: other.Start.Col + p.Col - self.Start.Col}
	}
	v.SetSelections([]buffer.Selection{s, {Anchor: mirror(s.Anchor), Active: mirror(s.Active)}}, 0)
	fn()
	if v.primary < len(v.sels) {
		v.SetSelections([]buffer.Selection{v.sels[v.primary]}, 0)
	}
}
