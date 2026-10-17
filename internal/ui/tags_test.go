package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
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
