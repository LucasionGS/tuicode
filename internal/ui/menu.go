package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

// MenuItem is an entry in a menu or context menu.
type MenuItem struct {
	Label     string
	Command   string
	Args      any
	Run       func()
	Separator bool
	Disabled  bool
	Checked   func() bool
	Submenu   []MenuItem
	Key       string
}

// MenuBar is the File/Edit/... menu in the title bar.
type MenuBar struct {
	app   *App
	open  int
	hover int
	itemX []int
	popup *ContextMenu
}

func newMenuBar(a *App) *MenuBar {
	return &MenuBar{app: a, open: -1, hover: -1}
}

func (m *MenuBar) contains(x, y int) bool {
	if m.app.rTitle.Contains(x, y) {
		return true
	}
	if m.popup != nil && m.popup.containsDeep(x, y) {
		return true
	}
	return false
}

func (m *MenuBar) close() {
	m.open = -1
	m.popup = nil
	if m.app.focus == FocusMenu {
		m.app.focus = m.app.prevFocus
		if m.app.focus == FocusMenu {
			m.app.focus = FocusEditor
		}
	}
}

func (m *MenuBar) toggle(i int) {
	if m.open == i {
		m.close()
		return
	}
	m.openMenu(i)
}

func (m *MenuBar) openMenu(i int) {
	a := m.app
	if i < 0 || i >= len(menuNames) {
		return
	}
	if a.focus != FocusMenu {
		a.prevFocus = a.focus
	}
	a.focus = FocusMenu
	m.open = i
	x := 1
	if i < len(m.itemX) {
		x = m.itemX[i]
	}
	m.popup = &ContextMenu{app: a, items: a.menuItems(menuNames[i]), x: x, y: a.rTitle.Y + 1, selected: -1}
	m.popup.onClose = func() { m.close() }
}

func (m *MenuBar) drawDropdown() {
	if m.open >= 0 && m.popup != nil {
		m.popup.draw()
	}
}

func (m *MenuBar) handleKey(key string) bool {
	if m.popup == nil {
		return false
	}
	switch key {
	case "left":
		if m.popup.child == nil {
			m.openMenu((m.open - 1 + len(menuNames)) % len(menuNames))
			return true
		}
	case "right":
		if m.popup.child == nil && (m.popup.selected < 0 || len(m.popup.items[m.popup.selected].Submenu) == 0) {
			m.openMenu((m.open + 1) % len(menuNames))
			return true
		}
	case "escape":
		if m.popup.child == nil {
			m.close()
			return true
		}
	}
	return m.popup.handleKey(key)
}

func (a *App) menuItems(name string) []MenuItem {
	sep := MenuItem{Separator: true}
	on := func(b *bool) func() bool { return func() bool { return *b } }
	switch name {
	case "File":
		return []MenuItem{
			{Label: "New Text File", Command: "workbench.action.files.newUntitledFile"},
			{Label: "New File...", Command: "explorer.newFile"},
			{Label: "New Window", Command: "workbench.action.newWindow"},
			sep,
			{Label: "Open File...", Command: "workbench.action.files.openFile"},
			{Label: "Open Folder...", Command: "workbench.action.files.openFolder"},
			{Label: "Open Recent", Command: "workbench.action.openRecent"},
			sep,
			{Label: "Save", Command: "workbench.action.files.save"},
			{Label: "Save As...", Command: "workbench.action.files.saveAs"},
			{Label: "Save All", Command: "workbench.action.files.saveAll"},
			sep,
			{Label: "Auto Save", Run: func() { a.toggleAutoSave() }, Checked: func() bool { return a.settings.String("files.autoSave", "") != "off" }},
			{Label: "Preferences", Submenu: []MenuItem{
				{Label: "Settings", Command: "workbench.action.openSettings"},
				{Label: "Keyboard Shortcuts", Command: "workbench.action.openGlobalKeybindings"},
				sep,
				{Label: "Color Theme", Command: "workbench.action.selectTheme"},
				{Label: "File Icon Theme", Command: "workbench.action.selectIconTheme"},
				sep,
				{Label: "User Settings (JSON)", Command: "workbench.action.openSettingsJson"},
				{Label: "Keyboard Shortcuts (JSON)", Command: "workbench.action.openGlobalKeybindingsFile"},
			}},
			sep,
			{Label: "Revert File", Command: "workbench.action.files.revert"},
			{Label: "Close Editor", Command: "workbench.action.closeActiveEditor"},
			{Label: "Close Folder", Command: "workbench.action.closeFolder"},
			sep,
			{Label: "Exit", Command: "workbench.action.quit"},
		}
	case "Edit":
		return []MenuItem{
			{Label: "Undo", Command: "undo"},
			{Label: "Redo", Command: "redo"},
			sep,
			{Label: "Cut", Command: "editor.action.clipboardCutAction"},
			{Label: "Copy", Command: "editor.action.clipboardCopyAction"},
			{Label: "Paste", Command: "editor.action.clipboardPasteAction"},
			sep,
			{Label: "Find", Command: "actions.find"},
			{Label: "Replace", Command: "editor.action.startFindReplaceAction"},
			sep,
			{Label: "Find in Files", Command: "workbench.action.findInFiles"},
			{Label: "Replace in Files", Command: "workbench.action.replaceInFiles"},
			sep,
			{Label: "Toggle Line Comment", Command: "editor.action.commentLine"},
			{Label: "Toggle Block Comment", Command: "editor.action.blockComment"},
		}
	case "Selection":
		return []MenuItem{
			{Label: "Select All", Command: "editor.action.selectAll"},
			{Label: "Expand Selection", Command: "editor.action.smartSelect.expand"},
			{Label: "Shrink Selection", Command: "editor.action.smartSelect.shrink"},
			sep,
			{Label: "Copy Line Up", Command: "editor.action.copyLinesUpAction"},
			{Label: "Copy Line Down", Command: "editor.action.copyLinesDownAction"},
			{Label: "Move Line Up", Command: "editor.action.moveLinesUpAction"},
			{Label: "Move Line Down", Command: "editor.action.moveLinesDownAction"},
			{Label: "Duplicate Selection", Command: "editor.action.duplicateSelection"},
			sep,
			{Label: "Add Cursor Above", Command: "editor.action.insertCursorAbove"},
			{Label: "Add Cursor Below", Command: "editor.action.insertCursorBelow"},
			{Label: "Add Cursors to Line Ends", Command: "editor.action.insertCursorAtEndOfEachLineSelected"},
			{Label: "Add Next Occurrence", Command: "editor.action.addSelectionToNextFindMatch"},
			{Label: "Select All Occurrences", Command: "editor.action.selectHighlights"},
			sep,
			{Label: "Column Selection Mode", Command: "editor.action.toggleColumnSelection"},
		}
	case "View":
		return []MenuItem{
			{Label: "Command Palette...", Command: "workbench.action.showCommands"},
			sep,
			{Label: "Appearance", Submenu: []MenuItem{
				{Label: "Zen Mode", Command: "workbench.action.toggleZenMode", Checked: on(&a.zenMode)},
				sep,
				{Label: "Menu Bar", Command: "workbench.action.toggleMenuBar", Checked: on(&a.titleVisible)},
				{Label: "Primary Side Bar", Command: "workbench.action.toggleSidebarVisibility", Checked: on(&a.sidebarVisible)},
				{Label: "Status Bar", Command: "workbench.action.toggleStatusbarVisibility", Checked: on(&a.statusVisible)},
				{Label: "Activity Bar", Command: "workbench.action.toggleActivityBarVisibility", Checked: on(&a.activityVisible)},
				{Label: "Panel", Command: "workbench.action.togglePanel", Checked: on(&a.panelVisible)},
				sep,
				{Label: "Minimap", Command: "editor.action.toggleMinimap", Checked: on(&a.minimapEnabled)},
				{Label: "Breadcrumbs", Command: "breadcrumbs.toggle", Checked: on(&a.breadcrumbs)},
				{Label: "Sticky Scroll", Command: "editor.action.toggleStickyScroll", Checked: on(&a.stickyScroll)},
				{Label: "Render Whitespace", Command: "editor.action.toggleRenderWhitespace", Checked: func() bool { return a.whitespaceRender == "all" }},
				{Label: "Line Numbers", Command: "editor.action.toggleLineNumbers", Checked: func() bool { return a.lineNumbers != "off" }},
			}},
			{Label: "Editor Layout", Submenu: []MenuItem{
				{Label: "Split Right", Command: "workbench.action.splitEditor"},
				{Label: "Single", Command: "workbench.action.editorLayoutSingle"},
			}},
			sep,
			{Label: "Explorer", Command: "workbench.view.explorer"},
			{Label: "Search", Command: "workbench.view.search"},
			{Label: "Source Control", Command: "workbench.view.scm"},
			{Label: "Outline", Command: "outline.focus"},
			{Label: "Language Servers", Command: "workbench.view.extensions"},
			sep,
			{Label: "Problems", Command: "workbench.actions.view.problems"},
			{Label: "Output", Command: "workbench.action.output.toggleOutput"},
			{Label: "Debug Console", Command: "workbench.debug.action.toggleRepl"},
			{Label: "Terminal", Command: "workbench.action.terminal.toggleTerminal"},
			sep,
			{Label: "Word Wrap", Command: "editor.action.toggleWordWrap", Checked: func() bool { v := a.ActiveView(); return v != nil && v.wrap }},
		}
	case "Go":
		return []MenuItem{
			{Label: "Back", Command: "workbench.action.navigateBack"},
			{Label: "Forward", Command: "workbench.action.navigateForward"},
			{Label: "Last Edit Location", Command: "workbench.action.navigateToLastEditLocation"},
			sep,
			{Label: "Switch Editor", Submenu: []MenuItem{
				{Label: "Next Editor", Command: "workbench.action.nextEditor"},
				{Label: "Previous Editor", Command: "workbench.action.previousEditor"},
				sep,
				{Label: "Next Used Editor", Command: "workbench.action.quickOpenNavigateNextInEditorPicker"},
				{Label: "Previous Used Editor", Command: "workbench.action.quickOpenNavigatePreviousInEditorPicker"},
			}},
			{Label: "Switch Group", Submenu: []MenuItem{
				{Label: "Group 1", Command: "workbench.action.focusFirstEditorGroup"},
				{Label: "Group 2", Command: "workbench.action.focusSecondEditorGroup"},
				{Label: "Group 3", Command: "workbench.action.focusThirdEditorGroup"},
				sep,
				{Label: "Next Group", Command: "workbench.action.focusRightGroup"},
				{Label: "Previous Group", Command: "workbench.action.focusLeftGroup"},
			}},
			sep,
			{Label: "Go to File...", Command: "workbench.action.quickOpen"},
			{Label: "Go to Symbol in Workspace...", Command: "workbench.action.showAllSymbols"},
			sep,
			{Label: "Go to Symbol in Editor...", Command: "workbench.action.gotoSymbol"},
			{Label: "Go to Definition", Command: "editor.action.revealDefinition"},
			{Label: "Go to Declaration", Command: "editor.action.goToDeclaration"},
			{Label: "Go to Type Definition", Command: "editor.action.goToTypeDefinition"},
			{Label: "Go to Implementations", Command: "editor.action.goToImplementation"},
			{Label: "Go to References", Command: "editor.action.goToReferences"},
			sep,
			{Label: "Go to Line/Column...", Command: "workbench.action.gotoLine"},
			{Label: "Go to Bracket", Command: "editor.action.jumpToBracket"},
			sep,
			{Label: "Next Problem", Command: "editor.action.marker.nextInFiles"},
			{Label: "Previous Problem", Command: "editor.action.marker.prevInFiles"},
			sep,
			{Label: "Next Change", Command: "workbench.action.editor.nextChange"},
			{Label: "Previous Change", Command: "workbench.action.editor.previousChange"},
		}
	case "Run":
		return []MenuItem{
			{Label: "Start Debugging", Command: "workbench.action.debug.start"},
			{Label: "Run Without Debugging", Command: "workbench.action.debug.run"},
			sep,
			{Label: "Run Task...", Command: "workbench.action.tasks.runTask"},
			{Label: "Run Build Task...", Command: "workbench.action.tasks.build"},
			{Label: "Run Test Task...", Command: "workbench.action.tasks.test"},
		}
	case "Terminal":
		return []MenuItem{
			{Label: "New Terminal", Command: "workbench.action.terminal.new"},
			{Label: "Split Terminal", Command: "workbench.action.terminal.split"},
			sep,
			{Label: "Run Task...", Command: "workbench.action.tasks.runTask"},
			{Label: "Run Build Task...", Command: "workbench.action.tasks.build"},
			{Label: "Run Active File", Command: "workbench.action.terminal.runActiveFile"},
			{Label: "Run Selected Text", Command: "workbench.action.terminal.runSelectedText"},
			sep,
			{Label: "Clear Terminal", Command: "workbench.action.terminal.clear"},
			{Label: "Kill Terminal", Command: "workbench.action.terminal.kill"},
			{Label: "Select Default Profile", Command: "workbench.action.terminal.selectDefaultShell"},
		}
	case "Help":
		return []MenuItem{
			{Label: "Welcome", Command: "workbench.action.showWelcomePage"},
			{Label: "Show All Commands", Command: "workbench.action.showCommands"},
			{Label: "Keyboard Shortcuts Reference", Command: "workbench.action.keybindingsReference"},
			sep,
			{Label: "Show Language Server Output", Command: "editor.action.showLanguageServerLog"},
			{Label: "Restart Language Servers", Command: "editor.action.restartLanguageServer"},
			sep,
			{Label: "Check for Updates...", Command: "update.checkForUpdate"},
			sep,
			{Label: "About", Command: "workbench.action.showAboutDialog"},
		}
	}
	return nil
}

func (a *App) toggleAutoSave() {
	val := "afterDelay"
	if a.settings.String("files.autoSave", "") != "off" {
		val = "off"
	}
	if err := setUserSetting(a, "files.autoSave", val); err != nil {
		a.notify(NotifyError, err.Error())
	}
}

//#region Context menu

// ContextMenu is a popup list of menu items.
type ContextMenu struct {
	app      *App
	items    []MenuItem
	x, y     int
	selected int
	rect     Rect
	child    *ContextMenu
	parent   *ContextMenu
	onClose  func()
}

func (a *App) openContextMenu(x, y int, items []MenuItem) {
	a.menu.close()
	a.ctxMenu = &ContextMenu{app: a, items: items, x: x, y: y, selected: -1}
}

func (m *ContextMenu) containsDeep(x, y int) bool {
	if m.rect.Contains(x, y) {
		return true
	}
	return m.child != nil && m.child.containsDeep(x, y)
}

func (m *ContextMenu) closeAll() {
	root := m
	for root.parent != nil {
		root = root.parent
	}
	if root.onClose != nil {
		root.onClose()
	}
	if root.app.ctxMenu == root {
		root.app.ctxMenu = nil
	}
}

func (m *ContextMenu) activate(i int) {
	if i < 0 || i >= len(m.items) {
		return
	}
	it := m.items[i]
	if it.Separator || it.Disabled {
		return
	}
	if len(it.Submenu) > 0 {
		m.openChild(i)
		return
	}
	m.closeAll()
	if it.Run != nil {
		it.Run()
	} else if it.Command != "" {
		m.app.RunCommand(it.Command, it.Args)
	}
}

func (m *ContextMenu) openChild(i int) {
	it := m.items[i]
	m.child = &ContextMenu{app: m.app, items: it.Submenu, x: m.rect.X + m.rect.W, y: m.rect.Y + i, selected: 0, parent: m}
}

func (m *ContextMenu) handleKey(key string) bool {
	if m.child != nil {
		if key == "left" || key == "escape" {
			m.child = nil
			return true
		}
		return m.child.handleKey(key)
	}
	move := func(d int) {
		n := len(m.items)
		for i := 0; i < n; i++ {
			m.selected = (m.selected + d + n) % n
			if !m.items[m.selected].Separator {
				return
			}
		}
	}
	switch key {
	case "down":
		move(1)
	case "up":
		if m.selected < 0 {
			m.selected = 0
		}
		move(-1)
	case "home":
		m.selected = -1
		move(1)
	case "end":
		m.selected = 0
		move(-1)
	case "enter", "space":
		m.activate(m.selected)
	case "right":
		if m.selected >= 0 && len(m.items[m.selected].Submenu) > 0 {
			m.openChild(m.selected)
		}
	case "escape":
		m.closeAll()
	default:
		// first-letter navigation
		if len(key) == 1 {
			for i, it := range m.items {
				if !it.Separator && strings.HasPrefix(strings.ToLower(it.Label), key) {
					m.selected = i
					return true
				}
			}
		}
		return false
	}
	return true
}

func (m *ContextMenu) draw() {
	a := m.app
	th := a.theme
	w := 10
	for _, it := range m.items {
		lw := StrWidth(it.Label) + 6
		k := it.Key
		if k == "" && it.Command != "" {
			k = a.keyLabel(it.Command)
		}
		if k != "" {
			lw += StrWidth(k) + 4
		}
		if lw > w {
			w = lw
		}
	}
	w = min(w, a.width-2)
	h := len(m.items) + 2
	x, y := m.x, m.y
	if x+w > a.width {
		if m.parent != nil {
			x = m.parent.rect.X - w
		} else {
			x = a.width - w
		}
	}
	if x < 0 {
		x = 0
	}
	if y+h > a.height {
		y = max(a.height-h, 0)
	}
	m.rect = Rect{x, y, w, min(h, a.height)}
	mark := a.hitMark()
	a.cv.clip = Rect{0, 0, a.width, a.height}
	bgc := th.C("menu.background")
	fgc := th.C("menu.foreground")
	base := styleFB(fgc, bgc)
	a.cv.Fill(m.rect, ' ', base)
	a.cv.Box(m.rect, styleFB(th.OverColor("menu.border", bgc), bgc))
	for i, it := range m.items {
		ry := y + 1 + i
		if ry >= y+m.rect.H-1 {
			break
		}
		if it.Separator {
			for sx := x + 1; sx < x+w-1; sx++ {
				a.cv.Set(sx, ry, '─', styleFB(th.OverColor("menu.separatorBackground", bgc), bgc))
			}
			continue
		}
		hover := a.mouseY == ry && a.mouseX > x && a.mouseX < x+w-1
		sel := i == m.selected || hover
		st := base
		if it.Disabled {
			st = st.Foreground(th.OverColor("disabledForeground", bgc))
			if !th.Has("disabledForeground") {
				st = st.Foreground(th.OverColor("descriptionForeground", bgc))
			}
		} else if sel {
			st = styleFB(th.C("menu.selectionForeground"), th.C("menu.selectionBackground"))
		}
		a.cv.Fill(Rect{x + 1, ry, w - 2, 1}, ' ', st)
		if it.Checked != nil && it.Checked() {
			a.cv.Text(x+1, ry, x+3, a.Icon(IcoCheck), st)
		}
		a.cv.Text(x+3, ry, x+w-2, it.Label, st)
		right := it.Key
		if right == "" && it.Command != "" {
			right = a.keyLabel(it.Command)
		}
		if len(it.Submenu) > 0 {
			right = a.Icon(IcoChevronRight)
		}
		if right != "" {
			rst := st
			if !sel {
				rst = st.Foreground(th.OverColor("descriptionForeground", bgc))
			}
			a.cv.TextRight(x+1, ry, w-3, right, rst)
		}
		idx := i
		a.addHit(Rect{x + 1, ry, w - 2, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				m.selected = idx
				m.activate(idx)
			case MouseMove:
				m.selected = idx
				if len(m.items[idx].Submenu) > 0 {
					if m.child == nil || m.child.y != m.rect.Y+idx {
						m.openChild(idx)
					}
				} else {
					m.child = nil
				}
			}
			return true
		})
	}
	a.addHitUnder(mark, m.rect, func(ev MouseEvent) bool { return true })
	if m.child != nil {
		m.child.draw()
	}
}

//#endregion

//#region Dialog

// Dialog is a modal message box.
type Dialog struct {
	app      *App
	title    string
	message  string
	buttons  []string
	selected int
	done     func(int)
}

func (a *App) showDialog(title, message string, buttons []string, done func(int)) {
	a.closeMenus()
	if a.focus != FocusDialog {
		a.prevFocus = a.focus
	}
	a.dialog = &Dialog{app: a, title: title, message: message, buttons: buttons, done: done}
	a.focus = FocusDialog
}

func (d *Dialog) finish(i int) {
	a := d.app
	a.dialog = nil
	a.focus = a.prevFocus
	if a.focus == FocusDialog {
		a.focus = FocusEditor
	}
	if d.done != nil {
		d.done(i)
	}
}

func (d *Dialog) handleKey(key, text string, isText bool) {
	switch key {
	case "left", "shift+tab":
		d.selected = (d.selected - 1 + len(d.buttons)) % len(d.buttons)
	case "right", "tab":
		d.selected = (d.selected + 1) % len(d.buttons)
	case "enter", "space":
		d.finish(d.selected)
	case "escape":
		d.finish(len(d.buttons) - 1)
	default:
		// mnemonic: first letter of a button
		if isText {
			for i, b := range d.buttons {
				if strings.HasPrefix(strings.ToLower(b), strings.ToLower(text)) {
					d.finish(i)
					return
				}
			}
		}
	}
}

func (d *Dialog) draw() {
	a := d.app
	th := a.theme
	msgLines := []string{}
	for _, l := range strings.Split(d.message, "\n") {
		msgLines = append(msgLines, wrapText(l, 56)...)
	}
	w := 60
	btnW := 0
	for _, b := range d.buttons {
		btnW += StrWidth(b) + 5
	}
	w = max(w, btnW+4)
	w = min(w, a.width-2)
	h := 5 + len(msgLines)
	x := (a.width - w) / 2
	y := max((a.height-h)/3, 0)
	r := Rect{x, y, w, h}
	// dim the background
	for yy := 0; yy < a.height; yy++ {
		for xx := 0; xx < a.width; xx++ {
			ch, _, st, _ := a.screen.GetContent(xx, yy)
			fg, bg, _ := st.Decompose()
			a.cv.Set(xx, yy, ch, st.Foreground(blendTcell(fg, tcell.ColorBlack)).Background(blendTcell(bg, tcell.ColorBlack)))
		}
	}
	bgc := th.C("editorWidget.background")
	base := styleFB(th.C("editorWidget.foreground"), bgc)
	a.cv.Fill(r, ' ', base)
	a.cv.Box(r, styleFB(th.OverColor("editorWidget.border", bgc), bgc))
	icon := a.Icon(IcoWarning)
	a.cv.Text(x+2, y+1, x+4, icon, base.Foreground(th.C("editorWarning.foreground")))
	a.cv.TextEllipsis(x+4, y+1, w-6, d.title, base.Bold(true))
	for i, l := range msgLines {
		a.cv.Text(x+4, y+2+i, x+w-2, l, base.Foreground(th.C("descriptionForeground")))
	}
	bx := x + w - 2
	by := y + h - 2
	for i := len(d.buttons) - 1; i >= 0; i-- {
		label := " " + d.buttons[i] + " "
		bw := StrWidth(label)
		bx -= bw
		st := styleFB(th.C("button.secondaryForeground"), th.C("button.secondaryBackground"))
		if i == 0 {
			st = styleFB(th.C("button.foreground"), th.C("button.background"))
		}
		if i == d.selected {
			st = st.Underline(tcell.UnderlineStyleSolid, th.C("focusBorder")).Bold(true)
		}
		a.cv.Text(bx, by, bx+bw, label, st)
		idx := i
		a.addHit(Rect{bx, by, bw, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				d.finish(idx)
			}
			return true
		})
		bx -= 2
	}
	a.addHit(Rect{0, 0, a.width, a.height}, func(ev MouseEvent) bool {
		return !r.Contains(ev.X, ev.Y) || true
	})
	// re-register button hits above the blocker
	bx = x + w - 2
	for i := len(d.buttons) - 1; i >= 0; i-- {
		label := " " + d.buttons[i] + " "
		bw := StrWidth(label)
		bx -= bw
		idx := i
		a.addHit(Rect{bx, by, bw, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				d.finish(idx)
			}
			return true
		})
		bx -= 2
	}
}

func wrapText(s string, w int) []string {
	if StrWidth(s) <= w {
		return []string{s}
	}
	var out []string
	cur := ""
	for _, word := range strings.Fields(s) {
		if cur == "" {
			cur = word
		} else if StrWidth(cur)+1+StrWidth(word) <= w {
			cur += " " + word
		} else {
			out = append(out, cur)
			cur = word
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

//#endregion
