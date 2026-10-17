package ui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
)

// FindWidget is the editor's find/replace overlay.
type FindWidget struct {
	v           *EditorView
	visible     bool
	replaceMode bool
	input       TextInput
	replace     TextInput
	focus       int
	caseSens    bool
	wholeWord   bool
	regex       bool
	preserve    bool
	inSelection bool
	selRange    buffer.Range
	matches     []buffer.Range
	current     int
	version     int
	query       string
	err         string
	rect        Rect
}

func (v *EditorView) ensureFind() *FindWidget {
	if v.find == nil {
		a := v.app
		f := &FindWidget{v: v, current: -1, version: -1}
		f.input = newTextInput(a, "Find")
		f.replace = newTextInput(a, "Replace")
		f.input.OnChange = func() { f.version = -1; f.update(true) }
		f.replace.OnChange = func() {}
		// share history across editors
		f.input.history = a.findHistory
		v.find = f
	}
	return v.find
}

func (a *App) openFind(replace bool) {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		if a.focus == FocusPanel && a.panelView == "terminal" {
			a.terminals.find()
		}
		return
	}
	f := v.ensureFind()
	f.visible = true
	if replace {
		f.replaceMode = true
	}
	// seed from selection or word at cursor
	sel := v.Primary()
	seed := ""
	if !sel.Empty() {
		r := sel.Range()
		if r.Start.Line == r.End.Line {
			seed = v.buf().TextRange(r)
		} else if a.settings.String("editor.find.autoFindInSelection", "") == "multiline" {
			f.inSelection = true
			f.selRange = r
		}
	} else if wr, ok := v.WordAt(sel.Active); ok {
		seed = v.buf().TextRange(wr)
	}
	if seed != "" {
		if f.regex {
			seed = regexp.QuoteMeta(seed)
		}
		f.input.SetText(seed)
	}
	f.input.SelectAll()
	f.focus = 0
	if replace && seed != "" && f.input.Text() != "" {
		f.focus = 1
		f.replace.SelectAll()
	}
	f.version = -1
	f.update(false)
	a.setFocus(FocusWidget)
}

func (a *App) closeFind() {
	v := a.ActiveView()
	if v == nil || v.find == nil {
		return
	}
	v.find.visible = false
	v.find.inSelection = false
	a.findHistory = v.find.input.history
	if a.focus == FocusWidget {
		a.setFocus(FocusEditor)
	}
}

func (a *App) findCycleFocus() {
	v := a.ActiveView()
	if v == nil || v.find == nil || !v.find.visible {
		return
	}
	f := v.find
	if f.replaceMode {
		f.focus = 1 - f.focus
	}
	a.setFocus(FocusWidget)
}

func (a *App) findToggle(what string) {
	v := a.ActiveView()
	if v == nil {
		return
	}
	f := v.ensureFind()
	switch what {
	case "case":
		f.caseSens = !f.caseSens
	case "word":
		f.wholeWord = !f.wholeWord
	case "regex":
		f.regex = !f.regex
	case "preserve":
		f.preserve = !f.preserve
	case "selection":
		f.inSelection = !f.inSelection
		if f.inSelection {
			f.selRange = v.Primary().Range()
			if f.selRange.Empty() {
				f.selRange = buffer.Range{Start: buffer.Pos{Line: v.Cursor().Line}, End: buffer.Pos{Line: v.Cursor().Line, Col: v.buf().LineLen(v.Cursor().Line)}}
			}
		}
	}
	f.version = -1
	f.update(false)
}

// update recomputes matches. When jump is set, the current match follows the cursor.
func (f *FindWidget) update(jump bool) {
	v := f.v
	b := v.buf()
	q := f.input.Text()
	if f.version == b.Version() && q == f.query {
		return
	}
	f.version = b.Version()
	f.query = q
	f.err = ""
	f.matches = f.matches[:0]
	if q == "" {
		f.current = -1
		return
	}
	if f.regex || f.wholeWord && strings.ContainsAny(q, " ") {
		pat := q
		if !f.regex {
			pat = regexp.QuoteMeta(q)
		}
		if f.wholeWord {
			pat = `\b` + pat + `\b`
		}
		if !f.caseSens {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile("(?m)" + pat)
		if err != nil {
			f.err = "Invalid regular expression"
			return
		}
		f.matches = regexMatches(b, re, 20000)
	} else {
		f.matches = v.findAll(q, f.wholeWord, f.caseSens)
		if len(f.matches) > 20000 {
			f.matches = f.matches[:20000]
		}
	}
	if f.inSelection {
		out := f.matches[:0]
		for _, m := range f.matches {
			if !m.Start.Less(f.selRange.Start) && !f.selRange.End.Less(m.End) {
				out = append(out, m)
			}
		}
		f.matches = out
	}
	f.current = -1
	if len(f.matches) == 0 {
		return
	}
	// nearest match at or after the selection start
	from := v.Primary().Range().Start
	f.current = 0
	for i, m := range f.matches {
		if !m.Start.Less(from) {
			f.current = i
			break
		}
	}
	if jump {
		f.selectCurrent()
	}
}

func (f *FindWidget) selectCurrent() {
	if f.current < 0 || f.current >= len(f.matches) {
		return
	}
	v := f.v
	m := f.matches[f.current]
	v.SetSelections([]buffer.Selection{{Anchor: m.Start, Active: m.End}}, 0)
	_, _, visible := v.screenPos(m.Start)
	v.ensurePosVisible(m.Start, !visible)
	// keep the match out from under the widget
	if _, y, ok := v.screenPos(m.Start); ok && y < v.rect.Y+3 && f.visible {
		v.ScrollBy(-3)
	}
}

// regexMatches finds regex matches over the whole document.
func regexMatches(b *buffer.Buffer, re *regexp.Regexp, limit int) []buffer.Range {
	text := b.Text()
	lineStarts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	toPos := func(off int) buffer.Pos {
		// binary search the line
		lo, hi := 0, len(lineStarts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if lineStarts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		col := len([]rune(text[lineStarts[lo]:off]))
		return buffer.Pos{Line: lo, Col: col}
	}
	var out []buffer.Range
	for _, m := range re.FindAllStringIndex(text, limit) {
		if m[0] == m[1] && len(out) > 0 && out[len(out)-1].Start == toPos(m[0]) {
			continue
		}
		out = append(out, buffer.Range{Start: toPos(m[0]), End: toPos(m[1])})
	}
	return out
}

func (a *App) findMove(dir int) {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		return
	}
	f := v.ensureFind()
	if f.input.Text() == "" {
		a.openFind(false)
		return
	}
	f.update(false)
	if len(f.matches) == 0 {
		return
	}
	f.input.AddHistory(f.input.Text())
	cur := v.Primary().Range()
	if dir > 0 {
		idx := 0
		for i, m := range f.matches {
			if cur.End.Less(m.Start) || (m.Start == cur.End && !cur.Empty()) || (cur.Empty() && !m.Start.Less(cur.Start)) {
				idx = i
				break
			}
			if i == len(f.matches)-1 {
				idx = 0
			}
		}
		if f.current >= 0 && f.current < len(f.matches) && f.matches[f.current] == cur {
			idx = (f.current + 1) % len(f.matches)
		}
		f.current = idx
	} else {
		idx := len(f.matches) - 1
		for i := len(f.matches) - 1; i >= 0; i-- {
			if f.matches[i].Start.Less(cur.Start) {
				idx = i
				break
			}
		}
		if f.current >= 0 && f.current < len(f.matches) && f.matches[f.current] == cur {
			idx = (f.current - 1 + len(f.matches)) % len(f.matches)
		}
		f.current = idx
	}
	f.selectCurrent()
}

func (a *App) findSelection(dir int) {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		return
	}
	f := v.ensureFind()
	sel := v.Primary()
	text := ""
	if !sel.Empty() {
		text = v.buf().TextRange(sel.Range())
	} else if wr, ok := v.WordAt(sel.Active); ok {
		text = v.buf().TextRange(wr)
		v.SetSelections([]buffer.Selection{{Anchor: wr.Start, Active: wr.End}}, 0)
	}
	if text == "" {
		return
	}
	f.input.SetText(text)
	f.version = -1
	f.update(false)
	a.findMove(dir)
}

func (a *App) findSelectAll() {
	v := a.ActiveView()
	if v == nil || v.find == nil {
		return
	}
	f := v.find
	f.update(false)
	if len(f.matches) == 0 {
		return
	}
	sels := make([]buffer.Selection, len(f.matches))
	for i, m := range f.matches {
		sels[i] = buffer.Selection{Anchor: m.Start, Active: m.End}
	}
	v.SetSelections(sels, max(f.current, 0))
	a.closeFind()
	a.setFocus(FocusEditor)
}

// replacementFor computes the replacement text for a match.
func (f *FindWidget) replacementFor(m buffer.Range) string {
	b := f.v.buf()
	matched := b.TextRange(m)
	repl := f.replace.Text()
	if f.regex {
		pat := f.input.Text()
		if !f.caseSens {
			pat = "(?i)" + pat
		}
		if re, err := regexp.Compile(pat); err == nil {
			// VS Code uses $1 and \n escapes
			repl = strings.ReplaceAll(repl, `\n`, "\n")
			repl = strings.ReplaceAll(repl, `\t`, "\t")
			repl = re.ReplaceAllString(matched, repl)
		}
	}
	if f.preserve {
		repl = preserveCase(matched, repl)
	}
	return repl
}

func preserveCase(orig, repl string) string {
	if orig == strings.ToUpper(orig) && strings.ToUpper(orig) != strings.ToLower(orig) {
		return strings.ToUpper(repl)
	}
	rs := []rune(orig)
	if len(rs) > 0 && unicode.IsUpper(rs[0]) {
		rr := []rune(repl)
		if len(rr) > 0 {
			rr[0] = unicode.ToUpper(rr[0])
		}
		return string(rr)
	}
	return repl
}

func (a *App) findReplace(all bool) {
	v := a.ActiveView()
	if v == nil || v.find == nil {
		return
	}
	f := v.find
	f.update(false)
	if len(f.matches) == 0 {
		return
	}
	f.input.AddHistory(f.input.Text())
	if all {
		edits := make([]buffer.Edit, len(f.matches))
		for i, m := range f.matches {
			edits[i] = buffer.Edit{Range: m, Text: f.replacementFor(m)}
		}
		n := len(edits)
		v.buf().CloseUndoGroup()
		v.applying = true
		v.buf().ApplyEdits(edits, v.sels, "replaceAll")
		v.applying = false
		v.buf().CloseUndoGroup()
		v.SetSelections(v.sels, v.primary)
		a.setStatusMessage(fmt.Sprintf("Replaced %d occurrences", n))
		f.version = -1
		f.update(false)
		return
	}
	// replace the current match if it is selected, then move to the next
	cur := v.Primary().Range()
	idx := -1
	for i, m := range f.matches {
		if m == cur {
			idx = i
		}
	}
	if idx < 0 {
		a.findMove(1)
		return
	}
	m := f.matches[idx]
	text := f.replacementFor(m)
	v.applyEdits([]buffer.Edit{{Range: m, Text: text}}, "", func(res []buffer.Range) []buffer.Selection {
		return []buffer.Selection{buffer.Caret(res[0].End)}
	})
	f.version = -1
	f.update(false)
	if len(f.matches) > 0 {
		from := v.Cursor()
		f.current = 0
		for i, mm := range f.matches {
			if !mm.Start.Less(from) {
				f.current = i
				break
			}
		}
		f.selectCurrent()
	}
}

func (f *FindWidget) draw(er Rect) {
	v := f.v
	a := v.app
	th := a.theme
	f.update(false)
	w := min(max(er.W/2, 44), er.W-2)
	if w < 20 {
		return
	}
	h := 1
	if f.replaceMode {
		h = 2
	}
	x := er.X + er.W - w - 2
	y := er.Y
	f.rect = Rect{x, y, w, h}
	bgc := th.C("editorWidget.background")
	fgc := th.C("editorWidget.foreground")
	base := styleFB(fgc, bgc)
	a.cv.Fill(f.rect, ' ', base)
	// toggle replace chevron
	chev := a.Icon(IcoChevronRight)
	if f.replaceMode {
		chev = a.Icon(IcoChevronDown)
	}
	a.cv.Text(x, y, x+1, chev, base)
	a.addHit(Rect{x, y, 1, h}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			f.replaceMode = !f.replaceMode
			if !f.replaceMode {
				f.focus = 0
			}
		}
		return true
	})
	// toggles inside the find input
	toggles := []struct {
		icon string
		on   bool
		what string
		tip  string
	}{
		{a.Icon(IcoCaseSensitive), f.caseSens, "case", "Match Case (Alt+C)"},
		{a.Icon(IcoWholeWord), f.wholeWord, "word", "Match Whole Word (Alt+W)"},
		{a.Icon(IcoRegex), f.regex, "regex", "Use Regular Expression (Alt+R)"},
	}
	togW := 0
	for _, t := range toggles {
		togW += StrWidth(t.icon) + 1
	}
	count := "No results"
	if f.err != "" {
		count = f.err
	} else if f.input.Text() == "" {
		count = "No results"
	} else if len(f.matches) > 0 {
		cur := "?"
		if f.current >= 0 {
			// show the index only when the current match is selected
			cur = fmt.Sprint(f.current + 1)
		}
		count = fmt.Sprintf("%s of %d", cur, len(f.matches))
		if len(f.matches) >= 20000 {
			count = fmt.Sprintf("%s of 20000+", cur)
		}
	}
	countW := 11
	btnW := 8
	inputW := w - 2 - togW - countW - btnW
	if inputW < 8 {
		inputW = 8
	}
	ibg := th.C("input.background")
	ist := styleFB(th.C("input.foreground"), ibg)
	phSt := styleFB(th.C("input.placeholderForeground"), ibg)
	ix := x + 2
	focusedFind := a.focus == FocusWidget && f.focus == 0 && a.ActiveView() == v
	a.cv.Fill(Rect{ix, y, inputW + togW, 1}, ' ', ist)
	f.input.Draw(ix+1, y, inputW-1, ist, phSt, focusedFind)
	a.addHit(Rect{ix, y, inputW, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			f.focus = 0
			a.setFocus(FocusWidget)
		}
		return false
	})
	if f.err != "" || (len(f.matches) == 0 && f.input.Text() != "") {
		a.cv.Set(ix, y, '▏', styleFB(th.C("inputValidation.errorBorder"), ibg))
	} else if focusedFind {
		a.cv.Set(ix, y, '▏', styleFB(th.C("focusBorder"), ibg))
	}
	tx := ix + inputW
	for _, t := range toggles {
		st := ist
		if t.on {
			st = styleFB(th.C("inputOption.activeForeground"), th.OverColor("inputOption.activeBackground", ibg))
		}
		tw := StrWidth(t.icon)
		a.cv.Text(tx, y, tx+tw, t.icon, st)
		what := t.what
		tip := t.tip
		a.addHit(Rect{tx, y, tw, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.findToggle(what)
			case MouseMove:
				a.showTooltipAt(ev.X, y+h, tip)
			}
			return true
		})
		tx += tw + 1
	}
	cst := base
	if len(f.matches) == 0 && f.input.Text() != "" {
		cst = base.Foreground(th.C("errorForeground"))
	}
	a.cv.Text(tx+1, y, tx+1+countW, count, cst)
	bx := x + w - btnW
	buttons := []struct {
		icon string
		run  func()
		tip  string
	}{
		{a.Icon(IcoArrowUp), func() { a.findMove(-1) }, "Previous Match (Shift+Enter)"},
		{a.Icon(IcoArrowDown), func() { a.findMove(1) }, "Next Match (Enter)"},
		{a.Icon(IcoSelection), func() { a.findToggle("selection") }, "Find in Selection (Alt+L)"},
		{a.Icon(IcoClose), func() { a.closeFind() }, "Close (Escape)"},
	}
	for i, btn := range buttons {
		st := base
		if i == 2 && f.inSelection {
			st = styleFB(th.C("inputOption.activeForeground"), th.OverColor("inputOption.activeBackground", bgc))
		}
		bw := StrWidth(btn.icon)
		a.cv.Text(bx, y, bx+bw, btn.icon, st)
		run := btn.run
		tip := btn.tip
		a.addHit(Rect{bx, y, bw, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				run()
			case MouseMove:
				a.showTooltipAt(ev.X, y+h, tip)
			}
			return true
		})
		bx += bw + 1
	}
	if f.replaceMode {
		ry := y + 1
		focusedRepl := a.focus == FocusWidget && f.focus == 1 && a.ActiveView() == v
		a.cv.Fill(Rect{ix, ry, inputW + togW, 1}, ' ', ist)
		f.replace.Draw(ix+1, ry, inputW-1, ist, phSt, focusedRepl)
		if focusedRepl {
			a.cv.Set(ix, ry, '▏', styleFB(th.C("focusBorder"), ibg))
		}
		a.addHit(Rect{ix, ry, inputW, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				f.focus = 1
				a.setFocus(FocusWidget)
			}
			return false
		})
		pst := ist
		if f.preserve {
			pst = styleFB(th.C("inputOption.activeForeground"), th.OverColor("inputOption.activeBackground", ibg))
		}
		pIcon := a.Icon(IcoPreserveCase)
		a.cv.Text(ix+inputW, ry, ix+inputW+StrWidth(pIcon), pIcon, pst)
		a.addHit(Rect{ix + inputW, ry, StrWidth(pIcon), 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.findToggle("preserve")
			}
			return true
		})
		rbx := ix + inputW + togW + 1
		rIcon, raIcon := a.Icon(IcoReplace), a.Icon(IcoReplaceAll)
		a.cv.Text(rbx, ry, rbx+2, rIcon, base)
		a.addHit(Rect{rbx, ry, StrWidth(rIcon), 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.findReplace(false)
			case MouseMove:
				a.showTooltipAt(ev.X, ry+1, "Replace (Enter)")
			}
			return true
		})
		a.cv.Text(rbx+3, ry, rbx+5, raIcon, base)
		a.addHit(Rect{rbx + 3, ry, StrWidth(raIcon), 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.findReplace(true)
			case MouseMove:
				a.showTooltipAt(ev.X, ry+1, "Replace All (Ctrl+Alt+Enter)")
			}
			return true
		})
	}
	// shadow line below the widget
	sy := y + h
	if sy < er.Y+er.H {
		for sx := x; sx < x+w; sx++ {
			ch, _, cst, _ := a.screen.GetContent(sx, sy)
			a.cv.Set(sx, sy, ch, cst.Underline(false).Background(blendTcell(bgOf(cst), th.OverColor("widget.shadow", bgOf(cst)))))
		}
	}
	a.addHit(f.rect, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			a.setFocus(FocusWidget)
		}
		return true
	})
	_ = tcell.StyleDefault
}
