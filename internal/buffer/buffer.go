package buffer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Edit replaces Range with Text.
type Edit struct {
	Range Range
	Text  string
}

// Change describes an applied edit, delivered to listeners.
type Change struct {
	Old     Range  // range in the document before the edit
	NewEnd  Pos    // end of the inserted text after the edit
	Text    string // inserted text
	Removed string // removed text
}

type appliedEdit struct {
	forward Edit // edit as applied
	inverse Edit // edit that undoes it
}

type undoGroup struct {
	id        int
	edits     []appliedEdit
	before    []Selection
	after     []Selection
	kind      string
	closed    bool
	timestamp time.Time
}

// Buffer is an in-memory text document made of lines of runes.
type Buffer struct {
	lines [][]rune

	Path         string
	EOL          string
	BOM          bool
	LanguageID   string
	TabSize      int
	InsertSpaces bool
	ReadOnly     bool
	ModTime      time.Time

	version   int
	nextGroup int
	savedID   int
	undo      []*undoGroup
	redo      []*undoGroup

	listeners    map[int]func(Change)
	nextListener int

	// Untitled buffers have no path yet; UntitledName is shown instead.
	UntitledName string
}

// New creates an empty buffer.
func New() *Buffer {
	return &Buffer{
		lines:        [][]rune{{}},
		EOL:          "\n",
		TabSize:      4,
		InsertSpaces: true,
		listeners:    map[int]func(Change){},
		LanguageID:   "plaintext",
	}
}

// NewFromString creates a buffer containing text.
func NewFromString(text string) *Buffer {
	b := New()
	b.setText(text)
	return b
}

// Load reads a file from disk into a new buffer.
func Load(path string) (*Buffer, error) {
	b := New()
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	b.Path = path
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return b, nil
		}
		return nil, err
	}
	if st, err := os.Stat(path); err == nil {
		b.ModTime = st.ModTime()
	}
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		b.BOM = true
		data = data[3:]
	}
	text := string(data)
	if strings.Contains(text, "\r\n") {
		b.EOL = "\r\n"
	}
	b.setText(text)
	b.DetectIndentation()
	return b, nil
}

// Reload re-reads the file from disk, keeping listeners. It is recorded as an undoable edit.
func (b *Buffer) Reload() error {
	if b.Path == "" {
		return nil
	}
	data, err := os.ReadFile(b.Path)
	if err != nil {
		return err
	}
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		data = data[3:]
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if text == b.Text() {
		b.MarkSaved()
		return nil
	}
	end := b.EndPos()
	b.ApplyEdits([]Edit{{Range{Pos{0, 0}, end}, text}}, nil, "reload")
	b.CloseUndoGroup()
	b.MarkSaved()
	if st, err := os.Stat(b.Path); err == nil {
		b.ModTime = st.ModTime()
	}
	return nil
}

func (b *Buffer) setText(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	parts := strings.Split(text, "\n")
	b.lines = make([][]rune, len(parts))
	for i, p := range parts {
		b.lines[i] = []rune(p)
	}
}

// Save writes the buffer to its path.
func (b *Buffer) Save() error {
	if b.Path == "" {
		return errors.New("Buffer has no path. Use Save As to choose a location.")
	}
	return b.SaveAs(b.Path)
}

// SaveAs writes the buffer to path and adopts it as the buffer's path.
func (b *Buffer) SaveAs(path string) error {
	var sb strings.Builder
	if b.BOM {
		sb.WriteString("\xEF\xBB\xBF")
	}
	for i, l := range b.lines {
		if i > 0 {
			sb.WriteString(b.EOL)
		}
		sb.WriteString(string(l))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode()
	}
	if err := os.WriteFile(path, []byte(sb.String()), mode); err != nil {
		return err
	}
	b.Path = path
	b.MarkSaved()
	if st, err := os.Stat(path); err == nil {
		b.ModTime = st.ModTime()
	}
	return nil
}

// MarkSaved records the current state as the saved state.
func (b *Buffer) MarkSaved() {
	b.savedID = b.stateID()
	b.CloseUndoGroup()
}

func (b *Buffer) stateID() int {
	if len(b.undo) == 0 {
		return 0
	}
	return b.undo[len(b.undo)-1].id
}

// Dirty reports whether the buffer differs from its saved state.
func (b *Buffer) Dirty() bool { return b.stateID() != b.savedID }

// Version increases on every change.
func (b *Buffer) Version() int { return b.version }

// Name returns the file name or the untitled name.
func (b *Buffer) Name() string {
	if b.Path == "" {
		if b.UntitledName != "" {
			return b.UntitledName
		}
		return "Untitled"
	}
	return filepath.Base(b.Path)
}

// LineCount returns the number of lines (always at least 1).
func (b *Buffer) LineCount() int { return len(b.lines) }

// Line returns the runes of line i. The slice must not be modified.
func (b *Buffer) Line(i int) []rune {
	if i < 0 || i >= len(b.lines) {
		return nil
	}
	return b.lines[i]
}

// LineString returns line i as a string.
func (b *Buffer) LineString(i int) string { return string(b.Line(i)) }

// LineLen returns the number of runes on line i.
func (b *Buffer) LineLen(i int) int { return len(b.Line(i)) }

// Text returns the full content using "\n" line endings.
func (b *Buffer) Text() string {
	var sb strings.Builder
	for i, l := range b.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(l))
	}
	return sb.String()
}

// Lines returns all lines as strings.
func (b *Buffer) Lines() []string {
	out := make([]string, len(b.lines))
	for i, l := range b.lines {
		out[i] = string(l)
	}
	return out
}

// EndPos returns the position after the last character.
func (b *Buffer) EndPos() Pos {
	last := len(b.lines) - 1
	return Pos{last, len(b.lines[last])}
}

// Clamp keeps p within the document.
func (b *Buffer) Clamp(p Pos) Pos {
	if p.Line < 0 {
		return Pos{0, 0}
	}
	if p.Line >= len(b.lines) {
		return b.EndPos()
	}
	if p.Col < 0 {
		p.Col = 0
	}
	if p.Col > len(b.lines[p.Line]) {
		p.Col = len(b.lines[p.Line])
	}
	return p
}

// TextRange returns the text covered by r.
func (b *Buffer) TextRange(r Range) string {
	r = r.Normalized()
	s, e := b.Clamp(r.Start), b.Clamp(r.End)
	if s.Line == e.Line {
		return string(b.lines[s.Line][s.Col:e.Col])
	}
	var sb strings.Builder
	sb.WriteString(string(b.lines[s.Line][s.Col:]))
	for i := s.Line + 1; i < e.Line; i++ {
		sb.WriteByte('\n')
		sb.WriteString(string(b.lines[i]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(b.lines[e.Line][:e.Col]))
	return sb.String()
}

// RuneAt returns the rune at p, or '\n' at line end, or 0 past the end.
func (b *Buffer) RuneAt(p Pos) rune {
	if p.Line < 0 || p.Line >= len(b.lines) {
		return 0
	}
	l := b.lines[p.Line]
	if p.Col < len(l) && p.Col >= 0 {
		return l[p.Col]
	}
	if p.Line < len(b.lines)-1 {
		return '\n'
	}
	return 0
}

// AddListener registers a change callback and returns an id for RemoveListener.
func (b *Buffer) AddListener(fn func(Change)) int {
	b.nextListener++
	b.listeners[b.nextListener] = fn
	return b.nextListener
}

// RemoveListener unregisters a change callback.
func (b *Buffer) RemoveListener(id int) { delete(b.listeners, id) }

// applyOne applies a single edit and returns the applied record.
func (b *Buffer) applyOne(e Edit) (appliedEdit, Change) {
	r := e.Range.Normalized()
	r.Start, r.End = b.Clamp(r.Start), b.Clamp(r.End)
	removed := b.TextRange(r)
	text := strings.ReplaceAll(e.Text, "\r\n", "\n")
	insLines := strings.Split(text, "\n")

	startLine := b.lines[r.Start.Line]
	endLine := b.lines[r.End.Line]
	prefix := append([]rune{}, startLine[:r.Start.Col]...)
	suffix := append([]rune{}, endLine[r.End.Col:]...)

	newLines := make([][]rune, len(insLines))
	for i, s := range insLines {
		newLines[i] = []rune(s)
	}
	lastLen := len(newLines[len(newLines)-1])
	newLines[0] = append(prefix, newLines[0]...)
	newEnd := Pos{r.Start.Line + len(newLines) - 1, lastLen}
	if len(newLines) == 1 {
		newEnd.Col = r.Start.Col + lastLen
	}
	newLines[len(newLines)-1] = append(newLines[len(newLines)-1], suffix...)

	// splice
	tail := b.lines[r.End.Line+1:]
	merged := make([][]rune, 0, r.Start.Line+len(newLines)+len(tail))
	merged = append(merged, b.lines[:r.Start.Line]...)
	merged = append(merged, newLines...)
	merged = append(merged, tail...)
	b.lines = merged
	b.version++

	ch := Change{Old: r, NewEnd: newEnd, Text: text, Removed: removed}
	return appliedEdit{
		forward: Edit{r, text},
		inverse: Edit{Range{r.Start, newEnd}, removed},
	}, ch
}

func (b *Buffer) notify(ch Change) {
	ids := make([]int, 0, len(b.listeners))
	for id := range b.listeners {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if fn, ok := b.listeners[id]; ok {
			fn(ch)
		}
	}
}

// ApplyEdits applies several non-overlapping edits as one undoable step.
// It returns, for each edit in the given order, the range of its inserted text
// in final document coordinates. before is the selection state to restore on undo.
// kind controls undo coalescing ("type", "delete", or anything else for no coalescing).
func (b *Buffer) ApplyEdits(edits []Edit, before []Selection, kind string) []Range {
	if len(edits) == 0 {
		return nil
	}
	order := make([]int, len(edits))
	for i := range order {
		order[i] = i
	}
	// apply from the bottom of the document to the top
	sort.SliceStable(order, func(a, c int) bool {
		ra, rc := edits[order[a]].Range.Normalized(), edits[order[c]].Range.Normalized()
		return rc.Start.Less(ra.Start)
	})
	results := make([]Range, len(edits))
	applied := make([]appliedEdit, 0, len(edits))
	for k, idx := range order {
		ae, ch := b.applyOne(edits[idx])
		applied = append(applied, ae)
		results[idx] = Range{ch.Old.Start, ch.NewEnd}
		// shift results of edits applied earlier (they are later in the document)
		for _, prev := range order[:k] {
			results[prev].Start = TransformPos(results[prev].Start, ch.Old, ch.NewEnd, true)
			results[prev].End = TransformPos(results[prev].End, ch.Old, ch.NewEnd, true)
		}
		b.notify(ch)
	}
	b.recordUndo(applied, before, kind)
	return results
}

func (b *Buffer) recordUndo(applied []appliedEdit, before []Selection, kind string) {
	b.redo = nil
	now := time.Now()
	if top := b.topGroup(); top != nil && !top.closed && kind != "" && top.kind == kind &&
		(kind == "type" || kind == "delete") && now.Sub(top.timestamp) < 2*time.Second && top.id != b.savedID {
		top.edits = append(top.edits, applied...)
		top.timestamp = now
		return
	}
	b.nextGroup++
	b.undo = append(b.undo, &undoGroup{
		id:        b.nextGroup,
		edits:     applied,
		before:    append([]Selection{}, before...),
		kind:      kind,
		timestamp: now,
	})
	if len(b.undo) > 10000 {
		b.undo = b.undo[len(b.undo)-10000:]
	}
}

func (b *Buffer) topGroup() *undoGroup {
	if len(b.undo) == 0 {
		return nil
	}
	return b.undo[len(b.undo)-1]
}

// CloseUndoGroup stops the current undo group from absorbing further edits.
func (b *Buffer) CloseUndoGroup() {
	if top := b.topGroup(); top != nil {
		top.closed = true
	}
}

// SetAfterSelections records the selection state after the latest undo group.
func (b *Buffer) SetAfterSelections(sels []Selection) {
	if top := b.topGroup(); top != nil {
		top.after = append([]Selection{}, sels...)
	}
}

// CanUndo reports whether there is anything to undo.
func (b *Buffer) CanUndo() bool { return len(b.undo) > 0 }

// CanRedo reports whether there is anything to redo.
func (b *Buffer) CanRedo() bool { return len(b.redo) > 0 }

// Undo reverts the last group and returns the selections to restore.
func (b *Buffer) Undo() ([]Selection, bool) {
	g := b.topGroup()
	if g == nil {
		return nil, false
	}
	b.undo = b.undo[:len(b.undo)-1]
	for i := len(g.edits) - 1; i >= 0; i-- {
		_, ch := b.applyOne(g.edits[i].inverse)
		b.notify(ch)
	}
	g.closed = true
	b.redo = append(b.redo, g)
	return g.before, true
}

// Redo re-applies the last undone group and returns the selections to restore.
func (b *Buffer) Redo() ([]Selection, bool) {
	if len(b.redo) == 0 {
		return nil, false
	}
	g := b.redo[len(b.redo)-1]
	b.redo = b.redo[:len(b.redo)-1]
	var last Pos
	for _, ae := range g.edits {
		_, ch := b.applyOne(ae.forward)
		last = ch.NewEnd
		b.notify(ch)
	}
	b.undo = append(b.undo, g)
	if len(g.after) > 0 {
		return g.after, true
	}
	return []Selection{Caret(last)}, true
}

// DetectIndentation guesses tab size and whether spaces are used from the content.
func (b *Buffer) DetectIndentation() {
	tabs, spaces := 0, 0
	counts := map[int]int{}
	prevIndent := 0
	for i, l := range b.lines {
		if i > 2000 {
			break
		}
		if len(l) == 0 {
			continue
		}
		if l[0] == '\t' {
			tabs++
			continue
		}
		n := 0
		for n < len(l) && l[n] == ' ' {
			n++
		}
		if n == len(l) {
			continue
		}
		if n > 0 {
			spaces++
		}
		d := n - prevIndent
		if d < 0 {
			d = -d
		}
		if d > 1 && d <= 8 {
			counts[d]++
		}
		prevIndent = n
	}
	if tabs == 0 && spaces == 0 {
		return
	}
	if tabs > spaces {
		b.InsertSpaces = false
		return
	}
	b.InsertSpaces = true
	best, bestN := 0, 0
	for d, n := range counts {
		if n > bestN || (n == bestN && d < best) {
			best, bestN = d, n
		}
	}
	if best > 0 {
		b.TabSize = best
	}
}

// IndentString returns one level of indentation.
func (b *Buffer) IndentString() string {
	if b.InsertSpaces {
		return strings.Repeat(" ", b.TabSize)
	}
	return "\t"
}

// LeadingWhitespace returns the indentation prefix of line i.
func (b *Buffer) LeadingWhitespace(i int) string {
	l := b.Line(i)
	n := 0
	for n < len(l) && (l[n] == ' ' || l[n] == '\t') {
		n++
	}
	return string(l[:n])
}

// FirstNonWhitespace returns the column of the first non-blank rune, or the line length.
func (b *Buffer) FirstNonWhitespace(i int) int {
	l := b.Line(i)
	n := 0
	for n < len(l) && (l[n] == ' ' || l[n] == '\t') {
		n++
	}
	return n
}

// OffsetToPos converts a byte offset in Text() into a position.
func (b *Buffer) OffsetToPos(text string, off int) Pos {
	line, col := 0, 0
	for i, r := range text {
		if i >= off {
			break
		}
		if r == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return Pos{line, col}
}

// PosToUTF16 converts a rune column into a UTF-16 code unit column.
func (b *Buffer) PosToUTF16(p Pos) int {
	l := b.Line(p.Line)
	n := 0
	for i := 0; i < p.Col && i < len(l); i++ {
		if l[i] >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// UTF16ToCol converts a UTF-16 code unit column into a rune column on line.
func (b *Buffer) UTF16ToCol(line, u int) int {
	l := b.Line(line)
	n := 0
	for i, r := range l {
		if n >= u {
			return i
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return len(l)
}

// ByteCol converts a rune column into a UTF-8 byte column.
func (b *Buffer) ByteCol(p Pos) int {
	l := b.Line(p.Line)
	n := 0
	for i := 0; i < p.Col && i < len(l); i++ {
		n += utf8.RuneLen(l[i])
	}
	return n
}
