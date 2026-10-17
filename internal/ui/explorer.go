package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/lsp"
)

type exNode struct {
	path     string
	name     string
	isDir    bool
	children []*exNode
	loaded   bool
	modTime  time.Time
	virtual  bool // the invisible parent of a multi-root workspace's folders
	isRoot   bool // a workspace root folder
}

type exRow struct {
	node  *exNode
	depth int
	label string // may be a compacted "a/b/c"
	input bool
	root  bool // a multi-root workspace folder
}

// Explorer is the file tree view.
type Explorer struct {
	app           *App
	root          *exNode
	expanded      map[string]bool
	rows          []exRow
	selected      int
	scroll        int
	rect          Rect
	editing       *exEdit
	clipboard     []string
	cutMode       bool
	lastCheck     time.Time
	typeBuf       string
	typeTime      time.Time
	collapsedRoot bool
	outlineOpen   bool
	dirty         bool
	multi         map[string]bool // extra selected paths (Ctrl/Shift+click)
	anchor        int             // row where a Shift selection starts
}

type exEdit struct {
	input  TextInput
	parent string
	isDir  bool
	rename string // path being renamed
}

func newExplorer(a *App) *Explorer {
	e := &Explorer{app: a, expanded: map[string]bool{}}
	e.setRoot(a.root)
	return e
}

func (e *Explorer) setRoot(root string) {
	if root == "" {
		e.root = nil
		e.rows = nil
		return
	}
	folders := e.app.folders()
	if len(folders) > 1 {
		// multi-root: each folder is a top-level node
		e.root = &exNode{name: e.app.workspace.Name() + " (Workspace)", isDir: true, virtual: true, loaded: true}
		for _, f := range folders {
			e.root.children = append(e.root.children, &exNode{path: f.Path, name: f.Name, isDir: true, isRoot: true})
		}
		e.expanded[folders[0].Path] = true
		e.refresh()
		return
	}
	name := filepath.Base(root)
	if len(folders) == 1 && e.app.workspace != nil {
		root, name = folders[0].Path, folders[0].Name
	}
	e.root = &exNode{path: root, name: name, isDir: true}
	e.expanded[root] = true
	e.refresh()
}

// folderOf returns the root folder that contains path (the most specific one in a multi-root workspace).
func (e *Explorer) folderOf(path string) string {
	if e.root == nil {
		return ""
	}
	if !e.root.virtual {
		if path == e.root.path || strings.HasPrefix(path, e.root.path+string(filepath.Separator)) {
			return e.root.path
		}
		return ""
	}
	best := ""
	for _, f := range e.root.children {
		if (path == f.path || strings.HasPrefix(path, f.path+string(filepath.Separator))) && len(f.path) > len(best) {
			best = f.path
		}
	}
	return best
}

func (e *Explorer) load(n *exNode) {
	if n.virtual {
		return
	}
	entries, err := os.ReadDir(n.path)
	n.loaded = true
	if st, err := os.Stat(n.path); err == nil {
		n.modTime = st.ModTime()
	}
	if err != nil {
		n.children = nil
		return
	}
	old := map[string]*exNode{}
	for _, c := range n.children {
		old[c.name] = c
	}
	var kids []*exNode
	match := e.app.excludeMatcher()
	for _, ent := range entries {
		name := ent.Name()
		full := filepath.Join(n.path, name)
		isDir := ent.IsDir()
		if !isDir && ent.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil && st.IsDir() {
				isDir = true
			}
		}
		rel := name
		if base := e.folderOf(full); base != "" {
			if r, err := filepath.Rel(base, full); err == nil {
				rel = r
			}
		}
		if match(rel, isDir) {
			continue
		}
		if o, ok := old[name]; ok && o.isDir == isDir {
			kids = append(kids, o)
			continue
		}
		kids = append(kids, &exNode{path: full, name: name, isDir: isDir})
	}
	sort.Slice(kids, func(i, j int) bool {
		if kids[i].isDir != kids[j].isDir {
			return kids[i].isDir
		}
		return naturalLess(strings.ToLower(kids[i].name), strings.ToLower(kids[j].name))
	})
	n.children = kids
}

// naturalLess compares strings with numbers in natural order.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ca, cb := a[0], b[0]
		if isDigit(ca) && isDigit(cb) {
			i, j := 0, 0
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na := strings.TrimLeft(a[:i], "0")
			nb := strings.TrimLeft(b[:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[i:], b[j:]
			continue
		}
		if ca != cb {
			return ca < cb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// refresh reloads expanded directories and rebuilds rows.
func (e *Explorer) refresh() {
	if e.root == nil {
		return
	}
	var walk func(n *exNode)
	walk = func(n *exNode) {
		if !n.isDir {
			return
		}
		if e.expanded[n.path] || n == e.root {
			e.load(n)
			for _, c := range n.children {
				walk(c)
			}
		}
	}
	walk(e.root)
	e.lastCheck = time.Now()
	e.rebuild()
}

func (e *Explorer) refreshSoon() {
	e.dirty = true
	e.app.Post(func() {
		if e.dirty {
			e.dirty = false
			e.refresh()
		}
	})
}

// refreshIfChanged polls modification times of expanded folders.
func (e *Explorer) refreshIfChanged() {
	if e.root == nil {
		return
	}
	changed := false
	var walk func(n *exNode)
	walk = func(n *exNode) {
		if changed || !n.isDir || !(e.expanded[n.path] || n == e.root) {
			return
		}
		if n.virtual {
			for _, c := range n.children {
				walk(c)
			}
			return
		}
		if st, err := os.Stat(n.path); err == nil && !st.ModTime().Equal(n.modTime) {
			changed = true
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(e.root)
	if changed {
		e.refresh()
	}
}

func (e *Explorer) rebuild() {
	var sel string
	if e.selected >= 0 && e.selected < len(e.rows) && e.rows[e.selected].node != nil {
		sel = e.rows[e.selected].node.path
	}
	e.rows = e.rows[:0]
	if e.root == nil || e.collapsedRoot {
		return
	}
	compact := e.app.settings.Bool("explorer.compactFolders", "")
	var add func(n *exNode, depth int)
	add = func(n *exNode, depth int) {
		for _, c := range n.children {
			label := c.name
			node := c
			if c.isDir && compact && !c.isRoot {
				// fold single-folder chains into one row
				for e.expanded[node.path] {
					if !node.loaded {
						e.load(node)
					}
					if len(node.children) != 1 || !node.children[0].isDir {
						break
					}
					node = node.children[0]
					label += "/" + node.name
				}
			}
			e.rows = append(e.rows, exRow{node: node, depth: depth, label: label, root: node.isRoot})
			if e.editing != nil && e.editing.rename == "" && e.editing.parent == node.path {
				e.rows = append(e.rows, exRow{depth: depth + 1, input: true})
			}
			if node.isDir && e.expanded[node.path] {
				if !node.loaded {
					e.load(node)
				}
				add(node, depth+1)
			}
		}
	}
	if e.editing != nil && e.editing.rename == "" && e.editing.parent == e.root.path && !e.root.virtual {
		e.rows = append(e.rows, exRow{depth: 0, input: true})
	}
	add(e.root, 0)
	if sel != "" {
		for i, r := range e.rows {
			if r.node != nil && r.node.path == sel {
				e.selected = i
				break
			}
		}
	}
	if e.selected >= len(e.rows) {
		e.selected = len(e.rows) - 1
	}
	if e.selected < 0 && len(e.rows) > 0 {
		e.selected = 0
	}
}

func (e *Explorer) expandedList() []string {
	var out []string
	for p, v := range e.expanded {
		if v && (e.root == nil || p != e.root.path) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (e *Explorer) collapseAll() {
	e.expanded = map[string]bool{}
	if e.root != nil {
		e.expanded[e.root.path] = true
	}
	e.rebuild()
}

// reveal expands parents of path and selects it.
func (e *Explorer) reveal(path string, focus bool) {
	base := e.folderOf(path)
	if base == "" || base == path {
		return
	}
	rel, _ := filepath.Rel(base, path)
	parts := strings.Split(rel, string(filepath.Separator))
	cur := base
	changed := false
	if !e.expanded[base] {
		e.expanded[base] = true
		changed = true
	}
	for _, p := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, p)
		if !e.expanded[cur] {
			e.expanded[cur] = true
			changed = true
		}
	}
	if changed {
		e.refresh()
	}
	for i, r := range e.rows {
		if r.node != nil && r.node.path == path {
			e.selected = i
			e.ensureVisible()
			break
		}
	}
	if focus {
		e.app.setFocus(FocusSidebar)
	}
}

func (e *Explorer) ensureVisible() {
	h := e.rect.H - 1
	if h <= 0 {
		return
	}
	if e.selected < e.scroll {
		e.scroll = e.selected
	}
	if e.selected >= e.scroll+h {
		e.scroll = e.selected - h + 1
	}
}

func (e *Explorer) current() *exNode {
	if e.selected >= 0 && e.selected < len(e.rows) {
		return e.rows[e.selected].node
	}
	return nil
}

func (e *Explorer) toggle(n *exNode) {
	if !n.isDir {
		return
	}
	e.expanded[n.path] = !e.expanded[n.path]
	if e.expanded[n.path] {
		e.load(n)
	}
	e.rebuild()
}

func (e *Explorer) open(n *exNode, preview bool, focusEditor bool) {
	if n == nil {
		return
	}
	if n.isDir {
		e.toggle(n)
		return
	}
	e.app.OpenFile(n.path, preview)
	if focusEditor {
		e.app.focusEditor()
	}
}

func (e *Explorer) focusedInput() *TextInput {
	if e.editing != nil {
		return &e.editing.input
	}
	return nil
}

func (e *Explorer) handleKey(key, text string, isText bool) bool {
	a := e.app
	if e.editing != nil {
		switch key {
		case "enter":
			e.commitEdit()
			return true
		case "escape":
			e.editing = nil
			e.rebuild()
			return true
		}
		return false
	}
	if len(e.rows) == 0 {
		if key == "escape" {
			a.focusEditor()
			return true
		}
		return false
	}
	if e.selected < 0 || e.selected >= len(e.rows) {
		e.selected = 0
	}
	n := e.current()
	switch key {
	case "shift+up", "shift+down":
		if len(e.multi) == 0 {
			e.anchor = e.selected
		}
		if key == "shift+up" {
			e.selectRange(max(e.selected-1, 0))
		} else {
			e.selectRange(min(e.selected+1, len(e.rows)-1))
		}
		e.ensureVisible()
		return true
	case "up", "down", "home", "end", "pageup", "pagedown", "left", "right", "escape":
		e.multi = nil
	}
	switch key {
	case "up":
		if e.selected > 0 {
			e.selected--
		}
	case "down":
		if e.selected < len(e.rows)-1 {
			e.selected++
		}
	case "pageup":
		e.selected = max(e.selected-(e.rect.H-2), 0)
	case "pagedown":
		e.selected = max(min(e.selected+(e.rect.H-2), len(e.rows)-1), 0)
	case "home":
		e.selected = 0
	case "end":
		e.selected = len(e.rows) - 1
	case "left":
		if n != nil && n.isDir && e.expanded[n.path] {
			e.toggle(n)
		} else {
			// select the parent
			d := e.rows[e.selected].depth
			for i := e.selected - 1; i >= 0; i-- {
				if e.rows[i].depth < d {
					e.selected = i
					break
				}
			}
		}
	case "right":
		if n != nil && n.isDir {
			if !e.expanded[n.path] {
				e.toggle(n)
			} else if e.selected+1 < len(e.rows) {
				e.selected++
			}
		}
	case "enter":
		e.open(n, false, true)
	case "space":
		e.open(n, true, false)
	case "f2":
		e.startRename()
	case "delete", "ctrl+backspace":
		e.deleteSelected(false)
	case "shift+delete":
		e.deleteSelected(true)
	case "ctrl+c", "ctrl+x":
		e.clipboard = nil
		for _, sn := range e.selectedNodes() {
			e.clipboard = append(e.clipboard, sn.path)
		}
		e.cutMode = key == "ctrl+x"
	case "ctrl+v":
		e.paste()
	case "escape":
		a.focusEditor()
	case "ctrl+alt+c":
		if n != nil {
			a.setClipboard(n.path)
		}
	default:
		if isText && text != " " {
			// type to navigate
			if time.Since(e.typeTime) > time.Second {
				e.typeBuf = ""
			}
			e.typeTime = time.Now()
			e.typeBuf += strings.ToLower(text)
			for i := 0; i < len(e.rows); i++ {
				idx := (e.selected + i) % len(e.rows)
				if len(e.typeBuf) == 1 && i == 0 {
					continue
				}
				r := e.rows[idx]
				if r.node != nil && strings.HasPrefix(strings.ToLower(r.label), e.typeBuf) {
					e.selected = idx
					break
				}
			}
			e.ensureVisible()
			return true
		}
		return false
	}
	e.ensureVisible()
	return true
}

// targetDir returns the folder that new entries go into.
func (e *Explorer) targetDir() string {
	if e.root == nil {
		return ""
	}
	n := e.current()
	if n == nil {
		if e.root.virtual {
			return e.root.children[0].path
		}
		return e.root.path
	}
	if n.isDir {
		return n.path
	}
	return filepath.Dir(n.path)
}

func (e *Explorer) newEntry(isDir bool) {
	a := e.app
	if e.root == nil {
		a.notify(NotifyInfo, "Open a folder first to create files in the explorer.")
		return
	}
	a.showSidebar("explorer")
	dir := e.targetDir()
	if dir != e.root.path {
		e.expanded[dir] = true
		e.refresh()
	}
	ed := &exEdit{parent: dir, isDir: isDir}
	ed.input = newTextInput(a, "")
	e.editing = ed
	e.rebuild()
	for i, r := range e.rows {
		if r.input {
			e.selected = i
		}
	}
	e.ensureVisible()
}

func (e *Explorer) startRename() {
	n := e.current()
	if n == nil {
		return
	}
	ed := &exEdit{rename: n.path, parent: filepath.Dir(n.path), isDir: n.isDir}
	ed.input = newTextInput(e.app, "")
	ed.input.SetText(n.name)
	// select the name without extension like VS Code
	if !n.isDir {
		ext := filepath.Ext(n.name)
		ed.input.anchor = 0
		ed.input.cursor = len([]rune(n.name)) - len([]rune(ext))
		if ed.input.cursor == 0 {
			ed.input.cursor = len([]rune(n.name))
		}
	}
	e.editing = ed
}

func (e *Explorer) commitEdit() {
	a := e.app
	ed := e.editing
	e.editing = nil
	name := strings.TrimSpace(ed.input.Text())
	if name == "" {
		e.rebuild()
		return
	}
	target := filepath.Join(ed.parent, name)
	if ed.rename != "" {
		if target == ed.rename {
			e.rebuild()
			return
		}
		if _, err := os.Stat(target); err == nil {
			a.notify(NotifyError, "A file or folder **"+name+"** already exists at this location. Please choose a different name.")
			e.rebuild()
			return
		}
		a.movePath(ed.rename, target, func(err error) {
			if err != nil {
				a.notify(NotifyError, "Unable to rename: "+err.Error())
			}
			e.refresh()
			e.reveal(target, false)
		})
		e.rebuild()
		return
	}
	if _, err := os.Stat(target); err == nil {
		a.notify(NotifyError, "A file or folder **"+name+"** already exists at this location. Please choose a different name.")
		e.rebuild()
		return
	}
	if ed.isDir || strings.HasSuffix(name, "/") {
		if err := fsMkdirAll(target, 0o755); err != nil {
			a.notify(NotifyError, err.Error())
		}
		e.expanded[target] = true
	} else {
		if err := fsMkdirAll(filepath.Dir(target), 0o755); err != nil {
			a.notify(NotifyError, err.Error())
		}
		if err := fsWriteFile(target, nil, 0o644); err != nil {
			a.notify(NotifyError, err.Error())
		} else {
			a.OpenFile(target, false)
			a.focusEditor()
		}
	}
	e.refresh()
	e.reveal(target, false)
}

// onFileRenamed updates open documents after a rename on disk.
func (a *App) onFileRenamed(from, to string) {
	for p, d := range a.docs {
		if p == from || strings.HasPrefix(p, from+string(filepath.Separator)) {
			np := to + strings.TrimPrefix(p, from)
			if a.lsp != nil {
				a.lsp.Close(d.Buf)
			}
			delete(a.docs, p)
			d.Buf.Path = np
			a.docs[np] = d
			if a.lsp != nil {
				a.lsp.Open(d.Buf)
			}
		}
	}
	a.refreshGit()
}

func (e *Explorer) deleteSelected(permanent bool) {
	a := e.app
	nodes := e.selectedNodes()
	for i := len(nodes) - 1; i >= 0; i-- {
		if nodes[i].isRoot || nodes[i] == e.root {
			nodes = append(nodes[:i], nodes[i+1:]...)
		}
	}
	if len(nodes) == 0 {
		return
	}
	n := nodes[0]
	trash := !permanent && hasTrash()
	what := "file"
	if n.isDir {
		what = "folder"
	}
	name := "'" + n.name + "'"
	detail := "You can restore this " + what + " from the Trash."
	if len(nodes) > 1 {
		name = fmt.Sprintf("the following %d files/folders", len(nodes))
		var names []string
		for _, x := range nodes {
			names = append(names, x.name)
		}
		detail = strings.Join(names, "\n") + "\n\nYou can restore these files from the Trash."
	}
	msg := "Are you sure you want to delete " + name + "?"
	btn := "Move to Trash"
	if !trash {
		msg = "Are you sure you want to permanently delete " + name + "?"
		detail = "This action is irreversible!"
		btn = "Delete"
	}
	doDelete := func() {
		for _, n := range nodes {
			var err error
			if trash {
				err = moveToTrash(n.path)
			} else {
				err = fsRemoveAll(n.path)
			}
			if err != nil {
				a.notify(NotifyError, "Failed to delete '"+n.name+"': "+err.Error())
			}
			// close editors of deleted files
			for p, d := range a.docs {
				if p == n.path || strings.HasPrefix(p, n.path+string(filepath.Separator)) {
					if !d.Buf.Dirty() {
						for _, g := range a.groups {
							for i := len(g.tabs) - 1; i >= 0; i-- {
								if g.tabs[i].special == nil && g.tabs[i].doc == d {
									a.closeTabNow(g, i)
								}
							}
						}
					}
				}
			}
		}
		e.multi = nil
		e.refresh()
		a.refreshGit()
	}
	if !a.settings.Bool("explorer.confirmDelete", "") {
		doDelete()
		return
	}
	a.showDialog(msg, detail, []string{btn, "Cancel"}, func(c int) {
		if c == 0 {
			doDelete()
		}
	})
}

func hasTrash() bool {
	for _, t := range []string{"gio", "trash-put", "kioclient5", "kioclient"} {
		if _, err := exec.LookPath(t); err == nil {
			return true
		}
	}
	return false
}

func moveToTrash(path string) error {
	if err := checkSandbox(path); err != nil {
		return err
	}
	if p, err := exec.LookPath("gio"); err == nil {
		return exec.Command(p, "trash", path).Run()
	}
	if p, err := exec.LookPath("trash-put"); err == nil {
		return exec.Command(p, path).Run()
	}
	for _, k := range []string{"kioclient5", "kioclient"} {
		if p, err := exec.LookPath(k); err == nil {
			return exec.Command(p, "move", path, "trash:/").Run()
		}
	}
	return fsRemoveAll(path)
}

func (e *Explorer) paste() {
	a := e.app
	dir := e.targetDir()
	for _, src := range e.clipboard {
		name := filepath.Base(src)
		dst := filepath.Join(dir, name)
		if dst == src && e.cutMode {
			continue
		}
		// avoid overwriting: "name copy.ext"
		for i := 1; ; i++ {
			if _, err := os.Stat(dst); err != nil {
				break
			}
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			suffix := " copy"
			if i > 1 {
				suffix = " copy " + itoaSimple(i)
			}
			dst = filepath.Join(dir, base+suffix+ext)
		}
		var err error
		if e.cutMode {
			a.movePath(src, dst, func(err error) {
				if err != nil {
					a.notify(NotifyError, "Failed to paste: "+err.Error())
				}
				e.refresh()
			})
		} else {
			err = copyPath(src, dst)
		}
		if err != nil {
			a.notify(NotifyError, "Failed to paste: "+err.Error())
		}
	}
	if e.cutMode {
		e.clipboard = nil
	}
	e.refresh()
}

func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func copyPath(src, dst string) error {
	if err := checkSandbox(dst); err != nil {
		return err
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := fsOpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	}
	if err := fsMkdirAll(dst, st.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, en := range entries {
		if err := copyPath(filepath.Join(src, en.Name()), filepath.Join(dst, en.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (e *Explorer) drawActions(r Rect) {
	a := e.app
	th := a.theme
	if !(r.Contains(a.mouseX, a.mouseY) || a.rSidebar.Contains(a.mouseX, a.mouseY) || a.focus == FocusSidebar) {
		a.cv.Text(r.X+r.W-2, r.Y, r.X+r.W, a.Icon(IcoEllipsis), styleFB(th.C("sideBarTitle.foreground"), th.C("sideBar.background")))
		return
	}
	acts := []struct {
		icon int
		cmd  string
		tip  string
	}{
		{IcoNewFile, "explorer.newFile", "New File..."},
		{IcoNewFolder, "explorer.newFolder", "New Folder..."},
		{IcoRefresh, "workbench.files.action.refreshFilesExplorer", "Refresh Explorer"},
		{IcoCollapseAll, "workbench.files.action.collapseExplorerFolders", "Collapse Folders in Explorer"},
	}
	x := r.X + r.W - len(acts)*2
	for _, ac := range acts {
		st := styleFB(th.C("sideBarTitle.foreground"), th.C("sideBar.background"))
		if a.mouseY == r.Y && a.mouseX == x {
			st = st.Background(th.OverColor("toolbar.hoverBackground", th.C("sideBar.background")))
		}
		a.cv.Text(x, r.Y, x+1, a.Icon(ac.icon), st)
		cmd := ac.cmd
		tip := ac.tip
		a.addHit(Rect{x, r.Y, 1, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.RunCommand(cmd, nil)
			case MouseMove:
				a.showTooltipAt(ev.X, r.Y+1, tip)
			}
			return true
		})
		x += 2
	}
}

func (e *Explorer) draw(r Rect) {
	mark := len(e.app.hits)
	a := e.app
	th := a.theme
	e.rect = r
	bgc := th.C("sideBar.background")
	focused := a.focus == FocusSidebar && a.sidebarView == "explorer"
	if e.root == nil {
		st := styleFB(th.C("sideBar.foreground"), bgc)
		a.cv.Text(r.X+2, r.Y+1, r.X+r.W, "You have not yet opened a folder.", st)
		bst := styleFB(th.C("button.foreground"), th.C("button.background"))
		label := "Open Folder"
		bw := min(r.W-4, 30)
		a.cv.Fill(Rect{r.X + 2, r.Y + 3, bw, 1}, ' ', bst)
		a.cv.TextCenter(r.X+2, r.Y+3, bw, label, bst)
		a.addHit(Rect{r.X + 2, r.Y + 3, bw, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.RunCommand("workbench.action.files.openFolder", nil)
			}
			return true
		})
		return
	}
	// section header
	hst := styleFB(th.C("sideBarSectionHeader.foreground"), th.OverColor("sideBarSectionHeader.background", bgc)).Bold(true)
	a.cv.Fill(Rect{r.X, r.Y, r.W, 1}, ' ', hst)
	chev := a.Icon(IcoChevronDown)
	if e.collapsedRoot {
		chev = a.Icon(IcoChevronRight)
	}
	a.cv.Text(r.X, r.Y, r.X+2, chev, hst)
	a.cv.TextEllipsis(r.X+2, r.Y, r.W-3, strings.ToUpper(e.root.name), hst)
	a.addHit(Rect{r.X, r.Y, r.W, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			e.collapsedRoot = !e.collapsedRoot
			e.rebuild()
		}
		return true
	})
	// outline section at the bottom
	outlineH := 1
	if e.outlineOpen {
		outlineH = max(r.H/3, 4)
	}
	body := Rect{r.X, r.Y + 1, r.W, r.H - 1 - outlineH}
	e.rect = Rect{body.X, body.Y - 1, body.W, body.H + 1}
	h := body.H
	if e.selected >= 0 {
		e.ensureVisible()
	}
	e.scroll = clampInt(e.scroll, 0, max(len(e.rows)-h, 0))
	indentW := clampInt(a.settings.Int("workbench.tree.indent", "", 8)/4, 1, 4)
	if indentW < 2 {
		indentW = 2
	}
	for i := 0; i < h; i++ {
		idx := e.scroll + i
		y := body.Y + i
		if idx >= len(e.rows) {
			break
		}
		row := e.rows[idx]
		sel := idx == e.selected || row.node != nil && e.multi[row.node.path]
		rbg := bgc
		rfg := th.C("sideBar.foreground")
		if sel && focused {
			rbg = th.C("list.activeSelectionBackground")
			rfg = th.C("list.activeSelectionForeground")
		} else if sel {
			rbg = th.C("list.inactiveSelectionBackground")
		} else if a.mouseY == y && body.Contains(a.mouseX, a.mouseY) {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		st := styleFB(rfg, rbg)
		a.cv.Fill(Rect{body.X, y, body.W, 1}, ' ', st)
		if sel && focused && th.Has("list.focusOutline") {
			a.cv.Set(body.X, y, '▏', styleFB(th.C("list.focusOutline"), rbg))
		}
		x := body.X + 1 + row.depth*indentW
		// indent guides
		guideSt := styleFB(th.OverColor("tree.indentGuidesStroke", rbg), rbg)
		if a.settings.String("workbench.tree.renderIndentGuides", "") != "none" && (body.Contains(a.mouseX, a.mouseY) || a.settings.String("workbench.tree.renderIndentGuides", "") == "always") {
			for d := 0; d < row.depth; d++ {
				a.cv.Set(body.X+1+d*indentW, y, '│', guideSt)
			}
		}
		if row.input {
			ed := e.editing
			icon := a.Icon(IcoFile)
			if ed.isDir {
				icon = a.Icon(IcoChevronRight)
			}
			x = a.cv.Text(x, y, body.X+body.W, icon+" ", st)
			ist := styleFB(th.C("input.foreground"), th.C("input.background"))
			ed.input.Draw(x, y, body.X+body.W-x-1, ist, ist, focused)
			continue
		}
		n := row.node
		if e.editing != nil && e.editing.rename == n.path {
			ist := styleFB(th.C("input.foreground"), th.C("input.background"))
			x = a.cv.Text(x, y, body.X+body.W, "  ", st)
			e.editing.input.Draw(x, y, body.X+body.W-x-1, ist, ist, focused)
			continue
		}
		if n.isDir {
			chev := a.Icon(IcoChevronRight)
			if e.expanded[n.path] {
				chev = a.Icon(IcoChevronDown)
			}
			x = a.cv.Text(x, y, body.X+body.W, chev+" ", st)
		} else {
			icon, col := a.FileIcon(n.name, false, false)
			if icon != "" {
				ist := styleFB(col, rbg)
				x = a.cv.Text(x, y, body.X+body.W, icon+" ", ist)
			} else {
				x = a.cv.Text(x, y, body.X+body.W, "  ", st)
			}
		}
		// git and diagnostics decorations
		nameSt := st
		gkey, letter := a.gitDecoration(n.path, n.isDir)
		if gkey != "" && !(sel && focused) {
			nameSt = nameSt.Foreground(th.C(gkey))
		}
		sev := a.pathSeverity(n.path, n.isDir)
		if sev == 1 && !(sel && focused) {
			nameSt = nameSt.Foreground(th.C("list.errorForeground"))
		} else if sev == 2 && !(sel && focused) && gkey == "" {
			nameSt = nameSt.Foreground(th.C("list.warningForeground"))
		}
		if e.cutMode && len(e.clipboard) > 0 && e.clipboard[0] == n.path {
			nameSt = nameSt.Dim(true)
		}
		right := letter
		if sev == 1 && !n.isDir {
			cnt := a.pathErrorCount(n.path)
			if cnt > 0 {
				right = itoaSimple(cnt) + " " + right
			}
		} else if sev == 1 && n.isDir {
			right = "●"
		} else if n.isDir && gkey != "" {
			right = "●"
		}
		rightW := StrWidth(right)
		if row.root {
			nameSt = nameSt.Bold(true)
		}
		a.cv.TextEllipsis(x, y, body.X+body.W-x-rightW-2, row.label, nameSt)
		if right != "" {
			rst := nameSt
			if sev == 1 && !(sel && focused) {
				rst = st.Foreground(th.C("list.errorForeground"))
			}
			a.cv.Text(body.X+body.W-rightW-1, y, body.X+body.W, right, rst)
		}
		node := n
		rowIdx := idx
		a.addHit(Rect{body.X, y, body.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.setFocus(FocusSidebar)
				if ev.Button == tcell.Button1 && ev.Mods&tcell.ModCtrl != 0 {
					// Ctrl+click toggles a row in the selection
					if e.multi == nil {
						e.multi = map[string]bool{}
					}
					if cur := e.current(); cur != nil {
						e.multi[cur.path] = true
					}
					e.multi[node.path] = !e.multi[node.path]
					e.selected, e.anchor = rowIdx, rowIdx
					return true
				}
				if ev.Button == tcell.Button1 && ev.Mods&tcell.ModShift != 0 {
					e.selectRange(rowIdx)
					return true
				}
				if ev.Button != tcell.Button2 || !e.multi[node.path] {
					e.multi = nil
				}
				e.selected, e.anchor = rowIdx, rowIdx
				switch ev.Button {
				case tcell.Button2:
					e.contextMenu(node, ev.X, ev.Y)
				case tcell.Button3:
					if !node.isDir {
						e.open(node, false, false)
					}
				default:
					if node.isDir {
						e.toggle(node)
					} else if ev.Clicks >= 2 {
						e.open(node, false, true)
					} else {
						// mouse clicks focus the editor; keyboard navigation keeps focus here
						e.open(node, true, true)
					}
					// drag to move files
					src := node
					a.drag = func(ev MouseEvent) {
						if ev.Kind != MouseRelease {
							return
						}
						e.dropAt(src, ev.X, ev.Y)
					}
				}
			case MouseWheel:
				e.scroll = clampInt(e.scroll+ev.WheelDY*3, 0, max(len(e.rows)-h, 0))
			}
			return true
		})
	}
	a.addHitUnder(mark, body, func(ev MouseEvent) bool {
		switch ev.Kind {
		case MouseWheel:
			e.scroll = clampInt(e.scroll+ev.WheelDY*3, 0, max(len(e.rows)-h, 0))
		case MousePress:
			a.setFocus(FocusSidebar)
			if ev.Button == tcell.Button2 {
				e.selected = -1
				target := e.root
				if target.virtual {
					target = target.children[0]
				}
				e.contextMenu(target, ev.X, ev.Y)
			}
		}
		return true
	})
	// scrollbar
	if len(e.rows) > h && h > 0 {
		sh := max(h*h/len(e.rows), 1)
		sy := e.scroll * (h - sh) / max(len(e.rows)-h, 1)
		if body.Contains(a.mouseX, a.mouseY) {
			for i := sy; i < sy+sh && i < h; i++ {
				a.cv.Set(body.X+body.W-1, body.Y+i, ' ', styleFB(bgc, th.OverColor("scrollbarSlider.background", bgc)))
			}
		}
	}
	// outline section
	oy := body.Y + body.H
	ohst := styleFB(th.C("sideBarSectionHeader.foreground"), th.OverColor("sideBarSectionHeader.background", bgc)).Bold(true)
	a.cv.Fill(Rect{r.X, oy, r.W, 1}, ' ', ohst)
	for x := r.X; x < r.X+r.W; x++ {
		a.cv.Set(x, oy, ' ', ohst.Underline(false))
	}
	ochev := a.Icon(IcoChevronRight)
	if e.outlineOpen {
		ochev = a.Icon(IcoChevronDown)
	}
	a.cv.Text(r.X, oy, r.X+2, ochev, ohst)
	a.cv.Text(r.X+2, oy, r.X+r.W, "OUTLINE", ohst)
	a.addHit(Rect{r.X, oy, r.W, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			e.outlineOpen = !e.outlineOpen
			if e.outlineOpen {
				a.outline.refresh()
			}
		}
		return true
	})
	if e.outlineOpen && outlineH > 1 {
		a.outline.drawList(Rect{r.X, oy + 1, r.W, outlineH - 1})
	}
}

func (e *Explorer) dropAt(src *exNode, x, y int) {
	a := e.app
	if !e.rect.Contains(x, y) {
		return
	}
	idx := e.scroll + (y - e.rect.Y - 1)
	if idx < 0 || idx >= len(e.rows) || e.rows[idx].node == nil {
		return
	}
	target := e.rows[idx].node
	dir := target.path
	if !target.isDir {
		dir = filepath.Dir(target.path)
	}
	srcs := []*exNode{src}
	if e.multi[src.path] {
		srcs = e.selectedNodes()
	}
	var moves []*exNode
	for _, n := range srcs {
		if dir == filepath.Dir(n.path) || strings.HasPrefix(dir+string(filepath.Separator), n.path+string(filepath.Separator)) || n.isRoot {
			continue
		}
		moves = append(moves, n)
	}
	if len(moves) == 0 {
		return
	}
	move := func() {
		for _, n := range moves {
			dst := filepath.Join(dir, n.name)
			a.movePath(n.path, dst, func(err error) {
				if err != nil {
					a.notify(NotifyError, "Failed to move: "+err.Error())
					return
				}
				e.refresh()
				e.reveal(dst, false)
			})
		}
		e.multi = nil
	}
	if a.settings.Bool("explorer.confirmDragAndDrop", "") {
		msg := "Are you sure you want to move '" + moves[0].name + "' into '" + filepath.Base(dir) + "'?"
		if len(moves) > 1 {
			msg = fmt.Sprintf("Are you sure you want to move the following %d files into '%s'?", len(moves), filepath.Base(dir))
		}
		a.showDialog(msg, "", []string{"Move", "Cancel"}, func(c int) {
			if c == 0 {
				move()
			}
		})
		return
	}
	move()
}

// selectedNodes returns the focused node plus the multi-selection, in tree order.
func (e *Explorer) selectedNodes() []*exNode {
	cur := e.current()
	var out []*exNode
	for _, r := range e.rows {
		if r.node != nil && (e.multi[r.node.path] || r.node == cur) {
			out = append(out, r.node)
		}
	}
	return out
}

// selectRange selects the rows between the anchor and i.
func (e *Explorer) selectRange(i int) {
	e.multi = map[string]bool{}
	from, to := min(e.anchor, i), max(e.anchor, i)
	for k := from; k <= to && k < len(e.rows); k++ {
		if k >= 0 && e.rows[k].node != nil {
			e.multi[e.rows[k].node.path] = true
		}
	}
	e.selected = i
}

func (e *Explorer) contextMenu(n *exNode, x, y int) {
	a := e.app
	var items []MenuItem
	items = append(items,
		MenuItem{Label: "New File...", Command: "explorer.newFile"},
		MenuItem{Label: "New Folder...", Command: "explorer.newFolder"},
		MenuItem{Label: "Reveal in File Manager", Run: func() {
			p := n.path
			if !n.isDir {
				p = filepath.Dir(p)
			}
			openExternal(p)
		}},
		MenuItem{Label: "Open in Integrated Terminal", Run: func() {
			p := n.path
			if !n.isDir {
				p = filepath.Dir(p)
			}
			a.showPanel("terminal")
			a.terminals.newTerminal(p)
		}},
		MenuItem{Separator: true},
		MenuItem{Label: "Find in Folder...", Run: func() {
			p := n.path
			if !n.isDir {
				p = filepath.Dir(p)
			}
			a.searchV.setInclude(a.relPath(p))
			a.findInFiles(false)
		}},
		MenuItem{Separator: true},
		MenuItem{Label: "Cut", Key: "Ctrl+X", Run: func() { e.handleKey("ctrl+x", "", false) }},
		MenuItem{Label: "Copy", Key: "Ctrl+C", Run: func() { e.handleKey("ctrl+c", "", false) }},
		MenuItem{Label: "Paste", Key: "Ctrl+V", Disabled: len(e.clipboard) == 0, Run: func() { e.paste() }},
		MenuItem{Separator: true},
		MenuItem{Label: "Copy Path", Key: "Ctrl+Alt+C", Run: func() { a.setClipboard(n.path) }},
		MenuItem{Label: "Copy Relative Path", Run: func() { a.setClipboard(a.relPath(n.path)) }},
	)
	if n != e.root && !n.isRoot {
		if !n.isDir {
			items = append(items, MenuItem{Separator: true},
				MenuItem{Label: "Open to the Side", Run: func() {
					a.splitEditor()
					a.OpenFile(n.path, false)
				}},
				MenuItem{Label: "Open Timeline (Git Log)", Run: func() { a.showFileHistory(n.path) }},
			)
			if a.gitRoot != "" {
				if _, ok := a.gitByPath[n.path]; ok {
					items = append(items, MenuItem{Label: "Open Changes", Run: func() { a.openGitDiff(n.path, false) }})
				}
			}
		}
		items = append(items, MenuItem{Separator: true},
			MenuItem{Label: "Rename...", Key: "F2", Run: func() { e.startRename() }},
			MenuItem{Label: "Delete", Key: "Delete", Run: func() { e.deleteSelected(false) }},
		)
	}
	a.openContextMenu(x, y, items)
}

// pathSeverity returns 1 for errors, 2 for warnings under path.
func (a *App) pathSeverity(path string, isDir bool) int {
	if a.lsp == nil {
		return 0
	}
	worst := 0
	for p, fds := range a.lsp.AllDiagnostics() {
		if p != path && !(isDir && strings.HasPrefix(p, path+string(filepath.Separator))) {
			continue
		}
		for _, fd := range fds {
			for _, d := range fd.Diagnostics {
				s := d.Severity
				if s == 0 {
					s = lsp.SeverityError
				}
				if s <= 2 && (worst == 0 || s < worst) {
					worst = s
				}
			}
		}
	}
	return worst
}

func (a *App) pathErrorCount(path string) int {
	if a.lsp == nil {
		return 0
	}
	n := 0
	for _, fd := range a.lsp.Diagnostics(path) {
		for _, d := range fd.Diagnostics {
			if d.Severity == lsp.SeverityError || d.Severity == 0 {
				n++
			}
		}
	}
	return n
}
