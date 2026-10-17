package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

type searchMatch struct {
	line    int
	col     int // rune column
	length  int // runes
	text    string
	preview string
}

type searchFile struct {
	path      string
	matches   []searchMatch
	collapsed bool
}

type searchRow struct {
	file  *searchFile
	match int // -1 for the file row
}

// SearchView is the Find in Files side bar view.
type SearchView struct {
	app         *App
	query       TextInput
	replace     TextInput
	include     TextInput
	exclude     TextInput
	focusField  int // 0 query, 1 replace, 2 include, 3 exclude, 4 results
	showReplace bool
	showDetails bool
	caseSens    bool
	wholeWord   bool
	regex       bool
	results     []*searchFile
	rows        []searchRow
	selected    int
	scroll      int
	total       int
	running     bool
	cancel      context.CancelFunc
	message     string
	debounce    *time.Timer
	refsTitle   string
	rect        Rect
	listRect    Rect
	truncated   bool
}

func newSearchView(a *App) *SearchView {
	s := &SearchView{app: a}
	s.query = newTextInput(a, "Search")
	s.replace = newTextInput(a, "Replace")
	s.include = newTextInput(a, "e.g. *.ts, src/**/include")
	s.exclude = newTextInput(a, "e.g. *.ts, src/**/exclude")
	s.query.OnChange = s.scheduleRun
	s.include.OnChange = s.scheduleRun
	s.exclude.OnChange = s.scheduleRun
	s.replace.OnChange = func() {}
	return s
}

func (s *SearchView) focusQuery() {
	s.focusField = 0
	s.query.SelectAll()
}

func (s *SearchView) toggleDetails() {
	s.showDetails = !s.showDetails
}

func (s *SearchView) setInclude(p string) {
	s.include.SetText(p)
	s.showDetails = true
}

func (s *SearchView) focusedInput() *TextInput {
	switch s.focusField {
	case 0:
		return &s.query
	case 1:
		if s.showReplace {
			return &s.replace
		}
	case 2:
		if s.showDetails {
			return &s.include
		}
	case 3:
		if s.showDetails {
			return &s.exclude
		}
	}
	return nil
}

// findInFiles opens the search view seeded with the editor selection.
func (a *App) findInFiles(replace bool) {
	s := a.searchV
	if v := a.ActiveView(); v != nil && v.special == nil && a.focus == FocusEditor {
		sel := v.Primary()
		if !sel.Empty() && sel.Range().Start.Line == sel.Range().End.Line {
			s.query.SetText(v.buf().TextRange(sel.Range()))
		} else if wr, ok := v.WordAt(sel.Active); ok && sel.Empty() {
			s.query.SetText(v.buf().TextRange(wr))
		}
	}
	if replace {
		s.showReplace = true
	}
	a.showSidebar("search")
	s.focusField = 0
	if replace && s.query.Text() != "" {
		s.focusField = 1
	}
	s.query.SelectAll()
	s.refsTitle = ""
	if s.query.Text() != "" {
		s.run()
	}
}

func (s *SearchView) scheduleRun() {
	if s.debounce != nil {
		s.debounce.Stop()
	}
	s.debounce = time.AfterFunc(300*time.Millisecond, func() {
		s.app.Post(func() { s.run() })
	})
}

func splitGlobs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// run starts a search.
func (s *SearchView) run() {
	a := s.app
	if s.cancel != nil {
		s.cancel()
	}
	s.refsTitle = ""
	q := s.query.Text()
	if q == "" || a.root == "" {
		s.results = nil
		s.rebuild()
		s.message = ""
		if a.root == "" && q != "" {
			s.message = "Open a folder to search in files."
		}
		return
	}
	s.query.AddHistory(q)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true
	s.message = ""
	root := a.root
	opts := searchOpts{query: q, regex: s.regex, caseSens: s.caseSens, wholeWord: s.wholeWord,
		include: splitGlobs(s.include.Text()), exclude: splitGlobs(s.exclude.Text())}
	for pat, v := range a.settings.Map("search.exclude") {
		if b, ok := v.(bool); ok && b {
			opts.exclude = append(opts.exclude, pat)
		}
	}
	gitRoot := a.gitRoot
	exclude := a.excludeMatcher()
	// open dirty documents are searched from memory
	dirty := map[string][]string{}
	for p, d := range a.docs {
		if d.Buf.Dirty() && d.Buf.Path != "" {
			dirty[p] = d.Buf.Lines()
		}
	}
	go func() {
		res, truncated, err := searchFiles(ctx, root, gitRoot, opts, exclude, dirty)
		a.Post(func() {
			if ctx.Err() != nil {
				return
			}
			s.running = false
			s.truncated = truncated
			if err != nil {
				s.message = err.Error()
			}
			s.results = res
			s.selected = 0
			s.scroll = 0
			s.rebuild()
		})
	}()
}

type searchOpts struct {
	query     string
	regex     bool
	caseSens  bool
	wholeWord bool
	include   []string
	exclude   []string
}

func (o searchOpts) compile() (*regexp.Regexp, error) {
	pat := o.query
	if !o.regex {
		pat = regexp.QuoteMeta(pat)
	}
	if o.wholeWord {
		pat = `\b` + pat + `\b`
	}
	if !o.caseSens {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, fmt.Errorf("Invalid regular expression: %v", err)
	}
	return re, nil
}

func globMatch(pat, rel string) bool {
	pat = strings.TrimPrefix(pat, "./")
	base := filepath.Base(rel)
	p := strings.TrimPrefix(pat, "**/")
	if ok, _ := filepath.Match(p, base); ok {
		return true
	}
	if ok, _ := filepath.Match(pat, rel); ok {
		return true
	}
	// folder patterns match everything below them
	trim := strings.TrimSuffix(strings.TrimSuffix(p, "/**"), "/")
	if strings.HasPrefix(rel, trim+"/") || rel == trim {
		return true
	}
	for _, seg := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if ok, _ := filepath.Match(trim, seg); ok {
			return true
		}
	}
	return false
}

const maxSearchResults = 20000

func searchFiles(ctx context.Context, root, gitRoot string, o searchOpts, exclude func(string, bool) bool, dirty map[string][]string) ([]*searchFile, bool, error) {
	re, err := o.compile()
	if err != nil {
		return nil, false, err
	}
	if rg, err := exec.LookPath("rg"); err == nil {
		return searchRipgrep(ctx, rg, root, o, re, dirty)
	}
	files := listFiles(root, gitRoot, exclude)
	var res []*searchFile
	total := 0
	for _, f := range files {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		rel, _ := filepath.Rel(root, f)
		if !includeOK(rel, o) {
			continue
		}
		var lines []string
		if dl, ok := dirty[f]; ok {
			lines = dl
		} else {
			data, err := os.ReadFile(f)
			if err != nil || len(data) > 10<<20 || isBinaryData(data) {
				continue
			}
			lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		}
		sf := &searchFile{path: f}
		for ln, l := range lines {
			for _, m := range re.FindAllStringIndex(l, -1) {
				if m[0] == m[1] {
					continue
				}
				sf.matches = append(sf.matches, searchMatch{line: ln, col: utf8.RuneCountInString(l[:m[0]]), length: utf8.RuneCountInString(l[m[0]:m[1]]), text: l})
				total++
			}
		}
		if len(sf.matches) > 0 {
			res = append(res, sf)
		}
		if total > maxSearchResults {
			return res, true, nil
		}
	}
	return res, false, nil
}

func includeOK(rel string, o searchOpts) bool {
	if len(o.include) > 0 {
		ok := false
		for _, p := range o.include {
			if globMatch(p, rel) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, p := range o.exclude {
		if globMatch(p, rel) {
			return false
		}
	}
	return true
}

func isBinaryData(b []byte) bool {
	n := min(len(b), 8000)
	for _, c := range b[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

func searchRipgrep(ctx context.Context, rg, root string, o searchOpts, re *regexp.Regexp, dirty map[string][]string) ([]*searchFile, bool, error) {
	args := []string{"--json", "--max-columns", "2000", "--max-filesize", "10M"}
	if !o.caseSens {
		args = append(args, "-i")
	} else {
		args = append(args, "-s")
	}
	if o.wholeWord {
		args = append(args, "-w")
	}
	if !o.regex {
		args = append(args, "-F")
	}
	for _, p := range o.include {
		args = append(args, "-g", p)
	}
	for _, p := range o.exclude {
		args = append(args, "-g", "!"+p)
	}
	args = append(args, "--", o.query, ".")
	cmd := exec.CommandContext(ctx, rg, args...)
	cmd.Dir = root
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	byPath := map[string]*searchFile{}
	var order []*searchFile
	total := 0
	truncated := false
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var msg struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				Lines struct {
					Text string `json:"text"`
				} `json:"lines"`
				LineNumber int `json:"line_number"`
				Submatches []struct {
					Start int `json:"start"`
					End   int `json:"end"`
				} `json:"submatches"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || msg.Type != "match" {
			continue
		}
		full := filepath.Join(root, msg.Data.Path.Text)
		if _, isDirty := dirty[full]; isDirty {
			continue
		}
		sf := byPath[full]
		if sf == nil {
			sf = &searchFile{path: full}
			byPath[full] = sf
			order = append(order, sf)
		}
		line := strings.TrimRight(msg.Data.Lines.Text, "\r\n")
		for _, sm := range msg.Data.Submatches {
			if sm.End > len(line) {
				continue
			}
			sf.matches = append(sf.matches, searchMatch{line: msg.Data.LineNumber - 1, col: utf8.RuneCountInString(line[:sm.Start]), length: utf8.RuneCountInString(line[sm.Start:sm.End]), text: line})
			total++
		}
		if total > maxSearchResults {
			truncated = true
			_ = cmd.Process.Kill()
			break
		}
	}
	_ = cmd.Wait()
	// dirty documents are searched in memory
	for p, lines := range dirty {
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") || !includeOK(rel, o) {
			continue
		}
		sf := &searchFile{path: p}
		for ln, l := range lines {
			for _, m := range re.FindAllStringIndex(l, -1) {
				if m[0] != m[1] {
					sf.matches = append(sf.matches, searchMatch{line: ln, col: utf8.RuneCountInString(l[:m[0]]), length: utf8.RuneCountInString(l[m[0]:m[1]]), text: l})
				}
			}
		}
		if len(sf.matches) > 0 {
			order = append(order, sf)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].path < order[j].path })
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	return order, truncated, nil
}

// showReferences displays LSP references in the results tree.
func (s *SearchView) showReferences(word string, locs []lsp.Location) {
	a := s.app
	byPath := map[string]*searchFile{}
	var order []*searchFile
	for _, l := range locs {
		p := lsp.URIToPath(l.URI)
		sf := byPath[p]
		if sf == nil {
			sf = &searchFile{path: p}
			byPath[p] = sf
			order = append(order, sf)
		}
		text := a.lineTextAt(p, l.Range.Start.Line)
		col := l.Range.Start.Character
		length := max(l.Range.End.Character-l.Range.Start.Character, 1)
		if l.Range.End.Line != l.Range.Start.Line {
			length = max(len([]rune(text))-col, 1)
		}
		sf.matches = append(sf.matches, searchMatch{line: l.Range.Start.Line, col: col, length: length, text: text})
	}
	sort.Slice(order, func(i, j int) bool { return order[i].path < order[j].path })
	for _, f := range order {
		sort.Slice(f.matches, func(i, j int) bool { return f.matches[i].line < f.matches[j].line })
	}
	s.results = order
	s.refsTitle = fmt.Sprintf("%d references to '%s'", len(locs), word)
	s.selected = 0
	s.scroll = 0
	s.focusField = 4
	s.rebuild()
}

func (s *SearchView) rebuild() {
	s.rows = s.rows[:0]
	s.total = 0
	for _, f := range s.results {
		s.rows = append(s.rows, searchRow{file: f, match: -1})
		s.total += len(f.matches)
		if f.collapsed {
			continue
		}
		for i := range f.matches {
			s.rows = append(s.rows, searchRow{file: f, match: i})
		}
	}
	if s.selected >= len(s.rows) {
		s.selected = max(len(s.rows)-1, 0)
	}
}

func (s *SearchView) openRow(i int, focusEditor bool, preview bool) {
	if i < 0 || i >= len(s.rows) {
		return
	}
	a := s.app
	r := s.rows[i]
	if r.match < 0 {
		r.file.collapsed = !r.file.collapsed
		s.rebuild()
		return
	}
	m := r.file.matches[r.match]
	v := a.OpenFile(r.file.path, preview)
	if v == nil {
		return
	}
	start := v.buf().Clamp(buffer.Pos{Line: m.line, Col: m.col})
	end := v.buf().Clamp(buffer.Pos{Line: m.line, Col: m.col + m.length})
	v.SetSelections([]buffer.Selection{{Anchor: start, Active: end}}, 0)
	v.ensurePosVisible(start, true)
	if focusEditor {
		a.focusEditor()
	}
}

func (s *SearchView) handleKey(key, text string, isText bool) bool {
	a := s.app
	if s.focusField < 4 {
		switch key {
		case "enter":
			s.run()
			return true
		case "down":
			if s.focusField == 0 && s.showReplace {
				s.focusField = 1
			} else if len(s.rows) > 0 {
				s.focusField = 4
			}
			return true
		case "up":
			if s.focusField == 1 {
				s.focusField = 0
			} else if s.focusField == 0 {
				s.query.HistoryMove(-1)
			}
			return true
		case "tab":
			s.focusField = s.nextField(1)
			return true
		case "shift+tab":
			s.focusField = s.nextField(-1)
			return true
		case "escape":
			a.focusEditor()
			return true
		case "alt+c":
			s.caseSens = !s.caseSens
			s.run()
			return true
		case "alt+w":
			s.wholeWord = !s.wholeWord
			s.run()
			return true
		case "alt+r":
			s.regex = !s.regex
			s.run()
			return true
		case "ctrl+alt+enter":
			s.replaceAll()
			return true
		}
		return false
	}
	switch key {
	case "up":
		if s.selected == 0 {
			s.focusField = 0
		} else {
			s.selected--
		}
	case "down":
		if s.selected < len(s.rows)-1 {
			s.selected++
		}
	case "pageup":
		s.selected = max(s.selected-s.listRect.H, 0)
	case "pagedown":
		s.selected = min(s.selected+s.listRect.H, max(len(s.rows)-1, 0))
	case "enter":
		s.openRow(s.selected, true, false)
	case "space":
		s.openRow(s.selected, false, true)
	case "left":
		if s.selected < len(s.rows) {
			r := s.rows[s.selected]
			r.file.collapsed = true
			s.rebuild()
			for i, x := range s.rows {
				if x.file == r.file && x.match < 0 {
					s.selected = i
				}
			}
		}
	case "right":
		if s.selected < len(s.rows) {
			s.rows[s.selected].file.collapsed = false
			s.rebuild()
		}
	case "delete":
		s.dismissSelected()
	case "escape":
		a.focusEditor()
	case "tab", "shift+tab":
		s.focusField = 0
	default:
		if isText {
			s.focusField = 0
			s.query.HandleKey(key, text, isText)
		}
		return isText
	}
	s.ensureVisible()
	return true
}

func (s *SearchView) nextField(d int) int {
	fields := []int{0}
	if s.showReplace {
		fields = append(fields, 1)
	}
	if s.showDetails {
		fields = append(fields, 2, 3)
	}
	if len(s.rows) > 0 {
		fields = append(fields, 4)
	}
	for i, f := range fields {
		if f == s.focusField {
			return fields[(i+d+len(fields))%len(fields)]
		}
	}
	return 0
}

func (s *SearchView) dismissSelected() {
	if s.selected >= len(s.rows) {
		return
	}
	r := s.rows[s.selected]
	if r.match < 0 {
		for i, f := range s.results {
			if f == r.file {
				s.results = append(s.results[:i], s.results[i+1:]...)
				break
			}
		}
	} else {
		f := r.file
		f.matches = append(f.matches[:r.match], f.matches[r.match+1:]...)
		if len(f.matches) == 0 {
			for i, x := range s.results {
				if x == f {
					s.results = append(s.results[:i], s.results[i+1:]...)
					break
				}
			}
		}
	}
	s.rebuild()
}

func (s *SearchView) ensureVisible() {
	h := s.listRect.H
	if h <= 0 {
		return
	}
	if s.selected < s.scroll {
		s.scroll = s.selected
	}
	if s.selected >= s.scroll+h {
		s.scroll = s.selected - h + 1
	}
}

// replaceAll applies the replacement in every matched file.
func (s *SearchView) replaceAll() {
	a := s.app
	if len(s.results) == 0 || s.refsTitle != "" {
		return
	}
	o := searchOpts{query: s.query.Text(), regex: s.regex, caseSens: s.caseSens, wholeWord: s.wholeWord}
	re, err := o.compile()
	if err != nil {
		a.notify(NotifyError, err.Error())
		return
	}
	repl := s.replace.Text()
	nFiles, nMatches := 0, 0
	for _, f := range s.results {
		count := len(f.matches)
		var edits []buffer.Edit
		for _, m := range f.matches {
			line := []rune(m.text)
			if m.col+m.length > len(line) {
				continue
			}
			matched := string(line[m.col : m.col+m.length])
			text := repl
			if s.regex {
				text = re.ReplaceAllString(matched, strings.ReplaceAll(repl, `\n`, "\n"))
			}
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: m.line, Col: m.col}, End: buffer.Pos{Line: m.line, Col: m.col + m.length}}, Text: text})
		}
		if d, ok := a.docs[f.path]; ok {
			d.Buf.CloseUndoGroup()
			d.Buf.ApplyEdits(edits, nil, "")
			d.Buf.CloseUndoGroup()
			a.saveDocument(d, false)
		} else {
			b, err := buffer.Load(f.path)
			if err != nil {
				continue
			}
			b.ApplyEdits(edits, nil, "")
			if err := b.Save(); err != nil {
				a.notify(NotifyError, "Failed to write "+f.path+": "+err.Error())
				continue
			}
		}
		nFiles++
		nMatches += count
	}
	a.notify(NotifyInfo, fmt.Sprintf("Replaced %d occurrences across %d files with '%s'.", nMatches, nFiles, repl))
	s.results = nil
	s.rebuild()
	a.refreshGit()
}

func (s *SearchView) drawActions(r Rect) {
	a := s.app
	th := a.theme
	acts := []struct {
		icon int
		run  func()
		tip  string
	}{
		{IcoRefresh, func() { s.run() }, "Refresh"},
		{IcoClearAll, func() { s.results = nil; s.query.SetText(""); s.rebuild() }, "Clear Search Results"},
		{IcoCollapseAll, func() {
			for _, f := range s.results {
				f.collapsed = true
			}
			s.rebuild()
		}, "Collapse All"},
	}
	x := r.X + r.W - len(acts)*2
	for _, ac := range acts {
		st := styleFB(th.C("sideBarTitle.foreground"), th.C("sideBar.background"))
		a.cv.Text(x, r.Y, x+1, a.Icon(ac.icon), st)
		run := ac.run
		tip := ac.tip
		a.addHit(Rect{x, r.Y, 1, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				run()
			case MouseMove:
				a.showTooltipAt(ev.X, r.Y+1, tip)
			}
			return true
		})
		x += 2
	}
}

func (s *SearchView) draw(r Rect) {
	mark := len(s.app.hits)
	a := s.app
	th := a.theme
	s.rect = r
	bgc := th.C("sideBar.background")
	focused := a.focus == FocusSidebar && a.sidebarView == "search"
	ibg := th.C("input.background")
	ist := styleFB(th.C("input.foreground"), ibg)
	phSt := styleFB(th.C("input.placeholderForeground"), ibg)
	y := r.Y
	// replace toggle
	chev := a.Icon(IcoChevronRight)
	if s.showReplace {
		chev = a.Icon(IcoChevronDown)
	}
	a.cv.Text(r.X, y, r.X+1, chev, styleFB(th.C("sideBar.foreground"), bgc))
	a.addHit(Rect{r.X, y, 1, 1 + boolInt(s.showReplace)}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			s.showReplace = !s.showReplace
		}
		return true
	})
	toggles := []struct {
		icon string
		on   *bool
	}{
		{a.Icon(IcoCaseSensitive), &s.caseSens},
		{a.Icon(IcoWholeWord), &s.wholeWord},
		{a.Icon(IcoRegex), &s.regex},
	}
	togW := 0
	for _, t := range toggles {
		togW += StrWidth(t.icon) + 1
	}
	inputX := r.X + 2
	inputW := r.W - 3
	a.cv.Fill(Rect{inputX, y, inputW, 1}, ' ', ist)
	s.query.Draw(inputX+1, y, inputW-togW-2, ist, phSt, focused && s.focusField == 0)
	a.addHit(Rect{inputX, y, inputW - togW, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			a.setFocus(FocusSidebar)
			s.focusField = 0
		}
		return false
	})
	if focused && s.focusField == 0 {
		a.cv.Set(inputX, y, '▏', styleFB(th.C("focusBorder"), ibg))
	}
	tx := inputX + inputW - togW
	for _, t := range toggles {
		st := ist
		if *t.on {
			st = styleFB(th.C("inputOption.activeForeground"), th.OverColor("inputOption.activeBackground", ibg))
		}
		a.cv.Text(tx, y, tx+StrWidth(t.icon), t.icon, st)
		on := t.on
		a.addHit(Rect{tx, y, StrWidth(t.icon), 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				*on = !*on
				s.run()
			}
			return true
		})
		tx += StrWidth(t.icon) + 1
	}
	y++
	if s.showReplace {
		a.cv.Fill(Rect{inputX, y, inputW, 1}, ' ', ist)
		s.replace.Draw(inputX+1, y, inputW-4, ist, phSt, focused && s.focusField == 1)
		if focused && s.focusField == 1 {
			a.cv.Set(inputX, y, '▏', styleFB(th.C("focusBorder"), ibg))
		}
		a.addHit(Rect{inputX, y, inputW - 3, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.setFocus(FocusSidebar)
				s.focusField = 1
			}
			return false
		})
		ra := a.Icon(IcoReplaceAll)
		a.cv.Text(inputX+inputW-2, y, inputX+inputW, ra, styleFB(th.C("sideBar.foreground"), bgc))
		a.addHit(Rect{inputX + inputW - 2, y, 2, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				s.replaceAll()
			case MouseMove:
				a.showTooltipAt(ev.X, y+1, "Replace All (Ctrl+Alt+Enter)")
			}
			return true
		})
		y++
	}
	// details toggle
	dst := styleFB(th.C("descriptionForeground"), bgc)
	a.cv.TextRight(r.X, y, r.W-1, a.Icon(IcoEllipsis), dst)
	a.addHit(Rect{r.X + r.W - 3, y, 3, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			s.showDetails = !s.showDetails
		}
		return true
	})
	if s.showDetails {
		a.cv.Text(r.X+2, y, r.X+r.W-3, "files to include", dst)
		y++
		a.cv.Fill(Rect{inputX, y, inputW, 1}, ' ', ist)
		s.include.Draw(inputX+1, y, inputW-2, ist, phSt, focused && s.focusField == 2)
		a.addHit(Rect{inputX, y, inputW, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.setFocus(FocusSidebar)
				s.focusField = 2
			}
			return false
		})
		y++
		a.cv.Text(r.X+2, y, r.X+r.W, "files to exclude", dst)
		y++
		a.cv.Fill(Rect{inputX, y, inputW, 1}, ' ', ist)
		s.exclude.Draw(inputX+1, y, inputW-2, ist, phSt, focused && s.focusField == 3)
		a.addHit(Rect{inputX, y, inputW, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.setFocus(FocusSidebar)
				s.focusField = 3
			}
			return false
		})
	}
	y++
	// summary
	summary := ""
	switch {
	case s.running:
		summary = "Searching..."
	case s.message != "":
		summary = s.message
	case s.refsTitle != "":
		summary = s.refsTitle
	case s.query.Text() != "" && len(s.results) == 0:
		summary = "No results found. Review your settings for configured exclusions and check your gitignore files."
	case len(s.results) > 0:
		summary = fmt.Sprintf("%d results in %d files", s.total, len(s.results))
		if s.truncated {
			summary += " (truncated)"
		}
	}
	for _, l := range wrapText(summary, r.W-3) {
		if y >= r.Y+r.H {
			break
		}
		a.cv.Text(r.X+2, y, r.X+r.W, l, dst)
		y++
	}
	// results
	list := Rect{r.X, y, r.W, r.Y + r.H - y}
	s.listRect = list
	s.scroll = clampInt(s.scroll, 0, max(len(s.rows)-list.H, 0))
	listFocused := focused && s.focusField == 4
	for i := 0; i < list.H; i++ {
		idx := s.scroll + i
		if idx >= len(s.rows) {
			break
		}
		row := s.rows[idx]
		ry := list.Y + i
		sel := idx == s.selected && s.focusField == 4
		rbg := bgc
		rfg := th.C("sideBar.foreground")
		if sel && listFocused {
			rbg = th.C("list.activeSelectionBackground")
			rfg = th.C("list.activeSelectionForeground")
		} else if sel {
			rbg = th.C("list.inactiveSelectionBackground")
		} else if a.mouseY == ry && list.Contains(a.mouseX, a.mouseY) {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		st := styleFB(rfg, rbg)
		a.cv.Fill(Rect{list.X, ry, list.W, 1}, ' ', st)
		if row.match < 0 {
			f := row.file
			chev := a.Icon(IcoChevronDown)
			if f.collapsed {
				chev = a.Icon(IcoChevronRight)
			}
			x := a.cv.Text(list.X+1, ry, list.X+list.W, chev+" ", st)
			icon, col := a.FileIcon(filepath.Base(f.path), false, false)
			if icon != "" {
				x = a.cv.Text(x, ry, list.X+list.W, icon+" ", styleFB(col, rbg))
			}
			name := filepath.Base(f.path)
			count := fmt.Sprint(len(f.matches))
			x = a.cv.Text(x, ry, list.X+list.W-len(count)-2, name, st)
			dir := filepath.Dir(a.relPath(f.path))
			if dir != "." {
				a.cv.TextEllipsis(x+1, ry, list.X+list.W-len(count)-3-x, dir, st.Foreground(th.OverColor("descriptionForeground", rbg)))
			}
			bst := styleFB(th.C("badge.foreground"), th.C("badge.background"))
			a.cv.Text(list.X+list.W-len(count)-2, ry, list.X+list.W, " "+count+" ", bst)
		} else {
			m := row.file.matches[row.match]
			rs := []rune(m.text)
			// show some context before the match
			start := max(m.col-10, 0)
			for start > 0 && start < m.col && rs[start] != ' ' && m.col-start < 20 {
				start--
			}
			lead := strings.TrimLeft(string(rs[start:min(m.col, len(rs))]), " \t")
			matchEnd := min(m.col+m.length, len(rs))
			x := list.X + 4
			if start > 0 {
				x = a.cv.Text(x, ry, list.X+list.W, "…", st)
			}
			x = a.cv.Text(x, ry, list.X+list.W, lead, st)
			hst := st.Background(th.OverColor("editor.findMatchHighlightBackground", rbg))
			matched := string(rs[min(m.col, len(rs)):matchEnd])
			if s.showReplace && s.replace.Text() != "" && s.refsTitle == "" {
				x = a.cv.Text(x, ry, list.X+list.W, matched, st.StrikeThrough(true).Background(th.OverColor("diffEditor.removedTextBackground", rbg)))
				x = a.cv.Text(x, ry, list.X+list.W, s.replace.Text(), st.Background(th.OverColor("diffEditor.insertedTextBackground", rbg)))
			} else {
				x = a.cv.Text(x, ry, list.X+list.W, matched, hst)
			}
			a.cv.Text(x, ry, list.X+list.W, string(rs[matchEnd:]), st)
		}
		rowIdx := idx
		a.addHit(Rect{list.X, ry, list.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.setFocus(FocusSidebar)
				s.focusField = 4
				s.selected = rowIdx
				if ev.Button == tcell.Button2 {
					a.openContextMenu(ev.X, ev.Y, []MenuItem{
						{Label: "Dismiss", Run: func() { s.dismissSelected() }},
						{Label: "Copy Path", Run: func() { a.setClipboard(s.rows[rowIdx].file.path) }},
					})
					return true
				}
				s.openRow(rowIdx, ev.Clicks >= 2, ev.Clicks < 2)
			case MouseWheel:
				s.scroll = clampInt(s.scroll+ev.WheelDY*3, 0, max(len(s.rows)-list.H, 0))
			}
			return true
		})
	}
	a.addHitUnder(mark, list, func(ev MouseEvent) bool {
		if ev.Kind == MouseWheel {
			s.scroll = clampInt(s.scroll+ev.WheelDY*3, 0, max(len(s.rows)-list.H, 0))
		}
		return true
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
