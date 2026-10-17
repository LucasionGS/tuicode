package ui

import (
	"sort"
	"strings"
	"unicode"

	"tuicode/internal/buffer"
)

var bracketPairs = map[rune]rune{'(': ')', '[': ']', '{': '}'}
var closingBrackets = map[rune]rune{')': '(', ']': '[', '}': '{'}
var quoteChars = map[rune]bool{'"': true, '\'': true, '`': true}

// applyEdits applies one edit per selection (edits[i] belongs to v.sels[i] when
// mapSels is set) and lets after compute the new selections from the inserted ranges.
func (v *EditorView) applyEdits(edits []buffer.Edit, kind string, after func(results []buffer.Range) []buffer.Selection) bool {
	if v.buf().ReadOnly {
		v.app.setStatusMessage("Cannot edit in read-only editor")
		return false
	}
	if len(edits) == 0 {
		return false
	}
	before := append([]buffer.Selection{}, v.sels...)
	primary := v.primary
	v.applying = true
	results := v.buf().ApplyEdits(edits, before, kind)
	v.applying = false
	if after != nil {
		sels := after(results)
		if primary >= len(sels) {
			primary = len(sels) - 1
		}
		v.SetSelections(sels, primary)
	}
	v.buf().SetAfterSelections(v.sels)
	v.lastEditPos = v.Cursor()
	v.hasLastEdit = true
	v.app.recordEditLocation(v)
	v.ensureVisible()
	return true
}

func (v *EditorView) lang() string { return v.doc.Lang.ID }

func (v *EditorView) autoCloseEnabled() bool {
	mode := v.app.settings.String("editor.autoClosingBrackets", v.lang())
	if mode == "never" || mode == "off" {
		return false
	}
	return v.lang() != "plaintext"
}

// Type inserts text at every cursor, handling auto-closing pairs and surround.
func (v *EditorView) Type(text string) {
	b := v.buf()
	rs := []rune(text)
	single := len(rs) == 1
	var ch rune
	if single {
		ch = rs[0]
	}
	auto := v.autoCloseEnabled()

	// overtype a closing char that is already there
	if single && auto && !v.HasSelection() {
		allOver := true
		for _, s := range v.sels {
			next := b.RuneAt(s.Active)
			_, isClose := closingBrackets[ch]
			if next != ch || (!isClose && !quoteChars[ch]) || !v.app.isAutoClosed(v, s.Active) {
				allOver = false
				break
			}
		}
		if allOver {
			v.MoveRight(false, false)
			b.CloseUndoGroup()
			return
		}
	}

	edits := make([]buffer.Edit, len(v.sels))
	cursorOffset := make([]int, len(v.sels)) // where to put the caret relative to the inserted text start (-1 = end)
	selectInner := make([]bool, len(v.sels))
	autoClosed := make([]bool, len(v.sels))
	for i, s := range v.sels {
		r := s.Range()
		edits[i] = buffer.Edit{Range: r, Text: text}
		cursorOffset[i] = -1
		if !single || !auto {
			continue
		}
		closeCh, isOpen := bracketPairs[ch]
		isQuote := quoteChars[ch] && v.lang() != "markdown"
		if !s.Empty() && (isOpen || isQuote) && v.app.settings.String("editor.autoSurround", v.lang()) != "never" {
			if isQuote {
				closeCh = ch
			}
			inner := b.TextRange(r)
			edits[i].Text = string(ch) + inner + string(closeCh)
			selectInner[i] = true
			continue
		}
		if !s.Empty() {
			continue
		}
		next := b.RuneAt(r.Start)
		var prev rune
		if r.Start.Col > 0 {
			prev = b.Line(r.Start.Line)[r.Start.Col-1]
		}
		nextOK := next == 0 || next == '\n' || next == ' ' || next == '\t' || strings.ContainsRune(")]};,:>", next)
		if isOpen && nextOK {
			edits[i].Text = string(ch) + string(closeCh)
			cursorOffset[i] = 1
			autoClosed[i] = true
		} else if isQuote && nextOK && !isWordRune(prev) && prev != ch && !v.inStringOrComment(r.Start) {
			if ch == '\'' && (v.lang() == "rust" || v.lang() == "plaintext") {
				continue
			}
			edits[i].Text = string(ch) + string(ch)
			cursorOffset[i] = 1
			autoClosed[i] = true
		}
	}
	kind := "type"
	if !single || ch == ' ' {
		kind = "type"
	}
	if single && !isWordRune(ch) {
		b.CloseUndoGroup()
	}
	v.applyEdits(edits, kind, func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			switch {
			case selectInner[i]:
				out[i] = buffer.Selection{Anchor: buffer.Pos{Line: r.Start.Line, Col: r.Start.Col + 1}, Active: buffer.Pos{Line: r.End.Line, Col: r.End.Col - 1}}
			case cursorOffset[i] >= 0:
				out[i] = buffer.Caret(buffer.Pos{Line: r.Start.Line, Col: r.Start.Col + cursorOffset[i]})
			default:
				out[i] = buffer.Caret(r.End)
			}
		}
		return out
	})
	for i, ac := range autoClosed {
		if ac && i < len(v.sels) {
			v.app.markAutoClosed(v, v.sels[i].Active)
		}
	}
	if single {
		if _, isClose := closingBrackets[ch]; isClose {
			v.electricDedent(ch)
		}
	}
}

// electricDedent re-indents a line that only contains a closing bracket.
func (v *EditorView) electricDedent(ch rune) {
	b := v.buf()
	var edits []buffer.Edit
	for _, s := range v.sels {
		p := s.Active
		line := b.Line(p.Line)
		if p.Col == 0 || p.Col > len(line) {
			continue
		}
		before := strings.TrimSpace(string(line[:p.Col-1]))
		if before != "" {
			continue
		}
		open, ok := v.matchBracket(buffer.Pos{Line: p.Line, Col: p.Col - 1})
		if !ok || open.Line == p.Line {
			continue
		}
		want := b.LeadingWhitespace(open.Line)
		have := string(line[:p.Col-1])
		if want != have {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: p.Line, Col: 0}, End: buffer.Pos{Line: p.Line, Col: p.Col - 1}}, Text: want})
		}
	}
	if len(edits) == len(v.sels) && len(edits) > 0 {
		v.applyEdits(edits, "type", func(res []buffer.Range) []buffer.Selection {
			out := make([]buffer.Selection, len(res))
			for i, r := range res {
				out[i] = buffer.Caret(buffer.Pos{Line: r.End.Line, Col: r.End.Col + 1})
			}
			return out
		})
	}
}

// inStringOrComment uses highlight spans to decide whether p is inside a string or comment.
func (v *EditorView) inStringOrComment(p buffer.Pos) bool {
	for _, sp := range v.doc.SpansFor(p.Line) {
		if p.Col > sp.Start && p.Col < sp.End {
			switch sp.Kind {
			case 1, 2, 3: // comment, string, escape
				return true
			}
		}
	}
	return false
}

// Newline inserts a line break with auto-indentation at every cursor.
func (v *EditorView) Newline() {
	b := v.buf()
	unit := b.IndentString()
	edits := make([]buffer.Edit, len(v.sels))
	caret := make([]buffer.Pos, len(v.sels))
	for i, s := range v.sels {
		r := s.Range()
		line := b.Line(r.Start.Line)
		indent := b.LeadingWhitespace(r.Start.Line)
		if r.Start.Col < len([]rune(indent)) {
			indent = string(line[:r.Start.Col])
		}
		beforeText := strings.TrimRight(string(line[:r.Start.Col]), " \t")
		afterLine := b.Line(r.End.Line)
		afterText := ""
		if r.End.Col <= len(afterLine) {
			afterText = string(afterLine[r.End.Col:])
		}
		trimmedAfter := strings.TrimLeft(afterText, " \t")
		var lastCh rune
		if beforeText != "" {
			br := []rune(beforeText)
			lastCh = br[len(br)-1]
		}
		extra := ""
		between := false
		if closeCh, ok := bracketPairs[lastCh]; ok {
			extra = unit
			if strings.HasPrefix(trimmedAfter, string(closeCh)) {
				between = true
			}
		} else if lastCh == ':' && v.lang() == "python" {
			extra = unit
		} else if lastCh == '>' && strings.HasSuffix(beforeText, ">") && !strings.HasSuffix(beforeText, "/>") && (v.lang() == "html" || v.lang() == "xml" || v.lang() == "vue" || v.lang() == "svelte") && strings.HasPrefix(trimmedAfter, "</") {
			extra = unit
			between = true
		}
		// doc comment continuation
		trimmedLine := strings.TrimSpace(beforeText)
		commentCont := ""
		if strings.HasPrefix(trimmedLine, "/**") && !strings.Contains(trimmedLine, "*/") {
			commentCont = " * "
			if !strings.HasPrefix(strings.TrimSpace(afterText), "*/") && !v.hasCommentCloseBelow(r.Start.Line) {
				between = true
				extra = ""
			}
		} else if strings.HasPrefix(trimmedLine, "*") && !strings.HasPrefix(trimmedLine, "*/") && v.inBlockComment(r.Start.Line) {
			commentCont = "* "
		}
		// remove whitespace-only line content when breaking (trimAutoWhitespace)
		start := r.Start
		if strings.TrimSpace(string(line[:r.Start.Col])) == "" && v.app.settings.Bool("editor.trimAutoWhitespace", "") && r.Start.Col > 0 && strings.TrimSpace(afterText) == "" {
			start = buffer.Pos{Line: r.Start.Line, Col: 0}
		}
		end := r.End
		// drop leading whitespace of the text moved to the new line
		end.Col += len([]rune(afterText)) - len([]rune(trimmedAfter))
		var text string
		var cp buffer.Pos
		switch {
		case commentCont != "" && between:
			text = "\n" + indent + commentCont + "\n" + indent + " */"
			cp = buffer.Pos{Line: start.Line + 1, Col: len([]rune(indent + commentCont))}
		case commentCont != "":
			text = "\n" + indent + commentCont
			cp = buffer.Pos{Line: start.Line + 1, Col: len([]rune(indent + commentCont))}
		case between:
			text = "\n" + indent + extra + "\n" + indent
			cp = buffer.Pos{Line: start.Line + 1, Col: len([]rune(indent + extra))}
		default:
			text = "\n" + indent + extra
			cp = buffer.Pos{Line: start.Line + 1, Col: len([]rune(indent + extra))}
		}
		edits[i] = buffer.Edit{Range: buffer.Range{Start: start, End: end}, Text: text}
		caret[i] = cp
	}
	b.CloseUndoGroup()
	v.applyEdits(edits, "newline", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			// caret offsets are relative to the edit start line
			delta := r.Start.Line - edits[i].Range.Start.Line
			out[i] = buffer.Caret(buffer.Pos{Line: caret[i].Line + delta, Col: caret[i].Col})
		}
		return out
	})
	b.CloseUndoGroup()
}

func (v *EditorView) hasCommentCloseBelow(line int) bool {
	b := v.buf()
	for i := line + 1; i < b.LineCount() && i < line+200; i++ {
		t := strings.TrimSpace(b.LineString(i))
		if strings.HasPrefix(t, "*/") || strings.HasSuffix(t, "*/") {
			return true
		}
		if !strings.HasPrefix(t, "*") {
			return false
		}
	}
	return false
}

func (v *EditorView) inBlockComment(line int) bool {
	b := v.buf()
	for i := line; i >= 0 && i > line-500; i-- {
		t := strings.TrimSpace(b.LineString(i))
		if strings.HasPrefix(t, "/*") {
			return true
		}
		if strings.Contains(t, "*/") && i != line {
			return false
		}
		if !strings.HasPrefix(t, "*") && i != line {
			return false
		}
	}
	return false
}

// Backspace deletes left of each cursor.
func (v *EditorView) Backspace(word bool) {
	b := v.buf()
	edits := make([]buffer.Edit, 0, len(v.sels))
	for _, s := range v.sels {
		r := s.Range()
		if !r.Empty() {
			edits = append(edits, buffer.Edit{Range: r})
			continue
		}
		p := r.Start
		if p.Col == 0 && p.Line == 0 {
			edits = append(edits, buffer.Edit{Range: r})
			continue
		}
		if word {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: v.wordLeft(p), End: p}})
			continue
		}
		if p.Col == 0 {
			prev := v.prevVisibleLine(p.Line)
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: prev, Col: b.LineLen(prev)}, End: p}})
			continue
		}
		line := b.Line(p.Line)
		prev := line[p.Col-1]
		// delete an auto-closed pair
		if close, ok := bracketPairs[prev]; ok && p.Col < len(line) && line[p.Col] == close && v.autoCloseEnabled() {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: p.Line, Col: p.Col - 1}, End: buffer.Pos{Line: p.Line, Col: p.Col + 1}}})
			continue
		}
		if quoteChars[prev] && p.Col < len(line) && line[p.Col] == prev && v.autoCloseEnabled() && v.app.isAutoClosed(v, p) {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: p.Line, Col: p.Col - 1}, End: buffer.Pos{Line: p.Line, Col: p.Col + 1}}})
			continue
		}
		// delete indentation to the previous tab stop
		if b.InsertSpaces && prev == ' ' && strings.TrimLeft(string(line[:p.Col]), " ") == "" {
			ts := v.tabSize()
			n := p.Col % ts
			if n == 0 {
				n = ts
			}
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: p.Line, Col: p.Col - n}, End: p}})
			continue
		}
		edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: p.Line, Col: p.Col - 1}, End: p}})
	}
	edits = mergeOverlapping(edits)
	v.applyDeletes(edits)
}

// DeleteForward deletes right of each cursor.
func (v *EditorView) DeleteForward(word bool) {
	b := v.buf()
	edits := make([]buffer.Edit, 0, len(v.sels))
	for _, s := range v.sels {
		r := s.Range()
		if !r.Empty() {
			edits = append(edits, buffer.Edit{Range: r})
			continue
		}
		p := r.Start
		var end buffer.Pos
		if word {
			end = v.wordEndForDelete(p)
		} else if p.Col < b.LineLen(p.Line) {
			end = buffer.Pos{Line: p.Line, Col: p.Col + 1}
		} else if p.Line < b.LineCount()-1 {
			end = buffer.Pos{Line: v.nextVisibleLine(p.Line), Col: 0}
			if end.Line >= b.LineCount() {
				end = b.EndPos()
			}
		} else {
			end = p
		}
		edits = append(edits, buffer.Edit{Range: buffer.Range{Start: p, End: end}})
	}
	edits = mergeOverlapping(edits)
	v.applyDeletes(edits)
}

func (v *EditorView) applyDeletes(edits []buffer.Edit) {
	empty := true
	for _, e := range edits {
		if !e.Range.Empty() {
			empty = false
		}
	}
	if empty {
		return
	}
	v.applyEdits(edits, "delete", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			out[i] = buffer.Caret(r.Start)
		}
		return out
	})
}

func mergeOverlapping(edits []buffer.Edit) []buffer.Edit {
	if len(edits) < 2 {
		return edits
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].Range.Start.Less(edits[j].Range.Start) })
	out := []buffer.Edit{edits[0]}
	for _, e := range edits[1:] {
		last := &out[len(out)-1]
		if e.Range.Start.Less(last.Range.End) {
			if last.Range.End.Less(e.Range.End) {
				last.Range.End = e.Range.End
			}
			continue
		}
		out = append(out, e)
	}
	return out
}

// Tab indents or inserts an indentation unit.
func (v *EditorView) Tab() {
	b := v.buf()
	for _, s := range v.sels {
		r := s.Range()
		if r.Start.Line != r.End.Line {
			v.IndentLines()
			return
		}
	}
	edits := make([]buffer.Edit, len(v.sels))
	for i, s := range v.sels {
		r := s.Range()
		text := "\t"
		if b.InsertSpaces {
			x := v.visualX(r.Start.Line, r.Start.Col)
			ts := v.tabSize()
			text = strings.Repeat(" ", ts-x%ts)
		}
		edits[i] = buffer.Edit{Range: r, Text: text}
	}
	v.applyEdits(edits, "type", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			out[i] = buffer.Caret(r.End)
		}
		return out
	})
}

// selectedLines returns sorted unique line numbers touched by selections.
func (v *EditorView) selectedLines() []int {
	seen := map[int]bool{}
	var lines []int
	for _, s := range v.sels {
		r := s.Range()
		end := r.End.Line
		if r.End.Col == 0 && r.End.Line > r.Start.Line {
			end--
		}
		for l := r.Start.Line; l <= end; l++ {
			if !seen[l] {
				seen[l] = true
				lines = append(lines, l)
			}
		}
	}
	sort.Ints(lines)
	return lines
}

// lineBlocks groups selected lines into contiguous blocks.
func (v *EditorView) lineBlocks() [][2]int {
	lines := v.selectedLines()
	var blocks [][2]int
	for _, l := range lines {
		if n := len(blocks); n > 0 && blocks[n-1][1] >= l-1 {
			blocks[n-1][1] = l
			continue
		}
		blocks = append(blocks, [2]int{l, l})
	}
	return blocks
}

// editLinesKeepSelections applies line-local edits and shifts selections on edited lines.
func (v *EditorView) editLinesKeepSelections(edits []buffer.Edit) {
	if len(edits) == 0 {
		return
	}
	// remember selections and let the buffer listener transform them
	sels := append([]buffer.Selection{}, v.sels...)
	before := append([]buffer.Selection{}, v.sels...)
	if v.buf().ReadOnly {
		return
	}
	res := v.buf().ApplyEdits(edits, before, "indent")
	_ = res
	// v.applying was false, so the listener already transformed v.sels
	_ = sels
	v.normalize()
	v.buf().SetAfterSelections(v.sels)
	v.ensureVisible()
}

// IndentLines indents all selected lines.
func (v *EditorView) IndentLines() {
	b := v.buf()
	unit := b.IndentString()
	var edits []buffer.Edit
	for _, l := range v.selectedLines() {
		if b.LineLen(l) == 0 && len(v.selectedLines()) > 1 {
			continue
		}
		edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l}, End: buffer.Pos{Line: l}}, Text: unit})
	}
	// selections starting at column 0 should not grow to include the indent
	starts := make([]buffer.Selection, len(v.sels))
	copy(starts, v.sels)
	v.editLinesKeepSelections(edits)
	for i := range v.sels {
		if i < len(starts) {
			s := starts[i]
			if !s.Empty() {
				r := s.Range()
				if r.Start.Col == 0 {
					if s.Reversed() {
						v.sels[i].Active.Col = 0
					} else {
						v.sels[i].Anchor.Col = 0
					}
				}
			}
		}
	}
}

// OutdentLines removes one indentation level from selected lines.
func (v *EditorView) OutdentLines() {
	b := v.buf()
	ts := v.tabSize()
	var edits []buffer.Edit
	for _, l := range v.selectedLines() {
		line := b.Line(l)
		n := 0
		if len(line) > 0 && line[0] == '\t' {
			n = 1
		} else {
			for n < len(line) && n < ts && line[n] == ' ' {
				n++
			}
		}
		if n > 0 {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l}, End: buffer.Pos{Line: l, Col: n}}})
		}
	}
	v.editLinesKeepSelections(edits)
}

// ToggleLineComment comments or uncomments selected lines.
func (v *EditorView) ToggleLineComment(mode int) {
	b := v.buf()
	tok := v.doc.Lang.LineComment
	if tok == "" {
		if v.doc.Lang.BlockComment[0] != "" {
			v.toggleBlockCommentLines()
		}
		return
	}
	lines := v.selectedLines()
	allCommented := true
	minIndent := -1
	for _, l := range lines {
		t := strings.TrimLeft(b.LineString(l), " \t")
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, tok) {
			allCommented = false
		}
		ind := len([]rune(b.LineString(l))) - len([]rune(t))
		if minIndent < 0 || ind < minIndent {
			minIndent = ind
		}
	}
	if minIndent < 0 {
		minIndent = 0
		allCommented = false
	}
	remove := allCommented
	if mode == 1 {
		remove = false
	} else if mode == 2 {
		remove = true
	}
	var edits []buffer.Edit
	for _, l := range lines {
		s := b.LineString(l)
		t := strings.TrimLeft(s, " \t")
		if t == "" && len(lines) > 1 {
			continue
		}
		ind := len([]rune(s)) - len([]rune(t))
		if remove {
			if !strings.HasPrefix(t, tok) {
				continue
			}
			n := len([]rune(tok))
			if strings.HasPrefix(t[len(tok):], " ") {
				n++
			}
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l, Col: ind}, End: buffer.Pos{Line: l, Col: ind + n}}})
		} else {
			col := minIndent
			if t == "" {
				col = len([]rune(s))
			}
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l, Col: col}, End: buffer.Pos{Line: l, Col: col}}, Text: tok + " "})
		}
	}
	v.editLinesKeepSelections(edits)
}

func (v *EditorView) toggleBlockCommentLines() {
	b := v.buf()
	open, close := v.doc.Lang.BlockComment[0], v.doc.Lang.BlockComment[1]
	lines := v.selectedLines()
	if len(lines) == 0 {
		return
	}
	first, last := lines[0], lines[len(lines)-1]
	ft := strings.TrimSpace(b.LineString(first))
	lt := strings.TrimSpace(b.LineString(last))
	if strings.HasPrefix(ft, open) && strings.HasSuffix(lt, close) {
		fs := b.LineString(first)
		fi := strings.Index(fs, open)
		ls := b.LineString(last)
		li := strings.LastIndex(ls, close)
		startCol := len([]rune(fs[:fi]))
		endCol := len([]rune(ls[:li]))
		openLen := len([]rune(open))
		if strings.HasPrefix(fs[fi+len(open):], " ") {
			openLen++
		}
		closeStart := endCol
		if endCol > 0 && strings.HasSuffix(ls[:li], " ") {
			closeStart--
		}
		edits := []buffer.Edit{
			{Range: buffer.Range{Start: buffer.Pos{Line: last, Col: closeStart}, End: buffer.Pos{Line: last, Col: endCol + len([]rune(close))}}},
			{Range: buffer.Range{Start: buffer.Pos{Line: first, Col: startCol}, End: buffer.Pos{Line: first, Col: startCol + openLen}}},
		}
		if first == last && closeStart < startCol+openLen {
			return
		}
		v.editLinesKeepSelections(edits)
		return
	}
	ind := b.FirstNonWhitespace(first)
	edits := []buffer.Edit{
		{Range: buffer.Range{Start: buffer.Pos{Line: last, Col: b.LineLen(last)}, End: buffer.Pos{Line: last, Col: b.LineLen(last)}}, Text: " " + close},
		{Range: buffer.Range{Start: buffer.Pos{Line: first, Col: ind}, End: buffer.Pos{Line: first, Col: ind}}, Text: open + " "},
	}
	v.editLinesKeepSelections(edits)
}

// ToggleBlockComment wraps selections in block comment markers.
func (v *EditorView) ToggleBlockComment() {
	b := v.buf()
	open, close := v.doc.Lang.BlockComment[0], v.doc.Lang.BlockComment[1]
	if open == "" {
		v.ToggleLineComment(0)
		return
	}
	if !v.HasSelection() {
		v.toggleBlockCommentLines()
		return
	}
	edits := make([]buffer.Edit, len(v.sels))
	unwrap := make([]bool, len(v.sels))
	for i, s := range v.sels {
		r := s.Range()
		text := b.TextRange(r)
		t := strings.TrimSpace(text)
		if strings.HasPrefix(t, open) && strings.HasSuffix(t, close) && len(t) >= len(open)+len(close) {
			inner := strings.TrimSuffix(strings.TrimPrefix(t, open), close)
			inner = strings.TrimPrefix(strings.TrimSuffix(inner, " "), " ")
			edits[i] = buffer.Edit{Range: r, Text: inner}
			unwrap[i] = true
		} else {
			edits[i] = buffer.Edit{Range: r, Text: open + " " + text + " " + close}
		}
	}
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			out[i] = buffer.Selection{Anchor: r.Start, Active: r.End}
		}
		return out
	})
}

// MoveLines moves selected lines up (dir<0) or down.
func (v *EditorView) MoveLines(dir int) {
	b := v.buf()
	blocks := v.lineBlocks()
	if len(blocks) == 0 {
		return
	}
	// merge blocks that are adjacent after the move
	var merged [][2]int
	for _, bl := range blocks {
		if n := len(merged); n > 0 && merged[n-1][1] >= bl[0]-2 {
			merged[n-1][1] = bl[1]
			continue
		}
		merged = append(merged, bl)
	}
	var edits []buffer.Edit
	for _, bl := range merged {
		s, e := bl[0], bl[1]
		if dir < 0 {
			if s == 0 {
				return
			}
			block := linesText(b, s, e)
			prev := b.LineString(s - 1)
			edits = append(edits, buffer.Edit{
				Range: buffer.Range{Start: buffer.Pos{Line: s - 1}, End: buffer.Pos{Line: e, Col: b.LineLen(e)}},
				Text:  block + "\n" + prev,
			})
		} else {
			if e >= b.LineCount()-1 {
				return
			}
			block := linesText(b, s, e)
			next := b.LineString(e + 1)
			edits = append(edits, buffer.Edit{
				Range: buffer.Range{Start: buffer.Pos{Line: s}, End: buffer.Pos{Line: e + 1, Col: b.LineLen(e + 1)}},
				Text:  next + "\n" + block,
			})
		}
	}
	old := append([]buffer.Selection{}, v.sels...)
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(old))
		for i, s := range old {
			s.Anchor.Line += dir
			s.Active.Line += dir
			out[i] = s
		}
		return out
	})
}

func linesText(b *buffer.Buffer, s, e int) string {
	parts := make([]string, 0, e-s+1)
	for l := s; l <= e; l++ {
		parts = append(parts, b.LineString(l))
	}
	return strings.Join(parts, "\n")
}

// CopyLines duplicates selected lines up or down.
func (v *EditorView) CopyLines(dir int) {
	b := v.buf()
	blocks := v.lineBlocks()
	var edits []buffer.Edit
	for _, bl := range blocks {
		s, e := bl[0], bl[1]
		block := linesText(b, s, e)
		edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: e, Col: b.LineLen(e)}, End: buffer.Pos{Line: e, Col: b.LineLen(e)}}, Text: "\n" + block})
	}
	old := append([]buffer.Selection{}, v.sels...)
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(old))
		shift := 0
		bi := 0
		for i, s := range old {
			// each selection moves by the size of its own block plus all blocks above
			for bi < len(blocks) && blocks[bi][1] < s.Range().Start.Line {
				shift += blocks[bi][1] - blocks[bi][0] + 1
				bi++
			}
			own := 0
			if dir > 0 && bi < len(blocks) {
				own = blocks[bi][1] - blocks[bi][0] + 1
			}
			s.Anchor.Line += shift + own
			s.Active.Line += shift + own
			out[i] = s
		}
		return out
	})
}

// DeleteLines removes all selected lines.
func (v *EditorView) DeleteLines() {
	b := v.buf()
	blocks := v.lineBlocks()
	var edits []buffer.Edit
	for _, bl := range blocks {
		s, e := bl[0], bl[1]
		if e < b.LineCount()-1 {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: s}, End: buffer.Pos{Line: e + 1}}})
		} else if s > 0 {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: s - 1, Col: b.LineLen(s - 1)}, End: buffer.Pos{Line: e, Col: b.LineLen(e)}}})
		} else {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{}, End: b.EndPos()}})
		}
	}
	px := v.visualXInRow(v.Cursor().Line, v.Cursor().Col)
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			line := r.Start.Line
			if r.Start.Col > 0 {
				line++
			}
			if line >= b.LineCount() {
				line = b.LineCount() - 1
			}
			out[i] = buffer.Caret(buffer.Pos{Line: line, Col: v.colAtVisualX(line, 0, b.LineLen(line), px, 0)})
		}
		return out
	})
}

// InsertLine opens a new line below (or above) each cursor.
func (v *EditorView) InsertLine(above bool) {
	b := v.buf()
	edits := make([]buffer.Edit, len(v.sels))
	indents := make([]string, len(v.sels))
	for i, s := range v.sels {
		l := s.Active.Line
		indent := b.LeadingWhitespace(l)
		if !above {
			t := strings.TrimRight(b.LineString(l), " \t")
			if t != "" {
				if _, ok := bracketPairs[[]rune(t)[len([]rune(t))-1]]; ok {
					indent += b.IndentString()
				}
			}
		}
		indents[i] = indent
		if above {
			edits[i] = buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l}, End: buffer.Pos{Line: l}}, Text: indent + "\n"}
		} else {
			end := buffer.Pos{Line: l, Col: b.LineLen(l)}
			edits[i] = buffer.Edit{Range: buffer.Range{Start: end, End: end}, Text: "\n" + indent}
		}
	}
	b.CloseUndoGroup()
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			if above {
				out[i] = buffer.Caret(buffer.Pos{Line: r.Start.Line, Col: len([]rune(indents[i]))})
			} else {
				out[i] = buffer.Caret(r.End)
			}
		}
		return out
	})
}

// replaceSelectionsWith transforms each selection's text (or whole line when empty and lineWise).
func (v *EditorView) transformSelections(fn func(string) string) {
	b := v.buf()
	edits := make([]buffer.Edit, len(v.sels))
	for i, s := range v.sels {
		r := s.Range()
		if r.Empty() {
			wr, ok := v.WordAt(r.Start)
			if !ok {
				edits[i] = buffer.Edit{Range: r}
				continue
			}
			r = wr
		}
		edits[i] = buffer.Edit{Range: r, Text: fn(b.TextRange(r))}
	}
	old := append([]buffer.Selection{}, v.sels...)
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			if old[i].Empty() {
				out[i] = old[i]
			} else {
				out[i] = buffer.Selection{Anchor: r.Start, Active: r.End}
			}
		}
		return out
	})
}

// transformLines replaces the full selected line range using fn.
func (v *EditorView) transformLines(fn func([]string) []string) {
	b := v.buf()
	lines := v.selectedLines()
	if len(lines) <= 1 {
		lines = make([]int, b.LineCount())
		for i := range lines {
			lines[i] = i
		}
	}
	s, e := lines[0], lines[len(lines)-1]
	var in []string
	for l := s; l <= e; l++ {
		in = append(in, b.LineString(l))
	}
	out := fn(in)
	v.applyEdits([]buffer.Edit{{Range: buffer.Range{Start: buffer.Pos{Line: s}, End: buffer.Pos{Line: e, Col: b.LineLen(e)}}, Text: strings.Join(out, "\n")}}, "", func(res []buffer.Range) []buffer.Selection {
		return []buffer.Selection{{Anchor: res[0].Start, Active: res[0].End}}
	})
}

// TrimTrailingWhitespace removes trailing whitespace on every line.
func (v *EditorView) TrimTrailingWhitespace() {
	b := v.buf()
	var edits []buffer.Edit
	for l := 0; l < b.LineCount(); l++ {
		line := b.Line(l)
		n := len(line)
		for n > 0 && (line[n-1] == ' ' || line[n-1] == '\t') {
			n--
		}
		if n < len(line) {
			// keep whitespace at cursors
			skip := false
			for _, s := range v.sels {
				if s.Active.Line == l && s.Active.Col > n {
					skip = true
				}
			}
			if !skip {
				edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l, Col: n}, End: buffer.Pos{Line: l, Col: len(line)}}})
			}
		}
	}
	v.editLinesKeepSelections(edits)
}

// JoinLines joins each selected line with the next.
func (v *EditorView) JoinLines() {
	b := v.buf()
	var edits []buffer.Edit
	for _, bl := range v.lineBlocks() {
		s, e := bl[0], bl[1]
		if s == e {
			e = s + 1
		}
		if e >= b.LineCount() {
			continue
		}
		for l := s; l < e; l++ {
			end := buffer.Pos{Line: l + 1, Col: b.FirstNonWhitespace(l + 1)}
			line := b.Line(l)
			n := len(line)
			for n > 0 && (line[n-1] == ' ' || line[n-1] == '\t') {
				n--
			}
			sep := " "
			if end.Col == b.LineLen(l+1) {
				sep = ""
			}
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l, Col: n}, End: end}, Text: sep})
		}
	}
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		return []buffer.Selection{buffer.Caret(res[len(res)-1].Start)}
	})
}

// Transpose swaps the characters around the cursor.
func (v *EditorView) Transpose() {
	b := v.buf()
	var edits []buffer.Edit
	for _, s := range v.sels {
		p := s.Active
		line := b.Line(p.Line)
		if len(line) < 2 {
			continue
		}
		c := p.Col
		if c >= len(line) {
			c = len(line) - 1
		}
		if c == 0 {
			c = 1
		}
		edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: p.Line, Col: c - 1}, End: buffer.Pos{Line: p.Line, Col: c + 1}}, Text: string([]rune{line[c], line[c-1]})})
	}
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			out[i] = buffer.Caret(r.End)
		}
		return out
	})
}

//#region Selection commands

// SelectAll selects the whole document.
func (v *EditorView) SelectAll() {
	v.pushCursorHistory()
	v.SetSelections([]buffer.Selection{{Anchor: buffer.Pos{}, Active: v.buf().EndPos()}}, 0)
}

// ExpandLineSelection selects whole lines, extending on repeat.
func (v *EditorView) ExpandLineSelection() {
	b := v.buf()
	v.pushCursorHistory()
	for i, s := range v.sels {
		r := s.Range()
		start := buffer.Pos{Line: r.Start.Line}
		// select through the next line start; a selection already ending at a line start grows by a line
		endLine := r.End.Line + 1
		if !s.Empty() && r.End.Col == 0 && r.Start.Col != 0 {
			endLine = r.End.Line
		}
		var end buffer.Pos
		if endLine >= b.LineCount() {
			end = b.EndPos()
		} else {
			end = buffer.Pos{Line: endLine}
		}
		v.sels[i] = buffer.Selection{Anchor: start, Active: end}
	}
	v.normalize()
	v.ensureVisible()
}

// searchText returns the text that Ctrl+D searches for, selecting the word if needed.
func (v *EditorView) AddNextOccurrence(moveLast bool) {
	b := v.buf()
	p := v.Primary()
	if p.Empty() {
		v.pushCursorHistory()
		for i, s := range v.sels {
			if s.Empty() {
				if wr, ok := v.WordAt(s.Active); ok {
					v.sels[i] = buffer.Selection{Anchor: wr.Start, Active: wr.End}
				}
			}
		}
		v.normalize()
		v.app.wholeWordNext = true
		return
	}
	text := b.TextRange(p.Range())
	if text == "" {
		return
	}
	// search after the last selection that contains this text
	last := v.sels[len(v.sels)-1].Range().End
	if v.primary < len(v.sels) {
		last = v.Primary().Range().End
	}
	r, ok := v.findNext(text, last, v.app.wholeWordNext, true)
	if !ok {
		return
	}
	for _, s := range v.sels {
		if s.Range() == r {
			// all occurrences already selected: just cycle the primary
			return
		}
	}
	v.pushCursorHistory()
	ns := buffer.Selection{Anchor: r.Start, Active: r.End}
	if moveLast {
		v.sels[v.primary] = ns
	} else {
		v.sels = append(v.sels, ns)
		v.prefX = append(v.prefX, -1)
		v.primary = len(v.sels) - 1
	}
	v.normalize()
	for i, s := range v.sels {
		if s.Range() == r {
			v.primary = i
		}
	}
	v.ensurePosVisible(r.End, false)
}

// findNext finds text after from (wrapping).
func (v *EditorView) findNext(text string, from buffer.Pos, wholeWord, caseSensitive bool) (buffer.Range, bool) {
	matches := v.findAll(text, wholeWord, caseSensitive)
	if len(matches) == 0 {
		return buffer.Range{}, false
	}
	for _, m := range matches {
		if !m.Start.Less(from) {
			return m, true
		}
	}
	return matches[0], true
}

func (v *EditorView) findAll(text string, wholeWord, caseSensitive bool) []buffer.Range {
	b := v.buf()
	var out []buffer.Range
	if strings.Contains(text, "\n") {
		full := b.Text()
		hay, needle := full, text
		if !caseSensitive {
			hay, needle = strings.ToLower(full), strings.ToLower(text)
		}
		off := 0
		for {
			i := strings.Index(hay[off:], needle)
			if i < 0 {
				break
			}
			s := b.OffsetToPos(full, off+i)
			e := b.OffsetToPos(full, off+i+len(needle))
			out = append(out, buffer.Range{Start: s, End: e})
			off += i + max(len(needle), 1)
		}
		return out
	}
	needle := []rune(text)
	if !caseSensitive {
		needle = []rune(strings.ToLower(text))
	}
	for l := 0; l < b.LineCount(); l++ {
		line := b.Line(l)
		hay := line
		if !caseSensitive {
			hay = []rune(strings.ToLower(string(line)))
			if len(hay) != len(line) {
				hay = line
			}
		}
		for c := 0; c+len(needle) <= len(hay); c++ {
			if !runesEqualAt(hay, c, needle) {
				continue
			}
			if wholeWord {
				if c > 0 && isWordRune(hay[c-1]) || c+len(needle) < len(hay) && isWordRune(hay[c+len(needle)]) {
					continue
				}
			}
			out = append(out, buffer.Range{Start: buffer.Pos{Line: l, Col: c}, End: buffer.Pos{Line: l, Col: c + len(needle)}})
			c += len(needle) - 1
		}
	}
	return out
}

func runesEqualAt(hay []rune, at int, needle []rune) bool {
	for i, r := range needle {
		if hay[at+i] != r {
			return false
		}
	}
	return true
}

// SelectAllOccurrences selects every occurrence of the current selection or word.
func (v *EditorView) SelectAllOccurrences() {
	p := v.Primary()
	whole := false
	r := p.Range()
	if p.Empty() {
		wr, ok := v.WordAt(p.Active)
		if !ok {
			return
		}
		r = wr
		whole = true
	}
	text := v.buf().TextRange(r)
	matches := v.findAll(text, whole, true)
	if len(matches) == 0 {
		return
	}
	v.pushCursorHistory()
	sels := make([]buffer.Selection, len(matches))
	prim := 0
	for i, m := range matches {
		sels[i] = buffer.Selection{Anchor: m.Start, Active: m.End}
		if m == r {
			prim = i
		}
	}
	v.SetSelections(sels, prim)
}

// AddCursorVertical adds a cursor on the line above (dir<0) or below.
func (v *EditorView) AddCursorVertical(dir int) {
	v.pushCursorHistory()
	ref := v.sels[0]
	if dir > 0 {
		ref = v.sels[len(v.sels)-1]
	}
	p := ref.Active
	row := v.rowOfPos(p)
	var next vpos
	var ok bool
	if dir < 0 {
		next, ok = v.prevRow(row)
	} else {
		next, ok = v.nextRow(row)
	}
	if !ok {
		return
	}
	x := v.visualXInRow(p.Line, p.Col)
	np := v.posAtRowX(next, x)
	v.sels = append(v.sels, buffer.Caret(np))
	v.prefX = append(v.prefX, x)
	v.primary = len(v.sels) - 1
	px := v.prefX
	v.normalize()
	_ = px
	for i, s := range v.sels {
		if s.Active == np {
			v.primary = i
		}
	}
	v.ensurePosVisible(np, false)
}

// CursorsAtLineEnds puts a cursor at the end of every selected line.
func (v *EditorView) CursorsAtLineEnds() {
	b := v.buf()
	v.pushCursorHistory()
	var sels []buffer.Selection
	for _, s := range v.sels {
		r := s.Range()
		for l := r.Start.Line; l <= r.End.Line; l++ {
			if l == r.End.Line && r.End.Col == 0 && l > r.Start.Line {
				continue
			}
			sels = append(sels, buffer.Caret(buffer.Pos{Line: l, Col: b.LineLen(l)}))
		}
	}
	v.SetSelections(sels, -1)
}

// CursorUndo restores the previous cursor state.
func (v *EditorView) CursorUndo() {
	if len(v.cursorHistory) == 0 {
		return
	}
	last := v.cursorHistory[len(v.cursorHistory)-1]
	v.cursorHistory = v.cursorHistory[:len(v.cursorHistory)-1]
	v.cursorFuture = append(v.cursorFuture, append([]buffer.Selection{}, v.sels...))
	v.SetSelections(append([]buffer.Selection{}, last...), -1)
	v.ensureVisible()
}

// CursorRedo re-applies a cursor state undone with CursorUndo.
func (v *EditorView) CursorRedo() {
	if len(v.cursorFuture) == 0 {
		return
	}
	next := v.cursorFuture[len(v.cursorFuture)-1]
	v.cursorFuture = v.cursorFuture[:len(v.cursorFuture)-1]
	v.cursorHistory = append(v.cursorHistory, append([]buffer.Selection{}, v.sels...))
	v.SetSelections(append([]buffer.Selection{}, next...), -1)
	v.ensureVisible()
}

// matchBracket finds the bracket matching the one at p.
func (v *EditorView) matchBracket(p buffer.Pos) (buffer.Pos, bool) {
	b := v.buf()
	r := b.RuneAt(p)
	if close, ok := bracketPairs[r]; ok {
		depth := 0
		limit := 0
		for l := p.Line; l < b.LineCount() && limit < 200000; l++ {
			line := b.Line(l)
			start := 0
			if l == p.Line {
				start = p.Col
			}
			for c := start; c < len(line); c++ {
				limit++
				if line[c] != r && line[c] != close {
					continue
				}
				if v.inStringOrComment(buffer.Pos{Line: l, Col: c}) && !v.inStringOrComment(p) {
					continue
				}
				if line[c] == r {
					depth++
				} else {
					depth--
					if depth == 0 {
						return buffer.Pos{Line: l, Col: c}, true
					}
				}
			}
		}
		return p, false
	}
	if open, ok := closingBrackets[r]; ok {
		depth := 0
		limit := 0
		for l := p.Line; l >= 0 && limit < 200000; l-- {
			line := b.Line(l)
			start := len(line) - 1
			if l == p.Line {
				start = p.Col
			}
			for c := start; c >= 0; c-- {
				limit++
				if line[c] != r && line[c] != open {
					continue
				}
				if v.inStringOrComment(buffer.Pos{Line: l, Col: c}) && !v.inStringOrComment(p) {
					continue
				}
				if line[c] == r {
					depth++
				} else {
					depth--
					if depth == 0 {
						return buffer.Pos{Line: l, Col: c}, true
					}
				}
			}
		}
	}
	return p, false
}

// bracketNearCursor returns the bracket position adjacent to p, if any.
func (v *EditorView) bracketNearCursor(p buffer.Pos) (buffer.Pos, bool) {
	b := v.buf()
	if r := b.RuneAt(p); bracketPairs[r] != 0 || closingBrackets[r] != 0 {
		return p, true
	}
	if p.Col > 0 {
		q := buffer.Pos{Line: p.Line, Col: p.Col - 1}
		if r := b.RuneAt(q); bracketPairs[r] != 0 || closingBrackets[r] != 0 {
			return q, true
		}
	}
	return p, false
}

// JumpToBracket moves to the matching bracket.
func (v *EditorView) JumpToBracket(selectTo bool) {
	p := v.Cursor()
	bp, ok := v.bracketNearCursor(p)
	if !ok {
		// jump to enclosing bracket
		if open, ok2 := v.enclosingBracket(p); ok2 {
			bp = open
		} else {
			return
		}
	}
	m, ok := v.matchBracket(bp)
	if !ok {
		return
	}
	if selectTo {
		s, e := bp, m
		if e.Less(s) {
			s, e = e, s
		}
		v.SetSelections([]buffer.Selection{{Anchor: s, Active: buffer.Pos{Line: e.Line, Col: e.Col + 1}}}, 0)
	} else {
		v.SetCursor(m)
	}
	v.ensureVisible()
}

// enclosingBracket finds the nearest unmatched opening bracket before p.
func (v *EditorView) enclosingBracket(p buffer.Pos) (buffer.Pos, bool) {
	b := v.buf()
	depth := map[rune]int{}
	limit := 0
	for l := p.Line; l >= 0 && limit < 100000; l-- {
		line := b.Line(l)
		start := len(line) - 1
		if l == p.Line {
			start = p.Col - 1
		}
		for c := start; c >= 0; c-- {
			limit++
			r := line[c]
			if _, ok := closingBrackets[r]; ok {
				if !v.inStringOrComment(buffer.Pos{Line: l, Col: c}) {
					depth[closingBrackets[r]]++
				}
			} else if _, ok := bracketPairs[r]; ok {
				if v.inStringOrComment(buffer.Pos{Line: l, Col: c}) {
					continue
				}
				if depth[r] == 0 {
					return buffer.Pos{Line: l, Col: c}, true
				}
				depth[r]--
			}
		}
	}
	return p, false
}

// ExpandSelection grows the selection: word, string, bracket contents, brackets, lines, document.
func (v *EditorView) ExpandSelection() {
	v.pushCursorHistory()
	for i, s := range v.sels {
		r := v.expandRange(s.Range())
		v.sels[i] = buffer.Selection{Anchor: r.Start, Active: r.End}
	}
	v.normalize()
}

func (v *EditorView) expandRange(r buffer.Range) buffer.Range {
	b := v.buf()
	contains := func(outer, inner buffer.Range) bool {
		return outer != inner && !inner.Start.Less(outer.Start) && !outer.End.Less(inner.End)
	}
	if r.Empty() {
		if wr, ok := v.WordAt(r.Start); ok {
			return wr
		}
	}
	if q, ok := v.enclosingQuotes(r); ok && q != r {
		return q
	}
	open, ok := v.enclosingBracket(r.Start)
	for ok {
		closePos, ok2 := v.matchBracket(open)
		if !ok2 {
			break
		}
		inner := buffer.Range{Start: buffer.Pos{Line: open.Line, Col: open.Col + 1}, End: closePos}
		outer := buffer.Range{Start: open, End: buffer.Pos{Line: closePos.Line, Col: closePos.Col + 1}}
		if contains(inner, r) {
			return inner
		}
		if contains(outer, r) {
			return outer
		}
		open, ok = v.enclosingBracket(open)
	}
	lr := buffer.Range{Start: buffer.Pos{Line: r.Start.Line}, End: buffer.Pos{Line: r.End.Line, Col: b.LineLen(r.End.Line)}}
	if lr != r {
		return lr
	}
	return buffer.Range{Start: buffer.Pos{}, End: b.EndPos()}
}

func (v *EditorView) enclosingQuotes(r buffer.Range) (buffer.Range, bool) {
	if r.Start.Line != r.End.Line {
		return r, false
	}
	line := v.buf().Line(r.Start.Line)
	for _, sp := range v.doc.SpansFor(r.Start.Line) {
		if sp.Kind != 2 || sp.End-sp.Start < 2 {
			continue
		}
		if sp.Start <= r.Start.Col && r.End.Col <= sp.End {
			inner := buffer.Range{Start: buffer.Pos{Line: r.Start.Line, Col: sp.Start + 1}, End: buffer.Pos{Line: r.Start.Line, Col: sp.End - 1}}
			if sp.End-1 < len(line) && quoteChars[line[sp.Start]] {
				if inner != r && !r.Start.Less(inner.Start) && !inner.End.Less(r.End) {
					return inner, true
				}
			}
			outer := buffer.Range{Start: buffer.Pos{Line: r.Start.Line, Col: sp.Start}, End: buffer.Pos{Line: r.Start.Line, Col: sp.End}}
			if outer != r {
				return outer, true
			}
		}
	}
	return r, false
}

// ShrinkSelection reverts the last expand.
func (v *EditorView) ShrinkSelection() {
	v.CursorUndo()
}

//#endregion

func toTitle(s string) string {
	rs := []rune(s)
	up := true
	for i, r := range rs {
		if up && unicode.IsLetter(r) {
			rs[i] = unicode.ToUpper(r)
			up = false
		} else {
			rs[i] = unicode.ToLower(r)
		}
		if !isWordRune(r) {
			up = true
		}
	}
	return string(rs)
}

func toSnake(s string) string {
	var sb strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])) {
			sb.WriteRune('_')
		}
		if r == '-' || r == ' ' {
			sb.WriteRune('_')
			continue
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}

func toCamel(s string, pascal bool) string {
	var sb strings.Builder
	up := pascal
	first := true
	for _, r := range s {
		if r == '_' || r == '-' || r == ' ' {
			up = true
			continue
		}
		if up {
			sb.WriteRune(unicode.ToUpper(r))
			up = false
		} else if first && !pascal {
			sb.WriteRune(unicode.ToLower(r))
		} else {
			sb.WriteRune(r)
		}
		first = false
	}
	return sb.String()
}

func toKebab(s string) string { return strings.ReplaceAll(toSnake(s), "_", "-") }
