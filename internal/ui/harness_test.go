package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

type harness struct {
	t   *testing.T
	a   *App
	s   tcell.SimulationScreen
}

func newHarness(t *testing.T, w, h int, opts Options) *harness {
	t.Helper()
	opts.NoLSP = true
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	a.attachScreen(s)
	hh := &harness{t: t, a: a, s: s}
	hh.flush()
	return hh
}

// flush runs posted callbacks and draws.
func (h *harness) flush() {
	for i := 0; i < 100; i++ {
		select {
		case fn := <-h.a.postCh:
			fn()
		default:
			i = 100
		}
	}
	h.a.draw()
}

func (h *harness) screen() []string {
	cells, w, ht := h.s.GetContents()
	var out []string
	for y := 0; y < ht; y++ {
		var sb strings.Builder
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				sb.WriteRune(c.Runes[0])
			} else {
				sb.WriteRune(' ')
			}
		}
		out = append(out, strings.TrimRight(sb.String(), " "))
	}
	return out
}

func (h *harness) dump() {
	h.t.Log("\n" + strings.Join(h.screen(), "\n"))
}

func (h *harness) key(k tcell.Key, r rune, m tcell.ModMask) {
	h.a.handleEvent(tcell.NewEventKey(k, r, m))
	h.flush()
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.a.handleEvent(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	h.flush()
}

func (h *harness) contains(s string) bool {
	for _, l := range h.screen() {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func TestStartupDump(t *testing.T) {
	if os.Getenv("TUI_DUMP") == "" {
		t.Skip("set TUI_DUMP=1")
	}
	h := newHarness(t, 140, 40, Options{Paths: []string{"app.go"}})
	h.dump()
}
