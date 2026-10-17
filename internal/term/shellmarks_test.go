package term

import "testing"

func TestShellMarks(t *testing.T) {
	e := New(40, 5)
	e.MaxScrollback = 100
	write := func(s string) { _, _ = e.Write([]byte(s)) }
	write("\x1b]633;A\x07$ \x1b]633;B\x07ls\r\n\x1b]633;E;ls -la\\x3b echo hi\x07\x1b]633;C\x07a.txt\r\nb.txt\r\n\x1b]633;D;0\x07")
	write("\x1b]133;A\x07$ false\r\n\x1b]133;C\x07\x1b]133;D;1\x07")
	write("\x1b]633;A\x07$ ")
	marks := e.Marks()
	if len(marks) != 3 {
		t.Fatalf("marks: %d", len(marks))
	}
	m := marks[0].Mark
	if m.Command != "ls -la; echo hi" || !m.Finished || m.ExitCode != 0 {
		t.Fatalf("first: %+v", m)
	}
	if got := e.CommandOutput(marks[0].Row); got != "a.txt\nb.txt" {
		t.Fatalf("output: %q", got)
	}
	if m2 := marks[1].Mark; !m2.Finished || m2.ExitCode != 1 {
		t.Fatalf("second: %+v", m2)
	}
	if marks[2].Mark.Executed {
		t.Fatal("prompt without a command marked as executed")
	}
	// marks scroll into the scrollback with their lines
	for i := 0; i < 10; i++ {
		write("\r\nline")
	}
	marks = e.Marks()
	if len(marks) != 3 || marks[0].Row >= 0 || e.LineAt(marks[0].Row).Mark != marks[0].Mark {
		t.Fatalf("after scrolling: %+v", marks)
	}
}
