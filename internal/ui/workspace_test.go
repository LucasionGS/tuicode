package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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
