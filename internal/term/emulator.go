package term

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Color encodes a terminal color. 0 is the default color, 1..256 are palette
// indices + 1 and values with ColorRGB set are 24-bit colors.
type Color uint32

const ColorRGB Color = 1 << 24

// RGB creates a 24-bit color.
func RGB(r, g, b uint8) Color { return ColorRGB | Color(r)<<16 | Color(g)<<8 | Color(b) }

// Indexed creates a palette color.
func Indexed(i int) Color { return Color(i + 1) }

// IsDefault reports whether c is the default color.
func (c Color) IsDefault() bool { return c == 0 }

// IsRGB reports whether c is a 24-bit color.
func (c Color) IsRGB() bool { return c&ColorRGB != 0 }

// Index returns the palette index of an indexed color.
func (c Color) Index() int { return int(c) - 1 }

// Components returns the 24-bit components.
func (c Color) Components() (uint8, uint8, uint8) {
	return uint8(c >> 16), uint8(c >> 8), uint8(c)
}

// Attr is a set of cell attributes.
type Attr uint16

const (
	AttrBold Attr = 1 << iota
	AttrDim
	AttrItalic
	AttrUnderline
	AttrBlink
	AttrReverse
	AttrHidden
	AttrStrike
	AttrCurly
	AttrDoubleUnderline
)

// Cell is a single character cell.
type Cell struct {
	Ch    rune
	FG    Color
	BG    Color
	Attr  Attr
	Width uint8 // 0 marks the right half of a wide character
}

// Line is a row of cells. Wrapped marks rows that continue onto the next row.
type Line struct {
	Cells   []Cell
	Wrapped bool
}

type cursorState struct {
	x, y     int
	fg, bg   Color
	attr     Attr
	origin   bool
	charsetG [2]bool
	gl       int
	wrapNext bool
}

// Emulator is a VT100/xterm compatible terminal state machine.
type Emulator struct {
	mu sync.Mutex

	cols, rows    int
	main          []Line
	alt           []Line
	altActive     bool
	scrollback    []Line
	MaxScrollback int

	x, y     int
	wrapNext bool
	fg, bg   Color
	attr     Attr

	savedMain, savedAlt cursorState

	top, bottom int

	// modes
	autowrap       bool
	origin         bool
	insert         bool
	AppCursor      bool
	AppKeypad      bool
	CursorVisible  bool
	BracketedPaste bool
	MouseMode      int // 0 off, 1000, 1002, 1003
	MouseSGR       bool
	FocusEvents    bool
	CursorStyle    int
	lnm            bool

	tabs []bool

	charsetG [2]bool // true = DEC special graphics
	gl       int

	Title string
	Cwd   string

	// parser
	state    int
	params   []int
	curParam int
	hasParam bool
	private  byte
	inter    []byte
	oscBuf   []byte
	utf8Buf  []byte
	lastRune rune

	// Reply sends data back to the program (device status replies etc.).
	Reply func([]byte)
	// Bell is called on BEL.
	Bell func()
	// OnTitle is called when the title changes.
	OnTitle func(string)
	// DefaultFG and DefaultBG are reported for OSC 10/11 queries.
	DefaultFG, DefaultBG [3]uint8

	// Dirty is set whenever visible content changes.
	Dirty bool

	pendingReplies [][]byte
	subParam       []bool
	nextIsSub      bool
}

const (
	stGround = iota
	stEscape
	stEscInter
	stCSI
	stOSC
	stOSCEsc
	stDCS
	stDCSEsc
	stCharset
)

// New creates an emulator with the given size.
func New(cols, rows int) *Emulator {
	e := &Emulator{MaxScrollback: 10000}
	e.cols, e.rows = max(cols, 1), max(rows, 1)
	e.reset()
	return e
}

func (e *Emulator) reset() {
	e.main = makeLines(e.cols, e.rows)
	e.alt = makeLines(e.cols, e.rows)
	e.altActive = false
	e.x, e.y = 0, 0
	e.wrapNext = false
	e.fg, e.bg, e.attr = 0, 0, 0
	e.top, e.bottom = 0, e.rows-1
	e.autowrap = true
	e.origin = false
	e.insert = false
	e.AppCursor = false
	e.AppKeypad = false
	e.CursorVisible = true
	e.BracketedPaste = false
	e.MouseMode = 0
	e.MouseSGR = false
	e.charsetG = [2]bool{}
	e.gl = 0
	e.resetTabs()
	e.Dirty = true
}

func (e *Emulator) resetTabs() {
	e.tabs = make([]bool, e.cols)
	for i := 8; i < e.cols; i += 8 {
		e.tabs[i] = true
	}
}

func makeLines(cols, rows int) []Line {
	ls := make([]Line, rows)
	for i := range ls {
		ls[i] = Line{Cells: blankCells(cols, 0)}
	}
	return ls
}

func blankCells(n int, bg Color) []Cell {
	c := make([]Cell, n)
	for i := range c {
		c[i] = Cell{Ch: ' ', BG: bg, Width: 1}
	}
	return c
}

// Lock locks the emulator for reading its state.
func (e *Emulator) Lock() { e.mu.Lock() }

// Unlock releases the lock.
func (e *Emulator) Unlock() { e.mu.Unlock() }

// Size returns the grid size.
func (e *Emulator) Size() (int, int) { return e.cols, e.rows }

// Cursor returns the cursor position.
func (e *Emulator) Cursor() (int, int) { return e.x, e.y }

// AltScreen reports whether the alternate screen is active.
func (e *Emulator) AltScreen() bool { return e.altActive }

func (e *Emulator) screen() []Line {
	if e.altActive {
		return e.alt
	}
	return e.main
}

// ScrollbackLen returns the number of scrollback lines.
func (e *Emulator) ScrollbackLen() int {
	if e.altActive {
		return 0
	}
	return len(e.scrollback)
}

// LineAt returns a row where negative indices address the scrollback
// (-1 is the most recent scrollback line).
func (e *Emulator) LineAt(y int) Line {
	if y < 0 {
		if e.altActive {
			return Line{}
		}
		i := len(e.scrollback) + y
		if i < 0 {
			return Line{}
		}
		return e.scrollback[i]
	}
	s := e.screen()
	if y >= len(s) {
		return Line{}
	}
	return s[y]
}

// Resize changes the grid size, moving lines to and from the scrollback.
func (e *Emulator) Resize(cols, rows int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cols, rows = max(cols, 1), max(rows, 1)
	if cols == e.cols && rows == e.rows {
		return
	}
	mainY := e.y
	if e.altActive {
		mainY = e.savedMain.y
	}
	var shift int
	e.main, shift = e.resizeScreen(e.main, cols, rows, mainY, true)
	if e.altActive {
		e.savedMain.y = clamp(e.savedMain.y-shift, 0, rows-1)
		var altShift int
		e.alt, altShift = e.resizeScreen(e.alt, cols, rows, e.y, false)
		e.y -= altShift
	} else {
		e.y -= shift
		e.alt = makeLines(cols, rows)
	}
	e.cols, e.rows = cols, rows
	e.x = clamp(e.x, 0, cols-1)
	e.y = clamp(e.y, 0, rows-1)
	e.top, e.bottom = 0, rows-1
	e.wrapNext = false
	oldTabs := e.tabs
	e.resetTabs()
	for i := 0; i < len(oldTabs) && i < cols; i++ {
		e.tabs[i] = oldTabs[i]
	}
	e.Dirty = true
}

// resizeScreen resizes a grid. It returns how many rows were removed from the top
// (negative when rows were pulled back in from the scrollback).
func (e *Emulator) resizeScreen(ls []Line, cols, rows, cursorY int, isMain bool) ([]Line, int) {
	for i := range ls {
		ls[i].Cells = resizeCells(ls[i].Cells, cols)
	}
	shift := 0
	if rows < len(ls) {
		excess := len(ls) - rows
		// trim blank rows below the cursor first
		for len(ls)-1 > cursorY && excess > 0 && lineBlank(ls[len(ls)-1]) {
			ls = ls[:len(ls)-1]
			excess--
		}
		if excess > 0 {
			if isMain {
				e.pushScrollback(ls[:excess]...)
			}
			ls = append([]Line{}, ls[excess:]...)
			shift = excess
		}
	} else if rows > len(ls) {
		add := rows - len(ls)
		if isMain {
			pull := min(add, len(e.scrollback))
			if pull > 0 {
				pulled := make([]Line, pull)
				copy(pulled, e.scrollback[len(e.scrollback)-pull:])
				for i := range pulled {
					pulled[i].Cells = resizeCells(pulled[i].Cells, cols)
				}
				e.scrollback = e.scrollback[:len(e.scrollback)-pull]
				ls = append(pulled, ls...)
				shift = -pull
				add -= pull
			}
		}
		for i := 0; i < add; i++ {
			ls = append(ls, Line{Cells: blankCells(cols, 0)})
		}
	}
	return ls, shift
}

func lineBlank(l Line) bool {
	for _, c := range l.Cells {
		if c.Ch != ' ' && c.Ch != 0 {
			return false
		}
		if c.BG != 0 {
			return false
		}
	}
	return true
}

func resizeCells(c []Cell, n int) []Cell {
	if len(c) == n {
		return c
	}
	if len(c) > n {
		c = c[:n]
		if n > 0 && c[n-1].Width == 2 {
			c[n-1] = Cell{Ch: ' ', Width: 1}
		}
		return c
	}
	return append(c, blankCells(n-len(c), 0)...)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (e *Emulator) pushScrollback(ls ...Line) {
	for _, l := range ls {
		cp := Line{Cells: append([]Cell{}, l.Cells...), Wrapped: l.Wrapped}
		e.scrollback = append(e.scrollback, cp)
	}
	if over := len(e.scrollback) - e.MaxScrollback; over > 0 {
		e.scrollback = append([]Line{}, e.scrollback[over:]...)
	}
}

// ClearScrollback removes all scrollback lines.
func (e *Emulator) ClearScrollback() {
	e.mu.Lock()
	e.scrollback = nil
	e.Dirty = true
	e.mu.Unlock()
}

// Write feeds program output into the emulator.
func (e *Emulator) Write(data []byte) (int, error) {
	n := e.write(data)
	e.mu.Lock()
	replies := e.pendingReplies
	e.pendingReplies = nil
	e.mu.Unlock()
	if e.Reply != nil {
		for _, r := range replies {
			e.Reply(r)
		}
	}
	return n, nil
}

func (e *Emulator) write(data []byte) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := len(data)
	if len(e.utf8Buf) > 0 {
		data = append(e.utf8Buf, data...)
		e.utf8Buf = nil
	}
	for len(data) > 0 {
		b := data[0]
		if b < 0x80 || e.state != stGround {
			e.feedByte(b)
			data = data[1:]
			continue
		}
		if !utf8.FullRune(data) {
			e.utf8Buf = append([]byte{}, data...)
			break
		}
		r, size := utf8.DecodeRune(data)
		data = data[size:]
		e.print(r)
	}
	e.Dirty = true
	return n
}

func (e *Emulator) feedByte(b byte) {
	switch e.state {
	case stGround:
		if b < 0x20 || b == 0x7f {
			e.control(b)
			return
		}
		e.print(rune(b))
	case stEscape:
		e.escape(b)
	case stEscInter:
		// ESC # 8 etc
		if b == '8' && len(e.inter) > 0 && e.inter[0] == '#' {
			// DECALN: fill screen with E
			s := e.screen()
			for i := range s {
				for j := range s[i].Cells {
					s[i].Cells[j] = Cell{Ch: 'E', Width: 1}
				}
			}
		}
		e.state = stGround
	case stCharset:
		g := 0
		if len(e.inter) > 0 && e.inter[0] == ')' {
			g = 1
		}
		e.charsetG[g] = b == '0'
		e.state = stGround
	case stCSI:
		e.csiByte(b)
	case stOSC:
		switch b {
		case 0x07:
			e.osc(string(e.oscBuf))
			e.state = stGround
		case 0x1b:
			e.state = stOSCEsc
		default:
			if len(e.oscBuf) < 1<<20 {
				e.oscBuf = append(e.oscBuf, b)
			}
		}
	case stOSCEsc:
		if b == '\\' {
			e.osc(string(e.oscBuf))
			e.state = stGround
		} else {
			e.oscBuf = append(e.oscBuf, 0x1b, b)
			e.state = stOSC
		}
	case stDCS:
		if b == 0x1b {
			e.state = stDCSEsc
		} else if b == 0x07 {
			e.state = stGround
		}
	case stDCSEsc:
		if b == '\\' {
			e.state = stGround
		} else {
			e.state = stDCS
		}
	}
}

func (e *Emulator) control(b byte) {
	switch b {
	case 0x07:
		if e.Bell != nil {
			e.Bell()
		}
	case 0x08:
		if e.x > 0 {
			e.x--
		}
		e.wrapNext = false
	case 0x09:
		e.tab(1)
	case 0x0a, 0x0b, 0x0c:
		e.lineFeed()
		if e.lnm {
			e.x = 0
		}
	case 0x0d:
		e.x = 0
		e.wrapNext = false
	case 0x0e:
		e.gl = 1
	case 0x0f:
		e.gl = 0
	case 0x1b:
		e.state = stEscape
		e.inter = e.inter[:0]
	}
}

func (e *Emulator) tab(n int) {
	for ; n > 0; n-- {
		x := e.x + 1
		for x < e.cols-1 && !e.tabs[x] {
			x++
		}
		e.x = min(x, e.cols-1)
	}
	e.wrapNext = false
}

func (e *Emulator) backTab(n int) {
	for ; n > 0; n-- {
		x := e.x - 1
		for x > 0 && !e.tabs[x] {
			x--
		}
		e.x = max(x, 0)
	}
}

func (e *Emulator) escape(b byte) {
	e.state = stGround
	switch b {
	case '[':
		e.state = stCSI
		e.params = e.params[:0]
		e.subParam = e.subParam[:0]
		e.nextIsSub = false
		e.curParam = 0
		e.hasParam = false
		e.private = 0
		e.inter = e.inter[:0]
	case ']':
		e.state = stOSC
		e.oscBuf = e.oscBuf[:0]
	case 'P', '_', '^', 'X':
		e.state = stDCS
	case '(', ')', '*', '+':
		e.inter = append(e.inter[:0], b)
		e.state = stCharset
	case '#', ' ', '%':
		e.inter = append(e.inter[:0], b)
		e.state = stEscInter
	case '7':
		e.saveCursor()
	case '8':
		e.restoreCursor()
	case 'D':
		e.lineFeed()
	case 'E':
		e.lineFeed()
		e.x = 0
	case 'H':
		if e.x < len(e.tabs) {
			e.tabs[e.x] = true
		}
	case 'M':
		e.reverseIndex()
	case 'c':
		e.reset()
	case '=':
		e.AppKeypad = true
	case '>':
		e.AppKeypad = false
	case '\\':
	}
}

func (e *Emulator) csiByte(b byte) {
	switch {
	case b >= '0' && b <= '9':
		e.curParam = e.curParam*10 + int(b-'0')
		if e.curParam > 65535 {
			e.curParam = 65535
		}
		e.hasParam = true
	case b == ';' || b == ':':
		e.params = append(e.params, paramOr(e.curParam, e.hasParam))
		e.subParam = append(e.subParam, e.nextIsSub)
		e.nextIsSub = b == ':'
		e.curParam = 0
		e.hasParam = false
	case b == '?' || b == '>' || b == '<' || b == '=':
		e.private = b
	case b >= 0x20 && b <= 0x2f:
		e.inter = append(e.inter, b)
	case b >= 0x40 && b <= 0x7e:
		if e.hasParam || len(e.params) > 0 {
			e.params = append(e.params, paramOr(e.curParam, e.hasParam))
			e.subParam = append(e.subParam, e.nextIsSub)
		}
		e.state = stGround
		e.csi(b)
	case b == 0x1b:
		e.state = stEscape
	case b < 0x20:
		e.control(b)
	}
}

func paramOr(v int, has bool) int {
	if !has {
		return -1
	}
	return v
}

func (e *Emulator) param(i, def int) int {
	if i < len(e.params) && e.params[i] > 0 {
		return e.params[i]
	}
	if i < len(e.params) && e.params[i] == 0 && def == 0 {
		return 0
	}
	return def
}

func (e *Emulator) csi(final byte) {
	if len(e.inter) > 0 {
		switch {
		case e.inter[0] == ' ' && final == 'q':
			e.CursorStyle = e.param(0, 0)
		case e.inter[0] == '!' && final == 'p':
			// soft reset
			e.CursorVisible = true
			e.origin = false
			e.autowrap = true
			e.insert = false
			e.top, e.bottom = 0, e.rows-1
			e.fg, e.bg, e.attr = 0, 0, 0
			e.AppCursor = false
		case e.inter[0] == '$' && final == 'p':
			// DECRQM: report mode as not recognized
			mode := e.param(0, 0)
			if e.private == '?' {
				e.reply(fmt.Sprintf("\x1b[?%d;0$y", mode))
			} else {
				e.reply(fmt.Sprintf("\x1b[%d;0$y", mode))
			}
		}
		return
	}
	if e.private == '>' || e.private == '<' || e.private == '=' {
		switch final {
		case 'c':
			if e.private == '>' {
				e.reply("\x1b[>0;10;1c")
			}
		case 'q':
			e.reply("\x1bP>|tuicode\x1b\\")
		}
		return
	}
	switch final {
	case '@':
		e.insertChars(e.param(0, 1))
	case 'A':
		e.moveCursor(e.x, e.y-e.param(0, 1), true)
	case 'B', 'e':
		e.moveCursor(e.x, e.y+e.param(0, 1), true)
	case 'C', 'a':
		e.moveCursor(e.x+e.param(0, 1), e.y, false)
	case 'D':
		e.moveCursor(e.x-e.param(0, 1), e.y, false)
	case 'E':
		e.moveCursor(0, e.y+e.param(0, 1), true)
	case 'F':
		e.moveCursor(0, e.y-e.param(0, 1), true)
	case 'G', '`':
		e.moveCursor(e.param(0, 1)-1, e.y, false)
	case 'H', 'f':
		y := e.param(0, 1) - 1
		x := e.param(1, 1) - 1
		if e.origin {
			y += e.top
		}
		e.x = clamp(x, 0, e.cols-1)
		if e.origin {
			e.y = clamp(y, e.top, e.bottom)
		} else {
			e.y = clamp(y, 0, e.rows-1)
		}
		e.wrapNext = false
	case 'I':
		e.tab(e.param(0, 1))
	case 'J':
		e.eraseDisplay(e.param(0, 0))
	case 'K':
		e.eraseLine(e.param(0, 0))
	case 'L':
		e.insertLines(e.param(0, 1))
	case 'M':
		e.deleteLines(e.param(0, 1))
	case 'P':
		e.deleteChars(e.param(0, 1))
	case 'S':
		if e.private == 0 {
			e.scrollUp(e.top, e.bottom, e.param(0, 1))
		}
	case 'T':
		e.scrollDown(e.top, e.bottom, e.param(0, 1))
	case 'X':
		n := e.param(0, 1)
		s := e.screen()
		for i := e.x; i < e.x+n && i < e.cols; i++ {
			s[e.y].Cells[i] = Cell{Ch: ' ', BG: e.bg, Width: 1}
		}
		e.wrapNext = false
	case 'Z':
		e.backTab(e.param(0, 1))
	case 'b':
		if e.lastRune != 0 {
			for i := 0; i < e.param(0, 1) && i < 65535; i++ {
				e.print(e.lastRune)
			}
		}
	case 'c':
		if e.private == 0 {
			e.reply("\x1b[?62;22c")
		}
	case 'd':
		y := e.param(0, 1) - 1
		if e.origin {
			y += e.top
		}
		e.y = clamp(y, 0, e.rows-1)
		e.wrapNext = false
	case 'g':
		switch e.param(0, 0) {
		case 0:
			if e.x < len(e.tabs) {
				e.tabs[e.x] = false
			}
		case 3:
			for i := range e.tabs {
				e.tabs[i] = false
			}
		}
	case 'h':
		e.setModes(true)
	case 'l':
		e.setModes(false)
	case 'm':
		if e.private == 0 {
			e.sgr()
		}
	case 'n':
		switch e.param(0, 0) {
		case 5:
			e.reply("\x1b[0n")
		case 6:
			y := e.y
			if e.origin {
				y -= e.top
			}
			if e.private == '?' {
				e.reply(fmt.Sprintf("\x1b[?%d;%dR", y+1, e.x+1))
			} else {
				e.reply(fmt.Sprintf("\x1b[%d;%dR", y+1, e.x+1))
			}
		}
	case 'r':
		if e.private == 0 {
			top := e.param(0, 1) - 1
			bot := e.param(1, e.rows) - 1
			top = clamp(top, 0, e.rows-1)
			bot = clamp(bot, 0, e.rows-1)
			if top < bot {
				e.top, e.bottom = top, bot
			}
			e.x = 0
			if e.origin {
				e.y = e.top
			} else {
				e.y = 0
			}
			e.wrapNext = false
		}
	case 's':
		if e.private == 0 {
			e.saveCursor()
		}
	case 'u':
		if e.private == 0 {
			e.restoreCursor()
		} else if e.private == '?' {
			// kitty keyboard query: report no flags
			e.reply("\x1b[?0u")
		}
	case 't':
		switch e.param(0, 0) {
		case 18:
			e.reply(fmt.Sprintf("\x1b[8;%d;%dt", e.rows, e.cols))
		}
	}
}

func (e *Emulator) reply(s string) {
	e.pendingReplies = append(e.pendingReplies, []byte(s))
}

func (e *Emulator) moveCursor(x, y int, vertical bool) {
	e.x = clamp(x, 0, e.cols-1)
	if vertical {
		top, bot := 0, e.rows-1
		if e.y >= e.top && e.y <= e.bottom {
			top, bot = e.top, e.bottom
		}
		e.y = clamp(y, top, bot)
	} else {
		e.y = clamp(y, 0, e.rows-1)
	}
	e.wrapNext = false
}

func (e *Emulator) setModes(on bool) {
	if len(e.params) == 0 {
		return
	}
	for _, p := range e.params {
		if e.private == '?' {
			switch p {
			case 1:
				e.AppCursor = on
			case 3:
				// DECCOLM: clear screen
				e.eraseDisplay(2)
				e.x, e.y = 0, 0
			case 5:
			case 6:
				e.origin = on
				e.x = 0
				if on {
					e.y = e.top
				} else {
					e.y = 0
				}
			case 7:
				e.autowrap = on
			case 12:
			case 25:
				e.CursorVisible = on
			case 47, 1047:
				e.setAlt(on, false)
			case 1049:
				if on {
					e.saveCursor()
					e.setAlt(true, true)
				} else {
					e.setAlt(false, false)
					e.restoreCursor()
				}
			case 1000, 1002, 1003:
				if on {
					e.MouseMode = p
				} else {
					e.MouseMode = 0
				}
			case 1004:
				e.FocusEvents = on
			case 1006:
				e.MouseSGR = on
			case 2004:
				e.BracketedPaste = on
			}
		} else {
			switch p {
			case 4:
				e.insert = on
			case 20:
				e.lnm = on
			}
		}
	}
}

func (e *Emulator) setAlt(on bool, clear bool) {
	if on == e.altActive {
		if on && clear {
			e.alt = makeLines(e.cols, e.rows)
		}
		return
	}
	e.altActive = on
	if on {
		e.alt = makeLines(e.cols, e.rows)
	}
	e.top, e.bottom = 0, e.rows-1
	e.wrapNext = false
}

func (e *Emulator) saveCursor() {
	st := cursorState{e.x, e.y, e.fg, e.bg, e.attr, e.origin, e.charsetG, e.gl, e.wrapNext}
	if e.altActive {
		e.savedAlt = st
	} else {
		e.savedMain = st
	}
}

func (e *Emulator) restoreCursor() {
	st := e.savedMain
	if e.altActive {
		st = e.savedAlt
	}
	e.x, e.y = clamp(st.x, 0, e.cols-1), clamp(st.y, 0, e.rows-1)
	e.fg, e.bg, e.attr = st.fg, st.bg, st.attr
	e.origin = st.origin
	e.charsetG = st.charsetG
	e.gl = st.gl
	e.wrapNext = st.wrapNext
}

var decGraphics = map[rune]rune{
	'`': '◆', 'a': '▒', 'b': '␉', 'c': '␌', 'd': '␍', 'e': '␊', 'f': '°', 'g': '±',
	'h': '␤', 'i': '␋', 'j': '┘', 'k': '┐', 'l': '┌', 'm': '└', 'n': '┼', 'o': '⎺',
	'p': '⎻', 'q': '─', 'r': '⎼', 's': '⎽', 't': '├', 'u': '┤', 'v': '┴', 'w': '┬',
	'x': '│', 'y': '≤', 'z': '≥', '{': 'π', '|': '≠', '}': '£', '~': '·',
}

func (e *Emulator) print(r rune) {
	if e.charsetG[e.gl] {
		if g, ok := decGraphics[r]; ok {
			r = g
		}
	}
	w := runewidth.RuneWidth(r)
	if w == 0 {
		// combining character: ignore (attach would need extra storage)
		return
	}
	e.lastRune = r
	s := e.screen()
	if e.wrapNext {
		if e.autowrap {
			s[e.y].Wrapped = true
			e.lineFeed()
			e.x = 0
			s = e.screen()
		}
		e.wrapNext = false
	}
	if w == 2 && e.x == e.cols-1 {
		if e.autowrap {
			s[e.y].Cells[e.x] = Cell{Ch: ' ', BG: e.bg, Width: 1}
			s[e.y].Wrapped = true
			e.lineFeed()
			e.x = 0
			s = e.screen()
		} else {
			return
		}
	}
	line := s[e.y].Cells
	if e.insert {
		copy(line[e.x+w:], line[e.x:])
	}
	// clear wide char halves we overwrite
	if line[e.x].Width == 0 && e.x > 0 {
		line[e.x-1] = Cell{Ch: ' ', BG: line[e.x-1].BG, Width: 1}
	}
	if line[e.x].Width == 2 && e.x+1 < e.cols {
		line[e.x+1] = Cell{Ch: ' ', BG: line[e.x+1].BG, Width: 1}
	}
	line[e.x] = Cell{Ch: r, FG: e.fg, BG: e.bg, Attr: e.attr, Width: uint8(w)}
	if w == 2 && e.x+1 < e.cols {
		line[e.x+1] = Cell{Ch: 0, FG: e.fg, BG: e.bg, Attr: e.attr, Width: 0}
	}
	if e.x+w >= e.cols {
		e.x = e.cols - 1
		e.wrapNext = true
	} else {
		e.x += w
	}
}

func (e *Emulator) lineFeed() {
	if e.y == e.bottom {
		e.scrollUp(e.top, e.bottom, 1)
	} else if e.y < e.rows-1 {
		e.y++
	}
	e.wrapNext = false
}

func (e *Emulator) reverseIndex() {
	if e.y == e.top {
		e.scrollDown(e.top, e.bottom, 1)
	} else if e.y > 0 {
		e.y--
	}
	e.wrapNext = false
}

func (e *Emulator) scrollUp(top, bot, n int) {
	s := e.screen()
	n = min(n, bot-top+1)
	if n <= 0 {
		return
	}
	if !e.altActive && top == 0 {
		e.pushScrollback(s[top : top+n]...)
	}
	copy(s[top:], s[top+n:bot+1])
	for i := bot - n + 1; i <= bot; i++ {
		s[i] = Line{Cells: blankCells(e.cols, e.bg)}
	}
}

func (e *Emulator) scrollDown(top, bot, n int) {
	s := e.screen()
	n = min(n, bot-top+1)
	if n <= 0 {
		return
	}
	copy(s[top+n:bot+1], s[top:bot+1-n])
	for i := top; i < top+n; i++ {
		s[i] = Line{Cells: blankCells(e.cols, e.bg)}
	}
}

func (e *Emulator) insertLines(n int) {
	if e.y < e.top || e.y > e.bottom {
		return
	}
	e.scrollDown(e.y, e.bottom, n)
	e.x = 0
	e.wrapNext = false
}

func (e *Emulator) deleteLines(n int) {
	if e.y < e.top || e.y > e.bottom {
		return
	}
	s := e.screen()
	n = min(n, e.bottom-e.y+1)
	copy(s[e.y:], s[e.y+n:e.bottom+1])
	for i := e.bottom - n + 1; i <= e.bottom; i++ {
		s[i] = Line{Cells: blankCells(e.cols, e.bg)}
	}
	e.x = 0
	e.wrapNext = false
}

func (e *Emulator) insertChars(n int) {
	line := e.screen()[e.y].Cells
	n = min(n, e.cols-e.x)
	copy(line[e.x+n:], line[e.x:])
	for i := e.x; i < e.x+n; i++ {
		line[i] = Cell{Ch: ' ', BG: e.bg, Width: 1}
	}
	e.wrapNext = false
}

func (e *Emulator) deleteChars(n int) {
	line := e.screen()[e.y].Cells
	n = min(n, e.cols-e.x)
	copy(line[e.x:], line[e.x+n:])
	for i := e.cols - n; i < e.cols; i++ {
		line[i] = Cell{Ch: ' ', BG: e.bg, Width: 1}
	}
	e.wrapNext = false
}

func (e *Emulator) eraseLine(mode int) {
	line := e.screen()[e.y].Cells
	from, to := 0, e.cols
	switch mode {
	case 0:
		from = e.x
	case 1:
		to = e.x + 1
	}
	for i := from; i < to && i < len(line); i++ {
		line[i] = Cell{Ch: ' ', BG: e.bg, Width: 1}
	}
	if mode != 1 {
		e.screen()[e.y].Wrapped = false
	}
	e.wrapNext = false
}

func (e *Emulator) eraseDisplay(mode int) {
	s := e.screen()
	switch mode {
	case 0:
		e.eraseLine(0)
		for i := e.y + 1; i < e.rows; i++ {
			s[i] = Line{Cells: blankCells(e.cols, e.bg)}
		}
	case 1:
		e.eraseLine(1)
		for i := 0; i < e.y; i++ {
			s[i] = Line{Cells: blankCells(e.cols, e.bg)}
		}
	case 2:
		// move content to scrollback like xterm does when clearing a used screen
		if !e.altActive {
			last := -1
			for i := len(s) - 1; i >= 0; i-- {
				if !lineBlank(s[i]) {
					last = i
					break
				}
			}
			if last >= 0 {
				e.pushScrollback(s[:last+1]...)
			}
		}
		for i := range s {
			s[i] = Line{Cells: blankCells(e.cols, e.bg)}
		}
	case 3:
		if !e.altActive {
			e.scrollback = nil
		}
	}
}

func (e *Emulator) sgr() {
	if len(e.params) == 0 {
		e.fg, e.bg, e.attr = 0, 0, 0
		return
	}
	for i := 0; i < len(e.params); i++ {
		p := max(e.params[i], 0)
		// colon separated sub-parameters belong to this parameter
		j := i + 1
		for j < len(e.params) && j < len(e.subParam) && e.subParam[j] {
			j++
		}
		subs := e.params[i+1 : j]
		switch {
		case p == 0:
			e.fg, e.bg, e.attr = 0, 0, 0
		case p == 1:
			e.attr |= AttrBold
		case p == 2:
			e.attr |= AttrDim
		case p == 3:
			e.attr |= AttrItalic
		case p == 4:
			e.attr &^= AttrUnderline | AttrCurly | AttrDoubleUnderline
			style := 1
			if len(subs) > 0 {
				style = subs[0]
			}
			switch style {
			case 0:
			case 2:
				e.attr |= AttrUnderline | AttrDoubleUnderline
			case 3, 4, 5:
				e.attr |= AttrUnderline | AttrCurly
			default:
				e.attr |= AttrUnderline
			}
		case p == 5 || p == 6:
			e.attr |= AttrBlink
		case p == 7:
			e.attr |= AttrReverse
		case p == 8:
			e.attr |= AttrHidden
		case p == 9:
			e.attr |= AttrStrike
		case p == 21:
			e.attr |= AttrUnderline | AttrDoubleUnderline
		case p == 22:
			e.attr &^= AttrBold | AttrDim
		case p == 23:
			e.attr &^= AttrItalic
		case p == 24:
			e.attr &^= AttrUnderline | AttrCurly | AttrDoubleUnderline
		case p == 25:
			e.attr &^= AttrBlink
		case p == 27:
			e.attr &^= AttrReverse
		case p == 28:
			e.attr &^= AttrHidden
		case p == 29:
			e.attr &^= AttrStrike
		case p >= 30 && p <= 37:
			e.fg = Indexed(p - 30)
		case p == 38 || p == 48 || p == 58:
			var c Color
			if len(subs) > 0 {
				c = colorFromParams(subs, true)
			} else {
				var adv int
				c, adv = e.extColor(i)
				j = i + 1 + adv
			}
			if p == 38 {
				e.fg = c
			} else if p == 48 {
				e.bg = c
			}
		case p == 39:
			e.fg = 0
		case p >= 40 && p <= 47:
			e.bg = Indexed(p - 40)
		case p == 49:
			e.bg = 0
		case p >= 90 && p <= 97:
			e.fg = Indexed(p - 90 + 8)
		case p >= 100 && p <= 107:
			e.bg = Indexed(p - 100 + 8)
		}
		i = j - 1
	}
}

// colorFromParams decodes "5;n" or "2;[cs;]r;g;b" color parameters.
func colorFromParams(ps []int, colon bool) Color {
	if len(ps) == 0 {
		return 0
	}
	switch ps[0] {
	case 5:
		if len(ps) > 1 {
			return Indexed(clamp(ps[1], 0, 255))
		}
	case 2:
		rest := ps[1:]
		if colon && len(rest) >= 4 {
			rest = rest[1:] // colorspace id
		}
		if len(rest) >= 3 {
			return RGB(uint8(clamp(rest[0], 0, 255)), uint8(clamp(rest[1], 0, 255)), uint8(clamp(rest[2], 0, 255)))
		}
	}
	return 0
}

func (e *Emulator) extColor(i int) (Color, int) {
	if i+1 >= len(e.params) {
		return 0, 0
	}
	switch e.params[i+1] {
	case 5:
		if i+2 < len(e.params) {
			return Indexed(clamp(e.params[i+2], 0, 255)), 2
		}
		return 0, 1
	case 2:
		if i+4 < len(e.params) {
			return colorFromParams(e.params[i+1:i+5], false), 4
		}
		return 0, len(e.params) - i - 1
	}
	return 0, 1
}

func (e *Emulator) osc(s string) {
	idx := strings.IndexByte(s, ';')
	if idx < 0 {
		return
	}
	cmd, arg := s[:idx], s[idx+1:]
	switch cmd {
	case "0", "2":
		e.Title = arg
		if e.OnTitle != nil {
			e.OnTitle(arg)
		}
	case "7":
		// file://host/path
		if strings.HasPrefix(arg, "file://") {
			rest := arg[7:]
			if j := strings.IndexByte(rest, '/'); j >= 0 {
				e.Cwd = rest[j:]
			}
		}
	case "10", "11":
		if arg == "?" {
			c := e.DefaultFG
			if cmd == "11" {
				c = e.DefaultBG
			}
			e.reply(fmt.Sprintf("\x1b]%s;rgb:%02x%02x/%02x%02x/%02x%02x\x1b\\", cmd, c[0], c[0], c[1], c[1], c[2], c[2]))
		}
	}
}

// Text returns the text of rows from y0 to y1 (inclusive, scrollback-relative indices allowed),
// joining wrapped rows.
func (e *Emulator) Text(y0, x0, y1, x1 int) string {
	var sb strings.Builder
	for y := y0; y <= y1; y++ {
		l := e.LineAt(y)
		from, to := 0, len(l.Cells)
		if y == y0 {
			from = x0
		}
		if y == y1 {
			to = min(x1+1, len(l.Cells))
		}
		var row strings.Builder
		for x := from; x < to && x < len(l.Cells); x++ {
			c := l.Cells[x]
			if c.Width == 0 {
				continue
			}
			row.WriteRune(c.Ch)
		}
		text := row.String()
		if !l.Wrapped || y == y1 {
			text = strings.TrimRight(text, " ")
		}
		sb.WriteString(text)
		if y != y1 && !l.Wrapped {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// XtermColor returns the RGB of a 256-color palette index (16..255).
func XtermColor(i int) (uint8, uint8, uint8) {
	if i < 16 {
		return 0, 0, 0
	}
	if i < 232 {
		i -= 16
		steps := []uint8{0, 95, 135, 175, 215, 255}
		return steps[i/36], steps[(i/6)%6], steps[i%6]
	}
	v := uint8(8 + (i-232)*10)
	return v, v, v
}

// ParseIntDefault is a small helper for callers parsing numeric strings.
func ParseIntDefault(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}
