package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/config"
	"tuicode/internal/git"
	"tuicode/internal/lsp"
	"tuicode/internal/syntax"
	"tuicode/internal/theme"
)

// Version is the application version.
const Version = "0.1.0"

// AppName is shown in window titles.
const AppName = "tuicode"

// Focus areas.
type Focus int

const (
	FocusEditor Focus = iota
	FocusSidebar
	FocusPanel
	FocusQuick
	FocusMenu
	FocusDialog
	FocusWidget // a text field inside the editor (find, rename)
)

// Mouse event kinds.
const (
	MousePress = iota
	MouseRelease
	MouseDrag
	MouseMove
	MouseWheel
)

// MouseEvent is a decoded mouse action.
type MouseEvent struct {
	X, Y    int
	Button  tcell.ButtonMask
	Kind    int
	Mods    tcell.ModMask
	Clicks  int
	WheelDY int
	WheelDX int
}

type hit struct {
	r       Rect
	handler func(ev MouseEvent) bool
}

// Options configure startup.
type Options struct {
	Paths    []string
	Line     int
	Col      int
	Wait     bool
	NoLSP    bool
	Diff     [2]string
	NewFile  bool
	ReadOnly bool
}

// App is the whole workbench.
type App struct {
	screen tcell.Screen
	cv     *Canvas

	theme     *theme.Theme
	settings  *config.Settings
	root      string
	gitRoot   string
	gitBranch string
	gitAhead  int
	gitBehind int
	gitHasUp  bool
	gitStatus []git.FileStatus
	gitByPath map[string]git.FileStatus
	gitBusy   bool
	lsp       *lsp.Manager
	nerdIcons bool

	commands       map[string]*Command
	bindings       []config.Binding
	pendingChord   []string
	recentCommands []string

	docs            map[string]*Document
	untitledCounter int

	groups      []*EditorGroup
	activeGroup int
	focus       Focus
	prevFocus   Focus

	width, height    int
	sidebarVisible   bool
	sidebarWidth     int
	sidebarView      string
	panelVisible     bool
	panelHeight      int
	panelMaximized   bool
	panelView        string
	zenMode          bool
	statusVisible    bool
	activityVisible  bool
	titleVisible     bool
	minimapEnabled   bool
	breadcrumbs      bool
	stickyScroll     bool
	whitespaceRender string
	lineNumbers      string

	rTitle, rActivity, rSidebar, rEditor, rPanel, rStatus Rect

	explorer  *Explorer
	searchV   *SearchView
	scm       *SCMView
	outline   *OutlineView
	hierView  *HierarchyView
	extView   *ExtensionsView
	terminals *TerminalPanel
	problems  *ProblemsPanel
	output    *OutputPanel

	quick         *QuickInput
	menu          *MenuBar
	ctxMenu       *ContextMenu
	dialog        *Dialog
	notifications []*Notification
	notifCenter   bool
	statusMsg     string
	statusMsgTime time.Time

	postCh  chan func()
	quit    bool
	opts    Options
	started time.Time

	// mouse
	hits        []hit
	lastButtons tcell.ButtonMask
	drag        func(ev MouseEvent)
	lastClick   time.Time
	lastClickX  int
	lastClickY  int
	clickCount  int
	mouseX      int
	mouseY      int
	hoverTimer  *time.Timer
	tooltip     *Tooltip

	// editing helpers
	clipboardFullLine bool
	clipboardText     string
	clipboardParts    []string
	wholeWordNext     bool
	autoClosed        map[*EditorView][]buffer.Pos

	navBack    []navEntry
	navForward []navEntry
	closedTabs []closedTab
	lastEdits  []navEntry

	autoSaveTimer *time.Timer
	tickCount     int

	cursor              cursorReq
	lastTitle           string
	sashDrag            string
	mouseOnPanelSash    bool
	unreadNotifications int
	notifHistory        []*Notification
	pasting             bool
	pasteBuf            strings.Builder
	findHistory         []string
	state               *stateFile
	fileIdx             *fileIndex
	keyLabels           map[string]string
	panics              int
	snippets            *snippetCache

	breakpoints   map[string][]*Breakpoint
	debug         *DebugSession
	debugView     *DebugView
	debugConsole  []consoleLine
	consoleScroll int
	consoleInput  TextInput
	lastLaunch    string
	watches       []string
	watchValues   map[string]string
}

type navEntry struct {
	path string
	pos  buffer.Pos
	doc  *Document
}

type closedTab struct {
	path  string
	group int
	pos   buffer.Pos
}

// New creates the application.
func New(opts Options) (*App, error) {
	a := &App{
		opts:            opts,
		docs:            map[string]*Document{},
		postCh:          make(chan func(), 4096),
		sidebarVisible:  true,
		sidebarWidth:    30,
		sidebarView:     "explorer",
		panelHeight:     12,
		panelView:       "terminal",
		statusVisible:   true,
		activityVisible: true,
		titleVisible:    true,
		autoClosed:      map[*EditorView][]buffer.Pos{},
		gitByPath:       map[string]git.FileStatus{},
		started:         time.Now(),
		breakpoints:     map[string][]*Breakpoint{},
		watchValues:     map[string]string{},
	}
	// workspace root
	var files []string
	for _, p := range opts.Paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			if a.root == "" {
				a.root = abs
			}
		} else {
			files = append(files, abs)
		}
	}
	if a.root == "" && len(files) == 0 {
		if wd, err := os.Getwd(); err == nil {
			a.root = wd
		}
	}
	if a.root == "" && len(files) > 0 {
		a.root = guessRoot(files[0])
	}
	a.loadSettings()
	a.registerCommands()
	a.groups = []*EditorGroup{a.newGroup()}

	a.explorer = newExplorer(a)
	a.searchV = newSearchView(a)
	a.scm = newSCMView(a)
	a.outline = newOutlineView(a)
	a.hierView = newHierarchyView(a)
	a.extView = newExtensionsView(a)
	a.terminals = newTerminalPanel(a)
	a.problems = newProblemsPanel(a)
	a.output = newOutputPanel(a)
	a.menu = newMenuBar(a)
	a.debugView = newDebugView(a)

	if !opts.NoLSP && a.settings.Bool("tuicode.lsp.enabled", "") {
		a.setupLSP()
	}
	a.refreshGit()

	for _, f := range files {
		a.OpenFile(f, false)
	}
	if opts.Diff[0] != "" {
		a.openDiffFiles(opts.Diff[0], opts.Diff[1])
	}
	if len(files) > 0 && (opts.Line > 0 || opts.Col > 0) {
		if v := a.ActiveView(); v != nil {
			v.SetCursor(v.buf().Clamp(buffer.Pos{Line: max(opts.Line-1, 0), Col: max(opts.Col-1, 0)}))
			v.ensurePosVisible(v.Cursor(), true)
		}
	}
	if opts.NewFile {
		a.NewUntitled()
	}
	if len(files) == 0 && opts.Diff[0] == "" && !opts.NewFile {
		switch a.settings.String("workbench.startupEditor", "") {
		case "welcomePage", "welcomePageInEmptyWorkbench":
			a.openWelcome()
		case "newUntitledFile":
			a.NewUntitled()
		}
		a.restoreSession()
	}
	if len(files) > 0 {
		a.focus = FocusEditor
	} else if a.ActiveView() == nil {
		a.focus = FocusSidebar
	}
	return a, nil
}

// guessRoot picks a workspace folder for a file: the current directory when the
// file is inside it, else the nearest repository or module root, else its folder.
func guessRoot(file string) string {
	if wd, err := os.Getwd(); err == nil && strings.HasPrefix(file, wd+string(filepath.Separator)) {
		home, _ := os.UserHomeDir()
		if wd != home && wd != "/" {
			return wd
		}
	}
	dir := filepath.Dir(file)
	for d := dir; d != "/" && d != "."; d = filepath.Dir(d) {
		for _, marker := range []string{".git", "go.mod", "package.json", "deno.json", "Cargo.toml", "pubspec.yaml"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d
			}
		}
	}
	return dir
}

func (a *App) loadSettings() {
	a.settings = config.LoadSettings(a.root)
	a.applySettings()
}

func (a *App) applySettings() {
	th, err := theme.Load(a.settings.String("workbench.colorTheme", ""), config.ThemesDir())
	if err != nil {
		th, _ = theme.Load("Dark Modern")
	}
	a.theme = th
	switch a.settings.String("tuicode.icons", "") {
	case "nerd":
		a.nerdIcons = true
	case "unicode", "none":
		a.nerdIcons = false
	default:
		a.nerdIcons = detectNerdFont()
	}
	a.minimapEnabled = a.settings.Bool("editor.minimap.enabled", "")
	a.breadcrumbs = a.settings.Bool("breadcrumbs.enabled", "")
	a.stickyScroll = a.settings.Bool("editor.stickyScroll.enabled", "")
	a.whitespaceRender = a.settings.String("editor.renderWhitespace", "")
	a.lineNumbers = a.settings.String("editor.lineNumbers", "")
	a.statusVisible = a.settings.Bool("workbench.statusBar.visible", "")
	a.titleVisible = a.settings.Bool("tuicode.titleBar", "") && a.settings.String("window.menuBarVisibility", "") != "hidden"
	a.activityVisible = a.settings.String("workbench.activityBar.location", "") != "hidden"
	importKB := a.settings.Bool("tuicode.importVSCodeKeybindings", "")
	binds, errs := config.LoadKeybindings(a.defaultBindings(), importKB)
	a.bindings = binds
	a.keyLabels = nil
	for _, e := range errs {
		a.settingsErrors(e)
	}
	for _, e := range a.settings.Errors {
		a.settingsErrors(e)
	}
	for _, d := range a.docs {
		a.applyEditorSettings(d)
	}
	if a.lsp != nil {
		a.lsp.Overrides = a.lspOverrides()
	}
}

func (a *App) settingsErrors(msg string) {
	a.Post(func() { a.notify(NotifyError, "Failed to parse "+msg) })
}

func (a *App) lspOverrides() map[string][]string {
	out := map[string][]string{}
	for lang, v := range a.settings.Map("tuicode.lsp.servers") {
		switch x := v.(type) {
		case []any:
			var cmd []string
			for _, p := range x {
				if s, ok := p.(string); ok {
					cmd = append(cmd, s)
				}
			}
			out[lang] = cmd
		case string:
			out[lang] = strings.Fields(x)
		case bool:
			if !x {
				out[lang] = nil
			}
		case nil:
			out[lang] = nil
		}
	}
	return out
}

// Post schedules fn on the UI goroutine.
func (a *App) Post(fn func()) {
	select {
	case a.postCh <- fn:
	default:
		go func() { a.postCh <- fn }()
	}
}

// Run starts the terminal UI and blocks until exit.
func (a *App) Run() error {
	s, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := s.Init(); err != nil {
		return err
	}
	a.screen = s
	a.cv = &Canvas{s: s}
	defer func() {
		if r := recover(); r != nil {
			s.Fini()
			a.saveSessionSafe()
			panic(r)
		}
	}()
	if a.settings.Bool("tuicode.mouse", "") {
		s.EnableMouse(tcell.MouseMotionEvents)
	}
	s.EnablePaste()
	s.EnableFocus()
	s.SetCursorStyle(tcell.CursorStyleBlinkingBar)
	a.width, a.height = s.Size()
	// tell kitty an editor is running so conditional key mappings can pass shortcuts through
	a.writeTTY("\x1b]1337;SetUserVar=in_editor=MQ==\x07")
	a.suggestKittySetup()
	defer a.writeTTY("\x1b]1337;SetUserVar=in_editor\x07")
	a.applyTerminalTitle()

	evCh := make(chan tcell.Event, 256)
	quitCh := make(chan struct{})
	go s.ChannelEvents(evCh, quitCh)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	a.draw()
	for !a.quit {
		select {
		case ev := <-evCh:
			if ev == nil {
				a.quit = true
				break
			}
			a.safely(func() { a.handleEvent(ev) })
		case fn := <-a.postCh:
			a.safely(fn)
		case <-ticker.C:
			a.safely(a.tick)
		}
		// drain everything that is already queued before drawing
	drain:
		for i := 0; i < 1000; i++ {
			select {
			case ev := <-evCh:
				if ev == nil {
					a.quit = true
					break drain
				}
				a.safely(func() { a.handleEvent(ev) })
			case fn := <-a.postCh:
				a.safely(fn)
			default:
				break drain
			}
		}
		if a.lsp != nil {
			a.safely(a.lsp.Flush)
		}
		a.safely(a.draw)
	}
	close(quitCh)
	a.shutdown()
	s.Fini()
	return nil
}

// safely runs fn and turns a panic into an error notification instead of crashing,
// so a bug in one feature never takes unsaved work down with it.
func (a *App) safely(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			a.output.append("tuicode", fmt.Sprintf("panic: %v\n%s", r, stack))
			a.panics++
			if a.panics < 20 {
				a.notify(NotifyError, fmt.Sprintf("Internal error: %v (details in Output > tuicode)", r))
			}
			a.dialog = nil
			a.ctxMenu = nil
			if a.screen != nil {
				a.screen.Show()
			}
		}
	}()
	fn()
}

func (a *App) shutdown() {
	a.saveSessionSafe()
	a.terminals.closeAll()
	if a.lsp != nil {
		a.lsp.ShutdownAll()
	}
}

func (a *App) saveSessionSafe() {
	defer func() { _ = recover() }()
	a.saveSession()
}

// tick runs periodic housekeeping.
func (a *App) tick() {
	a.tickCount++
	if a.statusMsg != "" && time.Since(a.statusMsgTime) > 5*time.Second {
		a.statusMsg = ""
	}
	a.expireNotifications()
	if a.tickCount%4 == 0 {
		for _, d := range a.docs {
			if d.refs > 0 {
				a.checkDisk(d)
			}
		}
	}
	if a.tickCount%10 == 0 && a.settings.Bool("git.autorefresh", "") {
		a.refreshGit()
	}
	if a.tickCount%4 == 0 {
		a.explorer.refreshIfChanged()
	}
}

func (a *App) handleEvent(ev tcell.Event) {
	switch e := ev.(type) {
	case *tcell.EventResize:
		a.width, a.height = e.Size()
		a.screen.Sync()
	case *tcell.EventKey:
		a.hideTooltip()
		a.handleKey(e)
	case *tcell.EventMouse:
		a.handleMouse(e)
	case *tcell.EventPaste:
		// tcell delivers paste content as key events between start and end markers
		if e.Start() {
			a.pasting = true
			a.pasteBuf.Reset()
		} else if e.End() {
			a.pasting = false
			text := strings.ReplaceAll(a.pasteBuf.String(), "\r\n", "\n")
			a.handlePaste(strings.ReplaceAll(text, "\r", "\n"))
		}
	case *tcell.EventFocus:
		if e.Focused {
			for _, d := range a.docs {
				a.checkDisk(d)
			}
			a.refreshGit()
		} else if a.settings.String("files.autoSave", "") == "onFocusChange" || a.settings.String("files.autoSave", "") == "onWindowChange" {
			a.saveAllDirty()
		}
	}
}

//#region Focus and context

// ActiveGroup returns the focused editor group.
func (a *App) ActiveGroup() *EditorGroup {
	if a.activeGroup >= len(a.groups) {
		a.activeGroup = len(a.groups) - 1
	}
	return a.groups[a.activeGroup]
}

// ActiveView returns the active text editor in the active group (nil for special pages).
func (a *App) ActiveView() *EditorView {
	v := a.ActiveGroup().Active()
	if v != nil && v.special != nil {
		return nil
	}
	return v
}

// ActiveTab returns the active tab, including special pages.
func (a *App) ActiveTab() *EditorView {
	return a.ActiveGroup().Active()
}

func (a *App) setFocus(f Focus) {
	if a.focus != f {
		a.prevFocus = a.focus
	}
	a.focus = f
}

// focusEditor focuses the active editor.
func (a *App) focusEditor() {
	a.setFocus(FocusEditor)
}

func (a *App) contextKey(key string) any {
	v := a.ActiveView()
	editorFocus := a.focus == FocusEditor && v != nil
	widgetInput := a.focus == FocusWidget && v != nil
	switch key {
	case "editorTextFocus", "editorFocus":
		return editorFocus
	case "textInputFocus":
		return editorFocus || a.focusedInput() != nil
	case "inputFocus":
		return a.focusedInput() != nil
	case "editorHasSelection":
		return v != nil && v.HasSelection()
	case "editorHasMultipleSelections":
		return v != nil && len(v.sels) > 1
	case "editorReadonly":
		return v != nil && v.buf().ReadOnly
	case "editorLangId", "resourceLangId":
		if v != nil {
			return v.doc.Lang.ID
		}
		return ""
	case "resourceExtname":
		if v != nil {
			return filepath.Ext(v.buf().Path)
		}
		return ""
	case "resourceFilename":
		if v != nil {
			return filepath.Base(v.buf().Path)
		}
		return ""
	case "editorIsOpen", "activeEditor":
		return v != nil
	case "terminalFocus":
		return a.focus == FocusPanel && a.panelView == "terminal" && a.panelVisible
	case "terminal.active", "terminalIsOpen":
		return len(a.terminals.terms) > 0
	case "panelFocus":
		return a.focus == FocusPanel
	case "sideBarFocus":
		return a.focus == FocusSidebar
	case "sideBarVisible":
		return a.sidebarVisible
	case "panelVisible":
		return a.panelVisible
	case "explorerViewletFocus", "filesExplorerFocus", "explorerFocus":
		return a.focus == FocusSidebar && a.sidebarView == "explorer"
	case "searchViewletFocus":
		return a.focus == FocusSidebar && a.sidebarView == "search"
	case "listFocus":
		return a.focus == FocusSidebar || (a.focus == FocusPanel && a.panelView == "problems")
	case "inQuickOpen", "quickInputVisible":
		return a.quick != nil
	case "suggestWidgetVisible":
		return v != nil && v.completion != nil && v.completion.visible()
	case "findWidgetVisible":
		return v != nil && v.find != nil && v.find.visible
	case "findInputFocussed":
		return widgetInput && v.find != nil && v.find.visible && v.find.focus == 0
	case "replaceInputFocussed":
		return widgetInput && v.find != nil && v.find.visible && v.find.focus == 1
	case "renameInputVisible":
		return v != nil && v.rename != nil
	case "parameterHintsVisible":
		return v != nil && v.sig != nil
	case "inSnippetMode", "hasNextTabstop", "hasPrevTabstop":
		return v != nil && v.snippet != nil
	case "isLinux":
		return true
	case "isWindows", "isMac", "isWeb":
		return false
	case "inZenMode":
		return a.zenMode
	case "multipleEditorGroups":
		return len(a.groups) > 1
	case "notificationCenterVisible":
		return a.notifCenter
	case "notificationToastsVisible":
		return len(a.notifications) > 0
	case "activePanel":
		if a.panelVisible {
			return "workbench.panel." + a.panelView
		}
		return ""
	case "activeViewlet":
		if a.sidebarVisible {
			return "workbench.view." + a.sidebarView
		}
		return ""
	case "editorTabMovesFocus":
		return false
	case "hasWordHighlights":
		return v != nil && len(v.highlights) > 0
	case "inDebugMode":
		return a.debug != nil && a.debug.state != "terminated"
	case "debugState":
		if a.debug == nil {
			return "inactive"
		}
		return a.debug.state
	case "inDebugRepl":
		return a.focus == FocusPanel && a.panelView == "debug"
	case "codeActionMenuVisible":
		return v != nil && v.codeActions != nil
	case "scmRepository", "scmProvider":
		return a.gitRoot != ""
	case "gitOpenRepositoryCount":
		if a.gitRoot != "" {
			return "1"
		}
		return "0"
	}
	if strings.HasPrefix(key, "config.") {
		return a.settings.Get(strings.TrimPrefix(key, "config."), "")
	}
	return nil
}

//#endregion

//#region Key handling

// commandsToSkipShell lists commands that run even while the terminal has focus.
var commandsToSkipShell = map[string]bool{
	"workbench.action.showCommands": true, "workbench.action.quickOpen": true, "workbench.action.terminal.toggleTerminal": true,
	"workbench.action.togglePanel": true, "workbench.action.toggleSidebarVisibility": true, "workbench.action.terminal.new": true,
	"workbench.action.terminal.copySelection": true, "workbench.action.terminal.paste": true, "workbench.action.terminal.split": true,
	"workbench.action.terminal.kill": true, "workbench.action.terminal.focusNext": true, "workbench.action.terminal.focusPrevious": true,
	"workbench.action.terminal.scrollUp": true, "workbench.action.terminal.scrollDown": true, "workbench.action.terminal.scrollUpPage": true,
	"workbench.action.terminal.scrollDownPage": true, "workbench.action.terminal.scrollToTop": true, "workbench.action.terminal.scrollToBottom": true,
	"workbench.action.terminal.clear": true, "workbench.action.terminal.selectAll": true, "workbench.action.terminal.focusFind": true,
	"workbench.view.explorer": true, "workbench.view.search": true, "workbench.view.scm": true, "workbench.view.extensions": true,
	"workbench.action.findInFiles": true, "workbench.action.focusActiveEditorGroup": true, "workbench.action.focusFirstEditorGroup": true,
	"workbench.action.focusSecondEditorGroup": true, "workbench.action.focusThirdEditorGroup": true, "workbench.action.quit": true,
	"workbench.actions.view.problems": true, "workbench.action.output.toggleOutput": true, "workbench.action.toggleMaximizedPanel": true,
	"workbench.action.files.newUntitledFile": true, "workbench.action.files.openFile": true, "workbench.action.openSettings": true,
	"workbench.action.gotoLine": true, "workbench.action.showAllSymbols": true, "workbench.action.closeActiveEditor": true,
	"workbench.action.nextEditor": true, "workbench.action.previousEditor": true, "workbench.action.togglePanelPosition": true,
	"workbench.action.terminal.focus": true, "workbench.action.toggleZenMode": true, "workbench.action.selectTheme": true,
	"workbench.action.files.saveAll": true, "workbench.action.openRecent": true, "workbench.action.terminal.toggleMaximizedPanel": true,
	"workbench.action.debug.start": true, "workbench.action.debug.stop": true, "workbench.action.debug.continue": true,
	"workbench.action.debug.stepOver": true, "workbench.action.debug.stepInto": true, "workbench.action.debug.stepOut": true,
	"workbench.action.terminal.resizePaneUp": true, "workbench.action.terminal.resizePaneDown": true, "workbench.action.closePanel": true,
}

func (a *App) matchBinding(chord []string) (*config.Binding, bool) {
	ctx := config.Context(a.contextKey)
	var exact *config.Binding
	partial := false
	// later bindings win, like VS Code
	for i := len(a.bindings) - 1; i >= 0; i-- {
		b := &a.bindings[i]
		if len(b.Keys) < len(chord) {
			continue
		}
		match := true
		for j := range chord {
			if b.Keys[j] != chord[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if !b.When.Eval(ctx) {
			continue
		}
		if len(b.Keys) == len(chord) {
			if exact == nil {
				exact = b
			}
		} else {
			partial = true
		}
	}
	if exact != nil && !partial {
		return exact, false
	}
	if partial {
		return nil, true
	}
	return exact, false
}

func (a *App) handleKey(ev *tcell.EventKey) {
	key, isText := KeyString(ev)
	if key == "" {
		return
	}
	if a.pasting {
		if isText {
			if key == "space" {
				a.pasteBuf.WriteString(" ")
			} else {
				a.pasteBuf.WriteString(key)
			}
		} else if key == "enter" {
			a.pasteBuf.WriteString("\r")
		} else if key == "ctrl+j" {
			a.pasteBuf.WriteString("\n")
		} else if key == "tab" {
			a.pasteBuf.WriteString("\t")
		}
		return
	}
	text := key
	if key == "space" {
		text = " "
	}
	// overlays first
	if a.dialog != nil {
		a.dialog.handleKey(key, text, isText)
		return
	}
	if a.ctxMenu != nil {
		if a.ctxMenu.handleKey(key) {
			return
		}
	}
	if a.menu.open >= 0 {
		if a.menu.handleKey(key) {
			return
		}
	}
	if len(a.pendingChord) == 0 {
		if a.quick != nil {
			if a.quick.handleKey(key, text, isText) {
				return
			}
		} else if in := a.focusedInput(); in != nil {
			if in.HandleKey(key, text, isText) {
				return
			}
		}
		if a.focus == FocusSidebar && a.sidebarVisible {
			if a.sidebarHandleKey(key, text, isText) {
				return
			}
		}
		if a.focus == FocusPanel && a.panelVisible && a.panelView == "terminal" && a.terminals.findOpen {
			if a.terminals.findHandleKey(key) {
				return
			}
		}
		if a.focus == FocusPanel && a.panelVisible {
			if a.panelView == "terminal" && !a.terminals.findOpen {
				// the shell gets keys unless they are bound to a command that skips the shell
				b, partial := a.matchBinding([]string{key})
				skip := (b != nil && (commandsToSkipShell[b.Command] || a.skipShellSetting(b.Command))) || (partial && key == "ctrl+k" && false)
				if !skip {
					a.terminals.sendKey(ev, key)
					return
				}
			} else if a.panelHandleKey(key, text, isText) {
				return
			}
		}
		if a.focus == FocusEditor {
			if v := a.ActiveTab(); v != nil {
				if v.handleWidgetKey(key, text, isText) {
					return
				}
			}
		}
		if a.focus == FocusWidget {
			if v := a.ActiveView(); v != nil && v.rename != nil {
				if v.rename.handleKey(key) {
					return
				}
			}
		}
	}
	chord := append(append([]string{}, a.pendingChord...), key)
	b, partial := a.matchBinding(chord)
	if partial {
		a.pendingChord = chord
		a.setStatusMessage(fmt.Sprintf("(%s) was pressed. Waiting for second key of chord...", config.FormatKey(chord)))
		return
	}
	a.pendingChord = nil
	if b != nil {
		if len(chord) > 1 {
			a.statusMsg = ""
		}
		a.RunCommand(b.Command, b.Args)
		return
	}
	if len(chord) > 1 {
		a.setStatusMessage(fmt.Sprintf("The key combination (%s) is not a command.", config.FormatKey(chord)))
		return
	}
	// plain typing
	if isText && a.focus == FocusEditor {
		if v := a.ActiveView(); v != nil {
			v.withLinkedEditing("type", text, func() { v.Type(text) })
			a.afterType(v, text)
		}
	}
}

func (a *App) skipShellSetting(cmd string) bool {
	for _, c := range a.settings.List("terminal.integrated.commandsToSkipShell", "") {
		if s, ok := c.(string); ok && s == cmd {
			return true
		}
	}
	return false
}

// focusedInput returns the text field that currently has keyboard focus.
func (a *App) focusedInput() *TextInput {
	if a.quick != nil {
		return &a.quick.input
	}
	switch a.focus {
	case FocusWidget:
		v := a.ActiveView()
		if v == nil {
			return nil
		}
		if v.rename != nil {
			return &v.rename.input
		}
		if v.find != nil && v.find.visible {
			if v.find.focus == 1 {
				return &v.find.replace
			}
			return &v.find.input
		}
	case FocusSidebar:
		if a.sidebarVisible {
			return a.sidebarInput()
		}
	case FocusPanel:
		if a.panelVisible && a.panelView == "terminal" && a.terminals.findOpen {
			return &a.terminals.findInput
		}
		if a.panelVisible && a.panelView == "debug" {
			if a.consoleInput.app == nil {
				a.consoleInput = newTextInput(a, "Evaluate expression (when stopped)")
			}
			return &a.consoleInput
		}
	}
	return nil
}

func (a *App) handlePaste(text string) {
	if text == "" {
		return
	}
	if a.quick != nil {
		a.quick.input.Insert(strings.ReplaceAll(text, "\n", " "))
		a.quick.onChange()
		return
	}
	if in := a.focusedInput(); in != nil {
		in.Insert(strings.ReplaceAll(text, "\n", " "))
		if in.OnChange != nil {
			in.OnChange()
		}
		return
	}
	if a.focus == FocusPanel && a.panelView == "terminal" {
		a.terminals.paste(text)
		return
	}
	if v := a.ActiveView(); v != nil && a.focus == FocusEditor {
		v.PasteText(text, nil, false)
	}
}

//#endregion

//#region Mouse handling

func (a *App) addHit(r Rect, h func(ev MouseEvent) bool) {
	if r.Empty() {
		return
	}
	a.hits = append(a.hits, hit{r: r, handler: h})
}

func (a *App) handleMouse(e *tcell.EventMouse) {
	x, y := e.Position()
	btn := e.Buttons()
	mods := e.Modifiers()
	a.mouseX, a.mouseY = x, y
	a.mouseOnPanelSash = false
	wheel := btn & (tcell.WheelUp | tcell.WheelDown | tcell.WheelLeft | tcell.WheelRight)
	btn &^= tcell.WheelUp | tcell.WheelDown | tcell.WheelLeft | tcell.WheelRight
	if wheel != 0 {
		me := MouseEvent{X: x, Y: y, Kind: MouseWheel, Mods: mods}
		switch {
		case wheel&tcell.WheelUp != 0:
			me.WheelDY = -1
		case wheel&tcell.WheelDown != 0:
			me.WheelDY = 1
		case wheel&tcell.WheelLeft != 0:
			me.WheelDX = -1
		case wheel&tcell.WheelRight != 0:
			me.WheelDX = 1
		}
		if mods&tcell.ModShift != 0 && me.WheelDY != 0 {
			me.WheelDX, me.WheelDY = me.WheelDY, 0
		}
		a.hideTooltip()
		a.dispatchMouse(me)
		return
	}
	prev := a.lastButtons
	a.lastButtons = btn
	switch {
	case btn != 0 && prev == 0:
		// press
		now := time.Now()
		if now.Sub(a.lastClick) < 400*time.Millisecond && abs(x-a.lastClickX) <= 1 && y == a.lastClickY {
			a.clickCount++
		} else {
			a.clickCount = 1
		}
		a.lastClick = now
		a.lastClickX, a.lastClickY = x, y
		a.drag = nil
		a.hideTooltip()
		a.dispatchMouse(MouseEvent{X: x, Y: y, Button: btn, Kind: MousePress, Mods: mods, Clicks: a.clickCount})
	case btn == 0 && prev != 0:
		if a.drag != nil {
			a.drag(MouseEvent{X: x, Y: y, Button: prev, Kind: MouseRelease, Mods: mods})
			a.drag = nil
		} else {
			a.dispatchMouse(MouseEvent{X: x, Y: y, Button: prev, Kind: MouseRelease, Mods: mods})
		}
	case btn != 0:
		if a.drag != nil {
			a.drag(MouseEvent{X: x, Y: y, Button: btn, Kind: MouseDrag, Mods: mods})
		}
	default:
		a.dispatchMouse(MouseEvent{X: x, Y: y, Kind: MouseMove, Mods: mods})
	}
}

func (a *App) dispatchMouse(me MouseEvent) {
	if me.Kind == MousePress {
		// clicking anywhere closes context menus unless they handle it
		if a.ctxMenu != nil && !a.ctxMenu.containsDeep(me.X, me.Y) {
			a.ctxMenu = nil
			if me.Button != tcell.Button2 {
				return
			}
		}
		if a.menu.open >= 0 && !a.menu.contains(me.X, me.Y) {
			a.menu.close()
			return
		}
	}
	for i := len(a.hits) - 1; i >= 0; i-- {
		h := a.hits[i]
		if h.r.Contains(me.X, me.Y) {
			if h.handler(me) {
				return
			}
		}
	}
}

//#endregion

//#region Status and notifications

func (a *App) setStatusMessage(msg string) {
	a.statusMsg = msg
	a.statusMsgTime = time.Now()
}

//#endregion

//#region Git

func (a *App) refreshGit() {
	if !a.settings.Bool("git.enabled", "") || a.gitBusy || a.root == "" {
		return
	}
	a.gitBusy = true
	root := a.root
	go func() {
		gr, ok := git.RepoRoot(root)
		var st []git.FileStatus
		var branch string
		var ahead, behind int
		var hasUp bool
		if ok {
			st, _ = git.Status(gr)
			branch = git.Branch(gr)
			ahead, behind, hasUp = git.AheadBehind(gr)
		}
		a.Post(func() {
			a.gitBusy = false
			changedRoot := a.gitRoot != gr
			if !ok {
				a.gitRoot = ""
				a.gitStatus = nil
				a.gitByPath = map[string]git.FileStatus{}
				return
			}
			a.gitRoot = gr
			a.gitBranch = branch
			a.gitAhead, a.gitBehind, a.gitHasUp = ahead, behind, hasUp
			oldStatus := a.gitStatus
			a.gitStatus = st
			a.gitByPath = map[string]git.FileStatus{}
			for _, f := range st {
				a.gitByPath[filepath.Join(gr, f.Path)] = f
			}
			if changedRoot || !sameStatus(oldStatus, st) {
				for _, d := range a.docs {
					a.loadGitBase(d)
				}
				a.scm.refresh()
			}
		})
	}()
}

func sameStatus(a, b []git.FileStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (a *App) gitStatusFor(path string) (git.FileStatus, bool) {
	st, ok := a.gitByPath[path]
	return st, ok
}

// gitDecoration returns the color key and letter for a path (files or folders).
func (a *App) gitDecoration(path string, isDir bool) (string, string) {
	if !isDir {
		st, ok := a.gitByPath[path]
		if !ok {
			return "", ""
		}
		return gitColorKey(st), gitLetter(st)
	}
	prefix := path + string(filepath.Separator)
	best := ""
	for p, st := range a.gitByPath {
		if strings.HasPrefix(p, prefix) {
			key := gitColorKey(st)
			if key == "gitDecoration.conflictingResourceForeground" {
				return key, ""
			}
			if key == "gitDecoration.modifiedResourceForeground" || best == "" {
				best = key
			}
		}
	}
	return best, ""
}

func gitColorKey(st git.FileStatus) string {
	switch {
	case st.Conflicted():
		return "gitDecoration.conflictingResourceForeground"
	case st.Untracked():
		return "gitDecoration.untrackedResourceForeground"
	case st.Y == 'D' || st.X == 'D':
		return "gitDecoration.deletedResourceForeground"
	case st.X == 'A' && st.Y == ' ':
		return "gitDecoration.addedResourceForeground"
	case st.X == 'R':
		return "gitDecoration.renamedResourceForeground"
	case st.X == '!':
		return "gitDecoration.ignoredResourceForeground"
	}
	return "gitDecoration.modifiedResourceForeground"
}

func gitLetter(st git.FileStatus) string {
	switch {
	case st.Conflicted():
		return "!"
	case st.Untracked():
		return "U"
	case st.Y != ' ':
		return git.Letter(st.Y)
	}
	return git.Letter(st.X)
}

//#endregion

//#region LSP

func (a *App) setupLSP() {
	m := lsp.NewManager(a.root, a.Post)
	m.Overrides = a.lspOverrides()
	m.Log = func(server, line string) {
		a.output.append("Language Server: "+server, line)
	}
	m.OnDiagnostics = func(path string) {
		if d, ok := a.docs[path]; ok {
			a.setDiagnostics(d)
		}
		a.problems.dirty = true
	}
	m.OnMessage = func(kind int, server, msg string) {
		lvl := NotifyInfo
		switch kind {
		case 1:
			lvl = NotifyError
		case 2:
			lvl = NotifyWarning
		case 4:
			a.output.append("Language Server: "+server, msg)
			return
		}
		a.notify(lvl, server+": "+msg)
	}
	m.ApplyEdit = func(edit lsp.WorkspaceEdit) bool {
		return a.applyWorkspaceEdit(edit)
	}
	m.ShowDocument = func(path string, sel *lsp.Range) {
		v := a.OpenFile(path, false)
		if v != nil && sel != nil {
			r := a.lsp.RangeFromLSP(v.buf(), *sel)
			v.SetSelections([]buffer.Selection{{Anchor: r.Start, Active: r.End}}, 0)
			v.ensurePosVisible(r.Start, true)
		}
	}
	m.OnMissing = func(lang *syntax.Language) {
		if !a.settings.Bool("tuicode.lsp.promptInstall", "") {
			return
		}
		server := lang.Servers[0][0]
		hint := installHint(server)
		msg := fmt.Sprintf("No language server found for %s. Install %s for IntelliSense.", lang.Name, server)
		var actions []NotifyAction
		if hint != "" {
			actions = append(actions, NotifyAction{Label: "Install", Run: func() { a.terminals.runText(hint) }})
		}
		actions = append(actions, NotifyAction{Label: "Don't Show Again", Run: func() {
			_ = setUserSetting(a, "tuicode.lsp.promptInstall", false)
		}})
		a.notify(NotifyInfo, msg, actions...)
	}
	m.Configuration = func(section string) any {
		if section == "" {
			return map[string]any{}
		}
		return a.settings.Section(section)
	}
	a.lsp = m
}

//#endregion

//#region Auto-closed pair tracking

func (a *App) markAutoClosed(v *EditorView, p buffer.Pos) {
	list := a.autoClosed[v]
	if len(list) > 20 {
		list = list[1:]
	}
	a.autoClosed[v] = append(list, p)
}

func (a *App) isAutoClosed(v *EditorView, p buffer.Pos) bool {
	for _, q := range a.autoClosed[v] {
		if q == p {
			return true
		}
	}
	return false
}

func (a *App) transformAutoClosed(v *EditorView, ch buffer.Change) {
	list := a.autoClosed[v]
	if len(list) == 0 {
		return
	}
	out := list[:0]
	for _, p := range list {
		// edits that remove the closing character drop it from the list
		if !p.Less(ch.Old.Start) && p.Less(ch.Old.End) {
			continue
		}
		np := buffer.TransformPos(p, ch.Old, ch.NewEnd, true)
		if np.Line == p.Line || ch.Old.Start.Line != p.Line {
			out = append(out, np)
		}
	}
	a.autoClosed[v] = out
}

//#endregion

// docChanged is called after every buffer change of an open document.
func (a *App) docChanged(d *Document) {
	a.scheduleAutoSave()
	a.outline.docChanged(d)
}

func (a *App) scheduleAutoSave() {
	mode := a.settings.String("files.autoSave", "")
	if mode != "afterDelay" {
		return
	}
	delay := time.Duration(a.settings.Int("files.autoSaveDelay", "", 1000)) * time.Millisecond
	if a.autoSaveTimer != nil {
		a.autoSaveTimer.Stop()
	}
	a.autoSaveTimer = time.AfterFunc(delay, func() {
		a.Post(func() {
			// auto save after delay skips formatting and other save participants, like VS Code
			for _, d := range a.docs {
				if d.Buf.Dirty() && d.Buf.Path != "" && !d.Virtual {
					a.writeDocument(d)
				}
			}
		})
	})
}

func (a *App) saveAllDirty() {
	for _, d := range a.docs {
		if d.Buf.Dirty() && d.Buf.Path != "" && !d.Virtual {
			a.saveDocument(d, false)
		}
	}
}

// relPath returns path relative to the workspace root when possible.
func (a *App) relPath(path string) string {
	if a.root != "" {
		if rel, err := filepath.Rel(a.root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

func (a *App) rootName() string {
	if a.root == "" {
		return ""
	}
	return filepath.Base(a.root)
}

func (a *App) applyTerminalTitle() {
	if a.screen == nil {
		return
	}
	title := a.windowTitle()
	a.screen.SetTitle(title)
}

func (a *App) windowTitle() string {
	tpl := a.settings.String("window.title", "")
	dirty, active, folder := "", "", a.rootName()
	if t := a.ActiveTab(); t != nil {
		active = a.ActiveGroup().tabLabel(t)
		if t.special == nil && t.buf().Dirty() {
			dirty = "● "
		}
	}
	r := strings.NewReplacer(
		"${dirty}", dirty,
		"${activeEditorShort}", active,
		"${activeEditorMedium}", active,
		"${activeEditorLong}", active,
		"${rootName}", folder,
		"${folderName}", folder,
		"${appName}", AppName,
		"${remoteName}", "",
		"${profileName}", "",
	)
	t := r.Replace(tpl)
	// collapse separators around empty parts
	parts := strings.Split(t, "${separator}")
	var keep []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			keep = append(keep, strings.TrimSpace(p))
		}
	}
	return strings.Join(keep, " - ")
}

// writeTTY writes a raw control sequence to the terminal.
func (a *App) writeTTY(seq string) {
	if a.screen == nil {
		return
	}
	if tty, ok := a.screen.Tty(); ok && tty != nil {
		_, _ = tty.Write([]byte(seq))
	}
}

// attachScreen sets up a screen without running the event loop (used by tests).
func (a *App) attachScreen(s tcell.Screen) {
	a.screen = s
	a.cv = &Canvas{s: s}
	a.width, a.height = s.Size()
}

// hitMark returns the current hit stack position, for addHitUnder.
func (a *App) hitMark() int { return len(a.hits) }

// addHitUnder registers a container region below the hits added after mark,
// so rows drawn inside it keep priority.
func (a *App) addHitUnder(mark int, r Rect, h func(ev MouseEvent) bool) {
	if r.Empty() {
		return
	}
	if mark > len(a.hits) {
		mark = len(a.hits)
	}
	a.hits = append(a.hits, hit{})
	copy(a.hits[mark+1:], a.hits[mark:])
	a.hits[mark] = hit{r: r, handler: h}
}
