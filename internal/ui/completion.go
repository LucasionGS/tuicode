package ui

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"

	"tuicode/internal/buffer"
	"tuicode/internal/fuzzy"
	"tuicode/internal/lsp"
)

type citem struct {
	item      *lsp.CompletionItem
	label     string
	filter    string
	sortText  string
	kind      int
	detail    string
	score     int
	positions []int
	word      bool
	resolved  bool
	docs      string
	emmet     bool
	rng       *buffer.Range // explicit replace range for built-in providers
}

// Completion is the suggest widget state for an editor.
type Completion struct {
	v           *EditorView
	items       []*citem
	filtered    []*citem
	selected    int
	scroll      int
	start       buffer.Pos
	active      bool
	incomplete  bool
	reqID       int
	explicit    bool
	rect        Rect
	docRect     Rect
	showDetails bool
	loading     bool
	client      *lsp.Client
	triggerChar string
	gen         int
}

func (c *Completion) visible() bool {
	return c != nil && c.active && (len(c.filtered) > 0 || c.loading && c.explicit)
}

func (c *Completion) close() {
	if c == nil {
		return
	}
	c.active = false
	c.items = nil
	c.filtered = nil
	if c.client != nil && c.reqID != 0 {
		c.client.Cancel(c.reqID)
	}
}

// wordStart finds the start of the identifier left of p.
func (v *EditorView) wordStart(p buffer.Pos) buffer.Pos {
	l := v.buf().Line(p.Line)
	c := min(p.Col, len(l))
	for c > 0 && (isWordRune(l[c-1]) || l[c-1] == '$' && v.lang() != "go") {
		c--
	}
	return buffer.Pos{Line: p.Line, Col: c}
}

func (a *App) triggerChars(v *EditorView) []string {
	if a.lsp == nil {
		return nil
	}
	c := a.lsp.Client(v.buf())
	if c == nil {
		return nil
	}
	return c.StringList("completionProvider.triggerCharacters")
}

func (a *App) sigTriggerChars(v *EditorView) ([]string, []string) {
	if a.lsp == nil {
		return nil, nil
	}
	c := a.lsp.Client(v.buf())
	if c == nil {
		return nil, nil
	}
	return c.StringList("signatureHelpProvider.triggerCharacters"), c.StringList("signatureHelpProvider.retriggerCharacters")
}

// afterType reacts to typed text: suggestions and parameter hints.
func (a *App) afterType(v *EditorView, text string) {
	v.hover = nil
	rs := []rune(text)
	if len(rs) != 1 {
		return
	}
	ch := rs[0]
	v.autoCloseTag(ch)
	if a.settingsJSONAfterType(v, ch) {
		return
	}
	isTrigger := false
	for _, t := range a.triggerChars(v) {
		if t == text {
			isTrigger = true
		}
	}
	emmetChar := emmetLangs[v.lang()] && strings.ContainsRune(".#>*+]}^$", ch) || cssEmmetLangs[v.lang()] && strings.ContainsRune(":#-!", ch)
	if isTrigger && a.settings.Bool("editor.suggestOnTriggerCharacters", v.lang()) {
		a.triggerCompletion(v, false, text)
	} else if emmetChar && a.emmetCompletion(v) != nil {
		if v.completion == nil {
			v.completion = &Completion{v: v}
		}
		c := v.completion
		if !c.active {
			c.items = nil
			c.active = true
			c.explicit = false
			c.loading = false
		}
		c.start = v.Cursor()
		a.refilterCompletion(v)
	} else if isWordRune(ch) {
		if v.completion != nil && v.completion.active {
			a.refilterCompletion(v)
		} else if a.quickSuggestionsEnabled(v) && !v.inStringOrComment(v.Cursor()) {
			// only start after typing the first character of a word
			ws := v.wordStart(v.Cursor())
			if v.Cursor().Col-ws.Col >= 1 {
				a.triggerCompletion(v, false, "")
			}
		}
	} else if v.completion != nil {
		v.completion.close()
	}
	// signature help
	if a.settings.Bool("editor.parameterHints.enabled", v.lang()) {
		triggers, retriggers := a.sigTriggerChars(v)
		for _, t := range triggers {
			if t == text {
				a.triggerSignatureHelp(v, text)
				return
			}
		}
		if v.sig != nil {
			for _, t := range retriggers {
				if t == text {
					a.triggerSignatureHelp(v, text)
					return
				}
			}
			if ch == ')' {
				a.triggerSignatureHelp(v, "")
			}
		}
	}
}

func (a *App) quickSuggestionsEnabled(v *EditorView) bool {
	switch q := a.settings.Get("editor.quickSuggestions", v.lang()).(type) {
	case bool:
		return q
	case map[string]any:
		other, _ := q["other"]
		switch o := other.(type) {
		case bool:
			return o
		case string:
			return o == "on" || o == "inline"
		}
		return true
	case string:
		return q == "on"
	}
	return true
}

func (a *App) afterBackspace(v *EditorView) {
	if v.completion != nil && v.completion.active {
		if v.Cursor().Less(v.completion.start) || v.Cursor() == v.completion.start && !v.completion.explicit {
			v.completion.close()
			return
		}
		a.refilterCompletion(v)
	}
	if v.sig != nil {
		a.triggerSignatureHelp(v, "")
	}
}

// afterEdit closes widgets that no longer apply after a non-typing edit.
func (a *App) afterEdit(v *EditorView) {
	if v.completion != nil && v.completion.active {
		cur := v.Cursor()
		if cur.Line != v.completion.start.Line || cur.Less(v.completion.start) {
			v.completion.close()
		}
	}
	v.hover = nil
}

func (a *App) afterCursorMove(v *EditorView) {
	if v.completion != nil && v.completion.active {
		cur := v.Cursor()
		if cur.Line != v.completion.start.Line || cur.Less(v.completion.start) || cur.Col > v.wordEndAfter(v.completion.start) {
			v.completion.close()
		}
	}
	if v.sig != nil {
		cur := v.Cursor()
		if cur.Line != v.sig.line {
			v.sig = nil
		} else {
			a.triggerSignatureHelp(v, "")
		}
	}
	v.hover = nil
	v.codeActions = nil
}

func (v *EditorView) wordEndAfter(p buffer.Pos) int {
	l := v.buf().Line(p.Line)
	c := p.Col
	for c < len(l) && isWordRune(l[c]) {
		c++
	}
	return c
}

// triggerCompletion requests suggestions at the primary cursor.
func (a *App) triggerCompletion(v *EditorView, explicit bool, triggerChar string) {
	if v.buf().ReadOnly {
		return
	}
	if v.completion == nil {
		v.completion = &Completion{v: v}
	}
	c := v.completion
	cur := v.Cursor()
	c.start = v.wordStart(cur)
	c.explicit = explicit
	c.active = true
	c.triggerChar = triggerChar
	c.selected = 0
	c.scroll = 0
	if items, start, ok := a.settingsCompletions(v); ok {
		c.start = start
		c.items = items
		c.loading = false
		c.gen++
		a.refilterCompletion(v)
		return
	}
	words := a.wordCompletions(v, c.start)
	snips := a.snippetCompletions(v.lang())
	words = append(snips, words...)
	if a.lsp != nil {
		kind := 1
		ctx := map[string]any{"triggerKind": kind}
		if triggerChar != "" {
			ctx["triggerKind"] = 2
			ctx["triggerCharacter"] = triggerChar
		}
		params := a.lsp.DocParams(v.buf(), cur)
		params["context"] = ctx
		if c.client != nil && c.reqID != 0 {
			c.client.Cancel(c.reqID)
		}
		c.loading = true
		version := v.buf().Version()
		c.gen++
		gen := c.gen
		id, ok := a.lsp.Request(v.buf(), "textDocument/completion", params, func(raw json.RawMessage, err error) {
			// responses to superseded (or cancelled) requests are ignored
			if v.completion != c || !c.active || c.gen != gen {
				return
			}
			c.loading = false
			c.reqID = 0
			if err != nil {
				c.items = words
				a.refilterCompletion(v)
				return
			}
			list := lsp.ParseCompletion(raw)
			c.incomplete = list.IsIncomplete
			items := make([]*citem, 0, len(list.Items))
			for i := range list.Items {
				it := &list.Items[i]
				ci := &citem{item: it, label: it.Label, filter: it.FilterText, sortText: it.SortText, kind: it.Kind, detail: it.Detail}
				if ci.filter == "" {
					ci.filter = it.Label
				}
				if ci.sortText == "" {
					ci.sortText = it.Label
				}
				if it.LabelDetails != nil && it.LabelDetails.Detail != "" {
					ci.label = it.Label + it.LabelDetails.Detail
				}
				if it.LabelDetails != nil && it.LabelDetails.Description != "" && ci.detail == "" {
					ci.detail = it.LabelDetails.Description
				}
				items = append(items, ci)
			}
			if len(items) == 0 && a.settings.String("editor.wordBasedSuggestions", v.lang()) != "off" {
				items = words
			} else {
				items = append(items, snips...)
			}
			c.items = items
			_ = version
			// if the server answered after more typing, refilter against the current word
			c.start = a.completionStartFor(v, list, c.start)
			a.refilterCompletion(v)
		})
		if ok {
			c.client = a.lsp.Client(v.buf())
			c.reqID = id
			// show word suggestions immediately while waiting
			if len(c.items) == 0 {
				c.items = words
				a.refilterCompletion(v)
			}
			return
		}
		c.loading = false
	}
	c.items = words
	a.refilterCompletion(v)
}

// completionStartFor uses the first item's edit range (if any) to determine the completion start.
func (a *App) completionStartFor(v *EditorView, list *lsp.CompletionList, def buffer.Pos) buffer.Pos {
	if len(list.Items) == 0 {
		return def
	}
	// items can have their own ranges (tsserver replaces "." for optional chaining items), so use the
	// start most items agree on
	counts := map[buffer.Pos]int{}
	best, bestN := def, 0
	for i := range list.Items {
		if i >= 200 {
			break
		}
		te, ok := list.Items[i].Edit()
		if !ok {
			counts[def]++
			if counts[def] > bestN {
				best, bestN = def, counts[def]
			}
			continue
		}
		p := a.lsp.FromLSP(v.buf(), te.Range.Start)
		if p.Line != def.Line || p.Col > v.Cursor().Col {
			continue
		}
		counts[p]++
		if counts[p] > bestN {
			best, bestN = p, counts[p]
		}
	}
	return best
}

// wordCompletions collects words from open documents of the same language.
func (a *App) wordCompletions(v *EditorView, start buffer.Pos) []*citem {
	mode := a.settings.String("editor.wordBasedSuggestions", v.lang())
	if mode == "off" {
		return nil
	}
	seen := map[string]bool{}
	cur := v.Cursor()
	curWord := ""
	if wr, ok := v.WordAt(cur); ok {
		curWord = v.buf().TextRange(wr)
	}
	var out []*citem
	collect := func(d *Document) {
		b := d.Buf
		limit := min(b.LineCount(), 20000)
		for l := 0; l < limit; l++ {
			line := b.Line(l)
			i := 0
			for i < len(line) {
				if !isWordRune(line[i]) || unicode.IsDigit(line[i]) {
					i++
					continue
				}
				j := i
				for j < len(line) && isWordRune(line[j]) {
					j++
				}
				if j-i >= 2 {
					w := string(line[i:j])
					if !seen[w] && w != curWord {
						seen[w] = true
						out = append(out, &citem{label: w, filter: w, sortText: w, kind: lsp.CIKText, word: true})
					}
				}
				i = j
			}
		}
	}
	collect(v.doc)
	if mode == "matchingDocuments" || mode == "allDocuments" {
		for _, d := range a.docs {
			if d != v.doc && (mode == "allDocuments" || d.Lang.ID == v.doc.Lang.ID) {
				collect(d)
			}
		}
	}
	return out
}

func (a *App) refilterCompletion(v *EditorView) {
	c := v.completion
	if c == nil || !c.active {
		return
	}
	cur := v.Cursor()
	em := a.emmetCompletion(v)
	if em != nil && cur.Line == c.start.Line && cur.Less(c.start) {
		c.start = cur
	}
	if cur.Line != c.start.Line || cur.Less(c.start) {
		c.close()
		return
	}
	prefix := string(v.buf().Line(cur.Line)[c.start.Col:min(cur.Col, v.buf().LineLen(cur.Line))])
	if c.incomplete && prefix != "" && c.reqID == 0 && !c.loading {
		// server asked to be re-queried as the word grows
		c.loading = true
		a.requeryIncomplete(v)
	}
	c.filtered = c.filtered[:0]
	for _, it := range c.items {
		if prefix == "" {
			it.score = 0
			it.positions = nil
			c.filtered = append(c.filtered, it)
			continue
		}
		s, pos, ok := fuzzy.Match(prefix, it.filter)
		if !ok {
			continue
		}
		// word-based suggestions need a strong prefix match
		if it.word && !strings.HasPrefix(strings.ToLower(it.filter), strings.ToLower(prefix[:1])) {
			continue
		}
		if strings.HasPrefix(it.filter, prefix) {
			s += 10
		}
		if it.filter != it.label {
			// recompute highlight positions on the label
			if _, p2, ok2 := fuzzy.Match(prefix, it.label); ok2 {
				pos = p2
			} else {
				pos = nil
			}
		}
		it.score = s
		it.positions = pos
		c.filtered = append(c.filtered, it)
	}
	defer func() {
		if em != nil {
			c.filtered = append([]*citem{em}, c.filtered...)
			c.active = true
			c.selected = 0
		}
	}()
	if prefix == "" {
		sort.SliceStable(c.filtered, func(i, j int) bool { return c.filtered[i].sortText < c.filtered[j].sortText })
	} else {
		sort.SliceStable(c.filtered, func(i, j int) bool {
			a, b := c.filtered[i], c.filtered[j]
			if a.word != b.word {
				return !a.word
			}
			if a.score != b.score {
				return a.score > b.score
			}
			return a.sortText < b.sortText
		})
	}
	// a single exact match that was fully typed closes the widget (like VS Code)
	if len(c.filtered) == 1 && c.filtered[0].filter == prefix && !c.explicit && c.filtered[0].item == nil {
		c.filtered = nil
	}
	// preselect
	c.selected = 0
	for i, it := range c.filtered {
		if it.item != nil && it.item.Preselect {
			c.selected = i
			break
		}
	}
	c.scroll = 0
	if len(c.filtered) == 0 && !c.loading {
		if !c.explicit {
			c.active = false
		}
	}
	a.resolveSelected(v)
}

func (a *App) requeryIncomplete(v *EditorView) {
	c := v.completion
	cur := v.Cursor()
	params := a.lsp.DocParams(v.buf(), cur)
	params["context"] = map[string]any{"triggerKind": 3}
	c.gen++
	gen := c.gen
	id, ok := a.lsp.Request(v.buf(), "textDocument/completion", params, func(raw json.RawMessage, err error) {
		if v.completion != c || !c.active || c.gen != gen {
			return
		}
		c.loading = false
		c.reqID = 0
		if err != nil {
			return
		}
		list := lsp.ParseCompletion(raw)
		c.incomplete = list.IsIncomplete
		items := make([]*citem, 0, len(list.Items))
		for i := range list.Items {
			it := &list.Items[i]
			ci := &citem{item: it, label: it.Label, filter: it.FilterText, sortText: it.SortText, kind: it.Kind, detail: it.Detail}
			if ci.filter == "" {
				ci.filter = it.Label
			}
			if ci.sortText == "" {
				ci.sortText = it.Label
			}
			items = append(items, ci)
		}
		c.items = items
		c.incomplete = false
		a.refilterCompletion(v)
		c.incomplete = list.IsIncomplete
	})
	if ok {
		c.reqID = id
	} else {
		c.loading = false
	}
}

// resolveSelected fetches documentation for the focused item.
func (a *App) resolveSelected(v *EditorView) {
	c := v.completion
	if c == nil || c.selected >= len(c.filtered) {
		return
	}
	it := c.filtered[c.selected]
	if it.item == nil || it.resolved {
		return
	}
	it.resolved = true
	it.docs = lsp.MarkupToText(it.item.Documentation)
	if a.lsp == nil || c.client == nil || !c.client.Supports("completionProvider.resolveProvider") {
		return
	}
	a.lsp.Request(v.buf(), "completionItem/resolve", it.item, func(raw json.RawMessage, err error) {
		if err != nil {
			return
		}
		var res lsp.CompletionItem
		if json.Unmarshal(raw, &res) != nil {
			return
		}
		if res.Detail != "" {
			it.detail = res.Detail
		}
		if d := lsp.MarkupToText(res.Documentation); d != "" {
			it.docs = d
		}
		if len(res.AdditionalTextEdits) > 0 {
			it.item.AdditionalTextEdits = res.AdditionalTextEdits
		}
		if len(res.TextEdit) > 0 && len(it.item.TextEdit) == 0 {
			it.item.TextEdit = res.TextEdit
		}
	})
}

func (c *Completion) handleKey(key string) bool {
	v := c.v
	a := v.app
	n := len(c.filtered)
	if n == 0 {
		if key == "escape" {
			c.close()
			return true
		}
		return false
	}
	rows := c.maxRows()
	switch key {
	case "down", "ctrl+n":
		c.selected = (c.selected + 1) % n
	case "up", "ctrl+p":
		c.selected = (c.selected - 1 + n) % n
	case "pagedown":
		c.selected = min(c.selected+rows-1, n-1)
	case "pageup":
		c.selected = max(c.selected-rows+1, 0)
	case "escape":
		c.close()
		return true
	case "tab":
		c.accept()
		return true
	case "enter":
		mode := a.settings.String("editor.acceptSuggestionOnEnter", v.lang())
		if mode == "off" {
			c.close()
			return false
		}
		c.accept()
		return true
	case "ctrl+space", "ctrl+i":
		c.showDetails = !c.showDetails
		return true
	default:
		return false
	}
	a.resolveSelected(v)
	return true
}

func (c *Completion) maxRows() int { return 12 }

// accept inserts the selected suggestion at every cursor.
func (c *Completion) accept() {
	v := c.v
	a := v.app
	if c.selected >= len(c.filtered) {
		c.close()
		return
	}
	it := c.filtered[c.selected]
	c.close()
	b := v.buf()
	cur := v.Cursor()
	replace := buffer.Range{Start: c.start, End: buffer.Pos{Line: cur.Line, Col: v.wordEndAfter(cur)}}
	if a.settings.String("editor.suggest.insertMode", "") == "insert" {
		replace.End = cur
	}
	text := it.label
	isSnippet := false
	var additional []lsp.TextEdit
	if it.item != nil {
		text = it.item.Label
		if it.item.InsertText != "" {
			text = it.item.InsertText
		}
		if te, ok := it.item.Edit(); ok {
			r := a.lspRange(b, te.Range)
			if r.Start.Line == cur.Line && r.End.Line == cur.Line {
				replace = r
				if replace.End.Less(cur) {
					replace.End = cur
				}
			}
			text = te.NewText
		}
		isSnippet = it.item.InsertTextFormat == 2
		additional = it.item.AdditionalTextEdits
	}
	if it.rng != nil && it.rng.Start.Line == cur.Line {
		replace = *it.rng
		// the range was computed when suggestions opened; recompute it for the text typed since
		if fresh, _, ok := a.settingsCompletions(v); ok && len(fresh) > 0 {
			replace = *fresh[0].rng
		}
		replace.End.Col = max(replace.End.Col, cur.Col)
	}
	if it.emmet {
		_, start := v.emmetAbbreviationBefore()
		replace = buffer.Range{Start: buffer.Pos{Line: cur.Line, Col: start}, End: cur}
	}
	b.CloseUndoGroup()
	if len(v.sels) > 1 {
		// apply the same relative edit at every cursor (snippets become plain text)
		if isSnippet {
			text, _ = parseSnippet(text, v.snippetVars)
		}
		before := cur.Col - replace.Start.Col
		after := replace.End.Col - cur.Col
		edits := make([]buffer.Edit, len(v.sels))
		for i, s := range v.sels {
			p := s.Active
			edits[i] = buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: p.Line, Col: max(p.Col-before, 0)}, End: buffer.Pos{Line: p.Line, Col: min(p.Col+after, b.LineLen(p.Line))}}, Text: text}
		}
		v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
			out := make([]buffer.Selection, len(res))
			for i, r := range res {
				out[i] = buffer.Caret(r.End)
			}
			return out
		})
	} else if isSnippet {
		v.insertSnippet(replace, text)
	} else {
		v.applyEdits([]buffer.Edit{{Range: replace, Text: text}}, "", func(res []buffer.Range) []buffer.Selection {
			return []buffer.Selection{buffer.Caret(res[0].End)}
		})
	}
	if len(additional) > 0 {
		var edits []buffer.Edit
		for _, te := range additional {
			edits = append(edits, buffer.Edit{Range: a.lspRange(b, te.Range), Text: te.NewText})
		}
		v.applying = false
		b.ApplyEdits(edits, v.sels, "")
	}
	b.CloseUndoGroup()
	v.ensureVisible()
	if it.item != nil && it.item.Command != nil {
		a.runLSPCommand(v, it.item.Command)
	}
	// typing "(" after a function usually wants parameter hints
	if it.kind == lsp.CIKFunction || it.kind == lsp.CIKMethod {
		if strings.HasSuffix(strings.TrimSuffix(text, "$0"), "(") || strings.Contains(text, "($") || strings.Contains(text, "(${") {
			a.triggerSignatureHelp(v, "(")
		}
	}
}

// runLSPCommand executes a command attached to a completion item or code action.
func (a *App) runLSPCommand(v *EditorView, cmd *lsp.Command) {
	switch cmd.Command {
	case "editor.action.triggerParameterHints":
		a.triggerSignatureHelp(v, "")
		return
	case "editor.action.triggerSuggest":
		time.AfterFunc(10*time.Millisecond, func() { a.Post(func() { a.triggerCompletion(v, true, "") }) })
		return
	case "":
		return
	}
	if _, ok := a.commands[cmd.Command]; ok {
		a.RunCommand(cmd.Command, cmd.Arguments)
		return
	}
	if a.lsp == nil {
		return
	}
	a.lsp.Request(v.buf(), "workspace/executeCommand", map[string]any{"command": cmd.Command, "arguments": cmd.Arguments}, func(raw json.RawMessage, err error) {
		if err != nil {
			a.notify(NotifyError, "Command "+cmd.Title+" failed: "+err.Error())
		}
	})
}

func (c *Completion) draw() {
	v := c.v
	a := v.app
	th := a.theme
	n := len(c.filtered)
	// the widget lines up with the typed word even when items replace more (tsserver replaces the ".")
	anchor := c.start
	if ws := v.wordStart(v.Cursor()); ws.Line == anchor.Line && anchor.Less(ws) {
		anchor = ws
	}
	cx, cy, ok := v.screenPos(anchor)
	if !ok {
		return
	}
	rows := min(n, c.maxRows())
	if n == 0 {
		rows = 1
	}
	// width from labels
	w := 20
	for i := 0; i < n && i < 200; i++ {
		lw := StrWidth(c.filtered[i].label) + 4
		if c.filtered[i].detail != "" && i == c.selected {
			lw += min(StrWidth(c.filtered[i].detail), 30) + 2
		}
		if lw > w {
			w = lw
		}
	}
	w = min(w, 60)
	x := cx - 3
	y := cy + 1
	if y+rows > a.height-1 && cy-rows >= 0 {
		y = cy - rows
	}
	if x+w > a.width {
		x = a.width - w
	}
	if x < 0 {
		x = 0
	}
	c.rect = Rect{x, y, w, rows}
	a.cv.clip = Rect{0, 0, a.width, a.height}
	bgc := th.C("editorSuggestWidget.background")
	fgc := th.C("editorSuggestWidget.foreground")
	base := styleFB(fgc, bgc)
	a.cv.Fill(c.rect, ' ', base)
	if n == 0 {
		a.cv.Text(x+1, y, x+w, "Loading...", base.Foreground(th.C("descriptionForeground")))
		return
	}
	if c.selected < c.scroll {
		c.scroll = c.selected
	}
	if c.selected >= c.scroll+rows {
		c.scroll = c.selected - rows + 1
	}
	for i := 0; i < rows; i++ {
		idx := c.scroll + i
		if idx >= n {
			break
		}
		it := c.filtered[idx]
		sel := idx == c.selected
		rbg := bgc
		rfg := fgc
		if sel {
			rbg = th.C("editorSuggestWidget.selectedBackground")
			if th.Has("editorSuggestWidget.selectedForeground") {
				rfg = th.C("editorSuggestWidget.selectedForeground")
			}
		}
		rst := styleFB(rfg, rbg)
		if it.item != nil && (it.item.Deprecated || hasTag(it.item.Tags, 1)) {
			rst = rst.StrikeThrough(true)
		}
		a.cv.Fill(Rect{x, y + i, w, 1}, ' ', rst)
		icon, ck := a.kindIcon(it.kind, false)
		a.cv.Text(x+1, y+i, x+3, icon, styleFB(th.C(ck), rbg))
		hl := rst.Foreground(th.C("editorSuggestWidget.highlightForeground")).Bold(true)
		if sel {
			hl = rst.Foreground(th.C("editorSuggestWidget.focusHighlightForeground")).Bold(true)
		}
		px := a.cv.Highlighted(x+3, y+i, x+w-1, it.label, rst, hl, it.positions)
		if it.detail != "" && (sel || a.settings.Bool("editor.suggest.showInlineDetails", "")) {
			dw := x + w - 1 - px - 1
			if dw > 3 {
				d := strings.ReplaceAll(it.detail, "\n", " ")
				dst := rst.Foreground(th.OverColor("descriptionForeground", rbg))
				if StrWidth(d) > dw {
					a.cv.TextEllipsis(px+1, y+i, dw, d, dst)
				} else {
					a.cv.TextRight(px+1, y+i, dw, d, dst)
				}
			}
		}
		rowIdx := idx
		a.addHit(Rect{x, y + i, w, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				if c.selected == rowIdx || ev.Clicks >= 2 {
					c.accept()
				} else {
					c.selected = rowIdx
					a.resolveSelected(v)
				}
			case MouseWheel:
				c.scroll = clampInt(c.scroll+ev.WheelDY, 0, max(n-rows, 0))
				c.selected = clampInt(c.selected, c.scroll, c.scroll+rows-1)
			}
			return true
		})
	}
	// scrollbar
	if n > rows {
		sh := max(rows*rows/n, 1)
		sy := c.scroll * (rows - sh) / max(n-rows, 1)
		for i := 0; i < rows; i++ {
			if i >= sy && i < sy+sh {
				a.cv.Set(x+w-1, y+i, ' ', styleFB(fgc, th.OverColor("scrollbarSlider.background", bgc)))
			}
		}
	}
	// documentation side panel
	it := c.filtered[c.selected]
	docs := it.docs
	if it.item != nil && docs == "" {
		docs = lsp.MarkupToText(it.item.Documentation)
	}
	detail := it.detail
	if docs == "" && (detail == "" || !c.showDetails) {
		return
	}
	dw := min(60, a.width-(x+w)-1)
	dx := x + w
	if dw < 25 {
		// not enough room on the right: place below/above
		if !c.showDetails {
			return
		}
		dw = w
		dx = x
	}
	lines := renderMarkdown(a, strings.TrimSpace(strings.Join(nonEmpty(detailBlock(detail, v.lang()), docs), "\n\n")), dw-2, v.doc.Lang.ID)
	dh := min(len(lines), max(rows, 6))
	if dh == 0 {
		return
	}
	dy := y
	if dx == x {
		dy = y + rows
		if dy+dh > a.height-1 {
			dy = y - dh
		}
	}
	c.docRect = Rect{dx, dy, dw, dh}
	a.cv.Fill(c.docRect, ' ', base)
	for i := 0; i < dh && i < len(lines); i++ {
		lines[i].draw(a, dx+1, dy+i, dx+dw-1, bgc)
	}
}

func detailBlock(detail, lang string) string {
	if detail == "" {
		return ""
	}
	return "```" + lang + "\n" + detail + "\n```"
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}
