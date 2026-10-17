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
	dir := tempWorkspace(t, map[string]string{"go.mod": "module calc\n\ngo 1.21\n", "calc_test.go": src})
	t.Setenv("GOCACHE", t.TempDir())
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
