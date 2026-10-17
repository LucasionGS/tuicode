package ui

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"time"

	"tuicode/internal/buffer"
)

type clipTool struct {
	copy         []string
	paste        []string
	copyPrimary  []string // the X11/Wayland primary selection, empty when unsupported
	pastePrimary []string
}

var clipTools = func() *clipTool {
	has := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" && has("wl-copy") && has("wl-paste") {
		return &clipTool{copy: []string{"wl-copy"}, paste: []string{"wl-paste", "--no-newline"},
			copyPrimary: []string{"wl-copy", "--primary"}, pastePrimary: []string{"wl-paste", "--primary", "--no-newline"}}
	}
	if os.Getenv("DISPLAY") != "" {
		if has("xclip") {
			return &clipTool{copy: []string{"xclip", "-selection", "clipboard", "-in"}, paste: []string{"xclip", "-selection", "clipboard", "-out"},
				copyPrimary: []string{"xclip", "-selection", "primary", "-in"}, pastePrimary: []string{"xclip", "-selection", "primary", "-out"}}
		}
		if has("xsel") {
			return &clipTool{copy: []string{"xsel", "--clipboard", "--input"}, paste: []string{"xsel", "--clipboard", "--output"},
				copyPrimary: []string{"xsel", "--primary", "--input"}, pastePrimary: []string{"xsel", "--primary", "--output"}}
		}
	}
	if has("pbcopy") {
		return &clipTool{copy: []string{"pbcopy"}, paste: []string{"pbpaste"}}
	}
	return nil
}()

// setClipboard copies text to the system clipboard (or OSC 52 / internal fallback).
func (a *App) setClipboard(text string) {
	a.clipboardText = text
	a.clipboardFullLine = false
	a.clipboardParts = nil
	if clipTools != nil && sandboxRoot == "" {
		cmd := exec.Command(clipTools.copy[0], clipTools.copy[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
		}
	}
	if (clipTools == nil || os.Getenv("SSH_TTY") != "") && a.screen != nil && sandboxRoot == "" {
		a.screen.SetClipboard([]byte(text))
	}
}

// getClipboard returns the clipboard text.
func (a *App) getClipboard() string {
	if clipTools != nil && sandboxRoot == "" {
		cmd := exec.Command(clipTools.paste[0], clipTools.paste[1:]...)
		var out bytes.Buffer
		cmd.Stdout = &out
		done := make(chan error, 1)
		if err := cmd.Start(); err == nil {
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err == nil {
					return strings.ReplaceAll(out.String(), "\r\n", "\n")
				}
			case <-time.After(time.Second):
				_ = cmd.Process.Kill()
			}
		}
	}
	return a.clipboardText
}

// Copy copies selections (or whole lines when nothing is selected).
func (v *EditorView) Copy() {
	a := v.app
	b := v.buf()
	if !v.HasSelection() {
		if !a.settings.Bool("editor.emptySelectionClipboard", "") {
			return
		}
		var parts []string
		seen := map[int]bool{}
		for _, s := range v.sels {
			l := s.Active.Line
			if seen[l] {
				continue
			}
			seen[l] = true
			parts = append(parts, b.LineString(l))
		}
		text := strings.Join(parts, "\n") + "\n"
		a.setClipboard(text)
		a.clipboardFullLine = true
		if len(parts) > 1 {
			a.clipboardParts = parts
		}
		return
	}
	var parts []string
	for _, s := range v.sels {
		parts = append(parts, b.TextRange(s.Range()))
	}
	a.setClipboard(strings.Join(parts, "\n"))
	if len(parts) > 1 {
		a.clipboardParts = parts
	}
}

// Cut copies then deletes selections (or whole lines).
func (v *EditorView) Cut() {
	if v.buf().ReadOnly {
		return
	}
	if !v.HasSelection() {
		v.Copy()
		full := v.app.clipboardFullLine
		parts := v.app.clipboardParts
		v.DeleteLines()
		v.app.clipboardFullLine = full
		v.app.clipboardParts = parts
		return
	}
	v.Copy()
	parts := v.app.clipboardParts
	edits := make([]buffer.Edit, len(v.sels))
	for i, s := range v.sels {
		edits[i] = buffer.Edit{Range: s.Range()}
	}
	v.buf().CloseUndoGroup()
	v.applyDeletes(edits)
	v.app.clipboardParts = parts
}

// Paste inserts the clipboard at every cursor.
func (v *EditorView) Paste() {
	a := v.app
	text := a.getClipboard()
	fullLine := a.clipboardFullLine && text == a.clipboardText
	var parts []string
	if text == a.clipboardText {
		parts = a.clipboardParts
	}
	v.PasteText(text, parts, fullLine)
	if a.settings.Bool("editor.formatOnPaste", v.lang()) {
		// formatting the pasted range is left to the language server's range formatter
		r := v.Primary().Range()
		a.formatDocument(v, &r, nil)
	}
}

// PasteText inserts text, distributing parts across cursors when counts match.
func (v *EditorView) PasteText(text string, parts []string, fullLine bool) {
	if v.buf().ReadOnly {
		return
	}
	b := v.buf()
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if len(parts) == 0 && len(v.sels) > 1 {
		lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		if len(lines) == len(v.sels) {
			parts = lines
		}
	}
	distribute := len(parts) == len(v.sels) && len(v.sels) > 1
	edits := make([]buffer.Edit, len(v.sels))
	lineMode := make([]bool, len(v.sels))
	for i, s := range v.sels {
		t := text
		if distribute {
			t = parts[i]
		}
		if fullLine && s.Empty() && !distribute {
			// full-line paste goes above the current line
			p := buffer.Pos{Line: s.Active.Line}
			edits[i] = buffer.Edit{Range: buffer.Range{Start: p, End: p}, Text: t}
			lineMode[i] = true
			continue
		}
		if fullLine && distribute && s.Empty() {
			p := buffer.Pos{Line: s.Active.Line}
			edits[i] = buffer.Edit{Range: buffer.Range{Start: p, End: p}, Text: t + "\n"}
			lineMode[i] = true
			continue
		}
		edits[i] = buffer.Edit{Range: s.Range(), Text: t}
	}
	old := append([]buffer.Selection{}, v.sels...)
	b.CloseUndoGroup()
	v.applyEdits(edits, "paste", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			if lineMode[i] {
				// keep the caret on its (moved) line
				nl := strings.Count(edits[i].Text, "\n")
				out[i] = buffer.Caret(buffer.Pos{Line: old[i].Active.Line + nl, Col: old[i].Active.Col})
				// lines inserted above other cursors shift them as well
				for j := 0; j < i; j++ {
					if lineMode[j] {
						out[i].Active.Line += strings.Count(edits[j].Text, "\n")
						out[i].Anchor = out[i].Active
					}
				}
				out[i].Anchor = out[i].Active
			} else {
				out[i] = buffer.Caret(r.End)
			}
		}
		return out
	})
	b.CloseUndoGroup()
}

// Undo reverts the last edit group.
func (v *EditorView) Undo() {
	if sels, ok := v.buf().Undo(); ok {
		if len(sels) > 0 {
			v.SetSelections(append([]buffer.Selection{}, sels...), -1)
		}
		v.ensurePosVisible(v.Cursor(), false)
	}
}

// Redo re-applies an undone edit group.
func (v *EditorView) Redo() {
	if sels, ok := v.buf().Redo(); ok {
		if len(sels) > 0 {
			v.SetSelections(append([]buffer.Selection{}, sels...), -1)
		}
		v.ensurePosVisible(v.Cursor(), false)
	}
}

// DuplicateSelection duplicates selections, or lines when nothing is selected.
func (v *EditorView) DuplicateSelection() {
	if !v.HasSelection() {
		v.CopyLines(1)
		return
	}
	b := v.buf()
	edits := make([]buffer.Edit, len(v.sels))
	for i, s := range v.sels {
		r := s.Range()
		edits[i] = buffer.Edit{Range: buffer.Range{Start: r.End, End: r.End}, Text: b.TextRange(r)}
	}
	v.applyEdits(edits, "", func(res []buffer.Range) []buffer.Selection {
		out := make([]buffer.Selection, len(res))
		for i, r := range res {
			out[i] = buffer.Selection{Anchor: r.Start, Active: r.End}
		}
		return out
	})
}

// columnSelect extends a column (box) selection with the keyboard.
func (v *EditorView) columnSelect(dy, dx int) {
	if !v.boxSelect || len(v.sels) == 0 {
		v.boxAnchor = v.Primary().Anchor
		v.boxSelect = true
		v.boxCursor = v.Primary().Active
	}
	b := v.buf()
	c := v.boxCursor
	c.Line = clampInt(c.Line+dy, 0, b.LineCount()-1)
	c.Col = max(c.Col+dx, 0)
	v.boxCursor = c
	v.updateBoxSelection(c)
	v.boxSelect = true
	v.ensurePosVisible(c, false)
}

// convertIndentation rewrites leading whitespace as spaces or tabs.
func (v *EditorView) convertIndentation(toSpaces bool) {
	b := v.buf()
	ts := v.tabSize()
	var edits []buffer.Edit
	for l := 0; l < b.LineCount(); l++ {
		ws := b.LeadingWhitespace(l)
		if ws == "" {
			continue
		}
		width := 0
		for _, r := range ws {
			if r == '\t' {
				width += ts - width%ts
			} else {
				width++
			}
		}
		var nw string
		if toSpaces {
			nw = strings.Repeat(" ", width)
		} else {
			nw = strings.Repeat("\t", width/ts) + strings.Repeat(" ", width%ts)
		}
		if nw != ws {
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: buffer.Pos{Line: l}, End: buffer.Pos{Line: l, Col: len([]rune(ws))}}, Text: nw})
		}
	}
	b.InsertSpaces = toSpaces
	v.editLinesKeepSelections(edits)
}

// setPrimarySelection publishes selected text as the primary selection, like VS Code on Linux.
func (a *App) setPrimarySelection(text string) {
	a.primaryText = text
	if clipTools == nil || len(clipTools.copyPrimary) == 0 || sandboxRoot != "" || text == "" {
		return
	}
	cmd := exec.Command(clipTools.copyPrimary[0], clipTools.copyPrimary[1:]...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// getPrimarySelection returns the primary selection (tuicode's own when no tool is available).
func (a *App) getPrimarySelection() string {
	if clipTools == nil || len(clipTools.pastePrimary) == 0 || sandboxRoot != "" {
		return a.primaryText
	}
	out, err := exec.Command(clipTools.pastePrimary[0], clipTools.pastePrimary[1:]...).Output()
	if err != nil {
		return a.primaryText
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n")
}

// syncPrimarySelection publishes the active editor's selection when it changed.
func (a *App) syncPrimarySelection() {
	if !a.settings.Bool("editor.selectionClipboard", "") {
		return
	}
	v := a.ActiveView()
	if v == nil || v.special != nil || !v.HasSelection() {
		return
	}
	text := v.SelectedText()
	if text != a.primaryText && len(text) < 1<<20 {
		a.setPrimarySelection(text)
	}
}
