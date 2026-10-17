package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"tuicode/internal/syntax"
)

func TestDenoOrNodeServer(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	for _, name := range []string{"deno", "typescript-language-server"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	for name, content := range map[string]string{"server/deno.json": "{}", "server/main.ts": "", "web/package.json": "{}", "web/src/app.ts": ""} {
		p := filepath.Join(root, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(content), 0o644)
	}
	m := NewManager(root, nil)
	ts := syntax.ByID("typescript")
	if cmd := m.ServerCommandFor(ts, filepath.Join(root, "server", "main.ts")); len(cmd) == 0 || filepath.Base(cmd[0]) != "deno" {
		t.Fatalf("server file: %v", cmd)
	}
	if cmd := m.ServerCommandFor(ts, filepath.Join(root, "web", "src", "app.ts")); len(cmd) == 0 || filepath.Base(cmd[0]) != "typescript-language-server" {
		t.Fatalf("web file: %v", cmd)
	}
	// "deno.enable" decides for the whole workspace
	m.Configuration = func(section string) any {
		if section == "deno" {
			return map[string]any{"enable": true}
		}
		return nil
	}
	if cmd := m.ServerCommandFor(ts, filepath.Join(root, "web", "src", "app.ts")); len(cmd) == 0 || filepath.Base(cmd[0]) != "deno" {
		t.Fatalf("deno.enable: %v", cmd)
	}
}
