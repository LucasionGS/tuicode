package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/git"
	"tuicode/internal/lsp"
	"tuicode/internal/syntax"
	"tuicode/internal/theme"
)

// rowCell is one rendered cell of a visual row.
type rowCell struct {
	ch    rune
	col   int // buffer column, -1 for virtual text
	width int
	tab   bool
	inlay bool
	ws    bool
	color tcell.Color // color decorator swatch
}

type span struct {
	start, end buffer.Pos
	key        string
}

// buildRow lays out the cells of one visual row.
func (v *EditorView) buildRow(line, seg int) ([]rowCell, int) {
	b := v.buf()
	l := b.Line(line)
	segs := v.segments(line)
	start := segs[seg]
	end := v.segEnd(line, seg)
	ts := v.tabSize()
	cells := make([]rowCell, 0, end-start+8)
	indent := v.segIndent(line, seg)
	x := v.visualX(line, start)
	hints := v.inlayHints[line]
	if colors := v.colorDecorations(line); len(colors) > 0 {
		hints = mergeInlays(hints, colors)
	}
	hi := 0
	for hi < len(hints) && hints[hi].col < start {
		hi++
	}
	addInlay := func(h inlay) {
		label := h.label
		if h.left {
			label = " " + label
		}
		if h.right {
			label += " "
		}
		for _, r := range label {
			cells = append(cells, rowCell{ch: r, col: -1, width: RuneWidth(r), inlay: true, color: h.color})
		}
	}
	for i := start; i < end; i++ {
		for hi < len(hints) && hints[hi].col == i {
			addInlay(hints[hi])
			hi++
		}
		r := l[i]
		if r == '\t' {
			w := ts - x%ts
			cells = append(cells, rowCell{ch: ' ', col: i, width: w, tab: true, ws: true})
			x += w
			continue
		}
		w := RuneWidth(r)
		if r < 0x20 || r == 0x7f {
			// control characters render as a symbol
			cells = append(cells, rowCell{ch: rune(0x2400 + int(r)%0x80), col: i, width: 1})
			if r == 0x7f {
				cells[len(cells)-1].ch = '␡'
			}
			x++
			continue
		}
		cells = append(cells, rowCell{ch: r, col: i, width: w, ws: r == ' '})
		x += w
	}
	if seg == len(segs)-1 {
		for hi < len(hints) {
			if hints[hi].col >= end {
				addInlay(hints[hi])
			}
			hi++
		}
	}
	return cells, indent
}

func (v *EditorView) gutterWidth() int {
	a := v.app
	w := 2 // git bar + fold chevron
	if a.lineNumbers != "off" {
		digits := len(fmt.Sprint(v.buf().LineCount()))
		if digits < 3 {
			digits = 3
		}
		w += digits + 1
	}
	return w + 1
}

func (v *EditorView) draw(r Rect, focused bool) {
	a := v.app
	th := a.theme
	b := v.buf()
	v.full = r
	bgc := th.C("editor.background")
	fgc := th.C("editor.foreground")
	base := styleFB(fgc, bgc)
	a.cv.clip = r
	a.cv.Fill(r, ' ', base)

	v.gutterW = v.gutterWidth()
	mmW := 0
	if a.minimapEnabled && r.W > 70 && !a.zenMode {
		mmW = clampInt(r.W/12, 6, 14)
	}
	sbW := 1
	text := Rect{r.X + v.gutterW, r.Y, r.W - v.gutterW - mmW - sbW, r.H}
	if text.W < 5 {
		text.W = max(r.W-v.gutterW, 1)
		mmW = 0
	}
	if text.W != v.rect.W {
		v.wrapCache = nil
	}
	v.rect = text
	v.minimap = Rect{text.X + text.W, r.Y, mmW, r.H}

	// keep the top row valid
	if v.topLine >= b.LineCount() {
		v.topLine = max(b.LineCount()-1, 0)
		v.topSeg = 0
	}
	if v.isHidden(v.topLine) {
		v.topLine = v.prevVisibleLine(v.topLine)
		v.topSeg = 0
	}
	if v.topSeg >= len(v.segments(v.topLine)) {
		v.topSeg = 0
	}

	v.computeHighlights()
	cur := v.Cursor()
	// overlay spans
	var overlays []span
	if v.find != nil && v.find.visible {
		for i, m := range v.find.matches {
			key := "editor.findMatchHighlightBackground"
			if i == v.find.current {
				key = "editor.findMatchBackground"
			}
			overlays = append(overlays, span{m.Start, m.End, key})
		}
	}
	for _, h := range v.highlights {
		overlays = append(overlays, span{h.Start, h.End, "editor.wordHighlightBackground"})
	}
	if v.snippet != nil {
		for _, ts := range v.snippet.placeholders() {
			if !ts.Empty() {
				overlays = append(overlays, span{ts.Start, ts.End, "editor.snippetTabstopHighlightBackground"})
			}
		}
	}
	var bracketPos []buffer.Pos
	if a.settings.String("editor.matchBrackets", "") != "never" {
		if bp, ok := v.bracketNearCursor(cur); ok {
			if m, ok2 := v.matchBracket(bp); ok2 {
				bracketPos = []buffer.Pos{bp, m}
			}
		}
	}
	// active indent guide
	guideX, guideFrom, guideTo := v.activeIndentGuide()
	bracketColors := a.settings.Bool("editor.bracketPairColorization.enabled", "")
	a.maybeRequestInlayHints(v)
	a.maybeRequestLightbulb(v)
	if v.doc.semVersion != v.buf().Version() && v.doc.semTimer == nil {
		a.scheduleSemanticTokens(v.doc)
	}

	lineHL := a.settings.String("editor.renderLineHighlight", "")
	hlBg := bgc
	if th.Defined("editor.lineHighlightBackground") {
		hlBg = th.OverColor("editor.lineHighlightBackground", bgc)
	} else if th.Has("editor.lineHighlightBorder") {
		hlBg = th.OverColor("editor.lineHighlightBorder", bgc)
	}

	// sticky scroll
	sticky := []int{}
	if a.stickyScroll && !v.wrap {
		sticky = v.computeSticky()
	}
	v.stickyRows = len(sticky)
	v.stickyLines = sticky

	row := v.top()
	type cursorDraw struct {
		x, y    int
		primary bool
	}
	var cursors []cursorDraw
	rulers := v.rulerColumns()
	showFold := a.settings.String("editor.showFoldingControls", "") == "always" || r.Contains(a.mouseX, a.mouseY) && a.mouseX < text.X
	errs := map[int]int{}
	for _, d := range v.doc.Diags {
		for l := d.Range.Start.Line; l <= d.Range.End.Line; l++ {
			if errs[l] == 0 || d.Severity < errs[l] {
				errs[l] = d.Severity
			}
		}
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		line := row.line
		if line >= b.LineCount() {
			break
		}
		isCurLine := line == cur.Line
		rowBg := bgc
		if isCurLine && (lineHL == "line" || lineHL == "all") && !v.HasSelection() && focused {
			rowBg = hlBg
		}
		// gutter
		gst := styleFB(th.C("editorLineNumber.foreground"), th.C("editorGutter.background"))
		if isCurLine {
			gst = gst.Foreground(th.C("editorLineNumber.activeForeground"))
		}
		a.cv.Fill(Rect{r.X, y, v.gutterW, 1}, ' ', styleFB(fgc, th.C("editorGutter.background")))
		gx := r.X
		if a.lineNumbers != "off" && row.seg == 0 {
			num := line + 1
			if a.lineNumbers == "relative" && !isCurLine {
				num = abs(line - cur.Line)
			}
			label := fmt.Sprint(num)
			digits := v.gutterW - 3
			a.cv.Text(gx+digits-len(label), y, gx+digits, label, gst)
		}
		if a.lineNumbers != "off" {
			gx += v.gutterW - 3
		}
		// git change bar
		if ch, ok := v.doc.gitChanges[line]; ok && row.seg == 0 && v.doc.hasGitBase {
			var key string
			glyph := '▎'
			switch ch {
			case git.ChangeAdded:
				key = "editorGutter.addedBackground"
			case git.ChangeModified:
				key = "editorGutter.modifiedBackground"
			case git.ChangeDeleted:
				key = "editorGutter.deletedBackground"
				glyph = '▁'
				if line == 0 {
					glyph = '▔'
				}
			}
			a.cv.Set(gx, y, glyph, styleFB(th.C(key), th.C("editorGutter.background")))
		} else if v.doc.hasGitBase && row.seg > 0 {
			if ch, ok := v.doc.gitChanges[line]; ok && ch != git.ChangeDeleted {
				key := "editorGutter.modifiedBackground"
				if ch == git.ChangeAdded {
					key = "editorGutter.addedBackground"
				}
				a.cv.Set(gx, y, '▎', styleFB(th.C(key), th.C("editorGutter.background")))
			}
		}
		gitX := gx
		gx++
		// fold chevron
		if row.seg == 0 && a.settings.Bool("editor.folding", "") {
			if _, folded := v.foldAt(line); folded {
				a.cv.Text(gx, y, gx+1, a.Icon(IcoChevronRight), styleFB(th.C("editorGutter.foldingControlForeground"), th.C("editorGutter.background")))
			} else if showFold {
				if _, ok := v.foldRangeAt(line); ok {
					a.cv.Text(gx, y, gx+1, a.Icon(IcoChevronDown), styleFB(th.C("editorGutter.foldingControlForeground"), th.C("editorGutter.background")))
				}
			}
		}
		foldX := gx
		lineNo := line
		a.addHit(Rect{r.X, y, v.gutterW, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress && ev.X == gitX && ev.Button == tcell.Button1 {
				if _, changed := v.doc.gitChanges[lineNo]; changed {
					v.openDirtyDiff(lineNo)
					return true
				}
			}
			v.gutterMouse(ev, lineNo, ev.X == foldX)
			return true
		})
		if v.lightbulbLine == line && row.seg == 0 && focused {
			lb := a.Icon(IcoLightbulb)
			a.cv.Text(r.X, y, r.X+1, lb, styleFB(th.C("editorLightBulb.foreground"), th.C("editorGutter.background")))
			a.addHit(Rect{r.X, y, 1, 1}, func(ev MouseEvent) bool {
				if ev.Kind == MousePress {
					a.showCodeActions(v, "")
				}
				return true
			})
		}

		// text
		cells, indent := v.buildRow(line, row.seg)
		a.cv.Fill(Rect{text.X, y, text.W, 1}, ' ', base.Background(rowBg))
		x := text.X - v.leftCol + indent
		spans := v.doc.SpansFor(line)
		si := 0
		lineIndent := v.indentLevel(line)
		if lineIndent < 0 {
			lineIndent = v.blankLineIndent(line)
		}
		var vx int // visual x within the line (for guides)
		if row.seg == 0 {
			vx = 0
		} else {
			vx = v.visualX(line, v.segments(line)[row.seg])
		}
		leading := true
		for _, c := range cells {
			if c.col >= 0 && leading && !c.ws {
				leading = false
			}
			if x >= text.X+text.W {
				break
			}
			st := base.Background(rowBg)
			if c.inlay && c.color != 0 {
				st = styleFB(c.color, rowBg)
			} else if c.inlay {
				st = styleFB(th.C("editorInlayHint.foreground"), th.OverColor("editorInlayHint.background", rowBg))
			} else {
				for si < len(spans) && spans[si].End <= c.col {
					si++
				}
				if sk, ok := v.doc.semanticAt(line, c.col); ok {
					st = th.Token(sk).Apply(st)
				} else if si < len(spans) && spans[si].Start <= c.col && (bracketColors || spans[si].Kind != syntax.KBracket) {
					sp := spans[si]
					if sp.Kind == syntax.KBracket {
						st = st.Foreground(th.C(fmt.Sprintf("editorBracketHighlight.foreground%d", int(sp.Depth)+1)))
					} else {
						st = th.Token(sp.Kind).Apply(st)
					}
				}
				pos := buffer.Pos{Line: line, Col: c.col}
				bgNow := rowBg
				for _, o := range overlays {
					if posInSpan(pos, o) {
						bgNow = th.OverColor(o.key, bgNow)
					}
				}
				for _, bp := range bracketPos {
					if bp == pos {
						bgNow = th.OverColor("editorBracketMatch.background", bgNow)
						st = st.Underline(tcell.UnderlineStyleSolid, th.C("editorBracketMatch.border"))
					}
				}
				inSel := false
				for _, s := range v.sels {
					sr := s.Range()
					if !sr.Empty() && !pos.Less(sr.Start) && pos.Less(sr.End) {
						inSel = true
						break
					}
				}
				if inSel {
					key := "editor.selectionBackground"
					if !focused {
						key = "editor.inactiveSelectionBackground"
					}
					bgNow = th.OverColor(key, bgNow)
					if th.Defined("editor.selectionForeground") {
						st = st.Foreground(th.C("editor.selectionForeground"))
					}
				}
				st = st.Background(bgNow)
				// diagnostics
				for _, d := range v.doc.Diags {
					if diagCovers(d, pos, b) {
						if d.Severity == lsp.SeverityHint {
							if hasTag(d.Tags, 1) {
								st = st.Dim(true)
							}
							continue
						}
						if hasTag(d.Tags, 1) {
							st = st.Dim(true)
							continue
						}
						if hasTag(d.Tags, 2) {
							st = st.StrikeThrough(true)
						}
						key := "editorError.foreground"
						switch d.Severity {
						case lsp.SeverityWarning:
							key = "editorWarning.foreground"
						case lsp.SeverityInformation:
							key = "editorInfo.foreground"
						}
						st = st.Underline(tcell.UnderlineStyleCurly, th.C(key))
						break
					}
				}
				// link underline when ctrl-hovering
				if v.linkRange != nil && posInRange(pos, *v.linkRange) {
					st = st.Underline(tcell.UnderlineStyleSolid, th.C("editorLink.activeForeground")).Foreground(th.C("editorLink.activeForeground"))
				}
				// whitespace rendering
				if c.ws {
					show := false
					switch a.whitespaceRender {
					case "all":
						show = true
					case "selection":
						show = inSel
					case "boundary":
						show = leading || c.tab || (c.col+1 < b.LineLen(line) && b.RuneAt(buffer.Pos{Line: line, Col: c.col + 1}) == ' ') || (c.col > 0 && b.RuneAt(buffer.Pos{Line: line, Col: c.col - 1}) == ' ')
					case "trailing":
						show = strings.TrimRight(string(b.Line(line)[c.col:]), " \t") == ""
					}
					if show {
						wst := st.Foreground(th.OverColor("editorWhitespace.foreground", bgNow))
						glyph := '·'
						if c.tab {
							glyph = '→'
						}
						if x >= text.X {
							a.cv.Set(x, y, glyph, wst)
						}
						for i := 1; i < c.width; i++ {
							if x+i >= text.X {
								a.cv.Set(x+i, y, ' ', st)
							}
						}
						x += c.width
						vx += c.width
						continue
					}
				}
			}
			if x >= text.X {
				if c.tab || c.ch == ' ' {
					for i := 0; i < c.width; i++ {
						a.cv.Set(x+i, y, ' ', st)
					}
				} else {
					a.cv.Set(x, y, c.ch, st)
					if c.width == 2 && x+1 < text.X+text.W {
						// wide rune occupies two cells
					}
				}
			}
			// indent guides inside leading whitespace
			if c.ws && leading && a.settings.Bool("editor.guides.indentation", "") && !c.inlay {
				for gx := vx; gx < vx+c.width; gx++ {
					if gx%v.tabSize() == 0 && gx < lineIndent {
						key := "editorIndentGuide.background1"
						if !th.Has(key) {
							key = "editorIndentGuide.background"
						}
						if gx == guideX && line >= guideFrom && line <= guideTo {
							key = "editorIndentGuide.activeBackground1"
							if !th.Has(key) {
								key = "editorIndentGuide.activeBackground"
							}
						}
						px := x + (gx - vx)
						if px >= text.X && px < text.X+text.W {
							_, _, cst, _ := a.screen.GetContent(px, y)
							a.cv.Set(px, y, '▏', cst.Foreground(th.OverColor(key, rowBg)))
						}
					}
				}
			}
			x += c.width
			vx += c.width
		}
		// guides on blank lines
		if len(cells) == 0 && a.settings.Bool("editor.guides.indentation", "") {
			for gx := 0; gx < lineIndent; gx += v.tabSize() {
				px := text.X - v.leftCol + gx
				if px >= text.X && px < text.X+text.W {
					key := "editorIndentGuide.background1"
					if !th.Has(key) {
						key = "editorIndentGuide.background"
					}
					if gx == guideX && line >= guideFrom && line <= guideTo {
						key = "editorIndentGuide.activeBackground1"
						if !th.Has(key) {
							key = "editorIndentGuide.activeBackground"
						}
					}
					a.cv.Set(px, y, '▏', styleFB(th.OverColor(key, rowBg), rowBg))
				}
			}
		}
		// selection extends past line end for multi-line selections
		lastSeg := row.seg == len(v.segments(line))-1
		if lastSeg && x < text.X+text.W {
			eol := buffer.Pos{Line: line, Col: b.LineLen(line)}
			for _, s := range v.sels {
				sr := s.Range()
				if !sr.Empty() && !eol.Less(sr.Start) && eol.Less(sr.End) {
					key := "editor.selectionBackground"
					if !focused {
						key = "editor.inactiveSelectionBackground"
					}
					if x >= text.X {
						a.cv.Set(x, y, ' ', base.Background(th.OverColor(key, rowBg)))
					}
					break
				}
			}
		}
		// folded placeholder
		if f, folded := v.foldAt(line); folded && lastSeg {
			fx := max(x+1, text.X)
			label := " ⋯ "
			fst := base.Background(th.OverColor("editor.foldBackground", rowBg)).Foreground(th.C("editorGutter.foldingControlForeground"))
			end := a.cv.Text(fx, y, text.X+text.W, label, fst)
			fold := f
			a.addHit(Rect{fx, y, end - fx, 1}, func(ev MouseEvent) bool {
				if ev.Kind == MousePress {
					v.Unfold(fold.Start)
				}
				return true
			})
		}
		// rulers
		for _, rc := range rulers {
			px := text.X - v.leftCol + rc
			if px >= text.X && px < text.X+text.W {
				ch, _, cst, _ := a.screen.GetContent(px, y)
				if ch == ' ' {
					a.cv.Set(px, y, '▏', cst.Foreground(th.OverColor("editorRuler.foreground", rowBg)))
				}
			}
		}
		// cursors on this row
		for i, s := range v.sels {
			p := s.Active
			if p.Line != line || v.segOf(line, p.Col) != row.seg {
				continue
			}
			// columns at a wrap boundary belong to the next row unless at line end
			cx := text.X - v.leftCol + indent
			for _, c := range cells {
				if c.col >= p.Col && c.col >= 0 {
					break
				}
				cx += c.width
			}
			if cx >= text.X && cx < text.X+text.W {
				cursors = append(cursors, cursorDraw{cx, y, i == v.primary})
			}
		}
		// mouse hit for the text row
		rowCopy := row
		cellsCopy := cells
		indentCopy := indent
		a.addHit(Rect{text.X, y, text.W, 1}, func(ev MouseEvent) bool {
			v.textMouse(ev, rowCopy, cellsCopy, indentCopy)
			return true
		})
		next, ok := v.nextRow(row)
		if !ok {
			// rows after the end of the document
			if y+1 < r.Y+r.H {
				endY := y + 1
				a.addHit(Rect{text.X, endY, text.W, r.Y + r.H - endY}, func(ev MouseEvent) bool {
					v.textMouseBelowEnd(ev)
					return true
				})
			}
			break
		}
		row = next
	}

	// sticky scroll overlay
	if len(sticky) > 0 {
		sbg := th.C("editorStickyScroll.background")
		for i, sl := range sticky {
			y := r.Y + i
			a.cv.Fill(Rect{r.X, y, r.W - mmW - sbW, 1}, ' ', styleFB(fgc, sbg))
			label := fmt.Sprint(sl + 1)
			if a.lineNumbers != "off" {
				digits := v.gutterW - 3
				a.cv.Text(r.X+digits-len(label), y, r.X+digits, label, styleFB(th.C("editorLineNumber.foreground"), sbg))
			}
			cells, _ := v.buildRow(sl, 0)
			x := text.X - v.leftCol
			spans := v.doc.SpansFor(sl)
			si := 0
			for _, c := range cells {
				if x >= text.X+text.W {
					break
				}
				st := styleFB(fgc, sbg)
				for si < len(spans) && spans[si].End <= c.col {
					si++
				}
				if si < len(spans) && spans[si].Start <= c.col && c.col >= 0 {
					sp := spans[si]
					if sp.Kind == syntax.KBracket {
						st = st.Foreground(th.C(fmt.Sprintf("editorBracketHighlight.foreground%d", int(sp.Depth)+1)))
					} else {
						st = th.Token(sp.Kind).Apply(st)
					}
				}
				if x >= text.X && !c.tab {
					a.cv.Set(x, y, c.ch, st)
				}
				x += c.width
			}
			target := sl
			a.addHit(Rect{r.X, y, r.W - mmW - sbW, 1}, func(ev MouseEvent) bool {
				if ev.Kind == MousePress {
					a.pushNav()
					v.SetCursor(buffer.Pos{Line: target, Col: v.buf().FirstNonWhitespace(target)})
					v.ensurePosVisible(v.Cursor(), true)
					a.focusEditor()
				}
				return true
			})
		}
		// shadow below sticky lines
		sy := r.Y + len(sticky) - 1
		for x := r.X; x < r.X+r.W-mmW-sbW; x++ {
			ch, comb, cst, _ := a.screen.GetContent(x, sy)
			_ = comb
			a.cv.Set(x, sy, ch, cst.Underline(tcell.UnderlineStyleSolid, th.OverColor("widget.shadow", sbg)))
		}
	}

	// cursors
	if focused {
		style := tcell.CursorStyleBlinkingBar
		switch a.settings.String("editor.cursorStyle", v.lang()) {
		case "block", "block-outline":
			style = tcell.CursorStyleBlinkingBlock
		case "underline", "underline-thin":
			style = tcell.CursorStyleBlinkingUnderline
		}
		if a.settings.String("editor.cursorBlinking", "") == "solid" {
			style++ // steady variants follow the blinking ones
		}
		for _, c := range cursors {
			if c.primary {
				if a.focus == FocusEditor && c.y >= r.Y+len(sticky) {
					a.setCursor(c.x, c.y, style)
				}
			} else {
				ch, _, cst, _ := a.screen.GetContent(c.x, c.y)
				fgCol := th.C("editorCursor.foreground")
				a.cv.Set(c.x, c.y, ch, cst.Background(fgCol).Foreground(th.C("editor.background")))
			}
		}
	}

	if mmW > 0 {
		v.drawMinimap(v.minimap)
	}
	v.drawScrollbar(Rect{r.X + r.W - sbW, r.Y, sbW, r.H})

	// widgets
	a.cv.clip = r
	if v.find != nil && v.find.visible {
		v.find.draw(r)
	}
	if v.completion != nil && v.completion.visible() {
		v.completion.draw()
	}
	if v.sig != nil {
		v.sig.draw()
	}
	if v.hover != nil {
		v.hover.draw()
	}
	if v.rename != nil {
		v.rename.draw()
	}
	if v.codeActions != nil {
		v.codeActions.draw()
	}
	if v.dirtyDiff != nil {
		v.dirtyDiff.draw()
	}
	a.cv.clip = Rect{0, 0, a.width, a.height}
}

func hasTag(tags []int, t int) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

func posInSpan(p buffer.Pos, s span) bool {
	return !p.Less(s.start) && p.Less(s.end)
}

func posInRange(p buffer.Pos, r buffer.Range) bool {
	return !p.Less(r.Start) && p.Less(r.End)
}

func diagCovers(d Diag, p buffer.Pos, b *buffer.Buffer) bool {
	r := d.Range
	if r.Empty() {
		// zero-width diagnostics mark the character at (or before) their position
		if r.Start.Col >= b.LineLen(r.Start.Line) && r.Start.Col > 0 {
			return p.Line == r.Start.Line && p.Col == r.Start.Col-1
		}
		return p == r.Start
	}
	return posInRange(p, r)
}

// screenPos returns the screen position of a buffer position, if visible.
func (v *EditorView) screenPos(p buffer.Pos) (int, int, bool) {
	if v.rect.Empty() {
		return 0, 0, false
	}
	target := v.rowOfPos(p)
	row := v.top()
	for y := v.rect.Y; y < v.rect.Y+v.rect.H; y++ {
		if row == target {
			cells, indent := v.buildRow(row.line, row.seg)
			x := v.rect.X - v.leftCol + indent
			for _, c := range cells {
				if c.col >= p.Col && c.col >= 0 {
					break
				}
				x += c.width
			}
			return x, y, x >= v.rect.X && x <= v.rect.X+v.rect.W
		}
		next, ok := v.nextRow(row)
		if !ok {
			break
		}
		row = next
	}
	return 0, 0, false
}

func (v *EditorView) rulerColumns() []int {
	var out []int
	for _, r := range v.app.settings.List("editor.rulers", v.lang()) {
		switch x := r.(type) {
		case float64:
			out = append(out, int(x))
		case map[string]any:
			if c, ok := x["column"].(float64); ok {
				out = append(out, int(c))
			}
		}
	}
	return out
}

// blankLineIndent guesses an indentation level for a blank line from its neighbors.
func (v *EditorView) blankLineIndent(line int) int {
	b := v.buf()
	prev, next := -1, -1
	for l := line - 1; l >= 0 && l > line-100; l-- {
		if i := v.indentLevel(l); i >= 0 {
			prev = i
			break
		}
	}
	for l := line + 1; l < b.LineCount() && l < line+100; l++ {
		if i := v.indentLevel(l); i >= 0 {
			next = i
			break
		}
	}
	if prev < 0 || next < 0 {
		return 0
	}
	return min(prev, next)
}

// activeIndentGuide returns the x and line span of the guide around the cursor.
func (v *EditorView) activeIndentGuide() (int, int, int) {
	b := v.buf()
	cur := v.Cursor().Line
	ind := v.indentLevel(cur)
	if ind < 0 {
		ind = v.blankLineIndent(cur)
	}
	if ind == 0 {
		return -1, 0, -1
	}
	start := -1
	for l := cur - 1; l >= 0 && l > cur-2000; l-- {
		li := v.indentLevel(l)
		if li >= 0 && li < ind {
			start = l
			break
		}
	}
	if start < 0 {
		return -1, 0, -1
	}
	sInd := v.indentLevel(start)
	end := cur
	for l := cur + 1; l < b.LineCount() && l < cur+5000; l++ {
		li := v.indentLevel(l)
		if li >= 0 && li <= sInd {
			break
		}
		end = l
	}
	return sInd, start + 1, end
}

// stickyChain returns the scope header lines that contain line, outermost first.
func (v *EditorView) stickyChain(line int) []int {
	b := v.buf()
	if line >= b.LineCount() || line <= 0 {
		return nil
	}
	ind := v.indentLevel(line)
	if ind < 0 {
		ind = v.blankLineIndent(line)
	} else if isClosingLine(b.LineString(line)) {
		// a closing bracket still belongs to the block it closes
		ind++
	}
	var chain []int
	for l := line - 1; l >= 0 && ind > 0 && l > line-5000; l-- {
		li := v.indentLevel(l)
		if li < 0 || li >= ind {
			continue
		}
		if f, ok := v.foldRangeAt(l); ok && (f.End >= line || f.End+1 == line && isClosingLine(b.LineString(line))) {
			chain = append([]int{l}, chain...)
		}
		ind = li
	}
	return chain
}

func isClosingLine(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && (strings.HasPrefix(t, "}") || strings.HasPrefix(t, ")") || strings.HasPrefix(t, "]") || t == "end" || strings.HasPrefix(t, "</"))
}

// computeSticky finds the scope header lines for sticky scroll.
func (v *EditorView) computeSticky() []int {
	if v.topLine == 0 && v.topSeg == 0 {
		return nil
	}
	maxN := v.app.settings.Int("editor.stickyScroll.maxLineCount", "", 5)
	maxN = min(maxN, max(v.rect.H/2-1, 0))
	// the sticky area covers k rows; the scopes shown belong to the first line below it
	var c []int
	for k := 0; k <= maxN; k++ {
		c = v.stickyChain(v.topLine + k)
		if len(c) > maxN {
			c = c[len(c)-maxN:]
		}
		if len(c) <= k {
			return c
		}
	}
	return c
}

// computeHighlights fills word occurrence highlights for the cursor word.
func (v *EditorView) computeHighlights() {
	a := v.app
	cur := v.Cursor()
	if v.highlights != nil && v.highlightsVersion == v.buf().Version() && v.highlightsPos == cur {
		return
	}
	v.highlightsVersion = v.buf().Version()
	v.highlightsPos = cur
	v.highlights = nil
	if len(v.sels) > 1 {
		v.highlights = []buffer.Range{}
		return
	}
	if v.HasSelection() {
		if !a.settings.Bool("editor.selectionHighlight", "") {
			return
		}
		sr := v.Primary().Range()
		if sr.Start.Line != sr.End.Line {
			return
		}
		text := v.buf().TextRange(sr)
		if strings.TrimSpace(text) == "" || len(text) > 200 {
			return
		}
		for _, m := range v.findAll(text, false, true) {
			if m != sr {
				v.highlights = append(v.highlights, m)
			}
		}
		if len(v.highlights) > 2000 {
			v.highlights = nil
		}
		return
	}
	if a.settings.String("editor.occurrencesHighlight", "") == "off" {
		return
	}
	// LSP document highlights are requested asynchronously; meanwhile use textual matches
	wr, ok := v.WordAt(cur)
	if !ok {
		v.highlights = []buffer.Range{}
		return
	}
	if a.requestDocumentHighlights(v, cur) {
		v.highlights = []buffer.Range{}
		return
	}
	word := v.buf().TextRange(wr)
	matches := v.findAll(word, true, true)
	if len(matches) > 1 && len(matches) < 2000 {
		v.highlights = matches
	} else {
		v.highlights = []buffer.Range{}
	}
}

//#region Minimap and scrollbar

var brailleBits = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

func (v *EditorView) drawMinimap(r Rect) {
	a := v.app
	th := a.theme
	b := v.buf()
	bgc := th.C("minimap.background")
	a.cv.Fill(r, ' ', styleFB(th.C("editor.foreground"), bgc))
	const linesPerRow = 4
	const charsPerDot = 2
	capacity := r.H * linesPerRow
	total := b.LineCount()
	// first line shown in the minimap scrolls proportionally with the editor
	first := 0
	if total > capacity {
		maxTop := max(v.maxTopIndex(), 1)
		first = int(float64(total-capacity) * float64(v.topLine) / float64(maxTop))
		first = clampInt(first, 0, total-capacity)
	}
	// slider
	visRows := v.rect.H
	sliderStart := (v.topLine - first) / linesPerRow
	sliderEnd := (v.topLine + visRows - first + linesPerRow - 1) / linesPerRow
	sliderBg := th.OverColor("minimapSlider.background", bgc)
	hover := r.Contains(a.mouseX, a.mouseY)
	if hover && th.Has("minimapSlider.hoverBackground") {
		sliderBg = th.OverColor("minimapSlider.hoverBackground", bgc)
	}
	if !hover && a.settings.String("editor.minimap.showSlider", "") != "always" {
		sliderBg = bgc
	}
	maxCol := r.W * 2 * charsPerDot
	ts := v.tabSize()
	for y := 0; y < r.H; y++ {
		rowBg := bgc
		if y >= sliderStart && y < sliderEnd {
			rowBg = sliderBg
		}
		for cx := 0; cx < r.W; cx++ {
			var bits rune
			var colorKind syntax.Kind = syntax.KText
			counts := map[syntax.Kind]int{}
			for dy := 0; dy < linesPerRow; dy++ {
				line := first + y*linesPerRow + dy
				if line >= total {
					break
				}
				l := b.Line(line)
				spans := v.doc.SpansFor(line)
				for dx := 0; dx < 2; dx++ {
					startX := (cx*2 + dx) * charsPerDot
					// map visual x to columns (approximate for tabs)
					filled := false
					var kind syntax.Kind = syntax.KText
					vx := 0
					for ci, ch := range l {
						w := 1
						if ch == '\t' {
							w = ts - vx%ts
						}
						if vx+w > startX && vx < startX+charsPerDot {
							if ch != ' ' && ch != '\t' {
								filled = true
								for _, sp := range spans {
									if ci >= sp.Start && ci < sp.End {
										kind = sp.Kind
										break
									}
								}
								break
							}
						}
						vx += w
						if vx >= startX+charsPerDot || vx > maxCol {
							break
						}
					}
					if filled {
						bits |= brailleBits[dy][dx]
						counts[kind]++
					}
				}
			}
			if bits == 0 {
				a.cv.Set(r.X+cx, r.Y+y, ' ', styleFB(bgc, rowBg))
				continue
			}
			best := 0
			for k, n := range counts {
				if n > best || (n == best && k > colorKind) {
					best = n
					colorKind = k
				}
			}
			fg := th.Token(colorKind).FG.Tcell()
			if colorKind == syntax.KBracket || colorKind == syntax.KPunctuation {
				fg = th.C("editor.foreground")
			}
			a.cv.Set(r.X+cx, r.Y+y, 0x2800+bits, styleFB(fg, rowBg).Dim(true))
		}
	}
	// decorations: selections and errors on the left edge
	for _, d := range v.doc.Diags {
		if d.Severity != lsp.SeverityError {
			continue
		}
		y := (d.Range.Start.Line - first) / linesPerRow
		if y >= 0 && y < r.H {
			a.cv.Set(r.X, r.Y+y, '▌', styleFB(th.OverColor("minimap.errorHighlight", bgc), bgc))
		}
	}
	a.addHit(r, func(ev MouseEvent) bool {
		if ev.Kind == MousePress || ev.Kind == MouseDrag {
			scrollTo := func(y int) {
				line := first + (y-r.Y)*linesPerRow - v.rect.H/2
				v.setTop(v.rowAtIndex(v.rowIndex(vpos{clampInt(line, 0, total-1), 0})))
				v.clampTop()
			}
			scrollTo(ev.Y)
			a.drag = func(ev MouseEvent) {
				if ev.Kind != MouseRelease {
					scrollTo(ev.Y)
				}
			}
		}
		if ev.Kind == MouseWheel {
			v.wheel(ev)
		}
		return true
	})
}

func (v *EditorView) clampTop() {
	idx := v.rowIndex(v.top())
	if m := v.maxTopIndex(); idx > m {
		v.setTop(v.rowAtIndex(m))
	}
}

func (v *EditorView) drawScrollbar(r Rect) {
	a := v.app
	th := a.theme
	bgc := th.C("editor.background")
	total := v.totalRows()
	h := r.H
	if h <= 0 {
		return
	}
	content := total + h - 1
	if !a.settings.Bool("editor.scrollBeyondLastLine", "") {
		content = max(total, h)
	}
	topIdx := v.rowIndex(v.top())
	sliderH := max(h*h/max(content, 1), 1)
	sliderY := 0
	if content > h {
		sliderY = int(float64(h-sliderH) * float64(topIdx) / float64(max(content-h, 1)))
	}
	hover := r.Contains(a.mouseX, a.mouseY) || v.scrollDragging
	sliderKey := "scrollbarSlider.background"
	if v.scrollDragging {
		sliderKey = "scrollbarSlider.activeBackground"
	} else if hover {
		sliderKey = "scrollbarSlider.hoverBackground"
	}
	showSlider := content > h
	for y := 0; y < h; y++ {
		bg := bgc
		if showSlider && y >= sliderY && y < sliderY+sliderH {
			bg = th.OverColor(sliderKey, bgc)
		}
		a.cv.Set(r.X, r.Y+y, ' ', styleFB(th.C("editor.foreground"), bg))
	}
	// overview ruler marks
	mark := func(line int, key string, ch rune) {
		idx := line
		if v.wrap || len(v.folds) > 0 {
			idx = v.rowIndex(v.rowOfPos(buffer.Pos{Line: line}))
		}
		y := r.Y + int(float64(idx)*float64(h)/float64(max(content, 1)))
		if y >= r.Y+h {
			y = r.Y + h - 1
		}
		_, _, cst, _ := a.screen.GetContent(r.X, y)
		a.cv.Set(r.X, y, ch, cst.Foreground(th.C(key)))
	}
	if v.doc.hasGitBase {
		for line, ch := range v.doc.gitChanges {
			key := "editorGutter.modifiedBackground"
			switch ch {
			case git.ChangeAdded:
				key = "editorGutter.addedBackground"
			case git.ChangeDeleted:
				key = "editorGutter.deletedBackground"
			}
			mark(line, key, '▏')
		}
	}
	if v.find != nil && v.find.visible && len(v.find.matches) < 5000 {
		for _, m := range v.find.matches {
			mark(m.Start.Line, "editor.findMatchBackground", '▐')
		}
	}
	for _, d := range v.doc.Diags {
		switch d.Severity {
		case lsp.SeverityError:
			mark(d.Range.Start.Line, "editorError.foreground", '▐')
		case lsp.SeverityWarning:
			mark(d.Range.Start.Line, "editorWarning.foreground", '▐')
		}
	}
	mark(v.Cursor().Line, "editorCursor.foreground", '─')
	a.addHit(r, func(ev MouseEvent) bool {
		switch ev.Kind {
		case MousePress:
			ry := ev.Y - r.Y
			if ry < sliderY || ry >= sliderY+sliderH {
				// jump so the slider centers on the click
				idx := int(float64(ry) * float64(content) / float64(h))
				v.setTop(v.rowAtIndex(max(idx-h/2, 0)))
				v.clampTop()
			}
			v.scrollDragging = true
			grab := ev.Y - (r.Y + sliderY)
			a.drag = func(ev MouseEvent) {
				if ev.Kind == MouseRelease {
					v.scrollDragging = false
					return
				}
				ny := ev.Y - r.Y - grab
				if h-sliderH > 0 {
					idx := int(float64(ny) * float64(max(content-h, 1)) / float64(h-sliderH))
					v.setTop(v.rowAtIndex(clampInt(idx, 0, max(content-h, 0))))
					v.clampTop()
				}
			}
		case MouseWheel:
			v.wheel(ev)
		}
		return true
	})
}

//#endregion

var colorLangs = map[string]bool{"css": true, "scss": true, "less": true, "html": true, "vue": true, "svelte": true, "json": true, "jsonc": true, "javascriptreact": true, "typescriptreact": true}

// colorDecorations finds hex colors on a line and returns swatches to draw before them.
func (v *EditorView) colorDecorations(line int) []inlay {
	if !colorLangs[v.lang()] {
		return nil
	}
	if b, ok := v.app.settings.Get("editor.colorDecorators", v.lang()).(bool); ok && !b {
		return nil
	}
	l := v.buf().Line(line)
	var out []inlay
	for i := 0; i < len(l); i++ {
		if l[i] != '#' {
			continue
		}
		j := i + 1
		for j < len(l) && j-i <= 8 && isHexRune(l[j]) {
			j++
		}
		n := j - i - 1
		if (n == 3 || n == 4 || n == 6 || n == 8) && (j == len(l) || !isWordRune(l[j])) {
			if c, ok := theme.ParseColor(string(l[i:j])); ok {
				out = append(out, inlay{col: i, label: "■", right: true, color: c.Tcell()})
			}
		}
		i = j - 1
	}
	return out
}

func isHexRune(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

func mergeInlays(a, b []inlay) []inlay {
	out := make([]inlay, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].col < out[j].col })
	return out
}
