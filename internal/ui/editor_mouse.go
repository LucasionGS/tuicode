package ui

import (
	"regexp"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
)

// posFromRowX maps a screen x within a rendered row to a buffer position.
func (v *EditorView) posFromRowX(row vpos, cells []rowCell, indent, x int) buffer.Pos {
	cx := v.rect.X - v.leftCol + indent
	b := v.buf()
	for _, c := range cells {
		if c.col < 0 {
			cx += c.width
			continue
		}
		if x < cx+(c.width+1)/2 {
			return buffer.Pos{Line: row.line, Col: c.col}
		}
		if x < cx+c.width {
			return buffer.Pos{Line: row.line, Col: c.col + 1}
		}
		cx += c.width
	}
	segs := v.segments(row.line)
	if row.seg+1 < len(segs) {
		return buffer.Pos{Line: row.line, Col: max(segs[row.seg+1]-1, segs[row.seg])}
	}
	return buffer.Pos{Line: row.line, Col: b.LineLen(row.line)}
}

func (v *EditorView) activate() {
	a := v.app
	for gi, g := range a.groups {
		if i := g.indexOf(v); i >= 0 {
			a.activeGroup = gi
			if g.active != i {
				g.setActive(i)
			}
		}
	}
	if a.focus != FocusEditor {
		a.setFocus(FocusEditor)
	}
}

func (v *EditorView) textMouse(ev MouseEvent, row vpos, cells []rowCell, indent int) {
	a := v.app
	switch ev.Kind {
	case MouseWheel:
		v.wheel(ev)
		return
	case MouseMove:
		p := v.posFromRowX(row, cells, indent, ev.X)
		v.mouseHover(p, ev)
		return
	case MouseRelease:
		return
	case MousePress:
	default:
		return
	}
	v.activate()
	p := v.posFromRowX(row, cells, indent, ev.X)
	v.closeWidgets(false)
	switch ev.Button {
	case tcell.Button2:
		// right click: keep selection if clicking inside it
		inside := false
		for _, s := range v.sels {
			if s.Range().Contains(p) && !s.Empty() {
				inside = true
			}
		}
		if !inside {
			v.SetCursor(p)
		}
		a.showEditorContextMenu(v, ev.X, ev.Y)
		return
	case tcell.Button3:
		// middle button drag makes a column selection; a plain middle click pastes the primary selection
		before := append([]buffer.Selection{}, v.sels...)
		v.boxAnchor = p
		v.boxSelect = true
		moved := false
		a.drag = func(ev MouseEvent) {
			if ev.Kind == MouseRelease {
				v.boxSelect = false
				if !moved && a.settings.Bool("editor.selectionClipboard", "") {
					v.SetSelections(before, 0)
					v.SetCursor(p)
					if text := a.getPrimarySelection(); text != "" {
						v.PasteText(text, nil, false)
					}
				}
				return
			}
			if q := v.posAtScreen(ev.X, ev.Y); q != p || moved {
				moved = true
				v.updateBoxSelection(q)
			}
		}
		return
	}
	alt := ev.Mods&tcell.ModAlt != 0
	shift := ev.Mods&tcell.ModShift != 0
	ctrl := ev.Mods&tcell.ModCtrl != 0
	if a.settings.String("editor.multiCursorModifier", "") == "ctrlCmd" {
		alt, ctrl = ctrl, alt
	}
	if ctrl && !shift && ev.Clicks == 1 {
		if url := v.urlAt(p); url != "" && a.settings.Bool("editor.links", "") {
			openExternal(url)
			a.setStatusMessage("Opening " + url)
			return
		}
		v.SetCursor(p)
		a.pushNav()
		a.goToDefinition(v, false)
		return
	}
	v.pushCursorHistory()
	v.dragWord, v.dragLine = false, false
	switch {
	case alt && shift:
		// column (box) selection
		v.boxSelect = true
		v.boxAnchor = v.Primary().Anchor
		v.updateBoxSelection(p)
	case alt && ev.Clicks == 1:
		v.sels = append(v.sels, buffer.Caret(p))
		v.prefX = append(v.prefX, -1)
		v.primary = len(v.sels) - 1
		v.normalize()
		for i, s := range v.sels {
			if s.Active == p {
				v.primary = i
			}
		}
	case shift:
		s := v.Primary()
		v.SetSelections([]buffer.Selection{{Anchor: s.Anchor, Active: p}}, 0)
	case ev.Clicks == 2:
		if wr, ok := v.WordAt(p); ok {
			v.SetSelections([]buffer.Selection{{Anchor: wr.Start, Active: wr.End}}, 0)
		} else {
			v.SetCursor(p)
		}
		v.dragWord = true
	case ev.Clicks >= 3:
		v.selectLines(p.Line, p.Line)
		v.dragLine = true
	default:
		v.SetCursor(p)
	}
	v.dragAnchor = v.Primary()
	v.dragging = true
	a.drag = func(ev MouseEvent) {
		if ev.Kind == MouseRelease {
			v.dragging = false
			v.boxSelect = false
			return
		}
		q := v.posAtScreen(ev.X, ev.Y)
		// auto-scroll when dragging past the edges
		if ev.Y < v.rect.Y {
			v.ScrollBy(-1)
		} else if ev.Y >= v.rect.Y+v.rect.H {
			v.ScrollBy(1)
		}
		if v.boxSelect {
			v.updateBoxSelection(q)
			return
		}
		anchor := v.dragAnchor
		switch {
		case v.dragLine:
			s, e := anchor.Range().Start.Line, q.Line
			if e < s {
				v.selectLinesReversed(e, anchor.Range().Start.Line)
			} else {
				v.selectLines(s, e)
			}
		case v.dragWord:
			wr, ok := v.WordAt(q)
			ar := anchor.Range()
			if !ok {
				wr = buffer.Range{Start: q, End: q}
			}
			if q.Less(ar.Start) {
				v.setPrimarySel(buffer.Selection{Anchor: ar.End, Active: wr.Start})
			} else {
				v.setPrimarySel(buffer.Selection{Anchor: ar.Start, Active: wr.End})
			}
		default:
			v.setPrimarySel(buffer.Selection{Anchor: anchor.Anchor, Active: q})
		}
	}
}

func (v *EditorView) setPrimarySel(s buffer.Selection) {
	if v.primary < len(v.sels) {
		v.sels[v.primary] = s
	} else {
		v.sels = []buffer.Selection{s}
		v.primary = 0
	}
	v.normalize()
}

func (v *EditorView) updateBoxSelection(p buffer.Pos) {
	a := v.boxAnchor
	ax := v.visualX(a.Line, a.Col)
	px := v.visualX(p.Line, p.Col)
	l0, l1 := a.Line, p.Line
	step := 1
	if l1 < l0 {
		step = -1
	}
	var sels []buffer.Selection
	b := v.buf()
	for l := l0; ; l += step {
		ac := v.colAtVisualX(l, 0, b.LineLen(l), ax, 0)
		pc := v.colAtVisualX(l, 0, b.LineLen(l), px, 0)
		if v.visualX(l, b.LineLen(l)) >= min(ax, px) || l == l0 {
			sels = append(sels, buffer.Selection{Anchor: buffer.Pos{Line: l, Col: ac}, Active: buffer.Pos{Line: l, Col: pc}})
		}
		if l == l1 {
			break
		}
	}
	v.SetSelections(sels, -1)
}

func (v *EditorView) selectLines(s, e int) {
	b := v.buf()
	end := buffer.Pos{Line: e + 1}
	if e+1 >= b.LineCount() {
		end = b.EndPos()
	}
	v.SetSelections([]buffer.Selection{{Anchor: buffer.Pos{Line: s}, Active: end}}, 0)
}

func (v *EditorView) selectLinesReversed(s, e int) {
	b := v.buf()
	end := buffer.Pos{Line: e + 1}
	if e+1 >= b.LineCount() {
		end = b.EndPos()
	}
	v.SetSelections([]buffer.Selection{{Anchor: end, Active: buffer.Pos{Line: s}}}, 0)
}

// posAtScreen maps any screen coordinate (possibly outside the text) to a position.
func (v *EditorView) posAtScreen(x, y int) buffer.Pos {
	row := v.top()
	if y < v.rect.Y {
		return buffer.Pos{Line: row.line, Col: 0}
	}
	for yy := v.rect.Y; yy < y; yy++ {
		next, ok := v.nextRow(row)
		if !ok {
			return v.buf().EndPos()
		}
		row = next
	}
	cells, indent := v.buildRow(row.line, row.seg)
	return v.posFromRowX(row, cells, indent, x)
}

func (v *EditorView) textMouseBelowEnd(ev MouseEvent) {
	switch ev.Kind {
	case MouseWheel:
		v.wheel(ev)
	case MousePress:
		v.activate()
		if ev.Button == tcell.Button1 {
			p := v.buf().EndPos()
			if ev.Mods&tcell.ModShift != 0 {
				v.SetSelections([]buffer.Selection{{Anchor: v.Primary().Anchor, Active: p}}, 0)
			} else {
				v.SetCursor(p)
			}
			v.dragAnchor = v.Primary()
			v.app.drag = func(ev MouseEvent) {
				if ev.Kind == MouseRelease {
					return
				}
				v.setPrimarySel(buffer.Selection{Anchor: v.dragAnchor.Anchor, Active: v.posAtScreen(ev.X, ev.Y)})
			}
		} else if ev.Button == tcell.Button2 {
			v.app.showEditorContextMenu(v, ev.X, ev.Y)
		}
	}
}

func (v *EditorView) gutterMouse(ev MouseEvent, line int, onFold bool) {
	a := v.app
	switch ev.Kind {
	case MouseWheel:
		v.wheel(ev)
		return
	case MousePress:
	default:
		return
	}
	v.activate()
	if onFold && ev.Button == tcell.Button1 {
		if _, folded := v.foldAt(line); folded {
			v.Unfold(line)
			return
		}
		if _, ok := v.foldRangeAt(line); ok {
			v.ToggleFold(line)
			return
		}
	}
	if ev.Button != tcell.Button1 {
		return
	}
	if ev.Mods&tcell.ModShift != 0 {
		s := v.Primary().Anchor.Line
		if line < s {
			v.selectLinesReversed(line, s)
		} else {
			v.selectLines(s, line)
		}
	} else {
		v.selectLines(line, line)
	}
	start := line
	a.drag = func(ev MouseEvent) {
		if ev.Kind == MouseRelease {
			return
		}
		q := v.posAtScreen(v.rect.X, ev.Y)
		if q.Line < start {
			v.selectLinesReversed(q.Line, start)
		} else {
			v.selectLines(start, q.Line)
		}
	}
}

// wheel scrolls the editor.
func (v *EditorView) wheel(ev MouseEvent) {
	sens := v.app.settings.Int("editor.mouseWheelScrollSensitivity", "", 1)
	if sens < 1 {
		sens = 1
	}
	if ev.WheelDX != 0 && !v.wrap {
		v.leftCol = max(v.leftCol+ev.WheelDX*4*sens, 0)
		return
	}
	n := 3 * sens
	if ev.Mods&tcell.ModAlt != 0 {
		n *= 5
	}
	v.ScrollBy(ev.WheelDY * n)
	v.hideHover()
}

// mouseHover schedules a hover request for a position.
func (v *EditorView) mouseHover(p buffer.Pos, ev MouseEvent) {
	a := v.app
	if ev.Mods&tcell.ModCtrl != 0 {
		if wr, ok := v.WordAt(p); ok {
			v.linkRange = &wr
		}
	} else {
		v.linkRange = nil
	}
	if !a.settings.Bool("editor.hover.enabled", "") {
		return
	}
	if v.hover != nil && v.hover.mouse && v.hover.contains(ev.X, ev.Y) {
		return
	}
	if v.hover != nil && v.hover.mouse && v.hover.anchor != p {
		if wr, ok := v.WordAt(v.hover.anchor); !ok || !wr.Contains(p) {
			v.hideHover()
		}
	}
	if a.hoverTimer != nil {
		a.hoverTimer.Stop()
	}
	delay := time.Duration(a.settings.Int("editor.hover.delay", "", 300)) * time.Millisecond
	a.hoverTimer = time.AfterFunc(delay, func() {
		a.Post(func() {
			if a.mouseX != ev.X || a.mouseY != ev.Y {
				return
			}
			if a.ActiveView() != v && !v.isVisible() {
				return
			}
			a.showHover(v, p, true)
		})
	})
}

func (v *EditorView) isVisible() bool {
	for _, g := range v.app.groups {
		if g.Active() == v {
			return true
		}
	}
	return false
}

// closeWidgets hides transient editor widgets.
func (v *EditorView) closeWidgets(keepFind bool) {
	if v.completion != nil {
		v.completion.close()
	}
	v.sig = nil
	v.hover = nil
	v.codeActions = nil
	v.dirtyDiff = nil
	if v.rename != nil {
		v.rename = nil
		v.app.setFocus(FocusEditor)
	}
}

func (v *EditorView) hideHover() {
	v.hover = nil
}

// handleWidgetKey lets editor widgets (suggest, parameter hints, snippets) intercept keys.
func (v *EditorView) handleWidgetKey(key, text string, isText bool) bool {
	if v.special != nil {
		return v.special.HandleKey(key, text, isText)
	}
	if v.codeActions != nil {
		return v.codeActions.handleKey(key)
	}
	if v.peek != nil && v.peek.handleKey(key) {
		return true
	}
	if v.dirtyDiff != nil && key == "escape" {
		v.dirtyDiff = nil
		return true
	}
	if v.completion != nil && v.completion.visible() {
		if v.completion.handleKey(key) {
			return true
		}
	}
	if v.hover != nil && !v.hover.mouse {
		switch key {
		case "escape":
			v.hover = nil
			return true
		case "up", "down", "pageup", "pagedown":
			if v.hover.focused {
				v.hover.scroll(key)
				return true
			}
		}
		if key != "ctrl+k" && key != "ctrl+i" {
			v.hover = nil
		}
	} else if v.hover != nil && key == "escape" {
		v.hover = nil
		return true
	}
	if v.sig != nil {
		switch key {
		case "escape":
			v.sig = nil
			return true
		case "up", "alt+up":
			if len(v.sig.help.Signatures) > 1 {
				v.sig.cycle(-1)
				return true
			}
		case "down", "alt+down":
			if len(v.sig.help.Signatures) > 1 {
				v.sig.cycle(1)
				return true
			}
		}
	}
	if v.snippet != nil {
		switch key {
		case "tab":
			if !(v.completion != nil && v.completion.visible()) {
				v.snippet.next(v)
				return true
			}
		case "shift+tab":
			v.snippet.prev(v)
			return true
		case "escape":
			v.snippet = nil
			return true
		}
	}
	return false
}

func (a *App) showEditorContextMenu(v *EditorView, x, y int) {
	items := []MenuItem{
		{Label: "Go to Definition", Command: "editor.action.revealDefinition"},
		{Label: "Go to Type Definition", Command: "editor.action.goToTypeDefinition"},
		{Label: "Go to Implementations", Command: "editor.action.goToImplementation"},
		{Label: "Go to References", Command: "editor.action.goToReferences"},
		{Label: "Peek", Submenu: []MenuItem{
			{Label: "Peek Definition", Command: "editor.action.peekDefinition"},
			{Label: "Peek References", Command: "editor.action.referenceSearch.trigger"},
		}},
		{Separator: true},
		{Label: "Find All References", Command: "references-view.findReferences"},
		{Label: "Show Call Hierarchy", Command: "references-view.showCallHierarchy"},
		{Label: "Show Type Hierarchy", Command: "references-view.showTypeHierarchy"},
		{Separator: true},
		{Label: "Rename Symbol", Command: "editor.action.rename"},
		{Label: "Change All Occurrences", Command: "editor.action.changeAll"},
		{Label: "Format Document", Command: "editor.action.formatDocument"},
		{Label: "Refactor...", Command: "editor.action.refactor"},
		{Label: "Source Action...", Command: "editor.action.sourceAction"},
		{Separator: true},
		{Label: "Cut", Command: "editor.action.clipboardCutAction"},
		{Label: "Copy", Command: "editor.action.clipboardCopyAction"},
		{Label: "Paste", Command: "editor.action.clipboardPasteAction"},
		{Separator: true},
		{Label: "Command Palette...", Command: "workbench.action.showCommands"},
	}
	if a.debug != nil && a.debug.state == "stopped" {
		items = append([]MenuItem{
			{Label: "Run to Cursor", Command: "editor.debug.action.runToCursor"},
			{Label: "Evaluate in Debug Console", Command: "editor.debug.action.selectionToRepl"},
			{Label: "Add to Watch", Command: "editor.debug.action.selectionToWatch"},
			{Separator: true},
		}, items...)
	}
	a.openContextMenu(x, y, items)
}

var urlRe = regexp.MustCompile(`https?://[^\s"'<>()\[\]{}` + "`" + `]+`)

// urlAt returns the URL under p, if any.
func (v *EditorView) urlAt(p buffer.Pos) string {
	line := v.buf().LineString(p.Line)
	for _, m := range urlRe.FindAllStringIndex(line, -1) {
		s := len([]rune(line[:m[0]]))
		e := len([]rune(line[:m[1]]))
		if p.Col >= s && p.Col < e {
			return strings.TrimRight(line[m[0]:m[1]], ".,;:")
		}
	}
	return ""
}
