package ui

import (
	"fmt"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/git"
)

// DirtyDiffPeek shows the original text of a changed region (clicking the gutter change bar).
type DirtyDiffPeek struct {
	v       *EditorView
	hunk    git.Hunk
	removed []string
	added   []string
	index   int
	total   int
}

func (v *EditorView) hunks() []git.Hunk {
	if !v.doc.hasGitBase {
		return nil
	}
	return git.Hunks(git.DiffLines(v.doc.gitBase, v.buf().Lines()))
}

// openDirtyDiff opens the change peek for the hunk touching line.
func (v *EditorView) openDirtyDiff(line int) {
	hs := v.hunks()
	for i, h := range hs {
		end := h.BStart + max(h.BLen, 1)
		if line >= h.BStart && line < end || h.BLen == 0 && (line == h.BStart || line == h.BStart-1) {
			v.showHunk(hs, i)
			return
		}
	}
}

func (v *EditorView) showHunk(hs []git.Hunk, i int) {
	h := hs[i]
	p := &DirtyDiffPeek{v: v, hunk: h, index: i, total: len(hs)}
	for k := 0; k < h.ALen; k++ {
		p.removed = append(p.removed, v.doc.gitBase[h.AStart+k])
	}
	for k := 0; k < h.BLen; k++ {
		p.added = append(p.added, v.buf().LineString(h.BStart+k))
	}
	v.dirtyDiff = p
	v.SetCursor(buffer.Pos{Line: min(h.BStart, v.buf().LineCount()-1)})
	v.ensurePosVisible(v.Cursor(), false)
}

func (p *DirtyDiffPeek) move(d int) {
	hs := p.v.hunks()
	if len(hs) == 0 {
		p.v.dirtyDiff = nil
		return
	}
	i := (p.index + d + len(hs)) % len(hs)
	p.v.showHunk(hs, i)
}

// revert restores the original lines of the hunk.
func (p *DirtyDiffPeek) revert() {
	v := p.v
	b := v.buf()
	h := p.hunk
	text := strings.Join(p.removed, "\n")
	var r buffer.Range
	switch {
	case h.BLen == 0:
		// lines were deleted: insert them back
		if h.BStart < b.LineCount() {
			r = buffer.Range{Start: buffer.Pos{Line: h.BStart}, End: buffer.Pos{Line: h.BStart}}
			text += "\n"
		} else {
			end := b.EndPos()
			r = buffer.Range{Start: end, End: end}
			text = "\n" + text
		}
	case h.ALen == 0:
		// lines were added: remove them
		endLine := h.BStart + h.BLen
		if endLine < b.LineCount() {
			r = buffer.Range{Start: buffer.Pos{Line: h.BStart}, End: buffer.Pos{Line: endLine}}
		} else if h.BStart > 0 {
			r = buffer.Range{Start: buffer.Pos{Line: h.BStart - 1, Col: b.LineLen(h.BStart - 1)}, End: b.EndPos()}
		} else {
			r = buffer.Range{Start: buffer.Pos{}, End: b.EndPos()}
		}
		text = ""
	default:
		last := h.BStart + h.BLen - 1
		r = buffer.Range{Start: buffer.Pos{Line: h.BStart}, End: buffer.Pos{Line: last, Col: b.LineLen(last)}}
	}
	b.CloseUndoGroup()
	v.applyEdits([]buffer.Edit{{Range: r, Text: text}}, "", func(res []buffer.Range) []buffer.Selection {
		return []buffer.Selection{buffer.Caret(res[0].Start)}
	})
	v.dirtyDiff = nil
}

func (p *DirtyDiffPeek) draw() {
	v := p.v
	a := v.app
	th := a.theme
	anchorLine := p.hunk.BStart + max(p.hunk.BLen, 1) - 1
	if p.hunk.BLen == 0 {
		anchorLine = max(p.hunk.BStart-1, 0)
	}
	_, y, ok := v.screenPos(buffer.Pos{Line: min(anchorLine, v.buf().LineCount()-1)})
	if !ok {
		return
	}
	r := v.full
	rows := len(p.removed) + 1
	if len(p.removed) == 0 {
		rows = 2
	}
	rows = min(rows, max(r.H/2, 3))
	top := y + 1
	if top+rows > r.Y+r.H {
		top = max(r.Y+r.H-rows, r.Y)
	}
	box := Rect{r.X, top, r.W - 1, rows}
	mark := a.hitMark()
	bgc := th.C("peekViewEditor.background")
	a.cv.clip = r
	a.cv.Fill(box, ' ', styleFB(th.C("editor.foreground"), bgc))
	// header
	hst := styleFB(th.C("foreground"), th.C("peekViewTitle.background"))
	a.cv.Fill(Rect{box.X, box.Y, box.W, 1}, ' ', hst)
	title := fmt.Sprintf(" %s  %d of %d changes", v.doc.Title(), p.index+1, p.total)
	a.cv.Text(box.X, box.Y, box.X+box.W, title, hst.Bold(true))
	type btn struct {
		label string
		run   func()
	}
	btns := []btn{
		{a.Icon(IcoDiscard) + " Revert", p.revert},
		{a.Icon(IcoArrowUp), func() { p.move(-1) }},
		{a.Icon(IcoArrowDown), func() { p.move(1) }},
		{a.Icon(IcoClose), func() { v.dirtyDiff = nil }},
	}
	bx := box.X + box.W
	for i := len(btns) - 1; i >= 0; i-- {
		bw := StrWidth(btns[i].label) + 2
		bx -= bw
		a.cv.Text(bx+1, box.Y, bx+bw, btns[i].label, hst)
		run := btns[i].run
		a.addHit(Rect{bx, box.Y, bw, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				run()
			}
			return true
		})
	}
	// border lines in the peek color
	for x := box.X; x < box.X+box.W; x++ {
		_, _, st, _ := a.screen.GetContent(x, box.Y)
		ch, _, _, _ := a.screen.GetContent(x, box.Y)
		a.cv.Set(x, box.Y, ch, st.Underline(false))
	}
	y = box.Y + 1
	if len(p.removed) == 0 {
		a.cv.Text(box.X+2, y, box.X+box.W, "(added lines)", styleFB(th.C("descriptionForeground"), bgc))
	}
	rbg := th.OverColor("diffEditor.removedLineBackground", bgc)
	for _, l := range p.removed {
		if y >= box.Y+box.H {
			break
		}
		a.cv.Fill(Rect{box.X, y, box.W, 1}, ' ', styleFB(th.C("editor.foreground"), rbg))
		a.cv.Text(box.X+1, y, box.X+box.W, "- "+strings.ReplaceAll(l, "\t", "    "), styleFB(th.C("editor.foreground"), rbg))
		y++
	}
	a.cv.Set(box.X, box.Y, '▌', styleFB(th.C("peekView.border"), th.C("peekViewTitle.background")))
	a.addHitUnder(mark, box, func(ev MouseEvent) bool { return true })
}
