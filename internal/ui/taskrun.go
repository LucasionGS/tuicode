package ui

import (
	"fmt"
	"strings"

	"tuicode/internal/lsp"
)

// taskRun is a task running in a terminal, with its problem matchers.
type taskRun struct {
	label    string
	cwd      string
	runs     []*matcherRun
	partial  []byte                                 // output after the last newline, only touched by the reader goroutine until exit
	problems map[string]map[string][]lsp.Diagnostic // source -> path -> diagnostics
	done     bool
	onDone   func(exitCode int)
}

// runTask runs a task command in its own terminal, reusing the terminal of a finished run of the same task.
func (tp *TerminalPanel) runTask(t task, cmd, cwd string, onDone func(int)) {
	a := tp.app
	for _, old := range append([]*TermInstance{}, tp.terms...) {
		if old.task != nil && old.task.label == t.Label {
			if !old.task.done {
				a.notify(NotifyInfo, "The task '"+t.Label+"' is already active.")
				tp.activate(old.id)
				return
			}
			tp.remove(old)
			old.t.Close()
		}
	}
	run := &taskRun{label: t.Label, cwd: cwd, problems: map[string]map[string][]lsp.Diagnostic{}, onDone: onDone}
	for _, m := range t.Matchers {
		run.runs = append(run.runs, &matcherRun{m: m})
		if a.lsp != nil {
			a.lsp.ClearSource("task:" + m.owner)
		}
	}
	shell, _ := tp.shell()
	ti := tp.startProc(cwd, shell, []string{"-c", cmd}, func(ti *TermInstance) {
		ti.task = run
		ti.name = "Task - " + t.Label
		ti.custom = true
		ti.t.Emulator.Write([]byte("\x1b[1m * \x1b[0m Executing task: " + cmd + " \r\n\r\n"))
		if len(run.runs) > 0 {
			ti.t.OnData = func(data []byte) {
				if probs := run.scan(data, false); len(probs) > 0 {
					a.Post(func() { tp.addProblems(run, probs) })
				}
			}
		}
	})
	if ti == nil {
		return
	}
	tp.groups = append(tp.groups, &TermGroup{terms: []*TermInstance{ti}})
	tp.active = len(tp.groups) - 1
	tp.rebuildFlat()
}

// scan feeds complete output lines (all remaining output when final) to the matchers.
func (r *taskRun) scan(data []byte, final bool) []taskProblem {
	r.partial = append(r.partial, data...)
	var out []taskProblem
	for {
		i := strings.IndexByte(string(r.partial), '\n')
		if i < 0 {
			if !final || len(r.partial) == 0 {
				break
			}
			i = len(r.partial)
		}
		line := stripANSI(string(r.partial[:i]))
		if i < len(r.partial) {
			r.partial = r.partial[i+1:]
		} else {
			r.partial = nil
		}
		for _, mr := range r.runs {
			out = append(out, mr.feed(line)...)
		}
	}
	return out
}

// addProblems publishes matched problems as diagnostics.
func (tp *TerminalPanel) addProblems(run *taskRun, probs []taskProblem) {
	a := tp.app
	touched := map[[2]string]bool{}
	for _, p := range probs {
		source := "task:" + p.matcher
		path := resolveProblemPath(p.path, run.cwd, a.root)
		if run.problems[source] == nil {
			run.problems[source] = map[string][]lsp.Diagnostic{}
		}
		run.problems[source][path] = append(run.problems[source][path], p.toDiagnostic())
		touched[[2]string{source, path}] = true
	}
	if a.lsp == nil {
		return
	}
	for k := range touched {
		a.lsp.SetDiagnostics(k[1], k[0], run.problems[k[0]][k[1]])
	}
}

// finishTask reports the end of a task and keeps its output until a key is pressed.
func (tp *TerminalPanel) finishTask(ti *TermInstance) {
	run := ti.task
	run.done = true
	if probs := run.scan(nil, true); len(probs) > 0 {
		tp.addProblems(run, probs)
	}
	code := ti.t.ExitCode()
	defer func() {
		if run.onDone != nil {
			run.onDone(code)
		}
	}()
	msg := "\r\n"
	if code != 0 {
		msg += fmt.Sprintf("\x1b[1m * \x1b[0m The terminal process terminated with exit code: %d.\r\n", code)
	}
	msg += "\x1b[1m * \x1b[0m Terminal will be reused by tasks, press any key to close it.\r\n"
	ti.t.Emulator.Write([]byte(msg))
	count := 0
	for _, bySource := range run.problems {
		for _, ds := range bySource {
			count += len(ds)
		}
	}
	if count > 0 {
		tp.app.problems.dirty = true
		tp.app.setStatusMessage(fmt.Sprintf("Task '%s' found %d problem(s).", run.label, count))
	}
}
