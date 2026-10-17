package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

type harness struct {
	t *testing.T
	a *App
	s tcell.SimulationScreen
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

// snapshot writes the screen as JSON for the PNG renderer in the scratchpad.
func (h *harness) snapshot(path string) {
	cells, w, ht := h.s.GetContents()
	type cj struct {
		R  string `json:"r"`
		FG string `json:"fg"`
		BG string `json:"bg"`
		B  bool   `json:"b,omitempty"`
		I  bool   `json:"i,omitempty"`
		U  bool   `json:"u,omitempty"`
		S  bool   `json:"s,omitempty"`
	}
	hex := func(c tcell.Color) string {
		r, g, b := c.RGB()
		if r < 0 {
			return ""
		}
		return fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}
	out := struct {
		W, H   int
		Cells  []cj
		CX, CY int
	}{W: w, H: ht}
	for _, c := range cells {
		fg, bg, attr := c.Style.Decompose()
		r := " "
		if len(c.Runes) > 0 {
			r = string(c.Runes)
		}
		out.Cells = append(out.Cells, cj{R: r, FG: hex(fg), BG: hex(bg), B: attr&tcell.AttrBold != 0, I: attr&tcell.AttrItalic != 0, U: c.Style.GetUnderlineStyle() != tcell.UnderlineStyleNone, S: attr&tcell.AttrStrikeThrough != 0})
	}
	if h.a.cursor.visible {
		out.CX, out.CY = h.a.cursor.x, h.a.cursor.y
	} else {
		out.CX, out.CY = -1, -1
	}
	data, _ := json.Marshal(out)
	_ = os.WriteFile(path, data, 0o644)
}
