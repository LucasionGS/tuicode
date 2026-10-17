package term

import (
	"strings"
	"testing"
	"time"
)

func TestBasic(t *testing.T) {
	e := New(10, 3)
	e.Write([]byte("hello\r\nworld\x1b[1;31mX\x1b[0m\r\nabcdefghijkl"))
	if got := e.Text(0, 0, 2, 9); got != "worldX\nabcdefghijkl" {
		t.Fatalf("got %q", got)
	}
	if e.LineAt(0).Cells[5].FG != Indexed(1) {
		t.Fatalf("color not set")
	}
	// wrap should have scrolled
	if e.ScrollbackLen() != 1 {
		t.Fatalf("scrollback %d", e.ScrollbackLen())
	}
	e.Write([]byte("\x1b[2J\x1b[H\x1b[38;2;1;2;3mZ\x1b[38:2::4:5:6mY"))
	if e.LineAt(0).Cells[0].FG != RGB(1, 2, 3) || e.LineAt(0).Cells[1].FG != RGB(4, 5, 6) {
		t.Fatalf("truecolor %x %x", e.LineAt(0).Cells[0].FG, e.LineAt(0).Cells[1].FG)
	}
	e.Resize(5, 5)
	e.Resize(20, 2)
}

func TestShell(t *testing.T) {
	term, err := Start("/bin/bash", []string{"--norc", "-c", "printf 'hi\\033[32mgreen\\033[0m\\n'; sleep 0.2"}, "/", nil, 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	term.Lock()
	txt := term.Text(0, 0, 4, 39)
	term.Unlock()
	if !strings.Contains(txt, "higreen") {
		t.Fatalf("got %q", txt)
	}
}
