package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

//#region Problems

type problemRow struct {
	path   string
	diag   *lsp.Diagnostic
	enc    string
	server string
	count  int
}

// ProblemsPanel lists diagnostics for all files.
type ProblemsPanel struct {
	app       *App
	rows      []problemRow
	selected  int
	scroll    int
	dirty     bool
	collapsed map[string]bool
	rect      Rect
	filter    string
}

func newProblemsPanel(a *App) *ProblemsPanel {
	return &ProblemsPanel{app: a, collapsed: map[string]bool{}, dirty: true}
}

func (p *ProblemsPanel) rebuild() {
	a := p.app
	p.rows = p.rows[:0]
	if a.lsp == nil {
		return
	}
	all := a.lsp.AllDiagnostics()
	var paths []string
	for path := range all {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		var diags []problemRow
		for _, fd := range all[path] {
			for i := range fd.Diagnostics {
				d := fd.Diagnostics[i]
				diags = append(diags, problemRow{path: path, diag: &d, enc: fd.Encoding, server: fd.Server})
			}
		}
		if len(diags) == 0 {
			continue
		}
		sort.SliceStable(diags, func(i, j int) bool {
			si, sj := sevOrDefault(diags[i].diag.Severity), sevOrDefault(diags[j].diag.Severity)
			if si != sj {
				return si < sj
			}
			return diags[i].diag.Range.Start.Line < diags[j].diag.Range.Start.Line
		})
		p.rows = append(p.rows, problemRow{path: path, count: len(diags)})
		if p.collapsed[path] {
			continue
		}
		p.rows = append(p.rows, diags...)
	}
	if p.selected >= len(p.rows) {
		p.selected = max(len(p.rows)-1, 0)
	}
	p.dirty = false
}

func sevOrDefault(s int) int {
	if s == 0 {
		return 1
	}
	return s
}

func (p *ProblemsPanel) open(i int, focus bool) {
	a := p.app
	if i < 0 || i >= len(p.rows) {
		return
	}
	r := p.rows[i]
	if r.diag == nil {
		p.collapsed[r.path] = !p.collapsed[r.path]
		p.rebuild()
		return
	}
	v := a.OpenFile(r.path, !focus)
	if v == nil {
		return
	}
	pos := lsp.FromLSPPos(v.buf(), r.diag.Range.Start, r.enc)
	end := lsp.FromLSPPos(v.buf(), r.diag.Range.End, r.enc)
	a.pushNav()
	v.SetSelections([]buffer.Selection{{Anchor: pos, Active: pos}}, 0)
	_ = end
	v.ensurePosVisible(pos, true)
	if focus {
		a.focusEditor()
	}
}

func (p *ProblemsPanel) handleKey(key string) bool {
	switch key {
	case "up":
		p.selected = max(p.selected-1, 0)
	case "down":
		p.selected = min(p.selected+1, max(len(p.rows)-1, 0))
	case "enter":
		p.open(p.selected, true)
	case "space":
		p.open(p.selected, false)
	case "escape":
		p.app.focusEditor()
	default:
		return false
	}
	return true
}

func (p *ProblemsPanel) draw(r Rect) {
	a := p.app
	th := a.theme
	p.rect = r
	if p.dirty {
		p.rebuild()
	}
	bgc := th.C("panel.background")
	if len(p.rows) == 0 {
		a.cv.Text(r.X+2, r.Y, r.X+r.W, "No problems have been detected in the workspace.", styleFB(th.C("foreground"), bgc))
		return
	}
	h := r.H
	if p.selected < p.scroll {
		p.scroll = p.selected
	}
	if p.selected >= p.scroll+h {
		p.scroll = p.selected - h + 1
	}
	focused := a.focus == FocusPanel && a.panelView == "problems"
	for i := 0; i < h; i++ {
		idx := p.scroll + i
		if idx >= len(p.rows) {
			break
		}
		row := p.rows[idx]
		y := r.Y + i
		rbg := bgc
		fg := th.C("foreground")
		if idx == p.selected && focused {
			rbg = th.C("list.activeSelectionBackground")
			fg = th.C("list.activeSelectionForeground")
		} else if idx == p.selected {
			rbg = th.C("list.inactiveSelectionBackground")
		} else if a.mouseY == y && r.Contains(a.mouseX, a.mouseY) {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		st := styleFB(fg, rbg)
		a.cv.Fill(Rect{r.X, y, r.W, 1}, ' ', st)
		if row.diag == nil {
			chev := a.Icon(IcoChevronDown)
			if p.collapsed[row.path] {
				chev = a.Icon(IcoChevronRight)
			}
			x := a.cv.Text(r.X+1, y, r.X+r.W, chev+" ", st)
			icon, col := a.FileIcon(filepath.Base(row.path), false, false)
			if icon != "" {
				x = a.cv.Text(x, y, r.X+r.W, icon+" ", styleFB(col, rbg))
			}
			x = a.cv.Text(x, y, r.X+r.W, filepath.Base(row.path), st)
			dir := filepath.Dir(a.relPath(row.path))
			if dir != "." {
				x = a.cv.Text(x+1, y, r.X+r.W, dir, st.Foreground(th.OverColor("descriptionForeground", rbg)))
			}
			cnt := fmt.Sprintf(" %d ", row.count)
			a.cv.Text(x+1, y, r.X+r.W, cnt, styleFB(th.C("badge.foreground"), th.C("badge.background")))
		} else {
			d := row.diag
			icon, key := a.Icon(IcoError), "problemsErrorIcon.foreground"
			switch d.Severity {
			case lsp.SeverityWarning:
				icon, key = a.Icon(IcoWarning), "problemsWarningIcon.foreground"
			case lsp.SeverityInformation, lsp.SeverityHint:
				icon, key = a.Icon(IcoInfo), "problemsInfoIcon.foreground"
			}
			x := a.cv.Text(r.X+4, y, r.X+r.W, icon+" ", st.Foreground(th.C(key)))
			msg := strings.ReplaceAll(d.Message, "\n", " ")
			x = a.cv.Text(x, y, r.X+r.W, msg, st)
			src := d.Source
			if src == "" {
				src = row.server
			}
			if code := d.CodeString(); code != "" {
				src += "(" + code + ")"
			}
			extra := fmt.Sprintf(" %s [Ln %d, Col %d]", src, d.Range.Start.Line+1, d.Range.Start.Character+1)
			a.cv.Text(x, y, r.X+r.W, extra, st.Foreground(th.OverColor("descriptionForeground", rbg)))
		}
		rowIdx := idx
		a.addHit(Rect{r.X, y, r.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.setFocus(FocusPanel)
				p.selected = rowIdx
				if ev.Button == tcell.Button2 {
					row := p.rows[rowIdx]
					if row.diag != nil {
						msg := row.diag.Message
						a.openContextMenu(ev.X, ev.Y, []MenuItem{
							{Label: "Copy", Run: func() { a.setClipboard(msg) }},
							{Label: "Copy Message", Run: func() { a.setClipboard(msg) }},
						})
					}
					return true
				}
				p.open(rowIdx, ev.Clicks >= 2)
			case MouseWheel:
				p.scroll = clampInt(p.scroll+ev.WheelDY*3, 0, max(len(p.rows)-h, 0))
			}
			return true
		})
	}
	a.addHit(r, func(ev MouseEvent) bool {
		if ev.Kind == MouseWheel {
			p.scroll = clampInt(p.scroll+ev.WheelDY*3, 0, max(len(p.rows)-h, 0))
		}
		return true
	})
}

// gotoProblem jumps to the next/previous diagnostic across files.
func (a *App) gotoProblem(dir int) {
	type loc struct {
		path string
		pos  lsp.Position
		enc  string
		msg  string
		sev  int
	}
	var all []loc
	if a.lsp == nil {
		return
	}
	for path, fds := range a.lsp.AllDiagnostics() {
		for _, fd := range fds {
			for _, d := range fd.Diagnostics {
				all = append(all, loc{path, d.Range.Start, fd.Encoding, d.Message, d.Severity})
			}
		}
	}
	if len(all) == 0 {
		a.setStatusMessage("No problems")
		return
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].path != all[j].path {
			return all[i].path < all[j].path
		}
		if all[i].pos.Line != all[j].pos.Line {
			return all[i].pos.Line < all[j].pos.Line
		}
		return all[i].pos.Character < all[j].pos.Character
	})
	curPath := ""
	curPos := lsp.Position{}
	if v := a.ActiveView(); v != nil {
		curPath = v.buf().Path
		c := v.Cursor()
		curPos = a.lsp.ToLSP(v.buf(), c)
	}
	after := func(l loc) bool {
		if l.path != curPath {
			return l.path > curPath
		}
		if l.pos.Line != curPos.Line {
			return l.pos.Line > curPos.Line
		}
		return l.pos.Character > curPos.Character
	}
	idx := -1
	if dir > 0 {
		for i, l := range all {
			if after(l) {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = 0
		}
	} else {
		for i := len(all) - 1; i >= 0; i-- {
			l := all[i]
			if !after(l) && !(l.path == curPath && l.pos == curPos) {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = len(all) - 1
		}
	}
	l := all[idx]
	a.pushNav()
	v := a.OpenFile(l.path, false)
	if v == nil {
		return
	}
	p := lsp.FromLSPPos(v.buf(), l.pos, l.enc)
	v.SetCursor(p)
	v.ensurePosVisible(p, true)
	a.focusEditor()
	a.showHover(v, p, false)
	a.setStatusMessage(fmt.Sprintf("%d of %d problems", idx+1, len(all)))
}

//#endregion

//#region Output

type outputChannel struct {
	name  string
	lines []string
}

// OutputPanel shows log channels (language servers, git, tasks).
type OutputPanel struct {
	app      *App
	channels []*outputChannel
	active   int
	scroll   int
	follow   bool
}

func newOutputPanel(a *App) *OutputPanel {
	return &OutputPanel{app: a, follow: true}
}

func (o *OutputPanel) channel(name string) *outputChannel {
	for _, c := range o.channels {
		if c.name == name {
			return c
		}
	}
	c := &outputChannel{name: name}
	o.channels = append(o.channels, c)
	sort.Slice(o.channels, func(i, j int) bool { return o.channels[i].name < o.channels[j].name })
	return c
}

func (o *OutputPanel) append(name, text string) {
	c := o.channel(name)
	ts := time.Now().Format("15:04:05.000")
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		c.lines = append(c.lines, ts+" "+l)
	}
	if len(c.lines) > 5000 {
		c.lines = c.lines[len(c.lines)-5000:]
	}
}

func (o *OutputPanel) selectChannel(name string) {
	for i, c := range o.channels {
		if c.name == name {
			o.active = i
		}
	}
}

func (o *OutputPanel) selectFirstWithPrefix(p string) {
	for i, c := range o.channels {
		if strings.HasPrefix(c.name, p) {
			o.active = i
			return
		}
	}
}

func (o *OutputPanel) handleKey(key string) bool {
	switch key {
	case "up":
		o.scroll--
		o.follow = false
	case "down":
		o.scroll++
	case "pageup":
		o.scroll -= 10
		o.follow = false
	case "pagedown":
		o.scroll += 10
	case "end", "ctrl+end":
		o.follow = true
	case "escape":
		o.app.focusEditor()
	default:
		return false
	}
	return true
}

func (o *OutputPanel) draw(r Rect) {
	a := o.app
	th := a.theme
	bgc := th.C("panel.background")
	if len(o.channels) == 0 {
		a.cv.Text(r.X+2, r.Y, r.X+r.W, "No output yet.", styleFB(th.C("descriptionForeground"), bgc))
		return
	}
	if o.active >= len(o.channels) {
		o.active = 0
	}
	c := o.channels[o.active]
	// channel picker on the first line, right aligned
	label := "▾ " + c.name
	lx := r.X + r.W - StrWidth(label) - 2
	dst := styleFB(th.C("dropdown.foreground"), th.C("dropdown.background"))
	a.cv.Text(lx, r.Y, r.X+r.W, " "+label+" ", dst)
	a.addHit(Rect{lx, r.Y, StrWidth(label) + 2, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			var items []MenuItem
			for i, ch := range o.channels {
				idx := i
				items = append(items, MenuItem{Label: ch.name, Run: func() { o.active = idx; o.follow = true }})
			}
			a.openContextMenu(lx, r.Y+1, items)
		}
		return true
	})
	body := Rect{r.X, r.Y + 1, r.W, r.H - 1}
	h := body.H
	if o.follow {
		o.scroll = max(len(c.lines)-h, 0)
	}
	o.scroll = clampInt(o.scroll, 0, max(len(c.lines)-h, 0))
	if o.scroll >= max(len(c.lines)-h, 0) {
		o.follow = true
	}
	st := styleFB(th.C("foreground"), bgc)
	for i := 0; i < h; i++ {
		idx := o.scroll + i
		if idx >= len(c.lines) {
			break
		}
		line := c.lines[idx]
		lst := st
		if len(line) > 13 {
			a.cv.Text(body.X+1, body.Y+i, body.X+13, line[:12], st.Foreground(th.C("descriptionForeground")))
			line = line[13:]
		}
		low := strings.ToLower(line)
		if strings.Contains(low, "error") || strings.Contains(low, "fatal") {
			lst = st.Foreground(th.C("editorError.foreground"))
		} else if strings.Contains(low, "warn") {
			lst = st.Foreground(th.C("editorWarning.foreground"))
		}
		a.cv.TextEllipsis(body.X+14, body.Y+i, body.W-15, line, lst)
	}
	a.addHit(body, func(ev MouseEvent) bool {
		switch ev.Kind {
		case MouseWheel:
			o.scroll += ev.WheelDY * 3
			if ev.WheelDY < 0 {
				o.follow = false
			}
		case MousePress:
			a.setFocus(FocusPanel)
		}
		return true
	})
}

//#endregion

//#region Language servers view

// ExtensionsView lists language support and server status (in place of the extensions marketplace).
type ExtensionsView struct {
	app      *App
	selected int
	scroll   int
}

func newExtensionsView(a *App) *ExtensionsView {
	return &ExtensionsView{app: a}
}

type langStatus struct {
	name    string
	id      string
	server  string
	found   bool
	running bool
	install string
}

func (e *ExtensionsView) statuses() []langStatus {
	a := e.app
	var out []langStatus
	seen := map[string]bool{}
	running := map[string]bool{}
	if a.lsp != nil {
		for _, c := range a.lsp.Clients() {
			running[c.Name] = true
		}
	}
	for _, l := range sortedLanguagesWithServers() {
		if len(l.Servers) == 0 {
			continue
		}
		key := l.Servers[0][0]
		if seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		st := langStatus{name: l.Name, id: l.ID, server: key, install: installHint(key)}
		if a.lsp != nil {
			if cmd := a.lsp.ServerCommand(l); cmd != nil {
				st.found = true
				st.server = filepath.Base(cmd[0])
				if len(cmd) > 1 && cmd[1] == "lsp" {
					st.server += " lsp"
				}
				st.running = running[filepath.Base(cmd[0])]
			}
		}
		out = append(out, st)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].running != out[j].running {
			return out[i].running
		}
		if out[i].found != out[j].found {
			return out[i].found
		}
		return out[i].name < out[j].name
	})
	return out
}

func installHint(server string) string {
	arch := fileExists("/etc/arch-release")
	switch server {
	case "gopls":
		return "go install golang.org/x/tools/gopls@latest"
	case "typescript-language-server":
		return "npm install -g typescript-language-server typescript"
	case "pyright-langserver":
		return "npm install -g pyright"
	case "rust-analyzer":
		return "rustup component add rust-analyzer"
	case "clangd":
		if arch {
			return "sudo pacman -S clang"
		}
		return "sudo apt install clangd"
	case "vscode-json-language-server", "vscode-css-language-server", "vscode-html-language-server":
		return "npm install -g vscode-langservers-extracted"
	case "bash-language-server":
		return "npm install -g bash-language-server"
	case "yaml-language-server":
		return "npm install -g yaml-language-server"
	case "lua-language-server":
		if arch {
			return "sudo pacman -S lua-language-server"
		}
		return "see github.com/LuaLS/lua-language-server"
	case "intelephense":
		return "npm install -g intelephense"
	case "dart":
		return "install the Dart SDK"
	case "docker-langserver":
		return "npm install -g dockerfile-language-server-nodejs"
	case "marksman":
		if arch {
			return "sudo pacman -S marksman"
		}
		return "see github.com/artempyanykh/marksman"
	case "taplo":
		return "cargo install taplo-cli --features lsp"
	case "zls":
		if arch {
			return "sudo pacman -S zls"
		}
		return "see github.com/zigtools/zls"
	case "csharp-ls":
		return "dotnet tool install -g csharp-ls"
	case "jdtls":
		if arch {
			return "yay -S jdtls"
		}
		return "see github.com/eclipse-jdtls/eclipse.jdt.ls"
	case "vue-language-server":
		return "npm install -g @vue/language-server"
	case "svelteserver":
		return "npm install -g svelte-language-server"
	case "ruby-lsp":
		return "gem install ruby-lsp"
	case "kotlin-language-server":
		return "see github.com/fwcd/kotlin-language-server"
	}
	return ""
}

func (e *ExtensionsView) handleKey(key, text string, isText bool) bool {
	sts := e.statuses()
	switch key {
	case "up":
		e.selected = max(e.selected-1, 0)
	case "down":
		e.selected = min(e.selected+1, len(sts)-1)
	case "enter":
		if e.selected < len(sts) && sts[e.selected].install != "" && !sts[e.selected].found {
			e.app.terminals.runText(sts[e.selected].install)
		}
	case "escape":
		e.app.focusEditor()
	default:
		return false
	}
	return true
}

func (e *ExtensionsView) draw(r Rect) {
	a := e.app
	th := a.theme
	bgc := th.C("sideBar.background")
	sts := e.statuses()
	y := r.Y
	desc := styleFB(th.C("descriptionForeground"), bgc)
	if a.lsp == nil {
		a.cv.Text(r.X+2, y, r.X+r.W, "Language servers are disabled.", desc)
		return
	}
	focused := a.focus == FocusSidebar && a.sidebarView == "extensions"
	h := r.H / 2
	if e.selected < e.scroll {
		e.scroll = e.selected
	}
	if e.selected >= e.scroll+h {
		e.scroll = e.selected - h + 1
	}
	for i := 0; i < h; i++ {
		idx := e.scroll + i
		if idx >= len(sts) {
			break
		}
		s := sts[idx]
		y = r.Y + i*2
		rbg := bgc
		fg := th.C("sideBar.foreground")
		if idx == e.selected && focused {
			rbg = th.C("list.activeSelectionBackground")
			fg = th.C("list.activeSelectionForeground")
		} else if idx == e.selected {
			rbg = th.C("list.inactiveSelectionBackground")
		}
		st := styleFB(fg, rbg)
		a.cv.Fill(Rect{r.X, y, r.W, 2}, ' ', st)
		icon := a.Icon(IcoServer)
		state := "not installed"
		ist := st.Foreground(th.OverColor("descriptionForeground", rbg))
		switch {
		case s.running:
			state = "running"
			ist = st.Foreground(th.C("gitDecoration.addedResourceForeground"))
		case s.found:
			state = "installed"
			ist = st.Foreground(th.C("textLink.foreground"))
		}
		x := a.cv.Text(r.X+1, y, r.X+r.W, icon+" ", ist)
		x = a.cv.Text(x, y, r.X+r.W, s.name, st.Bold(true))
		a.cv.TextRight(x, y, r.X+r.W-x-1, state, ist)
		line2 := s.server
		if !s.found && s.install != "" {
			line2 = s.install
		}
		a.cv.TextEllipsis(r.X+3, y+1, r.W-4, line2, st.Foreground(th.OverColor("descriptionForeground", rbg)))
		rowIdx := idx
		item := s
		a.addHit(Rect{r.X, y, r.W, 2}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.setFocus(FocusSidebar)
				e.selected = rowIdx
				if !item.found && item.install != "" {
					a.openContextMenu(ev.X, ev.Y, []MenuItem{
						{Label: "Install in Terminal", Run: func() { a.terminals.runText(item.install) }},
						{Label: "Copy Install Command", Run: func() { a.setClipboard(item.install) }},
					})
				} else if item.found {
					a.openContextMenu(ev.X, ev.Y, []MenuItem{
						{Label: "Restart Language Servers", Command: "editor.action.restartLanguageServer"},
						{Label: "Show Output", Command: "editor.action.showLanguageServerLog"},
					})
				}
			}
			if ev.Kind == MouseWheel {
				e.scroll = clampInt(e.scroll+ev.WheelDY, 0, max(len(sts)-h, 0))
			}
			return true
		})
	}
}

//#endregion
