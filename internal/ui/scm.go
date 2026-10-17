package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/git"
)

type scmRow struct {
	header string // "staged" / "changes" / "merge"
	file   *git.FileStatus
	staged bool
}

// SCMView is the Source Control side bar view.
type SCMView struct {
	app       *App
	message   TextInput
	focusMsg  bool
	rows      []scmRow
	selected  int
	scroll    int
	collapsed map[string]bool
	busy      string
	rect      Rect
}

func newSCMView(a *App) *SCMView {
	s := &SCMView{app: a, collapsed: map[string]bool{}}
	s.message = newTextInput(a, "Message (Ctrl+Enter to commit)")
	s.focusMsg = true
	return s
}

func (s *SCMView) focusedInput() *TextInput {
	if s.focusMsg {
		return &s.message
	}
	return nil
}

func (s *SCMView) refresh() {
	a := s.app
	s.rows = s.rows[:0]
	var staged, changes, merge []git.FileStatus
	for _, f := range a.gitStatus {
		switch {
		case f.Conflicted():
			merge = append(merge, f)
		default:
			if f.Staged() {
				staged = append(staged, f)
			}
			if f.Unstaged() {
				changes = append(changes, f)
			}
		}
	}
	add := func(name string, list []git.FileStatus, isStaged bool) {
		if len(list) == 0 {
			return
		}
		s.rows = append(s.rows, scmRow{header: name})
		if s.collapsed[name] {
			return
		}
		for i := range list {
			f := list[i]
			s.rows = append(s.rows, scmRow{file: &f, staged: isStaged})
		}
	}
	add("Merge Changes", merge, false)
	add("Staged Changes", staged, true)
	add("Changes", changes, false)
	if s.selected >= len(s.rows) {
		s.selected = max(len(s.rows)-1, 0)
	}
}

func (s *SCMView) current() *scmRow {
	if s.selected >= 0 && s.selected < len(s.rows) {
		return &s.rows[s.selected]
	}
	return nil
}

func (s *SCMView) handleKey(key, text string, isText bool) bool {
	a := s.app
	if s.focusMsg {
		switch key {
		case "ctrl+enter":
			a.gitCommit(false)
			return true
		case "down", "tab":
			if len(s.rows) > 0 {
				s.focusMsg = false
			}
			return true
		case "escape":
			a.focusEditor()
			return true
		case "enter":
			// single-line input: Enter commits like Ctrl+Enter when a message exists
			if strings.TrimSpace(s.message.Text()) != "" {
				a.gitCommit(false)
			}
			return true
		}
		return false
	}
	r := s.current()
	switch key {
	case "up":
		if s.selected == 0 {
			s.focusMsg = true
		} else {
			s.selected--
		}
	case "down":
		if s.selected < len(s.rows)-1 {
			s.selected++
		}
	case "enter":
		if r != nil {
			if r.file != nil {
				a.openGitDiff(filepath.Join(a.gitRoot, r.file.Path), r.staged)
			} else {
				s.collapsed[r.header] = !s.collapsed[r.header]
				s.refresh()
			}
		}
	case "space", "+":
		if r != nil && r.file != nil {
			if r.staged {
				a.gitRun(func() error { return git.Unstage(a.gitRoot, r.file.Path) })
			} else {
				a.gitRun(func() error { return git.Stage(a.gitRoot, r.file.Path) })
			}
		}
	case "-":
		if r != nil && r.file != nil && r.staged {
			a.gitRun(func() error { return git.Unstage(a.gitRoot, r.file.Path) })
		}
	case "delete":
		if r != nil && r.file != nil && !r.staged {
			s.discard([]git.FileStatus{*r.file})
		}
	case "ctrl+enter":
		a.gitCommit(false)
	case "escape":
		a.focusEditor()
	case "tab", "shift+tab":
		s.focusMsg = true
	default:
		return false
	}
	s.ensureVisible()
	return true
}

func (s *SCMView) ensureVisible() {
	h := s.rect.H - 3
	if h <= 0 {
		return
	}
	if s.selected < s.scroll {
		s.scroll = s.selected
	}
	if s.selected >= s.scroll+h {
		s.scroll = s.selected - h + 1
	}
}

func (s *SCMView) discard(files []git.FileStatus) {
	a := s.app
	msg := fmt.Sprintf("Are you sure you want to discard changes in %d files?", len(files))
	if len(files) == 1 {
		msg = "Are you sure you want to discard changes in '" + filepath.Base(files[0].Path) + "'?"
		if files[0].Untracked() {
			msg = "Are you sure you want to DELETE '" + filepath.Base(files[0].Path) + "'?"
		}
	}
	a.showDialog(msg, "This is IRREVERSIBLE!\nYour current working set will be FOREVER LOST if you proceed.", []string{"Discard Changes", "Cancel"}, func(c int) {
		if c != 0 {
			return
		}
		a.gitRun(func() error { return git.Discard(a.gitRoot, files) })
		a.Post(func() {
			for _, f := range files {
				if d, ok := a.docs[filepath.Join(a.gitRoot, f.Path)]; ok && !d.Buf.Dirty() {
					_ = d.Buf.Reload()
				}
			}
		})
	})
}

// gitRun runs a git operation in the background and refreshes afterwards.
func (a *App) gitRun(fn func() error) {
	if a.gitRoot == "" {
		return
	}
	go func() {
		err := fn()
		a.Post(func() {
			if err != nil {
				a.notify(NotifyError, "Git: "+err.Error())
				a.output.append("Git", err.Error())
			}
			a.gitBusy = false
			a.refreshGit()
		})
	}()
}

func (a *App) gitCommit(amend bool) {
	s := a.scm
	if a.gitRoot == "" {
		a.notify(NotifyInfo, "There is no Git repository in the current folder.", NotifyAction{Label: "Initialize Repository", Run: func() { a.RunCommand("git.init", nil) }})
		return
	}
	msg := strings.TrimSpace(s.message.Text())
	if msg == "" && !amend {
		a.promptInput("Commit", "Commit message", "", func(m string) {
			if m != "" {
				s.message.SetText(m)
				a.gitCommit(amend)
			}
		})
		return
	}
	hasStaged := false
	for _, f := range a.gitStatus {
		if f.Staged() {
			hasStaged = true
		}
	}
	doCommit := func(all bool) {
		root := a.gitRoot
		a.scm.busy = "Committing..."
		go func() {
			var err error
			if all {
				err = git.Stage(root, ".")
			}
			if err == nil {
				err = git.Commit(root, msg, false, amend)
			}
			a.Post(func() {
				a.scm.busy = ""
				if err != nil {
					a.notify(NotifyError, "Git: "+err.Error())
					return
				}
				s.message.SetText("")
				a.setStatusMessage("Committed: " + msg)
				a.refreshGit()
			})
		}()
	}
	if !hasStaged && !amend {
		if len(a.gitStatus) == 0 {
			a.notify(NotifyInfo, "There are no changes to commit.")
			return
		}
		if a.settings.Bool("git.enableSmartCommit", "") {
			doCommit(true)
			return
		}
		a.showDialog("There are no staged changes to commit.", "Would you like to stage all your changes and commit them directly?", []string{"Yes", "Always", "Cancel"}, func(c int) {
			switch c {
			case 0:
				doCommit(true)
			case 1:
				_ = setUserSetting(a, "git.enableSmartCommit", true)
				doCommit(true)
			}
		})
		return
	}
	doCommit(false)
}

func (a *App) gitRemoteOp(label string, args ...string) {
	if a.gitRoot == "" {
		return
	}
	root := a.gitRoot
	a.scm.busy = label + "..."
	a.setStatusMessage(label + "...")
	go func() {
		out, err := git.Run(root, args...)
		a.Post(func() {
			a.scm.busy = ""
			a.output.append("Git", "> git "+strings.Join(args, " "))
			if strings.TrimSpace(out) != "" {
				a.output.append("Git", out)
			}
			if err != nil {
				a.output.append("Git", err.Error())
				a.notify(NotifyError, "Git: "+firstLine(err.Error()), NotifyAction{Label: "Show Command Output", Run: func() {
					a.showPanel("output")
					a.output.selectChannel("Git")
				}})
			} else {
				a.setStatusMessage(label + " finished")
			}
			a.refreshGit()
		})
	}()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (a *App) registerGitCommands() {
	a.cmd("git.commit", "Git", "Commit", func(any) { a.gitCommit(false) })
	a.cmd("git.commitAmend", "Git", "Commit (Amend)", func(any) { a.gitCommit(true) })
	a.cmd("git.commitAll", "Git", "Commit All", func(any) {
		a.gitRun(func() error { return git.Stage(a.gitRoot, ".") })
		a.gitCommit(false)
	})
	a.cmd("git.stageAll", "Git", "Stage All Changes", func(any) { a.gitRun(func() error { return git.Stage(a.gitRoot, ".") }) })
	a.cmd("git.unstageAll", "Git", "Unstage All Changes", func(any) { a.gitRun(func() error { return git.Unstage(a.gitRoot, ".") }) })
	a.cmd("git.stage", "Git", "Stage Changes", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			rel, _ := filepath.Rel(a.gitRoot, v.buf().Path)
			a.gitRun(func() error { return git.Stage(a.gitRoot, rel) })
		}
	}))
	a.cmd("git.unstage", "Git", "Unstage Changes", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			rel, _ := filepath.Rel(a.gitRoot, v.buf().Path)
			a.gitRun(func() error { return git.Unstage(a.gitRoot, rel) })
		}
	}))
	a.cmd("git.cleanAll", "Git", "Discard All Changes", func(any) {
		var files []git.FileStatus
		for _, f := range a.gitStatus {
			if f.Unstaged() {
				files = append(files, f)
			}
		}
		if len(files) > 0 {
			a.scm.discard(files)
		}
	})
	a.cmd("git.clean", "Git", "Discard Changes", a.ed(func(v *EditorView) {
		if st, ok := a.gitByPath[v.buf().Path]; ok {
			a.scm.discard([]git.FileStatus{st})
		}
	}))
	a.cmd("git.push", "Git", "Push", func(any) { a.gitRemoteOp("Pushing", "push") })
	a.cmd("git.pull", "Git", "Pull", func(any) { a.gitRemoteOp("Pulling", "pull", "--no-rebase") })
	a.cmd("git.pullRebase", "Git", "Pull (Rebase)", func(any) { a.gitRemoteOp("Pulling", "pull", "--rebase") })
	a.cmd("git.fetch", "Git", "Fetch", func(any) { a.gitRemoteOp("Fetching", "fetch") })
	a.cmd("git.sync", "Git", "Sync", func(any) {
		if !a.gitHasUp {
			a.showDialog("The branch '"+a.gitBranch+"' has no remote branch. Would you like to publish this branch?", "", []string{"OK", "Cancel"}, func(c int) {
				if c == 0 {
					a.gitRemoteOp("Publishing", "push", "-u", "origin", a.gitBranch)
				}
			})
			return
		}
		run := func() {
			root := a.gitRoot
			a.scm.busy = "Syncing..."
			go func() {
				_, err := git.Run(root, "pull", "--no-rebase")
				if err == nil {
					_, err = git.Run(root, "push")
				}
				a.Post(func() {
					a.scm.busy = ""
					if err != nil {
						a.notify(NotifyError, "Git: "+firstLine(err.Error()))
					}
					a.refreshGit()
				})
			}()
		}
		if a.settings.Bool("git.confirmSync", "") {
			a.showDialog(fmt.Sprintf("This action will pull and push commits from and to 'origin/%s'.", a.gitBranch), "", []string{"OK", "OK, Don't Show Again", "Cancel"}, func(c int) {
				if c == 1 {
					_ = setUserSetting(a, "git.confirmSync", false)
				}
				if c <= 1 {
					run()
				}
			})
			return
		}
		run()
	})
	a.cmd("git.checkout", "Git", "Checkout to...", func(any) { a.gitCheckoutPicker() })
	a.cmd("git.branch", "Git", "Create Branch...", func(any) {
		a.promptInput("Branch name", "Please provide a new branch name", "", func(name string) {
			if name != "" {
				a.gitRemoteOp("Creating branch", "checkout", "-b", name)
			}
		})
	})
	a.cmd("git.stash", "Git", "Stash", func(any) { a.gitRemoteOp("Stashing", "stash", "push", "-u") })
	a.cmd("git.stashPop", "Git", "Pop Latest Stash", func(any) { a.gitRemoteOp("Popping stash", "stash", "pop") })
	a.cmd("git.undoCommit", "Git", "Undo Last Commit", func(any) { a.gitRemoteOp("Undoing commit", "reset", "--soft", "HEAD~1") })
	a.cmd("git.init", "Git", "Initialize Repository", func(any) {
		if a.root == "" {
			return
		}
		root := a.root
		go func() {
			_, err := git.Run(root, "init")
			a.Post(func() {
				if err != nil {
					a.notify(NotifyError, err.Error())
				}
				a.refreshGit()
			})
		}()
	})
	a.cmd("git.refresh", "Git", "Refresh", func(any) { a.refreshGit() })
	a.cmd("git.openChange", "Git", "Open Changes", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			a.openGitDiff(v.buf().Path, false)
		}
	}))
	a.cmd("git.openFile", "Git", "Open File", func(any) {
		if v := a.ActiveTab(); v != nil {
			if dp, ok := v.special.(*DiffPage); ok && dp.path != "" {
				a.OpenFile(dp.path, false)
			}
		}
	})
	a.cmd("git.viewHistory", "Git", "View File History", a.ed(func(v *EditorView) {
		if v.buf().Path != "" {
			a.showFileHistory(v.buf().Path)
		}
	}))
	a.cmd("git.blameLine", "Git", "Show Blame for Current Line", a.ed(func(v *EditorView) {
		if v.buf().Path == "" || a.gitRoot == "" {
			return
		}
		rel, _ := filepath.Rel(a.gitRoot, v.buf().Path)
		line := v.Cursor().Line + 1
		root := a.gitRoot
		go func() {
			b := git.BlameLine(root, rel, line)
			a.Post(func() {
				if b == "" {
					b = "No blame information"
				}
				a.notify(NotifyInfo, fmt.Sprintf("Line %d: %s", line, b))
			})
		}()
	}))
}

func (a *App) gitCheckoutPicker() {
	if a.gitRoot == "" {
		return
	}
	branches, err := git.Branches(a.gitRoot)
	if err != nil {
		a.notify(NotifyError, err.Error())
		return
	}
	items := []QuickItem{
		{Label: a.Icon(IcoAdd) + " Create new branch...", Data: "+new", AlwaysShow: true},
	}
	for _, b := range branches {
		desc := ""
		if b == a.gitBranch {
			desc = "current"
		}
		icon := a.Icon(IcoBranch)
		if strings.Contains(b, "/") {
			desc = "Remote branch"
		}
		items = append(items, QuickItem{Label: b, Icon: icon, Description: desc, Data: b})
	}
	a.openQuick(&QuickPick{Placeholder: "Select a branch or tag to checkout", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		b := it.Data.(string)
		if b == "+new" {
			a.RunCommand("git.branch", nil)
			return
		}
		if strings.HasPrefix(b, "origin/") {
			a.gitRemoteOp("Checking out", "checkout", "--track", b)
			return
		}
		a.gitRemoteOp("Checking out", "checkout", b)
	}})
}

func (a *App) showFileHistory(path string) {
	if a.gitRoot == "" {
		return
	}
	rel, _ := filepath.Rel(a.gitRoot, path)
	out, err := git.Run(a.gitRoot, "log", "--format=%h%x09%an%x09%ar%x09%s", "-n", "100", "--", rel)
	if err != nil {
		a.notify(NotifyError, err.Error())
		return
	}
	var items []QuickItem
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(l, "\t", 4)
		if len(f) < 4 {
			continue
		}
		items = append(items, QuickItem{Label: f[3], Description: f[1] + ", " + f[2], Detail: f[0], Data: f[0], Icon: a.Icon(IcoSCM)})
	}
	a.openQuick(&QuickPick{Title: "Timeline: " + filepath.Base(path), Placeholder: "Select a commit to compare with the working tree", Items: items, KeepOrder: true, MatchDescription: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		rev := it.Data.(string)
		base, err := git.Show(a.gitRoot, rev, rel)
		if err != nil {
			a.notify(NotifyError, err.Error())
			return
		}
		cur := a.currentText(path)
		a.openDiffPage(filepath.Base(path)+" ("+rev+") ↔ "+filepath.Base(path), path, base, cur)
	}})
}

// currentText returns the document text (if open) or the file on disk.
func (a *App) currentText(path string) string {
	if d, ok := a.docs[path]; ok {
		return d.Buf.Text()
	}
	data, _ := os.ReadFile(path)
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// openGitDiff opens a diff of a file against HEAD (or the index for staged changes).
func (a *App) openGitDiff(path string, staged bool) {
	if a.gitRoot == "" {
		a.OpenFile(path, false)
		return
	}
	rel, _ := filepath.Rel(a.gitRoot, path)
	st := a.gitByPath[path]
	var left, right, title string
	name := filepath.Base(path)
	if staged {
		left, _ = git.Show(a.gitRoot, "HEAD", rel)
		right, _ = git.Show(a.gitRoot, "", rel)
		title = name + " (Index)"
	} else {
		if st.X != ' ' && st.X != '?' && st.X != 0 {
			left, _ = git.Show(a.gitRoot, "", rel)
		} else {
			left, _ = git.Show(a.gitRoot, "HEAD", rel)
		}
		right = a.currentText(path)
		title = name + " (Working Tree)"
	}
	if st.Untracked() || st.X == 'A' && !staged {
		title = name + " (Untracked)"
	}
	if st.Y == 'D' || st.X == 'D' {
		title = name + " (Deleted)"
	}
	a.openDiffPage(title, path, left, right)
}

func (a *App) compareWithSaved(v *EditorView) {
	if v.buf().Path == "" {
		return
	}
	data, err := os.ReadFile(v.buf().Path)
	if err != nil {
		a.notify(NotifyError, err.Error())
		return
	}
	a.openDiffPage(v.doc.Title()+" (on disk) ↔ "+v.doc.Title(), v.buf().Path, strings.ReplaceAll(string(data), "\r\n", "\n"), v.buf().Text())
}

func (a *App) openDiffFiles(left, right string) {
	l, err1 := os.ReadFile(left)
	r, err2 := os.ReadFile(right)
	if err1 != nil || err2 != nil {
		a.notify(NotifyError, "Unable to read files for diff.")
		return
	}
	a.openDiffPage(filepath.Base(left)+" ↔ "+filepath.Base(right), right, string(l), string(r))
}

// gotoChange moves to the next/previous git change in the editor.
func (a *App) gotoChange(v *EditorView, dir int) {
	if dp, ok := v.special.(*DiffPage); ok {
		dp.nextChange(dir)
		return
	}
	if len(v.doc.gitChanges) == 0 {
		return
	}
	cur := v.Cursor().Line
	best := -1
	b := v.buf()
	for l := range v.doc.gitChanges {
		// only the first line of each block counts
		if _, prev := v.doc.gitChanges[l-1]; prev {
			continue
		}
		if dir > 0 && l > cur && (best < 0 || l < best) {
			best = l
		}
		if dir < 0 && l < cur && (best < 0 || l > best) {
			best = l
		}
	}
	if best < 0 {
		// wrap around
		for l := range v.doc.gitChanges {
			if dir > 0 && (best < 0 || l < best) || dir < 0 && (best < 0 || l > best) {
				best = l
			}
		}
	}
	if best >= 0 && best < b.LineCount() {
		a.pushNav()
		v.SetCursor(buffer0(best))
		v.ensurePosVisible(v.Cursor(), true)
	}
}

func (s *SCMView) drawActions(r Rect) {
	a := s.app
	th := a.theme
	acts := []struct {
		icon int
		cmd  string
		tip  string
	}{
		{IcoCheck, "git.commit", "Commit (Ctrl+Enter)"},
		{IcoRefresh, "git.refresh", "Refresh"},
		{IcoEllipsis, "", "More Actions..."},
	}
	x := r.X + r.W - len(acts)*2
	for _, ac := range acts {
		st := styleFB(th.C("sideBarTitle.foreground"), th.C("sideBar.background"))
		a.cv.Text(x, r.Y, x+1, a.Icon(ac.icon), st)
		cmd := ac.cmd
		tip := ac.tip
		bx := x
		a.addHit(Rect{x, r.Y, 1, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				if cmd == "" {
					a.openContextMenu(bx-20, r.Y+1, []MenuItem{
						{Label: "Pull", Command: "git.pull"},
						{Label: "Push", Command: "git.push"},
						{Label: "Fetch", Command: "git.fetch"},
						{Label: "Sync", Command: "git.sync"},
						{Separator: true},
						{Label: "Checkout to...", Command: "git.checkout"},
						{Label: "Create Branch...", Command: "git.branch"},
						{Separator: true},
						{Label: "Commit (Amend)", Command: "git.commitAmend"},
						{Label: "Undo Last Commit", Command: "git.undoCommit"},
						{Separator: true},
						{Label: "Stash", Command: "git.stash"},
						{Label: "Pop Latest Stash", Command: "git.stashPop"},
						{Separator: true},
						{Label: "Stage All Changes", Command: "git.stageAll"},
						{Label: "Unstage All Changes", Command: "git.unstageAll"},
						{Label: "Discard All Changes", Command: "git.cleanAll"},
					})
				} else {
					a.RunCommand(cmd, nil)
				}
			case MouseMove:
				a.showTooltipAt(ev.X, r.Y+1, tip)
			}
			return true
		})
		x += 2
	}
}

func (s *SCMView) draw(r Rect) {
	a := s.app
	th := a.theme
	s.rect = r
	bgc := th.C("sideBar.background")
	fg := th.C("sideBar.foreground")
	focused := a.focus == FocusSidebar && a.sidebarView == "scm"
	if a.gitRoot == "" {
		st := styleFB(fg, bgc)
		lines := wrapText("The folder currently open doesn't have a git repository. You can initialize a repository which will enable source control features powered by git.", r.W-4)
		for i, l := range lines {
			a.cv.Text(r.X+2, r.Y+1+i, r.X+r.W, l, st)
		}
		by := r.Y + 2 + len(lines)
		bst := styleFB(th.C("button.foreground"), th.C("button.background"))
		bw := min(r.W-4, 30)
		a.cv.Fill(Rect{r.X + 2, by, bw, 1}, ' ', bst)
		a.cv.TextCenter(r.X+2, by, bw, "Initialize Repository", bst)
		a.addHit(Rect{r.X + 2, by, bw, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.RunCommand("git.init", nil)
			}
			return true
		})
		return
	}
	y := r.Y
	ibg := th.C("input.background")
	ist := styleFB(th.C("input.foreground"), ibg)
	s.message.Placeholder = fmt.Sprintf("Message (Ctrl+Enter to commit on '%s')", a.gitBranch)
	a.cv.Fill(Rect{r.X + 1, y, r.W - 2, 1}, ' ', ist)
	s.message.Draw(r.X+2, y, r.W-4, ist, styleFB(th.C("input.placeholderForeground"), ibg), focused && s.focusMsg)
	if focused && s.focusMsg {
		a.cv.Set(r.X+1, y, '▏', styleFB(th.C("focusBorder"), ibg))
	}
	a.addHit(Rect{r.X + 1, y, r.W - 2, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			a.setFocus(FocusSidebar)
			s.focusMsg = true
		}
		return false
	})
	y++
	// commit button
	bst := styleFB(th.C("button.foreground"), th.C("button.background"))
	label := a.Icon(IcoCheck) + " Commit"
	if s.busy != "" {
		label = s.busy
	} else if len(a.gitStatus) == 0 && a.gitHasUp && a.gitAhead > 0 {
		label = a.Icon(IcoSync) + fmt.Sprintf(" Sync Changes %d↑", a.gitAhead)
	}
	a.cv.Fill(Rect{r.X + 1, y, r.W - 2, 1}, ' ', bst)
	a.cv.TextCenter(r.X+1, y, r.W-2, label, bst)
	a.addHit(Rect{r.X + 1, y, r.W - 2, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			if len(a.gitStatus) == 0 && a.gitAhead > 0 {
				a.RunCommand("git.sync", nil)
			} else {
				a.gitCommit(false)
			}
		}
		return true
	})
	y += 2
	list := Rect{r.X, y, r.W, r.Y + r.H - y}
	h := list.H
	s.scroll = clampInt(s.scroll, 0, max(len(s.rows)-h, 0))
	for i := 0; i < h; i++ {
		idx := s.scroll + i
		if idx >= len(s.rows) {
			break
		}
		row := s.rows[idx]
		ry := list.Y + i
		sel := idx == s.selected && !s.focusMsg
		hover := a.mouseY == ry && list.Contains(a.mouseX, a.mouseY)
		rbg := bgc
		rfg := fg
		if sel && focused {
			rbg = th.C("list.activeSelectionBackground")
			rfg = th.C("list.activeSelectionForeground")
		} else if sel {
			rbg = th.C("list.inactiveSelectionBackground")
		} else if hover {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		st := styleFB(rfg, rbg)
		a.cv.Fill(Rect{list.X, ry, list.W, 1}, ' ', st)
		rowIdx := idx
		if row.file == nil {
			chev := a.Icon(IcoChevronDown)
			if s.collapsed[row.header] {
				chev = a.Icon(IcoChevronRight)
			}
			x := a.cv.Text(list.X+1, ry, list.X+list.W, chev+" ", st.Bold(true))
			a.cv.Text(x, ry, list.X+list.W, row.header, st.Bold(true))
			count := 0
			for _, f := range a.gitStatus {
				switch row.header {
				case "Staged Changes":
					if f.Staged() && !f.Conflicted() {
						count++
					}
				case "Changes":
					if f.Unstaged() && !f.Conflicted() {
						count++
					}
				default:
					if f.Conflicted() {
						count++
					}
				}
			}
			cnt := fmt.Sprint(count)
			bst := styleFB(th.C("badge.foreground"), th.C("badge.background"))
			a.cv.Text(list.X+list.W-len(cnt)-3, ry, list.X+list.W, " "+cnt+" ", bst)
			if hover || sel {
				// section actions
				ax := list.X + list.W - len(cnt) - 9
				header := row.header
				if header == "Changes" {
					a.cv.Text(ax, ry, ax+1, a.Icon(IcoDiscard), st)
					a.addHit(Rect{ax, ry, 1, 1}, func(ev MouseEvent) bool {
						if ev.Kind == MousePress {
							a.RunCommand("git.cleanAll", nil)
						}
						return true
					})
					a.cv.Text(ax+2, ry, ax+3, a.Icon(IcoAdd), st)
					a.addHit(Rect{ax + 2, ry, 1, 1}, func(ev MouseEvent) bool {
						if ev.Kind == MousePress {
							a.RunCommand("git.stageAll", nil)
						}
						return true
					})
				} else if header == "Staged Changes" {
					a.cv.Text(ax+2, ry, ax+3, a.Icon(IcoRemove), st)
					a.addHit(Rect{ax + 2, ry, 1, 1}, func(ev MouseEvent) bool {
						if ev.Kind == MousePress {
							a.RunCommand("git.unstageAll", nil)
						}
						return true
					})
				}
			}
			a.addHit(Rect{list.X, ry, list.W, 1}, func(ev MouseEvent) bool {
				if ev.Kind == MousePress {
					a.setFocus(FocusSidebar)
					s.focusMsg = false
					s.selected = rowIdx
					s.collapsed[row.header] = !s.collapsed[row.header]
					s.refresh()
				}
				return true
			})
			continue
		}
		f := *row.file
		name := filepath.Base(f.Path)
		dir := filepath.Dir(f.Path)
		x := list.X + 3
		icon, col := a.FileIcon(name, false, false)
		if icon != "" {
			x = a.cv.Text(x, ry, list.X+list.W, icon+" ", styleFB(col, rbg))
		}
		key := gitColorKey(f)
		letter := gitLetter(f)
		if row.staged {
			letter = git.Letter(f.X)
		}
		nst := st
		if !(sel && focused) {
			nst = st.Foreground(th.C(key))
		}
		if f.Y == 'D' || f.X == 'D' {
			nst = nst.StrikeThrough(true)
		}
		x = a.cv.Text(x, ry, list.X+list.W-4, name, nst)
		if dir != "." {
			a.cv.TextEllipsis(x+1, ry, list.X+list.W-x-6, dir, st.Foreground(th.OverColor("descriptionForeground", rbg)))
		}
		lst := st
		if !(sel && focused) {
			lst = st.Foreground(th.C(key))
		}
		a.cv.Text(list.X+list.W-2, ry, list.X+list.W, letter, lst)
		if hover || sel {
			ax := list.X + list.W - 9
			acts := []struct {
				icon int
				run  func()
			}{
				{IcoFile, func() { a.OpenFile(filepath.Join(a.gitRoot, f.Path), false) }},
			}
			if row.staged {
				acts = append(acts, struct {
					icon int
					run  func()
				}{IcoRemove, func() { a.gitRun(func() error { return git.Unstage(a.gitRoot, f.Path) }) }})
			} else {
				acts = append(acts,
					struct {
						icon int
						run  func()
					}{IcoDiscard, func() { s.discard([]git.FileStatus{f}) }},
					struct {
						icon int
						run  func()
					}{IcoAdd, func() { a.gitRun(func() error { return git.Stage(a.gitRoot, f.Path) }) }},
				)
			}
			ax = list.X + list.W - 3 - len(acts)*2
			for _, act := range acts {
				a.cv.Fill(Rect{ax - 1, ry, 2, 1}, ' ', st)
				a.cv.Text(ax, ry, ax+1, a.Icon(act.icon), st)
				run := act.run
				a.addHit(Rect{ax, ry, 1, 1}, func(ev MouseEvent) bool {
					if ev.Kind == MousePress {
						run()
					}
					return true
				})
				ax += 2
			}
		}
		staged := row.staged
		a.addHit(Rect{list.X, ry, list.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.setFocus(FocusSidebar)
				s.focusMsg = false
				s.selected = rowIdx
				if ev.Button == tcell.Button2 {
					items := []MenuItem{
						{Label: "Open Changes", Run: func() { a.openGitDiff(filepath.Join(a.gitRoot, f.Path), staged) }},
						{Label: "Open File", Run: func() { a.OpenFile(filepath.Join(a.gitRoot, f.Path), false) }},
						{Separator: true},
					}
					if staged {
						items = append(items, MenuItem{Label: "Unstage Changes", Run: func() { a.gitRun(func() error { return git.Unstage(a.gitRoot, f.Path) }) }})
					} else {
						items = append(items,
							MenuItem{Label: "Stage Changes", Run: func() { a.gitRun(func() error { return git.Stage(a.gitRoot, f.Path) }) }},
							MenuItem{Label: "Discard Changes", Run: func() { s.discard([]git.FileStatus{f}) }},
						)
					}
					items = append(items, MenuItem{Separator: true},
						MenuItem{Label: "Add to .gitignore", Run: func() {
							gi := filepath.Join(a.gitRoot, ".gitignore")
							fh, err := os.OpenFile(gi, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
							if err == nil {
								_, _ = fh.WriteString("\n" + f.Path + "\n")
								fh.Close()
							}
							a.refreshGit()
						}},
						MenuItem{Label: "Reveal in Explorer View", Run: func() {
							a.showSidebar("explorer")
							a.explorer.reveal(filepath.Join(a.gitRoot, f.Path), true)
						}},
					)
					a.openContextMenu(ev.X, ev.Y, items)
					return true
				}
				a.openGitDiff(filepath.Join(a.gitRoot, f.Path), staged)
				if ev.Clicks >= 2 {
					a.OpenFile(filepath.Join(a.gitRoot, f.Path), false)
					a.focusEditor()
				}
			case MouseWheel:
				s.scroll = clampInt(s.scroll+ev.WheelDY*3, 0, max(len(s.rows)-h, 0))
			}
			return true
		})
	}
	a.addHit(list, func(ev MouseEvent) bool {
		if ev.Kind == MouseWheel {
			s.scroll = clampInt(s.scroll+ev.WheelDY*3, 0, max(len(s.rows)-h, 0))
		}
		return true
	})
}
