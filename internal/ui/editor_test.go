package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func tempWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// confine every write, git call and external program to the test folders
	resolved, _ := filepath.EvalSymlinks(filepath.Dir(dir))
	setSandbox(resolved)
	t.Cleanup(func() { setSandbox("") })
	return dir
}

func (h *harness) text() string {
	v := h.a.ActiveView()
	if v == nil {
		h.t.Fatal("no active editor")
	}
	return v.buf().Text()
}

func (h *harness) ctrl(r rune) { h.key(tcell.KeyRune, r, tcell.ModCtrl) }

func TestTypingAndUndo(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"main.go": "package main\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "main.go")}})
	h.key(tcell.KeyDown, 0, 0)
	h.typeText("func main(")
	if got := h.text(); got != "package main\nfunc main()" {
		t.Fatalf("autoclose: %q", got)
	}
	h.typeText(")")
	h.typeText(" {")
	h.key(tcell.KeyEnter, 0, 0)
	h.typeText("x := 1")
	want := "package main\nfunc main() {\n\tx := 1\n}"
	if got := h.text(); got != want {
		t.Fatalf("enter indent: %q", got)
	}
	h.ctrl('z')
	h.ctrl('z')
	if got := h.text(); strings.Contains(got, "x := 1") {
		t.Fatalf("undo did not revert: %q", got)
	}
	h.key(tcell.KeyRune, 'z', tcell.ModCtrl|tcell.ModShift)
	h.key(tcell.KeyRune, 'z', tcell.ModCtrl|tcell.ModShift)
	if got := h.text(); got != want {
		t.Fatalf("redo: %q", got)
	}
}

func TestMultiCursorAndLines(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "foo bar\nfoo baz\nqux foo\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "a.txt")}})
	h.ctrl('d')
	h.ctrl('d')
	h.ctrl('d')
	v := h.a.ActiveView()
	if len(v.sels) != 3 {
		t.Fatalf("ctrl+d: %d selections", len(v.sels))
	}
	h.typeText("X")
	if got := h.text(); got != "X bar\nX baz\nqux X\n" {
		t.Fatalf("multi type: %q", got)
	}
	h.key(tcell.KeyEsc, 0, 0)
	if len(v.sels) != 1 {
		t.Fatalf("escape should leave one cursor, got %d", len(v.sels))
	}
	h.key(tcell.KeyUp, 0, tcell.ModAlt)
	if got := h.text(); got != "X bar\nqux X\nX baz\n" {
		t.Fatalf("alt+down: %q", got)
	}
	h.key(tcell.KeyRune, 'k', tcell.ModCtrl|tcell.ModShift)
	if got := h.text(); got != "X bar\nX baz\n" {
		t.Fatalf("ctrl+shift+k: %q", got)
	}
}

func TestCommentToggle(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.py": "x = 1\ny = 2\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "a.py")}})
	h.key(tcell.KeyDown, 0, tcell.ModShift)
	h.key(tcell.KeyEnd, 0, tcell.ModShift)
	h.a.handleEvent(tcell.NewEventKey(tcell.KeyCtrlUnderscore, 0, tcell.ModCtrl))
	h.flush()
	if got := h.text(); got != "# x = 1\n# y = 2\n" {
		t.Fatalf("comment: %q", got)
	}
	h.a.handleEvent(tcell.NewEventKey(tcell.KeyCtrlUnderscore, 0, tcell.ModCtrl))
	h.flush()
	if got := h.text(); got != "x = 1\ny = 2\n" {
		t.Fatalf("uncomment: %q", got)
	}
}

func TestPaletteAndQuickOpen(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"src/hello.ts": "export const a = 1;\n", "README.md": "# hi\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{dir}})
	h.key(tcell.KeyRune, 'p', tcell.ModCtrl|tcell.ModShift)
	if h.a.quick == nil {
		t.Fatal("palette did not open")
	}
	h.typeText("toggle word wrap")
	if !h.contains("View: Toggle Word Wrap") {
		h.dump()
		t.Fatal("command not listed")
	}
	h.key(tcell.KeyEsc, 0, 0)
	h.ctrl('p')
	time.Sleep(200 * time.Millisecond)
	h.flush()
	h.typeText("hello")
	time.Sleep(100 * time.Millisecond)
	h.flush()
	if !h.contains("hello.ts") {
		h.dump()
		t.Fatal("file not found in quick open")
	}
	h.key(tcell.KeyEnter, 0, 0)
	if v := h.a.ActiveView(); v == nil || !strings.HasSuffix(v.buf().Path, "hello.ts") {
		t.Fatal("file not opened")
	}
	if !h.contains("export") {
		h.dump()
		t.Fatal("content not shown")
	}
}

func TestFindReplace(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "one two one\nthree one\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "a.txt")}})
	h.ctrl('h')
	f := h.a.ActiveView().find
	if f == nil || len(f.matches) != 3 || f.input.Text() != "one" || f.focus != 1 {
		t.Fatalf("find seeded from word: %+v", f)
	}
	h.typeText("1")
	if !h.contains("1 of 3") {
		h.dump()
		t.Fatal("count not shown")
	}
	h.key(tcell.KeyEnter, 0, tcell.ModCtrl|tcell.ModAlt)
	if got := h.text(); got != "1 two 1\nthree 1\n" {
		h.dump()
		t.Fatalf("replace all: %q", got)
	}
}

func TestTerminal(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "x\n"})
	t.Setenv("SHELL", "/bin/sh")
	h := newHarness(t, 120, 40, Options{Paths: []string{dir}})
	h.ctrl('`')
	if len(h.a.terminals.terms) != 1 {
		t.Fatal("terminal not created")
	}
	h.typeText("echo tuicode-$((40+2))")
	h.key(tcell.KeyEnter, 0, 0)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		h.flush()
		if h.contains("tuicode-42") {
			return
		}
	}
	h.dump()
	t.Fatal("terminal output not shown")
}

func TestTerminalCtrlC(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "x\n"})
	t.Setenv("SHELL", "/bin/sh")
	h := newHarness(t, 120, 40, Options{Paths: []string{dir}})
	h.ctrl('`')
	h.typeText("sleep 30; echo not-interrupted")
	h.key(tcell.KeyEnter, 0, 0)
	time.Sleep(300 * time.Millisecond)
	// legacy terminals send ctrl+c as a control byte, kitty as ctrl-modified rune
	h.a.handleEvent(tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl))
	h.typeText("echo after-$((1+1))")
	h.key(tcell.KeyEnter, 0, 0)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		h.flush()
		if h.contains("after-2") {
			for _, l := range h.screen() {
				if strings.HasSuffix(strings.TrimSpace(strings.TrimRight(l, "│ ")), "▕ not-interrupted") || strings.TrimSpace(l) == "not-interrupted" {
					t.Fatal("ctrl+c did not interrupt")
				}
			}
			return
		}
	}
	h.dump()
	t.Fatal("ctrl+c did not reach the shell")
}

func TestAutoClose(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.ts": "\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "a.ts")}})
	h.typeText("foo(a, [1, 2]);")
	if got := h.text(); got != "foo(a, [1, 2]);\n" {
		t.Fatalf("overtype: %q", got)
	}
	h.key(tcell.KeyEnter, 0, 0)
	h.typeText(`const s = "x";`)
	if got := h.text(); got != "foo(a, [1, 2]);\nconst s = \"x\";\n" {
		t.Fatalf("quotes: %q", got)
	}
	h.key(tcell.KeyEnter, 0, 0)
	h.typeText("don't")
	if got := h.text(); !strings.HasSuffix(got, "don't\n") {
		t.Fatalf("apostrophe in word: %q", got)
	}
	// backspace inside an empty pair removes both characters
	h.key(tcell.KeyEnter, 0, 0)
	h.typeText("(")
	h.key(tcell.KeyBackspace2, 0, 0)
	if got := h.text(); !strings.HasSuffix(got, "don't\n\n") {
		t.Fatalf("pair delete: %q", got)
	}
	// typing a bracket around a selection wraps it
	h.typeText("word")
	h.key(tcell.KeyLeft, 0, tcell.ModCtrl|tcell.ModShift)
	h.typeText("(")
	if got := h.text(); !strings.HasSuffix(got, "(word)\n") {
		t.Fatalf("surround: %q", got)
	}
}

func TestRegionFolding(t *testing.T) {
	src := "package x\n\n//#region Helpers\nfunc a() {}\n\nfunc b() {}\n//#endregion\n\nfunc c() {}\n"
	dir := tempWorkspace(t, map[string]string{"x.go": src})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "x.go")}})
	v := h.a.ActiveView()
	f, ok := v.foldRangeAt(2)
	if !ok || f.End != 6 {
		t.Fatalf("region fold: %+v %v", f, ok)
	}
	v.SetCursor(buffer0(2))
	h.key(tcell.KeyRune, '[', tcell.ModCtrl|tcell.ModShift)
	if !v.isHidden(4) || v.isHidden(7) {
		t.Fatalf("folded lines wrong: %+v", v.folds)
	}
	if !h.contains("Helpers") || h.contains("func a()") {
		h.dump()
		t.Fatal("fold not rendered")
	}
}

func TestEmmet(t *testing.T) {
	nodes, ok := parseEmmet("ul.list>li.item$*2>a")
	if !ok {
		t.Fatal("parse failed")
	}
	got := expandEmmet(nodes, false)
	want := "<ul class=\"list\">\n\t<li class=\"item1\"><a href=\"$1\">$2</a></li>\n\t<li class=\"item2\"><a href=\"$3\">$4</a></li>\n</ul>$0"
	if got != want {
		t.Fatalf("expand:\n%s\nwant:\n%s", got, want)
	}
	dir := tempWorkspace(t, map[string]string{"index.html": "\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "index.html")}})
	h.typeText("div.box>p")
	v := h.a.ActiveView()
	if v.completion == nil || !v.completion.visible() || !v.completion.filtered[0].emmet {
		h.dump()
		t.Fatal("emmet suggestion missing")
	}
	h.key(tcell.KeyTab, 0, 0)
	if got := h.text(); got != "<div class=\"box\">\n    <p></p>\n</div>\n" {
		t.Fatalf("emmet insert: %q", got)
	}
}
