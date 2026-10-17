package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
)

// Rect is a screen rectangle.
type Rect struct {
	X, Y, W, H int
}

// Contains reports whether (x, y) is inside the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Canvas draws into a tcell screen with clipping.
type Canvas struct {
	s    tcell.Screen
	clip Rect
}

func (c *Canvas) inClip(x, y int) bool { return c.clip.Contains(x, y) }

// Set draws one rune.
func (c *Canvas) Set(x, y int, r rune, st tcell.Style) {
	if !c.inClip(x, y) {
		return
	}
	c.s.SetContent(x, y, r, nil, st)
}

// Fill fills a rectangle with a rune.
func (c *Canvas) Fill(r Rect, ch rune, st tcell.Style) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			c.Set(x, y, ch, st)
		}
	}
}

// Text draws a string starting at x and returns the x after the last cell.
// It stops at maxX (exclusive).
func (c *Canvas) Text(x, y, maxX int, s string, st tcell.Style) int {
	for _, r := range s {
		if r == '\t' {
			r = ' '
		}
		w := runewidth.RuneWidth(r)
		if w == 0 {
			continue
		}
		if x+w > maxX {
			break
		}
		c.Set(x, y, r, st)
		if w == 2 {
			c.Set(x+1, y, ' ', st)
			// let the terminal draw the wide rune over both cells
			if c.inClip(x+1, y) {
				c.s.SetContent(x, y, r, nil, st)
			}
		}
		x += w
	}
	return x
}

// TextEllipsis draws text truncated with an ellipsis to fit width.
func (c *Canvas) TextEllipsis(x, y, width int, s string, st tcell.Style) int {
	if width <= 0 {
		return x
	}
	if runewidth.StringWidth(s) <= width {
		return c.Text(x, y, x+width, s, st)
	}
	t := runewidth.Truncate(s, width, "…")
	return c.Text(x, y, x+width, t, st)
}

// TextRight draws text right-aligned ending at x+width.
func (c *Canvas) TextRight(x, y, width int, s string, st tcell.Style) {
	w := runewidth.StringWidth(s)
	if w > width {
		s = runewidth.Truncate(s, width, "…")
		w = runewidth.StringWidth(s)
	}
	c.Text(x+width-w, y, x+width, s, st)
}

// TextCenter draws text centered within width.
func (c *Canvas) TextCenter(x, y, width int, s string, st tcell.Style) {
	w := runewidth.StringWidth(s)
	if w > width {
		c.TextEllipsis(x, y, width, s, st)
		return
	}
	c.Text(x+(width-w)/2, y, x+width, s, st)
}

// Box draws a single-line border around r.
func (c *Canvas) Box(r Rect, st tcell.Style) {
	if r.W < 2 || r.H < 2 {
		return
	}
	for x := r.X + 1; x < r.X+r.W-1; x++ {
		c.Set(x, r.Y, '─', st)
		c.Set(x, r.Y+r.H-1, '─', st)
	}
	for y := r.Y + 1; y < r.Y+r.H-1; y++ {
		c.Set(r.X, y, '│', st)
		c.Set(r.X+r.W-1, y, '│', st)
	}
	c.Set(r.X, r.Y, '╭', st)
	c.Set(r.X+r.W-1, r.Y, '╮', st)
	c.Set(r.X, r.Y+r.H-1, '╰', st)
	c.Set(r.X+r.W-1, r.Y+r.H-1, '╯', st)
}

// Highlighted draws text where runes at the given indices get a different style.
func (c *Canvas) Highlighted(x, y, maxX int, s string, st, hl tcell.Style, positions []int) int {
	set := map[int]bool{}
	for _, p := range positions {
		set[p] = true
	}
	i := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			i++
			continue
		}
		if x+w > maxX {
			if x < maxX {
				c.Set(x, y, '…', st)
			}
			return maxX
		}
		style := st
		if set[i] {
			style = hl
		}
		c.Set(x, y, r, style)
		x += w
		i++
	}
	return x
}

// StrWidth returns the display width of s.
func StrWidth(s string) int { return runewidth.StringWidth(s) }

// RuneWidth returns the display width of r (tabs count as 1 here).
func RuneWidth(r rune) int {
	w := runewidth.RuneWidth(r)
	if w == 0 && r != 0 {
		return 1
	}
	return w
}

// padRight pads s with spaces to width.
func padRight(s string, w int) string {
	n := StrWidth(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// styleFG returns a style with only a foreground and background.
func styleFB(fg, bg tcell.Color) tcell.Style {
	return tcell.StyleDefault.Foreground(fg).Background(bg)
}

// bgOf returns the background color of a style.
func bgOf(st tcell.Style) tcell.Color {
	_, bg, _ := st.Decompose()
	return bg
}
