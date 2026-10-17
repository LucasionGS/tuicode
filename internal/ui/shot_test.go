package ui

import (
	"os"
	"time"

	"github.com/gdamore/tcell/v2"
	"path/filepath"
	"testing"
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
