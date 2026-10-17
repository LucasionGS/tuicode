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

func TestScreenshotFile(t *testing.T) {
	out := os.Getenv("TUI_SHOT")
	file := os.Getenv("TUI_FILE")
	if out == "" || file == "" {
		t.Skip("set TUI_SHOT and TUI_FILE")
	}
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t, 150, 42, Options{Paths: []string{file}})
	h.settle()
	if cmd := os.Getenv("TUI_CMD"); cmd != "" {
		h.a.RunCommand(cmd, nil)
		h.settle()
	}
	h.snapshot(filepath.Join(out, "file.json"))
}

func TestScreenshotIntelliSense(t *testing.T) {
	out := os.Getenv("TUI_SHOT")
	if out == "" || os.Getenv("TUI_LSP") == "" {
		t.Skip("set TUI_SHOT and TUI_LSP")
	}
	src := "package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc main() {\n\tname := \"world\"\n\tfmt.Println(strings.ToUpper(name))\n\t\n}\n"
	dir := tempWorkspace(t, map[string]string{"go.mod": "module demo\n\ngo 1.21\n", "main.go": src})
	t.Setenv("GOCACHE", filepath.Join(os.Getenv("REAL_HOME"), ".cache", "go-build"))
	t.Setenv("GOPATH", filepath.Join(os.Getenv("REAL_HOME"), "go"))
	t.Setenv("GOMODCACHE", filepath.Join(os.Getenv("REAL_HOME"), "go", "pkg", "mod"))
	setSandbox("")
	h := newHarness(t, 140, 40, Options{Paths: []string{filepath.Join(dir, "main.go")}})
	v := h.a.ActiveView()
	for i := 0; i < 80 && h.a.lsp.Client(v.buf()) == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	time.Sleep(time.Second)
	h.settle()
	// completion with documentation
	v.SetCursor(buffer.Pos{Line: 10, Col: 1})
	h.typeText("strings.Rep")
	for i := 0; i < 40 && (v.completion == nil || len(v.completion.filtered) == 0); i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	time.Sleep(500 * time.Millisecond)
	h.settle()
	h.snapshot(filepath.Join(out, "completion.json"))
	h.key(tcell.KeyEsc, 0, 0)
	// mouse hover over Println
	x, y := h.find("Println")
	h.a.handleEvent(tcell.NewEventMouse(x+2, y, tcell.ButtonNone, 0))
	for i := 0; i < 30 && v.hover == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	h.settle()
	h.snapshot(filepath.Join(out, "hover.json"))
	// signature help
	h.key(tcell.KeyEsc, 0, 0)
	v.SetCursor(buffer.Pos{Line: 10, Col: v.buf().LineLen(10)})
	h.typeText("eat")
	v.closeWidgets(false)
	h.typeText("(")
	for i := 0; i < 30 && v.sig == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	h.settle()
	h.snapshot(filepath.Join(out, "signature.json"))
	h.a.lsp.ShutdownAll()
}

func TestScreenshotPeek(t *testing.T) {
	out := os.Getenv("TUI_SHOT")
	if out == "" || os.Getenv("TUI_LSP") == "" {
		t.Skip("set TUI_SHOT and TUI_LSP")
	}
	main := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(greet(\"a\"))\n\tfmt.Println(greet(\"b\"))\n}\n"
	lib := "package main\n\n// greet builds a greeting.\nfunc greet(name string) string {\n\treturn \"hello \" + name\n}\n\nfunc other() string {\n\treturn greet(\"c\")\n}\n"
	dir := tempWorkspace(t, map[string]string{"go.mod": "module demo\n\ngo 1.21\n", "main.go": main, "lib.go": lib})
	t.Setenv("GOCACHE", filepath.Join(os.Getenv("REAL_HOME"), ".cache", "go-build"))
	t.Setenv("GOPATH", filepath.Join(os.Getenv("REAL_HOME"), "go"))
	setSandbox("")
	h := newHarness(t, 140, 40, Options{Paths: []string{filepath.Join(dir, "main.go")}})
	v := h.a.ActiveView()
	for i := 0; i < 80 && h.a.lsp.Client(v.buf()) == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	time.Sleep(time.Second)
	v.SetCursor(buffer.Pos{Line: 5, Col: 15})
	h.key(tcell.KeyF12, 0, tcell.ModShift)
	for i := 0; i < 40 && v.peek == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	h.settle()
	h.snapshot(filepath.Join(out, "peek.json"))
	if v.peek == nil || len(v.peek.items) != 4 {
		t.Fatalf("peek items: %+v", v.peek)
	}
	h.a.lsp.ShutdownAll()
}

func TestScreenshotGrid(t *testing.T) {
	out := os.Getenv("TUI_SHOT")
	if out == "" {
		t.Skip("set TUI_SHOT")
	}
	wd, _ := os.Getwd()
	root := filepath.Dir(filepath.Dir(wd))
	h := newHarness(t, 150, 42, Options{Paths: []string{filepath.Join(root, "internal/ui/grouplayout.go")}})
	h.a.RunCommand("workbench.action.editorLayoutTwoByTwoGrid", nil)
	h.a.activeGroup = 3
	h.a.OpenFile(filepath.Join(root, "internal/ui/tags.go"), false)
	h.settle()
	h.snapshot(filepath.Join(out, "grid.json"))
}
