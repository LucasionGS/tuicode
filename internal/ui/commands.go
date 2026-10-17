package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/config"
	"tuicode/internal/syntax"
	"tuicode/internal/theme"
)

// Command is a runnable action, addressed by its VS Code command id.
type Command struct {
	ID       string
	Title    string
	Category string
	Run      func(args any)
	Hidden   bool
	When     func(a *App) bool
}

// FullTitle returns "Category: Title".
func (c *Command) FullTitle() string {
	if c.Category == "" {
		return c.Title
	}
	return c.Category + ": " + c.Title
}

func (a *App) cmd(id, category, title string, run func(args any)) {
	a.commands[id] = &Command{ID: id, Title: title, Category: category, Run: run}
}

func (a *App) hiddenCmd(id string, run func(args any)) {
	a.commands[id] = &Command{ID: id, Run: run, Hidden: true}
}

// alias registers id as another name for target.
func (a *App) alias(id, target string) {
	a.commands[id] = &Command{ID: id, Hidden: true, Run: func(args any) { a.RunCommand(target, args) }}
}

// RunCommand executes a command by id.
func (a *App) RunCommand(id string, args any) {
	c, ok := a.commands[id]
	if !ok {
		a.setStatusMessage(fmt.Sprintf("command '%s' not found", id))
		return
	}
	defer func() {
		if r := recover(); r != nil {
			a.notify(NotifyError, fmt.Sprintf("Running the command '%s' failed: %v", id, r))
		}
	}()
	c.Run(args)
}

// ed runs fn with the active text editor.
func (a *App) ed(fn func(v *EditorView)) func(args any) {
	return func(args any) {
		v := a.ActiveView()
		if v == nil || v.special != nil {
			return
		}
		fn(v)
	}
}

// edit is like ed but also closes widgets and resets suggest state.
func (a *App) edit(fn func(v *EditorView)) func(args any) {
	return func(args any) {
		v := a.ActiveView()
		if v == nil || v.special != nil {
			return
		}
		fn(v)
		a.afterEdit(v)
	}
}

func (a *App) move(fn func(v *EditorView)) func(args any) {
	return func(args any) {
		v := a.ActiveView()
		if v == nil || v.special != nil {
			return
		}
		v.buf().CloseUndoGroup()
		fn(v)
		a.afterCursorMove(v)
	}
}

func (a *App) registerCommands() {
	a.commands = map[string]*Command{}

	//#region File
	a.cmd("workbench.action.files.newUntitledFile", "File", "New Untitled Text File", func(any) { a.NewUntitled() })
	a.alias("welcome.showNewFileEntries", "workbench.action.files.newUntitledFile")
	a.cmd("workbench.action.files.openFile", "File", "Open File...", func(any) { a.promptOpenFile() })
	a.cmd("workbench.action.files.openFolder", "File", "Open Folder...", func(any) { a.promptOpenFolder() })
	a.cmd("workbench.action.files.save", "File", "Save", func(any) { a.saveActive(false) })
	a.cmd("workbench.action.files.saveAs", "File", "Save As...", func(any) { a.saveActive(true) })
	a.cmd("workbench.action.files.saveAll", "File", "Save All", func(any) { a.saveAll() })
	a.cmd("workbench.action.files.saveWithoutFormatting", "File", "Save without Formatting", func(any) {
		if v := a.ActiveView(); v != nil && v.special == nil {
			a.saveDocumentNoFormat(v.doc)
		}
	})
	a.cmd("workbench.action.files.revert", "File", "Revert File", a.ed(func(v *EditorView) {
		if v.buf().Path == "" {
			return
		}
		if err := v.buf().Reload(); err != nil {
			a.notify(NotifyError, "Failed to revert: "+err.Error())
		}
		v.SetSelections(v.sels, v.primary)
	}))
	a.cmd("workbench.action.closeActiveEditor", "View", "Close Editor", func(any) {
		g := a.ActiveGroup()
		if len(g.tabs) == 0 {
			if len(a.groups) > 1 {
				a.closeGroup(a.activeGroup)
			}
			return
		}
		a.closeTab(g, g.active)
	})
	a.cmd("workbench.action.closeAllEditors", "View", "Close All Editors", func(any) { a.closeAllEditors() })
	a.cmd("workbench.action.closeAllGroups", "View", "Close All Editor Groups", func(any) { a.closeAllEditors() })
	a.cmd("workbench.action.closeEditorsInGroup", "View", "Close All Editors in Group", func(any) { a.closeEditorsWhere(a.ActiveGroup(), func(i int) bool { return true }) })
	a.cmd("workbench.action.closeOtherEditors", "View", "Close Other Editors in Group", func(any) {
		g := a.ActiveGroup()
		act := g.Active()
		a.closeEditorsWhere(g, func(i int) bool { return g.tabs[i] != act })
	})
	a.cmd("workbench.action.closeEditorsToTheRight", "View", "Close Editors to the Right in Group", func(any) {
		g := a.ActiveGroup()
		act := g.active
		a.closeEditorsWhere(g, func(i int) bool { return i > act })
	})
	a.cmd("workbench.action.closeUnmodifiedEditors", "View", "Close Saved Editors in Group", func(any) {
		g := a.ActiveGroup()
		a.closeEditorsWhere(g, func(i int) bool { return g.tabs[i].special != nil || !g.tabs[i].buf().Dirty() })
	})
	a.cmd("workbench.action.reopenClosedEditor", "View", "Reopen Closed Editor", func(any) {
		if n := len(a.closedTabs); n > 0 {
			t := a.closedTabs[n-1]
			a.closedTabs = a.closedTabs[:n-1]
			if v := a.OpenFile(t.path, false); v != nil {
				v.SetCursor(v.buf().Clamp(t.pos))
				v.ensurePosVisible(v.Cursor(), true)
			}
		}
	})
	a.cmd("workbench.action.keepEditor", "View", "Keep Editor", func(any) {
		if v := a.ActiveView(); v != nil {
			v.preview = false
		}
	})
	a.cmd("workbench.action.closeFolder", "Workspaces", "Close Folder", func(any) {
		a.closeAllEditors()
		a.root = ""
		a.explorer.setRoot("")
	})
	a.cmd("workbench.action.quit", "File", "Exit", func(any) { a.requestQuit() })
	a.alias("workbench.action.closeWindow", "workbench.action.quit")
	a.cmd("workbench.action.newWindow", "File", "New Window", func(any) {
		a.notify(NotifyInfo, "tuicode runs in your terminal: open a new terminal tab and run `tuicode` there.")
	})
	a.cmd("workbench.action.files.copyPathOfActiveFile", "File", "Copy Path of Active File", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			a.setClipboard(v.buf().Path)
			a.setStatusMessage("Copied " + v.buf().Path)
		}
	}))
	a.alias("copyFilePath", "workbench.action.files.copyPathOfActiveFile")
	a.cmd("copyRelativeFilePath", "File", "Copy Relative Path of Active File", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			a.setClipboard(a.relPath(v.buf().Path))
			a.setStatusMessage("Copied " + a.relPath(v.buf().Path))
		}
	}))
	a.cmd("workbench.action.files.revealActiveFileInWindows", "File", "Reveal Active File in File Manager", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			openExternal(filepath.Dir(v.buf().Path))
		}
	}))
	a.cmd("workbench.files.action.showActiveFileInExplorer", "File", "Reveal Active File in Explorer View", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			a.showSidebar("explorer")
			a.explorer.reveal(v.buf().Path, true)
		}
	}))
	a.cmd("workbench.action.openRecent", "File", "Open Recent...", func(any) { a.showOpenRecent() })
	a.cmd("workbench.action.files.showOpenedFileInNewWindow", "File", "Open Active File in New Window", func(any) {
		a.notify(NotifyInfo, "Multiple windows are not supported in a terminal.")
	})
	//#endregion

	//#region Edit
	a.cmd("undo", "Edit", "Undo", a.edit(func(v *EditorView) { v.Undo() }))
	a.cmd("redo", "Edit", "Redo", a.edit(func(v *EditorView) { v.Redo() }))
	a.cmd("editor.action.clipboardCutAction", "Edit", "Cut", a.edit(func(v *EditorView) { v.Cut() }))
	a.cmd("editor.action.clipboardCopyAction", "Edit", "Copy", a.ed(func(v *EditorView) { v.Copy() }))
	a.cmd("editor.action.clipboardPasteAction", "Edit", "Paste", a.edit(func(v *EditorView) { v.Paste() }))
	a.cmd("actions.find", "Edit", "Find", func(any) { a.openFind(false) })
	a.cmd("editor.action.startFindReplaceAction", "Edit", "Replace", func(any) { a.openFind(true) })
	a.cmd("editor.action.nextMatchFindAction", "Edit", "Find Next", func(any) { a.findMove(1) })
	a.cmd("editor.action.previousMatchFindAction", "Edit", "Find Previous", func(any) { a.findMove(-1) })
	a.cmd("editor.action.nextSelectionMatchFindAction", "Edit", "Find Next Selection", func(any) { a.findSelection(1) })
	a.cmd("editor.action.previousSelectionMatchFindAction", "Edit", "Find Previous Selection", func(any) { a.findSelection(-1) })
	a.cmd("editor.action.selectAllMatches", "Edit", "Select All Occurrences of Find Match", func(any) { a.findSelectAll() })
	a.hiddenCmd("toggleFindCaseSensitive", func(any) { a.findToggle("case") })
	a.hiddenCmd("toggleFindRegex", func(any) { a.findToggle("regex") })
	a.hiddenCmd("toggleFindWholeWord", func(any) { a.findToggle("word") })
	a.hiddenCmd("togglePreserveCase", func(any) { a.findToggle("preserve") })
	a.hiddenCmd("toggleFindInSelection", func(any) { a.findToggle("selection") })
	a.hiddenCmd("editor.action.replaceOne", func(any) { a.findReplace(false) })
	a.hiddenCmd("editor.action.replaceAll", func(any) { a.findReplace(true) })
	a.hiddenCmd("closeFindWidget", func(any) { a.closeFind() })
	a.hiddenCmd("editor.action.focusNextFindWidgetInput", func(any) { a.findCycleFocus() })
	a.cmd("workbench.action.findInFiles", "Search", "Find in Files", func(any) { a.findInFiles(false) })
	a.cmd("workbench.action.replaceInFiles", "Search", "Replace in Files", func(any) { a.findInFiles(true) })
	a.cmd("workbench.action.search.toggleQueryDetails", "Search", "Toggle Search Details", func(any) { a.searchV.toggleDetails() })
	a.hiddenCmd("search.action.submit", func(any) { a.searchV.run() })
	a.cmd("editor.action.commentLine", "Edit", "Toggle Line Comment", a.edit(func(v *EditorView) { v.ToggleLineComment(0) }))
	a.cmd("editor.action.addCommentLine", "Edit", "Add Line Comment", a.edit(func(v *EditorView) { v.ToggleLineComment(1) }))
	a.cmd("editor.action.removeCommentLine", "Edit", "Remove Line Comment", a.edit(func(v *EditorView) { v.ToggleLineComment(2) }))
	a.cmd("editor.action.blockComment", "Edit", "Toggle Block Comment", a.edit(func(v *EditorView) { v.ToggleBlockComment() }))
	a.cmd("editor.action.deleteLines", "Edit", "Delete Line", a.edit(func(v *EditorView) { v.DeleteLines() }))
	a.cmd("editor.action.insertLineAfter", "Edit", "Insert Line Below", a.edit(func(v *EditorView) { v.InsertLine(false) }))
	a.cmd("editor.action.insertLineBefore", "Edit", "Insert Line Above", a.edit(func(v *EditorView) { v.InsertLine(true) }))
	a.cmd("editor.action.moveLinesDownAction", "Edit", "Move Line Down", a.edit(func(v *EditorView) { v.MoveLines(1) }))
	a.cmd("editor.action.moveLinesUpAction", "Edit", "Move Line Up", a.edit(func(v *EditorView) { v.MoveLines(-1) }))
	a.cmd("editor.action.copyLinesDownAction", "Edit", "Copy Line Down", a.edit(func(v *EditorView) { v.CopyLines(1) }))
	a.cmd("editor.action.copyLinesUpAction", "Edit", "Copy Line Up", a.edit(func(v *EditorView) { v.CopyLines(-1) }))
	a.cmd("editor.action.duplicateSelection", "Edit", "Duplicate Selection", a.edit(func(v *EditorView) { v.DuplicateSelection() }))
	a.cmd("editor.action.indentLines", "Edit", "Indent Line", a.edit(func(v *EditorView) { v.IndentLines() }))
	a.cmd("editor.action.outdentLines", "Edit", "Outdent Line", a.edit(func(v *EditorView) { v.OutdentLines() }))
	a.cmd("editor.action.trimTrailingWhitespace", "Edit", "Trim Trailing Whitespace", a.edit(func(v *EditorView) { v.TrimTrailingWhitespace() }))
	a.cmd("editor.action.joinLines", "Edit", "Join Lines", a.edit(func(v *EditorView) { v.JoinLines() }))
	a.cmd("editor.action.transposeLetters", "Edit", "Transpose Characters around the Cursor", a.edit(func(v *EditorView) { v.Transpose() }))
	a.cmd("editor.action.transformToUppercase", "Edit", "Transform to Uppercase", a.edit(func(v *EditorView) { v.transformSelections(strings.ToUpper) }))
	a.cmd("editor.action.transformToLowercase", "Edit", "Transform to Lowercase", a.edit(func(v *EditorView) { v.transformSelections(strings.ToLower) }))
	a.cmd("editor.action.transformToTitlecase", "Edit", "Transform to Title Case", a.edit(func(v *EditorView) { v.transformSelections(toTitle) }))
	a.cmd("editor.action.transformToSnakecase", "Edit", "Transform to Snake Case", a.edit(func(v *EditorView) { v.transformSelections(toSnake) }))
	a.cmd("editor.action.transformToCamelcase", "Edit", "Transform to Camel Case", a.edit(func(v *EditorView) { v.transformSelections(func(s string) string { return toCamel(s, false) }) }))
	a.cmd("editor.action.transformToPascalcase", "Edit", "Transform to Pascal Case", a.edit(func(v *EditorView) { v.transformSelections(func(s string) string { return toCamel(s, true) }) }))
	a.cmd("editor.action.transformToKebabcase", "Edit", "Transform to Kebab Case", a.edit(func(v *EditorView) { v.transformSelections(toKebab) }))
	a.cmd("editor.action.sortLinesAscending", "Edit", "Sort Lines Ascending", a.edit(func(v *EditorView) {
		v.transformLines(func(ls []string) []string { sort.Strings(ls); return ls })
	}))
	a.cmd("editor.action.sortLinesDescending", "Edit", "Sort Lines Descending", a.edit(func(v *EditorView) {
		v.transformLines(func(ls []string) []string { sort.Sort(sort.Reverse(sort.StringSlice(ls))); return ls })
	}))
	a.cmd("editor.action.removeDuplicateLines", "Edit", "Delete Duplicate Lines", a.edit(func(v *EditorView) {
		v.transformLines(func(ls []string) []string {
			seen := map[string]bool{}
			out := ls[:0]
			for _, l := range ls {
				if !seen[l] {
					seen[l] = true
					out = append(out, l)
				}
			}
			return out
		})
	}))
	a.cmd("editor.action.reverseLines", "Edit", "Reverse Lines", a.edit(func(v *EditorView) {
		v.transformLines(func(ls []string) []string {
			for i, j := 0, len(ls)-1; i < j; i, j = i+1, j-1 {
				ls[i], ls[j] = ls[j], ls[i]
			}
			return ls
		})
	}))
	a.hiddenCmd("type.newline", a.edit(func(v *EditorView) { v.Newline() }))
	a.hiddenCmd("tab", a.edit(func(v *EditorView) { v.Tab() }))
	a.hiddenCmd("outdent", a.edit(func(v *EditorView) { v.OutdentLines() }))
	a.hiddenCmd("deleteLeft", a.edit(func(v *EditorView) {
		v.withLinkedEditing("deleteLeft", "", func() { v.Backspace(false) })
		a.afterBackspace(v)
	}))
	a.hiddenCmd("deleteRight", a.edit(func(v *EditorView) {
		v.withLinkedEditing("deleteRight", "", func() { v.DeleteForward(false) })
	}))
	a.hiddenCmd("deleteWordLeft", a.edit(func(v *EditorView) { v.Backspace(true) }))
	a.hiddenCmd("deleteWordRight", a.edit(func(v *EditorView) { v.DeleteForward(true) }))
	a.hiddenCmd("deleteAllLeft", a.edit(func(v *EditorView) {
		v.MoveHome(true)
		v.Backspace(false)
	}))
	a.hiddenCmd("deleteAllRight", a.edit(func(v *EditorView) {
		v.MoveEnd(true)
		v.DeleteForward(false)
	}))
	a.hiddenCmd("editor.action.cancelWidgets", a.ed(func(v *EditorView) { a.escapeEditor(v) }))
	a.alias("removeSecondaryCursors", "editor.action.cancelWidgets")
	a.alias("cancelSelection", "editor.action.cancelWidgets")
	a.hiddenCmd("type", func(args any) {
		if m, ok := args.(map[string]any); ok {
			if t, ok := m["text"].(string); ok {
				if v := a.ActiveView(); v != nil && v.special == nil {
					v.withLinkedEditing("type", t, func() { v.Type(t) })
					a.afterType(v, t)
				}
			}
		}
	})
	//#endregion

	//#region Selection and cursor
	a.cmd("editor.action.selectAll", "Selection", "Select All", a.move(func(v *EditorView) { v.SelectAll() }))
	a.cmd("expandLineSelection", "Selection", "Expand Line Selection", a.move(func(v *EditorView) { v.ExpandLineSelection() }))
	a.cmd("editor.action.smartSelect.expand", "Selection", "Expand Selection", a.move(func(v *EditorView) { v.ExpandSelection() }))
	a.cmd("editor.action.smartSelect.shrink", "Selection", "Shrink Selection", a.move(func(v *EditorView) { v.ShrinkSelection() }))
	a.cmd("editor.action.addSelectionToPreviousFindMatch", "Selection", "Add Previous Occurrence", a.move(func(v *EditorView) { v.AddPreviousOccurrence() }))
	a.cmd("editor.action.addSelectionToNextFindMatch", "Selection", "Add Next Occurrence", a.move(func(v *EditorView) { v.AddNextOccurrence(false) }))
	a.cmd("editor.action.moveSelectionToNextFindMatch", "Selection", "Move Last Selection to Next Find Match", a.move(func(v *EditorView) { v.AddNextOccurrence(true) }))
	a.cmd("editor.action.selectHighlights", "Selection", "Select All Occurrences", a.move(func(v *EditorView) { v.SelectAllOccurrences() }))
	a.cmd("editor.action.changeAll", "Edit", "Change All Occurrences", a.move(func(v *EditorView) { v.SelectAllOccurrences() }))
	a.cmd("editor.action.insertCursorAbove", "Selection", "Add Cursor Above", a.move(func(v *EditorView) { v.AddCursorVertical(-1) }))
	a.cmd("editor.action.insertCursorBelow", "Selection", "Add Cursor Below", a.move(func(v *EditorView) { v.AddCursorVertical(1) }))
	a.cmd("editor.action.insertCursorAtEndOfEachLineSelected", "Selection", "Add Cursors to Line Ends", a.move(func(v *EditorView) { v.CursorsAtLineEnds() }))
	a.cmd("cursorUndo", "Selection", "Cursor Undo", a.move(func(v *EditorView) { v.CursorUndo() }))
	a.cmd("cursorRedo", "Selection", "Cursor Redo", a.move(func(v *EditorView) { v.CursorRedo() }))
	a.cmd("editor.action.jumpToBracket", "Go", "Go to Bracket", a.move(func(v *EditorView) { v.JumpToBracket(false) }))
	a.cmd("editor.action.selectToBracket", "Selection", "Select to Bracket", a.move(func(v *EditorView) { v.JumpToBracket(true) }))
	a.hiddenCmd("cursorLeft", a.move(func(v *EditorView) { v.MoveLeft(false, false) }))
	a.hiddenCmd("cursorRight", a.move(func(v *EditorView) { v.MoveRight(false, false) }))
	a.hiddenCmd("cursorUp", a.move(func(v *EditorView) { v.MoveVertical(-1, false) }))
	a.hiddenCmd("cursorDown", a.move(func(v *EditorView) { v.MoveVertical(1, false) }))
	a.hiddenCmd("cursorLeftSelect", a.move(func(v *EditorView) { v.MoveLeft(true, false) }))
	a.hiddenCmd("cursorRightSelect", a.move(func(v *EditorView) { v.MoveRight(true, false) }))
	a.hiddenCmd("cursorUpSelect", a.move(func(v *EditorView) { v.MoveVertical(-1, true) }))
	a.hiddenCmd("cursorDownSelect", a.move(func(v *EditorView) { v.MoveVertical(1, true) }))
	a.hiddenCmd("cursorWordLeft", a.move(func(v *EditorView) { v.MoveLeft(false, true) }))
	a.hiddenCmd("cursorWordEndRight", a.move(func(v *EditorView) { v.MoveRight(false, true) }))
	a.hiddenCmd("cursorWordLeftSelect", a.move(func(v *EditorView) { v.MoveLeft(true, true) }))
	a.hiddenCmd("cursorWordEndRightSelect", a.move(func(v *EditorView) { v.MoveRight(true, true) }))
	a.hiddenCmd("cursorHome", a.move(func(v *EditorView) { v.MoveHome(false) }))
	a.hiddenCmd("cursorEnd", a.move(func(v *EditorView) { v.MoveEnd(false) }))
	a.hiddenCmd("cursorHomeSelect", a.move(func(v *EditorView) { v.MoveHome(true) }))
	a.hiddenCmd("cursorEndSelect", a.move(func(v *EditorView) { v.MoveEnd(true) }))
	a.hiddenCmd("cursorTop", a.move(func(v *EditorView) { a.pushNav(); v.MoveDocStart(false) }))
	a.hiddenCmd("cursorBottom", a.move(func(v *EditorView) { a.pushNav(); v.MoveDocEnd(false) }))
	a.hiddenCmd("cursorTopSelect", a.move(func(v *EditorView) { v.MoveDocStart(true) }))
	a.hiddenCmd("cursorBottomSelect", a.move(func(v *EditorView) { v.MoveDocEnd(true) }))
	a.hiddenCmd("cursorPageUp", a.move(func(v *EditorView) { v.PageMove(-1, false) }))
	a.hiddenCmd("cursorPageDown", a.move(func(v *EditorView) { v.PageMove(1, false) }))
	a.hiddenCmd("cursorPageUpSelect", a.move(func(v *EditorView) { v.PageMove(-1, true) }))
	a.hiddenCmd("cursorPageDownSelect", a.move(func(v *EditorView) { v.PageMove(1, true) }))
	a.hiddenCmd("cursorColumnSelectLeft", a.move(func(v *EditorView) { v.columnSelect(0, -1) }))
	a.hiddenCmd("cursorColumnSelectRight", a.move(func(v *EditorView) { v.columnSelect(0, 1) }))
	a.hiddenCmd("cursorColumnSelectUp", a.move(func(v *EditorView) { v.columnSelect(-1, 0) }))
	a.hiddenCmd("cursorColumnSelectDown", a.move(func(v *EditorView) { v.columnSelect(1, 0) }))
	a.hiddenCmd("cursorColumnSelectPageUp", a.move(func(v *EditorView) { v.columnSelect(-v.rect.H, 0) }))
	a.hiddenCmd("scrollLineUp", a.ed(func(v *EditorView) { v.ScrollBy(-1) }))
	a.hiddenCmd("scrollLineDown", a.ed(func(v *EditorView) { v.ScrollBy(1) }))
	a.hiddenCmd("scrollPageUp", a.ed(func(v *EditorView) { v.ScrollBy(-v.rect.H) }))
	a.hiddenCmd("scrollPageDown", a.ed(func(v *EditorView) { v.ScrollBy(v.rect.H) }))
	a.cmd("editor.action.revealLine", "View", "Center Cursor", a.ed(func(v *EditorView) { v.ensurePosVisible(v.Cursor(), true) }))
	a.alias("editor.action.centerCursor", "editor.action.revealLine")
	a.cmd("editor.action.toggleColumnSelection", "Selection", "Toggle Column Selection Mode", func(any) {
		a.notify(NotifyInfo, "Use Shift+Alt+drag or Ctrl+Shift+Alt+Arrow keys for column selection.")
	})
	//#endregion

	//#region View
	a.cmd("workbench.action.showCommands", "", "Show All Commands", func(any) { a.quickAccess(">") })
	a.cmd("workbench.action.quickOpen", "", "Go to File...", func(any) { a.quickAccess("") })
	a.cmd("workbench.action.quickOpenNavigateNextInEditorPicker", "View", "Quick Open Next Recently Used Editor in Group", func(any) { a.editorPicker(1) })
	a.cmd("workbench.action.quickOpenNavigatePreviousInEditorPicker", "View", "Quick Open Previous Recently Used Editor in Group", func(any) { a.editorPicker(-1) })
	a.cmd("workbench.action.showAllEditors", "View", "Show All Editors By Appearance", func(any) { a.showAllEditors() })
	a.cmd("workbench.action.toggleSidebarVisibility", "View", "Toggle Primary Side Bar Visibility", func(any) {
		a.sidebarVisible = !a.sidebarVisible
		if !a.sidebarVisible && a.focus == FocusSidebar {
			a.focusEditor()
		}
	})
	a.cmd("workbench.action.toggleAuxiliaryBar", "View", "Toggle Secondary Side Bar Visibility", func(any) {
		a.notify(NotifyInfo, "The secondary side bar is not available in tuicode.")
	})
	a.cmd("workbench.action.togglePanel", "View", "Toggle Panel Visibility", func(any) {
		if a.panelVisible {
			a.panelVisible = false
			if a.focus == FocusPanel {
				a.focusEditor()
			}
			return
		}
		a.showPanel(a.panelView)
	})
	a.cmd("workbench.action.closePanel", "View", "Hide Panel", func(any) {
		a.panelVisible = false
		if a.focus == FocusPanel {
			a.focusEditor()
		}
	})
	a.cmd("workbench.action.toggleMaximizedPanel", "View", "Maximize Panel Size", func(any) {
		a.panelMaximized = !a.panelMaximized
		if !a.panelVisible {
			a.showPanel(a.panelView)
		}
	})
	a.alias("workbench.action.terminal.toggleMaximizedPanel", "workbench.action.toggleMaximizedPanel")
	a.cmd("workbench.action.toggleZenMode", "View", "Toggle Zen Mode", func(any) { a.zenMode = !a.zenMode; a.focusEditor() })
	a.hiddenCmd("workbench.action.exitZenMode", func(any) { a.zenMode = false })
	a.cmd("workbench.action.toggleFullScreen", "View", "Toggle Full Screen", func(any) { a.zenMode = !a.zenMode })
	a.cmd("editor.action.toggleWordWrap", "View", "Toggle Word Wrap", a.ed(func(v *EditorView) {
		v.wrap = !v.wrap
		v.wrapCache = nil
		v.leftCol = 0
		v.ensureVisible()
	}))
	a.cmd("editor.action.toggleMinimap", "View", "Toggle Minimap", func(any) { a.minimapEnabled = !a.minimapEnabled })
	a.cmd("breadcrumbs.toggle", "View", "Toggle Breadcrumbs", func(any) { a.breadcrumbs = !a.breadcrumbs })
	a.cmd("editor.action.toggleStickyScroll", "View", "Toggle Editor Sticky Scroll", func(any) { a.stickyScroll = !a.stickyScroll })
	a.cmd("editor.action.toggleRenderWhitespace", "View", "Toggle Render Whitespace", func(any) {
		if a.whitespaceRender == "all" {
			a.whitespaceRender = "none"
		} else {
			a.whitespaceRender = "all"
		}
	})
	a.cmd("workbench.action.toggleStatusbarVisibility", "View", "Toggle Status Bar Visibility", func(any) { a.statusVisible = !a.statusVisible })
	a.cmd("workbench.action.toggleActivityBarVisibility", "View", "Toggle Activity Bar Visibility", func(any) { a.activityVisible = !a.activityVisible })
	a.cmd("workbench.action.toggleMenuBar", "View", "Toggle Menu Bar", func(any) { a.titleVisible = !a.titleVisible })
	a.cmd("editor.action.toggleLineNumbers", "View", "Toggle Line Numbers", func(any) {
		if a.lineNumbers == "off" {
			a.lineNumbers = "on"
		} else if a.lineNumbers == "on" {
			a.lineNumbers = "relative"
		} else {
			a.lineNumbers = "off"
		}
	})
	a.cmd("workbench.view.explorer", "View", "Show Explorer", func(any) { a.toggleSidebarView("explorer") })
	a.cmd("workbench.view.search", "View", "Show Search", func(any) { a.showSidebar("search") })
	a.cmd("workbench.view.scm", "View", "Show Source Control", func(any) { a.toggleSidebarView("scm") })
	a.cmd("workbench.view.extensions", "View", "Show Language Servers", func(any) { a.toggleSidebarView("extensions") })
	a.cmd("outline.focus", "View", "Show Outline", func(any) { a.toggleSidebarView("outline") })
	a.cmd("workbench.actions.view.problems", "View", "Toggle Problems", func(any) { a.togglePanelView("problems") })
	a.cmd("workbench.action.output.toggleOutput", "View", "Toggle Output", func(any) { a.togglePanelView("output") })
	a.cmd("workbench.debug.action.toggleRepl", "View", "Toggle Debug Console", func(any) { a.togglePanelView("debug") })
	a.cmd("workbench.action.splitEditor", "View", "Split Editor", func(any) { a.splitEditor() })
	a.alias("workbench.action.splitEditorRight", "workbench.action.splitEditor")
	a.cmd("workbench.action.splitEditorDown", "View", "Split Editor Down", func(any) { a.splitEditorDir(true) })
	a.cmd("workbench.action.splitEditorOrthogonal", "View", "Split Editor in Orthogonal Direction", func(any) { a.splitEditorOrthogonal() })
	a.cmd("workbench.action.toggleEditorGroupLayout", "View", "Toggle Vertical/Horizontal Editor Layout", func(any) { a.toggleEditorGroupLayout() })
	a.cmd("workbench.action.editorLayoutTwoRows", "View", "Two Rows Editor Layout", func(any) {
		a.joinAllGroups()
		a.splitEditorDir(true)
	})
	a.cmd("workbench.action.editorLayoutTwoColumns", "View", "Two Columns Editor Layout", func(any) {
		a.joinAllGroups()
		a.splitEditorDir(false)
	})
	a.cmd("workbench.action.editorLayoutTwoByTwoGrid", "View", "Grid Editor Layout (2x2)", func(any) {
		a.joinAllGroups()
		a.splitEditorDir(false)
		a.activeGroup = 0
		a.splitEditorDir(true)
		a.activeGroup = 2
		a.splitEditorDir(true)
		a.activeGroup = 0
		a.focusEditor()
	})
	a.cmd("workbench.action.focusAboveGroup", "View", "Focus Editor Group Above", func(any) { a.focusGroupDir(0, -1) })
	a.cmd("workbench.action.focusBelowGroup", "View", "Focus Editor Group Below", func(any) { a.focusGroupDir(0, 1) })
	a.cmd("workbench.action.focusFirstEditorGroup", "View", "Focus First Editor Group", func(any) { a.focusGroup(0) })
	a.cmd("workbench.action.focusSecondEditorGroup", "View", "Focus Second Editor Group", func(any) { a.focusGroup(1) })
	a.cmd("workbench.action.focusThirdEditorGroup", "View", "Focus Third Editor Group", func(any) { a.focusGroup(2) })
	a.cmd("workbench.action.focusLeftGroup", "View", "Focus Left Editor Group", func(any) { a.focusGroupDir(-1, 0) })
	a.cmd("workbench.action.focusRightGroup", "View", "Focus Right Editor Group", func(any) { a.focusGroupDir(1, 0) })
	a.cmd("workbench.action.focusActiveEditorGroup", "View", "Focus Active Editor Group", func(any) { a.focusEditor() })
	a.cmd("workbench.action.nextEditor", "View", "Open Next Editor", func(any) { a.cycleEditor(1) })
	a.cmd("workbench.action.previousEditor", "View", "Open Previous Editor", func(any) { a.cycleEditor(-1) })
	a.cmd("workbench.action.moveEditorLeftInGroup", "View", "Move Editor Left", func(any) { a.moveTab(-1) })
	a.cmd("workbench.action.moveEditorRightInGroup", "View", "Move Editor Right", func(any) { a.moveTab(1) })
	a.cmd("workbench.action.moveEditorToNextGroup", "View", "Move Editor into Next Group", func(any) { a.moveEditorToGroup(1) })
	a.cmd("workbench.action.moveEditorToPreviousGroup", "View", "Move Editor into Previous Group", func(any) { a.moveEditorToGroup(-1) })
	a.cmd("workbench.action.moveActiveEditorGroupLeft", "View", "Move Editor Group Left", func(any) { a.moveGroup(-1) })
	a.cmd("workbench.action.moveActiveEditorGroupRight", "View", "Move Editor Group Right", func(any) { a.moveGroup(1) })
	a.cmd("workbench.action.editorLayoutSingle", "View", "Single Column Editor Layout", func(any) { a.joinAllGroups() })
	a.cmd("workbench.action.joinAllGroups", "View", "Join All Editor Groups", func(any) { a.joinAllGroups() })
	for i := 1; i <= 9; i++ {
		idx := i - 1
		a.hiddenCmd(fmt.Sprintf("workbench.action.openEditorAtIndex%d", i), func(any) {
			g := a.ActiveGroup()
			if idx < len(g.tabs) {
				g.setActive(idx)
				a.focusEditor()
			}
		})
	}
	a.hiddenCmd("workbench.action.lastEditorInGroup", func(any) {
		g := a.ActiveGroup()
		if len(g.tabs) > 0 {
			g.setActive(len(g.tabs) - 1)
		}
	})
	a.cmd("workbench.action.zoomIn", "View", "Zoom In", func(any) { a.setStatusMessage("Use your terminal's zoom (e.g. Ctrl+Shift+=) to change the font size.") })
	a.alias("workbench.action.zoomOut", "workbench.action.zoomIn")
	a.cmd("workbench.action.editor.changeLanguageMode", "", "Change Language Mode", func(any) { a.pickLanguage() })
	a.cmd("workbench.action.toggleAutoSave", "File", "Toggle Auto Save", func(any) { a.toggleAutoSave() })
	a.cmd("workbench.action.files.toggleActiveEditorReadonlyInSession", "File", "Toggle Active Editor Read-only in Session", a.ed(func(v *EditorView) { v.buf().ReadOnly = !v.buf().ReadOnly }))
	a.cmd("workbench.action.files.setActiveEditorReadonlyInSession", "File", "Set Active Editor Read-only in Session", a.ed(func(v *EditorView) { v.buf().ReadOnly = true }))
	a.cmd("workbench.action.files.setActiveEditorWriteableInSession", "File", "Set Active Editor Writeable in Session", a.ed(func(v *EditorView) { v.buf().ReadOnly = false }))
	a.cmd("workbench.panel.markers.view.focus", "View", "Focus on Problems View", func(any) { a.showPanel("problems") })
	a.alias("workbench.action.problems.focus", "workbench.panel.markers.view.focus")
	a.cmd("editor.action.wordHighlight.next", "", "Go to Next Symbol Highlight", a.move(func(v *EditorView) { v.jumpHighlight(1) }))
	a.cmd("editor.action.wordHighlight.prev", "", "Go to Previous Symbol Highlight", a.move(func(v *EditorView) { v.jumpHighlight(-1) }))
	a.cmd("workbench.action.editor.changeEOL", "", "Change End of Line Sequence", func(any) { a.pickEOL() })
	a.cmd("workbench.action.editor.changeEncoding", "", "Change File Encoding", func(any) {
		a.openQuick(&QuickPick{Placeholder: "Select Action", Items: []QuickItem{{Label: "Reopen with Encoding", Description: "UTF-8"}, {Label: "Save with Encoding", Description: "UTF-8"}},
			OnAccept: func(*QuickItem, string) { a.setStatusMessage("tuicode only supports UTF-8.") }})
	})
	a.cmd("editor.action.indentationToSpaces.menu", "", "Select Indentation", func(any) { a.pickIndentation() })
	a.cmd("editor.action.indentationToSpaces", "", "Convert Indentation to Spaces", a.edit(func(v *EditorView) { v.convertIndentation(true) }))
	a.cmd("editor.action.indentationToTabs", "", "Convert Indentation to Tabs", a.edit(func(v *EditorView) { v.convertIndentation(false) }))
	a.cmd("editor.action.indentUsingSpaces", "", "Indent Using Spaces", func(any) { a.pickTabSize(true) })
	a.cmd("editor.action.indentUsingTabs", "", "Indent Using Tabs", func(any) { a.pickTabSize(false) })
	a.cmd("editor.action.detectIndentation", "", "Detect Indentation from Content", a.ed(func(v *EditorView) { v.buf().DetectIndentation() }))
	//#endregion

	//#region Folding
	a.cmd("editor.fold", "", "Fold", a.ed(func(v *EditorView) { v.FoldAt(v.Cursor().Line) }))
	a.cmd("editor.unfold", "", "Unfold", a.ed(func(v *EditorView) {
		for _, s := range v.sels {
			v.Unfold(s.Active.Line)
		}
	}))
	a.cmd("editor.toggleFold", "", "Toggle Fold", a.ed(func(v *EditorView) {
		l := v.Cursor().Line
		if _, ok := v.foldAt(l); ok {
			v.Unfold(l)
		} else {
			v.FoldAt(l)
		}
	}))
	a.cmd("editor.foldRecursively", "", "Fold Recursively", a.ed(func(v *EditorView) {
		l := v.Cursor().Line
		if f, ok := v.foldRangeAt(l); ok {
			for line := f.Start; line <= f.End; line++ {
				if ff, ok := v.foldRangeAt(line); ok {
					if _, already := v.foldAt(line); !already {
						v.folds = append(v.folds, ff)
					}
				}
			}
			v.addFold(Fold{-1, -1})
			v.folds = v.folds[:len(v.folds)-1]
		}
	}))
	a.cmd("editor.unfoldRecursively", "", "Unfold Recursively", a.ed(func(v *EditorView) {
		l := v.Cursor().Line
		f, ok := v.foldRangeAt(l)
		if !ok {
			return
		}
		out := v.folds[:0]
		for _, x := range v.folds {
			if x.Start < f.Start || x.Start > f.End {
				out = append(out, x)
			}
		}
		v.folds = out
	}))
	a.cmd("editor.foldAll", "", "Fold All", a.ed(func(v *EditorView) { v.FoldAll(0) }))
	a.cmd("editor.unfoldAll", "", "Unfold All", a.ed(func(v *EditorView) { v.folds = nil }))
	for lvl := 1; lvl <= 7; lvl++ {
		l := lvl
		a.cmd(fmt.Sprintf("editor.foldLevel%d", l), "", fmt.Sprintf("Fold Level %d", l), a.ed(func(v *EditorView) { v.FoldAll(l) }))
	}
	a.cmd("editor.foldAllMarkerRegions", "", "Fold All Regions", a.ed(func(v *EditorView) {
		for line := 0; line < v.buf().LineCount(); line++ {
			if f, ok := v.regionRangeAt(line); ok {
				v.addFold(f)
			}
		}
	}))
	a.cmd("editor.unfoldAllMarkerRegions", "", "Unfold All Regions", a.ed(func(v *EditorView) {
		out := v.folds[:0]
		for _, f := range v.folds {
			if !regionStartRe.MatchString(v.buf().LineString(f.Start)) {
				out = append(out, f)
			}
		}
		v.folds = out
	}))
	a.cmd("editor.foldAllBlockComments", "", "Fold All Block Comments", a.ed(func(v *EditorView) {
		for line := 0; line < v.buf().LineCount(); line++ {
			if strings.HasPrefix(strings.TrimSpace(v.buf().LineString(line)), "/*") {
				if f, ok := v.foldRangeAt(line); ok {
					v.addFold(f)
				}
			}
		}
	}))
	//#endregion

	//#region Language features
	a.cmd("editor.action.triggerSuggest", "", "Trigger Suggest", a.ed(func(v *EditorView) { a.triggerCompletion(v, true, "") }))
	a.cmd("editor.action.triggerParameterHints", "", "Trigger Parameter Hints", a.ed(func(v *EditorView) { a.triggerSignatureHelp(v, "") }))
	a.cmd("editor.action.formatDocument", "", "Format Document", a.ed(func(v *EditorView) { a.formatDocument(v, nil, nil) }))
	a.cmd("editor.action.formatSelection", "", "Format Selection", a.ed(func(v *EditorView) {
		r := v.Primary().Range()
		a.formatDocument(v, &r, nil)
	}))
	a.cmd("editor.action.revealDefinition", "", "Go to Definition", a.ed(func(v *EditorView) { a.pushNav(); a.goToDefinition(v, false) }))
	a.alias("editor.action.goToDeclaration", "editor.action.revealDefinition")
	a.cmd("editor.action.revealDefinitionAside", "", "Open Definition to the Side", a.ed(func(v *EditorView) { a.goToDefinition(v, true) }))
	a.cmd("editor.action.peekDefinition", "", "Peek Definition", a.ed(func(v *EditorView) { a.peekLocations(v, "textDocument/definition", "Definitions") }))
	a.cmd("editor.action.goToTypeDefinition", "", "Go to Type Definition", a.ed(func(v *EditorView) { a.gotoLocations(v, "textDocument/typeDefinition", "type definition") }))
	a.cmd("editor.action.goToImplementation", "", "Go to Implementations", a.ed(func(v *EditorView) { a.gotoLocations(v, "textDocument/implementation", "implementation") }))
	a.cmd("editor.action.goToReferences", "", "Go to References", a.ed(func(v *EditorView) { a.peekReferences(v) }))
	a.alias("editor.action.referenceSearch.trigger", "editor.action.goToReferences")
	a.cmd("references-view.showCallHierarchy", "", "Calls: Show Call Hierarchy", a.ed(func(v *EditorView) { a.showHierarchy(v, "incoming") }))
	a.alias("editor.showCallHierarchy", "references-view.showCallHierarchy")
	a.cmd("references-view.showIncomingCalls", "", "Calls: Show Incoming Calls", a.ed(func(v *EditorView) { a.showHierarchy(v, "incoming") }))
	a.alias("editor.showIncomingCalls", "references-view.showIncomingCalls")
	a.cmd("references-view.showOutgoingCalls", "", "Calls: Show Outgoing Calls", a.ed(func(v *EditorView) { a.showHierarchy(v, "outgoing") }))
	a.alias("editor.showOutgoingCalls", "references-view.showOutgoingCalls")
	a.cmd("references-view.showTypeHierarchy", "", "Types: Show Type Hierarchy", a.ed(func(v *EditorView) { a.showHierarchy(v, "supertypes") }))
	a.alias("editor.showTypeHierarchy", "references-view.showTypeHierarchy")
	a.cmd("references-view.showSupertypes", "", "Types: Show Supertypes", a.ed(func(v *EditorView) { a.showHierarchy(v, "supertypes") }))
	a.cmd("references-view.showSubtypes", "", "Types: Show Subtypes", a.ed(func(v *EditorView) { a.showHierarchy(v, "subtypes") }))
	a.cmd("references-view.findReferences", "", "Find All References", a.ed(func(v *EditorView) { a.findAllReferences(v) }))
	a.cmd("editor.action.rename", "", "Rename Symbol", a.ed(func(v *EditorView) { a.startRename(v) }))
	a.cmd("editor.action.quickFix", "", "Quick Fix...", a.ed(func(v *EditorView) { a.showCodeActions(v, "") }))
	a.cmd("editor.action.refactor", "", "Refactor...", a.ed(func(v *EditorView) { a.showCodeActions(v, "refactor") }))
	a.cmd("editor.action.sourceAction", "", "Source Action...", a.ed(func(v *EditorView) { a.showCodeActions(v, "source") }))
	a.cmd("editor.action.organizeImports", "", "Organize Imports", a.ed(func(v *EditorView) { a.runCodeActionKind(v, "source.organizeImports") }))
	a.cmd("editor.action.autoFix", "", "Auto Fix...", a.ed(func(v *EditorView) { a.runCodeActionKind(v, "quickfix") }))
	a.cmd("editor.action.showHover", "", "Show or Focus Hover", a.ed(func(v *EditorView) {
		if v.hover != nil && !v.hover.mouse {
			v.hover.focused = true
			return
		}
		a.showHover(v, v.Cursor(), false)
	}))
	a.cmd("workbench.action.gotoLine", "", "Go to Line/Column...", func(any) { a.quickAccess(":") })
	a.cmd("workbench.action.gotoSymbol", "", "Go to Symbol in Editor...", func(any) { a.quickAccess("@") })
	a.cmd("workbench.action.showAllSymbols", "", "Go to Symbol in Workspace...", func(any) { a.quickAccess("#") })
	a.cmd("editor.action.marker.nextInFiles", "Go", "Go to Next Problem in Files (Error, Warning, Info)", func(any) { a.gotoProblem(1) })
	a.cmd("editor.action.marker.prevInFiles", "Go", "Go to Previous Problem in Files (Error, Warning, Info)", func(any) { a.gotoProblem(-1) })
	a.cmd("editor.action.marker.next", "Go", "Go to Next Problem (Error, Warning, Info)", func(any) { a.gotoProblem(1) })
	a.cmd("editor.action.marker.prev", "Go", "Go to Previous Problem (Error, Warning, Info)", func(any) { a.gotoProblem(-1) })
	a.cmd("workbench.action.navigateBack", "Go", "Go Back", func(any) { a.navigate(true) })
	a.cmd("workbench.action.navigateForward", "Go", "Go Forward", func(any) { a.navigate(false) })
	a.cmd("workbench.action.navigateToLastEditLocation", "Go", "Go to Last Edit Location", func(any) {
		if n := len(a.lastEdits); n > 0 {
			a.pushNav()
			a.openNavEntry(a.lastEdits[n-1])
		}
	})
	a.cmd("editor.action.restartLanguageServer", "", "Restart Language Servers", func(any) {
		if a.lsp != nil {
			a.lsp.Restart()
			a.setStatusMessage("Restarting language servers...")
		}
	})
	a.cmd("editor.action.showLanguageServerLog", "", "Show Language Server Output", func(any) {
		a.showPanel("output")
		a.output.selectFirstWithPrefix("Language Server")
	})
	a.cmd("breadcrumbs.focusAndSelect", "", "Focus and Select Breadcrumbs", func(any) { a.quickAccess("@") })
	a.cmd("editor.action.toggleTabFocusMode", "", "Toggle Tab Key Moves Focus", func(any) {
		a.setStatusMessage("Tab moves focus is not supported.")
	})
	a.cmd("editor.action.inspectTMScopes", "Developer", "Inspect Editor Tokens and Scopes", a.ed(func(v *EditorView) {
		p := v.Cursor()
		for _, sp := range v.doc.SpansFor(p.Line) {
			if p.Col >= sp.Start && p.Col < sp.End {
				a.notify(NotifyInfo, fmt.Sprintf("Token kind %d (%s), depth %d", sp.Kind, strings.Join(syntax.Scopes[sp.Kind], ", "), sp.Depth))
				return
			}
		}
		a.notify(NotifyInfo, "No token at cursor")
	}))
	//#endregion

	//#region Terminal
	a.cmd("workbench.action.terminal.toggleTerminal", "View", "Toggle Terminal", func(any) {
		if a.panelVisible && a.panelView == "terminal" {
			if a.focus == FocusPanel {
				a.panelVisible = false
				a.focusEditor()
			} else {
				a.setFocus(FocusPanel)
			}
			return
		}
		a.showPanel("terminal")
	})
	a.cmd("workbench.action.terminal.new", "Terminal", "Create New Terminal", func(any) {
		a.showPanel("terminal")
		a.terminals.newTerminal("")
	})
	a.cmd("workbench.action.terminal.split", "Terminal", "Split Terminal", func(any) {
		a.showPanel("terminal")
		a.terminals.split()
	})
	a.cmd("workbench.action.terminal.kill", "Terminal", "Kill the Active Terminal Instance", func(any) { a.terminals.killActive() })
	a.cmd("workbench.action.terminal.killAll", "Terminal", "Kill All Terminals", func(any) { a.terminals.closeAll() })
	a.cmd("workbench.action.terminal.focus", "Terminal", "Focus Terminal", func(any) { a.showPanel("terminal") })
	a.cmd("workbench.action.terminal.focusNext", "Terminal", "Focus Next Terminal in Terminal Group", func(any) { a.terminals.cycle(1) })
	a.cmd("workbench.action.terminal.focusPrevious", "Terminal", "Focus Previous Terminal in Terminal Group", func(any) { a.terminals.cycle(-1) })
	a.cmd("workbench.action.terminal.clear", "Terminal", "Clear", func(any) { a.terminals.clear() })
	a.cmd("workbench.action.terminal.copySelection", "Terminal", "Copy Selection", func(any) { a.terminals.copySelection() })
	a.cmd("workbench.action.terminal.paste", "Terminal", "Paste into Active Terminal", func(any) { a.terminals.paste(a.getClipboard()) })
	a.cmd("workbench.action.terminal.selectAll", "Terminal", "Select All", func(any) { a.terminals.selectAll() })
	a.hiddenCmd("workbench.action.terminal.scrollUp", func(any) { a.terminals.scroll(-1) })
	a.hiddenCmd("workbench.action.terminal.scrollDown", func(any) { a.terminals.scroll(1) })
	a.hiddenCmd("workbench.action.terminal.scrollUpPage", func(any) { a.terminals.scroll(-a.rPanel.H + 3) })
	a.hiddenCmd("workbench.action.terminal.scrollDownPage", func(any) { a.terminals.scroll(a.rPanel.H - 3) })
	a.hiddenCmd("workbench.action.terminal.scrollToTop", func(any) { a.terminals.scroll(-1 << 20) })
	a.hiddenCmd("workbench.action.terminal.scrollToBottom", func(any) { a.terminals.scroll(1 << 20) })
	a.hiddenCmd("workbench.action.terminal.focusFind", func(any) { a.terminals.find() })
	a.cmd("workbench.action.terminal.runSelectedText", "Terminal", "Run Selected Text In Active Terminal", a.ed(func(v *EditorView) {
		text := v.SelectedText()
		if text == "" {
			text = v.buf().LineString(v.Cursor().Line)
		}
		a.terminals.runText(text)
	}))
	a.cmd("workbench.action.terminal.runActiveFile", "Terminal", "Run Active File In Active Terminal", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			a.terminals.runText(shellQuote(v.buf().Path))
		}
	}))
	a.cmd("workbench.action.terminal.rename", "Terminal", "Rename...", func(any) { a.terminals.rename() })
	a.cmd("workbench.action.terminal.selectDefaultShell", "Terminal", "Select Default Profile", func(any) { a.pickShell() })
	a.cmd("workbench.action.tasks.runTask", "Tasks", "Run Task", func(any) { a.runTaskPicker("") })
	a.cmd("workbench.action.tasks.build", "Tasks", "Run Build Task", func(any) { a.runTaskPicker("build") })
	a.cmd("workbench.action.tasks.test", "Tasks", "Run Test Task", func(any) { a.runTaskPicker("test") })
	a.cmd("workbench.action.debug.start", "Debug", "Start Debugging", func(any) { a.runTaskPicker("run") })
	a.cmd("workbench.action.debug.run", "Debug", "Run Without Debugging", func(any) { a.runTaskPicker("run") })
	//#endregion

	//#region Preferences
	a.cmd("workbench.action.openSettings", "Preferences", "Open Settings (UI)", func(any) { a.openSettingsUI() })
	a.cmd("workbench.action.openSettingsJson", "Preferences", "Open User Settings (JSON)", func(any) { a.openUserSettingsJSON() })
	a.cmd("workbench.action.openWorkspaceSettingsFile", "Preferences", "Open Workspace Settings (JSON)", func(any) {
		if a.root != "" {
			p := filepath.Join(a.root, ".vscode", "settings.json")
			if _, err := os.Stat(p); err != nil {
				_ = fsMkdirAll(filepath.Dir(p), 0o755)
				_ = fsWriteFile(p, []byte("{\n}\n"), 0o644)
			}
			a.OpenFile(p, false)
		}
	})
	a.cmd("workbench.action.openGlobalKeybindings", "Preferences", "Open Keyboard Shortcuts", func(any) { a.openKeybindingsPage() })
	a.cmd("workbench.action.openGlobalKeybindingsFile", "Preferences", "Open Keyboard Shortcuts (JSON)", func(any) {
		config.EnsureUserFiles()
		a.OpenFile(config.UserKeybindingsPath(), false)
	})
	a.cmd("workbench.action.selectTheme", "Preferences", "Color Theme", func(any) { a.pickTheme() })
	a.cmd("tuicode.configureKitty", "Preferences", "Let VS Code Shortcuts Pass Through kitty", func(any) { a.installKittyPassthrough() })
	a.cmd("workbench.action.openSnippets", "Snippets", "Configure Snippets", func(any) { a.configureSnippets() })
	a.cmd("editor.action.insertSnippet", "Snippets", "Insert Snippet", a.ed(func(v *EditorView) { a.insertSnippetPicker(v) }))
	a.cmd("workbench.action.selectIconTheme", "Preferences", "File Icon Theme", func(any) { a.pickIconTheme() })
	a.cmd("workbench.action.reloadWindow", "Developer", "Reload Window", func(any) {
		a.loadSettings()
		a.setStatusMessage("Settings reloaded")
	})
	a.cmd("workbench.action.configureLanguageBasedSettings", "Preferences", "Configure Language Specific Settings...", func(any) { a.openUserSettingsJSON() })
	//#endregion

	//#region Help
	a.cmd("workbench.action.showWelcomePage", "Help", "Welcome", func(any) { a.openWelcome() })
	a.alias("welcome.showAllWalkthroughs", "workbench.action.showWelcomePage")
	a.cmd("workbench.action.keybindingsReference", "Help", "Keyboard Shortcuts Reference", func(any) { a.openKeybindingsPage() })
	a.cmd("workbench.action.showAboutDialog", "Help", "About", func(any) {
		a.showDialog("tuicode "+Version, "A Visual Studio Code inspired editor for the terminal.\nTheme: "+a.theme.Name+"\nWorkspace: "+a.root, []string{"OK", "Copy"}, func(c int) {
			if c == 1 {
				a.setClipboard("tuicode " + Version)
			}
		})
	})
	a.cmd("update.checkForUpdate", "Help", "Check for Updates...", func(any) { a.setStatusMessage("tuicode " + Version + " is up to date.") })
	a.cmd("notifications.toggleList", "Notifications", "Toggle Notifications", func(any) { a.toggleNotificationCenter() })
	a.cmd("notifications.clearAll", "Notifications", "Clear All Notifications", func(any) {
		a.notifications = nil
		a.notifHistory = nil
		a.unreadNotifications = 0
	})
	a.cmd("workbench.action.toggleDevTools", "Developer", "Show Running Language Servers", func(any) { a.toggleSidebarView("extensions") })
	//#endregion

	//#region Git
	a.registerGitCommands()
	//#endregion

	//#region Explorer
	a.cmd("explorer.newFile", "File", "New File...", func(any) { a.explorer.newEntry(false) })
	a.cmd("explorer.newFolder", "File", "New Folder...", func(any) { a.explorer.newEntry(true) })
	a.cmd("workbench.files.action.refreshFilesExplorer", "File", "Refresh Explorer", func(any) { a.explorer.refresh() })
	a.cmd("workbench.files.action.collapseExplorerFolders", "File", "Collapse Folders in Explorer", func(any) { a.explorer.collapseAll() })
	a.cmd("workbench.files.action.compareFileWith", "File", "Compare Active File With...", a.ed(func(v *EditorView) {
		items := a.fileItems()
		a.openQuick(&QuickPick{Placeholder: "Select a file to compare with", Items: items, MatchDescription: true, OnAccept: func(it *QuickItem, q string) {
			if it == nil {
				return
			}
			other := it.Data.(string)
			data, err := os.ReadFile(other)
			if err != nil {
				a.notify(NotifyError, err.Error())
				return
			}
			a.openDiffPage(v.doc.Title()+" ↔ "+filepath.Base(other), v.buf().Path, v.buf().Text(), string(data))
		}})
	}))
	a.cmd("workbench.action.files.rename", "File", "Rename...", a.ed(func(v *EditorView) {
		old := v.buf().Path
		if old == "" {
			a.saveActive(true)
			return
		}
		a.promptInput("Rename", "New file name", filepath.Base(old), func(name string) {
			if name == "" || name == filepath.Base(old) {
				return
			}
			target := filepath.Join(filepath.Dir(old), name)
			if fileExists(target) {
				a.notify(NotifyError, "A file named '"+name+"' already exists.")
				return
			}
			if err := fsRename(old, target); err != nil {
				a.notify(NotifyError, "Rename failed: "+err.Error())
				return
			}
			a.onFileRenamed(old, target)
			a.explorer.refreshSoon()
		})
	}))
	a.cmd("workbench.files.action.compareWithSaved", "File", "Compare Active File with Saved", a.ed(func(v *EditorView) { a.compareWithSaved(v) }))
	a.cmd("workbench.files.action.focusOpenEditorsView", "File", "Focus on Open Editors View", func(any) { a.showAllEditors() })
	a.cmd("workbench.action.compareEditor.nextChange", "Compare", "Go to Next Change", a.ed(func(v *EditorView) { a.gotoChange(v, 1) }))
	a.cmd("workbench.action.compareEditor.previousChange", "Compare", "Go to Previous Change", a.ed(func(v *EditorView) { a.gotoChange(v, -1) }))
	a.cmd("workbench.action.editor.nextChange", "", "Go to Next Change", a.ed(func(v *EditorView) { a.gotoChange(v, 1) }))
	a.cmd("workbench.action.editor.previousChange", "", "Go to Previous Change", a.ed(func(v *EditorView) { a.gotoChange(v, -1) }))
	//#endregion

	a.registerDebugCommands()

	// markdown preview renders the document as formatted text
	a.cmd("markdown.showPreview", "Markdown", "Open Preview", a.ed(func(v *EditorView) { a.openMarkdownPreview(v, false) }))
	a.cmd("markdown.showPreviewToSide", "Markdown", "Open Preview to the Side", a.ed(func(v *EditorView) { a.openMarkdownPreview(v, true) }))
}

func (a *App) toggleSidebarView(view string) {
	if a.sidebarVisible && a.sidebarView == view && a.focus == FocusSidebar {
		a.sidebarVisible = false
		a.focusEditor()
		return
	}
	a.showSidebar(view)
}

func (a *App) togglePanelView(view string) {
	if a.panelVisible && a.panelView == view {
		a.panelVisible = false
		if a.focus == FocusPanel {
			a.focusEditor()
		}
		return
	}
	a.showPanel(view)
}

func (a *App) requestQuit() {
	var dirty []*Document
	for _, d := range a.docs {
		if d.Buf.Dirty() && !d.Virtual {
			dirty = append(dirty, d)
		}
	}
	if len(dirty) == 0 {
		a.quit = true
		return
	}
	// hot exit: keep unsaved changes in a backup and restore them next time
	if mode := a.settings.String("files.hotExit", ""); mode == "onExit" || mode == "onExitAndWindowClose" {
		if err := a.writeBackups(dirty); err == nil {
			a.quit = true
			return
		}
	}
	msg := fmt.Sprintf("Do you want to save the changes to %d file(s)?", len(dirty))
	if len(dirty) == 1 {
		msg = "Do you want to save the changes you made to " + dirty[0].Title() + "?"
	}
	a.showDialog(msg, "Your changes will be lost if you don't save them.", []string{"Save All", "Don't Save", "Cancel"}, func(c int) {
		switch c {
		case 0:
			for _, d := range dirty {
				if !a.saveDocument(d, false) {
					return
				}
			}
			a.quit = true
		case 1:
			a.quit = true
		}
	})
}

// escapeEditor implements Escape in the editor: close widgets, drop extra cursors, clear selection.
func (a *App) escapeEditor(v *EditorView) {
	if v.completion != nil && v.completion.visible() || v.hover != nil || v.sig != nil || v.codeActions != nil {
		v.closeWidgets(false)
		return
	}
	if v.snippet != nil {
		v.snippet = nil
		return
	}
	if v.find != nil && v.find.visible {
		a.closeFind()
		return
	}
	if len(v.sels) > 1 {
		p := v.Primary()
		v.SetSelections([]buffer.Selection{p}, 0)
		return
	}
	if v.HasSelection() {
		v.SetCursor(v.Cursor())
		return
	}
	if a.panelVisible && a.settings.Bool("tuicode.escapeClosesPanel", "") {
		a.panelVisible = false
	}
}

func (a *App) pickTheme() {
	infos := theme.Available(config.ThemesDir())
	current := a.theme.Name
	var items []QuickItem
	sel := 0
	for _, groupDef := range []struct {
		label string
		light bool
	}{{"light themes", true}, {"dark themes", false}} {
		first := true
		for _, inf := range infos {
			isLight := inf.UIType == "vs" || inf.UIType == "hc-light"
			if isLight != groupDef.light {
				continue
			}
			it := QuickItem{Label: inf.Label, Data: inf}
			if first {
				it.Group = groupDef.label
				first = false
			}
			if inf.Origin != "Built-in" {
				it.Description = inf.Origin
			}
			if inf.Label == current {
				sel = len(items)
			}
			items = append(items, it)
		}
	}
	original := a.theme
	q := a.openQuick(&QuickPick{
		Placeholder:      "Select Color Theme (Up/Down Keys to Preview)",
		Items:            items,
		KeepOrder:        true,
		MatchDescription: true,
		OnSelect: func(it *QuickItem) {
			if it == nil {
				return
			}
			if th, err := theme.LoadInfo(it.Data.(theme.Info)); err == nil {
				a.theme = th
			}
		},
		OnAccept: func(it *QuickItem, q string) {
			if it == nil {
				a.theme = original
				return
			}
			inf := it.Data.(theme.Info)
			if th, err := theme.LoadInfo(inf); err == nil {
				a.theme = th
				if err := config.SetUser("workbench.colorTheme", inf.Label); err != nil {
					a.notify(NotifyError, "Failed to save the theme setting: "+err.Error())
				}
				a.settings = config.LoadSettings(a.root)
			}
		},
		OnCancel: func() { a.theme = original },
	})
	q.selected = sel
}

func (a *App) pickIconTheme() {
	items := []QuickItem{
		{Label: "Seti (Nerd Font)", Description: "requires a Nerd Font in your terminal", Data: "nerd"},
		{Label: "Minimal (Unicode)", Description: "works with any font", Data: "unicode"},
		{Label: "Auto Detect", Description: "use Nerd Font icons when one is installed", Data: "auto"},
	}
	a.openQuick(&QuickPick{Placeholder: "Select File Icon Theme", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		val := it.Data.(string)
		_ = config.SetUser("tuicode.icons", val)
		a.settings = config.LoadSettings(a.root)
		switch val {
		case "nerd":
			a.nerdIcons = true
		case "unicode":
			a.nerdIcons = false
		default:
			a.nerdIcons = detectNerdFont()
		}
	}})
}

func (a *App) pickLanguage() {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		return
	}
	var items []QuickItem
	items = append(items, QuickItem{Label: "Auto Detect", Data: ""})
	for _, l := range syntax.SortedLanguages() {
		desc := "(" + l.ID + ")"
		if l.ID == v.doc.Lang.ID {
			desc += " - Configured Language"
		}
		items = append(items, QuickItem{Label: l.Name, Description: desc, Data: l.ID})
	}
	a.openQuick(&QuickPick{Placeholder: "Select Language Mode", Items: items, KeepOrder: false, MatchDescription: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		id := it.Data.(string)
		var lang *syntax.Language
		if id == "" {
			first := ""
			if v.buf().LineCount() > 0 {
				first = v.buf().LineString(0)
			}
			lang = syntax.Detect(v.buf().Path, first)
		} else {
			lang = syntax.ByID(id)
		}
		a.SetLanguage(v.doc, lang)
	}})
}

func (a *App) pickEOL() {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		return
	}
	a.openQuick(&QuickPick{Placeholder: "Select End of Line Sequence", Items: []QuickItem{{Label: "LF", Data: "\n"}, {Label: "CRLF", Data: "\r\n"}}, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			v.buf().EOL = it.Data.(string)
			// mark dirty by recording a no-op edit group
			v.buf().ApplyEdits([]buffer.Edit{{Range: buffer.Range{}, Text: ""}}, v.sels, "eol")
		}
	}})
}

func (a *App) pickIndentation() {
	items := []QuickItem{
		{Label: "Indent Using Spaces", Data: "editor.action.indentUsingSpaces"},
		{Label: "Indent Using Tabs", Data: "editor.action.indentUsingTabs"},
		{Label: "Detect Indentation from Content", Data: "editor.action.detectIndentation"},
		{Label: "Convert Indentation to Spaces", Data: "editor.action.indentationToSpaces"},
		{Label: "Convert Indentation to Tabs", Data: "editor.action.indentationToTabs"},
		{Label: "Trim Trailing Whitespace", Data: "editor.action.trimTrailingWhitespace"},
	}
	a.openQuick(&QuickPick{Placeholder: "Select Action", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			a.RunCommand(it.Data.(string), nil)
		}
	}})
}

func (a *App) pickTabSize(spaces bool) {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		return
	}
	var items []QuickItem
	sel := 0
	for i := 1; i <= 8; i++ {
		it := QuickItem{Label: fmt.Sprint(i), Data: i}
		if i == v.buf().TabSize {
			it.Description = "Configured Tab Size"
			sel = i - 1
		}
		items = append(items, it)
	}
	q := a.openQuick(&QuickPick{Placeholder: "Select Tab Size for the Current File", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			v.buf().TabSize = it.Data.(int)
			v.buf().InsertSpaces = spaces
			v.wrapCache = nil
		}
	}})
	q.selected = sel
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"$`\\!&;|<>()*?[]#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
