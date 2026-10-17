package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"tuicode/internal/lsp"
)

// matcherPattern is one line of a problem matcher. The ints are capture group indexes, 0 = unused.
type matcherPattern struct {
	re                                          *regexp.Regexp
	file, line, column, endLine, endColumn, sev int
	message, code                               int
	loop                                        bool
	gap                                         int // lines allowed to be skipped before this pattern matches
}

// problemMatcher turns task output into diagnostics, like VS Code's `problemMatcher`.
type problemMatcher struct {
	owner    string
	source   string
	severity int
	patterns []matcherPattern
}

// taskProblem is one problem found in task output.
type taskProblem struct {
	path                           string
	line, col, endLine, endCol     int // 1-based, 0 = unknown
	severity                       int
	message, code, source, matcher string
}

func mp(re string, groups ...int) matcherPattern {
	p := matcherPattern{re: regexp.MustCompile(re)}
	fields := []*int{&p.file, &p.line, &p.column, &p.sev, &p.message, &p.code}
	for i, g := range groups {
		if i < len(fields) {
			*fields[i] = g
		}
	}
	return p
}

// builtinMatchers are the problem matchers VS Code and its common extensions ship.
var builtinMatchers = map[string]*problemMatcher{
	"go": {owner: "go", source: "go", patterns: []matcherPattern{
		mp(`^\s*(?:[^:\s]*: )?((?:[A-Za-z]:)?[^:\s][^:]*\.go):(\d+)(?::(\d+))?:\s*(.*)$`, 1, 2, 3, 0, 4),
	}},
	"tsc": {owner: "typescript", source: "ts", patterns: []matcherPattern{
		mp(`^([^\s].*?)[\(:](\d+)[,:](\d+)(?:\):\s+|\s+-\s+)(error|warning|info)\s+(TS\d+)\s*:\s*(.*)$`, 1, 2, 3, 4, 6, 5),
	}},
	"gcc": {owner: "cpp", source: "gcc", patterns: []matcherPattern{
		mp(`^(.*?):(\d+):(\d*):?\s+(?:fatal\s+)?(warning|error|note):\s+(.*)$`, 1, 2, 3, 4, 5),
	}},
	"rustc": {owner: "rustc", source: "rustc", patterns: []matcherPattern{
		mp(`^(warning|error)(?:\[(.*?)\])?: (.*)$`, 0, 0, 0, 1, 3, 2),
		mp(`^\s+--> (.*):(\d+):(\d+)$`, 1, 2, 3),
	}},
	"eslint-compact": {owner: "eslint", source: "eslint", patterns: []matcherPattern{
		mp(`^(.+):\sline\s(\d+),\scol\s(\d+),\s(Error|Warning|Info)\s-\s(.+)\s\((.+)\)$`, 1, 2, 3, 4, 5, 6),
	}},
	"eslint-stylish": {owner: "eslint", source: "eslint", patterns: []matcherPattern{
		mp(`^((?:[A-Za-z]:)?[/\\][^\s].*)$`, 1),
		func() matcherPattern {
			p := mp(`^\s+(\d+):(\d+)\s+(error|warning|info)\s+(.+?)(?:\s\s+(\S+))?$`, 0, 1, 2, 3, 4, 5)
			p.loop = true
			return p
		}(),
	}},
	"deno": {owner: "deno", source: "deno-ts", patterns: []matcherPattern{
		mp(`^(?:error: )?(TS\d+) \[(ERROR|WARN|WARNING)\]: (.*)$`, 0, 0, 0, 2, 3, 1),
		func() matcherPattern {
			p := mp(`^\s+at (?:file://)?(.+):(\d+):(\d+)$`, 1, 2, 3)
			p.gap = 4
			return p
		}(),
	}},
}

func init() {
	builtinMatchers["tsc-watch"] = builtinMatchers["tsc"]
	builtinMatchers["eslint"] = builtinMatchers["eslint-stylish"]
}

// parseProblemMatchers reads a tasks.json `problemMatcher` value: a name, a custom object or a list of them.
func parseProblemMatchers(raw json.RawMessage) []*problemMatcher {
	if len(raw) == 0 {
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	var out []*problemMatcher
	for _, item := range list {
		var name string
		if json.Unmarshal(item, &name) == nil {
			if m := builtinMatchers[strings.TrimPrefix(name, "$")]; m != nil {
				out = append(out, m)
			}
			continue
		}
		if m := parseCustomMatcher(item); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func parseCustomMatcher(raw json.RawMessage) *problemMatcher {
	var obj struct {
		Base     string          `json:"base"`
		Owner    string          `json:"owner"`
		Source   string          `json:"source"`
		Severity string          `json:"severity"`
		Pattern  json.RawMessage `json:"pattern"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	m := &problemMatcher{owner: obj.Owner, source: obj.Source, severity: severityFromString(obj.Severity)}
	if base := builtinMatchers[strings.TrimPrefix(obj.Base, "$")]; base != nil {
		m.patterns = base.patterns
		if m.owner == "" {
			m.owner = base.owner
		}
		if m.source == "" {
			m.source = base.source
		}
	}
	type rawPattern struct {
		Regexp    string `json:"regexp"`
		File      int    `json:"file"`
		Location  int    `json:"location"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		EndLine   int    `json:"endLine"`
		EndColumn int    `json:"endColumn"`
		Severity  int    `json:"severity"`
		Message   int    `json:"message"`
		Code      int    `json:"code"`
		Loop      bool   `json:"loop"`
	}
	var pats []rawPattern
	if json.Unmarshal(obj.Pattern, &pats) != nil {
		var one rawPattern
		if json.Unmarshal(obj.Pattern, &one) == nil && one.Regexp != "" {
			pats = []rawPattern{one}
		}
	}
	if len(pats) > 0 {
		m.patterns = nil
	}
	for _, rp := range pats {
		re, err := regexp.Compile(rp.Regexp)
		if err != nil {
			return nil
		}
		p := matcherPattern{re: re, file: rp.File, line: rp.Line, column: rp.Column, endLine: rp.EndLine, endColumn: rp.EndColumn,
			sev: rp.Severity, message: rp.Message, code: rp.Code, loop: rp.Loop}
		if len(pats) == 1 && p.file == 0 && p.message == 0 && rp.Location == 0 {
			// VS Code's defaults for a single line pattern
			p.file, p.line, p.column, p.sev, p.message = 1, 2, 3, 4, 5
		}
		if rp.Location > 0 {
			p.line = rp.Location
		}
		m.patterns = append(m.patterns, p)
	}
	if len(m.patterns) == 0 {
		return nil
	}
	if m.owner == "" {
		m.owner = "external"
	}
	return m
}

func severityFromString(s string) int {
	switch strings.ToLower(s) {
	case "error", "fatal":
		return lsp.SeverityError
	case "warning", "warn":
		return lsp.SeverityWarning
	case "info", "information", "note":
		return lsp.SeverityInformation
	case "hint":
		return lsp.SeverityHint
	}
	return 0
}

// matcherRun applies a matcher to output lines, keeping multi-line state.
type matcherRun struct {
	m       *problemMatcher
	idx     int // next pattern to match
	skipped int
	cur     taskProblem
	last    taskProblem // data collected before a looping pattern
}

func (r *matcherRun) apply(p matcherPattern, sub []string, into *taskProblem) {
	get := func(i int) string {
		if i > 0 && i < len(sub) {
			return sub[i]
		}
		return ""
	}
	atoi := func(i int) int {
		n, _ := strconv.Atoi(get(i))
		return n
	}
	if s := get(p.file); s != "" {
		into.path = s
	}
	if n := atoi(p.line); n > 0 {
		into.line = n
	}
	if n := atoi(p.column); n > 0 {
		into.col = n
	}
	if n := atoi(p.endLine); n > 0 {
		into.endLine = n
	}
	if n := atoi(p.endColumn); n > 0 {
		into.endCol = n
	}
	if s := get(p.sev); s != "" {
		into.severity = severityFromString(s)
	}
	if s := get(p.message); s != "" {
		into.message = s
	}
	if s := get(p.code); s != "" {
		into.code = s
	}
}

// feed matches one line and returns the problems it completes.
func (r *matcherRun) feed(line string) []taskProblem {
	pats := r.m.patterns
	if r.idx > 0 {
		p := pats[r.idx]
		if sub := p.re.FindStringSubmatch(line); sub != nil {
			r.skipped = 0
			if p.loop {
				prob := r.last
				r.apply(p, sub, &prob)
				return r.finish(prob)
			}
			r.apply(p, sub, &r.cur)
			r.idx++
			if r.idx == len(pats) {
				r.idx = 0
				return r.finish(r.cur)
			}
			if pats[r.idx].loop {
				r.last = r.cur
			}
			return nil
		}
		if p.gap > r.skipped && !pats[0].re.MatchString(line) {
			r.skipped++
			return nil
		}
		r.idx, r.skipped = 0, 0
	}
	sub := pats[0].re.FindStringSubmatch(line)
	if sub == nil {
		return nil
	}
	r.cur = taskProblem{}
	r.apply(pats[0], sub, &r.cur)
	if len(pats) == 1 {
		return r.finish(r.cur)
	}
	r.idx = 1
	if pats[1].loop {
		r.last = r.cur
	}
	return nil
}

func (r *matcherRun) finish(p taskProblem) []taskProblem {
	if p.path == "" || p.message == "" {
		return nil
	}
	if p.severity == 0 {
		p.severity = r.m.severity
	}
	if p.severity == 0 {
		p.severity = lsp.SeverityError
	}
	p.source = r.m.source
	p.matcher = r.m.owner
	return []taskProblem{p}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]`)

// stripANSI removes escape sequences and carriage-return overwrites from a line of output.
func stripANSI(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	s = strings.TrimRight(s, "\r")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// resolveProblemPath turns a matched file name into an absolute path, trying the task's folder first.
func resolveProblemPath(p, cwd, root string) string {
	p = strings.TrimPrefix(p, "file://")
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	for _, base := range []string{cwd, root} {
		if base == "" {
			continue
		}
		full := filepath.Join(base, p)
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	if cwd != "" {
		return filepath.Join(cwd, p)
	}
	return filepath.Join(root, p)
}

// toDiagnostic converts a problem to an LSP diagnostic with rune columns.
func (p taskProblem) toDiagnostic() lsp.Diagnostic {
	line := max(p.line-1, 0)
	r := lsp.Range{Start: lsp.Position{Line: line}, End: lsp.Position{Line: line, Character: 1 << 20}}
	if p.col > 0 {
		r.Start.Character = p.col - 1
		r.End = r.Start
	}
	if p.endLine > 0 {
		r.End.Line = p.endLine - 1
	}
	if p.endCol > 0 {
		r.End.Character = p.endCol - 1
	}
	d := lsp.Diagnostic{Range: r, Severity: p.severity, Message: p.message, Source: p.source}
	if p.code != "" {
		d.Code = p.code
	}
	return d
}
