package ui

import (
	"fmt"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

// MarkerWidget is VS Code's inline problem view (F8): the message under the problem's line with
// "1 of N problems" and navigation.
type MarkerWidget struct {
	v        *EditorView
	line     int
	severity int
	message  string
	source   string
	index    int
	total    int
}

func (m *MarkerWidget) draw() {
	v := m.v
	a := v.app
	th := a.theme
	_, y, ok := v.screenPos(buffer.Pos{Line: min(m.line, v.buf().LineCount()-1)})
	if !ok {
		return
	}
	r := v.full
	width := r.W - 1
	var lines []string
	for _, l := range strings.Split(m.message, "\n") {
		lines = append(lines, wrapWords(l, width-4, 6)...)
	}
	if m.source != "" {
		lines = append(lines, m.source)
	}
	rows := min(len(lines)+1, max(r.H/2, 3))
	top := y + 1
	if top+rows > r.Y+r.H {
		top = max(r.Y+r.H-rows, r.Y)
	}
	box := Rect{r.X, top, width, rows}
	mark := a.hitMark()
	borderKey, icon := "editorMarkerNavigationError.background", a.Icon(IcoError)
	switch m.severity {
	case lsp.SeverityWarning:
		borderKey, icon = "editorMarkerNavigationWarning.background", a.Icon(IcoWarning)
	case lsp.SeverityInformation, lsp.SeverityHint:
		borderKey, icon = "editorMarkerNavigationInfo.background", a.Icon(IcoInfo)
	}
	bgc := th.OverColor("editorMarkerNavigation.background", th.C("editor.background"))
	border := th.C(borderKey)
	a.cv.clip = r
	a.cv.Fill(box, ' ', styleFB(th.C("editor.foreground"), bgc))
	// header: icon, file, position and navigation
	hst := styleFB(th.C("foreground"), bgc)
	x := a.cv.Text(box.X+2, box.Y, box.X+box.W, icon+" ", styleFB(border, bgc))
	x = a.cv.Text(x, box.Y, box.X+box.W, v.doc.Title(), hst.Bold(true))
	a.cv.Text(x+2, box.Y, box.X+box.W, fmt.Sprintf("%d of %d problems", m.index+1, m.total), styleFB(th.C("descriptionForeground"), bgc))
	type btn struct {
		label string
		run   func()
	}
	btns := []btn{
		{a.Icon(IcoArrowDown), func() { a.gotoProblem(1) }},
		{a.Icon(IcoArrowUp), func() { a.gotoProblem(-1) }},
		{a.Icon(IcoClose), func() { v.marker = nil }},
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
	for i, l := range lines {
		ly := box.Y + 1 + i
		if ly >= box.Y+box.H {
			break
		}
		st := styleFB(th.C("editor.foreground"), bgc)
		if i == len(lines)-1 && m.source != "" {
			st = styleFB(th.C("descriptionForeground"), bgc)
		}
		a.cv.Text(box.X+2, ly, box.X+box.W, l, st)
	}
	// the colored frame on the left, like VS Code's zone widget
	for yy := box.Y; yy < box.Y+box.H; yy++ {
		a.cv.Set(box.X, yy, '▌', styleFB(border, bgc))
	}
	a.addHitUnder(mark, box, func(ev MouseEvent) bool { return true })
}

// showMarker opens the inline problem view for a diagnostic.
func (v *EditorView) showMarker(line, severity int, message, source string, index, total int) {
	v.marker = &MarkerWidget{v: v, line: line, severity: severity, message: message, source: source, index: index, total: total}
}
