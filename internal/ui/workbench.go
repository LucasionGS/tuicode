package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/config"
	"tuicode/internal/lsp"
)

const activityWidth = 3

// cursor request for the frame
type cursorReq struct {
	x, y    int
	visible bool
	style   tcell.CursorStyle
}

func (a *App) layout() {
	w, h := a.width, a.height
	y0 := 0
	a.rTitle = Rect{}
	if a.titleVisible && !a.zenMode {
		a.rTitle = Rect{0, 0, w, 1}
		y0 = 1
	}
	statusH := 0
	a.rStatus = Rect{}
	if a.statusVisible && !a.zenMode {
		statusH = 1
		a.rStatus = Rect{0, h - 1, w, 1}
	}
	mainH := max(h-y0-statusH, 1)
	x := 0
	a.rActivity = Rect{}
	if a.activityVisible && !a.zenMode {
		a.rActivity = Rect{0, y0, activityWidth, mainH}
		x = activityWidth
	}
	a.rSidebar = Rect{}
	if a.sidebarVisible && !a.zenMode {
		sw := clampInt(a.sidebarWidth, 15, max(w-x-20, 15))
		a.rSidebar = Rect{x, y0, sw, mainH}
		x += sw
	}
	area := Rect{x, y0, max(w-x, 1), mainH}
	a.rPanel = Rect{}
	if a.panelVisible && !a.zenMode {
		ph := clampInt(a.panelHeight, 4, max(mainH-4, 4))
		if a.panelMaximized {
			ph = mainH
		}
		a.rPanel = Rect{area.X, area.Y + area.H - ph, area.W, ph}
		area.H -= ph
	}
	a.rEditor = area
}

func (a *App) draw() {
	if a.screen == nil {
		return
	}
	a.hits = a.hits[:0]
	a.cursor = cursorReq{}
	a.layout()
	th := a.theme
	bg := styleFB(th.C("editor.foreground"), th.C("editor.background"))
	a.cv.clip = Rect{0, 0, a.width, a.height}
	a.cv.Fill(Rect{0, 0, a.width, a.height}, ' ', bg)

	a.drawEditorArea()
	if !a.rPanel.Empty() {
		a.drawPanel()
	}
	if !a.rSidebar.Empty() {
		a.drawSidebar()
	}
	if !a.rActivity.Empty() {
		a.drawActivityBar()
	}
	if !a.rStatus.Empty() {
		a.drawStatusBar()
	}
	if !a.rTitle.Empty() {
		a.drawTitleBar()
	}
	a.cv.clip = Rect{0, 0, a.width, a.height}
	// overlays
	a.drawNotifications()
	if a.quick != nil {
		a.quick.draw()
	}
	a.menu.drawDropdown()
	if a.ctxMenu != nil {
		a.ctxMenu.draw()
	}
	if a.dialog != nil {
		a.dialog.draw()
	}
	if a.tooltip != nil {
		a.drawTooltip()
	}
	if a.cursor.visible {
		a.screen.SetCursorStyle(a.cursor.style)
		a.screen.ShowCursor(a.cursor.x, a.cursor.y)
	} else {
		a.screen.HideCursor()
	}
	a.screen.Show()
	a.applyTerminalTitleIfChanged()
}

func (a *App) setCursor(x, y int, style tcell.CursorStyle) {
	a.cursor = cursorReq{x: x, y: y, visible: true, style: style}
}

func (a *App) applyTerminalTitleIfChanged() {
	t := a.windowTitle()
	if t != a.lastTitle {
		a.lastTitle = t
		a.screen.SetTitle(t)
	}
}

//#region Title bar

var menuNames = []string{"File", "Edit", "Selection", "View", "Go", "Run", "Terminal", "Help"}

func (a *App) drawTitleBar() {
	th := a.theme
	r := a.rTitle
	a.cv.clip = r
	st := styleFB(th.C("titleBar.activeForeground"), th.C("titleBar.activeBackground"))
	a.cv.Fill(r, ' ', st)
	x := r.X + 1
	a.menu.itemX = a.menu.itemX[:0]
	for i, name := range menuNames {
		label := " " + name + " "
		w := StrWidth(label)
		ist := st
		if a.menu.open == i || (a.menu.hover == i && a.menu.open < 0) {
			ist = styleFB(th.C("menubar.selectionForeground"), th.Over("menubar.selectionBackground", "titleBar.activeBackground"))
		}
		if x+w >= r.X+r.W/2-12 && i > 0 {
			// narrow terminal: collapse remaining menus into a hamburger
			label = " ≡ "
			w = 3
			a.cv.Text(x, r.Y, r.X+r.W, label, ist)
			idx := i
			a.menu.itemX = append(a.menu.itemX, x)
			a.addHit(Rect{x, r.Y, w, 1}, func(ev MouseEvent) bool {
				if ev.Kind == MousePress {
					a.menu.toggle(idx)
				}
				return true
			})
			x += w
			break
		}
		a.cv.Text(x, r.Y, r.X+r.W, label, ist)
		idx := i
		a.menu.itemX = append(a.menu.itemX, x)
		a.addHit(Rect{x, r.Y, w, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.menu.toggle(idx)
			case MouseMove:
				a.menu.hover = idx
				if a.menu.open >= 0 && a.menu.open != idx {
					a.menu.openMenu(idx)
				}
			}
			return true
		})
		x += w
	}
	menuEnd := x
	// command center
	label := a.rootName()
	if label == "" {
		label = AppName
	}
	if a.lspBusy() {
		label += " (indexing…)"
	}
	ccText := a.Icon(IcoSearch) + " " + label
	ccW := clampInt(StrWidth(ccText)+16, 20, 50)
	cx := r.X + (r.W-ccW)/2
	rightW := 10
	if cx < menuEnd+1 {
		cx = menuEnd + 1
	}
	if cx+ccW > r.X+r.W-rightW {
		ccW = r.X + r.W - rightW - cx
	}
	if a.drawDebugToolbar(Rect{cx, r.Y, ccW, 1}) {
		ccW = 0
	}
	if ccW >= 10 {
		ccBg := th.Over("commandCenter.background", "titleBar.activeBackground")
		if !th.Has("commandCenter.background") {
			ccBg = th.OverColor("input.background", th.C("titleBar.activeBackground"))
		}
		ccSt := styleFB(th.C("titleBar.activeForeground"), ccBg)
		a.cv.Fill(Rect{cx, r.Y, ccW, 1}, ' ', ccSt)
		a.cv.TextCenter(cx, r.Y, ccW, ccText, ccSt)
		a.addHit(Rect{cx, r.Y, ccW, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.RunCommand("workbench.action.quickOpen", nil)
			}
			return true
		})
	}
	// layout toggles on the right
	rx := r.X + r.W - 1
	type toggle struct {
		icon string
		cmd  string
		on   bool
	}
	toggles := []toggle{
		{"▣", "workbench.action.togglePanel", a.panelVisible},
		{"◧", "workbench.action.toggleSidebarVisibility", a.sidebarVisible},
	}
	if a.nerdIcons {
		toggles[0].icon = ""
		toggles[1].icon = ""
	}
	for _, t := range toggles {
		rx -= 2
		tst := st
		if !t.on {
			tst = tst.Foreground(th.Over("descriptionForeground", "titleBar.activeBackground"))
		}
		a.cv.Text(rx, r.Y, rx+1, t.icon, tst)
		cmd := t.cmd
		a.addHit(Rect{rx, r.Y, 1, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.RunCommand(cmd, nil)
			}
			return true
		})
	}
}

func (a *App) lspBusy() bool {
	return a.lsp != nil && len(a.lsp.Progress()) > 0
}

//#endregion

//#region Activity bar

type activityItem struct {
	id    string
	icon  int
	title string
	cmd   string
}

var activityItems = []activityItem{
	{"explorer", IcoExplorer, "Explorer (Ctrl+Shift+E)", "workbench.view.explorer"},
	{"search", IcoSearch, "Search (Ctrl+Shift+F)", "workbench.view.search"},
	{"scm", IcoSCM, "Source Control (Ctrl+Shift+G)", "workbench.view.scm"},
	{"debug", IcoDebug, "Run and Debug (Ctrl+Shift+D)", "workbench.view.debug"},
	{"extensions", IcoExtensions, "Language Servers (Ctrl+Shift+X)", "workbench.view.extensions"},
}

func (a *App) drawActivityBar() {
	th := a.theme
	r := a.rActivity
	a.cv.clip = r
	bgc := th.C("activityBar.background")
	a.cv.Fill(r, ' ', styleFB(th.C("activityBar.foreground"), bgc))
	y := r.Y
	for _, it := range activityItems {
		if y+1 >= r.Y+r.H {
			break
		}
		active := a.sidebarVisible && a.sidebarView == it.id
		fg := th.C("activityBar.inactiveForeground")
		if active {
			fg = th.C("activityBar.foreground")
		}
		hovered := a.mouseY >= y && a.mouseY <= y+1 && r.Contains(a.mouseX, a.mouseY)
		if hovered {
			fg = th.C("activityBar.foreground")
		}
		st := styleFB(fg, bgc)
		if active {
			a.cv.Set(r.X, y, '▎', styleFB(th.C("activityBar.activeBorder"), bgc))
			a.cv.Set(r.X, y+1, '▎', styleFB(th.C("activityBar.activeBorder"), bgc))
		}
		a.cv.Text(r.X+1, y, r.X+r.W, a.Icon(it.icon), st)
		// badges
		badge := 0
		switch it.id {
		case "scm":
			badge = len(a.gitStatus)
		}
		if badge > 0 {
			txt := fmt.Sprint(badge)
			if badge > 99 {
				txt = "9+"
			}
			bst := styleFB(th.C("activityBarBadge.foreground"), th.C("activityBarBadge.background"))
			a.cv.Text(r.X+r.W-len(txt), y+1, r.X+r.W, txt, bst)
		}
		cmd := it.cmd
		id := it.id
		title := it.title
		a.addHit(Rect{r.X, y, r.W, 2}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				if a.sidebarVisible && a.sidebarView == id {
					a.sidebarVisible = false
					a.focusEditor()
				} else {
					a.RunCommand(cmd, nil)
				}
			case MouseMove:
				a.showTooltipAt(r.X+r.W, ev.Y, title)
			}
			return true
		})
		y += 2
	}
	// bottom items: manage
	gy := r.Y + r.H - 2
	if gy > y {
		st := styleFB(th.C("activityBar.inactiveForeground"), bgc)
		a.cv.Text(r.X+1, gy, r.X+r.W, a.Icon(IcoSettings), st)
		a.addHit(Rect{r.X, gy, r.W, 2}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.showManageMenu(r.X+r.W, gy)
			}
			return true
		})
		ay := gy - 2
		if ay > y {
			a.cv.Text(r.X+1, ay, r.X+r.W, a.Icon(IcoAccount), st)
		}
	}
}

func (a *App) showManageMenu(x, y int) {
	items := []MenuItem{
		{Label: "Command Palette...", Command: "workbench.action.showCommands"},
		{Separator: true},
		{Label: "Settings", Command: "workbench.action.openSettings"},
		{Label: "Keyboard Shortcuts", Command: "workbench.action.openGlobalKeybindings"},
		{Label: "Language Servers", Command: "workbench.view.extensions"},
		{Separator: true},
		{Label: "Themes", Submenu: []MenuItem{
			{Label: "Color Theme", Command: "workbench.action.selectTheme"},
			{Label: "File Icon Theme", Command: "workbench.action.selectIconTheme"},
		}},
		{Separator: true},
		{Label: "Check for Updates...", Command: "update.checkForUpdate"},
	}
	a.openContextMenu(x, max(y-len(items), 0), items)
}

//#endregion

//#region Status bar

type statusItem struct {
	text    string
	cmd     string
	tooltip string
	fg      tcell.Color
	bg      tcell.Color
	hasBg   bool
}

func (a *App) drawStatusBar() {
	th := a.theme
	r := a.rStatus
	a.cv.clip = r
	bgKey := "statusBar.background"
	if a.root == "" {
		bgKey = "statusBar.noFolderBackground"
	}
	debugging := a.debug != nil && a.debug.state != "terminated"
	if debugging {
		bgKey = "statusBar.debuggingBackground"
	}
	bgc := th.C(bgKey)
	fgc := th.C("statusBar.foreground")
	base := styleFB(fgc, bgc)
	a.cv.Fill(r, ' ', base)

	var left, right []statusItem
	// remote indicator
	if th.Has("statusBarItem.remoteBackground") {
		left = append(left, statusItem{text: " " + a.remoteGlyph() + " ", cmd: "workbench.action.showCommands", tooltip: "tuicode", fg: th.C("statusBarItem.remoteForeground"), bg: th.C("statusBarItem.remoteBackground"), hasBg: true})
	}
	if a.gitRoot != "" {
		branch := a.gitBranch
		dirty := false
		for _, s := range a.gitStatus {
			if !s.Untracked() || true {
				dirty = true
				break
			}
		}
		if dirty {
			branch += "*"
		}
		left = append(left, statusItem{text: a.Icon(IcoBranch) + " " + branch, cmd: "git.checkout", tooltip: "Checkout Branch/Tag..."})
		sync := a.Icon(IcoSync)
		if a.gitHasUp && (a.gitAhead > 0 || a.gitBehind > 0) {
			sync += fmt.Sprintf(" %d↓ %d↑", a.gitBehind, a.gitAhead)
		}
		left = append(left, statusItem{text: sync, cmd: "git.sync", tooltip: "Synchronize Changes"})
	}
	if debugging {
		left = append(left, statusItem{text: a.Icon(IcoDebug) + " " + a.debug.name + " (" + a.debug.state + ")", cmd: "workbench.view.debug", tooltip: "Debug: " + a.debug.name})
	}
	errs, warns, infos := a.problemCounts()
	prob := fmt.Sprintf("%s %d  %s %d", a.Icon(IcoError), errs, a.Icon(IcoWarning), warns)
	if infos > 0 {
		prob += fmt.Sprintf("  %s %d", a.Icon(IcoInfo), infos)
	}
	left = append(left, statusItem{text: prob, cmd: "workbench.actions.view.problems", tooltip: "Problems"})
	if a.lsp != nil {
		for _, p := range a.lsp.Progress() {
			msg := p.Title
			if p.Message != "" {
				msg += ": " + p.Message
			}
			if p.Percentage > 0 {
				msg += fmt.Sprintf(" (%d%%)", p.Percentage)
			}
			left = append(left, statusItem{text: "⟳ " + p.Server + " " + msg, cmd: "workbench.action.output.toggleOutput"})
			break
		}
	}
	if len(a.pendingChord) == 0 && a.statusMsg != "" {
		left = append(left, statusItem{text: a.statusMsg})
	} else if len(a.pendingChord) > 0 {
		left = append(left, statusItem{text: a.statusMsg})
	}

	if v := a.ActiveView(); v != nil && a.ActiveGroup().activeIsEditor() {
		p := v.Cursor()
		pos := fmt.Sprintf("Ln %d, Col %d", p.Line+1, v.visualX(p.Line, p.Col)+1)
		if n := len(v.sels); n > 1 {
			pos += fmt.Sprintf(" (%d selections)", n)
		} else if v.HasSelection() {
			sel := v.Primary().Range()
			chars := len([]rune(v.buf().TextRange(sel)))
			pos += fmt.Sprintf(" (%d selected)", chars)
		}
		right = append(right, statusItem{text: pos, cmd: "workbench.action.gotoLine", tooltip: "Go to Line/Column"})
		ind := fmt.Sprintf("Spaces: %d", v.buf().TabSize)
		if !v.buf().InsertSpaces {
			ind = fmt.Sprintf("Tab Size: %d", v.buf().TabSize)
		}
		right = append(right, statusItem{text: ind, cmd: "editor.action.indentationToSpaces.menu", tooltip: "Select Indentation"})
		enc := "UTF-8"
		if v.buf().BOM {
			enc = "UTF-8 with BOM"
		}
		right = append(right, statusItem{text: enc, cmd: "workbench.action.editor.changeEncoding", tooltip: "Select Encoding"})
		eol := "LF"
		if v.buf().EOL == "\r\n" {
			eol = "CRLF"
		}
		right = append(right, statusItem{text: eol, cmd: "workbench.action.editor.changeEOL", tooltip: "Select End of Line Sequence"})
		lang := "{} " + v.doc.Lang.Name
		right = append(right, statusItem{text: lang, cmd: "workbench.action.editor.changeLanguageMode", tooltip: "Select Language Mode"})
		if a.lsp != nil && v.buf().Path != "" {
			if srv := a.lsp.ServerName(v.buf()); srv != "" {
				icon := a.Icon(IcoCheck)
				if a.lsp.Client(v.buf()) == nil {
					icon = "⟳"
				}
				right = append(right, statusItem{text: icon + " " + srv, cmd: "workbench.view.extensions", tooltip: "Language server " + srv})
			}
		}
	}
	bell := a.Icon(IcoBell)
	if len(a.notifications) > 0 || a.unreadNotifications > 0 {
		bell = a.Icon(IcoBellDot)
	}
	right = append(right, statusItem{text: bell, cmd: "notifications.toggleList", tooltip: "Notifications"})

	x := r.X
	for _, it := range left {
		x = a.drawStatusItem(x, r, it, base, true)
		if x >= r.X+r.W {
			break
		}
	}
	// right side, laid out from the right edge
	rx := r.X + r.W
	for i := len(right) - 1; i >= 0; i-- {
		it := right[i]
		w := StrWidth(it.text) + 2
		if rx-w < x+1 {
			break
		}
		rx -= w
		a.drawStatusItem(rx, r, it, base, false)
	}
}

func (a *App) remoteGlyph() string {
	if a.nerdIcons {
		return ""
	}
	return "><"
}

func (a *App) drawStatusItem(x int, r Rect, it statusItem, base tcell.Style, left bool) int {
	th := a.theme
	text := " " + it.text + " "
	if it.hasBg {
		text = it.text
	}
	w := StrWidth(text)
	st := base
	if it.hasBg {
		st = styleFB(it.fg, it.bg)
	}
	hover := a.mouseY == r.Y && a.mouseX >= x && a.mouseX < x+w && it.cmd != ""
	if hover && !it.hasBg {
		st = st.Background(th.OverColor("statusBarItem.hoverBackground", bgOf(st)))
	}
	end := a.cv.Text(x, r.Y, r.X+r.W, text, st)
	if it.cmd != "" {
		cmd := it.cmd
		tip := it.tooltip
		a.addHit(Rect{x, r.Y, w, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.RunCommand(cmd, nil)
			case MouseMove:
				if tip != "" {
					a.showTooltipAt(ev.X, r.Y-1, tip)
				}
			}
			return true
		})
	}
	return end
}

func (a *App) problemCounts() (int, int, int) {
	if a.lsp == nil {
		return 0, 0, 0
	}
	e, w, i := 0, 0, 0
	for _, fds := range a.lsp.AllDiagnostics() {
		for _, fd := range fds {
			for _, d := range fd.Diagnostics {
				switch d.Severity {
				case lsp.SeverityWarning:
					w++
				case lsp.SeverityInformation:
					i++
				case lsp.SeverityHint:
				default:
					e++
				}
			}
		}
	}
	return e, w, i
}

//#endregion

//#region Sidebar

func (a *App) drawSidebar() {
	th := a.theme
	r := a.rSidebar
	a.cv.clip = r
	bgc := th.C("sideBar.background")
	a.cv.Fill(r, ' ', styleFB(th.C("sideBar.foreground"), bgc))
	// title row
	title := ""
	switch a.sidebarView {
	case "explorer":
		title = "EXPLORER"
	case "search":
		title = "SEARCH"
	case "scm":
		title = "SOURCE CONTROL"
	case "outline":
		title = "OUTLINE"
	case "extensions":
		title = "LANGUAGE SERVERS"
	case "debug":
		title = "RUN AND DEBUG"
	}
	tst := styleFB(th.C("sideBarTitle.foreground"), bgc)
	a.cv.Text(r.X+2, r.Y, r.X+r.W-1, title, tst)
	body := Rect{r.X, r.Y + 1, r.W - 1, r.H - 1}
	switch a.sidebarView {
	case "explorer":
		a.explorer.drawActions(Rect{r.X, r.Y, r.W - 1, 1})
		a.explorer.draw(body)
	case "search":
		a.searchV.drawActions(Rect{r.X, r.Y, r.W - 1, 1})
		a.searchV.draw(body)
	case "scm":
		a.scm.drawActions(Rect{r.X, r.Y, r.W - 1, 1})
		a.scm.draw(body)
	case "outline":
		a.outline.draw(body)
	case "extensions":
		a.extView.draw(body)
	case "debug":
		a.debugView.draw(body)
	}
	a.cv.clip = r
	// sash on the right edge
	sash := Rect{r.X + r.W - 1, r.Y, 1, r.H}
	sashColor := bgc
	if th.Defined("sideBar.border") {
		sashColor = th.OverColor("sideBar.border", bgc)
	}
	dragging := a.sashDrag == "sidebar"
	hover := sash.Contains(a.mouseX, a.mouseY)
	for y := sash.Y; y < sash.Y+sash.H; y++ {
		if dragging || hover {
			a.cv.Set(sash.X, y, '▕', styleFB(th.C("sash.hoverBorder"), bgc))
		} else {
			a.cv.Set(sash.X, y, '▕', styleFB(sashColor, bgc))
		}
	}
	a.addHit(sash, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			a.sashDrag = "sidebar"
			a.drag = func(ev MouseEvent) {
				if ev.Kind == MouseRelease {
					a.sashDrag = ""
					return
				}
				a.sidebarWidth = clampInt(ev.X-a.rSidebar.X+1, 15, a.width-30)
			}
		}
		return true
	})
}

func (a *App) sidebarHandleKey(key, text string, isText bool) bool {
	switch a.sidebarView {
	case "explorer":
		return a.explorer.handleKey(key, text, isText)
	case "search":
		return a.searchV.handleKey(key, text, isText)
	case "scm":
		return a.scm.handleKey(key, text, isText)
	case "outline":
		return a.outline.handleKey(key, text, isText)
	case "extensions":
		return a.extView.handleKey(key, text, isText)
	case "debug":
		return a.debugView.handleKey(key, text, isText)
	}
	return false
}

func (a *App) sidebarInput() *TextInput {
	switch a.sidebarView {
	case "search":
		return a.searchV.focusedInput()
	case "scm":
		return a.scm.focusedInput()
	case "explorer":
		return a.explorer.focusedInput()
	}
	return nil
}

// showSidebar opens a sidebar view and focuses it.
func (a *App) showSidebar(view string) {
	a.sidebarVisible = true
	a.sidebarView = view
	a.setFocus(FocusSidebar)
	switch view {
	case "search":
		a.searchV.focusQuery()
	case "scm":
		a.scm.refresh()
	case "outline":
		a.outline.refresh()
	}
}

//#endregion

//#region Panel

var panelTabs = []struct {
	id    string
	title string
}{
	{"problems", "PROBLEMS"},
	{"output", "OUTPUT"},
	{"debug", "DEBUG CONSOLE"},
	{"terminal", "TERMINAL"},
	{"ports", "PORTS"},
}

func (a *App) drawPanel() {
	th := a.theme
	r := a.rPanel
	a.cv.clip = r
	bgc := th.C("panel.background")
	a.cv.Fill(r, ' ', styleFB(th.C("foreground"), bgc))
	// a border row is only needed when the panel blends into the editor
	border := th.C("panel.background") == th.C("editor.background")
	sashMark := a.hitMark()
	hy := r.Y
	if border {
		borderSt := styleFB(th.OverColor("panel.border", bgc), bgc)
		if a.sashDrag == "panel" || (a.mouseY == r.Y && r.Contains(a.mouseX, a.mouseY) && a.mouseOnPanelSash) {
			borderSt = styleFB(th.C("sash.hoverBorder"), bgc)
		}
		for x := r.X; x < r.X+r.W; x++ {
			a.cv.Set(x, r.Y, '▔', borderSt)
		}
		hy = r.Y + 1
	} else if a.sashDrag == "panel" {
		for x := r.X; x < r.X+r.W; x++ {
			a.cv.Set(x, r.Y, ' ', styleFB(th.C("foreground"), bgc).Underline(false))
		}
	}
	x := r.X + 1
	for _, t := range panelTabs {
		label := t.title
		if t.id == "problems" {
			e, w, i := a.problemCounts()
			if n := e + w + i; n > 0 {
				label += fmt.Sprintf(" %d", n)
			}
		}
		active := a.panelView == t.id
		st := styleFB(th.C("panelTitle.inactiveForeground"), bgc)
		if active {
			st = styleFB(th.C("panelTitle.activeForeground"), bgc).Underline(tcell.UnderlineStyleSolid, th.C("panelTitle.activeBorder"))
		}
		w := StrWidth(label)
		if x+w+2 > r.X+r.W-12 {
			break
		}
		a.cv.Text(x+1, hy, r.X+r.W, label, st)
		id := t.id
		a.addHit(Rect{x, hy, w + 2, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.showPanel(id)
			}
			return true
		})
		x += w + 3
	}
	// actions on the right
	actions := a.panelActions()
	ax := r.X + r.W - 1
	for i := len(actions) - 1; i >= 0; i-- {
		act := actions[i]
		w := StrWidth(act.icon)
		ax -= w + 1
		st := styleFB(th.C("foreground"), bgc)
		if a.mouseY == hy && a.mouseX >= ax && a.mouseX < ax+w {
			st = st.Background(th.OverColor("toolbar.hoverBackground", bgc))
			if !th.Has("toolbar.hoverBackground") {
				st = st.Background(th.OverColor("list.hoverBackground", bgc))
			}
		}
		a.cv.Text(ax, hy, ax+w, act.icon, st)
		cmd := act.cmd
		tip := act.title
		a.addHit(Rect{ax, hy, w, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.RunCommand(cmd, nil)
			case MouseMove:
				a.showTooltipAt(ev.X, hy+1, tip)
			}
			return true
		})
	}
	// sash: the border row, or the empty part of the header row
	a.addHitUnder(sashMark, Rect{r.X, r.Y, r.W, 1}, func(ev MouseEvent) bool {
		switch ev.Kind {
		case MouseMove:
			a.mouseOnPanelSash = true
		case MousePress:
			a.sashDrag = "panel"
			a.drag = func(ev MouseEvent) {
				if ev.Kind == MouseRelease {
					a.sashDrag = ""
					return
				}
				bottom := a.rPanel.Y + a.rPanel.H
				a.panelHeight = clampInt(bottom-ev.Y, 4, a.height-6)
				a.panelMaximized = false
			}
		}
		return true
	})
	body := Rect{r.X, hy + 1, r.W, r.Y + r.H - hy - 1}
	a.cv.clip = body
	switch a.panelView {
	case "terminal":
		a.terminals.draw(body)
	case "problems":
		a.problems.draw(body)
	case "output":
		a.output.draw(body)
	case "debug":
		a.drawDebugConsole(body)
	case "ports":
		a.cv.Text(body.X+2, body.Y+1, body.X+body.W, "No forwarded ports.", styleFB(th.C("descriptionForeground"), bgc))
	}
}

type panelAction struct {
	icon  string
	cmd   string
	title string
}

func (a *App) panelActions() []panelAction {
	var acts []panelAction
	switch a.panelView {
	case "terminal":
		acts = append(acts,
			panelAction{a.Icon(IcoAdd), "workbench.action.terminal.new", "New Terminal (Ctrl+Shift+`)"},
			panelAction{a.Icon(IcoSplit), "workbench.action.terminal.split", "Split Terminal (Ctrl+Shift+5)"},
			panelAction{a.Icon(IcoTrash), "workbench.action.terminal.kill", "Kill Terminal"},
		)
	case "output", "problems":
		acts = append(acts, panelAction{a.Icon(IcoClearAll), "workbench.output.action.clearOutput", "Clear Output"})
		if a.panelView == "problems" {
			acts = acts[:0]
			acts = append(acts, panelAction{a.Icon(IcoCollapseAll), "problems.action.collapseAll", "Collapse All"})
		}
	}
	maxIcon := a.Icon(IcoChevronUp)
	maxTitle := "Maximize Panel Size"
	if a.panelMaximized {
		maxIcon = a.Icon(IcoChevronDown)
		maxTitle = "Restore Panel Size"
	}
	acts = append(acts, panelAction{maxIcon, "workbench.action.toggleMaximizedPanel", maxTitle})
	acts = append(acts, panelAction{a.Icon(IcoClose), "workbench.action.closePanel", "Hide Panel"})
	return acts
}

func (a *App) showPanel(view string) {
	a.panelVisible = true
	a.panelView = view
	a.setFocus(FocusPanel)
	if view == "terminal" && len(a.terminals.terms) == 0 {
		a.terminals.newTerminal("")
	}
}

func (a *App) panelHandleKey(key, text string, isText bool) bool {
	switch a.panelView {
	case "debug":
		return a.consoleHandleKey(key)
	case "problems":
		return a.problems.handleKey(key)
	case "output":
		return a.output.handleKey(key)
	}
	return false
}

//#endregion

//#region Editor area

func (a *App) drawEditorArea() {
	r := a.rEditor
	if r.Empty() {
		return
	}
	n := len(a.groups)
	x := r.X
	for i, g := range a.groups {
		w := r.W / n
		if i == n-1 {
			w = r.X + r.W - x
		}
		gr := Rect{x, r.Y, w, r.H}
		a.cv.clip = gr
		g.draw(gr, i == a.activeGroup)
		if i < n-1 {
			// group divider
			th := a.theme
			for y := gr.Y; y < gr.Y+gr.H; y++ {
				a.cv.clip = Rect{0, 0, a.width, a.height}
				a.cv.Set(gr.X+gr.W-1, y, '│', styleFB(th.OverColor("editorGroup.border", th.C("editor.background")), th.C("editor.background")))
			}
		}
		x += w
	}
}

//#endregion

// keyLabel returns the display string of the primary keybinding for a command:
// the newest user binding, otherwise the first default one.
func (a *App) keyLabel(cmd string) string {
	if a.keyLabels == nil {
		a.keyLabels = map[string]string{}
		for _, b := range a.bindings {
			if b.Source == "default" {
				if _, ok := a.keyLabels[b.Command]; !ok {
					a.keyLabels[b.Command] = config.FormatKey(b.Keys)
				}
			}
		}
		for _, b := range a.bindings {
			if b.Source != "default" {
				a.keyLabels[b.Command] = config.FormatKey(b.Keys)
			}
		}
	}
	return a.keyLabels[cmd]
}

func repeatStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}
