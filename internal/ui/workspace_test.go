package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/config"
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
