package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

// Testing runs Go tests like VS Code's Testing view with the Go extension: tests are found in
// *_test.go files, run with `go test -json` and their results show in the gutter, a code lens,
// the Testing side bar view and as diagnostics on failing lines.

type testStatus int

const (
	testUnknown testStatus = iota
	testQueued
	testRunning
	testPassed
	testFailed
	testSkipped
)

type testCase struct {
	name    string
	file    string
	line    int
	dir     string // package directory
	status  testStatus
	elapsed float64
	output  []string
}

func (t *testCase) key() string { return t.dir + "\x00" + t.name }

// TestingView is the Testing side bar view.
type TestingView struct {
	app       *App
	tests     map[string]*testCase
	collapsed map[string]bool
	rows      []testRow
	selected  int
	scroll    int
	scanned   time.Time
	scanning  bool
	running   *exec.Cmd
	lastRun   []*testCase
	lastDebug bool
}

type testRow struct {
	dir  string // package row when test is nil
	test *testCase
}

func newTestingView(a *App) *TestingView {
	return &TestingView{app: a, tests: map[string]*testCase{}, collapsed: map[string]bool{}}
}

var goTestFuncRe = regexp.MustCompile(`^func (Test|Example|Fuzz)([A-Z0-9_]\w*)?\(`)

// goTestsIn finds the test functions in Go source lines.
func goTestsIn(lines []string) map[int]string {
	out := map[int]string{}
	for i, l := range lines {
		if !strings.HasPrefix(l, "func ") {
			continue
		}
		if m := goTestFuncRe.FindStringSubmatch(l); m != nil {
			name := strings.TrimPrefix(strings.SplitN(l, "(", 2)[0], "func ")
			out[i] = name
		}
	}
	return out
}

// testsInDoc returns the test functions of an open *_test.go document by line (cached per version).
func (d *Document) testsInDoc() map[int]string {
	if !strings.HasSuffix(d.Buf.Path, "_test.go") {
		return nil
	}
	if d.testsVersion == d.Buf.Version()+1 {
		return d.tests
	}
	d.tests = goTestsIn(d.Buf.Lines())
	d.testsVersion = d.Buf.Version() + 1
	return d.tests
}

// testAt returns the test case declared on a line of an open document.
func (a *App) testAt(d *Document, line int) *testCase {
	name, ok := d.testsInDoc()[line]
	if !ok {
		return nil
	}
	dir := filepath.Dir(d.Buf.Path)
	tv := a.testing
	t := tv.tests[dir+"\x00"+name]
	if t == nil {
		t = &testCase{name: name, dir: dir}
		tv.tests[t.key()] = t
	}
	t.file, t.line = d.Buf.Path, line
	return t
}

// scan discovers the tests of the workspace in the background.
func (tv *TestingView) scan() {
	a := tv.app
	if tv.scanning || time.Since(tv.scanned) < 5*time.Second {
		return
	}
	files := a.workspaceFiles(func() { tv.scanned = time.Time{}; tv.scan() })
	if a.fileIdx == nil || a.fileIdx.built.IsZero() {
		return // the callback scans once the file index is ready
	}
	tv.scanning = true
	var testFiles []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			testFiles = append(testFiles, f)
		}
	}
	go func() {
		found := map[string]testCase{}
		for _, f := range testFiles {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for line, name := range goTestsIn(strings.Split(string(data), "\n")) {
				t := testCase{name: name, file: f, line: line, dir: filepath.Dir(f)}
				found[t.key()] = t
			}
		}
		a.Post(func() {
			tv.scanning = false
			tv.scanned = time.Now()
			for k, t := range found {
				if old, ok := tv.tests[k]; ok {
					old.file, old.line = t.file, t.line
					continue
				}
				nt := t
				tv.tests[k] = &nt
			}
			scannedFiles := map[string]bool{}
			for _, f := range testFiles {
				scannedFiles[f] = true
			}
			for k, t := range tv.tests {
				if _, ok := found[k]; ok || t.status == testRunning || t.status == testQueued {
					continue
				}
				// drop tests whose file is gone or no longer declares them; open files keep theirs
				if d, open := a.docs[t.file]; open {
					if _, still := d.testsInDoc()[t.line]; still {
						continue
					}
				}
				if scannedFiles[t.file] || !fileExists(t.file) {
					delete(tv.tests, k)
				}
			}
			tv.rebuild()
		})
	}()
}

func (tv *TestingView) sortedDirs() map[string][]*testCase {
	byDir := map[string][]*testCase{}
	for _, t := range tv.tests {
		byDir[t.dir] = append(byDir[t.dir], t)
	}
	for _, list := range byDir {
		sort.Slice(list, func(i, j int) bool {
			if list[i].file != list[j].file {
				return list[i].file < list[j].file
			}
			return list[i].line < list[j].line
		})
	}
	return byDir
}

func (tv *TestingView) rebuild() {
	byDir := tv.sortedDirs()
	var dirs []string
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	tv.rows = tv.rows[:0]
	for _, d := range dirs {
		tv.rows = append(tv.rows, testRow{dir: d})
		if tv.collapsed[d] {
			continue
		}
		for _, t := range byDir[d] {
			tv.rows = append(tv.rows, testRow{dir: d, test: t})
		}
	}
	tv.selected = clampInt(tv.selected, 0, max(len(tv.rows)-1, 0))
}

// dirStatus summarizes the tests of a package: failed beats running beats passed.
func (tv *TestingView) dirStatus(dir string) testStatus {
	st := testUnknown
	for _, t := range tv.tests {
		if t.dir != dir {
			continue
		}
		switch {
		case t.status == testFailed:
			return testFailed
		case t.status == testRunning || t.status == testQueued:
			st = testRunning
		case t.status == testPassed && st == testUnknown:
			st = testPassed
		}
	}
	return st
}

// statusIcon returns the glyph and color key for a test status, like VS Code's testing icons.
func (a *App) statusIcon(s testStatus) (string, string) {
	switch s {
	case testPassed:
		return a.Icon(IcoPass), "testing.iconPassed"
	case testFailed:
		return a.Icon(IcoError), "testing.iconFailed"
	case testRunning, testQueued:
		return "⟳", "testing.iconQueued"
	case testSkipped:
		return "⊘", "testing.iconSkipped"
	}
	return "○", "testing.iconUnset"
}

//#region Running

// moduleRoot returns the folder with the go.mod that contains dir.
func moduleRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if fileExists(filepath.Join(d, "go.mod")) {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

// runTests runs test cases with `go test -json`, one package after the other.
func (a *App) runTests(cases []*testCase) {
	tv := a.testing
	if len(cases) == 0 {
		a.setStatusMessage("No tests found.")
		return
	}
	if tv.running != nil {
		a.setStatusMessage("Tests are already running.")
		return
	}
	tv.lastRun, tv.lastDebug = cases, false
	byDir := map[string][]*testCase{}
	var dirs []string
	for _, t := range cases {
		if _, ok := byDir[t.dir]; !ok {
			dirs = append(dirs, t.dir)
		}
		byDir[t.dir] = append(byDir[t.dir], t)
		t.status = testQueued
		t.output = nil
	}
	sort.Strings(dirs)
	if a.lsp != nil {
		a.lsp.ClearSource("test")
	}
	a.output.append("Test Results", fmt.Sprintf("Running %d test(s)...", len(cases)))
	tv.rebuild()
	var runNext func(i int)
	runNext = func(i int) {
		if i >= len(dirs) {
			tv.running = nil
			a.reportTestRun(cases)
			return
		}
		dir := dirs[i]
		var names []string
		for _, t := range byDir[dir] {
			names = append(names, regexp.QuoteMeta(t.name))
		}
		args := []string{"test", "-json", "-run", "^(" + strings.Join(names, "|") + ")$", "."}
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		out, err := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			a.notify(NotifyError, "Failed to run go test: "+err.Error())
			tv.running = nil
			return
		}
		tv.running = cmd
		go func() {
			sc := bufio.NewScanner(out)
			sc.Buffer(make([]byte, 64*1024), 4<<20)
			var pkgOutput []string
			for sc.Scan() {
				line := sc.Text()
				var ev struct {
					Action  string
					Test    string
					Output  string
					Elapsed float64
				}
				if json.Unmarshal([]byte(line), &ev) != nil {
					// build errors are printed before any JSON
					pkgOutput = append(pkgOutput, line)
					continue
				}
				a.Post(func() { a.testEvent(dir, ev.Action, ev.Test, ev.Output, ev.Elapsed) })
			}
			_ = cmd.Wait()
			a.Post(func() {
				for _, l := range pkgOutput {
					a.output.append("Test Results", l)
				}
				for _, t := range byDir[dir] {
					if t.status == testQueued || t.status == testRunning {
						// the package did not build, or the run was cancelled
						t.status = testFailed
						t.output = append(t.output, pkgOutput...)
					}
				}
				tv.rebuild()
				runNext(i + 1)
			})
		}()
	}
	runNext(0)
}

// testEvent applies one `go test -json` event.
func (a *App) testEvent(dir, action, name, output string, elapsed float64) {
	tv := a.testing
	if output != "" {
		a.output.append("Test Results", strings.TrimRight(output, "\n"))
	}
	if name == "" {
		return
	}
	// subtests report as Parent/sub; they count for their parent
	top, _, _ := strings.Cut(name, "/")
	t := tv.tests[dir+"\x00"+top]
	if t == nil {
		return
	}
	switch action {
	case "run":
		if name == top {
			t.status = testRunning
		}
	case "output":
		t.output = append(t.output, strings.TrimRight(output, "\n"))
	case "pass":
		if name == top {
			t.status, t.elapsed = testPassed, elapsed
		}
	case "fail":
		t.status, t.elapsed = testFailed, elapsed
	case "skip":
		if name == top {
			t.status = testSkipped
		}
	}
	if action == "fail" && name == top {
		a.testFailureDiagnostics(t)
	}
	tv.rebuild()
}

var goTestFailRe = regexp.MustCompile(`^\s+([\w.-]+\.go):(\d+): (.*)$`)

// testFailureDiagnostics turns "file_test.go:12: message" output lines into diagnostics.
func (a *App) testFailureDiagnostics(t *testCase) {
	if a.lsp == nil {
		return
	}
	byFile := map[string][]lsp.Diagnostic{}
	for i, l := range t.output {
		m := goTestFailRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		msg := m[3]
		// continuation lines are indented further
		for j := i + 1; j < len(t.output) && strings.HasPrefix(t.output[j], "        ") && !goTestFailRe.MatchString(t.output[j]); j++ {
			msg += "\n" + strings.TrimSpace(t.output[j])
		}
		line, _ := strconv.Atoi(m[2])
		path := filepath.Join(t.dir, m[1])
		byFile[path] = append(byFile[path], lsp.Diagnostic{
			Range:    lsp.Range{Start: lsp.Position{Line: line - 1}, End: lsp.Position{Line: line - 1, Character: 1 << 20}},
			Severity: lsp.SeverityError,
			Source:   "test",
			Message:  t.name + ": " + msg,
		})
	}
	for path, ds := range byFile {
		existing := []lsp.Diagnostic{}
		for _, fd := range a.lsp.Diagnostics(path) {
			if fd.Server == "test" {
				existing = fd.Diagnostics
			}
		}
		a.lsp.SetDiagnostics(path, "test", append(existing, ds...))
	}
}

// reportTestRun summarizes a finished run in the status bar.
func (a *App) reportTestRun(cases []*testCase) {
	passed, failed := 0, 0
	for _, t := range cases {
		switch t.status {
		case testPassed:
			passed++
		case testFailed:
			failed++
		}
	}
	msg := fmt.Sprintf("Tests: %d passed, %d failed", passed, failed)
	a.output.append("Test Results", msg)
	if failed > 0 {
		a.notify(NotifyError, msg, NotifyAction{Label: "Show Output", Run: func() {
			a.output.selectChannel("Test Results")
			a.showPanel("output")
		}})
	} else {
		a.setStatusMessage(msg)
	}
	a.problems.dirty = true
}

// debugTest starts Delve on one test, like "debug test" in VS Code.
func (a *App) debugTest(t *testCase) {
	a.testing.lastRun, a.testing.lastDebug = []*testCase{t}, true
	a.launch(map[string]any{
		"name":    "Debug " + t.name,
		"type":    "go",
		"request": "launch",
		"mode":    "test",
		"program": t.dir,
		"args":    []any{"-test.run", "^" + regexp.QuoteMeta(t.name) + "$"},
	}, false)
}

func (a *App) cancelTests() {
	if c := a.testing.running; c != nil && c.Process != nil {
		_ = c.Process.Kill()
	}
}

// testsInFile returns the tests of the active editor's file.
func (a *App) testsInFile(v *EditorView) []*testCase {
	var out []*testCase
	lines := make([]int, 0)
	for line := range v.doc.testsInDoc() {
		lines = append(lines, line)
	}
	sort.Ints(lines)
	for _, l := range lines {
		out = append(out, a.testAt(v.doc, l))
	}
	return out
}

// testAtCursor returns the test function that contains the cursor.
func (a *App) testAtCursor(v *EditorView) *testCase {
	cur := v.Cursor().Line
	best := -1
	for line := range v.doc.testsInDoc() {
		if line <= cur && line > best {
			best = line
		}
	}
	if best < 0 {
		return nil
	}
	return a.testAt(v.doc, best)
}

func (tv *TestingView) all() []*testCase {
	var out []*testCase
	for _, list := range tv.sortedDirs() {
		out = append(out, list...)
	}
	return out
}

func (a *App) registerTestingCommands() {
	// the view is created after the commands, so closures look it up when they run
	a.cmd("workbench.view.testing", "View", "Show Testing", func(any) {
		a.testing.scan()
		a.toggleSidebarView("testing")
	})
	a.alias("workbench.view.testing.focus", "workbench.view.testing")
	a.cmd("testing.runAll", "Test", "Run All Tests", func(any) {
		a.testing.scanned = time.Time{}
		a.testing.scan()
		a.runTests(a.testing.all())
	})
	a.cmd("testing.runAtCursor", "Test", "Run Test at Cursor", a.ed(func(v *EditorView) {
		if t := a.testAtCursor(v); t != nil {
			a.runTests([]*testCase{t})
		} else {
			a.setStatusMessage("No test found at the cursor.")
		}
	}))
	a.cmd("testing.runCurrentFile", "Test", "Run Tests in Current File", a.ed(func(v *EditorView) { a.runTests(a.testsInFile(v)) }))
	a.cmd("testing.debugAtCursor", "Test", "Debug Test at Cursor", a.ed(func(v *EditorView) {
		if t := a.testAtCursor(v); t != nil {
			a.debugTest(t)
		}
	}))
	a.cmd("testing.reRunLastRun", "Test", "Rerun Last Run", func(any) {
		if len(a.testing.lastRun) == 0 {
			a.setStatusMessage("There is no previous test run.")
		} else if a.testing.lastDebug {
			a.debugTest(a.testing.lastRun[0])
		} else {
			a.runTests(a.testing.lastRun)
		}
	})
	a.cmd("testing.reRunFailTests", "Test", "Rerun Failed Tests", func(any) {
		var failed []*testCase
		for _, t := range a.testing.all() {
			if t.status == testFailed {
				failed = append(failed, t)
			}
		}
		a.runTests(failed)
	})
	a.cmd("testing.cancelRun", "Test", "Cancel Test Run", func(any) { a.cancelTests() })
	a.cmd("testing.showMostRecentOutput", "Test", "Show Output", func(any) {
		a.output.selectChannel("Test Results")
		a.showPanel("output")
	})
}

//#endregion

//#region View

func (tv *TestingView) drawActions(r Rect) {
	a := tv.app
	th := a.theme
	acts := []struct {
		icon string
		cmd  string
		tip  string
	}{
		{a.Icon(IcoPlay), "testing.runAll", "Run Tests"},
		{a.Icon(IcoRefresh), "", "Refresh Tests"},
	}
	if tv.running != nil {
		acts[0] = struct {
			icon string
			cmd  string
			tip  string
		}{"■", "testing.cancelRun", "Cancel Test Run"}
	}
	x := r.X + r.W - len(acts)*2
	for _, ac := range acts {
		a.cv.Text(x, r.Y, x+1, ac.icon, styleFB(th.C("sideBarTitle.foreground"), th.C("sideBar.background")))
		cmd, tip := ac.cmd, ac.tip
		a.addHit(Rect{x, r.Y, 1, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				if cmd == "" {
					tv.scanned = time.Time{}
					tv.scan()
				} else {
					a.RunCommand(cmd, nil)
				}
			case MouseMove:
				a.showTooltipAt(ev.X, r.Y+1, tip)
			}
			return true
		})
		x += 2
	}
}

func (tv *TestingView) draw(r Rect) {
	a := tv.app
	th := a.theme
	bgc := th.C("sideBar.background")
	tv.scan()
	if len(tv.rows) == 0 {
		msg := "No tests have been found in this workspace yet."
		if tv.scanning {
			msg = "Looking for tests..."
		}
		a.cv.Text(r.X+2, r.Y, r.X+r.W, msg, styleFB(th.C("descriptionForeground"), bgc))
		return
	}
	focused := a.focus == FocusSidebar && a.sidebarView == "testing"
	if tv.selected < tv.scroll {
		tv.scroll = tv.selected
	}
	if tv.selected >= tv.scroll+r.H {
		tv.scroll = tv.selected - r.H + 1
	}
	tv.scroll = clampInt(tv.scroll, 0, max(len(tv.rows)-r.H, 0))
	for i := 0; i < r.H; i++ {
		idx := tv.scroll + i
		if idx >= len(tv.rows) {
			break
		}
		row := tv.rows[idx]
		y := r.Y + i
		rbg := bgc
		fg := th.C("sideBar.foreground")
		if idx == tv.selected && focused {
			rbg = th.C("list.activeSelectionBackground")
			fg = th.C("list.activeSelectionForeground")
		} else if idx == tv.selected {
			rbg = th.C("list.inactiveSelectionBackground")
		} else if a.mouseY == y && r.Contains(a.mouseX, a.mouseY) {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		st := styleFB(fg, rbg)
		a.cv.Fill(Rect{r.X, y, r.W, 1}, ' ', st)
		x := r.X + 1
		var status testStatus
		var label, detail string
		if row.test == nil {
			chev := a.Icon(IcoChevronDown)
			if tv.collapsed[row.dir] {
				chev = a.Icon(IcoChevronRight)
			}
			x = a.cv.Text(x, y, r.X+r.W, chev+" ", st)
			status = tv.dirStatus(row.dir)
			label = a.relPath(row.dir)
			if label == "" || label == "." {
				label = filepath.Base(row.dir)
			}
		} else {
			x += 2
			status = row.test.status
			label = row.test.name
			if row.test.elapsed > 0 {
				detail = fmt.Sprintf("%.2fs", row.test.elapsed)
			}
		}
		icon, key := a.statusIcon(status)
		x = a.cv.Text(x, y, r.X+r.W, icon+" ", st.Foreground(th.OverColor(key, rbg)))
		x = a.cv.Text(x, y, r.X+r.W-3, label, st)
		if detail != "" {
			a.cv.Text(x+1, y, r.X+r.W-3, detail, st.Foreground(th.OverColor("descriptionForeground", rbg)))
		}
		// run button on hover, like VS Code
		hover := a.mouseY == y && r.Contains(a.mouseX, a.mouseY)
		if hover {
			a.cv.Text(r.X+r.W-2, y, r.X+r.W, a.Icon(IcoPlay), st.Foreground(th.OverColor("testing.runAction", rbg)))
		}
		rw := row
		rowIdx := idx
		a.addHit(Rect{r.X, y, r.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				tv.selected = rowIdx
				a.setFocus(FocusSidebar)
				switch {
				case ev.Button == tcell.Button2:
					a.openContextMenu(ev.X, ev.Y, tv.rowMenu(rw))
				case ev.X >= r.X+r.W-2:
					a.runTests(tv.rowTests(rw))
				case rw.test == nil:
					tv.collapsed[rw.dir] = !tv.collapsed[rw.dir]
					tv.rebuild()
				default:
					tv.reveal(rw.test)
				}
			case MouseWheel:
				tv.scroll = clampInt(tv.scroll+ev.WheelDY*3, 0, max(len(tv.rows)-r.H, 0))
			}
			return true
		})
	}
}

func (tv *TestingView) rowTests(rw testRow) []*testCase {
	if rw.test != nil {
		return []*testCase{rw.test}
	}
	return tv.sortedDirs()[rw.dir]
}

func (tv *TestingView) rowMenu(rw testRow) []MenuItem {
	a := tv.app
	items := []MenuItem{{Label: "Run Test", Run: func() { a.runTests(tv.rowTests(rw)) }}}
	if rw.test != nil {
		t := rw.test
		items = append(items,
			MenuItem{Label: "Debug Test", Run: func() { a.debugTest(t) }},
			MenuItem{Separator: true},
			MenuItem{Label: "Go to Test", Run: func() { tv.reveal(t) }},
			MenuItem{Label: "Peek Output", Disabled: len(t.output) == 0, Run: func() { tv.showOutput(t) }},
		)
	}
	return items
}

func (tv *TestingView) reveal(t *testCase) {
	a := tv.app
	if v := a.OpenFile(t.file, false); v != nil {
		v.SetCursor(v.buf().Clamp(lsp.FromLSPPos(v.buf(), lsp.Position{Line: t.line}, "utf-32")))
		v.ensurePosVisible(v.Cursor(), true)
		a.focusEditor()
	}
}

// showOutput opens a test's output in a read-only editor.
func (tv *TestingView) showOutput(t *testCase) {
	tv.app.openVirtualDoc("Test Output: "+t.name, strings.Join(t.output, "\n"), "plaintext")
}

func (tv *TestingView) handleKey(key string) bool {
	if len(tv.rows) == 0 {
		return false
	}
	switch key {
	case "up":
		tv.selected = max(tv.selected-1, 0)
	case "down":
		tv.selected = min(tv.selected+1, len(tv.rows)-1)
	case "enter":
		rw := tv.rows[tv.selected]
		if rw.test != nil {
			tv.reveal(rw.test)
		} else {
			tv.collapsed[rw.dir] = !tv.collapsed[rw.dir]
			tv.rebuild()
		}
	case "ctrl+enter", "space":
		a := tv.app
		a.runTests(tv.rowTests(tv.rows[tv.selected]))
	case "escape":
		tv.app.focusEditor()
	default:
		return false
	}
	return true
}

//#endregion

// openVirtualDoc shows text in a read-only editor that is never saved.
func (a *App) openVirtualDoc(title, text, lang string) *EditorView {
	key := "virtual:" + title
	if d, ok := a.docs[key]; ok {
		d.Buf.ReadOnly = false
		d.Buf.ApplyEdits([]buffer.Edit{{Range: buffer.Range{End: d.Buf.EndPos()}, Text: text}}, nil, "")
		d.Buf.ReadOnly = true
	}
	d, ok := a.docs[key]
	if !ok {
		b := buffer.NewFromString(text)
		b.UntitledName = title
		b.LanguageID = lang
		b.ReadOnly = true
		d = a.newDocument(b)
		d.Virtual = true
		a.docs[key] = d
	}
	v := a.newEditorView(d)
	a.ActiveGroup().addTab(v, false)
	a.focusEditor()
	return v
}
