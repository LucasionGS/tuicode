package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestDebugGo(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv not installed")
	}
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := 41\n\tx++\n\tname := \"tui\"\n\tfmt.Println(name, x)\n}\n"
	dir := tempWorkspace(t, map[string]string{"go.mod": "module dbg\n\ngo 1.21\n", "main.go": src})
	// delve builds the program, so it needs the real Go caches
	t.Setenv("GOCACHE", filepath.Join(os.Getenv("REAL_HOME"), ".cache", "go-build"))
	t.Setenv("GOPATH", filepath.Join(os.Getenv("REAL_HOME"), "go"))
	t.Setenv("SHELL", "/bin/cat")
	setSandbox("")
	h := newHarness(t, 140, 40, Options{Paths: []string{filepath.Join(dir, "main.go")}})
	v := h.a.ActiveView()
	v.SetCursor(v.buf().Clamp(v.Cursor()))
	h.a.toggleBreakpoint(v.buf().Path, 6) // x++
	h.key(tcell.KeyF5, 0, 0)
	wait := func(cond func() bool, what string) {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			h.flush()
			if cond() {
				return
			}
		}
		h.dump()
		for _, l := range h.a.debugConsole {
			t.Log(l.text)
		}
		t.Fatalf("timeout waiting for %s", what)
	}
	wait(func() bool { return h.a.debug != nil && h.a.debug.state == "stopped" && len(h.a.debug.frames) > 0 }, "breakpoint")
	if l := h.a.debug.frames[h.a.debug.frameIdx].Line; l != 7 {
		t.Fatalf("stopped at line %d", l)
	}
	wait(func() bool { return h.contains("x: 41") }, "variables")
	h.key(tcell.KeyF10, 0, 0)
	wait(func() bool {
		return h.a.debug.state == "stopped" && len(h.a.debug.frames) > 0 && h.a.debug.frames[h.a.debug.frameIdx].Line == 8
	}, "step over")
	wait(func() bool { return h.contains("x: 42") }, "updated variable")
	if out := os.Getenv("TUI_SHOT"); out != "" {
		h.a.consoleAppend("input", "> name")
		h.a.consoleAppend("result", "\"tui\"")
		h.flush()
		h.snapshot(filepath.Join(out, "debug.json"))
	}
	// evaluate in the debug console
	var result string
	h.a.debug.evaluate("x * 2", "repl", func(r string, err error) { result = r })
	wait(func() bool { return result != "" }, "evaluate")
	if !strings.Contains(result, "84") {
		t.Fatalf("evaluate: %q", result)
	}
	h.key(tcell.KeyF5, 0, tcell.ModShift)
	wait(func() bool { return h.a.debug.state == "terminated" }, "stop")
}
