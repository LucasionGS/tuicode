package ui

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/git"
)

// externalFormatter maps a VS Code formatter extension in editor.defaultFormatter to its command line tool.
func (a *App) externalFormatter(v *EditorView) ([]string, bool) {
	id := a.settings.String("editor.defaultFormatter", v.lang())
	path := v.buf().Path
	if id == "" || path == "" {
		return nil, false
	}
	// node tools come from the project first, like the extensions resolve them
	local := func(name string) string {
		for d := filepath.Dir(path); filepath.Dir(d) != d; d = filepath.Dir(d) {
			if p := filepath.Join(d, "node_modules", ".bin", name); fileExists(p) {
				return p
			}
		}
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		return ""
	}
	look := func(name string) string {
		p, _ := exec.LookPath(name)
		return p
	}
	var cmd []string
	switch strings.ToLower(id) {
	case "esbenp.prettier-vscode", "prettier.prettier-vscode":
		if p := local("prettier"); p != "" {
			cmd = []string{p, "--stdin-filepath", path}
		}
	case "biomejs.biome":
		if p := local("biome"); p != "" {
			cmd = []string{p, "format", "--stdin-file-path=" + path}
		}
	case "charliermarsh.ruff":
		if p := look("ruff"); p != "" {
			cmd = []string{p, "format", "--stdin-filename", path, "-"}
		}
	case "ms-python.black-formatter":
		if p := look("black"); p != "" {
			cmd = []string{p, "-q", "--stdin-filename", path, "-"}
		}
	case "foxundermoon.shell-format", "mkhl.shfmt":
		if p := look("shfmt"); p != "" {
			cmd = []string{p, "-filename", path}
		}
	case "tamasfe.even-better-toml":
		if p := look("taplo"); p != "" {
			cmd = []string{p, "fmt", "-"}
		}
	case "xaver.clang-format":
		if p := look("clang-format"); p != "" {
			cmd = []string{p, "--assume-filename", path}
		}
	case "golang.go", "golang.go-nightly":
		return nil, false // gopls formats
	default:
		return nil, false
	}
	if cmd == nil {
		a.setStatusMessage("The formatter '" + id + "' is not installed; using the language server instead.")
		return nil, false
	}
	return cmd, true
}

// runExternalFormatter pipes text through a formatter command.
func runExternalFormatter(cmd []string, dir, text string) (string, error) {
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Dir = dir
	c.Stdin = strings.NewReader(text)
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(firstLine(msg))
	}
	return out.String(), nil
}

// lineDiffEdits turns the change from old to new into edits of whole lines.
func lineDiffEdits(old, new []string) []buffer.Edit {
	var edits []buffer.Edit
	for _, h := range git.Hunks(git.DiffLines(old, new)) {
		repl := strings.Join(new[h.BStart:h.BStart+h.BLen], "\n")
		end := h.AStart + h.ALen
		switch {
		case end < len(old):
			if h.BLen > 0 {
				repl += "\n"
			}
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: h.AStart}, End: buffer.Pos{Line: end}}, Text: repl})
		case h.AStart > 0:
			// the change reaches the end: take the newline before it along
			prev := h.AStart - 1
			if h.BLen > 0 {
				repl = "\n" + repl
			}
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: prev, Col: len([]rune(old[prev]))}, End: buffer.Pos{Line: len(old) - 1, Col: len([]rune(old[len(old)-1]))}}, Text: repl})
		default:
			edits = append(edits, buffer.Edit{Range: buffer.Range{End: buffer.Pos{Line: len(old) - 1, Col: len([]rune(old[len(old)-1]))}}, Text: repl})
		}
	}
	return edits
}

// formatExternal formats a document with a command line formatter.
func (a *App) formatExternal(v *EditorView, cmd []string, done func()) {
	text := v.buf().Text()
	version := v.buf().Version()
	dir := filepath.Dir(v.buf().Path)
	go func() {
		formatted, err := runExternalFormatter(cmd, dir, text)
		a.Post(func() {
			defer func() {
				if done != nil {
					done()
				}
			}()
			if err != nil {
				a.setStatusMessage("Format failed: " + err.Error())
				return
			}
			if v.buf().Version() != version || formatted == text {
				return
			}
			edits := lineDiffEdits(strings.Split(text, "\n"), strings.Split(formatted, "\n"))
			if len(edits) == 0 {
				return
			}
			// selections follow the edits through the buffer listeners
			v.buf().CloseUndoGroup()
			v.buf().ApplyEdits(edits, v.sels, "")
			v.buf().CloseUndoGroup()
		})
	}()
}
