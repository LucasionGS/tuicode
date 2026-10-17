package ui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tuicode/internal/buffer"
	"tuicode/internal/git"
	"tuicode/internal/lsp"
)

func quickPrefixMode(p string) string {
	switch p {
	case ">":
		return "commands"
	case "@", "@:":
		return "symbols"
	case "#":
		return "workspaceSymbols"
	case ":":
		return "line"
	case "?":
		return "help"
	case "%":
		return "text"
	case "term ":
		return "terminals"
	}
	return "files"
}

func detectMode(text string) (string, string) {
	for _, p := range []string{">", "@:", "@", "#", ":", "?", "%", "term "} {
		if strings.HasPrefix(text, p) {
			return quickPrefixMode(p), p
		}
	}
	return "files", ""
}

// quickAccess opens the unified quick access input with an initial value.
func (a *App) quickAccess(initial string) {
	p := &QuickPick{Value: initial}
	q := a.openQuick(p)
	var lastMode string
	var restore []restoreState
	p.OnChange = func(text string) {
		mode, _ := detectMode(text)
		if mode != lastMode {
			lastMode = mode
			p.Mode = mode
			p.NoFilter = false
			p.KeepOrder = false
			p.MatchDescription = false
			p.OnSelect = nil
			switch mode {
			case "commands":
				p.Placeholder = ""
				p.Items = a.commandItems()
				p.KeepOrder = false
			case "files":
				p.Placeholder = "Search files by name (append : to go to line or @ to go to symbol)"
				p.MatchDescription = true
				p.Items = a.fileItems()
			case "line":
				p.NoFilter = true
			case "symbols":
				p.NoFilter = false
				p.KeepOrder = true
				a.loadSymbolItems(q)
			case "workspaceSymbols":
				p.NoFilter = true
			case "help":
				p.Items = a.helpItems()
				p.NoFilter = true
			case "terminals":
				p.Items = a.terminalItems()
			}
			q.input.Placeholder = p.Placeholder
		}
		switch mode {
		case "line":
			p.Items = a.gotoLineItems(strings.TrimPrefix(text, ":"))
			if v := a.ActiveView(); v != nil && len(p.Items) > 0 && p.Items[0].Data != nil {
				if restore == nil {
					restore = []restoreState{{v, append([]buffer.Selection{}, v.sels...), v.top()}}
				}
				pos := p.Items[0].Data.(buffer.Pos)
				v.SetCursor(pos)
				v.ensurePosVisible(pos, true)
			}
		case "workspaceSymbols":
			a.loadWorkspaceSymbols(q, strings.TrimPrefix(text, "#"))
		case "files":
			if i := strings.LastIndex(text, ":"); i > 0 {
				// "file:line" filters by file and remembers the line
				_ = i
			}
		case "symbols":
			if v := a.ActiveView(); v != nil && restore == nil {
				restore = []restoreState{{v, append([]buffer.Selection{}, v.sels...), v.top()}}
			}
			p.OnSelect = func(it *QuickItem) {
				if it == nil || it.Data == nil {
					return
				}
				if v := a.ActiveView(); v != nil {
					if r, ok := it.Data.(buffer.Range); ok {
						v.SetSelections([]buffer.Selection{{Anchor: r.Start, Active: r.Start}}, 0)
						v.ensurePosVisible(r.Start, true)
					}
				}
			}
		}
	}
	p.OnCancel = func() {
		for _, r := range restore {
			r.v.SetSelections(r.sels, -1)
			r.v.setTop(r.top)
		}
	}
	p.OnAccept = func(it *QuickItem, query string) {
		mode, _ := detectMode(query)
		switch mode {
		case "commands":
			if it != nil {
				id := it.Data.(string)
				a.rememberCommand(id)
				a.RunCommand(id, nil)
			}
		case "files":
			if it == nil {
				return
			}
			path := it.Data.(string)
			line := 0
			if i := strings.LastIndex(query, ":"); i > 0 {
				line, _ = strconv.Atoi(strings.TrimSpace(query[i+1:]))
			}
			v := a.OpenFile(path, false)
			if v != nil && line > 0 {
				p := v.buf().Clamp(buffer.Pos{Line: line - 1})
				v.SetCursor(p)
				v.ensurePosVisible(p, true)
			}
			a.focusEditor()
		case "line":
			if it != nil && it.Data != nil {
				if v := a.ActiveView(); v != nil {
					a.pushNavAt(restore)
					pos := it.Data.(buffer.Pos)
					v.SetCursor(pos)
					v.ensurePosVisible(pos, true)
				}
			}
			a.focusEditor()
		case "symbols":
			if it != nil && it.Data != nil {
				if v := a.ActiveView(); v != nil {
					a.pushNavAt(restore)
					r := it.Data.(buffer.Range)
					v.SetCursor(r.Start)
					v.ensurePosVisible(r.Start, true)
				}
			} else {
				p.OnCancel()
			}
			a.focusEditor()
		case "workspaceSymbols":
			if it != nil && it.Data != nil {
				loc := it.Data.(lsp.Location)
				a.openLocation(loc)
			}
		case "help":
			if it != nil {
				a.quickAccess(it.Data.(string))
			}
		case "terminals":
			if it != nil {
				a.terminals.activate(it.Data.(int))
				a.showPanel("terminal")
			}
		}
	}
	p.OnChange(initial)
	q.refilter()
}

type restoreState struct {
	v    *EditorView
	sels []buffer.Selection
	top  vpos
}

func (a *App) pushNavAt(rs []restoreState) {
	if len(rs) == 0 {
		a.pushNav()
		return
	}
	r := rs[0]
	if len(r.sels) > 0 {
		a.navBack = append(a.navBack, navEntry{path: r.v.buf().Path, pos: r.sels[0].Active, doc: r.v.doc})
		a.navForward = nil
	}
}

func (a *App) helpItems() []QuickItem {
	return []QuickItem{
		{Label: "…", Description: "Go to File", Data: ""},
		{Label: ">", Description: "Show and Run Commands", Data: ">"},
		{Label: "@", Description: "Go to Symbol in Editor", Data: "@"},
		{Label: "#", Description: "Go to Symbol in Workspace", Data: "#"},
		{Label: ":", Description: "Go to Line/Column", Data: ":"},
		{Label: "term ", Description: "Show All Opened Terminals", Data: "term "},
		{Label: "?", Description: "Help", Data: "?"},
	}
}

func (a *App) commandItems() []QuickItem {
	var items []QuickItem
	seen := map[string]bool{}
	for _, id := range a.recentCommands {
		c, ok := a.commands[id]
		if !ok || c.Hidden {
			continue
		}
		seen[id] = true
		items = append(items, QuickItem{Label: c.FullTitle(), Keybinding: a.keyLabel(id), Data: id, Group: "recently used"})
	}
	var rest []QuickItem
	for id, c := range a.commands {
		if c.Hidden || seen[id] || c.Title == "" {
			continue
		}
		if c.When != nil && !c.When(a) {
			continue
		}
		rest = append(rest, QuickItem{Label: c.FullTitle(), Keybinding: a.keyLabel(id), Data: id})
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Label < rest[j].Label })
	if len(items) > 0 && len(rest) > 0 {
		rest[0].Group = "other commands"
		for i := range rest[1:] {
			rest[i+1].Group = "other commands"
		}
	}
	return append(items, rest...)
}

func (a *App) rememberCommand(id string) {
	for i, c := range a.recentCommands {
		if c == id {
			a.recentCommands = append(a.recentCommands[:i], a.recentCommands[i+1:]...)
			break
		}
	}
	a.recentCommands = append([]string{id}, a.recentCommands...)
	if len(a.recentCommands) > 15 {
		a.recentCommands = a.recentCommands[:15]
	}
}

func (a *App) gotoLineItems(q string) []QuickItem {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		return []QuickItem{{Label: "Open a text editor first to go to a line."}}
	}
	b := v.buf()
	q = strings.TrimSpace(q)
	cur := v.Cursor()
	if q == "" {
		return []QuickItem{{Label: fmt.Sprintf("Current Line: %d, Character: %d. Type a line number between 1 and %d to navigate to.", cur.Line+1, cur.Col+1, b.LineCount())}}
	}
	parts := strings.FieldsFunc(q, func(r rune) bool { return r == ':' || r == ',' })
	if len(parts) == 0 {
		return []QuickItem{{Label: "Please enter a valid line number."}}
	}
	line, err := strconv.Atoi(parts[0])
	if err != nil {
		return []QuickItem{{Label: "Please enter a valid line number."}}
	}
	col := 1
	if len(parts) > 1 {
		col, _ = strconv.Atoi(parts[1])
	}
	if line < 0 {
		line = b.LineCount() + line + 1
	}
	line = clampInt(line, 1, b.LineCount())
	pos := b.Clamp(buffer.Pos{Line: line - 1, Col: max(col-1, 0)})
	label := fmt.Sprintf("Go to line %d.", line)
	if len(parts) > 1 {
		label = fmt.Sprintf("Go to line %d, character %d.", line, col)
	}
	return []QuickItem{{Label: label, Data: pos}}
}

func (a *App) loadSymbolItems(q *QuickInput) {
	v := a.ActiveView()
	if v == nil || v.special != nil {
		q.message = "To go to a symbol, first open a text editor with symbol information."
		q.setItems(nil)
		return
	}
	build := func(syms []outlineSym) {
		var items []QuickItem
		for _, s := range syms {
			icon, ck := a.kindIcon(s.Kind, true)
			r := buffer.Range{Start: s.pos, End: s.pos}
			items = append(items, QuickItem{Label: s.Name, Description: s.Detail, Icon: icon, IconColor: a.theme.C(ck), Data: r})
		}
		if len(items) == 0 {
			q.message = "No editor symbols"
		}
		q.setItems(items)
	}
	syms := a.outline.flatSymbols(v.doc)
	if syms != nil {
		build(syms)
		return
	}
	q.busy = true
	a.outline.fetch(v.doc, func() {
		q.busy = false
		if a.quick == q {
			build(a.outline.flatSymbols(v.doc))
		}
	})
}

func (a *App) loadWorkspaceSymbols(q *QuickInput, query string) {
	v := a.ActiveView()
	if a.lsp == nil {
		q.message = "No language server available"
		return
	}
	var buf *Document
	if v != nil && v.special == nil {
		buf = v.doc
	} else {
		for _, d := range a.docs {
			if a.lsp.Client(d.Buf) != nil {
				buf = d
				break
			}
		}
	}
	if buf == nil {
		q.message = "Open a file with language support to search workspace symbols."
		return
	}
	q.busy = true
	_, ok := a.lsp.Request(buf.Buf, "workspace/symbol", map[string]any{"query": strings.TrimSpace(query)}, func(raw json.RawMessage, err error) {
		q.busy = false
		if a.quick != q {
			return
		}
		var syms []lsp.SymbolInformation
		_ = json.Unmarshal(raw, &syms)
		var items []QuickItem
		for _, s := range syms {
			icon, ck := a.kindIcon(s.Kind, true)
			path := lsp.URIToPath(s.Location.URI)
			items = append(items, QuickItem{Label: s.Name, Description: s.ContainerName + "  " + a.relPath(path), Icon: icon, IconColor: a.theme.C(ck), Data: s.Location})
		}
		if len(items) > 200 {
			items = items[:200]
		}
		q.setItems(items)
	})
	if !ok {
		q.busy = false
		q.message = "No language server available"
	}
}

func (a *App) openLocation(loc lsp.Location) *EditorView {
	path := lsp.URIToPath(loc.URI)
	a.pushNav()
	v := a.OpenFile(path, false)
	if v == nil {
		return nil
	}
	r := a.lspRange(v.buf(), loc.Range)
	v.SetSelections([]buffer.Selection{{Anchor: r.Start, Active: r.Start}}, 0)
	v.ensurePosVisible(r.Start, true)
	a.focusEditor()
	return v
}

//#region Workspace file index

type fileIndex struct {
	files   []string
	built   time.Time
	loading bool
}

var excludedDirs = map[string]bool{".git": true, "node_modules": true, ".svn": true, ".hg": true, "dist": false, ".cache": true, "__pycache__": true, ".venv": true, "target": false, ".idea": true}

func (a *App) excludeMatcher() func(rel string, isDir bool) bool {
	var patterns []string
	for pat, v := range a.settings.Map("files.exclude") {
		if b, ok := v.(bool); ok && b {
			patterns = append(patterns, pat)
		}
	}
	return func(rel string, isDir bool) bool {
		base := filepath.Base(rel)
		for _, pat := range patterns {
			p := strings.TrimPrefix(pat, "**/")
			if ok, _ := filepath.Match(p, base); ok {
				return true
			}
			if ok, _ := filepath.Match(pat, rel); ok {
				return true
			}
		}
		return false
	}
}

// workspaceFiles lists files in the workspace (cached, refreshed in the background).
func (a *App) workspaceFiles(done func()) []string {
	idx := a.fileIdx
	if idx == nil {
		idx = &fileIndex{}
		a.fileIdx = idx
	}
	if !idx.loading && (idx.built.IsZero() || time.Since(idx.built) > 10*time.Second) {
		idx.loading = true
		root := a.root
		gitRoot := a.gitRoot
		exclude := a.excludeMatcher()
		go func() {
			files := listFiles(root, gitRoot, exclude)
			a.Post(func() {
				idx.files = files
				idx.built = time.Now()
				idx.loading = false
				if done != nil {
					done()
				}
			})
		}()
	}
	return idx.files
}

func listFiles(root, gitRoot string, exclude func(string, bool) bool) []string {
	if root == "" {
		return nil
	}
	var out []string
	if gitRoot != "" && strings.HasPrefix(root, gitRoot) {
		res, err := git.Run(root, "ls-files", "-co", "--exclude-standard", "-z")
		if err == nil {
			for _, f := range strings.Split(res, "\x00") {
				if f == "" {
					continue
				}
				if exclude(f, false) {
					continue
				}
				full := filepath.Join(root, f)
				out = append(out, full)
				if len(out) > 200000 {
					break
				}
			}
			return out
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if p != root && (excludedDirs[d.Name()] || exclude(rel, true)) {
				return filepath.SkipDir
			}
			return nil
		}
		if exclude(rel, false) {
			return nil
		}
		out = append(out, p)
		if len(out) > 100000 {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

func (a *App) fileItems() []QuickItem {
	var items []QuickItem
	seen := map[string]bool{}
	// recently opened first
	for _, p := range a.recentFiles() {
		if seen[p] {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		seen[p] = true
		items = append(items, a.fileItem(p, "recently opened"))
	}
	files := a.workspaceFiles(func() {
		if a.quick != nil && a.quick.pick.Mode == "files" {
			a.quick.pick.Items = a.fileItems()
			a.quick.refilter()
		}
	})
	for _, f := range files {
		if seen[f] {
			continue
		}
		items = append(items, a.fileItem(f, ""))
	}
	if len(items) > 0 && len(files) > 0 {
		for i := range items {
			if items[i].Group == "" {
				items[i].Group = "files"
				break
			}
		}
	}
	return items
}

func (a *App) fileItem(path, group string) QuickItem {
	rel := a.relPath(path)
	dir := filepath.Dir(rel)
	if dir == "." {
		dir = ""
	}
	icon, col := a.FileIcon(filepath.Base(path), false, false)
	return QuickItem{Label: filepath.Base(path), Description: dir, Icon: icon, IconColor: col, Data: path, Group: group}
}

// quickOpenInDir opens quick open filtered to a folder (breadcrumb click).
func (a *App) quickOpenInDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var items []QuickItem
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			items = append(items, QuickItem{Label: e.Name(), Icon: a.Icon(IcoFolder), Data: p, Description: "folder"})
		} else {
			icon, col := a.FileIcon(e.Name(), false, false)
			items = append(items, QuickItem{Label: e.Name(), Icon: icon, IconColor: col, Data: p})
		}
	}
	a.openQuick(&QuickPick{Placeholder: a.relPath(dir), Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		p := it.Data.(string)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			a.quickOpenInDir(p)
			return
		}
		a.OpenFile(p, false)
		a.focusEditor()
	}})
}

//#endregion
