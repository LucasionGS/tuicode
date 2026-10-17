package ui

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tuicode/internal/config"
	"tuicode/internal/term"
)

//go:embed shell/tuicode.bash
var bashIntegration string

//go:embed shell/tuicode.zsh
var zshIntegration string

//go:embed shell/zshenv
var zshEnvIntegration string

//go:embed shell/tuicode.fish
var fishIntegration string

// shellIntegrationArgs returns the arguments and environment that load tuicode's shell integration
// for shell, or ok=false when the shell isn't supported or the profile already passes arguments.
func (a *App) shellIntegrationArgs(shell string, args []string) (newArgs, env []string, ok bool) {
	if !a.settings.Bool("terminal.integrated.shellIntegration.enabled", "") || len(args) > 0 {
		return nil, nil, false
	}
	dir := filepath.Join(config.Dir(), "shell-integration")
	write := func(name, content string) (string, bool) {
		p := filepath.Join(dir, name)
		if old, err := os.ReadFile(p); err == nil && string(old) == content {
			return p, true
		}
		if err := fsMkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", false
		}
		return p, fsWriteFile(p, []byte(content), 0o644) == nil
	}
	switch filepath.Base(shell) {
	case "bash":
		p, ok := write("tuicode.bash", bashIntegration)
		if !ok {
			return nil, nil, false
		}
		return []string{"--init-file", p}, nil, true
	case "zsh":
		if _, ok := write("zsh/.zshenv", zshEnvIntegration); !ok {
			return nil, nil, false
		}
		if _, ok := write("zsh/.zshrc", zshIntegration); !ok {
			return nil, nil, false
		}
		user := os.Getenv("ZDOTDIR")
		if user == "" {
			user, _ = os.UserHomeDir()
		}
		return nil, []string{"ZDOTDIR=" + filepath.Join(dir, "zsh"), "TUICODE_USER_ZDOTDIR=" + user}, true
	case "fish":
		p, ok := write("tuicode.fish", fishIntegration)
		if !ok {
			return nil, nil, false
		}
		return []string{"--init-command", "source " + shellQuote(p)}, nil, true
	}
	return nil, nil, false
}

// markColor returns the decoration color key for a command mark, like VS Code's command decorations.
func markColor(m *term.CommandMark) string {
	switch {
	case !m.Finished:
		return "terminalCommandDecoration.defaultBackground"
	case m.ExitCode != 0:
		return "terminalCommandDecoration.errorBackground"
	}
	return "terminalCommandDecoration.successBackground"
}

// drawDecorations draws command decorations in the padding column left of a terminal.
func (tp *TerminalPanel) drawDecorations(ti *TermInstance, r Rect) {
	a := tp.app
	th := a.theme
	mode := a.settings.String("terminal.integrated.shellIntegration.decorationsEnabled", "")
	if mode == "never" || mode == "overviewRuler" || r.X == 0 {
		return
	}
	t := ti.t
	t.Lock()
	if !t.ShellIntegration {
		t.Unlock()
		return
	}
	marks := t.Marks()
	t.Unlock()
	bg := th.C("terminal.background")
	x := r.X - 1
	for _, m := range marks {
		y := r.Y + m.Row + ti.scrollOff
		if y < r.Y || y >= r.Y+r.H || !m.Mark.Executed {
			continue
		}
		mark := m.Mark
		row := m.Row
		ch := '●'
		if !mark.Finished {
			ch = '○'
		}
		a.cv.Set(x, y, ch, styleFB(th.OverColor(markColor(mark), bg), bg))
		a.addHit(Rect{x, y, 1, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MouseMove:
				a.showTooltipAt(ev.X+1, ev.Y+1, describeMark(mark))
			case MousePress:
				tp.markMenu(ti, row, mark, ev.X, ev.Y+1)
			}
			return true
		})
	}
}

func describeMark(m *term.CommandMark) string {
	if !m.Finished {
		return "Command is running"
	}
	ago := timeAgo(m.Ended)
	if m.ExitCode != 0 {
		return fmt.Sprintf("Command executed %s and failed (Exit Code %d)", ago, m.ExitCode)
	}
	return "Command executed " + ago
}

// markMenu shows VS Code's command decoration actions.
func (tp *TerminalPanel) markMenu(ti *TermInstance, row int, m *term.CommandMark, x, y int) {
	a := tp.app
	items := []MenuItem{
		{Label: "Copy Command", Disabled: m.Command == "", Run: func() { a.setClipboard(m.Command) }},
		{Label: "Copy Output", Run: func() {
			ti.t.Lock()
			out := ti.t.CommandOutput(row)
			ti.t.Unlock()
			a.setClipboard(out)
		}},
		{Label: "Rerun Command", Disabled: m.Command == "", Run: func() {
			tp.activate(ti.id)
			ti.t.WriteInput([]byte(m.Command + "\r"))
			a.showPanel("terminal")
		}},
	}
	a.openContextMenu(x, y, items)
}

// scrollToCommand scrolls the active terminal to the previous (dir < 0) or next command prompt.
func (tp *TerminalPanel) scrollToCommand(dir int) {
	ti := tp.current()
	if ti == nil {
		return
	}
	t := ti.t
	t.Lock()
	marks := t.Marks()
	sb := t.ScrollbackLen()
	t.Unlock()
	if len(marks) == 0 {
		return
	}
	top := -ti.scrollOff // row shown on the first line
	target := 0
	found := false
	if dir < 0 {
		for i := len(marks) - 1; i >= 0; i-- {
			if marks[i].Row < top {
				target, found = marks[i].Row, true
				break
			}
		}
	} else {
		for _, m := range marks {
			if m.Row > top {
				target, found = m.Row, true
				break
			}
		}
	}
	if !found {
		if dir > 0 {
			ti.scrollOff = 0
		}
		return
	}
	ti.scrollOff = clampInt(-target, 0, sb)
}

// recentTerminalCommands lists commands run in the terminals this session, then the shell's history file.
func (a *App) recentTerminalCommands() []string {
	var out []string
	seen := map[string]bool{}
	type entry struct {
		cmd  string
		when time.Time
	}
	var session []entry
	for _, ti := range a.terminals.terms {
		ti.t.Lock()
		for _, m := range ti.t.Marks() {
			if m.Mark.Command != "" {
				session = append(session, entry{m.Mark.Command, m.Mark.Started})
			}
		}
		ti.t.Unlock()
	}
	sort.SliceStable(session, func(i, j int) bool { return session[i].when.After(session[j].when) })
	for _, e := range session {
		if !seen[e.cmd] {
			seen[e.cmd] = true
			out = append(out, e.cmd)
		}
	}
	for _, c := range shellHistory(a.terminals) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// shellHistory reads the newest entries of the default shell's history file.
func shellHistory(tp *TerminalPanel) []string {
	shell, _ := tp.shell()
	home, _ := os.UserHomeDir()
	var path string
	var lines []string
	switch filepath.Base(shell) {
	case "bash":
		path = filepath.Join(home, ".bash_history")
	case "zsh":
		path = filepath.Join(home, ".zsh_history")
		if h := os.Getenv("HISTFILE"); h != "" {
			path = h
		}
	case "fish":
		path = filepath.Join(home, ".local", "share", "fish", "fish_history")
	default:
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		switch filepath.Base(shell) {
		case "fish":
			// Example:
			// - cmd: git status
			//   when: 1700000000
			if c, ok := strings.CutPrefix(l, "- cmd: "); ok {
				lines = append(lines, strings.ReplaceAll(c, "\\n", "\n"))
			}
		case "zsh":
			// Example (extended history):
			// : 1700000000:0;git status
			if strings.HasPrefix(l, ": ") {
				if _, c, ok := strings.Cut(l, ";"); ok {
					l = c
				}
			}
			lines = append(lines, l)
		default:
			if !strings.HasPrefix(l, "#") {
				lines = append(lines, l)
			}
		}
	}
	// newest first, at most 500
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < 500; i-- {
		if c := strings.TrimSpace(lines[i]); c != "" {
			out = append(out, lines[i])
		}
	}
	return out
}

// runRecentCommand picks a command from the terminal history and runs it, like Ctrl+Alt+R in VS Code.
func (a *App) runRecentCommand() {
	cmds := a.recentTerminalCommands()
	if len(cmds) == 0 {
		a.setStatusMessage("No recent commands.")
		return
	}
	items := make([]QuickItem, len(cmds))
	for i, c := range cmds {
		items[i] = QuickItem{Label: strings.ReplaceAll(c, "\n", " ⏎ "), Data: c}
	}
	a.openQuick(&QuickPick{Placeholder: "Select a command to run", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		a.showPanel("terminal")
		if cur := a.terminals.current(); cur != nil {
			cur.scrollOff = 0
			cur.t.WriteInput([]byte(it.Data.(string) + "\r"))
		}
	}})
}
