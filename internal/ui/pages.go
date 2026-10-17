package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/config"
	"tuicode/internal/fuzzy"
	"tuicode/internal/syntax"
)

func sortedLanguagesWithServers() []*syntax.Language { return syntax.SortedLanguages() }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (a *App) openSpecial(p SpecialPage) {
	g := a.ActiveGroup()
	for i, t := range g.tabs {
		if t.special != nil && t.special.Title() == p.Title() {
			g.setActive(i)
			a.focusEditor()
			return
		}
	}
	v := &EditorView{app: a, special: p}
	g.addTab(v, false)
	a.focusEditor()
}

//#region Welcome

// WelcomePage is the start page.
type WelcomePage struct {
	app      *App
	selected int
}

type welcomeLink struct {
	label string
	key   string
	run   func()
}

func (a *App) openWelcome() {
	a.openSpecial(&WelcomePage{app: a})
}

// Title implements SpecialPage.
func (w *WelcomePage) Title() string { return "Welcome" }

func (w *WelcomePage) links() []welcomeLink {
	a := w.app
	links := []welcomeLink{
		{"New File...", a.keyLabel("workbench.action.files.newUntitledFile"), func() { a.RunCommand("workbench.action.files.newUntitledFile", nil) }},
		{"Open File...", a.keyLabel("workbench.action.files.openFile"), func() { a.RunCommand("workbench.action.files.openFile", nil) }},
		{"Open Folder...", a.keyLabel("workbench.action.files.openFolder"), func() { a.RunCommand("workbench.action.files.openFolder", nil) }},
		{"Go to File...", a.keyLabel("workbench.action.quickOpen"), func() { a.RunCommand("workbench.action.quickOpen", nil) }},
	}
	st := a.loadState()
	for i, f := range st.RecentFolders {
		if i >= 6 {
			break
		}
		folder := f
		links = append(links, welcomeLink{"recent:" + filepath.Base(f), a.homeRel(filepath.Dir(f)), func() {
			if config.IsWorkspaceFile(folder) {
				a.openWorkspace(folder)
			} else {
				a.openFolder(folder)
			}
		}})
	}
	return links
}

// HandleKey implements SpecialPage.
func (w *WelcomePage) HandleKey(key, text string, isText bool) bool {
	links := w.links()
	switch key {
	case "up":
		w.selected = max(w.selected-1, 0)
	case "down":
		w.selected = max(min(w.selected+1, len(links)-1), 0)
	case "enter":
		if w.selected < len(links) {
			links[w.selected].run()
		}
	default:
		return false
	}
	return true
}

// Draw implements SpecialPage.
func (w *WelcomePage) Draw(r Rect) {
	a := w.app
	th := a.theme
	bgc := th.C("editor.background")
	fg := th.C("editor.foreground")
	base := styleFB(fg, bgc)
	desc := styleFB(th.C("descriptionForeground"), bgc)
	link := styleFB(th.C("textLink.foreground"), bgc)
	x := r.X + max(r.W/10, 2)
	y := r.Y + max(r.H/10, 1)
	a.cv.Text(x, y, r.X+r.W, "tuicode", base.Bold(true))
	a.cv.Text(x, y+1, r.X+r.W, "Editing evolved, now in your terminal", desc)
	y += 3
	colW := max((r.W-(x-r.X))/2-2, 30)
	a.cv.Text(x, y, x+colW, "Start", base.Bold(true))
	links := w.links()
	ly := y + 1
	recentShown := false
	for i, l := range links {
		label := l.label
		st := link
		if strings.HasPrefix(label, "recent:") {
			if !recentShown {
				ly++
				a.cv.Text(x, ly, x+colW, "Recent", base.Bold(true))
				ly++
				recentShown = true
			}
			label = strings.TrimPrefix(label, "recent:")
		} else {
			label = a.welcomeIcon(i) + " " + label
		}
		if i == w.selected && a.focus == FocusEditor {
			st = st.Underline(true)
		}
		end := a.cv.Text(x, ly, x+colW, label, st)
		if l.key != "" {
			a.cv.Text(end+2, ly, x+colW, l.key, desc)
		}
		run := l.run
		idx := i
		a.addHit(Rect{x, ly, end - x, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				run()
			case MouseMove:
				w.selected = idx
			}
			return true
		})
		ly++
	}
	if !recentShown {
		ly++
		a.cv.Text(x, ly, x+colW, "Recent", base.Bold(true))
		a.cv.Text(x, ly+1, x+colW, "You have no recent folders, open a folder to start.", desc)
	}
	// tips column
	tx := x + colW + 2
	if tx+20 < r.X+r.W {
		a.cv.Text(tx, y, r.X+r.W, "Walkthrough: Get Started", base.Bold(true))
		tips := []struct{ text, cmd string }{
			{"Show All Commands", "workbench.action.showCommands"},
			{"Go to File", "workbench.action.quickOpen"},
			{"Find in Files", "workbench.action.findInFiles"},
			{"Toggle Terminal", "workbench.action.terminal.toggleTerminal"},
			{"Source Control", "workbench.view.scm"},
			{"Color Theme", "workbench.action.selectTheme"},
			{"Settings", "workbench.action.openSettings"},
			{"Keyboard Shortcuts", "workbench.action.openGlobalKeybindings"},
		}
		for i, t := range tips {
			ty := y + 1 + i
			end := a.cv.Text(tx, ty, r.X+r.W, "• "+t.text, link)
			if k := a.keyLabel(t.cmd); k != "" {
				a.cv.Text(end+2, ty, r.X+r.W, k, desc)
			}
			cmd := t.cmd
			a.addHit(Rect{tx, ty, end - tx, 1}, func(ev MouseEvent) bool {
				if ev.Kind == MousePress {
					a.RunCommand(cmd, nil)
				}
				return true
			})
		}
		ny := y + len(tips) + 2
		notes := []string{
			"VS Code settings and keybindings are imported automatically.",
			"Mouse: click, drag-select, Alt+click for multi-cursor, wheel to scroll.",
			"Language servers are detected on your PATH (see Language Servers view).",
		}
		for i, n := range notes {
			for j, l := range wrapText(n, r.X+r.W-tx-1) {
				a.cv.Text(tx, ny+i*2+j, r.X+r.W, l, desc)
			}
		}
	}
	// startup checkbox
	cy := r.Y + r.H - 2
	if cy > ly+1 {
		show := a.settings.String("workbench.startupEditor", "") == "welcomePage"
		box := a.checkbox(show)
		end := a.cv.Text(x, cy, r.X+r.W, box+" Show welcome page on startup", base)
		a.addHit(Rect{x, cy, end - x, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				val := "welcomePage"
				if show {
					val = "none"
				}
				_ = setUserSetting(a, "workbench.startupEditor", val)
			}
			return true
		})
	}
}

func (a *App) welcomeIcon(i int) string {
	switch i {
	case 0:
		return a.Icon(IcoNewFile)
	case 1:
		return a.Icon(IcoFile)
	case 2:
		return a.Icon(IcoFolderOpen)
	}
	return a.Icon(IcoSearch)
}

//#endregion

//#region Keyboard shortcuts

// KeybindingsPage lists and edits keyboard shortcuts.
type KeybindingsPage struct {
	app       *App
	search    TextInput
	rows      []kbRow
	selected  int
	scroll    int
	recording bool
	recorded  []string
	focusList bool
}

type kbRow struct {
	cmd    string
	title  string
	key    string
	when   string
	source string
	pos    []int
}

func (a *App) openKeybindingsPage() {
	p := &KeybindingsPage{app: a}
	p.search = newTextInput(a, "Type to search in keybindings")
	p.search.OnChange = p.filter
	p.filter()
	a.openSpecial(p)
}

// Title implements SpecialPage.
func (p *KeybindingsPage) Title() string { return "Keyboard Shortcuts" }

func (p *KeybindingsPage) filter() {
	a := p.app
	q := strings.ToLower(strings.TrimSpace(p.search.Text()))
	bound := map[string]bool{}
	var rows []kbRow
	for i := len(a.bindings) - 1; i >= 0; i-- {
		b := a.bindings[i]
		title := b.Command
		if c, ok := a.commands[b.Command]; ok && c.Title != "" {
			title = c.FullTitle()
		}
		src := "Default"
		if b.Source != "default" {
			src = "User"
			if strings.Contains(b.Source, "Code") {
				src = "VS Code"
			}
		}
		rows = append(rows, kbRow{cmd: b.Command, title: title, key: config.FormatKey(b.Keys), when: b.WhenSrc, source: src})
		bound[b.Command] = true
	}
	for id, c := range a.commands {
		if !bound[id] && !c.Hidden && c.Title != "" {
			rows = append(rows, kbRow{cmd: id, title: c.FullTitle()})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if (rows[i].key == "") != (rows[j].key == "") {
			return rows[i].key != ""
		}
		return rows[i].title < rows[j].title
	})
	if q != "" {
		var out []kbRow
		for _, r := range rows {
			if strings.Contains(strings.ToLower(r.key), q) || strings.Contains(strings.ToLower(r.cmd), q) {
				out = append(out, r)
				continue
			}
			if _, pos, ok := fuzzy.Match(q, r.title); ok {
				r.pos = pos
				out = append(out, r)
			}
		}
		rows = out
	}
	p.rows = rows
	p.selected = 0
	p.scroll = 0
}

// HandleKey implements SpecialPage.
func (p *KeybindingsPage) HandleKey(key, text string, isText bool) bool {
	a := p.app
	if p.recording {
		switch key {
		case "escape":
			p.recording = false
		case "enter":
			if len(p.recorded) > 0 && p.selected < len(p.rows) {
				p.saveBinding(p.rows[p.selected].cmd, strings.Join(p.recorded, " "))
			}
			p.recording = false
		default:
			if len(p.recorded) >= 2 {
				p.recorded = nil
			}
			p.recorded = append(p.recorded, key)
		}
		return true
	}
	switch key {
	case "up":
		if p.selected == 0 {
			p.focusList = false
		} else {
			p.selected--
		}
	case "down":
		if !p.focusList {
			p.focusList = true
		} else {
			p.selected = max(min(p.selected+1, len(p.rows)-1), 0)
		}
	case "pageup":
		p.selected = max(p.selected-10, 0)
	case "pagedown":
		p.selected = max(min(p.selected+10, len(p.rows)-1), 0)
	case "enter":
		if p.focusList && p.selected < len(p.rows) {
			p.recording = true
			p.recorded = nil
		}
	case "delete":
		if p.focusList && p.selected < len(p.rows) && p.rows[p.selected].key != "" {
			r := p.rows[p.selected]
			p.saveBinding("-"+r.cmd, strings.ToLower(strings.ReplaceAll(r.key, "UpArrow", "up")))
		}
	case "ctrl+c":
		if p.selected < len(p.rows) {
			r := p.rows[p.selected]
			b, _ := json.Marshal(map[string]string{"key": strings.ToLower(r.key), "command": r.cmd, "when": r.when})
			a.setClipboard(string(b))
		}
	default:
		if !p.focusList {
			return p.search.HandleKey(key, text, isText)
		}
		if isText {
			p.focusList = false
			return p.search.HandleKey(key, text, isText)
		}
		return false
	}
	p.ensureVisible()
	return true
}

func (p *KeybindingsPage) ensureVisible() {
	h := max(p.app.rEditor.H-6, 1)
	if p.selected < p.scroll {
		p.scroll = p.selected
	}
	if p.selected >= p.scroll+h {
		p.scroll = p.selected - h + 1
	}
}

func (p *KeybindingsPage) saveBinding(cmd, keys string) {
	a := p.app
	config.EnsureUserFiles()
	path := config.UserKeybindingsPath()
	data, _ := os.ReadFile(path)
	var list []map[string]any
	_ = config.UnmarshalJSONC(data, &list)
	list = append(list, map[string]any{"key": keys, "command": cmd})
	out, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	header := "// Place your key bindings in this file to override the defaults\n"
	if err := fsWriteFile(path, append([]byte(header), out...), 0o644); err != nil {
		a.notify(NotifyError, err.Error())
		return
	}
	a.applySettings()
	p.filter()
	a.setStatusMessage("Keybinding saved to " + path)
}

// Draw implements SpecialPage.
func (p *KeybindingsPage) Draw(r Rect) {
	mark := len(p.app.hits)
	a := p.app
	th := a.theme
	bgc := th.C("editor.background")
	base := styleFB(th.C("editor.foreground"), bgc)
	ibg := th.C("input.background")
	ist := styleFB(th.C("input.foreground"), ibg)
	focused := a.focus == FocusEditor && a.ActiveTab() != nil && a.ActiveTab().special == p
	a.cv.Fill(Rect{r.X + 2, r.Y + 1, r.W - 4, 1}, ' ', ist)
	p.search.Draw(r.X+3, r.Y+1, r.W-6, ist, styleFB(th.C("input.placeholderForeground"), ibg), focused && !p.focusList && !p.recording)
	hint := fmt.Sprintf("%d results   Enter: change keybinding   Delete: remove   Ctrl+C: copy as JSON", len(p.rows))
	a.cv.Text(r.X+2, r.Y+2, r.X+r.W, hint, styleFB(th.C("descriptionForeground"), bgc))
	colCmd := r.X + 2
	colKey := r.X + r.W*45/100
	colWhen := r.X + r.W*65/100
	colSrc := r.X + r.W - 10
	hy := r.Y + 4
	hst := base.Bold(true)
	a.cv.Text(colCmd, hy, colKey, "Command", hst)
	a.cv.Text(colKey, hy, colWhen, "Keybinding", hst)
	a.cv.Text(colWhen, hy, colSrc, "When", hst)
	a.cv.Text(colSrc, hy, r.X+r.W, "Source", hst)
	h := r.Y + r.H - hy - 1
	p.selected = clampInt(p.selected, 0, max(len(p.rows)-1, 0))
	p.scroll = clampInt(p.scroll, 0, max(len(p.rows)-h, 0))
	keySt := styleFB(th.C("keybindingLabel.foreground"), th.OverColor("keybindingLabel.background", bgc))
	for i := 0; i < h; i++ {
		idx := p.scroll + i
		if idx >= len(p.rows) {
			break
		}
		row := p.rows[idx]
		y := hy + 1 + i
		rbg := bgc
		if i%2 == 1 {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		if idx == p.selected && p.focusList {
			rbg = th.C("list.activeSelectionBackground")
		}
		st := styleFB(th.C("editor.foreground"), rbg)
		a.cv.Fill(Rect{r.X, y, r.W, 1}, ' ', st)
		hl := st.Foreground(th.C("list.highlightForeground")).Bold(true)
		a.cv.Highlighted(colCmd, y, colKey-1, row.title, st, hl, row.pos)
		if row.key != "" {
			kx := colKey
			for j, part := range strings.Split(row.key, " ") {
				if j > 0 {
					kx = a.cv.Text(kx, y, colWhen-1, " ", st)
				}
				kx = a.cv.Text(kx, y, colWhen-1, part, keySt)
			}
		}
		a.cv.TextEllipsis(colWhen, y, colSrc-colWhen-1, row.when, st.Foreground(th.OverColor("descriptionForeground", rbg)))
		a.cv.Text(colSrc, y, r.X+r.W, row.source, st.Foreground(th.OverColor("descriptionForeground", rbg)))
		rowIdx := idx
		a.addHit(Rect{r.X, y, r.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				p.focusList = true
				p.selected = rowIdx
				if ev.Clicks >= 2 {
					p.recording = true
					p.recorded = nil
				}
			case MouseWheel:
				p.scroll = clampInt(p.scroll+ev.WheelDY*3, 0, max(len(p.rows)-h, 0))
			}
			return true
		})
	}
	a.addHitUnder(mark, Rect{r.X, hy, r.W, h + 1}, func(ev MouseEvent) bool {
		if ev.Kind == MouseWheel {
			p.scroll = clampInt(p.scroll+ev.WheelDY*3, 0, max(len(p.rows)-h, 0))
		}
		return true
	})
	if p.recording {
		w := min(60, r.W-4)
		bx := r.X + (r.W-w)/2
		by := r.Y + r.H/3
		box := Rect{bx, by, w, 5}
		wbg := th.C("editorWidget.background")
		a.cv.Fill(box, ' ', styleFB(th.C("editorWidget.foreground"), wbg))
		a.cv.Box(box, styleFB(th.C("focusBorder"), wbg))
		a.cv.TextCenter(bx, by+1, w, "Press desired key combination and then press ENTER.", styleFB(th.C("editorWidget.foreground"), wbg))
		keys := config.FormatKey(p.recorded)
		a.cv.TextCenter(bx, by+3, w, keys, keySt)
	}
}

//#endregion

//#region Settings

// SettingsPage is a searchable settings editor backed by settings.json.
type SettingsPage struct {
	app       *App
	search    TextInput
	keys      []string
	selected  int
	scroll    int
	focusList bool
}

var settingEnums = map[string][]string{
	"editor.wordWrap":                        {"off", "on", "wordWrapColumn", "bounded"},
	"editor.lineNumbers":                     {"on", "off", "relative"},
	"editor.renderWhitespace":                {"none", "boundary", "selection", "trailing", "all"},
	"editor.cursorStyle":                     {"line", "block", "underline"},
	"editor.cursorBlinking":                  {"blink", "solid"},
	"editor.autoClosingBrackets":             {"always", "languageDefined", "beforeWhitespace", "never"},
	"editor.renderLineHighlight":             {"none", "gutter", "line", "all"},
	"editor.showFoldingControls":             {"always", "never", "mouseover"},
	"editor.acceptSuggestionOnEnter":         {"on", "smart", "off"},
	"editor.wordBasedSuggestions":            {"off", "currentDocument", "matchingDocuments", "allDocuments"},
	"editor.inlayHints.enabled":              {"on", "onUnlessPressed", "offUnlessPressed", "off"},
	"editor.occurrencesHighlight":            {"off", "singleFile", "multiFile"},
	"editor.matchBrackets":                   {"never", "near", "always"},
	"editor.multiCursorModifier":             {"ctrlCmd", "alt"},
	"files.autoSave":                         {"off", "afterDelay", "onFocusChange", "onWindowChange"},
	"files.eol":                              {"\n", "\r\n", "auto"},
	"workbench.startupEditor":                {"none", "welcomePage", "newUntitledFile"},
	"workbench.editor.showTabs":              {"multiple", "single", "none"},
	"workbench.activityBar.location":         {"default", "top", "bottom", "hidden"},
	"window.menuBarVisibility":               {"classic", "visible", "toggle", "hidden", "compact"},
	"tuicode.icons":                          {"auto", "nerd", "unicode"},
	"terminal.integrated.cursorStyle":        {"block", "line", "underline"},
	"terminal.integrated.rightClickBehavior": {"default", "copyPaste", "paste", "selectWord", "nothing"},
	"workbench.tree.renderIndentGuides":      {"none", "onHover", "always"},
}

func (a *App) openSettingsUI() {
	p := &SettingsPage{app: a}
	p.search = newTextInput(a, "Search settings")
	p.search.OnChange = p.filter
	p.filter()
	a.openSpecial(p)
}

func (a *App) openUserSettingsJSON() {
	config.EnsureUserFiles()
	a.OpenFile(config.UserSettingsPath(), false)
}

// Title implements SpecialPage.
func (p *SettingsPage) Title() string { return "Settings" }

func (p *SettingsPage) filter() {
	q := strings.ToLower(p.search.Text())
	var keys []string
	for k := range config.Defaults {
		if q == "" || strings.Contains(strings.ToLower(k), q) {
			keys = append(keys, k)
		}
	}
	keys = append(keys, "tuicode.icons")
	sort.Strings(keys)
	// dedupe
	out := keys[:0]
	for i, k := range keys {
		if i == 0 || keys[i-1] != k {
			out = append(out, k)
		}
	}
	p.keys = out
	p.selected = 0
	p.scroll = 0
}

func settingLabel(key string) (string, string) {
	i := strings.LastIndex(key, ".")
	if i < 0 {
		return "", key
	}
	cat := key[:i]
	name := key[i+1:]
	// camelCase to Title Case
	var sb strings.Builder
	for j, r := range name {
		if j == 0 {
			sb.WriteString(strings.ToUpper(string(r)))
			continue
		}
		if r >= 'A' && r <= 'Z' {
			sb.WriteRune(' ')
		}
		sb.WriteRune(r)
	}
	parts := strings.Split(cat, ".")
	for k, part := range parts {
		if part != "" {
			parts[k] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " › "), sb.String()
}

func (p *SettingsPage) edit(key string) {
	a := p.app
	cur := a.settings.Get(key, "")
	switch v := cur.(type) {
	case bool:
		_ = setUserSetting(a, key, !v)
		a.applySettings()
		return
	case float64:
		a.promptInput(key, "Enter a number", fmt.Sprint(v), func(s string) {
			var f float64
			if _, err := fmt.Sscan(s, &f); err == nil {
				_ = setUserSetting(a, key, f)
				a.applySettings()
			}
		})
		return
	}
	if opts, ok := settingEnums[key]; ok {
		var items []QuickItem
		for _, o := range opts {
			label := strings.ReplaceAll(strings.ReplaceAll(o, "\r", "\\r"), "\n", "\\n")
			items = append(items, QuickItem{Label: label, Data: o})
		}
		a.openQuick(&QuickPick{Title: key, Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
			if it != nil {
				_ = setUserSetting(a, key, it.Data.(string))
				a.applySettings()
			}
		}})
		return
	}
	switch cur.(type) {
	case string, nil:
		s, _ := cur.(string)
		if key == "workbench.colorTheme" {
			a.pickTheme()
			return
		}
		a.promptInput(key, "Enter a value", s, func(val string) {
			_ = setUserSetting(a, key, val)
			a.applySettings()
		})
	default:
		a.openUserSettingsJSON()
		a.setStatusMessage("Edit " + key + " in settings.json")
	}
}

// HandleKey implements SpecialPage.
func (p *SettingsPage) HandleKey(key, text string, isText bool) bool {
	switch key {
	case "up":
		if p.selected == 0 {
			p.focusList = false
		} else {
			p.selected--
		}
	case "down":
		if !p.focusList {
			p.focusList = true
		} else {
			p.selected = max(min(p.selected+1, len(p.keys)-1), 0)
		}
	case "pageup":
		p.selected = max(p.selected-5, 0)
	case "pagedown":
		p.selected = max(min(p.selected+5, len(p.keys)-1), 0)
	case "enter", "space":
		if p.focusList && p.selected < len(p.keys) {
			p.edit(p.keys[p.selected])
		}
	default:
		if !p.focusList || isText {
			p.focusList = false
			return p.search.HandleKey(key, text, isText)
		}
		return false
	}
	h := max((p.app.rEditor.H-5)/3, 1)
	if p.selected < p.scroll {
		p.scroll = p.selected
	}
	if p.selected >= p.scroll+h {
		p.scroll = p.selected - h + 1
	}
	return true
}

// Draw implements SpecialPage.
func (p *SettingsPage) Draw(r Rect) {
	mark := len(p.app.hits)
	a := p.app
	th := a.theme
	bgc := th.C("editor.background")
	base := styleFB(th.C("editor.foreground"), bgc)
	ibg := th.C("input.background")
	ist := styleFB(th.C("input.foreground"), ibg)
	focused := a.focus == FocusEditor && a.ActiveTab() != nil && a.ActiveTab().special == p
	a.cv.Fill(Rect{r.X + 2, r.Y + 1, r.W - 4, 1}, ' ', ist)
	p.search.Draw(r.X+3, r.Y+1, r.W-6, ist, styleFB(th.C("input.placeholderForeground"), ibg), focused && !p.focusList)
	link := styleFB(th.C("textLink.foreground"), bgc)
	lbl := "Open Settings (JSON)"
	lx := r.X + r.W - StrWidth(lbl) - 3
	a.cv.Text(lx, r.Y+2, r.X+r.W, lbl, link)
	a.addHit(Rect{lx, r.Y + 2, StrWidth(lbl), 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			a.openUserSettingsJSON()
		}
		return true
	})
	a.cv.Text(r.X+2, r.Y+2, lx, fmt.Sprintf("User  ·  %d settings", len(p.keys)), styleFB(th.C("descriptionForeground"), bgc))
	y := r.Y + 4
	h := (r.Y + r.H - y) / 3
	p.selected = clampInt(p.selected, 0, max(len(p.keys)-1, 0))
	p.scroll = clampInt(p.scroll, 0, max(len(p.keys)-h, 0))
	for i := 0; i < h; i++ {
		idx := p.scroll + i
		if idx >= len(p.keys) {
			break
		}
		key := p.keys[idx]
		cat, name := settingLabel(key)
		ry := y + i*3
		sel := idx == p.selected && p.focusList
		rbg := bgc
		if sel {
			rbg = th.OverColor("list.hoverBackground", bgc)
			a.cv.Fill(Rect{r.X + 1, ry, r.W - 2, 2}, ' ', styleFB(th.C("editor.foreground"), rbg))
			a.cv.Set(r.X+1, ry, '▎', styleFB(th.C("focusBorder"), rbg))
			a.cv.Set(r.X+1, ry+1, '▎', styleFB(th.C("focusBorder"), rbg))
		}
		st := base.Background(rbg)
		x := a.cv.Text(r.X+3, ry, r.X+r.W, cat+": ", st.Foreground(th.C("descriptionForeground")))
		a.cv.Text(x, ry, r.X+r.W, name, st.Bold(true).Foreground(th.C("settings.headerForeground")))
		val := a.settings.Get(key, "")
		var vs string
		switch v := val.(type) {
		case bool:
			if v {
				vs = a.checkbox(true) + " enabled"
			} else {
				vs = a.checkbox(false) + " disabled"
			}
		case string:
			vs = fmt.Sprintf("%q", v)
		case nil:
			vs = "null"
		default:
			b, _ := json.Marshal(v)
			vs = string(b)
		}
		vst := styleFB(th.C("dropdown.foreground"), th.C("dropdown.background"))
		a.cv.TextEllipsis(r.X+3, ry+1, min(r.W-6, 70), " "+vs+" ", vst)
		k := key
		rowIdx := idx
		a.addHit(Rect{r.X, ry, r.W, 2}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				p.focusList = true
				p.selected = rowIdx
				if ev.Y == ry+1 {
					p.edit(k)
				}
			case MouseWheel:
				p.scroll = clampInt(p.scroll+ev.WheelDY, 0, max(len(p.keys)-h, 0))
			}
			return true
		})
	}
	a.addHitUnder(mark, Rect{r.X, y, r.W, r.H - (y - r.Y)}, func(ev MouseEvent) bool {
		if ev.Kind == MouseWheel {
			p.scroll = clampInt(p.scroll+ev.WheelDY, 0, max(len(p.keys)-h, 0))
		}
		return true
	})
}

//#endregion

//#region Markdown preview

// MarkdownPreview renders a markdown document.
type MarkdownPreview struct {
	app    *App
	doc    *Document
	scroll int
	lines  []mdLine
	ver    int
	width  int
}

func (a *App) openMarkdownPreview(v *EditorView, side bool) {
	if side {
		a.splitEditor()
		// the split duplicated the editor; replace it with the preview
		g := a.ActiveGroup()
		if len(g.tabs) > 0 {
			g.removeTab(g.active)
		}
	}
	a.openSpecial(&MarkdownPreview{app: a, doc: v.doc, ver: -1})
}

// Title implements SpecialPage.
func (m *MarkdownPreview) Title() string { return "Preview " + m.doc.Title() }

// HandleKey implements SpecialPage.
func (m *MarkdownPreview) HandleKey(key, text string, isText bool) bool {
	switch key {
	case "up":
		m.scroll--
	case "down":
		m.scroll++
	case "pageup":
		m.scroll -= 20
	case "pagedown":
		m.scroll += 20
	case "home", "ctrl+home":
		m.scroll = 0
	case "end", "ctrl+end":
		m.scroll = len(m.lines)
	default:
		return false
	}
	return true
}

// Draw implements SpecialPage.
func (m *MarkdownPreview) Draw(r Rect) {
	a := m.app
	th := a.theme
	w := min(r.W-4, 100)
	if m.ver != m.doc.Buf.Version() || m.width != w {
		m.lines = renderMarkdown(a, m.doc.Buf.Text(), w, "")
		m.ver = m.doc.Buf.Version()
		m.width = w
	}
	m.scroll = clampInt(m.scroll, 0, max(len(m.lines)-r.H+1, 0))
	bgc := th.C("editor.background")
	x := r.X + (r.W-w)/2
	for i := 0; i < r.H; i++ {
		idx := m.scroll + i
		if idx >= len(m.lines) {
			break
		}
		m.lines[idx].draw(a, x, r.Y+i, x+w, bgc)
	}
	a.addHit(r, func(ev MouseEvent) bool {
		if ev.Kind == MouseWheel {
			m.scroll = clampInt(m.scroll+ev.WheelDY*3, 0, max(len(m.lines)-r.H+1, 0))
		}
		return ev.Kind == MouseWheel
	})
	_ = tcell.ColorDefault
}

//#endregion

// checkbox returns a checkbox glyph.
func (a *App) checkbox(on bool) string {
	if a.nerdIcons {
		if on {
			return "\U000f0135"
		}
		return "\U000f0131"
	}
	if on {
		return "[x]"
	}
	return "[ ]"
}
