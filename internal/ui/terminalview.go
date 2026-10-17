package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/term"
)

// TermInstance is one integrated terminal.
type TermInstance struct {
	t         *term.Terminal
	name      string
	custom    bool
	scrollOff int
	rect      Rect
	procName  string
	procCheck time.Time
	task      *taskRun // set when the terminal runs a task
	id        int
	// selection in absolute rows (negative = scrollback)
	selActive bool
	selStart  [2]int
	selEnd    [2]int
}

// TermGroup holds terminals shown side by side.
type TermGroup struct {
	terms  []*TermInstance
	active int
}

// TerminalPanel manages integrated terminals.
type TerminalPanel struct {
	app    *App
	groups []*TermGroup
	active int
	nextID int
	terms  []*TermInstance // flat list for quick checks
	rect   Rect

	// outputPending coalesces redraw requests from busy terminals
	outputPending atomic.Bool

	findOpen    bool
	findInput   TextInput
	findMatches []termMatch
	findIdx     int
}

type termMatch struct {
	row, col, length int // row is absolute: negative rows are in the scrollback
}

func newTerminalPanel(a *App) *TerminalPanel {
	return &TerminalPanel{app: a}
}

func (tp *TerminalPanel) shell() (string, []string) {
	a := tp.app
	if s := a.settings.String("terminal.integrated.shell.linux", ""); s != "" {
		return s, nil
	}
	prof := a.settings.String("terminal.integrated.defaultProfile.linux", "")
	if prof != "" {
		if profiles, ok := a.settings.Get("terminal.integrated.profiles.linux", "").(map[string]any); ok {
			if p, ok := profiles[prof].(map[string]any); ok {
				path, _ := p["path"].(string)
				var args []string
				if arr, ok := p["args"].([]any); ok {
					for _, x := range arr {
						if s, ok := x.(string); ok {
							args = append(args, s)
						}
					}
				}
				if path != "" {
					return path, args
				}
			}
		}
		if p, err := exec.LookPath(prof); err == nil {
			return p, nil
		}
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s, nil
	}
	for _, s := range []string{"bash", "zsh", "sh"} {
		if p, err := exec.LookPath(s); err == nil {
			return p, nil
		}
	}
	return "/bin/sh", nil
}

func (tp *TerminalPanel) rebuildFlat() {
	tp.terms = tp.terms[:0]
	for _, g := range tp.groups {
		tp.terms = append(tp.terms, g.terms...)
	}
}

func (tp *TerminalPanel) start(dir string) *TermInstance {
	shell, args := tp.shell()
	if newArgs, env, ok := tp.app.shellIntegrationArgs(shell, args); ok {
		return tp.startProc(dir, shell, newArgs, func(ti *TermInstance) { ti.t.AddEnv(env...) })
	}
	return tp.startProc(dir, shell, args)
}

// startProc starts a program in a new terminal instance.
func (tp *TerminalPanel) startProc(dir, shell string, args []string, prepare ...func(*TermInstance)) *TermInstance {
	a := tp.app
	if dir == "" {
		dir = a.settings.String("terminal.integrated.cwd", "")
		if dir == "" {
			dir = a.root
		}
		if dir == "" {
			dir, _ = os.Getwd()
		}
	}
	cols, rows := 80, 24
	if !tp.rect.Empty() {
		cols, rows = max(tp.rect.W, 10), max(tp.rect.H, 2)
	}
	var env []string
	for k, v := range a.settings.Map("terminal.integrated.env.linux") {
		if s, ok := v.(string); ok {
			env = append(env, k+"="+s)
		}
	}
	t := term.Prepare(shell, args, dir, env, cols, rows)
	tp.nextID++
	ti := &TermInstance{t: t, name: filepath.Base(shell), id: tp.nextID}
	t.MaxScrollback = a.settings.Int("terminal.integrated.scrollback", "", 1000)
	fg := a.theme.RGBA("terminal.foreground")
	bg := a.theme.RGBA("terminal.background")
	t.DefaultFG = [3]uint8{fg.R, fg.G, fg.B}
	t.DefaultBG = [3]uint8{bg.R, bg.G, bg.B}
	t.OnOutput = tp.notifyOutput
	t.OnExit = func() { a.Post(func() { tp.onExit(ti) }) }
	t.Bell = func() {}
	t.OnTitle = func(title string) {}
	for _, fn := range prepare {
		fn(ti)
	}
	if err := t.Run(); err != nil {
		a.notify(NotifyError, "The terminal process failed to launch: "+err.Error())
		return nil
	}
	return ti
}

// notifyOutput wakes the UI once per batch of terminal output.
func (tp *TerminalPanel) notifyOutput() {
	if tp.outputPending.CompareAndSwap(false, true) {
		tp.app.Post(func() { tp.outputPending.Store(false) })
	}
}

func (tp *TerminalPanel) newTerminal(dir string) {
	ti := tp.start(dir)
	if ti == nil {
		return
	}
	tp.groups = append(tp.groups, &TermGroup{terms: []*TermInstance{ti}})
	tp.active = len(tp.groups) - 1
	tp.rebuildFlat()
}

func (tp *TerminalPanel) split() {
	if len(tp.groups) == 0 {
		tp.newTerminal("")
		return
	}
	g := tp.groups[tp.active]
	dir := ""
	if cur := tp.current(); cur != nil && cur.t.Cwd != "" {
		dir = cur.t.Cwd
	}
	ti := tp.start(dir)
	if ti == nil {
		return
	}
	g.terms = append(g.terms, ti)
	g.active = len(g.terms) - 1
	tp.rebuildFlat()
}

func (tp *TerminalPanel) current() *TermInstance {
	if tp.active >= len(tp.groups) || tp.active < 0 {
		return nil
	}
	g := tp.groups[tp.active]
	if g.active >= len(g.terms) {
		g.active = len(g.terms) - 1
	}
	if g.active < 0 {
		return nil
	}
	return g.terms[g.active]
}

func (tp *TerminalPanel) activate(id int) {
	for gi, g := range tp.groups {
		for ti, t := range g.terms {
			if t.id == id {
				tp.active = gi
				g.active = ti
			}
		}
	}
}

func (tp *TerminalPanel) remove(ti *TermInstance) {
	for gi, g := range tp.groups {
		for i, t := range g.terms {
			if t == ti {
				g.terms = append(g.terms[:i], g.terms[i+1:]...)
				if g.active >= len(g.terms) {
					g.active = len(g.terms) - 1
				}
				if len(g.terms) == 0 {
					tp.groups = append(tp.groups[:gi], tp.groups[gi+1:]...)
					if tp.active >= len(tp.groups) {
						tp.active = len(tp.groups) - 1
					}
				}
				tp.rebuildFlat()
				return
			}
		}
	}
}

func (tp *TerminalPanel) onExit(ti *TermInstance) {
	a := tp.app
	if ti.task != nil && !ti.task.done {
		tp.finishTask(ti)
		return
	}
	tp.remove(ti)
	ti.t.Close()
	if len(tp.terms) == 0 && a.panelView == "terminal" {
		a.panelVisible = false
		if a.focus == FocusPanel {
			a.focusEditor()
		}
	}
}

func (tp *TerminalPanel) killActive() {
	if cur := tp.current(); cur != nil {
		if cur.task != nil {
			cur.task.done = true
		}
		cur.t.Close()
		tp.onExit(cur)
	}
}

func (tp *TerminalPanel) closeAll() {
	for _, t := range tp.terms {
		t.t.Close()
	}
	tp.groups = nil
	tp.terms = nil
}

func (tp *TerminalPanel) cycle(d int) {
	if len(tp.terms) == 0 {
		return
	}
	cur := tp.current()
	idx := 0
	for i, t := range tp.terms {
		if t == cur {
			idx = i
		}
	}
	idx = (idx + d + len(tp.terms)) % len(tp.terms)
	tp.activate(tp.terms[idx].id)
}

func (tp *TerminalPanel) clear() {
	cur := tp.current()
	if cur == nil {
		return
	}
	cur.t.ClearScrollback()
	cur.t.WriteInput([]byte{0x0c}) // ctrl+l redraws the prompt
	cur.scrollOff = 0
}

func (tp *TerminalPanel) scroll(n int) {
	cur := tp.current()
	if cur == nil {
		return
	}
	cur.t.Lock()
	maxOff := cur.t.ScrollbackLen()
	cur.t.Unlock()
	cur.scrollOff = clampInt(cur.scrollOff-n, 0, maxOff)
}

func (tp *TerminalPanel) runText(text string) {
	a := tp.app
	a.showPanel("terminal")
	cur := tp.current()
	if cur == nil {
		return
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\r"
	}
	text = strings.ReplaceAll(text, "\n", "\r")
	if cur.t.BracketedPaste && strings.Count(text, "\r") > 1 {
		cur.t.WriteInput([]byte("\x1b[200~" + strings.TrimSuffix(text, "\r") + "\x1b[201~\r"))
		return
	}
	cur.t.WriteInput([]byte(text))
}

func (tp *TerminalPanel) paste(text string) {
	cur := tp.current()
	if cur == nil || text == "" {
		return
	}
	cur.scrollOff = 0
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r")
	if cur.t.BracketedPaste {
		text = "\x1b[200~" + text + "\x1b[201~"
	}
	cur.t.WriteInput([]byte(text))
}

func (tp *TerminalPanel) selectAll() {
	cur := tp.current()
	if cur == nil {
		return
	}
	cur.t.Lock()
	sb := cur.t.ScrollbackLen()
	cols, rows := cur.t.Size()
	cur.t.Unlock()
	cur.selActive = true
	cur.selStart = [2]int{-sb, 0}
	cur.selEnd = [2]int{rows - 1, cols - 1}
}

func (tp *TerminalPanel) selectionText(ti *TermInstance) string {
	if !ti.selActive {
		return ""
	}
	s, e := ti.selStart, ti.selEnd
	if e[0] < s[0] || e[0] == s[0] && e[1] < s[1] {
		s, e = e, s
	}
	ti.t.Lock()
	defer ti.t.Unlock()
	return ti.t.Text(s[0], s[1], e[0], e[1])
}

func (tp *TerminalPanel) copySelection() {
	cur := tp.current()
	if cur == nil {
		return
	}
	if text := tp.selectionText(cur); text != "" {
		tp.app.setClipboard(text)
		cur.selActive = false
	}
}

func (tp *TerminalPanel) rename() {
	cur := tp.current()
	if cur == nil {
		return
	}
	tp.app.promptInput("Rename Terminal", "Enter terminal name", cur.name, func(s string) {
		if s != "" {
			cur.name = s
			cur.custom = true
		}
	})
}

func (tp *TerminalPanel) find() {
	if tp.current() == nil {
		return
	}
	if tp.findInput.app == nil {
		tp.findInput = newTextInput(tp.app, "Find")
		tp.findInput.OnChange = func() { tp.runFind(true) }
	}
	tp.findOpen = true
	tp.findInput.SelectAll()
	tp.runFind(true)
}

// runFind searches the active terminal's scrollback and screen.
func (tp *TerminalPanel) runFind(jump bool) {
	cur := tp.current()
	tp.findMatches = nil
	q := strings.ToLower(tp.findInput.Text())
	if cur == nil || q == "" {
		return
	}
	t := cur.t
	t.Lock()
	sb := t.ScrollbackLen()
	_, rows := t.Size()
	for row := -sb; row < rows; row++ {
		line := t.LineAt(row)
		var rs []rune
		for _, c := range line.Cells {
			if c.Width == 0 {
				continue
			}
			rs = append(rs, unicode.ToLower(c.Ch))
		}
		text := string(rs)
		off := 0
		for {
			i := strings.Index(text[off:], q)
			if i < 0 {
				break
			}
			col := len([]rune(text[:off+i]))
			tp.findMatches = append(tp.findMatches, termMatch{row: row, col: col, length: len([]rune(q))})
			off += i + len(q)
		}
	}
	t.Unlock()
	tp.findIdx = len(tp.findMatches) - 1
	if jump {
		tp.revealMatch()
	}
}

func (tp *TerminalPanel) findMove(d int) {
	if len(tp.findMatches) == 0 {
		return
	}
	tp.findIdx = (tp.findIdx + d + len(tp.findMatches)) % len(tp.findMatches)
	tp.revealMatch()
}

func (tp *TerminalPanel) revealMatch() {
	cur := tp.current()
	if cur == nil || tp.findIdx < 0 || tp.findIdx >= len(tp.findMatches) {
		return
	}
	m := tp.findMatches[tp.findIdx]
	h := max(cur.rect.H, 1)
	// visible rows are [-scrollOff, h-scrollOff)
	if m.row < -cur.scrollOff || m.row >= h-cur.scrollOff {
		cur.scrollOff = max(-m.row+h/2, 0)
		cur.t.Lock()
		cur.scrollOff = min(cur.scrollOff, cur.t.ScrollbackLen())
		cur.t.Unlock()
	}
}

// findHandleKey handles keys while the terminal find widget is open.
func (tp *TerminalPanel) findHandleKey(key string) bool {
	switch key {
	case "escape":
		tp.findOpen = false
		tp.findMatches = nil
		return true
	case "enter", "f3":
		tp.findMove(-1)
		return true
	case "shift+enter", "shift+f3":
		tp.findMove(1)
		return true
	}
	return false
}

func (tp *TerminalPanel) drawFind(r Rect) {
	a := tp.app
	th := a.theme
	w := min(40, r.W-2)
	x := r.X + r.W - w - 1
	bgc := th.C("editorWidget.background")
	a.cv.Fill(Rect{x, r.Y, w, 1}, ' ', styleFB(th.C("editorWidget.foreground"), bgc))
	ibg := th.C("input.background")
	ist := styleFB(th.C("input.foreground"), ibg)
	iw := w - 16
	a.cv.Fill(Rect{x + 1, r.Y, iw, 1}, ' ', ist)
	tp.findInput.Draw(x+2, r.Y, iw-2, ist, styleFB(th.C("input.placeholderForeground"), ibg), a.focus == FocusPanel)
	count := "No results"
	if len(tp.findMatches) > 0 {
		count = fmt.Sprintf("%d of %d", len(tp.findMatches)-tp.findIdx, len(tp.findMatches))
	}
	a.cv.Text(x+iw+2, r.Y, x+w-2, count, styleFB(th.C("editorWidget.foreground"), bgc))
	a.cv.Text(x+w-2, r.Y, x+w-1, a.Icon(IcoClose), styleFB(th.C("editorWidget.foreground"), bgc))
	a.addHit(Rect{x + w - 2, r.Y, 1, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			tp.findOpen = false
			tp.findMatches = nil
		}
		return true
	})
}

func (a *App) terminalItems() []QuickItem {
	var items []QuickItem
	for i, t := range a.terminals.terms {
		items = append(items, QuickItem{Label: fmt.Sprintf("%d: %s", i+1, t.displayName()), Icon: a.Icon(IcoTerminal), Data: t.id})
	}
	return items
}

func (ti *TermInstance) displayName() string {
	if ti.custom {
		return ti.name
	}
	if time.Since(ti.procCheck) > time.Second {
		ti.procCheck = time.Now()
		if n := ti.t.ForegroundProcessName(); n != "" {
			ti.procName = n
		}
	}
	if ti.procName != "" {
		return ti.procName
	}
	return ti.name
}

//#region Input

func modParam(mod tcell.ModMask) int {
	m := 1
	if mod&tcell.ModShift != 0 {
		m++
	}
	if mod&tcell.ModAlt != 0 {
		m += 2
	}
	if mod&tcell.ModCtrl != 0 {
		m += 4
	}
	return m
}

// sendKey encodes a key event for the shell.
func (tp *TerminalPanel) sendKey(ev *tcell.EventKey, key string) {
	cur := tp.current()
	if cur == nil {
		return
	}
	if cur.task != nil && cur.task.done {
		// "press any key to close it"
		tp.onExit(cur)
		return
	}
	t := cur.t
	mod := ev.Modifiers()
	var out []byte
	csi := func(final string) []byte {
		if m := modParam(mod); m > 1 {
			return []byte(fmt.Sprintf("\x1b[1;%d%s", m, final))
		}
		if t.AppCursor {
			return []byte("\x1bO" + final)
		}
		return []byte("\x1b[" + final)
	}
	tilde := func(n int) []byte {
		if m := modParam(mod); m > 1 {
			return []byte(fmt.Sprintf("\x1b[%d;%d~", n, m))
		}
		return []byte(fmt.Sprintf("\x1b[%d~", n))
	}
	switch ev.Key() {
	case tcell.KeyUp:
		out = csi("A")
	case tcell.KeyDown:
		out = csi("B")
	case tcell.KeyRight:
		out = csi("C")
	case tcell.KeyLeft:
		out = csi("D")
	case tcell.KeyHome:
		out = csi("H")
	case tcell.KeyEnd:
		out = csi("F")
	case tcell.KeyPgUp:
		out = tilde(5)
	case tcell.KeyPgDn:
		out = tilde(6)
	case tcell.KeyInsert:
		out = tilde(2)
	case tcell.KeyDelete:
		out = tilde(3)
	case tcell.KeyF1, tcell.KeyF2, tcell.KeyF3, tcell.KeyF4:
		c := "PQRS"[ev.Key()-tcell.KeyF1]
		if m := modParam(mod); m > 1 {
			out = []byte(fmt.Sprintf("\x1b[1;%d%c", m, c))
		} else {
			out = []byte{0x1b, 'O', c}
		}
	case tcell.KeyF5:
		out = tilde(15)
	case tcell.KeyF6:
		out = tilde(17)
	case tcell.KeyF7:
		out = tilde(18)
	case tcell.KeyF8:
		out = tilde(19)
	case tcell.KeyF9:
		out = tilde(20)
	case tcell.KeyF10:
		out = tilde(21)
	case tcell.KeyF11:
		out = tilde(23)
	case tcell.KeyF12:
		out = tilde(24)
	case tcell.KeyEnter:
		out = []byte{'\r'}
		if mod&tcell.ModAlt != 0 {
			out = []byte{0x1b, '\r'}
		}
	case tcell.KeyTab:
		out = []byte{'\t'}
	case tcell.KeyBacktab:
		out = []byte("\x1b[Z")
	case tcell.KeyEsc:
		out = []byte{0x1b}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if ev.Rune() == 'h' && mod&tcell.ModCtrl != 0 {
			out = []byte{0x08}
		} else if mod&tcell.ModCtrl != 0 {
			out = []byte{0x17}
		} else if mod&tcell.ModAlt != 0 {
			out = []byte{0x1b, 0x7f}
		} else {
			out = []byte{0x7f}
		}
	case tcell.KeyRune:
		r := ev.Rune()
		switch {
		case mod&tcell.ModCtrl != 0:
			if c, ok := ctrlByte(r); ok {
				out = []byte{c}
			} else {
				out = []byte(string(r))
			}
			if mod&tcell.ModAlt != 0 {
				out = append([]byte{0x1b}, out...)
			}
		case mod&tcell.ModAlt != 0:
			out = append([]byte{0x1b}, []byte(string(r))...)
		default:
			out = []byte(string(r))
		}
	default:
		k := ev.Key()
		if k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ {
			out = []byte{byte(k-tcell.KeyCtrlA) + 1}
			if mod&tcell.ModAlt != 0 {
				out = append([]byte{0x1b}, out...)
			}
		} else if k == tcell.KeyCtrlSpace {
			out = []byte{0}
		} else if k == tcell.KeyCtrlBackslash {
			out = []byte{0x1c}
		} else if k == tcell.KeyCtrlRightSq {
			out = []byte{0x1d}
		} else if k == tcell.KeyCtrlCarat {
			out = []byte{0x1e}
		} else if k == tcell.KeyCtrlUnderscore {
			out = []byte{0x1f}
		}
	}
	if len(out) == 0 {
		return
	}
	cur.scrollOff = 0
	cur.selActive = false
	t.WriteInput(out)
	_ = key
}

func ctrlByte(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r - 'a' + 1), true
	case r >= 'A' && r <= 'Z':
		return byte(r - 'A' + 1), true
	case r == ' ' || r == '@' || r == '2':
		return 0, true
	case r == '[' || r == '3':
		return 0x1b, true
	case r == '\\' || r == '4':
		return 0x1c, true
	case r == ']' || r == '5':
		return 0x1d, true
	case r == '^' || r == '6':
		return 0x1e, true
	case r == '_' || r == '/' || r == '-' || r == '7':
		return 0x1f, true
	case r == '8' || r == '?':
		return 0x7f, true
	}
	return 0, false
}

//#endregion

//#region Rendering

func (tp *TerminalPanel) color(c term.Color, fg bool) tcell.Color {
	th := tp.app.theme
	if c.IsDefault() {
		if fg {
			return th.C("terminal.foreground")
		}
		return th.C("terminal.background")
	}
	if c.IsRGB() {
		r, g, b := c.Components()
		return tcell.NewRGBColor(int32(r), int32(g), int32(b))
	}
	i := c.Index()
	if i < 16 {
		return th.C(ansiKeys[i])
	}
	r, g, b := term.XtermColor(i)
	return tcell.NewRGBColor(int32(r), int32(g), int32(b))
}

var ansiKeys = [16]string{
	"terminal.ansiBlack", "terminal.ansiRed", "terminal.ansiGreen", "terminal.ansiYellow",
	"terminal.ansiBlue", "terminal.ansiMagenta", "terminal.ansiCyan", "terminal.ansiWhite",
	"terminal.ansiBrightBlack", "terminal.ansiBrightRed", "terminal.ansiBrightGreen", "terminal.ansiBrightYellow",
	"terminal.ansiBrightBlue", "terminal.ansiBrightMagenta", "terminal.ansiBrightCyan", "terminal.ansiBrightWhite",
}

func (tp *TerminalPanel) draw(r Rect) {
	a := tp.app
	th := a.theme
	bgc := th.C("terminal.background")
	a.cv.Fill(r, ' ', styleFB(th.C("terminal.foreground"), bgc))
	if len(tp.groups) == 0 {
		tp.rect = r
		a.cv.Text(r.X+2, r.Y+1, r.X+r.W, "No terminal. Press Ctrl+Shift+` to create one.", styleFB(th.C("descriptionForeground"), bgc))
		return
	}
	// tabs list on the right when more than one terminal
	listW := 0
	if len(tp.terms) > 1 {
		listW = clampInt(r.W/5, 14, 28)
	}
	area := Rect{r.X, r.Y, r.W - listW, r.H}
	if listW > 0 {
		tp.drawList(Rect{r.X + r.W - listW, r.Y, listW, r.H})
	}
	tp.rect = area
	g := tp.groups[tp.active]
	n := len(g.terms)
	x := area.X
	for i, ti := range g.terms {
		w := area.W / n
		if i == n-1 {
			w = area.X + area.W - x
		}
		tr := Rect{x + 1, area.Y, w - 1, area.H}
		if i < n-1 {
			tr.W--
			for y := area.Y; y < area.Y+area.H; y++ {
				a.cv.Set(x+w-1, y, '│', styleFB(th.OverColor("terminal.border", bgc), bgc))
			}
		}
		focused := a.focus == FocusPanel && a.panelView == "terminal" && i == g.active && !tp.findOpen
		tp.drawTerm(ti, tr, focused, i == g.active)
		tp.drawDecorations(ti, tr)
		idx := i
		a.addHit(tr, func(ev MouseEvent) bool {
			g.active = idx
			tp.mouse(ti, tr, ev)
			return true
		})
		x += w
	}
	if tp.findOpen {
		tp.drawFind(area)
	}
}

func (tp *TerminalPanel) drawTerm(ti *TermInstance, r Rect, focused, active bool) {
	a := tp.app
	th := a.theme
	ti.rect = r
	if r.W < 2 || r.H < 1 {
		return
	}
	ti.t.SetSize(r.W, r.H)
	t := ti.t
	t.Lock()
	defer t.Unlock()
	sb := t.ScrollbackLen()
	if ti.scrollOff > sb {
		ti.scrollOff = sb
	}
	boldBright := true
	if v, ok := a.settings.Get("terminal.integrated.drawBoldTextInBrightColors", "").(bool); ok {
		boldBright = v
	}
	selBg := th.OverColor("terminal.selectionBackground", th.C("terminal.background"))
	ss, se := ti.selStart, ti.selEnd
	if se[0] < ss[0] || se[0] == ss[0] && se[1] < ss[1] {
		ss, se = se, ss
	}
	for y := 0; y < r.H; y++ {
		row := y - ti.scrollOff
		line := t.LineAt(row)
		for x := 0; x < r.W; x++ {
			var c term.Cell
			if x < len(line.Cells) {
				c = line.Cells[x]
			} else {
				c = term.Cell{Ch: ' ', Width: 1}
			}
			if c.Width == 0 {
				continue
			}
			fgC := c.FG
			if boldBright && c.Attr&term.AttrBold != 0 && !fgC.IsRGB() && !fgC.IsDefault() && fgC.Index() < 8 {
				fgC = term.Indexed(fgC.Index() + 8)
			}
			fg := tp.color(fgC, true)
			bg := tp.color(c.BG, false)
			if c.Attr&term.AttrReverse != 0 {
				fg, bg = bg, fg
			}
			if ti.selActive {
				inSel := (row > ss[0] || row == ss[0] && x >= ss[1]) && (row < se[0] || row == se[0] && x <= se[1])
				if inSel {
					bg = selBg
				}
			}
			if tp.findOpen && active {
				for mi, m := range tp.findMatches {
					if m.row == row && x >= m.col && x < m.col+m.length {
						key := "terminal.findMatchHighlightBackground"
						if mi == tp.findIdx {
							key = "terminal.findMatchBackground"
						}
						if !th.Has(key) {
							key = strings.Replace(key, "terminal.", "editor.", 1)
						}
						bg = th.OverColor(key, bg)
					}
				}
			}
			st := styleFB(fg, bg)
			if c.Attr&term.AttrBold != 0 {
				st = st.Bold(true)
			}
			if c.Attr&term.AttrDim != 0 {
				st = st.Dim(true)
			}
			if c.Attr&term.AttrItalic != 0 {
				st = st.Italic(true)
			}
			if c.Attr&term.AttrCurly != 0 {
				st = st.Underline(tcell.UnderlineStyleCurly)
			} else if c.Attr&term.AttrDoubleUnderline != 0 {
				st = st.Underline(tcell.UnderlineStyleDouble)
			} else if c.Attr&term.AttrUnderline != 0 {
				st = st.Underline(true)
			}
			if c.Attr&term.AttrStrike != 0 {
				st = st.StrikeThrough(true)
			}
			if c.Attr&term.AttrBlink != 0 {
				st = st.Blink(true)
			}
			ch := c.Ch
			if c.Attr&term.AttrHidden != 0 || ch == 0 {
				ch = ' '
			}
			a.cv.Set(r.X+x, r.Y+y, ch, st)
		}
	}
	cx, cy := t.Cursor()
	if ti.scrollOff == 0 && t.CursorVisible && cy < r.H && cx < r.W {
		if focused {
			style := tcell.CursorStyleSteadyBlock
			switch a.settings.String("terminal.integrated.cursorStyle", "") {
			case "line":
				style = tcell.CursorStyleSteadyBar
			case "underline":
				style = tcell.CursorStyleSteadyUnderline
			}
			if a.settings.Bool("terminal.integrated.cursorBlinking", "") {
				style--
			}
			switch t.CursorStyle {
			case 1, 2:
				style = tcell.CursorStyle(t.CursorStyle)
			case 3, 4, 5, 6:
				style = tcell.CursorStyle(t.CursorStyle)
			}
			a.setCursor(r.X+cx, r.Y+cy, style)
		} else if active {
			// unfocused: outline cursor approximation
			ch, _, st, _ := a.screen.GetContent(r.X+cx, r.Y+cy)
			a.cv.Set(r.X+cx, r.Y+cy, ch, st.Underline(tcell.UnderlineStyleSolid, th.C("terminalCursor.foreground")))
		}
	}
	// scrollbar while scrolled back
	if sb > 0 && (ti.scrollOff > 0 || r.Contains(a.mouseX, a.mouseY)) {
		total := sb + r.H
		sh := max(r.H*r.H/total, 1)
		sy := (sb - ti.scrollOff) * (r.H - sh) / max(sb, 1)
		for i := sy; i < sy+sh && i < r.H; i++ {
			_, _, st, _ := a.screen.GetContent(r.X+r.W-1, r.Y+i)
			a.cv.Set(r.X+r.W-1, r.Y+i, ' ', st.Background(th.OverColor("scrollbarSlider.background", th.C("terminal.background"))))
		}
	}
}

func (tp *TerminalPanel) drawList(r Rect) {
	a := tp.app
	th := a.theme
	bgc := th.C("panel.background")
	if th.Has("terminal.tab.activeBorder") {
		_ = th.C("terminal.tab.activeBorder")
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		a.cv.Set(r.X, y, '│', styleFB(th.OverColor("panel.border", bgc), bgc))
	}
	y := r.Y
	cur := tp.current()
	for _, g := range tp.groups {
		for i, ti := range g.terms {
			if y >= r.Y+r.H {
				return
			}
			sel := ti == cur
			rbg := bgc
			fg := th.C("foreground")
			if sel {
				rbg = th.C("list.inactiveSelectionBackground")
				if a.focus == FocusPanel {
					rbg = th.C("list.activeSelectionBackground")
					fg = th.C("list.activeSelectionForeground")
				}
			} else if a.mouseY == y && r.Contains(a.mouseX, a.mouseY) {
				rbg = th.OverColor("list.hoverBackground", bgc)
			}
			st := styleFB(fg, rbg)
			a.cv.Fill(Rect{r.X + 1, y, r.W - 1, 1}, ' ', st)
			prefix := " "
			if len(g.terms) > 1 {
				switch {
				case i == 0:
					prefix = "┌"
				case i == len(g.terms)-1:
					prefix = "└"
				default:
					prefix = "├"
				}
			}
			x := a.cv.Text(r.X+1, y, r.X+r.W, prefix, st)
			x = a.cv.Text(x, y, r.X+r.W, a.Icon(IcoTerminal)+" ", st)
			a.cv.TextEllipsis(x, y, r.X+r.W-x-2, ti.displayName(), st)
			hover := a.mouseY == y && r.Contains(a.mouseX, a.mouseY)
			if hover || sel {
				a.cv.Text(r.X+r.W-2, y, r.X+r.W, a.Icon(IcoTrash), st)
				tt := ti
				a.addHit(Rect{r.X + r.W - 2, y, 2, 1}, func(ev MouseEvent) bool {
					if ev.Kind == MousePress {
						tt.t.Close()
						tp.onExit(tt)
					}
					return true
				})
			}
			id := ti.id
			a.addHit(Rect{r.X + 1, y, r.W - 1, 1}, func(ev MouseEvent) bool {
				if ev.Kind == MousePress {
					tp.activate(id)
					a.setFocus(FocusPanel)
					if ev.Button == tcell.Button2 {
						tp.contextMenu(ev.X, ev.Y)
					}
				}
				return true
			})
			y++
		}
	}
}

func (tp *TerminalPanel) contextMenu(x, y int) {
	cur := tp.current()
	hasSel := cur != nil && cur.selActive
	tp.app.openContextMenu(x, y, []MenuItem{
		{Label: "Copy", Command: "workbench.action.terminal.copySelection", Disabled: !hasSel},
		{Label: "Paste", Command: "workbench.action.terminal.paste"},
		{Label: "Select All", Command: "workbench.action.terminal.selectAll"},
		{Separator: true},
		{Label: "Clear Terminal", Command: "workbench.action.terminal.clear"},
		{Separator: true},
		{Label: "Split Terminal", Command: "workbench.action.terminal.split"},
		{Label: "Rename...", Command: "workbench.action.terminal.rename"},
		{Label: "Kill Terminal", Command: "workbench.action.terminal.kill"},
	})
}

//#endregion

//#region Mouse

func (tp *TerminalPanel) mouse(ti *TermInstance, r Rect, ev MouseEvent) {
	a := tp.app
	t := ti.t
	if ev.Kind == MousePress {
		a.setFocus(FocusPanel)
	}
	// ctrl+click opens links and file paths
	if ev.Kind == MousePress && ev.Button == tcell.Button1 && ev.Mods&tcell.ModCtrl != 0 {
		if tp.openLinkAt(ti, r, ev.X, ev.Y) {
			return
		}
	}
	// forward to applications that requested mouse reporting
	if t.MouseMode != 0 && ev.Mods&tcell.ModShift == 0 && ti.scrollOff == 0 {
		tp.forwardMouse(ti, r, ev)
		return
	}
	switch ev.Kind {
	case MouseWheel:
		if t.AltScreen() {
			// alternate screen apps (less, man) get arrow keys
			seq := "\x1b[A"
			if ev.WheelDY > 0 {
				seq = "\x1b[B"
			}
			if t.AppCursor {
				seq = strings.Replace(seq, "[", "O", 1)
			}
			t.WriteInput([]byte(strings.Repeat(seq, 3)))
			return
		}
		tp.scroll(ev.WheelDY * 3)
	case MousePress:
		col := ev.X - r.X
		row := ev.Y - r.Y - ti.scrollOff
		switch ev.Button {
		case tcell.Button2:
			if a.settings.String("terminal.integrated.rightClickBehavior", "") == "paste" {
				tp.paste(a.getClipboard())
				return
			}
			tp.contextMenu(ev.X, ev.Y)
			return
		case tcell.Button3:
			tp.paste(a.getClipboard())
			return
		}
		if ev.Clicks == 2 {
			// select word
			t.Lock()
			line := t.LineAt(row)
			t.Unlock()
			s, e := col, col
			isW := func(i int) bool {
				if i < 0 || i >= len(line.Cells) {
					return false
				}
				ch := line.Cells[i].Ch
				return ch != ' ' && ch != 0 && !strings.ContainsRune("()[]{}<>'\"`|;,", ch)
			}
			for isW(s - 1) {
				s--
			}
			for isW(e + 1) {
				e++
			}
			ti.selActive = true
			ti.selStart = [2]int{row, s}
			ti.selEnd = [2]int{row, e}
			tp.afterSelect(ti)
			return
		}
		if ev.Clicks >= 3 {
			ti.selActive = true
			ti.selStart = [2]int{row, 0}
			ti.selEnd = [2]int{row, r.W - 1}
			tp.afterSelect(ti)
			return
		}
		ti.selActive = false
		ti.selStart = [2]int{row, col}
		ti.selEnd = ti.selStart
		a.drag = func(ev MouseEvent) {
			if ev.Y < r.Y {
				tp.scroll(-1)
			} else if ev.Y >= r.Y+r.H {
				tp.scroll(1)
			}
			c := clampInt(ev.X-r.X, 0, r.W-1)
			rr := clampInt(ev.Y-r.Y, 0, r.H-1) - ti.scrollOff
			ti.selEnd = [2]int{rr, c}
			ti.selActive = ti.selEnd != ti.selStart
			if ev.Kind == MouseRelease && ti.selActive {
				tp.afterSelect(ti)
			}
		}
	}
}

func (tp *TerminalPanel) afterSelect(ti *TermInstance) {
	if tp.app.settings.Bool("terminal.integrated.copyOnSelection", "") {
		if text := tp.selectionText(ti); text != "" {
			tp.app.setClipboard(text)
		}
	}
}

func (tp *TerminalPanel) forwardMouse(ti *TermInstance, r Rect, ev MouseEvent) {
	t := ti.t
	x := ev.X - r.X + 1
	y := ev.Y - r.Y + 1
	if x < 1 || y < 1 || x > r.W || y > r.H {
		if ev.Kind != MouseRelease && ev.Kind != MouseDrag {
			return
		}
		x = clampInt(x, 1, r.W)
		y = clampInt(y, 1, r.H)
	}
	var btn int
	release := false
	switch ev.Kind {
	case MouseWheel:
		btn = 64
		if ev.WheelDY > 0 {
			btn = 65
		}
	case MousePress:
		switch ev.Button {
		case tcell.Button1:
			btn = 0
		case tcell.Button3:
			btn = 1
		case tcell.Button2:
			btn = 2
		}
		tp.app.drag = func(e2 MouseEvent) { tp.forwardMouse(ti, r, e2) }
	case MouseRelease:
		btn = 0
		release = true
	case MouseDrag:
		if t.MouseMode < 1002 {
			return
		}
		btn = 32
	case MouseMove:
		if t.MouseMode != 1003 {
			return
		}
		btn = 35
	}
	if ev.Mods&tcell.ModAlt != 0 {
		btn |= 8
	}
	if ev.Mods&tcell.ModCtrl != 0 {
		btn |= 16
	}
	var seq string
	if t.MouseSGR {
		final := 'M'
		if release {
			final = 'm'
		}
		seq = fmt.Sprintf("\x1b[<%d;%d;%d%c", btn, x, y, final)
	} else {
		if release {
			btn = 3
		}
		if x > 223 || y > 223 {
			return
		}
		seq = string([]byte{0x1b, '[', 'M', byte(32 + btn), byte(32 + x), byte(32 + y)})
	}
	t.WriteInput([]byte(seq))
}

//#endregion

var _ = utf8.RuneLen

var termPathRe = regexp.MustCompile(`^(.+?)(?::(\d+))?(?::(\d+))?:?$`)

// openLinkAt opens the URL or file path under the given screen position.
func (tp *TerminalPanel) openLinkAt(ti *TermInstance, r Rect, x, y int) bool {
	a := tp.app
	t := ti.t
	t.Lock()
	line := t.LineAt(y - r.Y - ti.scrollOff)
	t.Unlock()
	col := x - r.X
	if col < 0 || col >= len(line.Cells) {
		return false
	}
	isDelim := func(ch rune) bool {
		return ch == ' ' || ch == 0 || strings.ContainsRune("\"'`()[]{}<>|,;", ch)
	}
	if isDelim(line.Cells[col].Ch) {
		return false
	}
	s, e := col, col
	for s > 0 && !isDelim(line.Cells[s-1].Ch) {
		s--
	}
	for e+1 < len(line.Cells) && !isDelim(line.Cells[e+1].Ch) {
		e++
	}
	var sb strings.Builder
	for i := s; i <= e; i++ {
		if line.Cells[i].Width > 0 {
			sb.WriteRune(line.Cells[i].Ch)
		}
	}
	word := strings.TrimRight(sb.String(), ".")
	if strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") {
		openExternal(word)
		a.setStatusMessage("Opening " + word)
		return true
	}
	m := termPathRe.FindStringSubmatch(word)
	if m == nil {
		return false
	}
	path := expandHome(m[1])
	if !filepath.IsAbs(path) {
		base := t.Cwd
		if base == "" {
			base = a.root
		}
		path = filepath.Join(base, path)
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	if st.IsDir() {
		a.showSidebar("explorer")
		a.explorer.reveal(path, true)
		return true
	}
	v := a.OpenFile(path, false)
	if v != nil && m[2] != "" {
		ln, _ := strconv.Atoi(m[2])
		cl := 1
		if m[3] != "" {
			cl, _ = strconv.Atoi(m[3])
		}
		p := v.buf().Clamp(buffer.Pos{Line: ln - 1, Col: cl - 1})
		v.SetCursor(p)
		v.ensurePosVisible(p, true)
	}
	a.focusEditor()
	return true
}

// startTerm starts a program in a pseudo terminal.
func startTerm(prog string, args []string, dir string, env []string, cols, rows int) (*term.Terminal, error) {
	return term.Start(prog, args, dir, env, cols, rows)
}
