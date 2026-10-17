package ui

import (
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/fuzzy"
)

// QuickItem is one entry in a quick pick.
type QuickItem struct {
	Label       string
	Description string
	Detail      string
	Icon        string
	IconColor   tcell.Color
	Keybinding  string
	Group       string // shown as a separator label above the item
	Data        any
	Positions   []int
	score       int
	AlwaysShow  bool
	Buttons     []QuickButton
}

// QuickButton is an inline action on an item.
type QuickButton struct {
	Icon    string
	Tooltip string
	Run     func(it *QuickItem)
}

// QuickPick configures a quick input.
type QuickPick struct {
	Title            string
	Placeholder      string
	Items            []QuickItem
	Value            string
	OnAccept         func(it *QuickItem, query string)
	OnChange         func(query string)
	OnSelect         func(it *QuickItem)
	OnCancel         func()
	NoFilter         bool
	MatchDescription bool
	KeepOrder        bool
	// Mode is used by quick access to know which provider is active.
	Mode string
}

// QuickInput is the command palette / quick open widget.
type QuickInput struct {
	app      *App
	input    TextInput
	pick     *QuickPick
	filtered []*QuickItem
	selected int
	scroll   int
	rect     Rect
	busy     bool
	message  string
}

func (a *App) openQuick(p *QuickPick) *QuickInput {
	q := &QuickInput{app: a, pick: p}
	q.input = newTextInput(a, p.Placeholder)
	q.input.SetText(p.Value)
	q.input.OnChange = q.onChange
	a.quick = q
	if a.focus != FocusQuick {
		a.prevFocus = a.focus
	}
	a.focus = FocusQuick
	a.closeMenus()
	if p.OnChange != nil {
		p.OnChange(q.input.Text())
	}
	q.refilter()
	return q
}

func (a *App) closeMenus() {
	a.ctxMenu = nil
	a.menu.close()
}

// closeQuick hides the quick input and restores focus.
func (a *App) closeQuick(cancelled bool) {
	q := a.quick
	if q == nil {
		return
	}
	a.quick = nil
	a.focus = a.prevFocus
	if a.focus == FocusQuick {
		a.focus = FocusEditor
	}
	if cancelled && q.pick.OnCancel != nil {
		q.pick.OnCancel()
	}
}

func (q *QuickInput) onChange() {
	if q.pick.OnChange != nil {
		q.pick.OnChange(q.input.Text())
	}
	q.refilter()
}

// setItems replaces the items (used by dynamic providers).
func (q *QuickInput) setItems(items []QuickItem) {
	q.pick.Items = items
	q.refilter()
}

func (q *QuickInput) query() string {
	text := q.input.Text()
	if q.pick.Mode != "" && q.pick.Mode != "files" {
		// strip the mode prefix
		for _, p := range []string{">", "@:", "@", "#", ":", "?", "%", "term "} {
			if strings.HasPrefix(text, p) && quickPrefixMode(p) == q.pick.Mode {
				return strings.TrimSpace(text[len(p):])
			}
		}
	}
	return strings.TrimSpace(text)
}

func (q *QuickInput) refilter() {
	query := q.query()
	q.filtered = q.filtered[:0]
	if q.pick.NoFilter || query == "" {
		for i := range q.pick.Items {
			it := &q.pick.Items[i]
			it.Positions = nil
			q.filtered = append(q.filtered, it)
		}
	} else {
		needle := strings.ReplaceAll(query, " ", "")
		for i := range q.pick.Items {
			it := &q.pick.Items[i]
			s, pos, ok := fuzzy.Match(needle, it.Label)
			if !ok && q.pick.MatchDescription {
				if s2, _, ok2 := fuzzy.MatchPath(needle, it.Description+"/"+it.Label); ok2 {
					s, pos, ok = s2-5, nil, true
				}
			}
			if !ok && !it.AlwaysShow {
				continue
			}
			it.score = s
			it.Positions = pos
			q.filtered = append(q.filtered, it)
		}
		if !q.pick.KeepOrder {
			sort.SliceStable(q.filtered, func(i, j int) bool { return q.filtered[i].score > q.filtered[j].score })
		}
	}
	q.selected = 0
	q.scroll = 0
	q.notifySelect()
}

func (q *QuickInput) notifySelect() {
	if q.pick.OnSelect != nil && q.selected < len(q.filtered) {
		q.pick.OnSelect(q.filtered[q.selected])
	}
}

func (q *QuickInput) accept() {
	var it *QuickItem
	if q.selected < len(q.filtered) {
		it = q.filtered[q.selected]
	}
	query := q.input.Text()
	pick := q.pick
	q.app.closeQuick(false)
	if pick.OnAccept != nil {
		pick.OnAccept(it, query)
	}
}

func (q *QuickInput) handleKey(key, text string, isText bool) bool {
	maxRows := q.maxRows()
	switch key {
	case "escape":
		q.app.closeQuick(true)
		return true
	case "enter":
		q.accept()
		return true
	case "down", "ctrl+n", "ctrl+tab":
		if len(q.filtered) > 0 {
			q.selected = (q.selected + 1) % len(q.filtered)
			q.notifySelect()
		}
		return true
	case "up", "ctrl+shift+n", "ctrl+shift+tab":
		if len(q.filtered) > 0 {
			q.selected = (q.selected - 1 + len(q.filtered)) % len(q.filtered)
			q.notifySelect()
		}
		return true
	case "pagedown":
		q.selected = min(q.selected+maxRows, max(len(q.filtered)-1, 0))
		q.notifySelect()
		return true
	case "pageup":
		q.selected = max(q.selected-maxRows, 0)
		q.notifySelect()
		return true
	case "ctrl+end":
		q.selected = max(len(q.filtered)-1, 0)
		q.notifySelect()
		return true
	case "ctrl+home":
		q.selected = 0
		q.notifySelect()
		return true
	case "tab":
		return true
	}
	// toggling the same picker closes it
	if (key == "ctrl+shift+p" || key == "f1") && q.pick.Mode == "commands" {
		q.app.closeQuick(true)
		return true
	}
	if key == "ctrl+p" && q.pick.Mode == "files" {
		// ctrl+p again moves down like VS Code
		if len(q.filtered) > 0 {
			q.selected = (q.selected + 1) % len(q.filtered)
		}
		return true
	}
	if q.input.HandleKey(key, text, isText) {
		return true
	}
	return false
}

func (q *QuickInput) maxRows() int {
	return clampInt(q.app.height/2, 5, 20)
}

func (q *QuickInput) draw() {
	mark := len(q.app.hits)
	a := q.app
	th := a.theme
	w := clampInt(a.width*6/10, min(60, a.width-2), 110)
	if w > a.width-2 {
		w = a.width - 2
	}
	x := (a.width - w) / 2
	y := 0
	if !a.rTitle.Empty() {
		y = 1
	}
	rows := min(len(q.filtered), q.maxRows())
	// count group separators
	h := 2 + rows
	if q.pick.Title != "" {
		h++
	}
	if len(q.filtered) == 0 {
		h++
	}
	q.rect = Rect{x, y, w, h}
	a.cv.clip = Rect{0, 0, a.width, a.height}
	bgc := th.C("quickInput.background")
	fgc := th.C("quickInput.foreground")
	base := styleFB(fgc, bgc)
	a.cv.Fill(q.rect, ' ', base)
	// border
	bst := styleFB(th.OverColor("widget.border", bgc), bgc)
	if !th.Has("widget.border") {
		bst = styleFB(th.OverColor("editorWidget.border", bgc), bgc)
	}
	_ = bst
	cy := y
	if q.pick.Title != "" {
		tbg := th.OverColor("quickInputTitle.background", bgc)
		a.cv.Fill(Rect{x, cy, w, 1}, ' ', styleFB(fgc, tbg))
		a.cv.TextCenter(x, cy, w, q.pick.Title, styleFB(fgc, tbg))
		cy++
	}
	// input box
	ibg := th.C("input.background")
	ist := styleFB(th.C("input.foreground"), ibg)
	inputR := Rect{x + 1, cy, w - 2, 1}
	a.cv.Fill(inputR, ' ', ist)
	phSt := styleFB(th.C("input.placeholderForeground"), ibg)
	q.input.Draw(inputR.X+1, cy, inputR.W-2, ist, phSt, true)
	if th.Has("focusBorder") {
		a.cv.Set(inputR.X, cy, '▏', styleFB(th.C("focusBorder"), ibg))
		a.cv.Set(inputR.X+inputR.W-1, cy, '▕', styleFB(th.C("focusBorder"), ibg))
	}
	cy++
	// keep selection visible
	if q.selected < q.scroll {
		q.scroll = q.selected
	}
	if q.selected >= q.scroll+rows {
		q.scroll = q.selected - rows + 1
	}
	if len(q.filtered) == 0 {
		msg := "No matching results"
		if q.message != "" {
			msg = q.message
		}
		if q.busy {
			msg = "Loading..."
		}
		a.cv.Text(x+2, cy, x+w-1, msg, styleFB(th.C("descriptionForeground"), bgc))
		cy++
	}
	hlKey := "list.highlightForeground"
	for i := 0; i < rows; i++ {
		idx := q.scroll + i
		if idx >= len(q.filtered) {
			break
		}
		it := q.filtered[idx]
		sel := idx == q.selected
		rbg := bgc
		rfg := fgc
		if sel {
			rbg = th.C("quickInputList.focusBackground")
			rfg = th.C("quickInputList.focusForeground")
		} else if a.mouseY == cy && q.rect.Contains(a.mouseX, a.mouseY) {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		rst := styleFB(rfg, rbg)
		a.cv.Fill(Rect{x, cy, w, 1}, ' ', rst)
		px := x + 2
		if it.Icon != "" {
			ic := it.IconColor
			if ic == 0 || sel {
				ic = rfg
			}
			px = a.cv.Text(px, cy, x+w-1, it.Icon, styleFB(ic, rbg))
			px++
		}
		hl := rst.Foreground(th.C(hlKey)).Bold(true)
		if sel {
			hl = rst.Foreground(th.Or("list.focusHighlightForeground", hlKey)).Bold(true)
		}
		right := ""
		if it.Keybinding != "" {
			right = it.Keybinding
		} else if it.Group != "" && (idx == 0 || q.filtered[idx-1].Group != it.Group) {
			right = it.Group
		}
		rightW := StrWidth(right)
		maxX := x + w - 2 - rightW - 1
		px = a.cv.Highlighted(px, cy, maxX, it.Label, rst, hl, it.Positions)
		if it.Description != "" && px+2 < maxX {
			dst := rst.Foreground(th.OverColor("descriptionForeground", rbg))
			a.cv.TextEllipsis(px+1, cy, maxX-px-1, it.Description, dst)
		}
		if right != "" {
			var rst2 tcell.Style
			if it.Keybinding != "" {
				rst2 = styleFB(th.C("keybindingLabel.foreground"), th.OverColor("keybindingLabel.background", rbg))
			} else {
				rst2 = rst.Foreground(th.C("pickerGroup.foreground"))
			}
			a.cv.Text(x+w-1-rightW, cy, x+w-1, right, rst2)
		}
		// separator line above a new group
		if it.Group != "" && idx > 0 && q.filtered[idx-1].Group != it.Group {
			for sx := x + 1; sx < x+w-1; sx++ {
				ch, _, cst, _ := a.screen.GetContent(sx, cy)
				a.cv.Set(sx, cy, ch, cst.Underline(false))
			}
		}
		rowIdx := idx
		a.addHit(Rect{x, cy, w, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				q.selected = rowIdx
				q.accept()
			case MouseWheel:
				q.scroll = clampInt(q.scroll+ev.WheelDY, 0, max(len(q.filtered)-rows, 0))
			}
			return true
		})
		cy++
	}
	// shadow
	shadow := th.OverColor("widget.shadow", th.C("editor.background"))
	for sx := x + 1; sx <= x+w && sx < a.width; sx++ {
		if cy < a.height {
			_, _, cst, _ := a.screen.GetContent(sx, cy)
			ch, _, _, _ := a.screen.GetContent(sx, cy)
			a.cv.Set(sx, cy, ch, cst.Background(blendTcell(bgOf(cst), shadow)))
		}
	}
	a.addHitUnder(mark, q.rect, func(ev MouseEvent) bool {
		if ev.Kind == MouseWheel {
			q.scroll = clampInt(q.scroll+ev.WheelDY, 0, max(len(q.filtered)-rows, 0))
		}
		return true
	})
	// make sure clicks outside reach the closing handler first by registering on top
	a.addHit(Rect{0, 0, a.width, a.height}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress && !q.rect.Contains(ev.X, ev.Y) {
			a.closeQuick(true)
			return true
		}
		return false
	})
}

func blendTcell(base, shadow tcell.Color) tcell.Color {
	r1, g1, b1 := base.RGB()
	r2, g2, b2 := shadow.RGB()
	if r1 < 0 {
		return base
	}
	mix := func(a, b int32) int32 { return (a*3 + b) / 4 }
	return tcell.NewRGBColor(mix(r1, r2), mix(g1, g2), mix(b1, b2))
}
