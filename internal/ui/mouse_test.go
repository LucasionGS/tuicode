package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func (h *harness) click(x, y int, btn tcell.ButtonMask, mods tcell.ModMask) {
	h.a.handleEvent(tcell.NewEventMouse(x, y, btn, mods))
	h.a.handleEvent(tcell.NewEventMouse(x, y, tcell.ButtonNone, mods))
	h.flush()
}

// find returns the screen position of the first occurrence of s.
func (h *harness) find(s string) (int, int) {
	for y, l := range h.screen() {
		if i := strings.Index(l, s); i >= 0 {
			return len([]rune(l[:i])), y
		}
	}
	h.dump()
	h.t.Fatalf("%q not on screen", s)
	return 0, 0
}

func TestMouse(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"one.txt": "hello world\nsecond line here\n", "two.txt": "other file\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{dir}})
	// open a file from the explorer
	x, y := h.find("one.txt")
	h.click(x, y, tcell.Button1, 0)
	v := h.a.ActiveView()
	if v == nil || !strings.HasSuffix(v.buf().Path, "one.txt") {
		t.Fatal("explorer click did not open file")
	}
	// click in text places the cursor
	x, y = h.find("second line")
	h.click(x+7, y, tcell.Button1, 0)
	if c := v.Cursor(); c.Line != 1 || c.Col != 7 {
		t.Fatalf("cursor after click: %+v", c)
	}
	// double click selects a word
	h.click(x+7, y, tcell.Button1, 0)
	if got := v.SelectedText(); got != "line" {
		t.Fatalf("double click selected %q", got)
	}
	// drag selection
	hx, hy := h.find("hello world")
	h.a.handleEvent(tcell.NewEventMouse(hx, hy, tcell.Button1, 0))
	h.a.handleEvent(tcell.NewEventMouse(hx+5, hy, tcell.Button1, 0))
	h.a.handleEvent(tcell.NewEventMouse(hx+5, hy, tcell.ButtonNone, 0))
	h.flush()
	if got := v.SelectedText(); got != "hello" {
		t.Fatalf("drag selected %q", got)
	}
	// alt+click adds a cursor
	h.click(hx+2, hy+1, tcell.Button1, tcell.ModAlt)
	if len(v.sels) != 2 {
		t.Fatalf("alt+click cursors: %d", len(v.sels))
	}
	// menu: open File, click New Text File
	mx, my := h.find("File")
	h.click(mx, my, tcell.Button1, 0)
	if h.a.menu.open != 0 {
		t.Fatal("menu did not open")
	}
	nx, ny := h.find("New Text File")
	h.click(nx, ny, tcell.Button1, 0)
	if v := h.a.ActiveView(); v == nil || v.buf().Path != "" {
		t.Fatal("new file not created from menu")
	}
	// tabs: click the first tab
	tx, ty := h.find("one.txt")
	h.click(tx, ty, tcell.Button1, 0)
	if v := h.a.ActiveView(); v == nil || filepath.Base(v.buf().Path) != "one.txt" {
		t.Fatal("tab click did not switch")
	}
}
