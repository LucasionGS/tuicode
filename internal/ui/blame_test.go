package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tuicode/internal/buffer"
	"tuicode/internal/term"
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

func TestShellIntegration(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	dir := tempWorkspace(t, map[string]string{"a.txt": "a\n"})
	t.Setenv("SHELL", bash)
	h := newHarness(t, 120, 30, Options{Paths: []string{dir}})
	h.a.showPanel("terminal")
	ti := h.a.terminals.current()
	waitFor := func(what string, cond func() bool) {
		for i := 0; i < 300 && !cond(); i++ {
			time.Sleep(10 * time.Millisecond)
			h.flush()
		}
		if !cond() {
			h.dump()
			t.Fatalf("timeout waiting for %s", what)
		}
	}
	marks := func() []term.MarkRef {
		ti.t.Lock()
		defer ti.t.Unlock()
		return ti.t.Marks()
	}
	waitFor("first prompt", func() bool { return len(marks()) == 1 })
	ti.t.WriteInput([]byte("echo hi; false\r"))
	waitFor("command end", func() bool { ms := marks(); return len(ms) == 2 && ms[0].Mark.Finished })
	m := marks()[0].Mark
	if m.Command != "echo hi; false" || m.ExitCode != 1 {
		t.Fatalf("mark: %+v", m)
	}
	row := marks()[0].Row
	ti.t.Lock()
	out := ti.t.CommandOutput(row)
	ti.t.Unlock()
	if out != "hi" {
		t.Fatalf("output: %q", out)
	}
	if cmds := h.a.recentTerminalCommands(); len(cmds) == 0 || cmds[0] != "echo hi; false" {
		t.Fatalf("recent: %v", cmds)
	}
	// long output keeps its command pinned at the top
	ti.t.WriteInput([]byte("seq 1 100\r"))
	waitFor("long output", func() bool { ms := marks(); return len(ms) == 3 && ms[1].Mark.Finished })
	h.a.terminals.scroll(-20)
	h.flush()
	if !h.contains("seq 1 100") {
		h.dump()
		t.Fatal("sticky command missing")
	}
	h.a.terminals.closeAll()
}

func TestDetectPorts(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(py, "-c", "import socket,time;s=socket.socket();s.bind(('127.0.0.1',0));s.listen();print(s.getsockname()[1],flush=True);time.sleep(20)")
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var port int
	if _, err := fmt.Fscan(out, &port); err != nil {
		t.Fatal(err)
	}
	ports := detectPorts([]int{cmd.Process.Pid})
	if len(ports) != 1 || ports[0].port != port || ports[0].process != "python3" {
		t.Fatalf("ports: %+v, want %d", ports, port)
	}
}
