package ui

import (
	"fmt"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/git"
	"tuicode/internal/syntax"
)

func buffer0(line int) buffer.Pos { return buffer.Pos{Line: line} }

type diffRow struct {
	l, r int  // line indices, -1 when absent
	kind byte // '=', '-', '+', '~'
}

// DiffPage is a read-only side-by-side diff editor.
type DiffPage struct {
	app     *App
	title   string
	path    string
	left    []string
	right   []string
	lSpans  [][]syntax.Span
	rSpans  [][]syntax.Span
	rows    []diffRow
	scroll  int
	cursor  int
	rect    Rect
	inline  bool
	changes int
}

func (a *App) openDiffPage(title, path, left, right string) {
	left = strings.ReplaceAll(left, "\r\n", "\n")
	right = strings.ReplaceAll(right, "\r\n", "\n")
	dp := &DiffPage{app: a, title: title, path: path}
	if left == "" {
		dp.left = nil
	} else {
		dp.left = strings.Split(left, "\n")
	}
	dp.right = strings.Split(right, "\n")
	if right == "" {
		dp.right = nil
	}
	lang := a.detectLanguage(path, "")
	hl := syntax.NewHighlighter(lang, path)
	dp.lSpans = hl.Highlight(dp.left)
	dp.rSpans = hl.Highlight(dp.right)
	ops := git.DiffLines(dp.left, dp.right)
	// pair runs of deletions and insertions as modified rows
	for i := 0; i < len(ops); {
		o := ops[i]
		if o.Kind == '=' {
			dp.rows = append(dp.rows, diffRow{o.ALine, o.BLine, '='})
			i++
			continue
		}
		var dels, ins []git.Op
		for i < len(ops) && ops[i].Kind == '-' {
			dels = append(dels, ops[i])
			i++
		}
		for i < len(ops) && ops[i].Kind == '+' {
			ins = append(ins, ops[i])
			i++
		}
		dp.changes++
		n := max(len(dels), len(ins))
		for k := 0; k < n; k++ {
			row := diffRow{-1, -1, '~'}
			if k < len(dels) {
				row.l = dels[k].ALine
			}
			if k < len(ins) {
				row.r = ins[k].BLine
			}
			if row.l < 0 {
				row.kind = '+'
			} else if row.r < 0 {
				row.kind = '-'
			}
			dp.rows = append(dp.rows, row)
		}
	}
	// replace an existing diff tab for the same title
	g := a.ActiveGroup()
	for i, t := range g.tabs {
		if old, ok := t.special.(*DiffPage); ok && old.title == title {
			t.special = dp
			g.setActive(i)
			dp.nextChange(1)
			a.focusEditor()
			return
		}
	}
	v := &EditorView{app: a, special: dp}
	g.addTab(v, true)
	dp.cursor = -1
	dp.nextChange(1)
	a.focusEditor()
}

// Title implements SpecialPage.
func (dp *DiffPage) Title() string { return dp.title }

func (dp *DiffPage) nextChange(dir int) {
	n := len(dp.rows)
	if n == 0 {
		return
	}
	i := dp.cursor
	// skip the current block
	for i >= 0 && i < n && dp.rows[i].kind != '=' {
		i += dir
	}
	for steps := 0; steps < n; steps++ {
		i += dir
		if i >= n {
			i = 0
		}
		if i < 0 {
			i = n - 1
		}
		if dp.rows[i].kind != '=' && (i == 0 || dp.rows[i-1].kind == '=' || dir < 0) {
			// for backwards moves find the block start
			for dir < 0 && i > 0 && dp.rows[i-1].kind != '=' {
				i--
			}
			dp.cursor = i
			dp.scroll = max(i-dp.rect.H/3, 0)
			return
		}
	}
	if dp.cursor < 0 {
		dp.cursor = 0
	}
}

// HandleKey implements SpecialPage.
func (dp *DiffPage) HandleKey(key, text string, isText bool) bool {
	h := max(dp.rect.H, 1)
	switch key {
	case "up":
		dp.cursor = max(dp.cursor-1, 0)
	case "down":
		dp.cursor = min(dp.cursor+1, len(dp.rows)-1)
	case "pageup":
		dp.cursor = max(dp.cursor-h, 0)
		dp.scroll = max(dp.scroll-h, 0)
	case "pagedown":
		dp.cursor = min(dp.cursor+h, len(dp.rows)-1)
		dp.scroll = min(dp.scroll+h, max(len(dp.rows)-h, 0))
	case "ctrl+home", "home":
		dp.cursor = 0
	case "ctrl+end", "end":
		dp.cursor = len(dp.rows) - 1
	case "f7", "alt+f5":
		dp.nextChange(1)
		return true
	case "shift+f7", "shift+alt+f5":
		dp.nextChange(-1)
		return true
	case "enter":
		dp.openAtCursor()
		return true
	default:
		return false
	}
	if dp.cursor < dp.scroll {
		dp.scroll = dp.cursor
	}
	if dp.cursor >= dp.scroll+h {
		dp.scroll = dp.cursor - h + 1
	}
	return true
}

func (dp *DiffPage) openAtCursor() {
	if dp.path == "" {
		return
	}
	line := 0
	for i := dp.cursor; i >= 0 && i < len(dp.rows); i-- {
		if dp.rows[i].r >= 0 {
			line = dp.rows[i].r
			break
		}
	}
	if v := dp.app.OpenFile(dp.path, false); v != nil {
		v.SetCursor(v.buf().Clamp(buffer.Pos{Line: line}))
		v.ensurePosVisible(v.Cursor(), true)
	}
}

// Draw implements SpecialPage.
func (dp *DiffPage) Draw(r Rect) {
	a := dp.app
	th := a.theme
	dp.rect = r
	bgc := th.C("editor.background")
	dp.scroll = clampInt(dp.scroll, 0, max(len(dp.rows)-r.H, 0))
	dp.inline = r.W < 60
	digits := len(fmt.Sprint(max(len(dp.left), len(dp.right))))
	if digits < 3 {
		digits = 3
	}
	half := r.W / 2
	drawSide := func(x, w, y int, lines []string, spans [][]syntax.Span, idx int, kind byte, other string, isLeft bool, cursorRow bool) {
		rowBg := bgc
		switch {
		case idx < 0:
			// filler
			fst := styleFB(th.OverColor("diffEditor.diagonalFill", bgc), bgc)
			if !th.Has("diffEditor.diagonalFill") {
				fst = styleFB(th.OverColor("editorWhitespace.foreground", bgc), bgc)
			}
			for xx := x; xx < x+w; xx++ {
				a.cv.Set(xx, y, '╱', fst)
			}
			return
		case kind == '-' || kind == '~' && isLeft:
			rowBg = th.OverColor("diffEditor.removedLineBackground", bgc)
		case kind == '+' || kind == '~' && !isLeft:
			rowBg = th.OverColor("diffEditor.insertedLineBackground", bgc)
		}
		if cursorRow && a.focus == FocusEditor {
			rowBg = th.OverColor("editor.lineHighlightBackground", rowBg)
		}
		base := styleFB(th.C("editor.foreground"), rowBg)
		a.cv.Fill(Rect{x, y, w, 1}, ' ', base)
		num := fmt.Sprint(idx + 1)
		a.cv.Text(x+digits-len(num), y, x+digits, num, styleFB(th.C("editorLineNumber.foreground"), rowBg))
		sign := " "
		if kind == '-' || kind == '~' && isLeft {
			sign = "-"
		} else if kind == '+' || kind == '~' && !isLeft {
			sign = "+"
		}
		a.cv.Text(x+digits+1, y, x+digits+2, sign, base)
		line := []rune(strings.ReplaceAll(lines[idx], "\t", "    "))
		orig := []rune(lines[idx])
		tabsChanged := len(line) != len(orig)
		// intra-line change range
		cs, ce := -1, -1
		if kind == '~' {
			o := []rune(other)
			p := 0
			for p < len(orig) && p < len(o) && orig[p] == o[p] {
				p++
			}
			s := 0
			for s < len(orig)-p && s < len(o)-p && orig[len(orig)-1-s] == o[len(o)-1-s] {
				s++
			}
			cs, ce = p, len(orig)-s
		}
		tx := x + digits + 3
		var sp []syntax.Span
		if idx < len(spans) && !tabsChanged {
			sp = spans[idx]
		}
		si := 0
		for ci, ch := range line {
			if tx >= x+w {
				break
			}
			st := base
			for si < len(sp) && sp[si].End <= ci {
				si++
			}
			if si < len(sp) && sp[si].Start <= ci {
				if sp[si].Kind == syntax.KBracket {
					st = st.Foreground(th.C(fmt.Sprintf("editorBracketHighlight.foreground%d", int(sp[si].Depth)+1)))
				} else {
					st = th.Token(sp[si].Kind).Apply(st)
				}
			}
			if ci >= cs && ci < ce {
				key := "diffEditor.insertedTextBackground"
				if isLeft {
					key = "diffEditor.removedTextBackground"
				}
				st = st.Background(th.OverColor(key, rowBg))
			}
			a.cv.Set(tx, y, ch, st)
			tx += RuneWidth(ch)
		}
	}
	for i := 0; i < r.H; i++ {
		idx := dp.scroll + i
		y := r.Y + i
		if idx >= len(dp.rows) {
			break
		}
		row := dp.rows[idx]
		cur := idx == dp.cursor
		otherL, otherR := "", ""
		if row.l >= 0 {
			otherR = dp.left[row.l]
		}
		if row.r >= 0 {
			otherL = dp.right[row.r]
		}
		if dp.inline {
			if row.kind == '=' || row.kind == '+' {
				drawSide(r.X, r.W, y, dp.right, dp.rSpans, row.r, row.kind, otherR, false, cur)
			} else if row.kind == '-' {
				drawSide(r.X, r.W, y, dp.left, dp.lSpans, row.l, row.kind, otherL, true, cur)
			} else {
				drawSide(r.X, r.W, y, dp.right, dp.rSpans, row.r, '+', otherR, false, cur)
			}
		} else {
			drawSide(r.X, half-1, y, dp.left, dp.lSpans, row.l, row.kind, otherL, true, cur)
			a.cv.Set(r.X+half-1, y, '│', styleFB(th.OverColor("editorGroup.border", bgc), bgc))
			drawSide(r.X+half, r.W-half, y, dp.right, dp.rSpans, row.r, row.kind, otherR, false, cur)
		}
		rowIdx := idx
		a.addHit(Rect{r.X, y, r.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				dp.cursor = rowIdx
				a.focusEditor()
				if ev.Clicks >= 2 {
					dp.openAtCursor()
				}
			case MouseWheel:
				dp.scroll = clampInt(dp.scroll+ev.WheelDY*3, 0, max(len(dp.rows)-r.H, 0))
			}
			return true
		})
	}
	if len(dp.rows) == 0 || dp.changes == 0 {
		msg := "Files are identical"
		a.cv.TextCenter(r.X, r.Y+r.H/2, r.W, msg, styleFB(th.C("descriptionForeground"), bgc))
	}
}
