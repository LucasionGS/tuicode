package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGoTesting(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	src := "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif 1+1 != 2 {\n\t\tt.Fatal(\"math is broken\")\n\t}\n}\n\nfunc TestBroken(t *testing.T) {\n\tt.Errorf(\"want %d, got %d\", 3, 4)\n}\n\nfunc helper() {}\n"
	// reuse the real build cache (go env reads it from the real home) so the test compiles fast
	cache, _ := exec.Command("go", "env", "GOCACHE").Output()
	dir := tempWorkspace(t, map[string]string{"go.mod": "module calc\n\ngo 1.21\n", "calc_test.go": src})
	if c := strings.TrimSpace(string(cache)); c != "" {
		t.Setenv("GOCACHE", c)
	} else {
		t.Setenv("GOCACHE", t.TempDir())
	}
	t.Setenv("GOFLAGS", "-mod=mod")
	h := newHarness(t, 140, 40, Options{Paths: []string{filepath.Join(dir, "calc_test.go")}})
	v := h.a.ActiveView()
	if tests := v.doc.testsInDoc(); len(tests) != 2 || tests[4] != "TestAdd" || tests[10] != "TestBroken" {
		t.Fatalf("discovered: %v", tests)
	}
	h.flush()
	if !h.contains("run test | debug test") {
		h.dump()
		t.Fatal("test lenses missing")
	}
	h.a.RunCommand("testing.runCurrentFile", nil)
	add := h.a.testAt(v.doc, 4)
	broken := h.a.testAt(v.doc, 10)
	for i := 0; i < 600 && (h.a.testing.running != nil || add.status < testPassed || broken.status < testPassed); i++ {
		time.Sleep(20 * time.Millisecond)
		h.flush()
	}
	if add.status != testPassed || broken.status != testFailed {
		t.Fatalf("statuses: add %d broken %d\n%s", add.status, broken.status, strings.Join(broken.output, "\n"))
	}
	// the failing line gets a diagnostic
	found := false
	for _, d := range v.doc.Diags {
		if d.Range.Start.Line == 11 && strings.Contains(d.Message, "want 3, got 4") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics: %+v", v.doc.Diags)
	}
	h.a.RunCommand("workbench.view.testing", nil)
	h.flush()
	if h.a.sidebarView != "testing" || len(h.a.testing.rows) != 3 || !h.contains("TESTING") {
		h.dump()
		t.Fatal("testing view rows missing")
	}
	if out := os.Getenv("TUI_SHOT"); out != "" {
		h.a.focusEditor()
		h.flush()
		h.snapshot(filepath.Join(out, "testing.json"))
	}
}

func TestDebugGoTest(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv not installed")
	}
	src := "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tx := 41\n\tx++\n\tif x != 42 {\n\t\tt.Fatal(x)\n\t}\n}\n"
	dir := tempWorkspace(t, map[string]string{"go.mod": "module calc\n\ngo 1.21\n", "calc_test.go": src})
	t.Setenv("GOCACHE", filepath.Join(os.Getenv("REAL_HOME"), ".cache", "go-build"))
	t.Setenv("GOPATH", filepath.Join(os.Getenv("REAL_HOME"), "go"))
	t.Setenv("SHELL", "/bin/cat")
	setSandbox("")
	h := newHarness(t, 140, 40, Options{Paths: []string{filepath.Join(dir, "calc_test.go")}})
	v := h.a.ActiveView()
	h.a.toggleBreakpoint(v.buf().Path, 6) // x++
	h.a.debugTest(h.a.testAt(v.doc, 4))
	for i := 0; i < 600 && !(h.a.debug != nil && h.a.debug.state == "stopped" && len(h.a.debug.frames) > 0); i++ {
		time.Sleep(50 * time.Millisecond)
		h.flush()
	}
	if h.a.debug == nil || h.a.debug.state != "stopped" || h.a.debug.frames[h.a.debug.frameIdx].Line != 7 {
		h.dump()
		t.Fatal("did not stop in the test")
	}
	h.a.debug.stop()
}

func TestDenoTesting(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not installed")
	}
	src := "Deno.test(\"adds\", () => {\n  if (1 + 1 !== 2) throw new Error(\"bad\");\n});\n\nDeno.test(\"broken thing\", () => {\n  throw new Error(\"expected 3 got 4\");\n});\n"
	dir := tempWorkspace(t, map[string]string{"deno.json": "{}\n", "main_test.ts": src})
	t.Setenv("DENO_DIR", t.TempDir())
	h := newHarness(t, 140, 40, Options{Paths: []string{filepath.Join(dir, "main_test.ts")}})
	v := h.a.ActiveView()
	if tests := v.doc.testsInDoc(); len(tests) != 2 || tests[0] != "adds" || tests[4] != "broken thing" {
		t.Fatalf("discovered: %v", tests)
	}
	h.flush()
	if !h.contains("▶ Run Test") {
		h.dump()
		t.Fatal("deno test lens missing")
	}
	h.a.RunCommand("testing.runCurrentFile", nil)
	adds, broken := h.a.testAt(v.doc, 0), h.a.testAt(v.doc, 4)
	for i := 0; i < 1000 && (h.a.testing.running != nil || adds.status < testPassed || broken.status < testPassed); i++ {
		time.Sleep(20 * time.Millisecond)
		h.flush()
	}
	if adds.status != testPassed || broken.status != testFailed {
		t.Fatalf("statuses: adds %d broken %d", adds.status, broken.status)
	}
	found := false
	for _, d := range v.doc.Diags {
		if d.Range.Start.Line == 5 && strings.Contains(d.Message, "expected 3 got 4") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics: %+v", v.doc.Diags)
	}
}

// TestVitest needs TUI_VITEST_DIR: a project with vitest installed and the sum.test.ts below.
func TestVitest(t *testing.T) {
	dir := os.Getenv("TUI_VITEST_DIR")
	if dir == "" {
		t.Skip("set TUI_VITEST_DIR")
	}
	tempWorkspace(t, nil)
	setSandbox("")
	file := filepath.Join(dir, "sum.test.ts")
	h := newHarness(t, 140, 40, Options{Paths: []string{file}})
	v := h.a.ActiveView()
	tests := v.doc.testsInDoc()
	if tests[3] != "math adds" || tests[6] != "math breaks" || tests[11] != "top level" {
		t.Fatalf("discovered: %v", tests)
	}
	h.a.RunCommand("testing.runCurrentFile", nil)
	adds, breaks, top := h.a.testAt(v.doc, 3), h.a.testAt(v.doc, 6), h.a.testAt(v.doc, 11)
	for i := 0; i < 1500 && (h.a.testing.running != nil || adds.status < testPassed); i++ {
		time.Sleep(20 * time.Millisecond)
		h.flush()
	}
	if adds.status != testPassed || breaks.status != testFailed || top.status != testPassed {
		t.Fatalf("statuses: %d %d %d", adds.status, breaks.status, top.status)
	}
	found := false
	for _, d := range v.doc.Diags {
		if d.Range.Start.Line == 7 && strings.Contains(d.Message, "expected 2 to be 3") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics: %+v", v.doc.Diags)
	}
}

func TestCargoTesting(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not installed")
	}
	src := "pub fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\n\n#[cfg(test)]\nmod tests {\n    use super::*;\n\n    #[test]\n    fn adds() {\n        assert_eq!(add(1, 1), 2);\n    }\n\n    #[test]\n    fn breaks() {\n        assert_eq!(add(1, 1), 3);\n    }\n}\n"
	dir := tempWorkspace(t, map[string]string{"Cargo.toml": "[package]\nname = \"calc\"\nversion = \"0.1.0\"\nedition = \"2021\"\n", "src/lib.rs": src})
	t.Setenv("CARGO_TARGET_DIR", t.TempDir())
	// rustup finds its toolchains through the real home
	if real := os.Getenv("REAL_HOME"); real != "" {
		t.Setenv("RUSTUP_HOME", filepath.Join(real, ".rustup"))
		t.Setenv("CARGO_HOME", filepath.Join(real, ".cargo"))
	} else {
		t.Skip("set REAL_HOME")
	}
	h := newHarness(t, 140, 40, Options{Paths: []string{filepath.Join(dir, "src", "lib.rs")}})
	v := h.a.ActiveView()
	if tests := v.doc.testsInDoc(); tests[9] != "adds" || tests[14] != "breaks" {
		t.Fatalf("discovered: %v", tests)
	}
	h.a.RunCommand("testing.runCurrentFile", nil)
	adds, breaks := h.a.testAt(v.doc, 9), h.a.testAt(v.doc, 14)
	for i := 0; i < 3000 && (h.a.testing.running != nil || adds.status < testPassed); i++ {
		time.Sleep(20 * time.Millisecond)
		h.flush()
	}
	if adds.status != testPassed || breaks.status != testFailed {
		t.Fatalf("statuses: %d %d\n%s", adds.status, breaks.status, strings.Join(h.a.output.channel("Test Results").lines, "\n"))
	}
	found := false
	for _, d := range v.doc.Diags {
		if d.Range.Start.Line == 15 && strings.Contains(d.Message, "left == right") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics: %+v\n%s", v.doc.Diags, strings.Join(h.a.output.channel("Test Results").lines, "\n"))
	}
}
