package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

func TestAutoCloseTag(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"index.html": "\n", "app.tsx": "\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "index.html")}})
	h.typeText(`<div class="a>b">`)
	if got := h.text(); got != "<div class=\"a>b\"></div>\n" {
		t.Fatalf("close on >: %q", got)
	}
	h.typeText("<br>x<p>")
	if got := h.text(); got != "<div class=\"a>b\"><br>x<p></p></div>\n" {
		t.Fatalf("void tag: %q", got)
	}
	v := h.a.ActiveView()
	v.buf().ApplyEdits([]buffer.Edit{{Range: buffer.Range{Start: buffer.Pos{Line: 1}, End: buffer.Pos{Line: 1}}, Text: "<section><span>a"}}, nil, "")
	v.SetCursor(buffer.Pos{Line: 1, Col: 16})
	h.typeText("</")
	if got := h.text(); got != "<div class=\"a>b\"><br>x<p></p></div>\n<section><span>a</span>" {
		t.Fatalf("close on </: %q", got)
	}

	v.buf().ApplyEdits([]buffer.Edit{{Range: buffer.Range{Start: buffer.Pos{Line: 1, Col: 16}, End: buffer.Pos{Line: 1, Col: 23}}, Text: "\n"}}, nil, "")
	v.SetCursor(buffer.Pos{Line: 2})
	h.typeText("<li>a</")
	if got := v.buf().LineString(2); got != "<li>a</</li>" {
		t.Fatalf("no duplicate close: %q", got)
	}

	h.a.OpenFile(filepath.Join(dir, "app.tsx"), false)
	h.typeText("const a = useState<number>(0); const b = <Foo.Bar x={() => 1}>")
	if got := h.text(); got != "const a = useState<number>(0); const b = <Foo.Bar x={() => 1}></Foo.Bar>\n" {
		t.Fatalf("jsx: %q", got)
	}
}

func TestLinkedEditing(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"index.html": "<div>\n  <div>x</div>\n</div>\n"})
	if err := os.MkdirAll(filepath.Join(dir, ".vscode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".vscode", "settings.json"), []byte(`{"editor.linkedEditing": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, 120, 40, Options{Paths: []string{dir}})
	h.a.OpenFile(filepath.Join(dir, "index.html"), false)
	h.a.focusEditor()
	v := h.a.ActiveView()
	v.SetCursor(buffer.Pos{Line: 0, Col: 4})
	h.key(tcell.KeyBackspace2, 0, 0)
	h.key(tcell.KeyBackspace2, 0, 0)
	h.key(tcell.KeyBackspace2, 0, 0)
	h.typeText("section")
	if got := h.text(); got != "<section>\n  <div>x</div>\n</section>\n" {
		t.Fatalf("linked: %q", got)
	}
	if v.Cursor() != (buffer.Pos{Line: 0, Col: 8}) || len(v.sels) != 1 {
		t.Fatalf("cursor %v sels %d", v.Cursor(), len(v.sels))
	}
	// typing a space leaves the closing tag alone
	h.typeText(" ")
	if got := h.text(); got != "<section >\n  <div>x</div>\n</section>\n" {
		t.Fatalf("space: %q", got)
	}
}

func TestSettingsJSONCompletion(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{".vscode/settings.json": "{\n  \n}\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{dir}})
	h.a.OpenFile(filepath.Join(dir, ".vscode", "settings.json"), false)
	h.a.focusEditor()
	v := h.a.ActiveView()
	v.SetCursor(buffer.Pos{Line: 1, Col: 2})
	h.typeText(`"editor.linkedEd`)
	if v.completion == nil || !v.completion.visible() || v.completion.filtered[0].label != "editor.linkedEditing" {
		h.dump()
		t.Fatal("settings suggestion missing")
	}
	h.key(tcell.KeyEnter, 0, 0)
	if got := h.text(); got != "{\n  \"editor.linkedEditing\": false\n}\n" {
		t.Fatalf("accept: %q", got)
	}
	// the value placeholder is selected; typing replaces it
	h.typeText("t")
	if v.completion == nil || !v.completion.visible() || v.completion.filtered[0].label != "true" {
		h.dump()
		t.Fatal("bool value suggestion missing")
	}
	h.key(tcell.KeyEnter, 0, 0)
	if got := h.text(); got != "{\n  \"editor.linkedEditing\": true\n}\n" {
		t.Fatalf("value: %q", got)
	}
}

func TestEditorGridLayout(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "alpha\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "a.txt")}})
	h.a.RunCommand("workbench.action.editorLayoutTwoByTwoGrid", nil)
	h.flush()
	if len(h.a.groups) != 4 {
		t.Fatalf("groups: %d", len(h.a.groups))
	}
	rects := h.a.groupRects(h.a.rEditor)
	if rects[0].X != rects[1].X || rects[1].Y <= rects[0].Y || rects[2].X <= rects[0].X || rects[3].Y <= rects[2].Y {
		t.Fatalf("not a 2x2 grid: %v", rects)
	}
	h.a.RunCommand("workbench.action.focusBelowGroup", nil)
	if h.a.activeGroup != 1 {
		t.Fatalf("below: %d", h.a.activeGroup)
	}
	h.a.RunCommand("workbench.action.focusRightGroup", nil)
	if h.a.activeGroup != 3 {
		t.Fatalf("right: %d", h.a.activeGroup)
	}
	// closing a column's top group keeps the other column intact
	h.a.closeGroup(2)
	if len(h.a.groupColumns()) != 2 || h.a.groups[2].below {
		t.Fatalf("columns after close: %v", h.a.groupColumns())
	}
	h.a.RunCommand("workbench.action.toggleEditorGroupLayout", nil)
	if len(h.a.groupColumns()) != 1 {
		t.Fatalf("toggle: %v", h.a.groupColumns())
	}
	h.flush()
	if strings.Count(strings.Join(h.screen(), "\n"), "a.txt") < 3 {
		h.dump()
		t.Fatal("stacked groups not drawn")
	}
}

func TestPinnedTabs(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "c.txt": "c\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{dir}})
	h.a.closeAllEditors()
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		h.a.OpenFile(filepath.Join(dir, n), false)
	}
	g := h.a.ActiveGroup()
	h.a.RunCommand("workbench.action.pinEditor", nil) // c.txt
	if g.tabs[0].doc.Title() != "c.txt" || !g.tabs[0].pinned {
		t.Fatalf("pinned tab not moved first")
	}
	h.a.RunCommand("workbench.action.closeActiveEditor", nil)
	if len(g.tabs) != 3 || g.Active().pinned {
		t.Fatalf("ctrl+w closed a pinned tab: %d tabs", len(g.tabs))
	}
	h.a.RunCommand("workbench.action.closeOtherEditors", nil)
	if len(g.tabs) != 2 {
		t.Fatalf("close others: %d tabs", len(g.tabs))
	}
	h.a.RunCommand("workbench.action.closeEditorsInGroup", nil)
	if len(g.tabs) != 1 || g.tabs[0].doc.Title() != "c.txt" {
		t.Fatalf("close all keeps pinned first: %d tabs", len(g.tabs))
	}
	h.a.RunCommand("workbench.action.closeEditorsInGroup", nil)
	if len(g.tabs) != 0 {
		t.Fatalf("second close all: %d tabs", len(g.tabs))
	}
}

func TestCodeLensRows(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "one\ntwo\nthree\nfour\n"})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "a.txt")}})
	v := h.a.ActiveView()
	v.setLenses(map[int][]*codeLens{
		0: {{line: 0, command: &lsp.Command{Title: "2 references"}}},
		2: {{line: 2, command: &lsp.Command{Title: "run test"}}, {line: 2, command: &lsp.Command{Title: "debug test"}}},
	})
	h.flush()
	scr := h.screen()
	joined := strings.Join(scr, "\n")
	if !strings.Contains(joined, "2 references") || !strings.Contains(joined, "run test | debug test") {
		h.dump()
		t.Fatal("lens rows not drawn")
	}
	if v.totalRows() != 7 {
		t.Fatalf("total rows: %d", v.totalRows())
	}
	// moving across lens rows lands on text lines only
	v.SetCursor(buffer.Pos{Line: 1, Col: 1})
	h.key(tcell.KeyDown, 0, 0)
	if v.Cursor().Line != 2 {
		t.Fatalf("down: %v", v.Cursor())
	}
	h.key(tcell.KeyUp, 0, 0)
	h.key(tcell.KeyUp, 0, 0)
	if v.Cursor().Line != 0 {
		t.Fatalf("up: %v", v.Cursor())
	}
	for i := 0; i < len(scr); i++ {
		if strings.Contains(scr[i], "2 references") {
			if !strings.Contains(scr[i+1], "one") {
				t.Fatalf("lens not above its line: %q / %q", scr[i], scr[i+1])
			}
		}
	}
}

func TestMarkdownFolding(t *testing.T) {
	md := "# Title\n\nIntro\n\n## Install\n\n```bash\n# not a heading\nmake\n```\n\n## Usage\n\ntext\n"
	dir := tempWorkspace(t, map[string]string{"README.md": md})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "README.md")}})
	v := h.a.ActiveView()
	cases := map[int][2]int{0: {0, 13}, 4: {4, 9}, 6: {6, 9}, 11: {11, 13}}
	for line, want := range cases {
		f, ok := v.foldRangeAt(line)
		if !ok || f.Start != want[0] || f.End != want[1] {
			t.Errorf("line %d: got %v %v, want %v", line, f, ok, want)
		}
	}
	if _, ok := v.foldRangeAt(7); ok {
		t.Error("a comment in a code block folded like a heading")
	}
	syms := markdownSymbols(v.doc)
	if len(syms) != 1 || syms[0].Name != "Title" || len(syms[0].Children) != 2 || syms[0].Children[1].Name != "Usage" || syms[0].Children[0].Range.End.Line != 10 {
		t.Fatalf("outline: %+v", syms)
	}
}
