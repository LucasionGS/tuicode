package ui

import (
	"regexp"
	"sort"

	"github.com/gdamore/tcell/v2"
	"strings"

	"tuicode/internal/buffer"
)

// Fold hides lines Start+1..End.
type Fold struct {
	Start int
	End   int
}

type vpos struct {
	line int
	seg  int
}

// EditorView is one editor tab showing a document.
type EditorView struct {
	app *App
	doc *Document

	sels    []buffer.Selection
	primary int
	prefX   []int

	topLine int
	topSeg  int
	leftCol int

	rect    Rect // text area from the last draw
	full    Rect // whole editor area including gutter and minimap
	gutterW int
	minimap Rect

	folds []Fold
	wrap  bool

	listener int
	applying bool

	wrapCache        map[int][]int
	wrapIndent       map[int]int
	wrapCacheVersion int
	wrapCacheWidth   int

	cursorHistory [][]buffer.Selection
	cursorFuture  [][]buffer.Selection

	find        *FindWidget
	completion  *Completion
	hover       *Hover
	sig         *SignatureHelpState
	snippet     *SnippetSession
	rename      *RenameBox
	codeActions *CodeActionMenu

	highlights        []buffer.Range
	highlightsVersion int
	highlightsPos     buffer.Pos
	inlayHints        map[int][]inlay
	inlayVersion      int

	preview bool
	// stickyRows is how many sticky scroll rows were drawn.
	stickyRows  int
	stickyLines []int

	special SpecialPage

	linkRange      *buffer.Range
	scrollDragging bool
	boxCursor      buffer.Pos
	inlayTop       int
	inlayPending   bool
	lightbulbLine  int
	dirtyDiff      *DirtyDiffPeek
	peek           *PeekView
	lightbulbKey   [2]int

	dragging   bool
	dragAnchor buffer.Selection
	dragWord   bool
	dragLine   bool
	boxSelect  bool
	boxAnchor  buffer.Pos

	lastEditPos buffer.Pos
	hasLastEdit bool
}

type inlay struct {
	col   int
	label string
	left  bool
	right bool
	color tcell.Color
}

func (a *App) newEditorView(d *Document) *EditorView {
	v := &EditorView{app: a, doc: d, lightbulbLine: -1, lightbulbKey: [2]int{-1, -1}}
	v.sels = []buffer.Selection{buffer.Caret(buffer.Pos{})}
	v.prefX = []int{-1}
	v.wrap = a.settings.String("editor.wordWrap", d.Lang.ID) != "off"
	v.listener = d.Buf.AddListener(v.onChange)
	d.views[v] = true
	d.refs++
	return v
}

func (v *EditorView) dispose() {
	v.doc.Buf.RemoveListener(v.listener)
	delete(v.doc.views, v)
	v.doc.refs--
}

func (v *EditorView) buf() *buffer.Buffer { return v.doc.Buf }

// onChange keeps selections and folds of this view aligned with edits made elsewhere.
func (v *EditorView) onChange(ch buffer.Change) {
	v.wrapCache = nil
	v.app.transformAutoClosed(v, ch)
	if !v.applying {
		for i := range v.sels {
			v.sels[i].Anchor = buffer.TransformPos(v.sels[i].Anchor, ch.Old, ch.NewEnd, false)
			v.sels[i].Active = buffer.TransformPos(v.sels[i].Active, ch.Old, ch.NewEnd, false)
		}
	}
	// folds
	delta := ch.NewEnd.Line - ch.Old.End.Line
	out := v.folds[:0]
	for _, f := range v.folds {
		if ch.Old.End.Line <= f.Start {
			if ch.Old.Start.Line < f.Start || (ch.Old.Start.Line == f.Start && ch.Old.End.Line == f.Start && delta != 0 && ch.Old.Start.Col == 0) {
				f.Start += delta
				f.End += delta
			}
			out = append(out, f)
			continue
		}
		if ch.Old.Start.Line > f.End {
			out = append(out, f)
			continue
		}
		// edit touches the hidden region: unfold
	}
	v.folds = out
	if v.snippet != nil {
		v.snippet.transform(ch)
	}
	if v.hasLastEdit {
		v.lastEditPos = buffer.TransformPos(v.lastEditPos, ch.Old, ch.NewEnd, true)
	}
	v.highlights = nil
}

//#region Selections

// Primary returns the primary selection.
func (v *EditorView) Primary() buffer.Selection {
	if v.primary >= len(v.sels) {
		v.primary = len(v.sels) - 1
	}
	return v.sels[v.primary]
}

// Cursor returns the primary cursor position.
func (v *EditorView) Cursor() buffer.Pos { return v.Primary().Active }

// SetSelections replaces all selections; the last one becomes primary unless primary >= 0.
func (v *EditorView) SetSelections(sels []buffer.Selection, primary int) {
	if len(sels) == 0 {
		sels = []buffer.Selection{buffer.Caret(buffer.Pos{})}
	}
	b := v.buf()
	for i := range sels {
		sels[i].Anchor = b.Clamp(sels[i].Anchor)
		sels[i].Active = b.Clamp(sels[i].Active)
	}
	if primary < 0 || primary >= len(sels) {
		primary = len(sels) - 1
	}
	v.sels = sels
	v.primary = primary
	v.prefX = make([]int, len(sels))
	for i := range v.prefX {
		v.prefX[i] = -1
	}
	v.normalize()
}

// SetCursor moves to a single caret.
func (v *EditorView) SetCursor(p buffer.Pos) {
	v.SetSelections([]buffer.Selection{buffer.Caret(p)}, 0)
}

// normalize sorts selections and merges overlapping ones.
func (v *EditorView) normalize() {
	if len(v.prefX) != len(v.sels) {
		v.prefX = make([]int, len(v.sels))
		for i := range v.prefX {
			v.prefX[i] = -1
		}
	}
	type item struct {
		s       buffer.Selection
		px      int
		primary bool
	}
	items := make([]item, len(v.sels))
	for i, s := range v.sels {
		items[i] = item{s, v.prefX[i], i == v.primary}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].s.Range().Start.Less(items[j].s.Range().Start)
	})
	out := items[:0:0]
	for _, it := range items {
		if n := len(out); n > 0 {
			prev := &out[n-1]
			pr, cr := prev.s.Range(), it.s.Range()
			overlap := cr.Start.Less(pr.End) || (cr.Start == pr.End && (cr.Empty() || pr.Empty()))
			if overlap {
				end := pr.End
				if end.Less(cr.End) {
					end = cr.End
				}
				if prev.s.Reversed() {
					prev.s = buffer.Selection{Anchor: end, Active: pr.Start}
				} else {
					prev.s = buffer.Selection{Anchor: pr.Start, Active: end}
				}
				prev.primary = prev.primary || it.primary
				continue
			}
		}
		out = append(out, it)
	}
	v.sels = make([]buffer.Selection, len(out))
	v.prefX = make([]int, len(out))
	v.primary = 0
	for i, it := range out {
		v.sels[i] = it.s
		v.prefX[i] = it.px
		if it.primary {
			v.primary = i
		}
	}
}

func (v *EditorView) pushCursorHistory() {
	cp := append([]buffer.Selection{}, v.sels...)
	if n := len(v.cursorHistory); n > 0 {
		last := v.cursorHistory[n-1]
		if len(last) == len(cp) {
			same := true
			for i := range last {
				if last[i] != cp[i] {
					same = false
					break
				}
			}
			if same {
				return
			}
		}
	}
	v.cursorHistory = append(v.cursorHistory, cp)
	if len(v.cursorHistory) > 100 {
		v.cursorHistory = v.cursorHistory[1:]
	}
}

// HasSelection reports whether any selection is non-empty.
func (v *EditorView) HasSelection() bool {
	for _, s := range v.sels {
		if !s.Empty() {
			return true
		}
	}
	return false
}

// SelectedText returns the primary selection's text.
func (v *EditorView) SelectedText() string {
	return v.buf().TextRange(v.Primary().Range())
}

//#endregion

//#region Words

type charClass int

const (
	ccSpace charClass = iota
	ccSep
	ccWord
)

const wordSeparators = "`~!@#$%^&*()-=+[{]}\\|;:'\",.<>/?"

func classOf(r rune) charClass {
	switch {
	case r == ' ' || r == '\t' || r == '\n' || r == 0:
		return ccSpace
	case strings.ContainsRune(wordSeparators, r):
		return ccSep
	}
	return ccWord
}

func isWordRune(r rune) bool { return classOf(r) == ccWord }

// WordAt returns the range of the word at p (or touching p on its left).
func (v *EditorView) WordAt(p buffer.Pos) (buffer.Range, bool) {
	l := v.buf().Line(p.Line)
	s, e := p.Col, p.Col
	if s < len(l) && isWordRune(l[s]) || s > 0 && s <= len(l) && isWordRune(l[s-1]) {
		for s > 0 && isWordRune(l[s-1]) {
			s--
		}
		for e < len(l) && isWordRune(l[e]) {
			e++
		}
		if e > s {
			return buffer.Range{Start: buffer.Pos{Line: p.Line, Col: s}, End: buffer.Pos{Line: p.Line, Col: e}}, true
		}
	}
	return buffer.Range{Start: p, End: p}, false
}

// wordLeft finds the position for ctrl+left.
func (v *EditorView) wordLeft(p buffer.Pos) buffer.Pos {
	b := v.buf()
	if p.Col == 0 {
		if p.Line == 0 {
			return p
		}
		return buffer.Pos{Line: p.Line - 1, Col: b.LineLen(p.Line - 1)}
	}
	l := b.Line(p.Line)
	c := p.Col
	for c > 0 && classOf(l[c-1]) == ccSpace {
		c--
	}
	if c == 0 {
		return buffer.Pos{Line: p.Line, Col: 0}
	}
	cls := classOf(l[c-1])
	for c > 0 && classOf(l[c-1]) == cls {
		c--
	}
	return buffer.Pos{Line: p.Line, Col: c}
}

// wordRight finds the position for ctrl+right (end of the next word).
func (v *EditorView) wordRight(p buffer.Pos) buffer.Pos {
	b := v.buf()
	l := b.Line(p.Line)
	if p.Col >= len(l) {
		if p.Line >= b.LineCount()-1 {
			return p
		}
		return buffer.Pos{Line: p.Line + 1, Col: 0}
	}
	c := p.Col
	for c < len(l) && classOf(l[c]) == ccSpace {
		c++
	}
	if c == len(l) {
		return buffer.Pos{Line: p.Line, Col: c}
	}
	cls := classOf(l[c])
	for c < len(l) && classOf(l[c]) == cls {
		c++
	}
	return buffer.Pos{Line: p.Line, Col: c}
}

// wordStartRight finds the start of the next word (used by delete word right).
func (v *EditorView) wordEndForDelete(p buffer.Pos) buffer.Pos {
	return v.wordRight(p)
}

//#endregion

//#region Layout: wrapping, folding, visual rows

func (v *EditorView) tabSize() int {
	if v.buf().TabSize <= 0 {
		return 4
	}
	return v.buf().TabSize
}

// visualX returns the visual column of col on line (tabs expanded).
func (v *EditorView) visualX(line, col int) int {
	l := v.buf().Line(line)
	ts := v.tabSize()
	x := 0
	for i := 0; i < col && i < len(l); i++ {
		x += runeAdvance(l[i], x, ts)
	}
	if col > len(l) {
		x += col - len(l)
	}
	return x
}

func runeAdvance(r rune, x, ts int) int {
	if r == '\t' {
		return ts - x%ts
	}
	return RuneWidth(r)
}

// colAtVisualX returns the column nearest to visual x on line, starting at segment start col.
func (v *EditorView) colAtVisualX(line, startCol, endCol, targetX, baseX int) int {
	l := v.buf().Line(line)
	ts := v.tabSize()
	x := baseX
	for i := startCol; i < endCol && i < len(l); i++ {
		w := runeAdvance(l[i], x, ts)
		if x+w > targetX {
			if targetX-x > w/2 && w > 1 {
				return i + 1
			}
			return i
		}
		x += w
	}
	if endCol > len(l) {
		return len(l)
	}
	return endCol
}

func (v *EditorView) textWidth() int {
	w := v.rect.W
	if w <= 0 {
		w = 80
	}
	return w
}

var zeroSeg = []int{0}

// segments returns the start columns of each wrapped row of line.
func (v *EditorView) segments(line int) []int {
	if !v.wrap {
		return zeroSeg
	}
	width := v.textWidth() - 1
	if width < 10 {
		width = 10
	}
	if v.wrapCache == nil || v.wrapCacheVersion != v.buf().Version() || v.wrapCacheWidth != width {
		v.wrapCache = map[int][]int{}
		v.wrapIndent = map[int]int{}
		v.wrapCacheVersion = v.buf().Version()
		v.wrapCacheWidth = width
	}
	if s, ok := v.wrapCache[line]; ok {
		return s
	}
	l := v.buf().Line(line)
	ts := v.tabSize()
	// continuation indent equals the line's indentation
	indent := 0
	for _, r := range l {
		if r == ' ' {
			indent++
		} else if r == '\t' {
			indent += ts - indent%ts
		} else {
			break
		}
	}
	if indent > width/2 {
		indent = 0
	}
	v.wrapIndent[line] = indent
	segs := []int{0}
	x := 0
	limit := width
	lastBreak := -1
	segStart := 0
	for i := 0; i < len(l); i++ {
		w := runeAdvance(l[i], x, ts)
		if x+w > limit && i > segStart {
			brk := i
			if lastBreak > segStart && lastBreak < i {
				brk = lastBreak
			}
			segs = append(segs, brk)
			segStart = brk
			// recompute x for the new segment
			x = indent
			limit = width
			for j := brk; j < i; j++ {
				x += runeAdvance(l[j], x, ts)
			}
			lastBreak = -1
		}
		x += w
		if l[i] == ' ' || l[i] == '\t' {
			lastBreak = i + 1
		} else if i+1 < len(l) && classOf(l[i]) == ccSep && classOf(l[i+1]) == ccWord {
			lastBreak = i + 1
		}
	}
	v.wrapCache[line] = segs
	return segs
}

func (v *EditorView) segIndent(line, seg int) int {
	if seg == 0 || !v.wrap {
		return 0
	}
	v.segments(line)
	return v.wrapIndent[line]
}

// segOf returns which wrapped segment contains col.
func (v *EditorView) segOf(line, col int) int {
	segs := v.segments(line)
	for i := len(segs) - 1; i >= 0; i-- {
		if col >= segs[i] {
			// a column exactly at a break belongs to the next segment unless it is the end
			return i
		}
	}
	return 0
}

func (v *EditorView) segEnd(line, seg int) int {
	segs := v.segments(line)
	if seg+1 < len(segs) {
		return segs[seg+1]
	}
	return v.buf().LineLen(line)
}

// isHidden reports whether a line is inside a folded region.
func (v *EditorView) isHidden(line int) bool {
	for _, f := range v.folds {
		if line > f.Start && line <= f.End {
			return true
		}
	}
	return false
}

// foldAt returns the fold starting at line.
func (v *EditorView) foldAt(line int) (Fold, bool) {
	for _, f := range v.folds {
		if f.Start == line {
			return f, true
		}
	}
	return Fold{}, false
}

func (v *EditorView) nextVisibleLine(line int) int {
	n := line + 1
	for _, f := range v.folds {
		if f.Start == line && f.End >= n {
			n = f.End + 1
		}
	}
	for v.isHidden(n) && n < v.buf().LineCount() {
		n++
	}
	return n
}

func (v *EditorView) prevVisibleLine(line int) int {
	n := line - 1
	for n > 0 && v.isHidden(n) {
		n--
	}
	return n
}

func (v *EditorView) nextRow(p vpos) (vpos, bool) {
	if p.seg+1 < len(v.segments(p.line)) {
		return vpos{p.line, p.seg + 1}, true
	}
	n := v.nextVisibleLine(p.line)
	if n >= v.buf().LineCount() {
		return p, false
	}
	return vpos{n, 0}, true
}

func (v *EditorView) prevRow(p vpos) (vpos, bool) {
	if p.seg > 0 {
		return vpos{p.line, p.seg - 1}, true
	}
	if p.line == 0 {
		return p, false
	}
	n := v.prevVisibleLine(p.line)
	if n < 0 {
		return p, false
	}
	return vpos{n, len(v.segments(n)) - 1}, true
}

// rowOfPos returns the visual row (line, seg) containing p.
func (v *EditorView) rowOfPos(p buffer.Pos) vpos {
	line := p.Line
	for v.isHidden(line) && line > 0 {
		line--
	}
	if line != p.Line {
		return vpos{line, 0}
	}
	return vpos{line, v.segOf(line, p.Col)}
}

// rowsBetween counts visual rows from a to b (b after a). Stops at limit.
func (v *EditorView) rowsBetween(a, b vpos, limit int) int {
	n := 0
	cur := a
	for n < limit {
		if cur == b {
			return n
		}
		next, ok := v.nextRow(cur)
		if !ok {
			return n
		}
		cur = next
		n++
	}
	return n
}

func vposLess(a, b vpos) bool {
	if a.line != b.line {
		return a.line < b.line
	}
	return a.seg < b.seg
}

// totalRows returns the number of visual rows in the document.
func (v *EditorView) totalRows() int {
	if !v.wrap && len(v.folds) == 0 {
		return v.buf().LineCount()
	}
	n := 0
	for line := 0; line < v.buf().LineCount(); line = v.nextVisibleLine(line) {
		n += len(v.segments(line))
	}
	return n
}

// rowIndex returns the index of a visual row from the document start.
func (v *EditorView) rowIndex(p vpos) int {
	if !v.wrap && len(v.folds) == 0 {
		return p.line
	}
	n := 0
	for line := 0; line < p.line && line < v.buf().LineCount(); line = v.nextVisibleLine(line) {
		n += len(v.segments(line))
	}
	return n + p.seg
}

// rowAtIndex returns the visual row at index i.
func (v *EditorView) rowAtIndex(i int) vpos {
	if i <= 0 {
		return vpos{0, 0}
	}
	if !v.wrap && len(v.folds) == 0 {
		return vpos{min(i, v.buf().LineCount()-1), 0}
	}
	n := 0
	last := vpos{0, 0}
	for line := 0; line < v.buf().LineCount(); line = v.nextVisibleLine(line) {
		segs := len(v.segments(line))
		if n+segs > i {
			return vpos{line, i - n}
		}
		n += segs
		last = vpos{line, segs - 1}
	}
	return last
}

//#endregion

//#region Scrolling

func (v *EditorView) top() vpos { return vpos{v.topLine, v.topSeg} }

func (v *EditorView) setTop(p vpos) {
	v.topLine, v.topSeg = p.line, p.seg
}

// ScrollBy scrolls by n visual rows.
func (v *EditorView) ScrollBy(n int) {
	cur := v.top()
	if n > 0 {
		maxIdx := v.maxTopIndex()
		idx := v.rowIndex(cur)
		if idx+n > maxIdx {
			n = maxIdx - idx
		}
		for i := 0; i < n; i++ {
			next, ok := v.nextRow(cur)
			if !ok {
				break
			}
			cur = next
		}
	} else {
		for i := 0; i < -n; i++ {
			prev, ok := v.prevRow(cur)
			if !ok {
				break
			}
			cur = prev
		}
	}
	v.setTop(cur)
}

func (v *EditorView) maxTopIndex() int {
	total := v.totalRows()
	h := max(v.rect.H, 1)
	if v.app.settings.Bool("editor.scrollBeyondLastLine", "") {
		return max(total-1, 0)
	}
	return max(total-h, 0)
}

// ensureVisible scrolls so the primary cursor is visible.
func (v *EditorView) ensureVisible() {
	v.ensurePosVisible(v.Cursor(), false)
}

func (v *EditorView) ensurePosVisible(p buffer.Pos, center bool) {
	h := v.rect.H
	if h <= 0 {
		h = 20
	}
	margin := v.app.settings.Int("editor.cursorSurroundingLines", "", 0)
	if margin*2 >= h {
		margin = 0
	}
	// unfold when the cursor lands in a fold
	if v.isHidden(p.Line) {
		v.unfoldContaining(p.Line)
	}
	row := v.rowOfPos(p)
	top := v.top()
	sticky := v.stickyRows
	if center {
		cur := row
		for i := 0; i < h/2; i++ {
			prev, ok := v.prevRow(cur)
			if !ok {
				break
			}
			cur = prev
		}
		v.setTop(cur)
	} else if vposLess(row, top) || v.rowsBetween(top, row, margin+sticky) < margin+sticky && vposLess(vpos{0, 0}, top) {
		cur := row
		for i := 0; i < margin+sticky; i++ {
			prev, ok := v.prevRow(cur)
			if !ok {
				break
			}
			cur = prev
		}
		if vposLess(cur, top) || cur == top {
			v.setTop(cur)
		}
	} else {
		dist := v.rowsBetween(top, row, h+1)
		if dist >= h-margin {
			cur := row
			for i := 0; i < h-1-margin; i++ {
				prev, ok := v.prevRow(cur)
				if !ok {
					break
				}
				cur = prev
			}
			v.setTop(cur)
		}
	}
	if !v.wrap {
		x := v.visualX(p.Line, p.Col)
		w := v.textWidth()
		if x < v.leftCol {
			v.leftCol = max(x-4, 0)
		} else if x >= v.leftCol+w-1 {
			v.leftCol = x - w + 2
		}
	} else {
		v.leftCol = 0
	}
}

//#endregion

//#region Folding

var regionStartRe = regexp.MustCompile(`^\s*(?://|#|--|/\*|<!--|;|%)\s*#?region\b`)
var regionEndRe = regexp.MustCompile(`^\s*(?://|#|--|/\*|<!--|;|%)\s*#?endregion\b`)

// regionRangeAt returns the range of a "#region" marker block starting at line.
func (v *EditorView) regionRangeAt(line int) (Fold, bool) {
	b := v.buf()
	if !regionStartRe.MatchString(b.LineString(line)) {
		return Fold{}, false
	}
	depth := 0
	for l := line + 1; l < b.LineCount() && l < line+20000; l++ {
		s := b.LineString(l)
		if regionStartRe.MatchString(s) {
			depth++
		} else if regionEndRe.MatchString(s) {
			if depth == 0 {
				return Fold{line, l}, true
			}
			depth--
		}
	}
	return Fold{}, false
}

// foldRangeAt computes the fold range starting at line from region markers or indentation.
func (v *EditorView) foldRangeAt(line int) (Fold, bool) {
	b := v.buf()
	if line >= b.LineCount()-1 {
		return Fold{}, false
	}
	if f, ok := v.regionRangeAt(line); ok {
		return f, true
	}
	ind := v.indentLevel(line)
	if ind < 0 {
		return Fold{}, false
	}
	end := -1
	for i := line + 1; i < b.LineCount(); i++ {
		li := v.indentLevel(i)
		if li < 0 {
			continue
		}
		if li <= ind {
			break
		}
		end = i
	}
	if end <= line {
		return Fold{}, false
	}
	return Fold{line, end}, true
}

// indentLevel returns the visual indent of a line, or -1 for blank lines.
func (v *EditorView) indentLevel(line int) int {
	l := v.buf().Line(line)
	x := 0
	ts := v.tabSize()
	for _, r := range l {
		switch r {
		case ' ':
			x++
		case '\t':
			x += ts - x%ts
		default:
			return x
		}
	}
	return -1
}

// Fold folds the innermost region containing line.
func (v *EditorView) FoldAt(line int) bool {
	for l := line; l >= 0; l-- {
		if f, ok := v.foldRangeAt(l); ok && f.End >= line {
			if _, already := v.foldAt(l); already {
				continue
			}
			v.addFold(f)
			return true
		}
	}
	return false
}

func (v *EditorView) addFold(f Fold) {
	v.folds = append(v.folds, f)
	sort.Slice(v.folds, func(i, j int) bool { return v.folds[i].Start < v.folds[j].Start })
	// move cursors out of hidden lines
	for i := range v.sels {
		if v.isHidden(v.sels[i].Active.Line) {
			p := buffer.Pos{Line: f.Start, Col: v.buf().LineLen(f.Start)}
			v.sels[i] = buffer.Caret(p)
		}
	}
	v.normalize()
	if v.isHidden(v.topLine) {
		v.topLine = f.Start
		v.topSeg = 0
	}
}

// Unfold removes the fold starting at or containing line.
func (v *EditorView) Unfold(line int) bool {
	for i, f := range v.folds {
		if f.Start == line {
			v.folds = append(v.folds[:i], v.folds[i+1:]...)
			return true
		}
	}
	return v.unfoldContaining(line)
}

func (v *EditorView) unfoldContaining(line int) bool {
	changed := false
	out := v.folds[:0]
	for _, f := range v.folds {
		if line > f.Start && line <= f.End {
			changed = true
			continue
		}
		out = append(out, f)
	}
	v.folds = out
	return changed
}

// ToggleFold folds or unfolds at line.
func (v *EditorView) ToggleFold(line int) {
	if _, ok := v.foldAt(line); ok {
		v.Unfold(line)
		return
	}
	if f, ok := v.foldRangeAt(line); ok {
		v.addFold(f)
	}
}

// FoldAll folds every top-level-or-deeper region at or above the given level (0 = all).
func (v *EditorView) FoldAll(level int) {
	v.folds = nil
	b := v.buf()
	var stack []int
	for line := 0; line < b.LineCount(); line++ {
		f, ok := v.foldRangeAt(line)
		ind := v.indentLevel(line)
		for len(stack) > 0 && ind >= 0 && v.indentLevel(stack[len(stack)-1]) >= ind {
			stack = stack[:len(stack)-1]
		}
		if !ok {
			continue
		}
		depth := len(stack) + 1
		stack = append(stack, line)
		if level == 0 || depth == level {
			v.folds = append(v.folds, f)
		}
	}
	for i := range v.sels {
		if v.isHidden(v.sels[i].Active.Line) {
			for _, f := range v.folds {
				if v.sels[i].Active.Line > f.Start && v.sels[i].Active.Line <= f.End {
					v.sels[i] = buffer.Caret(buffer.Pos{Line: f.Start, Col: 0})
					break
				}
			}
		}
	}
	v.normalize()
	if v.isHidden(v.topLine) {
		v.topLine = 0
		v.topSeg = 0
	}
}

//#endregion

//#region Movement

func (v *EditorView) moveEach(extend bool, fn func(i int, s buffer.Selection) buffer.Pos) {
	for i, s := range v.sels {
		p := fn(i, s)
		if extend {
			v.sels[i].Active = p
		} else {
			v.sels[i] = buffer.Caret(p)
		}
	}
	v.normalize()
	v.ensureVisible()
}

func (v *EditorView) resetPrefX() {
	for i := range v.prefX {
		v.prefX[i] = -1
	}
}

// MoveLeft moves cursors left (or collapses selections).
func (v *EditorView) MoveLeft(extend, word bool) {
	b := v.buf()
	v.moveEach(extend, func(i int, s buffer.Selection) buffer.Pos {
		if !extend && !s.Empty() && !word {
			return s.Range().Start
		}
		p := s.Active
		if word {
			return v.wordLeft(p)
		}
		if p.Col > 0 {
			return buffer.Pos{Line: p.Line, Col: p.Col - 1}
		}
		if p.Line > 0 {
			pl := v.prevVisibleLine(p.Line)
			return buffer.Pos{Line: pl, Col: b.LineLen(pl)}
		}
		return p
	})
	v.resetPrefX()
}

// MoveRight moves cursors right (or collapses selections).
func (v *EditorView) MoveRight(extend, word bool) {
	b := v.buf()
	v.moveEach(extend, func(i int, s buffer.Selection) buffer.Pos {
		if !extend && !s.Empty() && !word {
			return s.Range().End
		}
		p := s.Active
		if word {
			return v.wordRight(p)
		}
		if p.Col < b.LineLen(p.Line) {
			if f, ok := v.foldAt(p.Line); ok && p.Col == b.LineLen(p.Line)-1 {
				_ = f
			}
			return buffer.Pos{Line: p.Line, Col: p.Col + 1}
		}
		nl := v.nextVisibleLine(p.Line)
		if nl < b.LineCount() {
			return buffer.Pos{Line: nl, Col: 0}
		}
		return p
	})
	v.resetPrefX()
}

// visualXInRow returns the x of col within its wrapped segment (including continuation indent).
func (v *EditorView) visualXInRow(line, col int) int {
	seg := v.segOf(line, col)
	segs := v.segments(line)
	return v.visualX(line, col) - v.visualX(line, segs[seg]) + v.segIndent(line, seg)
}

func (v *EditorView) posAtRowX(row vpos, x int) buffer.Pos {
	segs := v.segments(row.line)
	start := segs[row.seg]
	end := v.segEnd(row.line, row.seg)
	base := v.visualX(row.line, start)
	indent := v.segIndent(row.line, row.seg)
	target := base + x - indent
	col := v.colAtVisualX(row.line, start, end, target, base)
	if row.seg+1 < len(segs) && col >= end {
		col = end - 1
		if col < start {
			col = start
		}
	}
	return buffer.Pos{Line: row.line, Col: col}
}

// MoveVertical moves cursors up (dy<0) or down by visual rows.
func (v *EditorView) MoveVertical(dy int, extend bool) {
	b := v.buf()
	for i, s := range v.sels {
		if !extend && !s.Empty() && dy != 0 {
			// collapse to the side we move towards, then move from there
			if dy < 0 {
				s = buffer.Caret(s.Range().Start)
			} else {
				s = buffer.Caret(s.Range().End)
			}
		}
		p := s.Active
		if v.prefX[i] < 0 {
			v.prefX[i] = v.visualXInRow(p.Line, p.Col)
		}
		row := v.rowOfPos(p)
		moved := 0
		for moved < abs(dy) {
			var ok bool
			var next vpos
			if dy < 0 {
				next, ok = v.prevRow(row)
			} else {
				next, ok = v.nextRow(row)
			}
			if !ok {
				break
			}
			row = next
			moved++
		}
		var np buffer.Pos
		if moved == 0 {
			// at document edge: go to start/end
			if dy < 0 {
				np = buffer.Pos{}
			} else {
				np = b.EndPos()
			}
			v.prefX[i] = -1
		} else {
			np = v.posAtRowX(row, v.prefX[i])
		}
		if extend {
			v.sels[i].Active = np
		} else {
			v.sels[i] = buffer.Caret(np)
		}
	}
	px := append([]int{}, v.prefX...)
	v.normalize()
	if len(px) == len(v.prefX) {
		v.prefX = px
	}
	v.ensureVisible()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// MoveHome moves to the first non-blank character, toggling with column 0.
func (v *EditorView) MoveHome(extend bool) {
	b := v.buf()
	v.moveEach(extend, func(i int, s buffer.Selection) buffer.Pos {
		p := s.Active
		if v.wrap {
			seg := v.segOf(p.Line, p.Col)
			if seg > 0 {
				start := v.segments(p.Line)[seg]
				if p.Col != start {
					return buffer.Pos{Line: p.Line, Col: start}
				}
			}
		}
		fnw := b.FirstNonWhitespace(p.Line)
		if p.Col == fnw {
			return buffer.Pos{Line: p.Line, Col: 0}
		}
		return buffer.Pos{Line: p.Line, Col: fnw}
	})
	v.resetPrefX()
}

// MoveEnd moves to the end of the line (or wrapped segment).
func (v *EditorView) MoveEnd(extend bool) {
	b := v.buf()
	v.moveEach(extend, func(i int, s buffer.Selection) buffer.Pos {
		p := s.Active
		if v.wrap {
			seg := v.segOf(p.Line, p.Col)
			segs := v.segments(p.Line)
			if seg+1 < len(segs) {
				end := segs[seg+1] - 1
				if p.Col != end {
					return buffer.Pos{Line: p.Line, Col: end}
				}
			}
		}
		return buffer.Pos{Line: p.Line, Col: b.LineLen(p.Line)}
	})
	for i := range v.prefX {
		v.prefX[i] = 1 << 30
	}
}

// MoveDocStart moves to the start of the document.
func (v *EditorView) MoveDocStart(extend bool) {
	p := v.Primary()
	if extend {
		v.SetSelections([]buffer.Selection{{Anchor: p.Anchor, Active: buffer.Pos{}}}, 0)
	} else {
		v.SetCursor(buffer.Pos{})
	}
	v.ensureVisible()
}

// MoveDocEnd moves to the end of the document.
func (v *EditorView) MoveDocEnd(extend bool) {
	p := v.Primary()
	end := v.buf().EndPos()
	if extend {
		v.SetSelections([]buffer.Selection{{Anchor: p.Anchor, Active: end}}, 0)
	} else {
		v.SetCursor(end)
	}
	v.ensureVisible()
}

// PageMove moves the cursor and the viewport by a page.
func (v *EditorView) PageMove(dir int, extend bool) {
	h := max(v.rect.H-1, 1)
	v.ScrollBy(dir * h)
	v.MoveVertical(dir*h, extend)
}

//#endregion
