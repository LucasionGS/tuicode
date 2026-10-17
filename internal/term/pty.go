package term

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/creack/pty"
)

// Terminal runs a shell in a pseudo terminal and feeds its output into an Emulator.
type Terminal struct {
	*Emulator
	Name    string
	Shell   string
	cmd     *exec.Cmd
	pty     *os.File
	wmu     sync.Mutex
	exited  bool
	ExitErr error

	// OnOutput is called (from a background goroutine) after output was processed.
	OnOutput func()
	// OnData receives a copy of the raw output (from a background goroutine), before it is processed.
	OnData func([]byte)
	// OnExit is called when the process exits.
	OnExit func()
}

// Start launches shell (with args) in dir.
func Start(shell string, args []string, dir string, env []string, cols, rows int) (*Terminal, error) {
	t := Prepare(shell, args, dir, env, cols, rows)
	if err := t.Run(); err != nil {
		return nil, err
	}
	return t, nil
}

// Prepare creates a terminal without starting it, so callbacks can be set before any output is read.
func Prepare(shell string, args []string, dir string, env []string, cols, rows int) *Terminal {
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=tuicode")
	t := &Terminal{
		Emulator: New(cols, rows),
		Name:     filepath.Base(shell),
		Shell:    shell,
		cmd:      cmd,
	}
	t.Emulator.Reply = func(b []byte) { t.WriteInput(b) }
	return t
}

// AddEnv adds environment variables to a prepared terminal.
func (t *Terminal) AddEnv(kv ...string) {
	t.cmd.Env = append(t.cmd.Env, kv...)
}

// SetArgs replaces the arguments of a prepared terminal's program.
func (t *Terminal) SetArgs(args ...string) {
	t.cmd.Args = append([]string{t.cmd.Args[0]}, args...)
}

// Run starts a prepared terminal's process.
func (t *Terminal) Run() error {
	cols, rows := t.Emulator.Size()
	f, err := pty.StartWithSize(t.cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return err
	}
	t.pty = f
	go t.readLoop()
	return nil
}

func (t *Terminal) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			if t.OnData != nil {
				t.OnData(append([]byte(nil), buf[:n]...))
			}
			t.Emulator.Write(buf[:n])
			if t.OnOutput != nil {
				t.OnOutput()
			}
		}
		if err != nil {
			break
		}
	}
	t.ExitErr = t.cmd.Wait()
	t.exited = true
	if t.OnExit != nil {
		t.OnExit()
	}
}

// ExitCode returns the exit status of the process once it has ended (-1 when it was killed).
func (t *Terminal) ExitCode() int {
	if t.cmd.ProcessState == nil {
		return 0
	}
	return t.cmd.ProcessState.ExitCode()
}

// Exited reports whether the shell process has ended.
func (t *Terminal) Exited() bool { return t.exited }

// Pid returns the process id of the shell.
func (t *Terminal) Pid() int {
	if t.cmd.Process == nil {
		return 0
	}
	return t.cmd.Process.Pid
}

// WriteInput sends input bytes to the shell.
func (t *Terminal) WriteInput(b []byte) {
	if t.exited {
		return
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, _ = t.pty.Write(b)
}

// SetSize resizes the pty and the emulator.
func (t *Terminal) SetSize(cols, rows int) {
	c, r := t.Emulator.Size()
	if c == cols && r == rows {
		return
	}
	t.Emulator.Resize(cols, rows)
	_ = pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close kills the shell.
func (t *Terminal) Close() {
	if t.cmd.Process != nil && !t.exited {
		_ = t.cmd.Process.Kill()
	}
	_ = t.pty.Close()
}

// ForegroundProcessName returns the name of the process currently in the foreground of the pty.
func (t *Terminal) ForegroundProcessName() string {
	pgid, err := foregroundPgrp(t.pty)
	if err != nil || pgid <= 0 {
		return t.Name
	}
	data, err := os.ReadFile(filepath.Join("/proc", itoa(pgid), "comm"))
	if err != nil {
		return t.Name
	}
	name := string(data)
	for len(name) > 0 && (name[len(name)-1] == '\n' || name[len(name)-1] == ' ') {
		name = name[:len(name)-1]
	}
	return name
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
