package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tuicode/internal/buffer"
)

func TestGitBlame(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := tempWorkspace(t, map[string]string{"a.txt": "first\nsecond\n"})
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.com", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "Add a.txt")
	if err := os.MkdirAll(filepath.Join(dir, ".vscode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".vscode", "settings.json"), []byte(`{"git.blame.editorDecoration.enabled": true, "git.blame.statusBarItem.enabled": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "a.txt")}})
	h.a.refreshGit()
	for i := 0; i < 200 && !h.contains("Add a.txt, Ada (now)"); i++ {
		time.Sleep(10 * time.Millisecond)
		h.flush()
	}
	if !h.contains("Add a.txt, Ada (now)") || !h.contains("Ada (now)") {
		h.dump()
		t.Fatal("blame decoration missing")
	}
	// typing makes the line uncommitted
	h.typeText("x")
	for i := 0; i < 200 && !h.contains("Not Committed Yet"); i++ {
		time.Sleep(10 * time.Millisecond)
		h.flush()
	}
	if !h.contains("Not Committed Yet") {
		h.dump()
		t.Fatal("uncommitted blame missing")
	}
}

func TestStageHunk(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := tempWorkspace(t, map[string]string{"a.txt": "one\ntwo\nthree\nfour\nfive\n"})
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.com", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	h := newHarness(t, 120, 30, Options{Paths: []string{filepath.Join(dir, "a.txt")}})
	h.a.refreshGit()
	v := h.a.ActiveView()
	for i := 0; i < 200 && !v.doc.hasGitBase; i++ {
		time.Sleep(10 * time.Millisecond)
		h.flush()
	}
	h.typeText("1")         // line 1 changes
	v.SetCursor(bpos(4, 0)) // line 5 changes too
	h.typeText("5")
	h.flush()
	if hs := v.hunks(); len(hs) != 2 {
		t.Fatalf("hunks: %+v", hs)
	}
	v.SetCursor(bpos(0, 0))
	h.a.RunCommand("git.stageSelectedRanges", nil)
	for i := 0; i < 200 && len(v.hunks()) != 1; i++ {
		time.Sleep(10 * time.Millisecond)
		h.flush()
	}
	if got := git("show", ":a.txt"); got != "1one\ntwo\nthree\nfour\nfive\n" {
		t.Fatalf("index: %q", got)
	}
	if hs := v.hunks(); len(hs) != 1 || hs[0].BStart != 4 {
		t.Fatalf("remaining hunks: %+v", hs)
	}
	// revert the other change in the editor
	v.SetCursor(bpos(4, 0))
	h.a.RunCommand("git.revertSelectedRanges", nil)
	if got := h.text(); got != "1one\ntwo\nthree\nfour\nfive\n" {
		t.Fatalf("revert: %q", got)
	}
}

func bpos(line, col int) buffer.Pos { return buffer.Pos{Line: line, Col: col} }
