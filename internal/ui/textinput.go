package ui

import (
	"github.com/gdamore/tcell/v2"
)

// TextInput is a single-line text field.
type TextInput struct {
	app         *App
	text        []rune
	cursor      int
	anchor      int
	scroll      int
	Placeholder string
	OnChange    func()
	history     []string
	histIdx     int
	undo        []string
}

func newTextInput(a *App, placeholder string) TextInput {
	return TextInput{app: a, Placeholder: placeholder, anchor: -1}
}

// Text returns the content.
func (t *TextInput) Text() string { return string(t.text) }

// SetText replaces the content and moves the cursor to the end.
func (t *TextInput) SetText(s string) {
	t.text = []rune(s)
	t.cursor = len(t.text)
	t.anchor = -1
}

// SelectAll selects the whole text.
func (t *TextInput) SelectAll() {
	t.anchor = 0
	t.cursor = len(t.text)
}

func (t *TextInput) selRange() (int, int, bool) {
	if t.anchor < 0 || t.anchor == t.cursor {
		return t.cursor, t.cursor, false
	}
	if t.anchor < t.cursor {
		return t.anchor, t.cursor, true
	}
	return t.cursor, t.anchor, true
}

func (t *TextInput) deleteSel() bool {
	s, e, ok := t.selRange()
	if !ok {
		return false
	}
	t.text = append(t.text[:s:s], t.text[e:]...)
	t.cursor = s
	t.anchor = -1
	return true
}

// Insert inserts text at the cursor, replacing the selection.
func (t *TextInput) Insert(s string) {
	t.pushUndo()
	t.deleteSel()
	rs := []rune(s)
	nt := make([]rune, 0, len(t.text)+len(rs))
	nt = append(nt, t.text[:t.cursor]...)
	nt = append(nt, rs...)
	nt = append(nt, t.text[t.cursor:]...)
	t.text = nt
	t.cursor += len(rs)
}

func (t *TextInput) pushUndo() {
	cur := string(t.text)
	if n := len(t.undo); n > 0 && t.undo[n-1] == cur {
		return
	}
	t.undo = append(t.undo, cur)
	if len(t.undo) > 100 {
		t.undo = t.undo[1:]
	}
}

func (t *TextInput) wordLeft(i int) int {
	for i > 0 && classOf(t.text[i-1]) == ccSpace {
		i--
	}
	if i == 0 {
		return 0
	}
	c := classOf(t.text[i-1])
	for i > 0 && classOf(t.text[i-1]) == c {
		i--
	}
	return i
}

func (t *TextInput) wordRight(i int) int {
	n := len(t.text)
	for i < n && classOf(t.text[i]) == ccSpace {
		i++
	}
	if i == n {
		return n
	}
	c := classOf(t.text[i])
	for i < n && classOf(t.text[i]) == c {
		i++
	}
	return i
}

func (t *TextInput) move(to int, extend bool) {
	if extend {
		if t.anchor < 0 {
			t.anchor = t.cursor
		}
	} else {
		t.anchor = -1
	}
	t.cursor = clampInt(to, 0, len(t.text))
}

// HandleKey processes editing keys. It returns false for keys it does not use
// (enter, escape, up, down...) so the owner can handle them.
func (t *TextInput) HandleKey(key, text string, isText bool) bool {
	changed := false
	switch key {
	case "left", "shift+left":
		s, _, ok := t.selRange()
		if ok && key == "left" {
			t.move(s, false)
		} else {
			t.move(t.cursor-1, key == "shift+left")
		}
	case "right", "shift+right":
		_, e, ok := t.selRange()
		if ok && key == "right" {
			t.move(e, false)
		} else {
			t.move(t.cursor+1, key == "shift+right")
		}
	case "ctrl+left", "ctrl+shift+left", "alt+left":
		t.move(t.wordLeft(t.cursor), key == "ctrl+shift+left")
	case "ctrl+right", "ctrl+shift+right", "alt+right":
		t.move(t.wordRight(t.cursor), key == "ctrl+shift+right")
	case "home", "shift+home":
		t.move(0, key == "shift+home")
	case "end", "shift+end":
		t.move(len(t.text), key == "shift+end")
	case "backspace", "shift+backspace":
		t.pushUndo()
		if !t.deleteSel() && t.cursor > 0 {
			t.text = append(t.text[:t.cursor-1], t.text[t.cursor:]...)
			t.cursor--
		}
		changed = true
	case "ctrl+backspace", "alt+backspace":
		t.pushUndo()
		if !t.deleteSel() {
			w := t.wordLeft(t.cursor)
			t.text = append(t.text[:w], t.text[t.cursor:]...)
			t.cursor = w
		}
		changed = true
	case "delete":
		t.pushUndo()
		if !t.deleteSel() && t.cursor < len(t.text) {
			t.text = append(t.text[:t.cursor], t.text[t.cursor+1:]...)
		}
		changed = true
	case "ctrl+delete":
		t.pushUndo()
		if !t.deleteSel() {
			w := t.wordRight(t.cursor)
			t.text = append(t.text[:t.cursor], t.text[w:]...)
		}
		changed = true
	case "ctrl+a":
		t.SelectAll()
	case "ctrl+c":
		if s, e, ok := t.selRange(); ok {
			t.app.setClipboard(string(t.text[s:e]))
		} else {
			return false
		}
	case "ctrl+x":
		if s, e, ok := t.selRange(); ok {
			t.app.setClipboard(string(t.text[s:e]))
			t.pushUndo()
			t.deleteSel()
			changed = true
		}
	case "ctrl+v", "ctrl+shift+v", "shift+insert":
		clip := t.app.getClipboard()
		for i, r := range clip {
			if r == '\n' || r == '\r' {
				clip = clip[:i]
				break
			}
		}
		t.Insert(clip)
		changed = true
	case "ctrl+z":
		if n := len(t.undo); n > 0 {
			t.text = []rune(t.undo[n-1])
			t.undo = t.undo[:n-1]
			t.cursor = len(t.text)
			t.anchor = -1
			changed = true
		}
	default:
		if !isText {
			return false
		}
		t.Insert(text)
		changed = true
	}
	if changed && t.OnChange != nil {
		t.OnChange()
	}
	return true
}

// AddHistory remembers a submitted value.
func (t *TextInput) AddHistory(s string) {
	if s == "" {
		return
	}
	for i, h := range t.history {
		if h == s {
			t.history = append(t.history[:i], t.history[i+1:]...)
			break
		}
	}
	t.history = append(t.history, s)
	t.histIdx = len(t.history)
}

// HistoryMove navigates the history (dir -1 = older).
func (t *TextInput) HistoryMove(dir int) bool {
	if len(t.history) == 0 {
		return false
	}
	t.histIdx = clampInt(t.histIdx+dir, 0, len(t.history))
	if t.histIdx == len(t.history) {
		t.SetText("")
	} else {
		t.SetText(t.history[t.histIdx])
	}
	if t.OnChange != nil {
		t.OnChange()
	}
	return true
}

// Draw renders the field and places the cursor if focused.
func (t *TextInput) Draw(x, y, w int, st, placeholderSt tcell.Style, focused bool) {
	a := t.app
	if w <= 0 {
		return
	}
	a.cv.Fill(Rect{x, y, w, 1}, ' ', st)
	if len(t.text) == 0 && t.Placeholder != "" {
		a.cv.TextEllipsis(x, y, w, t.Placeholder, placeholderSt)
	}
	// keep cursor in view
	cw := 0
	for i := 0; i < t.cursor && i < len(t.text); i++ {
		cw += RuneWidth(t.text[i])
	}
	if cw < t.scroll {
		t.scroll = cw
	}
	if cw-t.scroll >= w-1 {
		t.scroll = cw - w + 2
	}
	if t.scroll < 0 {
		t.scroll = 0
	}
	s, e, hasSel := t.selRange()
	selBg := a.theme.OverColor("editor.selectionBackground", bgOf(st))
	px := x - t.scroll
	for i, r := range t.text {
		rw := RuneWidth(r)
		if px >= x && px+rw <= x+w {
			cst := st
			if hasSel && i >= s && i < e {
				cst = cst.Background(selBg)
			}
			a.cv.Set(px, y, r, cst)
		}
		px += rw
	}
	if focused {
		a.setCursor(x+cw-t.scroll, y, tcell.CursorStyleBlinkingBar)
	}
	// clicking moves the cursor
	a.addHit(Rect{x, y, w, 1}, func(ev MouseEvent) bool {
		if ev.Kind != MousePress {
			return false
		}
		target := ev.X - x + t.scroll
		pos := 0
		acc := 0
		for i, r := range t.text {
			if acc+RuneWidth(r)/2 >= target {
				pos = i
				break
			}
			acc += RuneWidth(r)
			pos = i + 1
		}
		if ev.Clicks >= 2 {
			t.SelectAll()
		} else {
			t.move(pos, ev.Mods&tcell.ModShift != 0)
		}
		return false
	})
}
