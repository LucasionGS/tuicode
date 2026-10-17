package ui

import (
	"os"
	"path/filepath"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/config"
	"tuicode/internal/git"
)

// registerMoreCommands adds the less common VS Code commands.
func (a *App) registerMoreCommands() {
	//#region Editor
	a.cmd("editor.action.deleteAllLeft", "", "Delete All Left", a.edit(func(v *EditorView) { v.deleteAllSide(true) }))
	a.cmd("editor.action.deleteAllRight", "", "Delete All Right", a.edit(func(v *EditorView) { v.deleteAllSide(false) }))
	a.cmd("editor.action.transpose", "", "Transpose Characters around the Cursor", a.edit(func(v *EditorView) { v.transposeChars() }))
	a.cmd("editor.action.removeBrackets", "", "Remove Brackets", a.edit(func(v *EditorView) { v.removeEnclosingBrackets() }))
	a.cmd("editor.action.fixAll", "", "Fix All", a.ed(func(v *EditorView) { a.showCodeActions(v, "source.fixAll") }))
	a.cmd("editor.action.forceRetokenize", "", "Developer: Force Retokenize", a.ed(func(v *EditorView) {
		v.doc.hlVersion = -1
		v.doc.semVersion = -1
	}))
	a.cmd("editor.action.toggleRenderControlCharacter", "View", "Toggle Control Characters", func(any) {
		_ = setUserSetting(a, "editor.renderControlCharacters", !a.settings.Bool("editor.renderControlCharacters", ""))
	})
	a.cmd("editor.action.changeTabDisplaySize", "", "Change Tab Display Size", a.ed(func(v *EditorView) {
		var items []QuickItem
		for n := 1; n <= 8; n++ {
			items = append(items, QuickItem{Label: itoaSimple(n), Data: n})
		}
		a.openQuick(&QuickPick{Placeholder: "Select Tab Size for the Current File", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
			if it != nil {
				v.buf().TabSize = it.Data.(int)
				v.wrapCache = nil
			}
		}})
	}))
	a.alias("editor.action.goToLastEditLocation", "workbench.action.navigateToLastEditLocation")
	//#endregion

	//#region Editor groups
	a.cmd("workbench.action.firstEditorInGroup", "View", "Open First Editor in Group", func(any) {
		if g := a.ActiveGroup(); len(g.tabs) > 0 {
			g.setActive(0)
		}
	})
	a.cmd("workbench.action.closeEditorsToTheLeft", "View", "Close Editors to the Left in Group", func(any) {
		g := a.ActiveGroup()
		act := g.active
		a.closeEditorsWhere(g, func(i int) bool { return i < act && !g.tabs[i].pinned })
	})
	a.cmd("workbench.action.closeEditorsAndGroup", "View", "Close Editor Group", func(any) {
		if len(a.groups) > 1 {
			a.closeGroup(a.activeGroup)
		} else {
			a.closeAllInGroup(a.ActiveGroup())
		}
	})
	a.cmd("workbench.action.splitEditorLeft", "View", "Split Editor Left", func(any) {
		a.splitEditorDir(false)
		a.moveGroupTo(a.activeGroup, a.activeGroup-1)
	})
	a.cmd("workbench.action.splitEditorUp", "View", "Split Editor Up", func(any) {
		a.splitEditorDir(true)
		a.moveGroupTo(a.activeGroup, a.activeGroup-1)
	})
	a.cmd("workbench.action.moveEditorToRightGroup", "View", "Move Editor into Right Group", func(any) { a.moveEditorDir(1, 0) })
	a.cmd("workbench.action.moveEditorToLeftGroup", "View", "Move Editor into Left Group", func(any) { a.moveEditorDir(-1, 0) })
	a.cmd("workbench.action.moveEditorToBelowGroup", "View", "Move Editor into Group Below", func(any) { a.moveEditorDir(0, 1) })
	a.cmd("workbench.action.moveEditorToAboveGroup", "View", "Move Editor into Group Above", func(any) { a.moveEditorDir(0, -1) })
	a.cmd("workbench.action.toggleMaximizeEditorGroup", "View", "Toggle Maximize Editor Group", func(any) { a.toggleMaximizeGroup() })
	a.alias("workbench.action.minimizeOtherEditors", "workbench.action.toggleMaximizeEditorGroup")
	a.cmd("workbench.action.increaseViewSize", "View", "Increase Current View Size", func(any) { a.resizeFocusedView(2) })
	a.cmd("workbench.action.decreaseViewSize", "View", "Decrease Current View Size", func(any) { a.resizeFocusedView(-2) })
	a.alias("workbench.action.quickOpenPreviousRecentlyUsedEditor", "workbench.action.quickOpenPreviousRecentlyUsedEditorInGroup")
	//#endregion

	//#region Focus
	a.cmd("workbench.action.focusSideBar", "View", "Focus into Primary Side Bar", func(any) {
		a.sidebarVisible = true
		a.setFocus(FocusSidebar)
	})
	a.cmd("workbench.action.focusPanel", "View", "Focus into Panel", func(any) { a.showPanel(a.panelView) })
	a.cmd("workbench.action.focusActivityBar", "View", "Focus Activity Bar", func(any) {
		a.sidebarVisible = true
		a.setFocus(FocusSidebar)
	})
	a.cmd("workbench.action.focusBreadcrumbs", "View", "Focus Breadcrumbs", func(any) { a.RunCommand("breadcrumbs.focusAndSelect", nil) })
	//#endregion

	//#region Files and workspaces
	a.alias("workbench.action.files.newFile", "workbench.action.files.newUntitledFile")
	a.alias("workbench.action.files.openFileFolder", "workbench.action.files.openFile")
	a.alias("explorer.openToSide", "explorer.openToSideCommand")
	a.cmd("explorer.openToSideCommand", "", "Open to the Side", func(any) {
		if n := a.explorer.current(); n != nil && !n.isDir {
			a.splitEditor()
			a.OpenFile(n.path, false)
		}
	})
	a.cmd("filesExplorer.findInFolder", "", "Find in Folder...", func(any) {
		if n := a.explorer.current(); n != nil {
			p := n.path
			if !n.isDir {
				p = filepath.Dir(p)
			}
			a.searchV.setInclude(a.relPath(p))
			a.findInFiles(false)
		}
	})
	a.alias("workbench.files.action.createFileFromExplorer", "explorer.newFile")
	a.cmd("workbench.action.addRootFolder", "Workspaces", "Add Folder to Workspace...", func(any) {
		start := a.root
		if start == "" {
			start, _ = os.Getwd()
		}
		a.pathPicker("Add Folder to Workspace", filepath.Dir(start)+string(filepath.Separator), true, func(p string) { a.addRootFolder(p) })
	})
	a.cmd("workbench.action.removeRootFolder", "Workspaces", "Remove Folder from Workspace...", func(any) {
		if a.workspace == nil || len(a.workspace.Folders) < 2 {
			a.setStatusMessage("Only folders of a multi-root workspace can be removed.")
			return
		}
		var items []QuickItem
		for i, f := range a.workspace.Folders {
			items = append(items, QuickItem{Label: f.Name, Description: a.homeRel(f.Path), Data: i})
		}
		a.openQuick(&QuickPick{Placeholder: "Select workspace folder to remove", Items: items, OnAccept: func(it *QuickItem, q string) {
			if it == nil {
				return
			}
			ws := a.workspace
			i := it.Data.(int)
			ws.Folders = append(ws.Folders[:i], ws.Folders[i+1:]...)
			a.saveWorkspaceFile()
		}})
	})
	a.cmd("workbench.action.saveWorkspaceAs", "Workspaces", "Save Workspace As...", func(any) {
		if a.root == "" {
			return
		}
		initial := filepath.Join(a.root, filepath.Base(a.root)+".code-workspace")
		if a.workspace != nil {
			initial = a.workspace.File
		}
		a.promptInput("Save Workspace As", "Path of the .code-workspace file", initial, func(p string) {
			p = expandHome(p)
			if p == "" {
				return
			}
			if !config.IsWorkspaceFile(p) {
				p += ".code-workspace"
			}
			if a.workspace == nil {
				a.workspace = &config.Workspace{Folders: a.folders()}
			}
			a.workspace.File = p
			a.saveWorkspaceFile()
		})
	})
	a.alias("workbench.action.openGlobalSettings", "workbench.action.openSettings")
	a.alias("workbench.action.openWorkspaceSettings", "workbench.action.openSettings")
	a.alias("workbench.action.openFolderSettings", "workbench.action.openWorkspaceSettingsFile")
	a.alias("workbench.action.openDefaultKeybindingsFile", "workbench.action.openGlobalKeybindingsFile")
	a.alias("notifications.showList", "notifications.toggleList")
	a.alias("notifications.focusToasts", "notifications.toggleList")
	a.alias("workbench.action.showErrorsWarnings", "workbench.panel.markers.view.focus")
	//#endregion

	//#region Search results
	a.cmd("search.action.focusNextSearchResult", "Search", "Focus Next Search Result", func(any) { a.searchV.stepResult(1) })
	a.cmd("search.action.focusPreviousSearchResult", "Search", "Focus Previous Search Result", func(any) { a.searchV.stepResult(-1) })
	a.cmd("search.action.collapseSearchResults", "Search", "Collapse All", func(any) {
		for _, f := range a.searchV.results {
			f.collapsed = true
		}
		a.searchV.rebuild()
	})
	a.cmd("search.action.clearSearchResults", "Search", "Clear Search Results", func(any) {
		a.searchV.results = nil
		a.searchV.query.SetText("")
		a.searchV.rebuild()
	})
	a.cmd("search.action.refreshSearchResults", "Search", "Refresh", func(any) { a.searchV.run() })
	a.alias("workbench.view.search.focus", "workbench.view.search")
	//#endregion

	//#region Tasks
	a.cmd("workbench.action.tasks.terminate", "Tasks", "Terminate Task", func(any) { a.terminateTask() })
	a.cmd("workbench.action.tasks.restartTask", "Tasks", "Restart Running Task", func(any) {
		if t, ok := a.lastTask(); ok {
			a.terminateTask()
			a.runTask(t)
		}
	})
	a.cmd("workbench.action.tasks.reRunTask", "Tasks", "Rerun Last Task", func(any) {
		if t, ok := a.lastTask(); ok {
			a.runTask(t)
		} else {
			a.setStatusMessage("There is no last task to rerun.")
		}
	})
	a.cmd("workbench.action.tasks.showTasks", "Tasks", "Show Running Tasks", func(any) {
		var items []QuickItem
		for _, ti := range a.terminals.terms {
			if ti.task != nil && !ti.task.done {
				items = append(items, QuickItem{Label: ti.task.label, Data: ti.id})
			}
		}
		if len(items) == 0 {
			a.setStatusMessage("There are no running tasks.")
			return
		}
		a.openQuick(&QuickPick{Placeholder: "Select the task to show its output", Items: items, OnAccept: func(it *QuickItem, q string) {
			if it != nil {
				a.terminals.activate(it.Data.(int))
				a.showPanel("terminal")
			}
		}})
	})
	a.alias("workbench.action.tasks.configureTaskRunner", "workbench.action.tasks.configureDefaultBuildTask")
	//#endregion

	//#region Terminal
	a.cmd("workbench.action.terminal.sendSequence", "Terminal", "Send Custom Sequence to Terminal", func(args any) {
		m, _ := args.(map[string]any)
		text, _ := m["text"].(string)
		if cur := a.terminals.current(); cur != nil && text != "" {
			cur.t.WriteInput([]byte(a.substituteVars(text)))
		}
	})
	a.alias("workbench.action.terminal.newInActiveWorkspace", "workbench.action.terminal.new")
	//#endregion

	//#region Debug
	a.cmd("workbench.debug.viewlet.action.enableAllBreakpoints", "Debug", "Enable All Breakpoints", func(any) { a.setAllBreakpoints(false) })
	a.cmd("workbench.debug.viewlet.action.disableAllBreakpoints", "Debug", "Disable All Breakpoints", func(any) { a.setAllBreakpoints(true) })
	//#endregion

	//#region Git
	a.cmd("git.createTag", "Git", "Create Tag", func(any) {
		a.promptInput("Git: Create Tag", "Tag name", "", func(name string) {
			if name == "" {
				return
			}
			a.promptInput("Git: Create Tag", "Message (leave empty for a lightweight tag)", "", func(msg string) {
				root := a.gitRoot
				a.gitRun(func() error {
					args := []string{"tag", name}
					if msg != "" {
						args = []string{"tag", "-a", name, "-m", msg}
					}
					_, err := git.Run(root, args...)
					return err
				})
			})
		})
	})
	a.cmd("git.deleteBranch", "Git", "Delete Branch...", func(any) {
		a.pickBranch("Select a branch to delete", false, func(b string) {
			root := a.gitRoot
			a.gitRun(func() error {
				_, err := git.Run(root, "branch", "-d", b)
				return err
			})
		})
	})
	a.cmd("git.renameBranch", "Git", "Rename Branch...", func(any) {
		a.promptInput("Git: Rename Branch", "New branch name", a.gitBranch, func(name string) {
			if name == "" || name == a.gitBranch {
				return
			}
			root := a.gitRoot
			a.gitRun(func() error {
				_, err := git.Run(root, "branch", "-m", name)
				return err
			})
		})
	})
	a.cmd("git.merge", "Git", "Merge...", func(any) {
		a.pickBranch("Select a branch to merge from", true, func(b string) {
			root := a.gitRoot
			a.gitRun(func() error {
				_, err := git.Run(root, "merge", b)
				return err
			})
		})
	})
	a.cmd("git.rebase", "Git", "Rebase Branch...", func(any) {
		a.pickBranch("Select a branch to rebase onto", true, func(b string) {
			root := a.gitRoot
			a.gitRun(func() error {
				_, err := git.Run(root, "rebase", b)
				return err
			})
		})
	})
	a.cmd("git.publish", "Git", "Publish Branch", func(any) {
		branch := a.gitBranch
		root := a.gitRoot
		a.gitRun(func() error {
			_, err := git.Run(root, "push", "-u", "origin", branch)
			return err
		})
	})
	a.cmd("git.showOutput", "Git", "Show Git Output", func(any) {
		a.output.selectChannel("Git")
		a.showPanel("output")
	})
	a.cmd("git.revertChange", "Git", "Revert Change", a.ed(func(v *EditorView) { v.revertHunks(v.hunksInSelections()) }))
	//#endregion
}

// deleteAllSide deletes from each cursor to the start (left) or end of its line.
func (v *EditorView) deleteAllSide(left bool) {
	var edits []buffer.Edit
	for _, s := range v.sels {
		r := s.Range()
		if r.Empty() {
			if left {
				r.Start.Col = 0
			} else {
				r.End.Col = v.buf().LineLen(r.End.Line)
			}
		}
		edits = append(edits, buffer.Edit{Range: r})
	}
	v.buf().CloseUndoGroup()
	v.applyDeletes(edits)
}

// transposeChars swaps the characters around each cursor.
func (v *EditorView) transposeChars() {
	b := v.buf()
	var edits []buffer.Edit
	for _, s := range v.sels {
		p := s.Active
		line := b.Line(p.Line)
		if len(line) < 2 {
			continue
		}
		c := p.Col
		if c >= len(line) {
			c = len(line) - 1
		}
		if c == 0 {
			c = 1
		}
		edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: p.Line, Col: c - 1}, End: buffer.Pos{Line: p.Line, Col: c + 1}}, Text: string([]rune{line[c], line[c-1]})})
	}
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			out[i] = buffer.Caret(r.End)
		}
		return out
	})
}

// removeEnclosingBrackets deletes the bracket pair around the cursor.
func (v *EditorView) removeEnclosingBrackets() {
	p := v.Cursor()
	open, ok := v.bracketNearCursor(p)
	if ok {
		if r := v.buf().RuneAt(open); r == ')' || r == ']' || r == '}' {
			if m, ok2 := v.matchBracket(open); ok2 {
				open = m
			}
		}
	} else if open, ok = v.enclosingBracket(p); !ok {
		return
	}
	closePos, ok := v.matchBracket(open)
	if !ok {
		return
	}
	v.buf().CloseUndoGroup()
	v.applyEdits([]buffer.Edit{
		{Range: buffer.Range{Start: open, End: buffer.Pos{Line: open.Line, Col: open.Col + 1}}},
		{Range: buffer.Range{Start: closePos, End: buffer.Pos{Line: closePos.Line, Col: closePos.Col + 1}}},
	}, "", nil)
}

// moveGroupTo moves group i to index j, keeping the layout flags in place.
func (a *App) moveGroupTo(i, j int) {
	if i == j || i < 0 || j < 0 || i >= len(a.groups) || j >= len(a.groups) {
		return
	}
	a.groups[i], a.groups[j] = a.groups[j], a.groups[i]
	a.groups[i].below, a.groups[j].below = a.groups[j].below, a.groups[i].below
	a.activeGroup = j
}

// moveEditorDir moves the active editor into the neighbouring group in a direction, creating it if needed.
func (a *App) moveEditorDir(dx, dy int) {
	g := a.ActiveGroup()
	v := g.Active()
	if v == nil {
		return
	}
	from := a.activeGroup
	a.focusGroupDir(dx, dy)
	if a.activeGroup == from {
		if len(g.tabs) < 2 || len(a.groups) >= maxEditorGroups {
			return
		}
		a.insertGroup(dy != 0)
		if dx < 0 || dy < 0 {
			a.moveGroupTo(a.activeGroup, from)
			from++
		}
	}
	tg := a.ActiveGroup()
	idx := g.indexOf(v)
	g.tabs = append(g.tabs[:idx], g.tabs[idx+1:]...)
	g.removeMRU(v)
	if len(g.tabs) > 0 {
		g.setActive(min(idx, len(g.tabs)-1))
	}
	tg.addTab(v, false)
	if len(g.tabs) == 0 {
		a.removeGroupAt(a.groupIndex(g))
		a.activeGroup = a.groupIndex(tg)
	}
	a.focusEditor()
}

// toggleMaximizeGroup hides the other editor groups, or brings them back.
func (a *App) toggleMaximizeGroup() {
	if a.hiddenGroups != nil {
		active := a.ActiveGroup()
		a.groups = a.hiddenGroups
		a.hiddenGroups = nil
		a.activeGroup = a.groupIndex(active)
		return
	}
	if len(a.groups) < 2 {
		return
	}
	a.hiddenGroups = a.groups
	a.groups = []*EditorGroup{a.ActiveGroup()}
	a.activeGroup = 0
}

// resizeFocusedView grows or shrinks the side bar or panel that has focus.
func (a *App) resizeFocusedView(d int) {
	switch a.focus {
	case FocusSidebar:
		a.sidebarWidth = clampInt(a.sidebarWidth+d, 15, a.width-30)
	case FocusPanel:
		a.panelHeight = clampInt(a.panelHeight+d, 4, a.height-6)
	}
}

// addRootFolder adds a folder, turning a single folder into a multi-root workspace.
func (a *App) addRootFolder(p string) {
	p = filepath.Clean(expandHome(p))
	if a.workspace == nil {
		folders := a.folders()
		folders = append(folders, config.WorkspaceFolder{Name: filepath.Base(p), Path: p})
		a.workspace = &config.Workspace{Folders: folders}
		a.notify(NotifyInfo, "The folder was added to an untitled workspace. Use \"Save Workspace As...\" to keep it.")
		a.explorer.setRoot(a.root)
		a.fileIdx = nil
		return
	}
	for _, f := range a.workspace.Folders {
		if f.Path == p {
			return
		}
	}
	a.workspace.Folders = append(a.workspace.Folders, config.WorkspaceFolder{Name: filepath.Base(p), Path: p})
	a.saveWorkspaceFile()
}

// saveWorkspaceFile writes the folders of the workspace back to its `.code-workspace` file.
func (a *App) saveWorkspaceFile() {
	ws := a.workspace
	if ws == nil {
		return
	}
	if ws.File != "" {
		if err := config.SaveWorkspace(ws, fsWriteFile); err != nil {
			a.notify(NotifyError, err.Error())
			return
		}
		a.addRecentFolder(ws.File)
	}
	a.explorer.setRoot(a.root)
	a.fileIdx = nil
}

// setAllBreakpoints enables or disables every breakpoint.
func (a *App) setAllBreakpoints(disabled bool) {
	for path, list := range a.breakpoints {
		for _, bp := range list {
			bp.Disabled = disabled
		}
		a.syncBreakpoints(path)
	}
}

// pickBranch lets the user choose a branch, remote ones included when remotes is true.
func (a *App) pickBranch(placeholder string, remotes bool, done func(string)) {
	if a.gitRoot == "" {
		return
	}
	args := []string{"branch", "--format=%(refname:short)"}
	if remotes {
		args = append(args, "-a")
	}
	out, err := git.Run(a.gitRoot, args...)
	if err != nil {
		a.notify(NotifyError, err.Error())
		return
	}
	var items []QuickItem
	for _, b := range strings.Split(strings.TrimSpace(out), "\n") {
		if b == "" || b == a.gitBranch || strings.HasSuffix(b, "/HEAD") {
			continue
		}
		items = append(items, QuickItem{Label: b, Data: b})
	}
	a.openQuick(&QuickPick{Placeholder: placeholder, Items: items, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			done(it.Data.(string))
		}
	}})
}

// terminateTask kills the running task (asking which one when several run).
func (a *App) terminateTask() {
	var running []*TermInstance
	for _, ti := range a.terminals.terms {
		if ti.task != nil && !ti.task.done {
			running = append(running, ti)
		}
	}
	if len(running) == 0 {
		a.setStatusMessage("There are no running tasks.")
		return
	}
	kill := func(ti *TermInstance) {
		ti.t.Close()
	}
	if len(running) == 1 {
		kill(running[0])
		return
	}
	var items []QuickItem
	for _, ti := range running {
		items = append(items, QuickItem{Label: ti.task.label, Data: ti})
	}
	a.openQuick(&QuickPick{Placeholder: "Select a task to terminate", Items: items, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			kill(it.Data.(*TermInstance))
		}
	}})
}

// lastTask returns the most recently started task.
func (a *App) lastTask() (task, bool) {
	if a.lastRunTask == nil {
		return task{}, false
	}
	return *a.lastRunTask, true
}
