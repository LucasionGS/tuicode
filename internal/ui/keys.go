package ui

import (
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

var specialKeys = map[tcell.Key]string{
	tcell.KeyEnter: "enter", tcell.KeyEsc: "escape", tcell.KeyTab: "tab", tcell.KeyBacktab: "tab",
	tcell.KeyBackspace: "backspace", tcell.KeyBackspace2: "backspace", tcell.KeyDelete: "delete", tcell.KeyInsert: "insert",
	tcell.KeyHome: "home", tcell.KeyEnd: "end", tcell.KeyPgUp: "pageup", tcell.KeyPgDn: "pagedown",
	tcell.KeyUp: "up", tcell.KeyDown: "down", tcell.KeyLeft: "left", tcell.KeyRight: "right",
	tcell.KeyF1: "f1", tcell.KeyF2: "f2", tcell.KeyF3: "f3", tcell.KeyF4: "f4", tcell.KeyF5: "f5", tcell.KeyF6: "f6",
	tcell.KeyF7: "f7", tcell.KeyF8: "f8", tcell.KeyF9: "f9", tcell.KeyF10: "f10", tcell.KeyF11: "f11", tcell.KeyF12: "f12",
	tcell.KeyF13: "f13", tcell.KeyF14: "f14", tcell.KeyF15: "f15", tcell.KeyF16: "f16", tcell.KeyF17: "f17",
	tcell.KeyF18: "f18", tcell.KeyF19: "f19", tcell.KeyF20: "f20", tcell.KeyF21: "f21", tcell.KeyF22: "f22",
	tcell.KeyF23: "f23", tcell.KeyF24: "f24",
}

// shiftedSymbols maps shifted US layout symbols back to their base key.
var shiftedSymbols = map[rune]rune{
	'!': '1', '@': '2', '#': '3', '$': '4', '%': '5', '^': '6', '&': '7', '*': '8', '(': '9', ')': '0',
	'_': '-', '+': '=', '{': '[', '}': ']', '|': '\\', ':': ';', '"': '\'', '<': ',', '>': '.', '?': '/', '~': '`',
}

// KeyString converts a key event into a VS Code style key string, e.g. "ctrl+shift+p".
// The second result is true when the event is plain text input.
func KeyString(ev *tcell.EventKey) (string, bool) {
	mod := ev.Modifiers()
	k := ev.Key()
	r := ev.Rune()
	ctrl := mod&tcell.ModCtrl != 0
	shift := mod&tcell.ModShift != 0
	alt := mod&tcell.ModAlt != 0
	meta := mod&tcell.ModMeta != 0

	var key string
	switch {
	case k == tcell.KeyRune:
		if r == ' ' {
			key = "space"
			if !ctrl && !alt && !meta {
				return "space", true
			}
			break
		}
		if !ctrl && !alt && !meta {
			return string(r), true
		}
		if unicode.IsUpper(r) {
			shift = true
			r = unicode.ToLower(r)
		}
		if base, ok := shiftedSymbols[r]; ok && (ctrl || alt) {
			// ctrl+? arrives as ctrl+shift+/ on most layouts; keep the symbol when shift is not reported
			if shift {
				r = base
			}
		}
		key = string(r)
	case k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ && r != 0 && unicode.IsLetter(r):
		ctrl = true
		if unicode.IsUpper(r) {
			shift = true
		}
		key = string(unicode.ToLower(r))
	case k == tcell.KeyBacktab:
		key = "tab"
		shift = true
	case k == tcell.KeyCtrlSpace:
		ctrl = true
		key = "space"
	case k == tcell.KeyCtrlBackslash:
		ctrl = true
		key = "\\"
	case k == tcell.KeyCtrlRightSq:
		ctrl = true
		key = "]"
	case k == tcell.KeyCtrlUnderscore:
		// legacy terminals send ctrl+/ as ^_
		ctrl = true
		key = "/"
	case k == tcell.KeyCtrlCarat:
		ctrl = true
		key = "6"
	default:
		if name, ok := specialKeys[k]; ok {
			key = name
		} else if k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ {
			ctrl = true
			key = string(rune('a' + int(k-tcell.KeyCtrlA)))
		} else {
			return "", false
		}
	}
	var sb strings.Builder
	if ctrl {
		sb.WriteString("ctrl+")
	}
	if shift {
		sb.WriteString("shift+")
	}
	if alt {
		sb.WriteString("alt+")
	}
	if meta {
		sb.WriteString("meta+")
	}
	sb.WriteString(key)
	return sb.String(), false
}
