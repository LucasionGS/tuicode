package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

func afterFunc(ms int, fn func()) *time.Timer {
	return time.AfterFunc(time.Duration(ms)*time.Millisecond, fn)
}

// lspRange converts a server range for buffer b.
func (a *App) lspRange(b *buffer.Buffer, r lsp.Range) buffer.Range {
	if a.lsp == nil {
		return buffer.Range{Start: buffer.Pos{Line: r.Start.Line, Col: r.Start.Character}, End: buffer.Pos{Line: r.End.Line, Col: r.End.Character}}
	}
	return a.lsp.RangeFromLSP(b, r)
}

func (a *App) noLSP(v *EditorView, feature string) bool {
	if a.lsp == nil || a.lsp.Client(v.buf()) == nil {
		if a.lsp != nil && a.lsp.HasDoc(v.buf()) {
			a.setStatusMessage("The language server is still starting...")
		} else {
			a.setStatusMessage("No " + feature + " provider for '" + v.doc.Lang.Name + "'. Install a language server (see Language Servers view).")
		}
		return true
	}
	return false
}

// goToDefinition jumps to the definition of the symbol at the cursor.
func (a *App) goToDefinition(v *EditorView, aside bool) {
	if a.lsp == nil || a.lsp.Client(v.buf()) == nil {
		// fall back to a textual search for the word in open documents
		a.textualDefinition(v)
		return
	}
	a.lsp.Request(v.buf(), "textDocument/definition", a.lsp.DocParams(v.buf(), v.Cursor()), func(raw json.RawMessage, err error) {
		if err != nil {
			a.setStatusMessage("Definition request failed: " + err.Error())
			return
		}
		locs := lsp.ParseLocations(raw)
		if len(locs) == 0 {
			word := ""
			if wr, ok := v.WordAt(v.Cursor()); ok {
				word = v.buf().TextRange(wr)
			}
			a.setStatusMessage(fmt.Sprintf("No definition found for '%s'", word))
			return
		}
		if aside {
			a.splitEditor()
		}
		if len(locs) == 1 {
			a.openLocation(locs[0])
			return
		}
		a.showLocationPicker("Definitions", locs)
	})
}

func (a *App) textualDefinition(v *EditorView) {
	wr, ok := v.WordAt(v.Cursor())
	if !ok {
		return
	}
	word := v.buf().TextRange(wr)
	for _, m := range v.findAll(word, true, true) {
		if m != wr {
			a.pushNav()
			v.SetCursor(m.Start)
			v.ensurePosVisible(m.Start, true)
			return
		}
	}
	a.setStatusMessage(fmt.Sprintf("No definition found for '%s'", word))
}

func (a *App) gotoLocations(v *EditorView, method, what string) {
	if a.noLSP(v, what) {
		return
	}
	a.pushNav()
	a.lsp.Request(v.buf(), method, a.lsp.DocParams(v.buf(), v.Cursor()), func(raw json.RawMessage, err error) {
		locs := lsp.ParseLocations(raw)
		if err != nil || len(locs) == 0 {
			a.setStatusMessage("No " + what + " found")
			return
		}
		if len(locs) == 1 {
			a.openLocation(locs[0])
			return
		}
		a.showLocationPicker(strings.Title(what)+"s", locs)
	})
}

func (a *App) peekLocations(v *EditorView, method, title string) {
	if a.noLSP(v, "definition") {
		return
	}
	a.lsp.Request(v.buf(), method, a.lsp.DocParams(v.buf(), v.Cursor()), func(raw json.RawMessage, err error) {
		locs := lsp.ParseLocations(raw)
		if err != nil || len(locs) == 0 {
			a.setStatusMessage("No results")
			return
		}
		a.showLocationPicker(title, locs)
	})
}

// showLocationPicker lists locations with a live preview, like VS Code's peek view.
func (a *App) showLocationPicker(title string, locs []lsp.Location) {
	sort.SliceStable(locs, func(i, j int) bool {
		if locs[i].URI != locs[j].URI {
			return locs[i].URI < locs[j].URI
		}
		return locs[i].Range.Start.Line < locs[j].Range.Start.Line
	})
	var items []QuickItem
	lastFile := ""
	for _, l := range locs {
		path := lsp.URIToPath(l.URI)
		text := a.lineTextAt(path, l.Range.Start.Line)
		it := QuickItem{Label: strings.TrimSpace(text), Description: fmt.Sprintf("%s:%d", a.relPath(path), l.Range.Start.Line+1), Data: l}
		if path != lastFile {
			it.Group = filepath.Base(path)
			lastFile = path
		}
		items = append(items, it)
	}
	g := a.ActiveGroup()
	origView := g.Active()
	var origSels []buffer.Selection
	var origTop vpos
	if origView != nil && origView.special == nil {
		origSels = append([]buffer.Selection{}, origView.sels...)
		origTop = origView.top()
	}
	origIdx := g.active
	a.openQuick(&QuickPick{
		Title:            fmt.Sprintf("%s (%d)", title, len(locs)),
		Placeholder:      "Filter results",
		Items:            items,
		KeepOrder:        true,
		MatchDescription: true,
		OnSelect: func(it *QuickItem) {
			if it == nil {
				return
			}
			loc := it.Data.(lsp.Location)
			path := lsp.URIToPath(loc.URI)
			if v := a.OpenFile(path, true); v != nil {
				r := a.lspRange(v.buf(), loc.Range)
				v.SetSelections([]buffer.Selection{{Anchor: r.Start, Active: r.End}}, 0)
				v.ensurePosVisible(r.Start, true)
			}
		},
		OnAccept: func(it *QuickItem, q string) {
			if it == nil {
				return
			}
			if origView != nil && origView.special == nil {
				a.navBack = append(a.navBack, navEntry{path: origView.buf().Path, pos: origSels[0].Active, doc: origView.doc})
			}
			if v := a.openLocation(it.Data.(lsp.Location)); v != nil {
				v.preview = false
			}
		},
		OnCancel: func() {
			if origView != nil && origIdx < len(g.tabs) {
				if i := g.indexOf(origView); i >= 0 {
					g.setActive(i)
					if origSels != nil {
						origView.SetSelections(origSels, -1)
						origView.setTop(origTop)
					}
				}
			}
		},
	})
}

// lineTextAt returns a line from an open document or from disk.
func (a *App) lineTextAt(path string, line int) string {
	if d, ok := a.docs[path]; ok {
		return d.Buf.LineString(line)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if line < len(lines) {
		return strings.TrimRight(lines[line], "\r")
	}
	return ""
}

func (a *App) referencesRequest(v *EditorView, cb func([]lsp.Location)) {
	if a.noLSP(v, "reference") {
		return
	}
	params := a.lsp.DocParams(v.buf(), v.Cursor())
	params["context"] = map[string]any{"includeDeclaration": true}
	a.lsp.Request(v.buf(), "textDocument/references", params, func(raw json.RawMessage, err error) {
		var locs []lsp.Location
		_ = json.Unmarshal(raw, &locs)
		if err != nil || len(locs) == 0 {
			a.setStatusMessage("No references found")
			return
		}
		cb(locs)
	})
}

func (a *App) peekReferences(v *EditorView) {
	a.referencesRequest(v, func(locs []lsp.Location) { a.showLocationPicker("References", locs) })
}

func (a *App) findAllReferences(v *EditorView) {
	word := ""
	if wr, ok := v.WordAt(v.Cursor()); ok {
		word = v.buf().TextRange(wr)
	}
	a.referencesRequest(v, func(locs []lsp.Location) {
		a.searchV.showReferences(word, locs)
		a.showSidebar("search")
	})
}

//#region Rename

// RenameBox is the inline rename input.
type RenameBox struct {
	v     *EditorView
	input TextInput
	pos   buffer.Pos
	rng   buffer.Range
}

func (a *App) startRename(v *EditorView) {
	if a.noLSP(v, "rename") {
		return
	}
	c := a.lsp.Client(v.buf())
	p := v.Cursor()
	open := func(r buffer.Range, placeholder string) {
		rb := &RenameBox{v: v, pos: r.Start, rng: r}
		rb.input = newTextInput(a, "")
		rb.input.SetText(placeholder)
		rb.input.SelectAll()
		v.rename = rb
		a.setFocus(FocusWidget)
	}
	wr, ok := v.WordAt(p)
	if !ok {
		a.setStatusMessage("The element can't be renamed.")
		return
	}
	if prep, _ := c.Cap("renameProvider").(map[string]any); prep != nil && prep["prepareProvider"] == true {
		a.lsp.Request(v.buf(), "textDocument/prepareRename", a.lsp.DocParams(v.buf(), p), func(raw json.RawMessage, err error) {
			if err != nil {
				a.setStatusMessage("Rename failed: " + err.Error())
				return
			}
			if string(raw) == "null" {
				a.setStatusMessage("The element can't be renamed.")
				return
			}
			var withPh struct {
				Range       *lsp.Range `json:"range"`
				Placeholder string     `json:"placeholder"`
			}
			var plain lsp.Range
			if json.Unmarshal(raw, &withPh) == nil && withPh.Range != nil {
				r := a.lspRange(v.buf(), *withPh.Range)
				ph := withPh.Placeholder
				if ph == "" {
					ph = v.buf().TextRange(r)
				}
				open(r, ph)
				return
			}
			if json.Unmarshal(raw, &plain) == nil && (plain.End != lsp.Position{}) {
				r := a.lspRange(v.buf(), plain)
				open(r, v.buf().TextRange(r))
				return
			}
			open(wr, v.buf().TextRange(wr))
		})
		return
	}
	open(wr, v.buf().TextRange(wr))
}

func (rb *RenameBox) handleKey(key string) bool {
	v := rb.v
	a := v.app
	switch key {
	case "escape":
		v.rename = nil
		a.setFocus(FocusEditor)
		return true
	case "enter":
		name := rb.input.Text()
		v.rename = nil
		a.setFocus(FocusEditor)
		if name == "" || name == v.buf().TextRange(rb.rng) {
			return true
		}
		params := a.lsp.DocParams(v.buf(), rb.pos)
		params["newName"] = name
		a.lsp.Request(v.buf(), "textDocument/rename", params, func(raw json.RawMessage, err error) {
			if err != nil {
				a.notify(NotifyError, "Rename failed: "+err.Error())
				return
			}
			var we lsp.WorkspaceEdit
			if json.Unmarshal(raw, &we) != nil {
				return
			}
			if a.applyWorkspaceEdit(we) {
				edits, _ := we.AllEdits()
				n := 0
				for _, e := range edits {
					n += len(e)
				}
				a.setStatusMessage(fmt.Sprintf("Renamed %d occurrences in %d files", n, len(edits)))
			}
		})
		return true
	}
	return false
}

func (rb *RenameBox) draw() {
	v := rb.v
	a := v.app
	th := a.theme
	x, y, ok := v.screenPos(rb.pos)
	if !ok {
		return
	}
	w := max(30, StrWidth(rb.input.Text())+4)
	top := y + 1
	if top+2 > a.height {
		top = y - 2
	}
	r := Rect{x, top, w, 2}
	if r.X+r.W > a.width {
		r.X = max(a.width-r.W, 0)
	}
	bgc := th.C("editorWidget.background")
	a.cv.clip = Rect{0, 0, a.width, a.height}
	a.cv.Fill(r, ' ', styleFB(th.C("editorWidget.foreground"), bgc))
	ibg := th.C("input.background")
	ist := styleFB(th.C("input.foreground"), ibg)
	a.cv.Fill(Rect{r.X + 1, r.Y, r.W - 2, 1}, ' ', ist)
	rb.input.Draw(r.X+1, r.Y, r.W-2, ist, ist, a.focus == FocusWidget)
	a.cv.Text(r.X+1, r.Y+1, r.X+r.W-1, "Enter to Rename, Shift+Enter to Preview", styleFB(th.C("descriptionForeground"), bgc))
}

//#endregion

//#region Code actions

// CodeActionMenu lists quick fixes and refactorings.
type CodeActionMenu struct {
	v        *EditorView
	actions  []lsp.CodeAction
	selected int
	anchor   buffer.Pos
	rect     Rect
}

func (a *App) codeActionParams(v *EditorView, only string) map[string]any {
	r := v.Primary().Range()
	var diags []lsp.Diagnostic
	for _, d := range v.doc.Diags {
		if !(d.Range.End.Less(r.Start) || r.End.Less(d.Range.Start)) || d.Range.Start.Line == r.Start.Line {
			diags = append(diags, d.Raw)
		}
	}
	if diags == nil {
		diags = []lsp.Diagnostic{}
	}
	ctx := map[string]any{"diagnostics": diags, "triggerKind": 1}
	if only != "" {
		ctx["only"] = []string{only}
	}
	return map[string]any{
		"textDocument": map[string]any{"uri": lsp.PathToURI(v.buf().Path)},
		"range":        lsp.Range{Start: a.lsp.ToLSP(v.buf(), r.Start), End: a.lsp.ToLSP(v.buf(), r.End)},
		"context":      ctx,
	}
}

func parseCodeActions(raw json.RawMessage) []lsp.CodeAction {
	var items []json.RawMessage
	_ = json.Unmarshal(raw, &items)
	var out []lsp.CodeAction
	for _, it := range items {
		var ca lsp.CodeAction
		if json.Unmarshal(it, &ca) != nil {
			continue
		}
		// a bare Command is also allowed
		var probe map[string]any
		_ = json.Unmarshal(it, &probe)
		if _, isCmd := probe["command"].(string); isCmd {
			ca.Command = it
		}
		if ca.Disabled != nil {
			continue
		}
		out = append(out, ca)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].IsPreferred && !out[j].IsPreferred })
	return out
}

func (a *App) showCodeActions(v *EditorView, only string) {
	if a.noLSP(v, "code action") {
		return
	}
	a.lsp.Request(v.buf(), "textDocument/codeAction", a.codeActionParams(v, only), func(raw json.RawMessage, err error) {
		if err != nil {
			a.setStatusMessage("Code actions failed: " + err.Error())
			return
		}
		acts := parseCodeActions(raw)
		if len(acts) == 0 {
			a.setStatusMessage("No code actions available")
			return
		}
		v.codeActions = &CodeActionMenu{v: v, actions: acts, anchor: v.Cursor()}
	})
}

func (a *App) runCodeActionKind(v *EditorView, kind string) {
	if a.noLSP(v, "code action") {
		return
	}
	a.lsp.Request(v.buf(), "textDocument/codeAction", a.codeActionParams(v, kind), func(raw json.RawMessage, err error) {
		acts := parseCodeActions(raw)
		if len(acts) == 0 {
			a.setStatusMessage("No code actions of kind " + kind)
			return
		}
		a.applyCodeAction(v, acts[0])
	})
}

func (a *App) applyCodeAction(v *EditorView, ca lsp.CodeAction) {
	run := func(ca lsp.CodeAction) {
		if ca.Edit != nil {
			a.applyWorkspaceEdit(*ca.Edit)
		}
		if len(ca.Command) > 0 && string(ca.Command) != "null" {
			var cmd lsp.Command
			if json.Unmarshal(ca.Command, &cmd) == nil && cmd.Command != "" {
				a.runLSPCommand(v, &cmd)
			}
		}
	}
	c := a.lsp.Client(v.buf())
	if ca.Edit == nil && len(ca.Command) == 0 && c != nil && c.Supports("codeActionProvider.resolveProvider") {
		a.lsp.Request(v.buf(), "codeAction/resolve", ca, func(raw json.RawMessage, err error) {
			var res lsp.CodeAction
			if err == nil && json.Unmarshal(raw, &res) == nil {
				run(res)
			}
		})
		return
	}
	run(ca)
}

func (m *CodeActionMenu) handleKey(key string) bool {
	v := m.v
	switch key {
	case "up":
		m.selected = (m.selected - 1 + len(m.actions)) % len(m.actions)
	case "down":
		m.selected = (m.selected + 1) % len(m.actions)
	case "escape":
		v.codeActions = nil
	case "enter", "tab":
		ca := m.actions[m.selected]
		v.codeActions = nil
		v.app.applyCodeAction(v, ca)
	default:
		v.codeActions = nil
		return false
	}
	return true
}

func (m *CodeActionMenu) draw() {
	v := m.v
	a := v.app
	th := a.theme
	x, y, ok := v.screenPos(m.anchor)
	if !ok {
		return
	}
	w := 20
	for _, ca := range m.actions {
		w = max(w, StrWidth(ca.Title)+4)
	}
	w = min(w, a.width-2)
	h := min(len(m.actions), 15)
	top := y + 1
	if top+h > a.height-1 {
		top = max(y-h, 0)
	}
	left := clampInt(x, 0, max(a.width-w, 0))
	m.rect = Rect{left, top, w, h}
	bgc := th.C("menu.background")
	a.cv.clip = Rect{0, 0, a.width, a.height}
	a.cv.Fill(m.rect, ' ', styleFB(th.C("menu.foreground"), bgc))
	start := 0
	if m.selected >= h {
		start = m.selected - h + 1
	}
	for i := 0; i < h; i++ {
		idx := start + i
		if idx >= len(m.actions) {
			break
		}
		ca := m.actions[idx]
		st := styleFB(th.C("menu.foreground"), bgc)
		if idx == m.selected {
			st = styleFB(th.C("menu.selectionForeground"), th.C("menu.selectionBackground"))
		}
		a.cv.Fill(Rect{left, top + i, w, 1}, ' ', st)
		icon := " "
		if strings.HasPrefix(ca.Kind, "quickfix") || ca.Kind == "" {
			icon = a.Icon(IcoLightbulb)
		} else if strings.HasPrefix(ca.Kind, "refactor") {
			icon = a.Icon(IcoGear)
		}
		ist := st
		if idx != m.selected {
			ist = st.Foreground(th.C("editorLightBulb.foreground"))
		}
		a.cv.Text(left+1, top+i, left+3, icon, ist)
		a.cv.TextEllipsis(left+3, top+i, w-4, ca.Title, st)
		rowIdx := idx
		a.addHit(Rect{left, top + i, w, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				m.selected = rowIdx
				m.handleKey("enter")
			}
			return true
		})
	}
}

//#endregion

//#region Formatting and edits

// formatDocument formats the document (or a range) and calls done afterwards.
func (a *App) formatDocument(v *EditorView, r *buffer.Range, done func()) {
	finish := func() {
		if done != nil {
			done()
		}
	}
	if a.lsp == nil || a.lsp.Client(v.buf()) == nil {
		if done == nil {
			a.setStatusMessage("There is no formatter for '" + v.doc.Lang.ID + "' files installed.")
		}
		finish()
		return
	}
	c := a.lsp.Client(v.buf())
	opts := map[string]any{
		"tabSize":                v.buf().TabSize,
		"insertSpaces":           v.buf().InsertSpaces,
		"trimTrailingWhitespace": a.settings.Bool("files.trimTrailingWhitespace", v.lang()),
		"insertFinalNewline":     a.settings.Bool("files.insertFinalNewline", v.lang()),
	}
	method := "textDocument/formatting"
	params := map[string]any{"textDocument": map[string]any{"uri": lsp.PathToURI(v.buf().Path)}, "options": opts}
	if r != nil && !r.Empty() && c.Supports("documentRangeFormattingProvider") {
		method = "textDocument/rangeFormatting"
		params["range"] = lsp.Range{Start: a.lsp.ToLSP(v.buf(), r.Start), End: a.lsp.ToLSP(v.buf(), r.End)}
	} else if !c.Supports("documentFormattingProvider") {
		if done == nil {
			a.setStatusMessage("The language server for '" + v.doc.Lang.ID + "' does not support formatting.")
		}
		finish()
		return
	}
	version := v.buf().Version()
	a.lsp.Request(v.buf(), method, params, func(raw json.RawMessage, err error) {
		if err != nil {
			a.setStatusMessage("Format failed: " + err.Error())
			finish()
			return
		}
		if v.buf().Version() != version {
			finish()
			return
		}
		var edits []lsp.TextEdit
		_ = json.Unmarshal(raw, &edits)
		a.applyTextEdits(v.doc, edits)
		finish()
	})
}

// applyTextEdits applies server edits to a document as one undo step, keeping cursors.
func (a *App) applyTextEdits(d *Document, edits []lsp.TextEdit) {
	if len(edits) == 0 {
		return
	}
	var bes []buffer.Edit
	for _, e := range edits {
		bes = append(bes, buffer.Edit{Range: a.lspRange(d.Buf, e.Range), Text: e.NewText})
	}
	var sels []buffer.Selection
	if v := a.viewForDoc(d); v != nil {
		sels = append(sels, v.sels...)
	}
	d.Buf.CloseUndoGroup()
	d.Buf.ApplyEdits(bes, sels, "")
	d.Buf.CloseUndoGroup()
	for v := range d.views {
		v.normalize()
	}
}

// applyWorkspaceEdit applies edits across files.
func (a *App) applyWorkspaceEdit(we lsp.WorkspaceEdit) bool {
	edits, ops := we.AllEdits()
	for _, op := range ops {
		kind, _ := op["kind"].(string)
		switch kind {
		case "create":
			uri, _ := op["uri"].(string)
			p := lsp.URIToPath(uri)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if _, err := os.Stat(p); err != nil {
				_ = os.WriteFile(p, nil, 0o644)
			}
		case "rename":
			oldURI, _ := op["oldUri"].(string)
			newURI, _ := op["newUri"].(string)
			op, np := lsp.URIToPath(oldURI), lsp.URIToPath(newURI)
			_ = os.MkdirAll(filepath.Dir(np), 0o755)
			if err := os.Rename(op, np); err == nil {
				if d, ok := a.docs[op]; ok {
					delete(a.docs, op)
					d.Buf.Path = np
					a.docs[np] = d
					if a.lsp != nil {
						a.lsp.Reopen(d.Buf)
					}
				}
			}
		case "delete":
			uri, _ := op["uri"].(string)
			_ = os.RemoveAll(lsp.URIToPath(uri))
		}
	}
	ok := true
	for uri, tes := range edits {
		path := lsp.URIToPath(uri)
		if d, open := a.docs[path]; open {
			a.applyTextEdits(d, tes)
			continue
		}
		// not open: edit on disk
		b, err := buffer.Load(path)
		if err != nil {
			ok = false
			continue
		}
		var bes []buffer.Edit
		for _, e := range tes {
			bes = append(bes, buffer.Edit{Range: lsp.FromLSPRangeGuess(b, e.Range), Text: e.NewText})
		}
		b.ApplyEdits(bes, nil, "")
		if err := b.Save(); err != nil {
			ok = false
		}
	}
	a.explorer.refreshSoon()
	return ok
}

//#endregion

//#region Document highlights and inlay hints

// requestDocumentHighlights asks the server for occurrences; returns false if unsupported.
func (a *App) requestDocumentHighlights(v *EditorView, p buffer.Pos) bool {
	if a.lsp == nil {
		return false
	}
	c := a.lsp.Client(v.buf())
	if c == nil || !c.Supports("documentHighlightProvider") {
		return false
	}
	version := v.buf().Version()
	a.lsp.Request(v.buf(), "textDocument/documentHighlight", a.lsp.DocParams(v.buf(), p), func(raw json.RawMessage, err error) {
		if v.buf().Version() != version || v.Cursor() != p {
			return
		}
		var hs []lsp.DocumentHighlight
		_ = json.Unmarshal(raw, &hs)
		out := []buffer.Range{}
		for _, h := range hs {
			out = append(out, a.lspRange(v.buf(), h.Range))
		}
		if len(out) <= 1 {
			out = []buffer.Range{}
		}
		v.highlights = out
		v.highlightsVersion = version
		v.highlightsPos = p
	})
	return true
}

// maybeRequestInlayHints refreshes inlay hints for the visible range.
func (a *App) maybeRequestInlayHints(v *EditorView) {
	mode := a.settings.String("editor.inlayHints.enabled", v.lang())
	if mode == "off" || mode == "offUnlessPressed" || a.lsp == nil {
		v.inlayHints = nil
		return
	}
	c := a.lsp.Client(v.buf())
	if c == nil || !c.Supports("inlayHintProvider") {
		return
	}
	if v.inlayVersion == v.buf().Version() && v.inlayTop == v.topLine {
		return
	}
	if v.inlayPending {
		return
	}
	v.inlayPending = true
	version := v.buf().Version()
	top := v.topLine
	end := min(v.buf().LineCount()-1, top+max(v.rect.H, 50)+20)
	params := map[string]any{
		"textDocument": map[string]any{"uri": lsp.PathToURI(v.buf().Path)},
		"range":        lsp.Range{Start: lsp.Position{Line: max(top-20, 0)}, End: lsp.Position{Line: end, Character: 0}},
	}
	a.lsp.Request(v.buf(), "textDocument/inlayHint", params, func(raw json.RawMessage, err error) {
		v.inlayPending = false
		v.inlayVersion = version
		v.inlayTop = top
		if err != nil || v.buf().Version() != version {
			return
		}
		var hints []lsp.InlayHint
		_ = json.Unmarshal(raw, &hints)
		m := map[int][]inlay{}
		for _, h := range hints {
			p := a.lsp.FromLSP(v.buf(), h.Position)
			m[p.Line] = append(m[p.Line], inlay{col: p.Col, label: h.LabelText(), left: h.PaddingLeft, right: h.PaddingRight})
		}
		for l := range m {
			sort.SliceStable(m[l], func(i, j int) bool { return m[l][i].col < m[l][j].col })
		}
		v.inlayHints = m
		v.wrapCache = nil
	})
}

//#endregion

var _ = tcell.ColorDefault
