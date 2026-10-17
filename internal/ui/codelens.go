package ui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

// codeLens is one LSP code lens shown above its line.
type codeLens struct {
	raw     json.RawMessage
	line    int
	command *lsp.Command
	run     func() // built-in lenses (merge conflicts) run this instead of a command
}

type lspCodeLens struct {
	Range   lsp.Range    `json:"range"`
	Command *lsp.Command `json:"command"`
}

// lensRows returns how many code lens rows are drawn above line.
func (v *EditorView) lensRows(line int) int {
	if _, ok := v.lenses[line]; ok {
		return 1
	}
	if v.doc.conflictStarts[line] && v.app.settings.Bool("merge-conflict.codeLens.enabled", "") {
		return 1
	}
	if v.testLensesEnabled() {
		if _, ok := v.doc.testsInDoc()[line]; ok {
			return 1
		}
	}
	return 0
}

// testLensesEnabled mirrors the Go extension's "go.enableCodeLens": {"runtest": true}.
func (v *EditorView) testLensesEnabled() bool {
	switch v.lang() {
	case "typescript", "typescriptreact", "javascript", "javascriptreact":
		return v.app.settings.Bool("deno.codeLens.test", "")
	case "go":
	default:
		return false
	}
	m, ok := v.app.settings.Get("go.enableCodeLens", "go").(map[string]any)
	if !ok {
		return true
	}
	on, ok := m["runtest"].(bool)
	return !ok || on
}

// testLenses returns "run test | debug test" above a Go test function.
func (v *EditorView) testLenses(line int) []*codeLens {
	if !v.testLensesEnabled() {
		return nil
	}
	if _, ok := v.doc.testsInDoc()[line]; !ok {
		return nil
	}
	a := v.app
	if v.doc.testKind == "deno" || v.doc.testKind == "vitest" {
		// the Deno extension's lens
		return []*codeLens{{line: line, command: &lsp.Command{Title: "▶ Run Test"}, run: func() { a.runTests([]*testCase{a.testAt(v.doc, line)}) }}}
	}
	return []*codeLens{
		{line: line, command: &lsp.Command{Title: "run test"}, run: func() { a.runTests([]*testCase{a.testAt(v.doc, line)}) }},
		{line: line, command: &lsp.Command{Title: "debug test"}, run: func() { a.debugTest(a.testAt(v.doc, line)) }},
	}
}

// hasLenses reports whether any lens rows may exist, so row math can't take its fast path.
func (v *EditorView) hasLenses() bool {
	return len(v.lenses) > 0 || len(v.doc.mergeConflicts()) > 0 || v.testLensesEnabled() && len(v.doc.testsInDoc()) > 0
}

// setLenses replaces the code lenses, showing the first line's lens when scrolled to the top.
func (v *EditorView) setLenses(m map[int][]*codeLens) {
	atTop := v.topLine == 0 && v.topSeg <= 0
	v.lenses = m
	v.wrapCache = nil
	if atTop {
		v.topSeg = -v.lensRows(0)
	}
}

// lensTitles returns the visible lens titles on a line.
func (v *EditorView) lensTitles(line int) []*codeLens {
	out := v.conflictLenses(line)
	out = append(out, v.testLenses(line)...)
	for _, l := range v.lenses[line] {
		if l.command != nil && l.command.Title != "" {
			out = append(out, l)
		}
	}
	return out
}

// maybeRequestCodeLens refreshes code lenses after edits settle.
func (a *App) maybeRequestCodeLens(v *EditorView) {
	if !a.settings.Bool("editor.codeLens", v.lang()) {
		v.lenses = nil
		return
	}
	if a.lsp == nil || v.special != nil {
		return
	}
	c := a.lsp.Client(v.buf())
	if c == nil || !c.Supports("codeLensProvider") {
		return
	}
	version := v.buf().Version()
	if v.lensVersion == version || v.lensPending {
		return
	}
	// wait until typing pauses, like VS Code
	if wait := 500*time.Millisecond - time.Since(v.lensAsked); wait > 0 {
		if !v.lensTimer {
			v.lensTimer = true
			time.AfterFunc(wait, func() { a.Post(func() { v.lensTimer = false }) })
		}
		return
	}
	v.lensPending = true
	v.lensAsked = time.Now()
	params := map[string]any{"textDocument": map[string]any{"uri": lsp.PathToURI(v.buf().Path)}}
	a.lsp.Request(v.buf(), "textDocument/codeLens", params, func(raw json.RawMessage, err error) {
		v.lensPending = false
		v.lensVersion = version
		if err != nil || v.buf().Version() != version {
			v.lensVersion = -1
			return
		}
		var items []json.RawMessage
		_ = json.Unmarshal(raw, &items)
		m := map[int][]*codeLens{}
		var unresolved []*codeLens
		for _, it := range items {
			var l lspCodeLens
			if json.Unmarshal(it, &l) != nil {
				continue
			}
			p := a.lsp.FromLSP(v.buf(), l.Range.Start)
			cl := &codeLens{raw: it, line: p.Line, command: l.Command}
			m[p.Line] = append(m[p.Line], cl)
			if l.Command == nil {
				unresolved = append(unresolved, cl)
			}
		}
		// keep the previous titles while unresolved lenses load, so rows don't flicker
		for line, ls := range m {
			for _, l := range ls {
				if l.command != nil {
					continue
				}
				for _, old := range v.lenses[line] {
					if old.command != nil {
						l.command = old.command
						break
					}
				}
			}
		}
		v.setLenses(m)
		if c.Cap("codeLensProvider") != nil && len(unresolved) > 0 && len(unresolved) < 500 {
			if prov, _ := c.Cap("codeLensProvider").(map[string]any); prov != nil && prov["resolveProvider"] == true {
				for _, l := range unresolved {
					cl := l
					a.lsp.Request(v.buf(), "codeLens/resolve", cl.raw, func(raw json.RawMessage, err error) {
						var r lspCodeLens
						if err == nil && json.Unmarshal(raw, &r) == nil && r.Command != nil {
							cl.command = r.Command
						}
					})
				}
			}
		}
	})
}

// runCodeLens executes a lens command, handling VS Code's client side commands.
func (a *App) runCodeLens(v *EditorView, l *codeLens) {
	if l.run != nil {
		l.run()
		return
	}
	cmd := l.command
	if cmd == nil || cmd.Command == "" {
		return
	}
	switch cmd.Command {
	case "editor.action.showReferences", "editor.action.peekLocations", "editor.action.goToLocations":
		// arguments: uri, position, locations
		if len(cmd.Arguments) >= 3 {
			data, _ := json.Marshal(cmd.Arguments[2])
			var locs []lsp.Location
			if json.Unmarshal(data, &locs) == nil {
				if len(locs) == 0 {
					a.setStatusMessage("No results.")
					return
				}
				v.SetCursor(buffer.Pos{Line: l.line, Col: v.buf().FirstNonWhitespace(l.line)})
				a.showPeek(v, strings.TrimSpace(cmd.Title), locs)
				return
			}
		}
	}
	if _, ok := a.commands[cmd.Command]; ok {
		a.runLSPCommand(v, cmd)
		return
	}
	if c := a.lsp.Client(v.buf()); c != nil {
		supported := false
		if prov, _ := c.Cap("executeCommandProvider").(map[string]any); prov != nil {
			if list, _ := prov["commands"].([]any); list != nil {
				for _, x := range list {
					supported = supported || x == cmd.Command
				}
			}
		}
		if !supported {
			a.setStatusMessage("The command '" + cmd.Command + "' needs a VS Code extension and isn't available.")
			return
		}
	}
	a.runLSPCommand(v, cmd)
}

// drawLensRow draws the code lenses of line on screen row y.
func (v *EditorView) drawLensRow(line, y int) {
	a := v.app
	th := a.theme
	r := v.rect
	bgc := th.C("editor.background")
	a.cv.Fill(Rect{r.X - v.gutterW, y, v.gutterW + r.W, 1}, ' ', styleFB(th.C("editor.foreground"), bgc))
	lenses := v.lensTitles(line)
	x := r.X + v.visualX(line, v.buf().FirstNonWhitespace(line)) - v.leftCol
	st := styleFB(th.C("editorCodeLens.foreground"), bgc)
	for i, l := range lenses {
		if i > 0 {
			x = a.cv.Text(x, y, r.X+r.W, " | ", st)
		}
		title := l.command.Title
		hover := a.mouseY == y && a.mouseX >= x && a.mouseX < x+StrWidth(title)
		tst := st
		if hover {
			tst = styleFB(th.C("editorLink.activeForeground"), bgc).Underline(true)
		}
		start := x
		x = a.cv.Text(x, y, r.X+r.W, title, tst)
		lens := l
		a.addHit(Rect{start, y, x - start, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress && ev.Button == tcell.Button1 {
				a.runCodeLens(v, lens)
			}
			return true
		})
	}
}
