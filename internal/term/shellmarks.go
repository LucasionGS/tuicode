package term

import (
	"strconv"
	"strings"
	"time"
)

// CommandMark is a shell command reported through shell integration (OSC 633, or FinalTerm's OSC 133).
type CommandMark struct {
	Command  string
	Cwd      string
	Executed bool // output started (C)
	Finished bool // the command ended (D)
	ExitCode int
	Started  time.Time
	Ended    time.Time
}

// MarkRef is a mark and the row of its prompt (negative rows are in the scrollback).
type MarkRef struct {
	Row  int
	Mark *CommandMark
}

// Example: "A", "E;git status;nonce", "D;1", "P;Cwd=/home/x"
func (e *Emulator) shellMark(arg string) {
	if arg == "" {
		return
	}
	kind, rest, _ := strings.Cut(arg, ";")
	switch kind {
	case "A":
		e.ShellIntegration = true
		m := &CommandMark{Cwd: e.Cwd}
		e.curMark = m
		if !e.altActive {
			if s := e.screen(); e.y < len(s) {
				s[e.y].Mark = m
			}
		}
	case "E":
		if e.curMark != nil {
			cmd, _, _ := strings.Cut(rest, ";")
			e.curMark.Command = unescapeMark(cmd)
		}
	case "C":
		if e.curMark != nil {
			e.curMark.Executed = true
			e.curMark.Started = time.Now()
			if !e.altActive {
				if s := e.screen(); e.y < len(s) {
					s[e.y].Output = e.curMark
				}
			}
			if e.curMark.Cwd == "" {
				e.curMark.Cwd = e.Cwd
			}
		}
	case "D":
		if m := e.curMark; m != nil && m.Executed && !m.Finished {
			m.Finished = true
			m.Ended = time.Now()
			code, _, _ := strings.Cut(rest, ";")
			m.ExitCode, _ = strconv.Atoi(code)
		}
	case "P":
		if v, ok := strings.CutPrefix(rest, "Cwd="); ok {
			e.Cwd = unescapeMark(v)
			if e.curMark != nil && !e.curMark.Executed {
				e.curMark.Cwd = e.Cwd
			}
		}
	}
}

// unescapeMark decodes VS Code's escaping in OSC 633 values: \\ and \xHH.
func unescapeMark(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			if s[i+1] == '\\' {
				sb.WriteByte('\\')
				i++
				continue
			}
			if s[i+1] == 'x' && i+3 < len(s) {
				if n, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
					sb.WriteByte(byte(n))
					i += 3
					continue
				}
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// Marks returns the command marks of the main screen and its scrollback, oldest first.
// The caller must hold the lock.
func (e *Emulator) Marks() []MarkRef {
	var out []MarkRef
	for i, l := range e.scrollback {
		if l.Mark != nil {
			out = append(out, MarkRef{Row: i - len(e.scrollback), Mark: l.Mark})
		}
	}
	if e.altActive {
		return out
	}
	for i, l := range e.main {
		if l.Mark != nil {
			out = append(out, MarkRef{Row: i, Mark: l.Mark})
		}
	}
	return out
}

// CommandOutput returns the output of the command whose prompt is on row, up to the next prompt.
// The caller must hold the lock.
func (e *Emulator) CommandOutput(row int) string {
	marks := e.Marks()
	end := len(e.main) - 1
	if !e.altActive {
		end = e.y
	}
	for i, m := range marks {
		if m.Row == row && i+1 < len(marks) {
			end = marks[i+1].Row - 1
		}
	}
	// output starts where the shell reported it, below a possibly multi-line prompt
	start := row + 1
	for y := row; y <= end; y++ {
		if l := e.LineAt(y); l.Output != nil && l.Output == e.LineAt(row).Mark {
			start = y
			break
		}
	}
	if start > end {
		return ""
	}
	return strings.TrimRight(e.Text(start, 0, end, e.cols-1), "\n ")
}
