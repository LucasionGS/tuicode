package ui

import (
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestRandomInput drives the workbench with random keys and mouse events to catch panics.
func TestRandomInput(t *testing.T) {
	n := 3000
	if v := os.Getenv("TUI_FUZZ"); v != "" {
		n, _ = strconv.Atoi(v)
	}
	dir := tempWorkspace(t, map[string]string{
		"main.go":       "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n\tif true {\n\t\treturn\n\t}\n}\n",
		"src/app.ts":    "export function add(a: number, b: number): number {\n  return a + b;\n}\n",
		"README.md":     "# Title\n\nSome *text* with `code`.\n",
		"data/x.json":   "{\"a\": [1, 2, {\"b\": null}]}\n",
		"wide/日本.txt": "日本語のテキスト\nemoji 😀 here\n\tTabbed\n",
	})
	t.Setenv("SHELL", "/bin/sh")
	h := newHarness(t, 100, 30, Options{Paths: []string{dir}})
	h.a.OpenFile(filepath.Join(dir, "main.go"), false)
	seed := int64(len(dir))
	if s := os.Getenv("TUI_SEED"); s != "" {
		seed, _ = strconv.ParseInt(s, 10, 64)
	}
	rng := rand.New(rand.NewSource(seed))
	keys := []tcell.Key{tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight, tcell.KeyHome, tcell.KeyEnd,
		tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyEnter, tcell.KeyBackspace2, tcell.KeyDelete, tcell.KeyTab, tcell.KeyBacktab,
		tcell.KeyEsc, tcell.KeyF1, tcell.KeyF2, tcell.KeyF3, tcell.KeyF8, tcell.KeyF12}
	runes := []rune("abcdefghijklmnopqrstuvwxyz (){}[]\"'`;:.,/\\-=+_<>?!日😀")
	ctrlRunes := []rune("abcdefgijklnoprstuvwxyz/\\[]`.,=-1234")
	mods := []tcell.ModMask{0, tcell.ModShift, tcell.ModCtrl, tcell.ModAlt, tcell.ModCtrl | tcell.ModShift, tcell.ModAlt | tcell.ModShift}
	for i := 0; i < n; i++ {
		var ev tcell.Event
		switch r := rng.Intn(10); {
		case r < 4:
			ev = tcell.NewEventKey(tcell.KeyRune, runes[rng.Intn(len(runes))], 0)
		case r < 6:
			ev = tcell.NewEventKey(keys[rng.Intn(len(keys))], 0, mods[rng.Intn(len(mods))])
		case r < 8:
			c := ctrlRunes[rng.Intn(len(ctrlRunes))]
			// never quit during the run
			if c == 'q' {
				c = 'z'
			}
			ev = tcell.NewEventKey(tcell.KeyRune, c, mods[2+rng.Intn(3)])
		default:
			x, y := rng.Intn(100), rng.Intn(30)
			btns := []tcell.ButtonMask{tcell.Button1, tcell.Button2, tcell.Button3, tcell.WheelUp, tcell.WheelDown, tcell.ButtonNone}
			b := btns[rng.Intn(len(btns))]
			h.a.handleEvent(tcell.NewEventMouse(x, y, b, mods[rng.Intn(2)]))
			if b == tcell.Button1 || b == tcell.Button2 || b == tcell.Button3 {
				ev = tcell.NewEventMouse(x+rng.Intn(3), y, tcell.ButtonNone, 0)
			}
		}
		if ev != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic at step %d on %#v: %v\n%s", i, ev, r, stackTrace())
					}
				}()
				h.a.handleEvent(ev)
				if h.a.quit {
					h.a.quit = false
				}
				if h.a.dialog != nil && rng.Intn(3) == 0 {
					h.a.dialog.finish(len(h.a.dialog.buttons) - 1)
				}
			}()
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic in draw at step %d: %v\n%s", i, r, stackTrace())
				}
			}()
			h.flush()
		}()
	}
	h.a.terminals.closeAll()
}

func stackTrace() string {
	buf := make([]byte, 1<<16)
	n := runtimeStack(buf)
	return string(buf[:n])
}
