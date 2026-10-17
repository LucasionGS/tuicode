package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tuicode/internal/buffer"
	"tuicode/internal/config"
	"tuicode/internal/lsp"
	"tuicode/internal/syntax"
)

// OpenFile opens (or focuses) a file in the active group.
func (a *App) OpenFile(path string, preview bool) *EditorView {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		a.openFolder(path)
		return nil
	}
	if isImagePath(path) {
		a.openImage(path)
		return nil
	}
	d, ok := a.docs[path]
	if !ok {
		if st, err := os.Stat(path); err == nil && st.Size() > 64*1024*1024 {
			a.notify(NotifyError, fmt.Sprintf("The file %s is too large to open (%d MB).", filepath.Base(path), st.Size()>>20))
			return nil
		}
		if isBinaryFile(path) {
			a.notify(NotifyWarning, "The file "+filepath.Base(path)+" is not displayed in the text editor because it is either binary or uses an unsupported text encoding.")
			return nil
		}
		b, err := buffer.Load(path)
		if err != nil {
			a.notify(NotifyError, "Unable to open '"+filepath.Base(path)+"': "+err.Error())
			return nil
		}
		if a.opts.ReadOnly {
			b.ReadOnly = true
		}
		d = a.newDocument(b)
		a.docs[path] = d
		a.requestHighlight(d)
		a.loadGitBase(d)
		if a.lsp != nil {
			a.lsp.Open(b)
			a.setDiagnostics(d)
		}
		a.addRecentFile(path)
	}
	if !a.settings.Bool("workbench.editor.enablePreview", "") {
		preview = false
	}
	v := a.showDocument(d, preview)
	return v
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8000)
	n, _ := f.Read(buf)
	for _, c := range buf[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

// NewUntitled creates a new untitled document.
func (a *App) NewUntitled() *EditorView {
	a.untitledCounter++
	b := buffer.New()
	b.UntitledName = fmt.Sprintf("Untitled-%d", a.untitledCounter)
	d := a.newDocument(b)
	d.untitledN = a.untitledCounter
	a.docs["untitled:"+b.UntitledName] = d
	v := a.newEditorView(d)
	a.ActiveGroup().addTab(v, false)
	a.focusEditor()
	return v
}

func (a *App) openFolder(path string) {
	if err := checkSandbox(path); err != nil {
		a.notify(NotifyError, err.Error())
		return
	}
	a.saveSessionSafe()
	a.closeAllEditors()
	a.root = path
	a.explorer.setRoot(path)
	if a.lsp != nil {
		a.lsp.ShutdownAll()
		a.setupLSP()
	}
	a.fileIdx = nil
	a.loadSettings()
	a.refreshGit()
	a.addRecentFolder(path)
	a.restoreSession()
	a.showSidebar("explorer")
}

func (a *App) saveActive(as bool) {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		return
	}
	if as || v.buf().Path == "" {
		a.promptSaveAs(v.doc, nil)
		return
	}
	a.saveDocument(v.doc, false)
}

func (a *App) saveAll() {
	for _, d := range a.docs {
		if d.Buf.Dirty() && !d.Virtual {
			if d.Buf.Path == "" {
				a.promptSaveAs(d, nil)
				return
			}
			a.saveDocument(d, false)
		}
	}
}

// saveDocument saves, applying save participants (format, trim). It returns false on failure.
func (a *App) saveDocument(d *Document, _ bool) bool {
	if d.Virtual {
		return true
	}
	if d.Buf.Path == "" {
		a.promptSaveAs(d, nil)
		return false
	}
	lang := d.Lang.ID
	v := a.viewForDoc(d)
	hasLSP := a.lsp != nil && a.lsp.Client(d.Buf) != nil && v != nil
	format := func() {
		if a.settings.Bool("editor.formatOnSave", lang) && hasLSP {
			a.formatDocument(v, nil, func() {
				a.runSaveParticipants(d)
				a.writeDocument(d)
			})
			return
		}
		a.runSaveParticipants(d)
		a.writeDocument(d)
	}
	if kinds := a.codeActionsOnSave(lang); len(kinds) > 0 && hasLSP {
		a.runCodeActionsOnSave(v, kinds, format)
		return true
	}
	format()
	return true
}

// codeActionsOnSave returns the code action kinds enabled for saving.
func (a *App) codeActionsOnSave(lang string) []string {
	var kinds []string
	m, _ := a.settings.Get("editor.codeActionsOnSave", lang).(map[string]any)
	for k, v := range m {
		switch x := v.(type) {
		case bool:
			if x {
				kinds = append(kinds, k)
			}
		case string:
			if x == "explicit" || x == "always" {
				kinds = append(kinds, k)
			}
		}
	}
	sort.Strings(kinds)
	return kinds
}

// runCodeActionsOnSave applies code actions of the given kinds one after another.
func (a *App) runCodeActionsOnSave(v *EditorView, kinds []string, done func()) {
	if len(kinds) == 0 {
		done()
		return
	}
	kind := kinds[0]
	rest := kinds[1:]
	b := v.buf()
	params := map[string]any{
		"textDocument": map[string]any{"uri": lsp.PathToURI(b.Path)},
		"range":        lsp.Range{Start: lsp.Position{}, End: a.lsp.ToLSP(b, b.EndPos())},
		"context":      map[string]any{"diagnostics": []lsp.Diagnostic{}, "only": []string{kind}, "triggerKind": 2},
	}
	version := b.Version()
	finished := false
	timer := afterFunc(1500, func() {
		a.Post(func() {
			if !finished {
				finished = true
				a.runCodeActionsOnSave(v, rest, done)
			}
		})
	})
	_, ok := a.lsp.Request(b, "textDocument/codeAction", params, func(raw json.RawMessage, err error) {
		if finished {
			return
		}
		finished = true
		timer.Stop()
		if err == nil && b.Version() == version {
			for _, ca := range parseCodeActions(raw) {
				if ca.Edit != nil {
					a.applyWorkspaceEdit(*ca.Edit)
				}
			}
		}
		a.runCodeActionsOnSave(v, rest, done)
	})
	if !ok {
		finished = true
		timer.Stop()
		a.runCodeActionsOnSave(v, rest, done)
	}
}

func (a *App) saveDocumentNoFormat(d *Document) bool {
	return a.writeDocument(d)
}

func (a *App) viewForDoc(d *Document) *EditorView {
	if v := a.ActiveView(); v != nil && v.special == nil && v.doc == d {
		return v
	}
	for v := range d.views {
		return v
	}
	return nil
}

func (a *App) runSaveParticipants(d *Document) {
	lang := d.Lang.ID
	b := d.Buf
	var edits []buffer.Edit
	if a.settings.Bool("files.trimTrailingWhitespace", lang) {
		for l := 0; l < b.LineCount(); l++ {
			line := b.Line(l)
			n := len(line)
			for n > 0 && (line[n-1] == ' ' || line[n-1] == '\t') {
				n--
			}
			if n < len(line) {
				edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l, Col: n}, End: buffer.Pos{Line: l, Col: len(line)}}})
			}
		}
	}
	if len(edits) > 0 {
		b.ApplyEdits(edits, nil, "save")
		edits = nil
	}
	if a.settings.Bool("files.trimFinalNewlines", lang) {
		last := b.LineCount() - 1
		n := last
		for n > 0 && b.LineLen(n) == 0 && b.LineLen(n-1) == 0 {
			n--
		}
		if n < last {
			b.ApplyEdits([]buffer.Edit{{Range: buffer.Range{Start: buffer.Pos{Line: n}, End: b.EndPos()}}}, nil, "save")
		}
	}
	if a.settings.Bool("files.insertFinalNewline", lang) {
		if last := b.LineCount() - 1; b.LineLen(last) != 0 {
			end := b.EndPos()
			b.ApplyEdits([]buffer.Edit{{Range: buffer.Range{Start: end, End: end}, Text: "\n"}}, nil, "save")
		}
	}
}

func (a *App) writeDocument(d *Document) bool {
	if err := d.Buf.Save(); err != nil {
		a.showDialog("Failed to save '"+d.Title()+"'", err.Error(), []string{"Retry", "Save As...", "Cancel"}, func(c int) {
			switch c {
			case 0:
				a.writeDocument(d)
			case 1:
				a.promptSaveAs(d, nil)
			}
		})
		return false
	}
	if a.lsp != nil {
		a.lsp.Saved(d.Buf)
	}
	a.loadGitBase(d)
	a.refreshGit()
	a.explorer.refreshSoon()
	// saving settings files reloads them
	if d.Buf.Path == config.UserSettingsPath() || d.Buf.Path == config.UserKeybindingsPath() || strings.HasSuffix(d.Buf.Path, filepath.Join(".vscode", "settings.json")) {
		a.loadSettings()
		a.setStatusMessage("Settings reloaded")
	}
	return true
}

// promptSaveAs asks for a path and saves there.
func (a *App) promptSaveAs(d *Document, after func()) {
	initial := d.Buf.Path
	if initial == "" {
		dir := a.root
		if dir == "" {
			dir, _ = os.Getwd()
		}
		initial = filepath.Join(dir, d.Title())
	}
	a.promptInput("Save As", "Enter a file path", initial, func(path string) {
		if path == "" {
			return
		}
		path = expandHome(path)
		if !filepath.IsAbs(path) && a.root != "" {
			path = filepath.Join(a.root, path)
		}
		old := d.Buf.Path
		oldKey := ""
		for k, x := range a.docs {
			if x == d {
				oldKey = k
			}
		}
		if _, err := os.Stat(path); err == nil && path != old {
			a.showDialog("'"+filepath.Base(path)+"' already exists. Do you want to replace it?", "A file with the same name already exists. Replacing it will overwrite its current contents.", []string{"Replace", "Cancel"}, func(c int) {
				if c == 0 {
					a.finishSaveAs(d, path, oldKey, after)
				}
			})
			return
		}
		a.finishSaveAs(d, path, oldKey, after)
	})
}

func (a *App) finishSaveAs(d *Document, path, oldKey string, after func()) {
	if a.lsp != nil {
		a.lsp.Close(d.Buf)
	}
	if err := d.Buf.SaveAs(path); err != nil {
		a.notify(NotifyError, "Failed to save: "+err.Error())
		return
	}
	delete(a.docs, oldKey)
	a.docs[path] = d
	first := ""
	if d.Buf.LineCount() > 0 {
		first = d.Buf.LineString(0)
	}
	if d.Lang.ID == "plaintext" {
		a.SetLanguage(d, a.detectLanguage(path, first))
	} else if a.lsp != nil {
		a.lsp.Open(d.Buf)
	}
	a.addRecentFile(path)
	a.loadGitBase(d)
	a.refreshGit()
	a.explorer.refreshSoon()
	if after != nil {
		after()
	}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// promptInput shows a single text input in the quick input area.
func (a *App) promptInput(title, placeholder, initial string, done func(string)) {
	q := a.openQuick(&QuickPick{Title: title, Placeholder: placeholder, Value: initial, NoFilter: true, OnAccept: func(it *QuickItem, query string) {
		done(strings.TrimSpace(query))
	}})
	q.message = "Press 'Enter' to confirm or 'Escape' to cancel"
}

// promptOpenFile shows a path picker that completes directory entries.
func (a *App) promptOpenFile() {
	start := a.root
	if v := a.ActiveView(); v != nil && v.special == nil && v.buf().Path != "" {
		start = filepath.Dir(v.buf().Path)
	}
	if start == "" {
		start, _ = os.Getwd()
	}
	a.pathPicker("Open File", start+string(filepath.Separator), false, func(p string) {
		a.OpenFile(p, false)
		a.focusEditor()
	})
}

func (a *App) promptOpenFolder() {
	start := a.root
	if start == "" {
		start, _ = os.Getwd()
	}
	a.pathPicker("Open Folder", filepath.Dir(start)+string(filepath.Separator), true, func(p string) {
		a.openFolder(p)
	})
}

// pathPicker is a simple file system browser in the quick input.
func (a *App) pathPicker(title, initial string, folders bool, done func(string)) {
	p := &QuickPick{Title: title, Value: initial, KeepOrder: true}
	var q *QuickInput
	list := func(text string) {
		dir := text
		prefix := ""
		if !strings.HasSuffix(text, string(filepath.Separator)) {
			dir = filepath.Dir(text)
			prefix = filepath.Base(text)
		}
		dir = expandHome(dir)
		entries, err := os.ReadDir(dir)
		var items []QuickItem
		if folders {
			items = append(items, QuickItem{Label: "Open this folder", Description: dir, Data: "open:" + dir, AlwaysShow: true})
		}
		items = append(items, QuickItem{Label: "..", Data: "dir:" + filepath.Dir(strings.TrimSuffix(dir, string(filepath.Separator))), AlwaysShow: true})
		if err == nil {
			for _, e := range entries {
				name := e.Name()
				if prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
					continue
				}
				full := filepath.Join(dir, name)
				isDir := e.IsDir()
				if !isDir && e.Type()&os.ModeSymlink != 0 {
					if st, err := os.Stat(full); err == nil && st.IsDir() {
						isDir = true
					}
				}
				if isDir {
					items = append(items, QuickItem{Label: name + "/", Icon: a.Icon(IcoFolder), Data: "dir:" + full})
				} else if !folders {
					icon, col := a.FileIcon(name, false, false)
					items = append(items, QuickItem{Label: name, Icon: icon, IconColor: col, Data: "file:" + full})
				}
			}
		}
		p.Items = items
		p.NoFilter = true
		if q != nil {
			q.refilter()
		}
	}
	p.OnChange = list
	p.OnAccept = func(it *QuickItem, text string) {
		if it == nil {
			// a typed path that does not exist yet: create/open it
			path := expandHome(text)
			if path != "" && !folders {
				done(path)
			}
			return
		}
		data := it.Data.(string)
		switch {
		case strings.HasPrefix(data, "dir:"):
			next := strings.TrimPrefix(data, "dir:")
			if !strings.HasSuffix(next, string(filepath.Separator)) {
				next += string(filepath.Separator)
			}
			a.pathPicker(title, next, folders, done)
		case strings.HasPrefix(data, "open:"):
			done(strings.TrimPrefix(data, "open:"))
		case strings.HasPrefix(data, "file:"):
			done(strings.TrimPrefix(data, "file:"))
		}
	}
	q = a.openQuick(p)
	if len(q.filtered) > 1 {
		q.selected = 1
		if folders {
			q.selected = 0
		}
	}
}

//#region Groups

func (a *App) closeAllEditors() {
	for _, g := range a.groups {
		a.closeEditorsWhere(g, func(int) bool { return true })
	}
}

// closeEditorsWhere closes matching tabs (dirty tabs prompt first).
func (a *App) closeEditorsWhere(g *EditorGroup, pred func(i int) bool) {
	var targets []*EditorView
	for i, t := range g.tabs {
		if pred(i) {
			targets = append(targets, t)
		}
	}
	var dirty []*EditorView
	for _, t := range targets {
		if t.special == nil && t.buf().Dirty() && t.doc.refs <= 1 && !t.doc.Virtual {
			dirty = append(dirty, t)
		}
	}
	closeAll := func() {
		for _, t := range targets {
			if i := g.indexOf(t); i >= 0 {
				a.closeTabNow(g, i)
			}
		}
	}
	if len(dirty) == 0 {
		closeAll()
		return
	}
	a.showDialog(fmt.Sprintf("Do you want to save the changes to the following %d files?", len(dirty)), editorNames(dirty), []string{"Save All", "Don't Save", "Cancel"}, func(c int) {
		switch c {
		case 0:
			for _, t := range dirty {
				if !a.saveDocument(t.doc, false) {
					return
				}
			}
			closeAll()
		case 1:
			closeAll()
		}
	})
}

func editorNames(vs []*EditorView) string {
	var names []string
	for _, v := range vs {
		names = append(names, v.doc.Title())
	}
	return strings.Join(names, "\n")
}

func (a *App) closeGroup(i int) {
	if len(a.groups) <= 1 || i < 0 || i >= len(a.groups) {
		return
	}
	g := a.groups[i]
	for len(g.tabs) > 0 {
		g.removeTab(0)
	}
	a.groups = append(a.groups[:i], a.groups[i+1:]...)
	if a.activeGroup >= len(a.groups) {
		a.activeGroup = len(a.groups) - 1
	}
}

func (a *App) splitEditor() {
	v := a.ActiveView()
	if len(a.groups) >= 4 {
		a.setStatusMessage("Too many editor groups")
		return
	}
	ng := a.newGroup()
	at := a.activeGroup + 1
	a.groups = append(a.groups, nil)
	copy(a.groups[at+1:], a.groups[at:])
	a.groups[at] = ng
	a.activeGroup = at
	if v != nil && v.special == nil {
		nv := a.newEditorView(v.doc)
		nv.SetSelections(append([]buffer.Selection{}, v.sels...), v.primary)
		nv.topLine, nv.topSeg = v.topLine, v.topSeg
		nv.wrap = v.wrap
		ng.addTab(nv, false)
	}
	a.focusEditor()
}

func (a *App) focusGroup(i int) {
	if i < 0 {
		return
	}
	if i >= len(a.groups) {
		// focusing a group beyond the last splits like VS Code
		if i == len(a.groups) {
			a.splitEditor()
		}
		return
	}
	a.activeGroup = i
	a.focusEditor()
}

func (a *App) cycleEditor(dir int) {
	g := a.ActiveGroup()
	if len(g.tabs) == 0 {
		return
	}
	g.setActive((g.active + dir + len(g.tabs)) % len(g.tabs))
	a.focusEditor()
}

func (a *App) moveTab(dir int) {
	g := a.ActiveGroup()
	i := g.active
	j := i + dir
	if j < 0 || j >= len(g.tabs) {
		return
	}
	g.tabs[i], g.tabs[j] = g.tabs[j], g.tabs[i]
	g.active = j
}

func (a *App) moveEditorToGroup(dir int) {
	g := a.ActiveGroup()
	v := g.Active()
	if v == nil {
		return
	}
	target := a.activeGroup + dir
	if target < 0 {
		return
	}
	if target >= len(a.groups) {
		if len(a.groups) >= 4 {
			return
		}
		a.groups = append(a.groups, a.newGroup())
	}
	tg := a.groups[target]
	// detach without disposing
	idx := g.active
	g.tabs = append(g.tabs[:idx], g.tabs[idx+1:]...)
	g.removeMRU(v)
	if len(g.tabs) > 0 {
		g.setActive(min(idx, len(g.tabs)-1))
	}
	tg.addTab(v, false)
	a.activeGroup = target
	if len(g.tabs) == 0 {
		gi := a.groupIndex(g)
		a.groups = append(a.groups[:gi], a.groups[gi+1:]...)
		a.activeGroup = a.groupIndex(tg)
	}
	a.focusEditor()
}

func (a *App) moveGroup(dir int) {
	i := a.activeGroup
	j := i + dir
	if j < 0 || j >= len(a.groups) {
		return
	}
	a.groups[i], a.groups[j] = a.groups[j], a.groups[i]
	a.activeGroup = j
}

func (a *App) joinAllGroups() {
	if len(a.groups) <= 1 {
		return
	}
	first := a.groups[0]
	for _, g := range a.groups[1:] {
		for _, t := range g.tabs {
			dup := false
			for _, ft := range first.tabs {
				if ft.special == nil && t.special == nil && ft.doc == t.doc {
					dup = true
				}
			}
			if dup {
				a.disposeView(t)
				continue
			}
			first.tabs = append(first.tabs, t)
		}
	}
	a.groups = []*EditorGroup{first}
	a.activeGroup = 0
}

// editorPicker implements Ctrl+Tab: pick from recently used editors.
func (a *App) editorPicker(dir int) {
	g := a.ActiveGroup()
	if len(g.mru) == 0 {
		return
	}
	var items []QuickItem
	for _, v := range g.mru {
		label := g.tabLabel(v)
		desc := ""
		if v.special == nil && v.buf().Path != "" {
			desc = filepath.Dir(a.relPath(v.buf().Path))
			if desc == "." {
				desc = ""
			}
		}
		icon, col := a.FileIcon(label, false, false)
		if v.special == nil && v.buf().Dirty() {
			label = "● " + label
		}
		items = append(items, QuickItem{Label: label, Description: desc, Icon: icon, IconColor: col, Data: v})
	}
	q := a.openQuick(&QuickPick{Placeholder: "Select an editor", Items: items, KeepOrder: true, MatchDescription: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		v := it.Data.(*EditorView)
		if i := g.indexOf(v); i >= 0 {
			g.setActive(i)
		}
		a.focusEditor()
	}})
	if len(q.filtered) > 1 {
		if dir > 0 {
			q.selected = 1
		} else {
			q.selected = len(q.filtered) - 1
		}
	}
}

func (a *App) showAllEditors() {
	var items []QuickItem
	for gi, g := range a.groups {
		for _, v := range g.tabs {
			label := g.tabLabel(v)
			icon, col := a.FileIcon(label, false, false)
			desc := ""
			if len(a.groups) > 1 {
				desc = fmt.Sprintf("Group %d", gi+1)
			}
			items = append(items, QuickItem{Label: label, Description: desc, Icon: icon, IconColor: col, Data: [2]any{gi, v}})
		}
	}
	a.openQuick(&QuickPick{Placeholder: "Search open editors by name", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		d := it.Data.([2]any)
		gi := d[0].(int)
		v := d[1].(*EditorView)
		if gi < len(a.groups) {
			a.activeGroup = gi
			if i := a.groups[gi].indexOf(v); i >= 0 {
				a.groups[gi].setActive(i)
			}
		}
		a.focusEditor()
	}})
}

//#endregion

//#region Recent files and sessions

type stateFile struct {
	RecentFiles   []string                 `json:"recentFiles"`
	RecentFolders []string                 `json:"recentFolders"`
	Workspaces    map[string]workspaceSave `json:"workspaces"`
	Commands      []string                 `json:"recentCommands"`
	SearchHistory []string                 `json:"searchHistory"`
}

type workspaceSave struct {
	Tabs          []tabSave `json:"tabs"`
	Active        int       `json:"active"`
	SidebarWidth  int       `json:"sidebarWidth"`
	SidebarView   string    `json:"sidebarView"`
	SidebarHidden bool      `json:"sidebarHidden"`
	PanelHeight   int       `json:"panelHeight"`
	PanelVisible  bool      `json:"panelVisible"`
	PanelView     string    `json:"panelView"`
	Expanded      []string  `json:"expanded"`
	Saved         time.Time `json:"saved"`
}

type tabSave struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Top  int    `json:"top"`
}

func statePath() string { return filepath.Join(config.Dir(), "state.json") }

func (a *App) loadState() *stateFile {
	if a.state != nil {
		return a.state
	}
	st := &stateFile{Workspaces: map[string]workspaceSave{}}
	if data, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(data, st)
		if st.Workspaces == nil {
			st.Workspaces = map[string]workspaceSave{}
		}
	}
	a.state = st
	if len(a.recentCommands) == 0 {
		a.recentCommands = st.Commands
	}
	return st
}

func (a *App) writeState() {
	st := a.loadState()
	st.Commands = a.recentCommands
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = fsMkdirAll(config.Dir(), 0o755)
	_ = fsWriteFile(statePath(), data, 0o644)
}

func (a *App) addRecentFile(path string) {
	st := a.loadState()
	st.RecentFiles = prependUnique(st.RecentFiles, path, 50)
}

func (a *App) addRecentFolder(path string) {
	st := a.loadState()
	st.RecentFolders = prependUnique(st.RecentFolders, path, 30)
}

func prependUnique(list []string, s string, limit int) []string {
	out := []string{s}
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (a *App) recentFiles() []string {
	st := a.loadState()
	var out []string
	for _, f := range st.RecentFiles {
		if a.root == "" || strings.HasPrefix(f, a.root+string(filepath.Separator)) {
			out = append(out, f)
		}
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func (a *App) showOpenRecent() {
	st := a.loadState()
	var items []QuickItem
	first := true
	for _, f := range st.RecentFolders {
		it := QuickItem{Label: filepath.Base(f), Description: a.homeRel(f), Icon: a.Icon(IcoFolder), Data: f}
		if first {
			it.Group = "folders"
			first = false
		}
		items = append(items, it)
	}
	first = true
	for _, f := range st.RecentFiles {
		icon, col := a.FileIcon(filepath.Base(f), false, false)
		it := QuickItem{Label: filepath.Base(f), Description: a.homeRel(filepath.Dir(f)), Icon: icon, IconColor: col, Data: f}
		if first {
			it.Group = "files"
			first = false
		}
		items = append(items, it)
	}
	a.openQuick(&QuickPick{Placeholder: "Select to open (Ctrl+R)", Items: items, KeepOrder: true, MatchDescription: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		p := it.Data.(string)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			a.openFolder(p)
		} else {
			a.OpenFile(p, false)
			a.focusEditor()
		}
	}})
}

func (a *App) homeRel(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (a *App) saveSession() {
	if a.root == "" {
		a.writeState()
		return
	}
	st := a.loadState()
	ws := workspaceSave{
		SidebarWidth:  a.sidebarWidth,
		SidebarView:   a.sidebarView,
		SidebarHidden: !a.sidebarVisible,
		PanelHeight:   a.panelHeight,
		PanelVisible:  a.panelVisible,
		PanelView:     a.panelView,
		Saved:         time.Now(),
		Expanded:      a.explorer.expandedList(),
	}
	g := a.ActiveGroup()
	for i, v := range g.tabs {
		if v.special != nil || v.buf().Path == "" || v.doc.Virtual {
			continue
		}
		if i == g.active {
			ws.Active = len(ws.Tabs)
		}
		c := v.Cursor()
		ws.Tabs = append(ws.Tabs, tabSave{Path: v.buf().Path, Line: c.Line, Col: c.Col, Top: v.topLine})
	}
	st.Workspaces[a.root] = ws
	a.addRecentFolder(a.root)
	a.writeState()
}

func (a *App) restoreSession() {
	defer a.restoreBackups()
	if a.root == "" {
		return
	}
	st := a.loadState()
	ws, ok := st.Workspaces[a.root]
	if !ok {
		return
	}
	if ws.SidebarWidth > 0 {
		a.sidebarWidth = ws.SidebarWidth
	}
	if ws.SidebarView != "" {
		a.sidebarView = ws.SidebarView
		if a.sidebarView == "search" || a.sidebarView == "outline" {
			a.sidebarView = "explorer"
		}
	}
	a.sidebarVisible = !ws.SidebarHidden
	if ws.PanelHeight > 0 {
		a.panelHeight = ws.PanelHeight
	}
	if ws.PanelView != "" {
		a.panelView = ws.PanelView
	}
	for _, e := range ws.Expanded {
		a.explorer.expanded[e] = true
	}
	a.explorer.refresh()
	g := a.ActiveGroup()
	var opened []*EditorView
	for _, t := range ws.Tabs {
		if _, err := os.Stat(t.Path); err != nil {
			continue
		}
		v := a.OpenFile(t.Path, false)
		if v == nil {
			continue
		}
		v.SetCursor(v.buf().Clamp(buffer.Pos{Line: t.Line, Col: t.Col}))
		v.topLine = clampInt(t.Top, 0, v.buf().LineCount()-1)
		opened = append(opened, v)
	}
	if ws.Active < len(opened) {
		if i := g.indexOf(opened[ws.Active]); i >= 0 {
			g.setActive(i)
		}
	}
	if len(opened) > 0 {
		// drop the welcome page when restoring editors
		for i := len(g.tabs) - 1; i >= 0; i-- {
			if _, ok := g.tabs[i].special.(*WelcomePage); ok {
				g.removeTab(i)
			}
		}
		a.focus = FocusEditor
	}
	if ws.PanelVisible {
		a.panelVisible = true
		if a.panelView == "terminal" {
			a.Post(func() {
				if len(a.terminals.terms) == 0 {
					a.terminals.newTerminal("")
				}
			})
		}
	}
}

//#endregion

//#region Hot exit

type backupEntry struct {
	Path     string `json:"path,omitempty"`
	Untitled string `json:"untitled,omitempty"`
	Text     string `json:"text"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Lang     string `json:"lang"`
}

func (a *App) backupPath() string {
	key := a.root
	if key == "" {
		key = "empty"
	}
	sum := 0
	for _, c := range key {
		sum = sum*31 + int(c)
	}
	return filepath.Join(config.Dir(), "backups", fmt.Sprintf("%x.json", uint32(sum)))
}

// writeBackups stores unsaved documents for hot exit.
func (a *App) writeBackups(dirty []*Document) error {
	var entries []backupEntry
	for _, d := range dirty {
		e := backupEntry{Path: d.Buf.Path, Text: d.Buf.Text(), Lang: d.Lang.ID}
		if d.Buf.Path == "" {
			e.Untitled = d.Buf.Name()
		}
		if v := a.viewForDoc(d); v != nil {
			c := v.Cursor()
			e.Line, e.Col = c.Line, c.Col
		}
		entries = append(entries, e)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	p := a.backupPath()
	if err := fsMkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return fsWriteFile(p, data, 0o600)
}

// restoreBackups reopens unsaved documents from the last session.
func (a *App) restoreBackups() {
	p := a.backupPath()
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	_ = os.Remove(p)
	var entries []backupEntry
	if json.Unmarshal(data, &entries) != nil {
		return
	}
	for _, e := range entries {
		var v *EditorView
		if e.Path != "" {
			v = a.OpenFile(e.Path, false)
		} else {
			v = a.NewUntitled()
			if e.Lang != "" {
				a.SetLanguage(v.doc, syntax.ByID(e.Lang))
			}
		}
		if v == nil {
			continue
		}
		b := v.buf()
		if b.Text() != e.Text {
			b.ApplyEdits([]buffer.Edit{{Range: buffer.Range{Start: buffer.Pos{}, End: b.EndPos()}, Text: e.Text}}, nil, "restore")
			b.CloseUndoGroup()
		}
		v.SetCursor(b.Clamp(buffer.Pos{Line: e.Line, Col: e.Col}))
		v.ensurePosVisible(v.Cursor(), true)
	}
	if len(entries) > 0 {
		a.setStatusMessage(fmt.Sprintf("Restored %d unsaved editor(s) from the last session", len(entries)))
	}
}

//#endregion
