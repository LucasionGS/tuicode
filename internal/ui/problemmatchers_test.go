package ui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func matchAll(m *problemMatcher, out string) []taskProblem {
	r := &matcherRun{m: m}
	var probs []taskProblem
	for _, l := range strings.Split(out, "\n") {
		probs = append(probs, r.feed(stripANSI(l))...)
	}
	return probs
}

func TestProblemMatchers(t *testing.T) {
	cases := []struct {
		matcher, output string
		want            []string // path:line:col:severity:message
	}{
		{"go", "# tuicode/internal/ui\ninternal/ui/app.go:12:3: undefined: foo\n./main.go:4:1: syntax error\nok  	tuicode	0.1s",
			[]string{"internal/ui/app.go:12:3:1:undefined: foo", "./main.go:4:1:1:syntax error"}},
		{"tsc", "src/a.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'.\nsrc/b.ts:1:1 - warning TS6133: 'x' is declared but its value is never read.",
			[]string{"src/a.ts:3:7:1:Type 'string' is not assignable to type 'number'.", "src/b.ts:1:1:2:'x' is declared but its value is never read."}},
		{"gcc", "main.c:5:10: \x1b[31merror:\x1b[0m expected ';' before '}' token\nmain.c:2:1: warning: unused variable 'x'",
			[]string{"main.c:5:10:1:expected ';' before '}' token", "main.c:2:1:2:unused variable 'x'"}},
		{"rustc", "error[E0308]: mismatched types\n --> src/main.rs:2:18\n  |\nwarning: unused variable: `x`\n --> src/lib.rs:7:9",
			[]string{"src/main.rs:2:18:1:mismatched types", "src/lib.rs:7:9:2:unused variable: `x`"}},
		{"eslint-stylish", "/home/u/p/a.js\n  1:10  error  'x' is defined but never used  no-unused-vars\n  4:3   warning  Unexpected console statement  no-console\n\n✖ 2 problems",
			[]string{"/home/u/p/a.js:1:10:1:'x' is defined but never used", "/home/u/p/a.js:4:3:2:Unexpected console statement"}},
		{"deno", "TS2322 [ERROR]: Type 'string' is not assignable to type 'number'.\nconst x: number = \"a\";\n      ^\n    at file:///tmp/p/main.ts:1:7",
			[]string{"/tmp/p/main.ts:1:7:1:Type 'string' is not assignable to type 'number'."}},
	}
	for _, c := range cases {
		probs := matchAll(builtinMatchers[c.matcher], c.output)
		var got []string
		for _, p := range probs {
			got = append(got, strings.Join([]string{p.path, itoaSimple(p.line), itoaSimple(p.col), itoaSimple(p.severity), p.message}, ":"))
		}
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("$%s:\ngot  %q\nwant %q", c.matcher, got, c.want)
		}
	}
	// custom matcher from tasks.json
	ms := parseProblemMatchers(json.RawMessage(`[{"owner": "lint", "pattern": {"regexp": "^(.*)\\|(\\d+)\\|(warn|err)\\|(.*)$", "file": 1, "line": 2, "severity": 3, "message": 4}}, "$go"]`))
	if len(ms) != 2 || ms[1] != builtinMatchers["go"] {
		t.Fatalf("parse: %d matchers", len(ms))
	}
	probs := matchAll(ms[0], "a.txt|3|warn|too long")
	if len(probs) != 1 || probs[0].path != "a.txt" || probs[0].line != 3 || probs[0].severity != 2 || probs[0].message != "too long" {
		t.Fatalf("custom: %+v", probs)
	}
}

func TestTaskProblems(t *testing.T) {
	dir := tempWorkspace(t, map[string]string{
		"main.go": "package main\n\nfunc main() {\n\tfoo()\n}\n",
		".vscode/tasks.json": `{"version": "2.0.0", "tasks": [{"label": "check", "type": "shell",
			"command": "printf 'main.go:4:2: undefined: foo\\n'; exit 1", "problemMatcher": "$go", "group": {"kind": "build", "isDefault": true}}]}`,
	})
	t.Setenv("SHELL", "/bin/sh")
	h := newHarness(t, 120, 40, Options{Paths: []string{filepath.Join(dir, "main.go")}})
	h.a.RunCommand("workbench.action.tasks.build", nil)
	var ti *TermInstance
	for i := 0; i < 200; i++ {
		h.flush()
		if cur := h.a.terminals.current(); cur != nil && cur.task != nil && cur.task.done {
			ti = cur
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ti == nil {
		h.dump()
		t.Fatal("task did not finish")
	}
	v := h.a.ActiveView()
	if len(v.doc.Diags) != 1 || v.doc.Diags[0].Range.Start.Line != 3 || v.doc.Diags[0].Message != "undefined: foo" {
		t.Fatalf("diagnostics: %+v", v.doc.Diags)
	}
	h.a.showPanel("terminal")
	h.flush()
	if !h.contains("Executing task") || !h.contains("press any key to close it") || !h.contains("exit code: 1") {
		h.dump()
		t.Fatal("task terminal messages missing")
	}
	// any key closes the finished task terminal
	h.typeText("x")
	if len(h.a.terminals.terms) != 0 {
		t.Fatalf("task terminal still open: %d", len(h.a.terminals.terms))
	}
}
