package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
	"tuicode/internal/syntax"
)

type peekItem struct {
	path  string
	start buffer.Pos
	end   buffer.Pos
}

type peekFile struct {
	lines []string
	spans [][]syntax.Span
}

// PeekView is VS Code's inline references/definition peek.
type PeekView struct {
	v        *EditorView
	anchor   int
	title    string
	items    []peekItem
	selected int
	rows     []peekRow
	scroll   int
	files    map[string]*peekFile
	collapse map[string]bool
	rect     Rect
}

type peekRow struct {
	path  string
	item  int // -1 for a file header
	count int
}

// showPeek opens the peek view under the cursor line.
func (a *App) showPeek(v *EditorView, title string, locs []lsp.Location) {
	if len(locs) == 0 {
		return
	}
	p := &PeekView{v: v, anchor: v.Cursor().Line, title: title, files: map[string]*peekFile{}, collapse: map[string]bool{}}
	for _, l := range locs {
		path := lsp.URIToPath(l.URI)
		f := p.file(a, path)
		start := buffer.Pos{Line: l.Range.Start.Line, Col: l.Range.Start.Character}
		end := buffer.Pos{Line: l.Range.End.Line, Col: l.Range.End.Character}
		if d, ok := a.docs[path]; ok {
			r := a.lspRange(d.Buf, l.Range)
			start, end = r.Start, r.End
		} else if start.Line < len(f.lines) {
			// convert UTF-16 columns using the line text
			tmp := buffer.NewFromString(f.lines[start.Line])
			start.Col = lsp.FromLSPPos(tmp, lsp.Position{Line: 0, Character: l.Range.Start.Character}, "utf-16").Col
			if end.Line == start.Line {
				end.Col = lsp.FromLSPPos(tmp, lsp.Position{Line: 0, Character: l.Range.End.Character}, "utf-16").Col
			}
		}
		p.items = append(p.items, peekItem{path: path, start: start, end: end})
	}
	cur := v.buf().Path
	sort.SliceStable(p.items, func(i, j int) bool {
		a, b := p.items[i], p.items[j]
		if a.path != b.path {
			// the current file first, then by path
			if a.path == cur {
				return true
			}
			if b.path == cur {
				return false
			}
			return a.path < b.path
		}
		return a.start.Less(b.start)
	})
	// preselect the item closest to the cursor in this file
	for i, it := range p.items {
		if it.path == cur && !v.Cursor().Less(it.start) {
			p.selected = i
		}
	}
	p.rebuild()
	v.peek = p
	// make room below the anchor like VS Code's view zone does
	if _, y, ok := v.screenPos(buffer.Pos{Line: p.anchor}); ok && y > v.rect.Y+v.rect.H/3 {
		v.ScrollBy(y - (v.rect.Y + v.rect.H/4))
	}
}

func (p *PeekView) file(a *App, path string) *peekFile {
	if f, ok := p.files[path]; ok {
		return f
	}
	f := &peekFile{}
	if d, ok := a.docs[path]; ok {
		f.lines = d.Buf.Lines()
		f.spans = d.spans
	} else if data, err := os.ReadFile(path); err == nil {
		f.lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		lang := a.detectLanguage(path, "")
		if len(f.lines) < 20000 {
			f.spans = syntax.NewHighlighter(lang, path).Highlight(f.lines)
		}
	}
	p.files[path] = f
	return f
}

func (p *PeekView) rebuild() {
	p.rows = p.rows[:0]
	files := map[string]int{}
	for _, it := range p.items {
		files[it.path]++
	}
	if len(files) == 1 {
		for i := range p.items {
			p.rows = append(p.rows, peekRow{path: p.items[i].path, item: i})
		}
		return
	}
	last := ""
	for i, it := range p.items {
		if it.path != last {
			p.rows = append(p.rows, peekRow{path: it.path, item: -1, count: files[it.path]})
			last = it.path
		}
		if !p.collapse[it.path] {
			p.rows = append(p.rows, peekRow{path: it.path, item: i})
		}
	}
}

func (p *PeekView) selectedRow() int {
	for i, r := range p.rows {
		if r.item == p.selected {
			return i
		}
	}
	return 0
}

func (p *PeekView) move(d int) {
	ri := p.selectedRow()
	for {
		ri += d
		if ri < 0 || ri >= len(p.rows) {
			return
		}
		if p.rows[ri].item >= 0 {
			p.selected = p.rows[ri].item
			return
		}
	}
}

func (p *PeekView) open() {
	v := p.v
	a := v.app
	it := p.items[p.selected]
	v.peek = nil
	a.pushNav()
	nv := a.OpenFile(it.path, false)
	if nv != nil {
		nv.SetSelections([]buffer.Selection{{Anchor: it.start, Active: it.start}}, 0)
		nv.ensurePosVisible(it.start, true)
	}
	a.focusEditor()
}

func (p *PeekView) handleKey(key string) bool {
	switch key {
	case "escape":
		p.v.peek = nil
	case "up":
		p.move(-1)
	case "down":
		p.move(1)
	case "pageup":
		for i := 0; i < 5; i++ {
			p.move(-1)
		}
	case "pagedown":
		for i := 0; i < 5; i++ {
			p.move(1)
		}
	case "enter":
		p.open()
	case "left", "right":
		ri := p.selectedRow()
		path := p.rows[ri].path
		p.collapse[path] = key == "left"
		p.rebuild()
	default:
		return false
	}
	return true
}

func (p *PeekView) draw() {
	v := p.v
	a := v.app
	th := a.theme
	_, ay, ok := v.screenPos(buffer.Pos{Line: p.anchor})
	if !ok {
		return
	}
	full := v.full
	top := ay + 1
	h := min(14, full.Y+full.H-top)
	if h < 5 {
		top = max(ay-14, full.Y)
		h = min(14, ay-top)
	}
	if h < 4 {
		return
	}
	r := Rect{full.X, top, full.W, h}
	p.rect = r
	mark := a.hitMark()
	a.cv.clip = r
	border := th.C("peekView.border")
	// title row
	titleBg := th.C("peekViewTitle.background")
	a.cv.Fill(Rect{r.X, r.Y, r.W, 1}, ' ', styleFB(th.C("foreground"), titleBg))
	it := p.items[p.selected]
	x := a.cv.Text(r.X+1, r.Y, r.X+r.W, filepath.Base(it.path), styleFB(th.Or("peekViewTitleLabel.foreground", "foreground"), titleBg))
	if dir := filepath.Dir(a.relPath(it.path)); dir != "." {
		x = a.cv.Text(x+1, r.Y, r.X+r.W, dir, styleFB(th.Or("peekViewTitleDescription.foreground", "descriptionForeground"), titleBg))
	}
	count := fmt.Sprintf(" – %d %s", len(p.items), strings.ToLower(p.title))
	a.cv.Text(x, r.Y, r.X+r.W-4, count, styleFB(th.C("descriptionForeground"), titleBg))
	a.cv.Text(r.X+r.W-3, r.Y, r.X+r.W-1, a.Icon(IcoClose), styleFB(th.C("foreground"), titleBg))
	a.addHit(Rect{r.X + r.W - 3, r.Y, 2, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			v.peek = nil
		}
		return true
	})
	for xx := r.X; xx < r.X+r.W; xx++ {
		ch, _, st, _ := a.screen.GetContent(xx, r.Y)
		a.cv.Set(xx, r.Y, ch, st.Underline(tcell.UnderlineStyleSolid, border))
	}
	body := Rect{r.X, r.Y + 1, r.W, r.H - 2}
	listW := clampInt(r.W*35/100, 20, 60)
	prev := Rect{body.X, body.Y, body.W - listW, body.H}
	list := Rect{body.X + body.W - listW, body.Y, listW, body.H}
	// preview of the selected location
	ebg := th.C("peekViewEditor.background")
	a.cv.Fill(prev, ' ', styleFB(th.C("editor.foreground"), ebg))
	f := p.file(a, it.path)
	first := max(it.start.Line-prev.H/3, 0)
	digits := len(fmt.Sprint(len(f.lines)))
	for i := 0; i < prev.H; i++ {
		ln := first + i
		y := prev.Y + i
		if ln >= len(f.lines) {
			break
		}
		num := fmt.Sprint(ln + 1)
		numSt := styleFB(th.C("editorLineNumber.foreground"), ebg)
		if ln == it.start.Line {
			numSt = numSt.Foreground(th.C("editorLineNumber.activeForeground"))
		}
		a.cv.Text(prev.X+1+digits-len(num), y, prev.X+prev.W, num, numSt)
		tx := prev.X + digits + 3
		rs := []rune(f.lines[ln])
		var spans []syntax.Span
		if ln < len(f.spans) {
			spans = f.spans[ln]
		}
		si := 0
		vx := 0
		ts := v.tabSize()
		for ci, ch := range rs {
			if tx >= prev.X+prev.W {
				break
			}
			st := styleFB(th.C("editor.foreground"), ebg)
			for si < len(spans) && spans[si].End <= ci {
				si++
			}
			if si < len(spans) && spans[si].Start <= ci && spans[si].Kind != syntax.KBracket {
				st = th.Token(spans[si].Kind).Apply(st)
			}
			pos := buffer.Pos{Line: ln, Col: ci}
			if !pos.Less(it.start) && pos.Less(it.end) {
				st = st.Background(th.OverColor("peekViewEditor.matchHighlightBackground", ebg))
			}
			w := 1
			if ch == '\t' {
				w = ts - vx%ts
				ch = ' '
			} else {
				w = RuneWidth(ch)
			}
			for k := 0; k < w; k++ {
				c := ch
				if k > 0 {
					c = ' '
				}
				if k == 0 || ch == ' ' {
					a.cv.Set(tx+k, y, c, st)
				}
			}
			tx += w
			vx += w
		}
	}
	a.addHit(prev, func(ev MouseEvent) bool {
		if ev.Kind == MousePress && ev.Clicks >= 2 {
			p.open()
		}
		return true
	})
	// results list
	lbg := th.C("peekViewResult.background")
	a.cv.Fill(list, ' ', styleFB(th.C("foreground"), lbg))
	sel := p.selectedRow()
	if sel < p.scroll {
		p.scroll = sel
	}
	if sel >= p.scroll+list.H {
		p.scroll = sel - list.H + 1
	}
	for i := 0; i < list.H; i++ {
		ri := p.scroll + i
		if ri >= len(p.rows) {
			break
		}
		row := p.rows[ri]
		y := list.Y + i
		rbg := lbg
		if row.item == p.selected && row.item >= 0 {
			rbg = th.OverColor("peekViewResult.selectionBackground", lbg)
		}
		st := styleFB(th.Or("peekViewResult.lineForeground", "foreground"), rbg)
		a.cv.Fill(Rect{list.X, y, list.W, 1}, ' ', st)
		if row.item < 0 {
			chev := a.Icon(IcoChevronDown)
			if p.collapse[row.path] {
				chev = a.Icon(IcoChevronRight)
			}
			icon, col := a.FileIcon(filepath.Base(row.path), false, false)
			x := a.cv.Text(list.X+1, y, list.X+list.W, chev+" ", st)
			if icon != "" {
				x = a.cv.Text(x, y, list.X+list.W, icon+" ", styleFB(col, rbg))
			}
			x = a.cv.Text(x, y, list.X+list.W, filepath.Base(row.path), st.Foreground(th.Or("peekViewResult.fileForeground", "foreground")))
			a.cv.Text(x+1, y, list.X+list.W-1, fmt.Sprint(row.count), styleFB(th.C("badge.foreground"), th.C("badge.background")))
		} else {
			itm := p.items[row.item]
			pf := p.file(a, itm.path)
			text := ""
			if itm.start.Line < len(pf.lines) {
				text = pf.lines[itm.start.Line]
			}
			rs := []rune(text)
			lead := len(rs) - len([]rune(strings.TrimLeft(text, " \t")))
			x := list.X + 3
			for ci := lead; ci < len(rs) && x < list.X+list.W-1; ci++ {
				cst := st
				pos := buffer.Pos{Line: itm.start.Line, Col: ci}
				if !pos.Less(itm.start) && pos.Less(itm.end) {
					cst = cst.Background(th.OverColor("peekViewResult.matchHighlightBackground", rbg))
				}
				ch := rs[ci]
				if ch == '\t' {
					ch = ' '
				}
				a.cv.Set(x, y, ch, cst)
				x += RuneWidth(ch)
			}
		}
		rowIdx := ri
		a.addHit(Rect{list.X, y, list.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				row := p.rows[rowIdx]
				if row.item < 0 {
					p.collapse[row.path] = !p.collapse[row.path]
					p.rebuild()
				} else {
					p.selected = row.item
					if ev.Clicks >= 2 {
						p.open()
					}
				}
			case MouseWheel:
				p.scroll = clampInt(p.scroll+ev.WheelDY, 0, max(len(p.rows)-list.H, 0))
			}
			return true
		})
	}
	// bottom border
	by := r.Y + r.H - 1
	a.cv.Fill(Rect{r.X, by, r.W, 1}, '▔', styleFB(border, th.C("editor.background")))
	a.addHitUnder(mark, r, func(ev MouseEvent) bool { return true })
	a.cv.clip = v.full
}
