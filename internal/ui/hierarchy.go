package ui

import (
	"encoding/json"
	"path/filepath"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

// hierarchyItem is an LSP CallHierarchyItem or TypeHierarchyItem.
type hierarchyItem struct {
	Name           string    `json:"name"`
	Kind           int       `json:"kind"`
	Detail         string    `json:"detail"`
	URI            string    `json:"uri"`
	Range          lsp.Range `json:"range"`
	SelectionRange lsp.Range `json:"selectionRange"`
}

type hierNode struct {
	raw      json.RawMessage
	item     hierarchyItem
	children []*hierNode
	loaded   bool
	loading  bool
	expanded bool
	depth    int
	call     *lsp.Range // where the call happens (incoming: in item, outgoing: in the parent)
}

// HierarchyView shows call and type hierarchies in the side bar.
type HierarchyView struct {
	app      *App
	mode     string // "incoming", "outgoing", "supertypes", "subtypes"
	buf      *buffer.Buffer
	root     *hierNode
	rows     []*hierNode
	selected int
	scroll   int
	message  string
}

func newHierarchyView(a *App) *HierarchyView { return &HierarchyView{app: a, mode: "incoming"} }

func (h *HierarchyView) isCalls() bool { return h.mode == "incoming" || h.mode == "outgoing" }

func (h *HierarchyView) title() string {
	if h.isCalls() {
		return "CALL HIERARCHY"
	}
	return "TYPE HIERARCHY"
}

// show prepares a hierarchy for the symbol at the cursor.
func (a *App) showHierarchy(v *EditorView, mode string) {
	if a.noLSP(v, "hierarchy") {
		return
	}
	h := a.hierView
	method := "textDocument/prepareCallHierarchy"
	if mode == "supertypes" || mode == "subtypes" {
		method = "textDocument/prepareTypeHierarchy"
	}
	a.lsp.Request(v.buf(), method, a.lsp.DocParams(v.buf(), v.Cursor()), func(raw json.RawMessage, err error) {
		var items []json.RawMessage
		if err == nil {
			_ = json.Unmarshal(raw, &items)
		}
		if len(items) == 0 {
			if err != nil {
				a.setStatusMessage(err.Error())
			} else {
				a.setStatusMessage("No results.")
			}
			return
		}
		h.mode = mode
		h.buf = v.buf()
		h.root = &hierNode{raw: items[0], expanded: true}
		_ = json.Unmarshal(items[0], &h.root.item)
		h.selected = 0
		h.scroll = 0
		h.load(h.root)
		a.showSidebar("hierarchy")
	})
}

// load fetches the children of a node.
func (h *HierarchyView) load(n *hierNode) {
	a := h.app
	if n.loaded || n.loading || a.lsp == nil {
		return
	}
	n.loading = true
	method := map[string]string{
		"incoming":   "callHierarchy/incomingCalls",
		"outgoing":   "callHierarchy/outgoingCalls",
		"supertypes": "typeHierarchy/supertypes",
		"subtypes":   "typeHierarchy/subtypes",
	}[h.mode]
	mode := h.mode
	a.lsp.Request(h.buf, method, map[string]any{"item": n.raw}, func(raw json.RawMessage, err error) {
		n.loading = false
		n.loaded = true
		if err != nil || h.mode != mode {
			return
		}
		n.children = nil
		if h.isCalls() {
			var calls []struct {
				From       json.RawMessage `json:"from"`
				To         json.RawMessage `json:"to"`
				FromRanges []lsp.Range     `json:"fromRanges"`
			}
			_ = json.Unmarshal(raw, &calls)
			for _, c := range calls {
				itemRaw := c.From
				if mode == "outgoing" {
					itemRaw = c.To
				}
				child := &hierNode{raw: itemRaw, depth: n.depth + 1}
				_ = json.Unmarshal(itemRaw, &child.item)
				if len(c.FromRanges) > 0 {
					r := c.FromRanges[0]
					child.call = &r
				}
				n.children = append(n.children, child)
			}
		} else {
			var items []json.RawMessage
			_ = json.Unmarshal(raw, &items)
			for _, it := range items {
				child := &hierNode{raw: it, depth: n.depth + 1}
				_ = json.Unmarshal(it, &child.item)
				n.children = append(n.children, child)
			}
		}
		h.rebuild()
	})
}

func (h *HierarchyView) rebuild() {
	h.rows = nil
	var walk func(n *hierNode)
	walk = func(n *hierNode) {
		h.rows = append(h.rows, n)
		if n.expanded {
			for _, c := range n.children {
				walk(c)
			}
		}
	}
	if h.root != nil {
		walk(h.root)
	}
	h.selected = clampInt(h.selected, 0, max(len(h.rows)-1, 0))
}

// toggleDirection switches between callers and callees (or supertypes and subtypes).
func (h *HierarchyView) toggleDirection() {
	if h.root == nil {
		return
	}
	h.mode = map[string]string{"incoming": "outgoing", "outgoing": "incoming", "supertypes": "subtypes", "subtypes": "supertypes"}[h.mode]
	root := &hierNode{raw: h.root.raw, item: h.root.item, expanded: true}
	h.root = root
	h.selected = 0
	h.load(root)
	h.rebuild()
}

// open reveals a node: the call site for incoming calls, otherwise the symbol.
func (h *HierarchyView) open(n *hierNode, focus bool) {
	a := h.app
	loc := lsp.Location{URI: n.item.URI, Range: n.item.SelectionRange}
	if n.call != nil && h.mode == "incoming" {
		loc.Range = *n.call
	}
	v := a.openLocation(loc)
	if v != nil && !focus {
		a.setFocus(FocusSidebar)
	}
}

func (h *HierarchyView) toggle(n *hierNode) {
	n.expanded = !n.expanded
	if n.expanded {
		h.load(n)
	}
	h.rebuild()
}

func (h *HierarchyView) handleKey(key, text string, isText bool) bool {
	a := h.app
	if len(h.rows) == 0 {
		if key == "escape" {
			a.focusEditor()
			return true
		}
		return false
	}
	h.selected = clampInt(h.selected, 0, len(h.rows)-1)
	n := h.rows[h.selected]
	switch key {
	case "up":
		h.selected = max(h.selected-1, 0)
	case "down":
		h.selected = min(h.selected+1, len(h.rows)-1)
	case "home":
		h.selected = 0
	case "end":
		h.selected = len(h.rows) - 1
	case "right":
		if !n.expanded {
			h.toggle(n)
		} else if len(n.children) > 0 {
			h.selected++
		}
	case "left":
		if n.expanded && n != h.root {
			h.toggle(n)
		} else {
			for i := h.selected - 1; i >= 0; i-- {
				if h.rows[i].depth < n.depth {
					h.selected = i
					break
				}
			}
		}
	case "enter":
		h.open(n, true)
	case "space":
		h.open(n, false)
	case "escape":
		a.focusEditor()
	default:
		return false
	}
	return true
}

func (h *HierarchyView) drawActions(r Rect) {
	a := h.app
	th := a.theme
	if h.root == nil {
		return
	}
	tip := "Show Outgoing Calls"
	switch h.mode {
	case "outgoing":
		tip = "Show Incoming Calls"
	case "supertypes":
		tip = "Show Subtypes"
	case "subtypes":
		tip = "Show Supertypes"
	}
	x := r.X + r.W - 2
	icon := IcoArrowDown
	if h.mode == "outgoing" || h.mode == "subtypes" {
		icon = IcoArrowUp
	}
	a.cv.Text(x, r.Y, x+1, a.Icon(icon), styleFB(th.C("sideBarTitle.foreground"), th.C("sideBar.background")))
	a.addHit(Rect{x, r.Y, 1, 1}, func(ev MouseEvent) bool {
		switch ev.Kind {
		case MousePress:
			h.toggleDirection()
		case MouseMove:
			a.showTooltipAt(ev.X, r.Y+1, tip)
		}
		return true
	})
}

func (h *HierarchyView) draw(r Rect) {
	a := h.app
	th := a.theme
	bgc := th.C("sideBar.background")
	desc := styleFB(th.C("descriptionForeground"), bgc)
	if h.root == nil {
		a.cv.Text(r.X+2, r.Y, r.X+r.W, "Place the cursor on a function and run \"Show Call Hierarchy\".", desc)
		return
	}
	label := map[string]string{"incoming": "Callers Of ", "outgoing": "Calls From ", "supertypes": "Supertypes Of ", "subtypes": "Subtypes Of "}[h.mode]
	a.cv.Text(r.X+2, r.Y, r.X+r.W, label+h.root.item.Name, desc)
	body := Rect{r.X, r.Y + 1, r.W, r.H - 1}
	if len(h.rows) == 0 {
		h.rebuild()
	}
	focused := a.focus == FocusSidebar && a.sidebarView == "hierarchy"
	hh := body.H
	if h.selected < h.scroll {
		h.scroll = h.selected
	}
	if h.selected >= h.scroll+hh {
		h.scroll = h.selected - hh + 1
	}
	h.scroll = clampInt(h.scroll, 0, max(len(h.rows)-hh, 0))
	for i := 0; i < hh; i++ {
		idx := h.scroll + i
		if idx >= len(h.rows) {
			break
		}
		n := h.rows[idx]
		y := body.Y + i
		rbg := bgc
		fg := th.C("sideBar.foreground")
		if idx == h.selected && focused {
			rbg = th.C("list.activeSelectionBackground")
			fg = th.C("list.activeSelectionForeground")
		} else if idx == h.selected {
			rbg = th.C("list.inactiveSelectionBackground")
		} else if a.mouseY == y && body.Contains(a.mouseX, a.mouseY) {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		st := styleFB(fg, rbg)
		a.cv.Fill(Rect{body.X, y, body.W, 1}, ' ', st)
		x := body.X + 1 + n.depth*2
		chev := " "
		switch {
		case n.loading:
			chev = "…"
		case n.loaded && len(n.children) == 0:
			chev = " "
		case n.expanded:
			chev = a.Icon(IcoChevronDown)
		default:
			chev = a.Icon(IcoChevronRight)
		}
		chevX := x
		x = a.cv.Text(x, y, body.X+body.W, chev+" ", st)
		icon, ck := a.kindIcon(n.item.Kind, true)
		x = a.cv.Text(x, y, body.X+body.W, icon+" ", styleFB(th.C(ck), rbg))
		x = a.cv.Text(x, y, body.X+body.W-1, n.item.Name, st)
		detail := n.item.Detail
		if p := lsp.URIToPath(n.item.URI); p != "" {
			if detail != "" {
				detail += " · "
			}
			detail += filepath.Base(p)
		}
		if detail != "" {
			a.cv.TextEllipsis(x+1, y, body.X+body.W-x-2, detail, st.Foreground(th.OverColor("descriptionForeground", rbg)))
		}
		node, rowIdx := n, idx
		a.addHit(Rect{body.X, y, body.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				h.selected = rowIdx
				a.setFocus(FocusSidebar)
				if ev.X <= chevX+1 && ev.X >= chevX {
					h.toggle(node)
				} else {
					h.open(node, ev.Clicks >= 2)
				}
			case MouseWheel:
				h.scroll = clampInt(h.scroll+ev.WheelDY*3, 0, max(len(h.rows)-hh, 0))
			}
			return true
		})
	}
}
