package ui

import "tuicode/internal/buffer"

// Editor groups are laid out as columns; a group with `below` set is stacked under the previous group.

const maxEditorGroups = 6

// groupColumns returns the group indexes of each column.
func (a *App) groupColumns() [][]int {
	var cols [][]int
	for i, g := range a.groups {
		if i == 0 || !g.below {
			cols = append(cols, []int{i})
			continue
		}
		cols[len(cols)-1] = append(cols[len(cols)-1], i)
	}
	return cols
}

// groupRects splits r between the groups.
func (a *App) groupRects(r Rect) []Rect {
	rects := make([]Rect, len(a.groups))
	cols := a.groupColumns()
	x := r.X
	for ci, col := range cols {
		w := r.W / len(cols)
		if ci == len(cols)-1 {
			w = r.X + r.W - x
		}
		y := r.Y
		for ri, gi := range col {
			h := r.H / len(col)
			if ri == len(col)-1 {
				h = r.Y + r.H - y
			}
			rects[gi] = Rect{x, y, w, h}
			y += h
		}
		x += w
	}
	return rects
}

// removeGroupAt drops a group from the layout, keeping the columns of the others.
func (a *App) removeGroupAt(i int) {
	if i < 0 || i >= len(a.groups) {
		return
	}
	if !a.groups[i].below && i+1 < len(a.groups) {
		a.groups[i+1].below = false
	}
	a.groups = append(a.groups[:i], a.groups[i+1:]...)
	if len(a.groups) > 0 {
		a.groups[0].below = false
	}
	if a.activeGroup > i || a.activeGroup >= len(a.groups) {
		a.activeGroup = max(a.activeGroup-1, 0)
	}
}

// insertGroup adds a group to the right of the active group's column, or below the active group.
func (a *App) insertGroup(down bool) *EditorGroup {
	ng := a.newGroup()
	at := a.activeGroup + 1
	if down {
		ng.below = true
	} else {
		for at < len(a.groups) && a.groups[at].below {
			at++
		}
	}
	a.groups = append(a.groups, nil)
	copy(a.groups[at+1:], a.groups[at:])
	a.groups[at] = ng
	a.activeGroup = at
	return ng
}

// splitEditorDir opens the active editor in a new group to the right or below.
func (a *App) splitEditorDir(down bool) {
	v := a.ActiveView()
	if len(a.groups) >= maxEditorGroups {
		a.setStatusMessage("Too many editor groups")
		return
	}
	ng := a.insertGroup(down)
	if v != nil && v.special == nil {
		nv := a.newEditorView(v.doc)
		nv.SetSelections(append([]buffer.Selection{}, v.sels...), v.primary)
		nv.topLine, nv.topSeg = v.topLine, v.topSeg
		nv.wrap = v.wrap
		ng.addTab(nv, false)
	}
	a.focusEditor()
}

// splitEditorOrthogonal splits against the current layout direction, like VS Code.
func (a *App) splitEditorOrthogonal() {
	cols := a.groupColumns()
	a.splitEditorDir(len(cols) > 1 || len(a.groups) == 1)
}

// toggleEditorGroupLayout switches between side by side and stacked groups.
func (a *App) toggleEditorGroupLayout() {
	if len(a.groups) < 2 {
		return
	}
	stack := len(a.groupColumns()) > 1
	for i, g := range a.groups {
		g.below = stack && i > 0
	}
}

// focusGroupDir moves focus to the nearest group in a direction (dx, dy).
func (a *App) focusGroupDir(dx, dy int) {
	rects := a.groupRects(a.rEditor)
	if a.activeGroup >= len(rects) {
		return
	}
	cur := rects[a.activeGroup]
	cx, cy := cur.X+cur.W/2, cur.Y+cur.H/2
	best, bestDist := -1, 1<<30
	for i, r := range rects {
		if i == a.activeGroup {
			continue
		}
		ok := false
		switch {
		case dx < 0:
			ok = r.X+r.W <= cur.X && r.Y < cur.Y+cur.H && r.Y+r.H > cur.Y
		case dx > 0:
			ok = r.X >= cur.X+cur.W && r.Y < cur.Y+cur.H && r.Y+r.H > cur.Y
		case dy < 0:
			ok = r.Y+r.H <= cur.Y && r.X < cur.X+cur.W && r.X+r.W > cur.X
		case dy > 0:
			ok = r.Y >= cur.Y+cur.H && r.X < cur.X+cur.W && r.X+r.W > cur.X
		}
		if !ok {
			continue
		}
		d := abs(r.X+r.W/2-cx) + abs(r.Y+r.H/2-cy)
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	if best >= 0 {
		a.activeGroup = best
		a.focusEditor()
	}
}
