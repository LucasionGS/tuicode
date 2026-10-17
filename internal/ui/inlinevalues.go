package ui

import (
	"strings"

	"tuicode/internal/buffer"
)

// inlineValues returns "name = value" annotations for lines of the stopped function, like VS Code's
// debug.inlineValues: local variables are shown after the lines that mention them.
func (a *App) inlineValues(v *EditorView, frameLine int) map[int]string {
	s := a.debug
	if s == nil || s.state != "stopped" || frameLine < 0 || len(s.scopes) == 0 {
		return nil
	}
	switch a.settings.Get("debug.inlineValues", "").(type) {
	case bool:
		if !a.settings.Bool("debug.inlineValues", "") {
			return nil
		}
	default:
		if a.settings.String("debug.inlineValues", "") == "off" {
			return nil
		}
	}
	locals, ok := s.vars[s.scopes[0].VariablesReference]
	if !ok || len(locals) == 0 {
		return nil
	}
	values := map[string]string{}
	for _, lv := range locals {
		val := strings.ReplaceAll(lv.Value, "\n", " ")
		if len([]rune(val)) > 40 {
			val = string([]rune(val)[:39]) + "…"
		}
		values[lv.Name] = val
	}
	// the stopped function starts at its symbol, or a screenful above
	start := max(frameLine-60, 0)
	for _, sym := range a.outline.symbolPath(v.doc, buffer.Pos{Line: frameLine}) {
		if sym.Kind == 6 || sym.Kind == 12 || sym.Kind == 9 {
			start = a.lsp.FromLSP(v.buf(), sym.Range.Start).Line
		}
	}
	out := map[int]string{}
	for line := start; line <= frameLine && line < v.buf().LineCount(); line++ {
		l := v.buf().Line(line)
		seen := map[string]bool{}
		var parts []string
		for i := 0; i < len(l); {
			if !isWordRune(l[i]) {
				i++
				continue
			}
			j := i
			for j < len(l) && isWordRune(l[j]) {
				j++
			}
			// skip member accesses like obj.field
			name := string(l[i:j])
			if val, ok := values[name]; ok && !seen[name] && (i == 0 || l[i-1] != '.') && !v.inStringOrComment(buffer.Pos{Line: line, Col: i}) {
				seen[name] = true
				parts = append(parts, name+" = "+val)
			}
			i = j
		}
		if len(parts) > 0 {
			out[line] = strings.Join(parts, ", ")
		}
	}
	return out
}
