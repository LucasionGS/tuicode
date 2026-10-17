package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/lsp"
)

// SpecialPage is a non-text editor (welcome page, keyboard shortcuts...).
type SpecialPage interface {
	Title() string
	Draw(r Rect)
	HandleKey(key, text string, isText bool) bool
}

// EditorGroup is a column of tabs.
type EditorGroup struct {
	app      *App
	tabs     []*EditorView
	active   int
	mru      []*EditorView
	tabsRect Rect
	scrollX  int
	dragTab  int
	below    bool // stacked under the previous group in the same column
}

func (a *App) newGroup() *EditorGroup {
	return &EditorGroup{app: a, dragTab: -1}
}

// Active returns the active editor or nil.
func (g *EditorGroup) Active() *EditorView {
	if len(g.tabs) == 0 {
		return nil
	}
	if g.active >= len(g.tabs) {
		g.active = len(g.tabs) - 1
	}
	return g.tabs[g.active]
}

func (g *EditorGroup) activeIsEditor() bool {
	v := g.Active()
	return v != nil && v.special == nil
}

func (g *EditorGroup) indexOf(v *EditorView) int {
	for i, t := range g.tabs {
		if t == v {
			return i
		}
	}
	return -1
}

func (g *EditorGroup) setActive(i int) {
	if i < 0 || i >= len(g.tabs) {
		return
	}
	g.active = i
	v := g.tabs[i]
	// maintain MRU order
	for j, m := range g.mru {
		if m == v {
			g.mru = append(g.mru[:j], g.mru[j+1:]...)
			break
		}
	}
	g.mru = append([]*EditorView{v}, g.mru...)
	g.app.onActiveEditorChanged()
}

// addTab inserts a view after the active tab (or replaces the preview tab).
func (g *EditorGroup) addTab(v *EditorView, preview bool) {
	v.preview = preview
	// replace an existing preview tab
	for i, t := range g.tabs {
		if t.preview && t != v && preview {
			g.app.disposeView(t)
			g.tabs[i] = v
			g.removeMRU(t)
			g.setActive(i)
			return
		}
	}
	at := len(g.tabs)
	if len(g.tabs) > 0 {
		at = g.active + 1
	}
	for at < len(g.tabs) && g.tabs[at].pinned && !v.pinned {
		at++
	}
	g.tabs = append(g.tabs, nil)
	copy(g.tabs[at+1:], g.tabs[at:])
	g.tabs[at] = v
	g.setActive(at)
}

func (g *EditorGroup) removeMRU(v *EditorView) {
	for j, m := range g.mru {
		if m == v {
			g.mru = append(g.mru[:j], g.mru[j+1:]...)
			return
		}
	}
}

// removeTab removes a tab without prompting.
func (g *EditorGroup) removeTab(i int) {
	if i < 0 || i >= len(g.tabs) {
		return
	}
	v := g.tabs[i]
	g.tabs = append(g.tabs[:i], g.tabs[i+1:]...)
	g.removeMRU(v)
	g.app.disposeView(v)
	if len(g.tabs) == 0 {
		g.active = 0
		g.app.onActiveEditorChanged()
		return
	}
	// activate the most recently used remaining tab
	if len(g.mru) > 0 {
		g.setActive(g.indexOf(g.mru[0]))
	} else {
		g.setActive(min(i, len(g.tabs)-1))
	}
}

func (g *EditorGroup) tabLabel(v *EditorView) string {
	if v.special != nil {
		return v.special.Title()
	}
	name := v.doc.Title()
	// disambiguate same names
	for _, t := range g.tabs {
		if t != v && t.special == nil && t.doc.Title() == name && v.buf().Path != "" {
			dir := filepath.Base(filepath.Dir(v.buf().Path))
			return name + " · " + dir
		}
	}
	return name
}

func (g *EditorGroup) draw(r Rect, focused bool) {
	a := g.app
	th := a.theme
	if len(g.tabs) == 0 {
		g.tabsRect = Rect{}
		wmMark := a.hitMark()
		a.drawWatermark(r)
		a.addHitUnder(wmMark, r, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.activeGroup = a.groupIndex(g)
				a.focusEditor()
			}
			return true
		})
		return
	}
	showTabs := a.settings.String("workbench.editor.showTabs", "") != "none" && !a.zenMode
	y := r.Y
	if showTabs {
		g.tabsRect = Rect{r.X, r.Y, r.W, 1}
		g.drawTabs(g.tabsRect, focused)
		a.cv.clip = r
		y++
	}
	v := g.Active()
	if v.special == nil && a.breadcrumbs && !a.zenMode {
		bst := styleFB(th.C("breadcrumb.foreground"), th.C("breadcrumb.background"))
		a.cv.Fill(Rect{r.X, y, r.W, 1}, ' ', bst)
		a.drawBreadcrumbs(v, Rect{r.X, y, r.W, 1})
		y++
	}
	er := Rect{r.X, y, r.W, r.Y + r.H - y}
	if v.special != nil {
		bg := styleFB(th.C("editor.foreground"), th.C("editor.background"))
		a.cv.Fill(er, ' ', bg)
		v.full = er
		v.special.Draw(er)
		a.addHit(er, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.activeGroup = a.groupIndex(g)
				a.focusEditor()
			}
			return false
		})
		return
	}
	v.draw(er, focused && a.focus == FocusEditor || focused && a.focus == FocusWidget)
}

func (a *App) groupIndex(g *EditorGroup) int {
	for i, x := range a.groups {
		if x == g {
			return i
		}
	}
	return 0
}

func (g *EditorGroup) drawTabs(r Rect, focused bool) {
	a := g.app
	th := a.theme
	bgc := th.C("editorGroupHeader.tabsBackground")
	a.cv.Fill(r, ' ', styleFB(th.C("tab.inactiveForeground"), bgc))
	// editor actions on the right
	actW := 4
	if a.nerdIcons {
		actW = 4
	}
	tabArea := Rect{r.X, r.Y, max(r.W-actW, 1), 1}
	// measure tabs
	widths := make([]int, len(g.tabs))
	total := 0
	for i, v := range g.tabs {
		w := StrWidth(g.tabLabel(v)) + 4
		if a.nerdIcons {
			w += 2
		}
		widths[i] = w
		total += w
	}
	// scroll so the active tab is visible
	start := 0
	for i := 0; i < g.active; i++ {
		start += widths[i]
	}
	if start < g.scrollX {
		g.scrollX = start
	}
	if start+widths[g.active] > g.scrollX+tabArea.W {
		g.scrollX = start + widths[g.active] - tabArea.W
	}
	if total <= tabArea.W {
		g.scrollX = 0
	}
	x := tabArea.X - g.scrollX
	a.cv.clip = tabArea
	for i, v := range g.tabs {
		w := widths[i]
		active := i == g.active
		hover := a.mouseY == r.Y && a.mouseX >= x && a.mouseX < x+w && tabArea.Contains(a.mouseX, a.mouseY)
		bgk := "tab.inactiveBackground"
		fgk := "tab.inactiveForeground"
		if active {
			bgk = "tab.activeBackground"
			fgk = "tab.activeForeground"
			if !focused {
				fgk = "tab.unfocusedActiveForeground"
			}
		} else if hover && th.Has("tab.hoverBackground") {
			bgk = "tab.hoverBackground"
		}
		tbg := th.C(bgk)
		st := styleFB(th.C(fgk), tbg)
		if v.preview {
			st = st.Italic(true)
		}
		// diagnostics color the tab label like VS Code
		if v.special == nil {
			if sev := a.worstSeverity(v.doc); sev == lsp.SeverityError {
				st = st.Foreground(th.C("list.errorForeground"))
			} else if sev == lsp.SeverityWarning {
				st = st.Foreground(th.C("list.warningForeground"))
			} else if key, _ := a.gitDecoration(v.buf().Path, false); key != "" && active {
				st = st.Foreground(th.C(key))
			}
		}
		a.cv.Fill(Rect{x, r.Y, w, 1}, ' ', st)
		tx := x + 1
		if a.nerdIcons {
			icon, col := a.FileIcon(g.tabLabel(v), false, false)
			if v.special != nil {
				icon, col = "", th.C("textLink.foreground")
			}
			a.cv.Text(tx, r.Y, x+w, icon, styleFB(col, tbg))
			tx += 2
		}
		a.cv.Text(tx, r.Y, x+w, g.tabLabel(v), st)
		// close button or dirty dot
		cx := x + w - 2
		dirty := v.special == nil && v.buf().Dirty()
		closeHover := a.mouseY == r.Y && a.mouseX == cx
		switch {
		case v.pinned && !closeHover:
			a.cv.Text(cx, r.Y, cx+1, a.Icon(IcoPinned), st)
		case closeHover:
			a.cv.Text(cx, r.Y, cx+1, a.Icon(IcoClose), st.Foreground(th.C("tab.activeForeground")))
		case dirty:
			a.cv.Text(cx, r.Y, cx+1, a.Icon(IcoDirty), st)
		case active || hover:
			a.cv.Text(cx, r.Y, cx+1, a.Icon(IcoClose), st)
		}
		// active tab top border drawn on the row above (title bar) when possible
		if active && th.Has("tab.activeBorderTop") && r.Y > 0 && focused {
			a.cv.clip = Rect{tabArea.X, r.Y - 1, tabArea.W, 1}
			above := a.rTitle
			if above.Contains(x, r.Y-1) {
				for bx := x; bx < x+w; bx++ {
					a.cv.Set(bx, r.Y-1, '▁', styleFB(th.C("tab.activeBorderTop"), th.C("titleBar.activeBackground")))
				}
			}
			a.cv.clip = tabArea
		}
		if !active && th.Has("tab.border") && th.Defined("tab.border") {
			a.cv.Set(x+w-1, r.Y, '▏', styleFB(th.C("tab.border"), tbg))
		}
		idx := i
		tabX := x
		a.addHit(Rect{max(x, tabArea.X), r.Y, min(w, tabArea.X+tabArea.W-max(x, tabArea.X)), 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.activeGroup = a.groupIndex(g)
				switch ev.Button {
				case tcell.Button3:
					if !g.tabs[idx].pinned {
						a.closeTab(g, idx)
					}
				case tcell.Button2:
					g.setActive(idx)
					a.showTabContextMenu(g, idx, ev.X, ev.Y+1)
				default:
					if ev.X == tabX+w-2 {
						if g.tabs[idx].pinned {
							g.tabs[idx].pinned = false
						} else {
							a.closeTab(g, idx)
						}
						return true
					}
					if ev.Clicks == 2 {
						g.tabs[idx].preview = false
					}
					g.setActive(idx)
					a.focusEditor()
					g.dragTab = idx
					a.drag = func(ev MouseEvent) {
						if ev.Kind == MouseRelease {
							g.dragTab = -1
							return
						}
						// reorder by dragging
						target := g.tabAtX(ev.X, widths, tabArea)
						if target >= 0 && target != g.dragTab {
							t := g.tabs[g.dragTab]
							g.tabs = append(g.tabs[:g.dragTab], g.tabs[g.dragTab+1:]...)
							g.tabs = append(g.tabs[:target], append([]*EditorView{t}, g.tabs[target:]...)...)
							g.dragTab = target
							g.active = target
						}
					}
				}
			case MouseMove:
				if v := g.tabs[idx]; v.special == nil && v.buf().Path != "" {
					a.showTooltipAt(ev.X, r.Y+1, a.relPath(v.buf().Path))
				}
			}
			return true
		})
		x += w
	}
	a.cv.clip = r
	// empty area: double click creates a new file
	if x < tabArea.X+tabArea.W {
		a.addHit(Rect{x, r.Y, tabArea.X + tabArea.W - x, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress && ev.Clicks == 2 {
				a.activeGroup = a.groupIndex(g)
				a.NewUntitled()
			}
			return true
		})
	}
	// actions: split, more
	ax := r.X + r.W - actW + 1
	ast := styleFB(th.C("tab.inactiveForeground"), bgc).Foreground(th.C("foreground"))
	a.cv.Text(ax, r.Y, ax+1, a.Icon(IcoSplit), ast)
	a.addHit(Rect{ax, r.Y, 1, 1}, func(ev MouseEvent) bool {
		switch ev.Kind {
		case MousePress:
			a.activeGroup = a.groupIndex(g)
			a.RunCommand("workbench.action.splitEditor", nil)
		case MouseMove:
			a.showTooltipAt(ev.X-10, r.Y+1, "Split Editor Right (Ctrl+\\)")
		}
		return true
	})
	a.cv.Text(ax+2, r.Y, ax+3, a.Icon(IcoEllipsis), ast)
	a.addHit(Rect{ax + 2, r.Y, 1, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			a.activeGroup = a.groupIndex(g)
			a.openContextMenu(ev.X-20, r.Y+1, []MenuItem{
				{Label: "Close All", Command: "workbench.action.closeEditorsInGroup"},
				{Label: "Close Saved", Command: "workbench.action.closeUnmodifiedEditors"},
				{Separator: true},
				{Label: "Split Up", Command: "workbench.action.splitEditor"},
				{Label: "Word Wrap", Command: "editor.action.toggleWordWrap"},
				{Separator: true},
				{Label: "Lock Group", Command: "workbench.action.lockEditorGroup", Disabled: true},
			})
		}
		return true
	})
}

func (g *EditorGroup) tabAtX(x int, widths []int, area Rect) int {
	tx := area.X - g.scrollX
	for i, w := range widths {
		if x >= tx && x < tx+w {
			return i
		}
		tx += w
	}
	return -1
}

func (a *App) worstSeverity(d *Document) int {
	worst := 0
	for _, dg := range d.Diags {
		if worst == 0 || dg.Severity < worst {
			worst = dg.Severity
		}
	}
	return worst
}

func pinMenuItem(v *EditorView) MenuItem {
	if v.pinned {
		return MenuItem{Label: "Unpin", Command: "workbench.action.unpinEditor"}
	}
	return MenuItem{Label: "Pin", Command: "workbench.action.pinEditor"}
}

// setPinned pins or unpins the active tab, keeping pinned tabs first like VS Code.
func (g *EditorGroup) setPinned(v *EditorView, pinned bool) {
	i := g.indexOf(v)
	if i < 0 || v.pinned == pinned {
		return
	}
	v.pinned = pinned
	v.preview = false
	g.tabs = append(g.tabs[:i], g.tabs[i+1:]...)
	at := 0
	for at < len(g.tabs) && g.tabs[at].pinned {
		at++
	}
	g.tabs = append(g.tabs, nil)
	copy(g.tabs[at+1:], g.tabs[at:])
	g.tabs[at] = v
	g.active = at
}

func (a *App) showTabContextMenu(g *EditorGroup, idx int, x, y int) {
	v := g.tabs[idx]
	items := []MenuItem{
		{Label: "Close", Command: "workbench.action.closeActiveEditor"},
		{Label: "Close Others", Command: "workbench.action.closeOtherEditors"},
		{Label: "Close to the Right", Command: "workbench.action.closeEditorsToTheRight"},
		{Label: "Close Saved", Command: "workbench.action.closeUnmodifiedEditors"},
		{Label: "Close All", Command: "workbench.action.closeEditorsInGroup"},
		{Separator: true},
	}
	if v.special == nil && v.buf().Path != "" {
		items = append(items,
			MenuItem{Label: "Copy Path", Command: "copyFilePath"},
			MenuItem{Label: "Copy Relative Path", Command: "copyRelativeFilePath"},
			MenuItem{Separator: true},
			MenuItem{Label: "Reveal in Explorer View", Command: "workbench.files.action.showActiveFileInExplorer"},
			MenuItem{Separator: true},
		)
	}
	items = append(items,
		MenuItem{Label: "Keep Open", Command: "workbench.action.keepEditor"},
		pinMenuItem(v),
		MenuItem{Separator: true},
		MenuItem{Label: "Split Right", Command: "workbench.action.splitEditor"},
	)
	a.openContextMenu(x, y, items)
}

func (a *App) drawBreadcrumbs(v *EditorView, r Rect) {
	th := a.theme
	st := styleFB(th.C("breadcrumb.foreground"), th.C("breadcrumb.background"))
	sep := " " + a.Icon(IcoChevronRight) + " "
	x := r.X + 1
	var parts []string
	if v.buf().Path != "" {
		rel := a.relPath(v.buf().Path)
		parts = strings.Split(rel, string(filepath.Separator))
	} else {
		parts = []string{v.doc.Title()}
	}
	for i, p := range parts {
		if i > 0 {
			x = a.cv.Text(x, r.Y, r.X+r.W, sep, st)
		}
		pst := st
		if i == len(parts)-1 {
			if a.nerdIcons {
				icon, col := a.FileIcon(p, false, false)
				x = a.cv.Text(x, r.Y, r.X+r.W, icon+" ", styleFB(col, th.C("breadcrumb.background")))
			}
		}
		start := x
		x = a.cv.Text(x, r.Y, r.X+r.W, p, pst)
		if v.buf().Path != "" {
			// the dropdown lists the folder that contains this segment
			depth := len(parts) - 1 - i
			dir := filepath.Dir(v.buf().Path)
			for k := 0; k < depth; k++ {
				dir = filepath.Dir(dir)
			}
			bx := start
			a.addHit(Rect{start, r.Y, x - start, 1}, func(ev MouseEvent) bool {
				if ev.Kind == MousePress {
					a.breadcrumbFolderMenu(dir, bx, r.Y+1)
				}
				return true
			})
		}
	}
	// symbol path
	syms := a.outline.symbolPath(v.doc, v.Cursor())
	for si, s := range syms {
		x = a.cv.Text(x, r.Y, r.X+r.W, sep, st)
		icon, ck := a.kindIcon(s.Kind, true)
		x = a.cv.Text(x, r.Y, r.X+r.W, icon+" ", styleFB(th.C(ck), th.C("breadcrumb.background")))
		start := x
		x = a.cv.Text(x, r.Y, r.X+r.W, s.Name, st)
		level := si
		bx := start
		a.addHit(Rect{start, r.Y, x - start, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.breadcrumbSymbolMenu(v, syms, level, bx, r.Y+1)
			}
			return true
		})
	}
}

// breadcrumbFolderMenu lists a folder like VS Code's breadcrumb picker: folders open in place, files open.
func (a *App) breadcrumbFolderMenu(dir string, x, y int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	match := a.excludeMatcher()
	var dirs, files []MenuItem
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(dir, name)
		if match(name, e.IsDir()) || name == ".git" {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, MenuItem{Label: a.Icon(IcoChevronRight) + " " + name, Run: func() { a.breadcrumbFolderMenu(full, x, y) }})
			continue
		}
		icon, _ := a.FileIcon(name, false, false)
		if icon == "" {
			icon = " "
		}
		files = append(files, MenuItem{Label: icon + " " + name, Run: func() {
			a.OpenFile(full, false)
			a.focusEditor()
		}})
	}
	items := append([]MenuItem{}, dirs...)
	items = append(items, files...)
	if parent := filepath.Dir(dir); parent != dir && strings.HasPrefix(dir, a.root+string(filepath.Separator)) {
		items = append([]MenuItem{{Label: "..", Run: func() { a.breadcrumbFolderMenu(parent, x, y) }}, {Separator: true}}, items...)
	}
	if len(items) > 0 {
		a.openContextMenu(x, y, items)
	}
}

// breadcrumbSymbolMenu lists the symbols next to syms[level], like VS Code's breadcrumb outline picker.
func (a *App) breadcrumbSymbolMenu(v *EditorView, syms []lsp.DocumentSymbol, level, x, y int) {
	var siblings []lsp.DocumentSymbol
	if level == 0 {
		siblings = a.outline.rootSymbols(v.doc)
	} else {
		siblings = syms[level-1].Children
	}
	var items []MenuItem
	for _, s := range siblings {
		sym := s
		icon, _ := a.kindIcon(s.Kind, true)
		items = append(items, MenuItem{Label: icon + " " + s.Name, Checked: func() bool { return sym.Name == syms[level].Name }, Run: func() {
			pos := a.lsp.FromLSP(v.buf(), sym.SelectionRange.Start)
			a.pushNav()
			v.SetCursor(pos)
			v.ensurePosVisible(pos, true)
			a.focusEditor()
		}})
	}
	if len(items) > 0 {
		a.openContextMenu(x, y, items)
	}
}

func (a *App) drawWatermark(r Rect) {
	th := a.theme
	bg := th.C("editor.background")
	a.cv.Fill(r, ' ', styleFB(th.C("editor.foreground"), bg))
	type wm struct{ label, cmd string }
	items := []wm{
		{"Show All Commands", "workbench.action.showCommands"},
		{"Go to File", "workbench.action.quickOpen"},
		{"Find in Files", "workbench.action.findInFiles"},
		{"Toggle Terminal", "workbench.action.terminal.toggleTerminal"},
		{"Open Folder", "workbench.action.files.openFolder"},
	}
	cy := r.Y + r.H/2 - len(items)
	if cy < r.Y {
		cy = r.Y
	}
	labelSt := styleFB(th.Over("descriptionForeground", "editor.background"), bg)
	keySt := styleFB(th.C("keybindingLabel.foreground"), th.OverColor("keybindingLabel.background", bg))
	// logo
	logo := []string{"╺┳╸╻ ╻╻┏━╸┏━┓╺┳┓┏━╸", " ┃ ┃ ┃┃┃  ┃ ┃ ┃┃┣╸ ", " ╹ ┗━┛╹┗━╸┗━┛╺┻┛┗━╸"}
	if cy-len(logo)-1 > r.Y {
		ly := cy - len(logo) - 1
		for i, l := range logo {
			a.cv.TextCenter(r.X, ly+i, r.W, l, styleFB(th.OverColor("descriptionForeground", bg), bg).Dim(true))
		}
	}
	for i, it := range items {
		y := cy + i*2
		if y >= r.Y+r.H {
			break
		}
		key := a.keyLabel(it.cmd)
		mid := r.X + r.W/2
		lw := StrWidth(it.label)
		a.cv.Text(mid-lw-1, y, r.X+r.W, it.label, labelSt)
		if key != "" {
			x := mid + 1
			for j, part := range strings.Split(key, "+") {
				if j > 0 {
					x = a.cv.Text(x, y, r.X+r.W, "+", labelSt)
				}
				x = a.cv.Text(x, y, r.X+r.W, " "+part+" ", keySt)
			}
		}
		cmd := it.cmd
		a.addHit(Rect{r.X, y, r.W, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.RunCommand(cmd, nil)
				return true
			}
			return false
		})
	}
}

// closeTab closes a tab, asking to save if needed.
func (a *App) closeTab(g *EditorGroup, idx int) {
	if idx < 0 || idx >= len(g.tabs) {
		return
	}
	v := g.tabs[idx]
	if v.special == nil && v.buf().Dirty() && v.doc.refs <= 1 && !v.doc.Virtual {
		name := v.doc.Title()
		a.showDialog("Do you want to save the changes you made to "+name+"?", "Your changes will be lost if you don't save them.",
			[]string{"Save", "Don't Save", "Cancel"}, func(choice int) {
				switch choice {
				case 0:
					if a.saveDocument(v.doc, false) {
						a.closeTabNow(g, g.indexOf(v))
					}
				case 1:
					a.closeTabNow(g, g.indexOf(v))
				}
			})
		return
	}
	a.closeTabNow(g, idx)
}

func (a *App) closeTabNow(g *EditorGroup, idx int) {
	if idx < 0 || idx >= len(g.tabs) {
		return
	}
	v := g.tabs[idx]
	if v.special == nil && v.buf().Path != "" {
		a.closedTabs = append(a.closedTabs, closedTab{path: v.buf().Path, group: a.groupIndex(g), pos: v.Cursor()})
	}
	g.removeTab(idx)
	if len(g.tabs) == 0 && len(a.groups) > 1 {
		a.removeGroupAt(a.groupIndex(g))
	}
}

// disposeView releases an editor view and closes its document when unused.
func (a *App) disposeView(v *EditorView) {
	if v.special != nil {
		return
	}
	v.dispose()
	delete(a.autoClosed, v)
	if v.doc.refs <= 0 {
		a.closeDocument(v.doc)
	}
}

func (a *App) closeDocument(d *Document) {
	if a.lsp != nil {
		a.lsp.Close(d.Buf)
	}
	d.Buf.RemoveListener(d.listener)
	for k, x := range a.docs {
		if x == d {
			delete(a.docs, k)
		}
	}
}

// onActiveEditorChanged updates dependent views.
func (a *App) onActiveEditorChanged() {
	if a.outline != nil {
		a.outline.refresh()
	}
	if a.explorer != nil && a.settings.Bool("explorer.autoReveal", "") {
		if v := a.ActiveView(); v != nil && v.special == nil && v.buf().Path != "" {
			a.explorer.reveal(v.buf().Path, false)
		}
	}
}

// pushNav records the current location for navigate back.
func (a *App) pushNav() {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		return
	}
	e := navEntry{path: v.buf().Path, pos: v.Cursor(), doc: v.doc}
	if n := len(a.navBack); n > 0 && a.navBack[n-1].doc == e.doc && abs(a.navBack[n-1].pos.Line-e.pos.Line) < 3 {
		a.navBack[n-1] = e
	} else {
		a.navBack = append(a.navBack, e)
	}
	if len(a.navBack) > 50 {
		a.navBack = a.navBack[1:]
	}
	a.navForward = nil
}

func (a *App) navigate(back bool) {
	from, to := &a.navBack, &a.navForward
	if !back {
		from, to = &a.navForward, &a.navBack
	}
	if len(*from) == 0 {
		return
	}
	e := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	if v := a.ActiveView(); v != nil && v.special == nil {
		*to = append(*to, navEntry{path: v.buf().Path, pos: v.Cursor(), doc: v.doc})
	}
	a.openNavEntry(e)
}

func (a *App) openNavEntry(e navEntry) {
	var v *EditorView
	if e.path != "" {
		v = a.OpenFile(e.path, false)
	} else if e.doc != nil {
		v = a.showDocument(e.doc, false)
	}
	if v != nil {
		v.SetCursor(v.buf().Clamp(e.pos))
		v.ensurePosVisible(v.Cursor(), true)
		a.focusEditor()
	}
}

func (a *App) recordEditLocation(v *EditorView) {
	e := navEntry{path: v.buf().Path, pos: v.Cursor(), doc: v.doc}
	if n := len(a.lastEdits); n > 0 && a.lastEdits[n-1].doc == e.doc && abs(a.lastEdits[n-1].pos.Line-e.pos.Line) < 5 {
		a.lastEdits[n-1] = e
		return
	}
	a.lastEdits = append(a.lastEdits, e)
	if len(a.lastEdits) > 30 {
		a.lastEdits = a.lastEdits[1:]
	}
}

// showDocument opens a view for an already-loaded document in the active group.
func (a *App) showDocument(d *Document, preview bool) *EditorView {
	g := a.ActiveGroup()
	for i, t := range g.tabs {
		if t.special == nil && t.doc == d {
			g.setActive(i)
			if !preview {
				t.preview = false
			}
			return t
		}
	}
	v := a.newEditorView(d)
	g.addTab(v, preview)
	return v
}
