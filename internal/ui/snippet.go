package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tuicode/internal/buffer"
)

type tabstop struct {
	index      int
	start, end int // rune offsets in the expanded text
	choices    []string
}

// parseSnippet expands LSP/TextMate snippet syntax into text plus tab stops.
func parseSnippet(src string, vars func(string) (string, bool)) (string, []tabstop) {
	var out []rune
	var stops []tabstop
	rs := []rune(src)
	var parse func(i int, stopAt rune) int
	parse = func(i int, stopAt rune) int {
		for i < len(rs) {
			c := rs[i]
			if stopAt != 0 && c == stopAt {
				return i
			}
			if c == '\\' && i+1 < len(rs) && strings.ContainsRune(`$}\,|`, rs[i+1]) {
				out = append(out, rs[i+1])
				i += 2
				continue
			}
			if c != '$' || i+1 >= len(rs) {
				out = append(out, c)
				i++
				continue
			}
			n := rs[i+1]
			switch {
			case n >= '0' && n <= '9':
				j := i + 1
				for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
					j++
				}
				idx, _ := strconv.Atoi(string(rs[i+1 : j]))
				stops = append(stops, tabstop{index: idx, start: len(out), end: len(out)})
				i = j
			case n == '{':
				j := i + 2
				k := j
				for k < len(rs) && rs[k] >= '0' && rs[k] <= '9' {
					k++
				}
				if k > j {
					idx, _ := strconv.Atoi(string(rs[j:k]))
					if k < len(rs) && rs[k] == '}' {
						stops = append(stops, tabstop{index: idx, start: len(out), end: len(out)})
						i = k + 1
						continue
					}
					if k < len(rs) && rs[k] == ':' {
						start := len(out)
						end := parse(k+1, '}')
						stops = append(stops, tabstop{index: idx, start: start, end: len(out)})
						i = end + 1
						continue
					}
					if k < len(rs) && rs[k] == '|' {
						// choice: ${1|a,b,c|}
						e := k + 1
						var choices []string
						cur := []rune{}
						for e < len(rs) && rs[e] != '|' {
							if rs[e] == '\\' && e+1 < len(rs) {
								cur = append(cur, rs[e+1])
								e += 2
								continue
							}
							if rs[e] == ',' {
								choices = append(choices, string(cur))
								cur = cur[:0]
							} else {
								cur = append(cur, rs[e])
							}
							e++
						}
						choices = append(choices, string(cur))
						start := len(out)
						if len(choices) > 0 {
							out = append(out, []rune(choices[0])...)
						}
						stops = append(stops, tabstop{index: idx, start: start, end: len(out), choices: choices})
						i = e + 2
						continue
					}
				}
				// variable: ${NAME} or ${NAME:default}
				k = j
				for k < len(rs) && (rs[k] == '_' || rs[k] >= 'A' && rs[k] <= 'Z' || rs[k] >= 'a' && rs[k] <= 'z' || rs[k] >= '0' && rs[k] <= '9') {
					k++
				}
				name := string(rs[j:k])
				if val, ok := vars(name); ok {
					out = append(out, []rune(val)...)
					if k < len(rs) && rs[k] == ':' {
						// skip default
						depth := 0
						for k < len(rs) {
							if rs[k] == '{' {
								depth++
							} else if rs[k] == '}' {
								if depth == 0 {
									break
								}
								depth--
							}
							k++
						}
					} else {
						for k < len(rs) && rs[k] != '}' {
							k++
						}
					}
					i = k + 1
					continue
				}
				if k < len(rs) && rs[k] == ':' {
					end := parse(k+1, '}')
					i = end + 1
					continue
				}
				for k < len(rs) && rs[k] != '}' {
					k++
				}
				out = append(out, []rune(name)...)
				i = k + 1
			case n == '_' || n >= 'A' && n <= 'Z' || n >= 'a' && n <= 'z':
				j := i + 1
				for j < len(rs) && (rs[j] == '_' || rs[j] >= 'A' && rs[j] <= 'Z' || rs[j] >= 'a' && rs[j] <= 'z' || rs[j] >= '0' && rs[j] <= '9') {
					j++
				}
				name := string(rs[i+1 : j])
				if val, ok := vars(name); ok {
					out = append(out, []rune(val)...)
				} else {
					out = append(out, []rune(name)...)
				}
				i = j
			default:
				out = append(out, c)
				i++
			}
		}
		return i
	}
	parse(0, 0)
	return string(out), stops
}

// snippetVars resolves TextMate snippet variables.
func (v *EditorView) snippetVars(name string) (string, bool) {
	b := v.buf()
	switch name {
	case "TM_FILENAME":
		return filepath.Base(b.Path), true
	case "TM_FILENAME_BASE":
		base := filepath.Base(b.Path)
		return strings.TrimSuffix(base, filepath.Ext(base)), true
	case "TM_DIRECTORY":
		return filepath.Dir(b.Path), true
	case "TM_FILEPATH":
		return b.Path, true
	case "RELATIVE_FILEPATH":
		return v.app.relPath(b.Path), true
	case "TM_LINE_INDEX":
		return strconv.Itoa(v.Cursor().Line), true
	case "TM_LINE_NUMBER":
		return strconv.Itoa(v.Cursor().Line + 1), true
	case "TM_CURRENT_LINE":
		return b.LineString(v.Cursor().Line), true
	case "TM_CURRENT_WORD":
		if wr, ok := v.WordAt(v.Cursor()); ok {
			return b.TextRange(wr), true
		}
		return "", true
	case "TM_SELECTED_TEXT", "SELECTION":
		return v.SelectedText(), true
	case "CLIPBOARD":
		return v.app.clipboardText, true
	case "WORKSPACE_NAME":
		return v.app.rootName(), true
	case "CURRENT_YEAR":
		return time.Now().Format("2006"), true
	case "CURRENT_YEAR_SHORT":
		return time.Now().Format("06"), true
	case "CURRENT_MONTH":
		return time.Now().Format("01"), true
	case "CURRENT_DATE":
		return time.Now().Format("02"), true
	case "CURRENT_HOUR":
		return time.Now().Format("15"), true
	case "CURRENT_MINUTE":
		return time.Now().Format("04"), true
	case "CURRENT_SECOND":
		return time.Now().Format("05"), true
	case "CURRENT_DAY_NAME":
		return time.Now().Format("Monday"), true
	case "CURRENT_MONTH_NAME":
		return time.Now().Format("January"), true
	case "LINE_COMMENT":
		return v.doc.Lang.LineComment, true
	case "BLOCK_COMMENT_START":
		return v.doc.Lang.BlockComment[0], true
	case "BLOCK_COMMENT_END":
		return v.doc.Lang.BlockComment[1], true
	case "UUID":
		return pseudoUUID(), true
	case "RANDOM":
		return strconv.Itoa(int(time.Now().UnixNano() % 1000000)), true
	}
	return "", false
}

func pseudoUUID() string {
	f, err := os.Open("/proc/sys/kernel/random/uuid")
	if err == nil {
		defer f.Close()
		b := make([]byte, 36)
		if n, _ := f.Read(b); n == 36 {
			return string(b)
		}
	}
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}

// SnippetSession tracks tab stops of an inserted snippet.
type SnippetSession struct {
	stops   map[int][]buffer.Range
	order   []int
	current int
}

func (s *SnippetSession) transform(ch buffer.Change) {
	for idx, rs := range s.stops {
		for i := range rs {
			rs[i].Start = buffer.TransformPos(rs[i].Start, ch.Old, ch.NewEnd, false)
			rs[i].End = buffer.TransformPos(rs[i].End, ch.Old, ch.NewEnd, true)
		}
		s.stops[idx] = rs
	}
}

func (s *SnippetSession) placeholders() []buffer.Range {
	var out []buffer.Range
	for i, idx := range s.order {
		if i < s.current || idx == 0 {
			continue
		}
		out = append(out, s.stops[idx]...)
	}
	return out
}

func (s *SnippetSession) selectCurrent(v *EditorView) {
	if s.current >= len(s.order) {
		v.snippet = nil
		return
	}
	idx := s.order[s.current]
	rs := s.stops[idx]
	sels := make([]buffer.Selection, len(rs))
	for i, r := range rs {
		sels[i] = buffer.Selection{Anchor: r.Start, Active: r.End}
	}
	v.SetSelections(sels, 0)
	v.ensureVisible()
	if idx == 0 {
		v.snippet = nil
	}
}

func (s *SnippetSession) next(v *EditorView) {
	s.current++
	s.selectCurrent(v)
}

func (s *SnippetSession) prev(v *EditorView) {
	if s.current > 0 {
		s.current--
	}
	s.selectCurrent(v)
}

// insertSnippet replaces r with an expanded snippet and starts a tab stop session.
func (v *EditorView) insertSnippet(r buffer.Range, snippet string) {
	b := v.buf()
	indent := b.LeadingWhitespace(r.Start.Line)
	text, stops := parseSnippet(snippet, v.snippetVars)
	// re-indent continuation lines and convert tabs to the buffer's indentation
	unit := b.IndentString()
	var sb strings.Builder
	offsetMap := []int{}
	rs := []rune(text)
	newOff := 0
	for i, c := range rs {
		offsetMap = append(offsetMap, newOff)
		switch {
		case c == '\n':
			sb.WriteRune(c)
			sb.WriteString(indent)
			newOff += 1 + len([]rune(indent))
		case c == '\t' && unit != "\t":
			sb.WriteString(unit)
			newOff += len([]rune(unit))
		default:
			sb.WriteRune(c)
			newOff++
		}
		_ = i
	}
	offsetMap = append(offsetMap, newOff)
	text = sb.String()
	for i := range stops {
		stops[i].start = offsetMap[stops[i].start]
		stops[i].end = offsetMap[stops[i].end]
	}
	res := v.buf().ApplyEdits([]buffer.Edit{{Range: r, Text: text}}, v.sels, "")
	start := res[0].Start
	toPos := func(off int) buffer.Pos {
		line, col := start.Line, start.Col
		for i, c := range []rune(text) {
			if i >= off {
				break
			}
			if c == '\n' {
				line++
				col = 0
			} else {
				col++
			}
		}
		return buffer.Pos{Line: line, Col: col}
	}
	if len(stops) == 0 {
		v.SetCursor(res[0].End)
		v.buf().SetAfterSelections(v.sels)
		v.ensureVisible()
		return
	}
	sess := &SnippetSession{stops: map[int][]buffer.Range{}}
	hasZero := false
	for _, ts := range stops {
		sess.stops[ts.index] = append(sess.stops[ts.index], buffer.Range{Start: toPos(ts.start), End: toPos(ts.end)})
		if ts.index == 0 {
			hasZero = true
		}
	}
	if !hasZero {
		sess.stops[0] = []buffer.Range{{Start: res[0].End, End: res[0].End}}
	}
	for idx := range sess.stops {
		if idx != 0 {
			sess.order = append(sess.order, idx)
		}
	}
	sort.Ints(sess.order)
	sess.order = append(sess.order, 0)
	if len(sess.order) == 1 {
		v.SetCursor(sess.stops[0][0].Start)
		v.buf().SetAfterSelections(v.sels)
		return
	}
	v.snippet = sess
	sess.selectCurrent(v)
	v.buf().SetAfterSelections(v.sels)
}
