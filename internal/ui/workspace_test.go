package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/config"
	"tuicode/internal/lsp"
)

func TestMultiRootWorkspace(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{
		"proj.code-workspace": `{
	// comments are allowed
	"folders": [
		{ "name": "Client", "path": "client" },
		{ "name": "Server", "path": "server" },
		{ "name": "Root", "path": "." },
	],
	"settings": { "editor.tabSize": 3 },
}`,
		"client/src/app.tsx":    "export const App = () => null;\n",
		"server/src/index.ts":   "console.log(\"server\");\n",
		"shared/types.d.ts":     "export type Id = number;\n",
		"README.md":             "# proj\n",
		"client/.vscode/x.json": "{}\n",
	})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "proj.code-workspace")}})
	a := h.a
	if a.workspace == nil || len(a.folders()) != 3 {
		t.Fatalf("workspace not loaded: %v", a.folders())
	}
	if a.root != dir {
		t.Fatalf("primary root %q, want %q", a.root, dir)
	}
	if got := a.settings.Int("editor.tabSize", "", 0); got != 3 {
		t.Fatalf("workspace settings: tabSize %d", got)
	}
	h.flush()
	if !h.contains("PROJ (WORKSPACE)") || !h.contains("Client") || !h.contains("Server") || !h.contains("Root") {
		h.dump()
		t.Fatal("explorer roots missing")
	}
	// the first folder starts expanded
	if !h.contains("src") {
		h.dump()
		t.Fatal("first folder not expanded")
	}
	// revealing a file inside the second folder expands it
	a.OpenFile(filepath.Join(dir, "server", "src", "index.ts"), false)
	a.explorer.reveal(filepath.Join(dir, "server", "src", "index.ts"), false)
	h.flush()
	if !h.contains("index.ts") {
		h.dump()
		t.Fatal("reveal in second folder failed")
	}
	// quick open sees files of every folder once
	var files []string
	done := make(chan bool, 1)
	a.workspaceFiles(func() { done <- true })
	for i := 0; i < 100; i++ {
		h.flush()
		select {
		case <-done:
			files = a.fileIdx.files
			i = 100
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	count := map[string]int{}
	for _, f := range files {
		count[f]++
	}
	for _, want := range []string{"client/src/app.tsx", "server/src/index.ts", "shared/types.d.ts", "README.md"} {
		if count[filepath.Join(dir, want)] != 1 {
			t.Fatalf("quick open %s seen %d times in %v", want, count[filepath.Join(dir, want)], files)
		}
	}
	if !strings.Contains(a.windowTitle(), "proj (Workspace)") {
		t.Fatalf("title: %q", a.windowTitle())
	}
}

func TestMultiRootTasks(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{
		"p.code-workspace":    `{"folders": [{"name": "Client", "path": "client"}, {"name": "Server", "path": "server"}]}`,
		"client/package.json": `{"scripts": {"dev": "vite"}}`,
		"server/deno.json":    `{"tasks": {"dev": "deno run -A main.ts"}}`,
	})
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "p.code-workspace")}})
	got := map[string]string{}
	for _, tk := range h.a.collectTasks() {
		got[tk.Label] = tk.Cwd
	}
	if got["npm: dev - Client"] != filepath.Join(dir, "client") || got["deno: dev - Server"] != filepath.Join(dir, "server") {
		t.Fatalf("tasks: %v", got)
	}
}

func TestRenameUpdatesImports(t *testing.T) {
	if os.Getenv("TUI_LSP") == "" {
		t.Skip("set TUI_LSP")
	}
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	dir := tempWorkspace(t, map[string]string{
		"tsconfig.json":         `{"compilerOptions": {"strict": true}}`,
		"main.ts":               "import { a } from \"./a\";\nconsole.log(a);\n",
		"a.ts":                  "export const a = 1;\n",
		".vscode/settings.json": `{"typescript.updateImportsOnFileMove.enabled": "always"}`,
	})
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "main.ts")}})
	v := h.a.ActiveView()
	for i := 0; i < 100 && (h.a.lsp.Client(v.buf()) == nil || h.a.lsp.Client(v.buf()).Cap("workspace.fileOperations.willRename") == nil); i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	if c := h.a.lsp.Client(v.buf()); c == nil || c.Cap("workspace.fileOperations.willRename") == nil {
		if c != nil {
			t.Logf("workspace caps: %v", c.Capabilities["workspace"])
		}
		t.Skip("server does not support willRenameFiles")
	}
	done := false
	h.a.movePath(filepath.Join(dir, "a.ts"), filepath.Join(dir, "b.ts"), func(err error) {
		if err != nil {
			t.Error(err)
		}
		done = true
	})
	for i := 0; i < 100 && !done; i++ {
		time.Sleep(50 * time.Millisecond)
		h.flush()
	}
	if !strings.Contains(h.text(), "./b") {
		t.Fatalf("import not updated: %q", h.text())
	}
	h.a.lsp.ShutdownAll()
}

func TestMoreCommands(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "f(x, [y])\nabcd\n", "other/b.txt": "b\n"})
	h := newHarness(t, 120, 30, Options{Paths: []string{dir}})
	h.a.OpenFile(filepath.Join(dir, "a.txt"), false)
	h.a.focusEditor()
	v := h.a.ActiveView()
	v.SetCursor(bpos(0, 6))
	h.a.RunCommand("editor.action.removeBrackets", nil)
	if got := v.buf().LineString(0); got != "f(x, y)" {
		t.Fatalf("remove brackets: %q", got)
	}
	v.SetCursor(bpos(1, 2))
	h.a.RunCommand("editor.action.transpose", nil)
	if got := v.buf().LineString(1); got != "acbd" {
		t.Fatalf("transpose: %q", got)
	}
	h.a.RunCommand("editor.action.deleteAllLeft", nil)
	if got := v.buf().LineString(1); got != "d" {
		t.Fatalf("delete all left: %q", got)
	}
	// maximizing a group hides the others until toggled back
	h.a.RunCommand("workbench.action.splitEditor", nil)
	h.a.RunCommand("workbench.action.toggleMaximizeEditorGroup", nil)
	if len(h.a.groups) != 1 {
		t.Fatalf("maximize: %d groups", len(h.a.groups))
	}
	h.a.RunCommand("workbench.action.toggleMaximizeEditorGroup", nil)
	if len(h.a.groups) != 2 || h.a.activeGroup != 1 {
		t.Fatalf("restore: %d groups, active %d", len(h.a.groups), h.a.activeGroup)
	}
	// adding a folder creates a multi-root workspace that can be saved
	h.a.addRootFolder(filepath.Join(dir, "other"))
	if len(h.a.folders()) != 2 {
		t.Fatalf("folders: %v", h.a.folders())
	}
	h.a.workspace.File = filepath.Join(dir, "x.code-workspace")
	h.a.saveWorkspaceFile()
	ws, err := config.LoadWorkspace(filepath.Join(dir, "x.code-workspace"))
	if err != nil || len(ws.Folders) != 2 || ws.Folders[1].Path != filepath.Join(dir, "other") {
		t.Fatalf("saved workspace: %+v %v", ws, err)
	}
}

func TestExplorerMultiSelect(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "a", "b.txt": "b", "c.txt": "c", "sub/keep.txt": "k"})
	h := newHarness(t, 120, 30, Options{Paths: []string{dir}})
	e := h.a.explorer
	h.a.showSidebar("explorer")
	h.flush()
	row := func(name string) int {
		for i, r := range e.rows {
			if r.node != nil && r.node.name == name {
				return i
			}
		}
		t.Fatalf("no row %s", name)
		return -1
	}
	e.selected = row("a.txt")
	h.a.sidebarHandleKey("shift+down", "", false)
	h.a.sidebarHandleKey("shift+down", "", false)
	if got := len(e.selectedNodes()); got != 3 {
		t.Fatalf("selected %d", got)
	}
	// cut all three and paste into the folder
	h.a.sidebarHandleKey("ctrl+x", "", false)
	e.selected = row("sub")
	e.multi = nil
	h.a.sidebarHandleKey("ctrl+v", "", false)
	h.flush()
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if !fileExists(filepath.Join(dir, "sub", n)) {
			t.Fatalf("%s not moved", n)
		}
	}
}

func TestJSONSchemaValidation(t *testing.T) {
	if os.Getenv("TUI_LSP") == "" {
		t.Skip("set TUI_LSP")
	}
	if _, err := exec.LookPath("vscode-json-language-server"); err != nil {
		t.Skip("vscode-json-language-server not installed")
	}
	// the schema comes from schemastore.org, like in VS Code
	dir := tempWorkspace(t, map[string]string{"package.json": "{\n  \"name\": \"x\",\n  \"version\": \"1.0.0\",\n  \"keywords\": \"abc\"\n}\n"})
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "package.json")}})
	v := h.a.ActiveView()
	for i := 0; i < 150 && len(v.doc.Diags) == 0; i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	if len(v.doc.Diags) != 1 || !strings.Contains(v.doc.Diags[0].Message, "array") {
		t.Fatalf("diagnostics: %+v", v.doc.Diags)
	}
	h.a.lsp.ShutdownAll()
}

func TestBreadcrumbPicker(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"go.mod": "module x\n", "src/app/main.go": "package main\n", "src/app/util.go": "package main\n", "src/lib/x.go": "package lib\n"})
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "src", "app", "main.go")}})
	h.flush()
	for y, row := range h.screen() {
		if x := strings.Index(row, "app"); x >= 0 && strings.Contains(row, "main.go") && y > 1 {
			cx := len([]rune(row[:x]))
			h.a.handleEvent(tcell.NewEventMouse(cx, y, tcell.Button1, 0))
			h.a.handleEvent(tcell.NewEventMouse(cx, y, tcell.ButtonNone, 0))
			break
		}
	}
	if h.a.ctxMenu == nil {
		h.dump()
		t.Fatal("no breadcrumb picker")
	}
	var labels []string
	for _, it := range h.a.ctxMenu.items {
		labels = append(labels, it.Label)
	}
	joined := strings.Join(labels, "|")
	if !strings.Contains(joined, "app") || !strings.Contains(joined, "lib") {
		t.Fatalf("picker: %v", labels)
	}
}

func TestProblemsFilter(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.go": "x\n", "web/b.ts": "y\n"})
	h := newHarness(t, 120, 30, Options{Paths: []string{dir}})
	d := func(msg string) lsp.Diagnostic {
		return lsp.Diagnostic{Message: msg, Severity: 1}
	}
	h.a.lsp.SetDiagnostics(filepath.Join(dir, "a.go"), "test", []lsp.Diagnostic{d("undefined: foo"), d("unused variable")})
	h.a.lsp.SetDiagnostics(filepath.Join(dir, "web", "b.ts"), "test", []lsp.Diagnostic{d("Cannot find name 'foo'")})
	h.a.showPanel("problems")
	count := func() int {
		h.flush()
		n := 0
		for _, r := range h.a.problems.rows {
			if r.diag != nil {
				n++
			}
		}
		return n
	}
	if n := count(); n != 3 {
		t.Fatalf("all: %d", n)
	}
	h.a.handleEvent(tcell.NewEventKey(tcell.KeyCtrlF, 0, tcell.ModCtrl))
	h.typeText("foo")
	if n := count(); n != 2 {
		t.Fatalf("text filter: %d", n)
	}
	h.a.problems.filter.SetText("foo, !**/*.ts")
	h.a.problems.dirty = true
	if n := count(); n != 1 {
		t.Fatalf("exclude glob: %d", n)
	}
	if !h.contains("undefined: foo") || h.contains("Cannot find name") {
		h.dump()
		t.Fatal("wrong rows shown")
	}
}

func TestLaunchInputs(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{".vscode/tasks.json": `{"version": "2.0.0",
		"inputs": [{"id": "who", "type": "pickString", "description": "Who?", "options": ["world", "you"]}],
		"tasks": [{"label": "greet", "type": "shell", "command": "echo hello ${input:who}"}]}`})
	h := newHarness(t, 120, 30, Options{Paths: []string{dir}})
	var got map[string]any
	h.a.resolveInteractiveVars(map[string]any{"command": "echo hello ${input:who}", "processId": "${command:pickProcess}"}, func(r map[string]any) { got = r })
	if h.a.quick == nil {
		t.Fatal("no picker for the input")
	}
	h.a.quick.input.SetText("you")
	h.a.quick.refilter()
	h.key(tcell.KeyEnter, 0, 0)
	if h.a.quick == nil {
		t.Fatal("no process picker")
	}
	h.a.quick.input.SetText("")
	h.a.quick.refilter()
	h.key(tcell.KeyEnter, 0, 0)
	if got == nil || got["command"] != "echo hello you" {
		t.Fatalf("resolved: %v", got)
	}
	if _, isNum := got["processId"].(int); !isNum {
		t.Fatalf("processId should be a number: %#v", got["processId"])
	}
}

func TestPrimarySelectionPaste(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.txt": "hello world\n\n"})
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "a.txt")}})
	v := h.a.ActiveView()
	h.flush()
	v.SetSelections([]buffer.Selection{{Anchor: buffer.Pos{Col: 6}, Active: buffer.Pos{Col: 11}}}, 0)
	h.a.syncPrimarySelection()
	// middle click on the empty second line pastes "world"
	_, y := h.find("hello world")
	x, _ := h.find("hello world")
	h.a.handleEvent(tcell.NewEventMouse(x, y+1, tcell.Button3, 0))
	h.a.handleEvent(tcell.NewEventMouse(x, y+1, tcell.ButtonNone, 0))
	if got := h.text(); got != "hello world\nworld\n" {
		t.Fatalf("middle click paste: %q", got)
	}
}

func TestLineDiffEdits(t *testing.T) {
	cases := [][2]string{
		{"a\nb\nc\n", "a\nB\nc\n"},
		{"a\nb\nc\n", "a\nc\n"},
		{"a\nb\nc", "a\nb\nc\nd"},
		{"a\nb\nc", "a"},
		{"x", "y\nz\n"},
		{"a\n\n\nb\n", "a\n\nb\n"},
	}
	for _, c := range cases {
		b := buffer.NewFromString(c[0])
		b.ApplyEdits(lineDiffEdits(strings.Split(c[0], "\n"), strings.Split(c[1], "\n")), nil, "")
		if got := b.Text(); got != c[1] {
			t.Errorf("%q -> %q: got %q", c[0], c[1], got)
		}
	}
}

func TestExternalFormatter(t *testing.T) {
	if _, err := exec.LookPath("prettier"); err != nil {
		t.Skip("prettier not installed")
	}
	src := "const a = {b:1,\n    c:2}\n"
	dir := tempWorkspace(t, map[string]string{"a.ts": src, ".vscode/settings.json": `{"editor.defaultFormatter": "esbenp.prettier-vscode"}`})
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "a.ts")}})
	h.a.RunCommand("editor.action.formatDocument", nil)
	for i := 0; i < 300 && h.text() == src; i++ {
		time.Sleep(20 * time.Millisecond)
		h.flush()
	}
	if got := h.text(); got != "const a = { b: 1, c: 2 };\n" {
		t.Fatalf("formatted: %q", got)
	}
}

// TestESLint needs TUI_ESLINT_DIR: a project with eslint installed, an eslint.config.js and main.js.
func TestESLint(t *testing.T) {
	dir := os.Getenv("TUI_ESLINT_DIR")
	if dir == "" || os.Getenv("TUI_LSP") == "" {
		t.Skip("set TUI_ESLINT_DIR and TUI_LSP")
	}
	tempWorkspace(t, nil)
	setSandbox("")
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "main.js")}})
	v := h.a.ActiveView()
	has := func(src string) bool {
		for _, d := range v.doc.Diags {
			if d.Source == src || strings.Contains(d.Source, src) {
				return true
			}
		}
		return false
	}
	for i := 0; i < 200 && !has("eslint"); i++ {
		time.Sleep(100 * time.Millisecond)
		h.flush()
	}
	if !has("eslint") {
		for _, c := range h.a.output.channels {
			t.Logf("%s: %v", c.name, c.lines)
		}
		t.Fatalf("no eslint diagnostics: %+v", v.doc.Diags)
	}
	// ESLint's quick fixes show up in the code action menu and apply
	v.SetCursor(buffer.Pos{Col: 1})
	h.a.showCodeActions(v, "")
	for i := 0; i < 100 && v.codeActions == nil; i++ {
		time.Sleep(50 * time.Millisecond)
		h.flush()
	}
	if v.codeActions == nil {
		t.Fatal("no code actions")
	}
	var titles []string
	pick := -1
	for i, ca := range v.codeActions.actions {
		titles = append(titles, ca.Title)
		if strings.Contains(ca.Title, "no-var") && strings.HasPrefix(ca.Title, "Fix") {
			pick = i
		}
	}
	if pick < 0 {
		t.Fatalf("no ESLint fix in %v", titles)
	}
	h.a.applyCodeAction(v, v.codeActions.actions[pick])
	for i := 0; i < 100 && strings.HasPrefix(h.text(), "var"); i++ {
		time.Sleep(50 * time.Millisecond)
		h.flush()
	}
	if !strings.HasPrefix(h.text(), "let unused") {
		t.Fatalf("fix not applied: %q", h.text())
	}
	v.buf().Undo()
	h.a.lsp.ShutdownAll()
}

func TestProblemHoverActions(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{"a.go": "package a\n\nvar x = y\n"})
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "a.go")}})
	v := h.a.ActiveView()
	h.a.lsp.SetDiagnostics(filepath.Join(dir, "a.go"), "test", []lsp.Diagnostic{{Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 8}, End: lsp.Position{Line: 2, Character: 9}}, Severity: 1, Message: "undefined: y"}})
	h.a.showHover(v, buffer.Pos{Line: 2, Col: 8}, true)
	h.flush()
	if !h.contains("undefined: y") || !h.contains("Quick Fix... (Ctrl+.)") || !h.contains("View Problem") {
		h.dump()
		t.Fatal("problem hover actions missing")
	}
}
