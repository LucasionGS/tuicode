package ui

import (
	"os"
	"time"

	"github.com/gdamore/tcell/v2"

	"path/filepath"
	"testing"
	"tuicode/internal/buffer"
	"tuicode/internal/theme"
)

// TestScreenshot writes snapshots when TUI_SHOT is set to an output folder.
func TestScreenshot(t *testing.T) {
	out := os.Getenv("TUI_SHOT")
	if out == "" {
		t.Skip("set TUI_SHOT")
	}
	wd, _ := os.Getwd()
	root := filepath.Dir(filepath.Dir(wd))
	h := newHarness(t, 150, 42, Options{Paths: []string{filepath.Join(root, "internal/ui/app.go")}})
	h.a.refreshGit()
	h.settle()
	h.snapshot(filepath.Join(out, "main.json"))
}

// settle waits for background work (highlighting, git) and redraws.
func (h *harness) settle() {
	for i := 0; i < 10; i++ {
		time.Sleep(30 * time.Millisecond)
		h.flush()
	}
}

func TestScreenshotStates(t *testing.T) {
	out := os.Getenv("TUI_SHOT")
	if out == "" {
		t.Skip("set TUI_SHOT")
	}
	wd, _ := os.Getwd()
	root := filepath.Dir(filepath.Dir(wd))
	t.Setenv("HOME", t.TempDir())
	_ = os.Chdir(root)
	defer os.Chdir(wd)
	h := newHarness(t, 150, 42, Options{Paths: []string{root}})
	h.settle()
	h.snapshot(filepath.Join(out, "welcome.json"))
	h.a.OpenFile(filepath.Join(root, "internal/buffer/buffer.go"), false)
	h.a.focusEditor()
	h.settle()
	h.key(tcell.KeyRune, 'p', tcell.ModCtrl|tcell.ModShift)
	h.typeText("toggle")
	h.settle()
	h.snapshot(filepath.Join(out, "palette.json"))
	h.key(tcell.KeyEsc, 0, 0)
	h.ctrl('f')
	h.typeText("lines")
	h.settle()
	h.snapshot(filepath.Join(out, "find.json"))
	h.key(tcell.KeyEsc, 0, 0)
	h.key(tcell.KeyRune, '`', tcell.ModCtrl)
	h.typeText("ls --color=always")
	h.key(tcell.KeyEnter, 0, 0)
	time.Sleep(500 * time.Millisecond)
	h.settle()
	h.snapshot(filepath.Join(out, "terminal.json"))
	h.key(tcell.KeyRune, 'f', tcell.ModCtrl|tcell.ModShift)
	h.typeText("ApplyEdits")
	h.key(tcell.KeyEnter, 0, 0)
	time.Sleep(500 * time.Millisecond)
	h.settle()
	h.snapshot(filepath.Join(out, "search.json"))
	h.a.RunCommand("workbench.view.scm", nil)
	h.settle()
	h.snapshot(filepath.Join(out, "scm.json"))
}

func TestScreenshotLSP(t *testing.T) {
	out := os.Getenv("TUI_SHOT")
	if out == "" || os.Getenv("TUI_LSP") == "" {
		t.Skip("set TUI_SHOT and TUI_LSP")
	}
	wd, _ := os.Getwd()
	root := filepath.Dir(filepath.Dir(wd))
	_ = os.Chdir(root)
	defer os.Chdir(wd)
	h := newHarness(t, 150, 42, Options{Paths: []string{filepath.Join(root, "internal/buffer/buffer.go")}})
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
		if v := h.a.ActiveView(); v != nil && v.doc.semSpans != nil {
			break
		}
	}
	h.settle()
	v := h.a.ActiveView()
	v.SetCursor(v.buf().Clamp(buffer.Pos{Line: 95, Col: 10}))
	v.ensurePosVisible(v.Cursor(), true)
	h.settle()
	h.snapshot(filepath.Join(out, "lsp.json"))
	h.a.lsp.ShutdownAll()
}

func TestScreenshotPages(t *testing.T) {
	out := os.Getenv("TUI_SHOT")
	if out == "" {
		t.Skip("set TUI_SHOT")
	}
	wd, _ := os.Getwd()
	root := filepath.Dir(filepath.Dir(wd))
	t.Setenv("HOME", t.TempDir())
	_ = os.Chdir(root)
	defer os.Chdir(wd)
	h := newHarness(t, 150, 42, Options{Paths: []string{root}})
	h.a.RunCommand("workbench.action.openSettings", nil)
	h.typeText("minimap")
	h.settle()
	h.snapshot(filepath.Join(out, "settings.json"))
	h.a.RunCommand("workbench.action.openGlobalKeybindings", nil)
	h.typeText("comment")
	h.settle()
	h.snapshot(filepath.Join(out, "keybindings.json"))
	h.a.closeAllEditors()
	h.a.OpenFile(filepath.Join(root, "internal/ui/app.go"), false)
	h.settle()
	v := h.a.ActiveView()
	v.SetCursor(buffer.Pos{Line: 300})
	v.ensurePosVisible(v.Cursor(), true)
	h.a.RunCommand("workbench.action.splitEditor", nil)
	h.a.OpenFile(filepath.Join(root, "internal/ui/keymap.go"), false)
	h.settle()
	h.snapshot(filepath.Join(out, "split.json"))
	h.a.joinAllGroups()
	left := "package main\n\nfunc a() {\n\tprintln(1)\n\tprintln(2)\n}\n\nfunc b() {}\n"
	right := "package main\n\nfunc a() {\n\tprintln(1)\n\tprintln(3)\n\tprintln(4)\n}\n\nfunc c() {}\n"
	h.a.openDiffPage("x.go ↔ y.go", filepath.Join(root, "x.go"), left, right)
	h.settle()
	h.snapshot(filepath.Join(out, "diff.json"))
	h.a.closeAllEditors()
	h.a.OpenFile(filepath.Join(root, "internal/ui/app.go"), false)
	th, _ := theme.Load("Light Modern")
	h.a.theme = th
	h.a.showPanel("problems")
	h.settle()
	h.snapshot(filepath.Join(out, "light.json"))
}
