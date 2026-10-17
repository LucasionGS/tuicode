package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestPerfLargeFile(t *testing.T) {
	if os.Getenv("TUI_PERF") == "" {
		t.Skip("set TUI_PERF=1")
	}
	src, _ := os.ReadFile("editor_draw.go")
	big := strings.Repeat(string(src), 100)
	dir := tempWorkspace(t, map[string]string{"big.go": big})
	start := time.Now()
	h := newHarness(t, 200, 60, Options{Paths: []string{filepath.Join(dir, "big.go")}})
	t.Logf("open+first draw: %v (%d lines)", time.Since(start), h.a.ActiveView().buf().LineCount())
	start = time.Now()
	for i := 0; i < 50; i++ {
		h.a.draw()
	}
	t.Logf("draw: %v per frame", time.Since(start)/50)
	start = time.Now()
	for i := 0; i < 100; i++ {
		h.a.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
		h.a.draw()
	}
	t.Logf("type+draw: %v per key", time.Since(start)/100)
	start = time.Now()
	h.key(tcell.KeyEnd, 0, tcell.ModCtrl)
	t.Logf("ctrl+end: %v", time.Since(start))
	start = time.Now()
	for i := 0; i < 50; i++ {
		h.key(tcell.KeyPgUp, 0, 0)
	}
	t.Logf("pageup: %v per key", time.Since(start)/50)
	h.a.RunCommand("editor.action.toggleWordWrap", nil)
	start = time.Now()
	for i := 0; i < 20; i++ {
		h.key(tcell.KeyPgUp, 0, 0)
	}
	t.Logf("pageup (wrap): %v per key", time.Since(start)/20)
	start = time.Now()
	hl := h.a.ActiveView().doc.hl
	hl.Highlight(h.a.ActiveView().buf().Lines())
	t.Logf("highlight all: %v", time.Since(start))
}

func TestPerfTerminalFlood(t *testing.T) {
	if os.Getenv("TUI_PERF") == "" {
		t.Skip("set TUI_PERF=1")
	}
	dir := tempWorkspace(t, map[string]string{"a.txt": "x\n"})
	t.Setenv("SHELL", "/bin/sh")
	h := newHarness(t, 160, 50, Options{Paths: []string{dir}})
	h.ctrl('`')
	h.typeText("seq 1 300000; echo DONE-$((1+1))")
	h.key(tcell.KeyEnter, 0, 0)
	start := time.Now()
	frames := 0
	for time.Since(start) < 30*time.Second {
		select {
		case fn := <-h.a.postCh:
			fn()
		case <-time.After(5 * time.Millisecond):
		}
		h.a.draw()
		frames++
		if h.contains("DONE-2") {
			break
		}
	}
	t.Logf("flood finished in %v with %d frames, %d goroutines", time.Since(start), frames, runtime.NumGoroutine())
	if !h.contains("DONE-2") {
		t.Fatal("flood did not finish")
	}
}
