package ui

import (
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

// mergeConflict is one `<<<<<<<` ... `>>>>>>>` block. Lines are 0-based.
type mergeConflict struct {
	start, base, sep, end int // base = -1 without a `|||||||` section
}

func (c mergeConflict) currentLines() (int, int) { // [from, to)
	if c.base >= 0 {
		return c.start + 1, c.base
	}
	return c.start + 1, c.sep
}

func (c mergeConflict) incomingLines() (int, int) { return c.sep + 1, c.end }

// mergeConflicts finds conflict blocks in a document (cached per version).
func (d *Document) mergeConflicts() []mergeConflict {
	if d.conflictVersion == d.Buf.Version()+1 {
		return d.conflicts
	}
	d.conflicts = nil
	d.conflictVersion = d.Buf.Version() + 1
	if d.Buf.LineCount() > 200000 {
		return nil
	}
	cur := mergeConflict{start: -1, base: -1, sep: -1}
	d.conflictStarts = map[int]bool{}
	for i := 0; i < d.Buf.LineCount(); i++ {
		l := d.Buf.Line(i)
		if len(l) < 7 || (l[0] != '<' && l[0] != '|' && l[0] != '=' && l[0] != '>') {
			continue
		}
		switch {
		case strings.HasPrefix(string(l[:7]), "<<<<<<<"):
			cur = mergeConflict{start: i, base: -1, sep: -1}
		case cur.start >= 0 && cur.sep < 0 && strings.HasPrefix(string(l[:7]), "|||||||"):
			cur.base = i
		case cur.start >= 0 && cur.sep < 0 && string(l[:7]) == "=======" && strings.TrimSpace(string(l[7:])) == "":
			cur.sep = i
		case cur.start >= 0 && cur.sep >= 0 && strings.HasPrefix(string(l[:7]), ">>>>>>>"):
			cur.end = i
			d.conflicts = append(d.conflicts, cur)
			d.conflictStarts[cur.start] = true
			cur = mergeConflict{start: -1, base: -1, sep: -1}
		}
	}
	return d.conflicts
}

// conflictAt returns the conflict containing line.
func (d *Document) conflictAt(line int) (mergeConflict, bool) {
	for _, c := range d.mergeConflicts() {
		if line >= c.start && line <= c.end {
			return c, true
		}
	}
	return mergeConflict{}, false
}

// conflictLineKey returns the background color key for a line inside a conflict.
func (d *Document) conflictLineKey(line int) string {
	c, ok := d.conflictAt(line)
	if !ok {
		return ""
	}
	switch {
	case line == c.start:
		return "merge.currentHeaderBackground"
	case c.base >= 0 && line == c.base:
		return "merge.commonHeaderBackground"
	case c.base >= 0 && line > c.base && line < c.sep:
		return "merge.commonContentBackground"
	case line < c.sep:
		return "merge.currentContentBackground"
	case line == c.sep:
		return ""
	case line < c.end:
		return "merge.incomingContentBackground"
	}
	return "merge.incomingHeaderBackground"
}

// conflictLenses returns the accept actions shown above a conflict's first line.
func (v *EditorView) conflictLenses(line int) []*codeLens {
	if !v.doc.conflictStarts[line] || !v.app.settings.Bool("merge-conflict.codeLens.enabled", "") {
		return nil
	}
	c, _ := v.doc.conflictAt(line)
	mk := func(title, which string) *codeLens {
		return &codeLens{line: line, command: &lsp.Command{Title: title}, run: func() { v.acceptConflict(c, which) }}
	}
	return []*codeLens{
		mk("Accept Current Change", "current"),
		mk("Accept Incoming Change", "incoming"),
		mk("Accept Both Changes", "both"),
		{line: line, command: &lsp.Command{Title: "Compare Changes"}, run: func() { v.compareConflict(c) }},
	}
}

func (v *EditorView) linesText(from, to int) string {
	var sb strings.Builder
	for i := from; i < to; i++ {
		sb.WriteString(v.buf().LineString(i))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// conflictEdit returns the edit that resolves c by keeping current, incoming or both.
func (v *EditorView) conflictEdit(c mergeConflict, which string) buffer.Edit {
	cf, ct := c.currentLines()
	inf, int_ := c.incomingLines()
	var keep string
	switch which {
	case "current":
		keep = v.linesText(cf, ct)
	case "incoming":
		keep = v.linesText(inf, int_)
	default:
		keep = v.linesText(cf, ct) + v.linesText(inf, int_)
	}
	end := buffer.Pos{Line: c.end + 1}
	if c.end+1 >= v.buf().LineCount() {
		end = buffer.Pos{Line: c.end, Col: v.buf().LineLen(c.end)}
		keep = strings.TrimSuffix(keep, "\n")
	}
	return buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: c.start}, End: end}, Text: keep}
}

func (v *EditorView) acceptConflict(c mergeConflict, which string) {
	v.buf().CloseUndoGroup()
	v.applyEdits([]buffer.Edit{v.conflictEdit(c, which)}, "", func(res []buffer.Range) []buffer.Selection {
		return []buffer.Selection{buffer.Caret(res[0].Start)}
	})
	v.buf().CloseUndoGroup()
}

// acceptAllConflicts resolves every conflict in the document the same way.
func (v *EditorView) acceptAllConflicts(which string) {
	cs := v.doc.mergeConflicts()
	if len(cs) == 0 {
		v.app.setStatusMessage("No merge conflicts found in this file.")
		return
	}
	edits := make([]buffer.Edit, 0, len(cs))
	for _, c := range cs {
		edits = append(edits, v.conflictEdit(c, which))
	}
	v.buf().CloseUndoGroup()
	v.applyEdits(edits, "", nil)
	v.buf().CloseUndoGroup()
}

// compareConflict shows the two sides of a conflict in a diff.
func (v *EditorView) compareConflict(c mergeConflict) {
	cf, ct := c.currentLines()
	inf, int_ := c.incomingLines()
	v.app.openDiffPage(v.doc.Title()+": Current ↔ Incoming", "", v.linesText(cf, ct), v.linesText(inf, int_))
}

// gotoConflict moves to the next (dir > 0) or previous conflict.
func (v *EditorView) gotoConflict(dir int) {
	cs := v.doc.mergeConflicts()
	if len(cs) == 0 {
		v.app.setStatusMessage("No merge conflicts found in this file.")
		return
	}
	line := v.Cursor().Line
	target := cs[0]
	if dir > 0 {
		for _, c := range cs {
			if c.start > line {
				target = c
				break
			}
		}
	} else {
		target = cs[len(cs)-1]
		for i := len(cs) - 1; i >= 0; i-- {
			if cs[i].start < line {
				target = cs[i]
				break
			}
		}
	}
	v.SetCursor(buffer.Pos{Line: target.start})
	v.ensurePosVisible(v.Cursor(), true)
}
