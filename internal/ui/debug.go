package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/config"
	"tuicode/internal/dap"
)

// Breakpoint is a source breakpoint.
type Breakpoint struct {
	Line      int // 0-based
	Condition string
	LogMsg    string
	Verified  bool
	Message   string
	Disabled  bool
}

type dapThread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type dapFrame struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Source *struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"source"`
	PresentationHint string `json:"presentationHint"`
}

type dapScope struct {
	Name               string `json:"name"`
	VariablesReference int    `json:"variablesReference"`
	Expensive          bool   `json:"expensive"`
}

type dapVar struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type"`
	VariablesReference int    `json:"variablesReference"`
	EvaluateName       string `json:"evaluateName"`
}

type consoleLine struct {
	text     string
	category string
}

// DebugSession is one running debug adapter.
type DebugSession struct {
	app        *App
	c          *dap.Client
	name       string
	config     map[string]any
	typ        string
	caps       map[string]any
	state      string // starting, running, stopped, terminated
	threads    []dapThread
	threadID   int
	frames     []dapFrame
	frameIdx   int
	scopes     []dapScope
	vars       map[int][]dapVar
	expanded   map[int]bool
	stopReason string
	exitCode   *int
}

// DebugView is the Run and Debug side bar view.
type DebugView struct {
	app      *App
	selected int
	scroll   int
	rows     []debugRow
	rect     Rect
}

type debugRow struct {
	kind  string // header, var, frame, bp, watch
	label string
	depth int
	ref   int
	v     *dapVar
	frame int
	path  string
	line  int
}

func newDebugView(a *App) *DebugView { return &DebugView{app: a} }

//#region Breakpoints

func (a *App) toggleBreakpoint(path string, line int) {
	if path == "" {
		return
	}
	list := a.breakpoints[path]
	for i, bp := range list {
		if bp.Line == line {
			a.breakpoints[path] = append(list[:i], list[i+1:]...)
			a.syncBreakpoints(path)
			return
		}
	}
	a.breakpoints[path] = append(list, &Breakpoint{Line: line})
	sort.Slice(a.breakpoints[path], func(i, j int) bool { return a.breakpoints[path][i].Line < a.breakpoints[path][j].Line })
	a.syncBreakpoints(path)
}

func (a *App) breakpointAt(path string, line int) *Breakpoint {
	for _, bp := range a.breakpoints[path] {
		if bp.Line == line {
			return bp
		}
	}
	return nil
}

// shiftBreakpoints keeps breakpoints on their lines while editing.
func (a *App) shiftBreakpoints(d *Document, ch buffer.Change) {
	list := a.breakpoints[d.Buf.Path]
	if len(list) == 0 {
		return
	}
	delta := ch.NewEnd.Line - ch.Old.End.Line
	if delta == 0 {
		return
	}
	out := list[:0]
	for _, bp := range list {
		switch {
		case bp.Line > ch.Old.End.Line || bp.Line == ch.Old.End.Line && ch.Old.End.Col == 0 && ch.Old.Start.Line < ch.Old.End.Line:
			bp.Line += delta
		case bp.Line > ch.Old.Start.Line && bp.Line <= ch.Old.End.Line:
			// the line was removed
			if delta < 0 {
				continue
			}
		}
		out = append(out, bp)
	}
	a.breakpoints[d.Buf.Path] = out
}

func (a *App) syncBreakpoints(path string) {
	s := a.debug
	if s == nil || s.c == nil || s.state == "terminated" || s.state == "starting" && !s.configuredOnce() {
		return
	}
	s.setBreakpoints(path, nil)
}

//#endregion

//#region Launch configurations

func (a *App) launchConfigs() []map[string]any {
	if a.root == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(a.root, ".vscode", "launch.json"))
	if err != nil {
		return nil
	}
	var lf struct {
		Configurations []map[string]any `json:"configurations"`
	}
	if config.UnmarshalJSONC(data, &lf) != nil {
		return nil
	}
	return lf.Configurations
}

// defaultLaunchConfig creates a configuration for the active file like VS Code does without launch.json.
func (a *App) defaultLaunchConfig() map[string]any {
	v := a.ActiveView()
	if v == nil || v.buf().Path == "" {
		return nil
	}
	switch v.doc.Lang.ID {
	case "go":
		return map[string]any{"name": "Launch Package", "type": "go", "request": "launch", "mode": "auto", "program": "${fileDirname}"}
	case "python":
		return map[string]any{"name": "Python Debugger: Current File", "type": "debugpy", "request": "launch", "program": "${file}", "console": "integratedTerminal"}
	}
	return nil
}

func (a *App) substituteAny(v any) any {
	switch x := v.(type) {
	case string:
		return a.substituteVars(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = a.substituteAny(e)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = a.substituteAny(e)
		}
		return out
	}
	return v
}

// startDebugging launches a session (F5).
func (a *App) startDebugging(noDebug bool) {
	if a.debug != nil && a.debug.state != "terminated" {
		if a.debug.state == "stopped" {
			a.debug.cont()
		}
		return
	}
	configs := a.launchConfigs()
	if len(configs) == 0 {
		cfg := a.defaultLaunchConfig()
		if cfg == nil {
			a.runTaskPicker("run")
			return
		}
		a.launch(cfg, noDebug)
		return
	}
	if len(configs) == 1 || a.lastLaunch != "" {
		for _, c := range configs {
			if n, _ := c["name"].(string); n == a.lastLaunch || len(configs) == 1 {
				a.launch(c, noDebug)
				return
			}
		}
	}
	var items []QuickItem
	for _, c := range configs {
		n, _ := c["name"].(string)
		t, _ := c["type"].(string)
		items = append(items, QuickItem{Label: n, Description: t, Data: c, Icon: a.Icon(IcoPlay)})
	}
	a.openQuick(&QuickPick{Placeholder: "Select a launch configuration", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			a.launch(it.Data.(map[string]any), noDebug)
		}
	}})
}

func (a *App) adapterFor(typ string, cfg map[string]any) ([]string, bool, error) {
	look := func(names ...string) string {
		for _, n := range names {
			if a.lsp != nil {
				if p, ok := a.lsp.LookPath(n); ok {
					return p
				}
			}
			if p, err := exec.LookPath(n); err == nil {
				return p
			}
		}
		return ""
	}
	switch typ {
	case "go":
		dlv := look("dlv")
		if dlv == "" {
			return nil, false, fmt.Errorf("Delve is not installed. Install it with: go install github.com/go-delve/delve/cmd/dlv@latest")
		}
		return []string{dlv, "dap", "--client-addr={addr}"}, true, nil
	case "debugpy", "python":
		py := look("python3", "python")
		if py == "" {
			return nil, false, fmt.Errorf("Python is not installed.")
		}
		if err := exec.Command(py, "-c", "import debugpy").Run(); err != nil {
			return nil, false, fmt.Errorf("debugpy is not installed. Install it with: %s -m pip install debugpy", py)
		}
		return []string{py, "-m", "debugpy.adapter"}, false, nil
	case "lldb", "lldb-dap", "codelldb":
		l := look("lldb-dap", "lldb-vscode")
		if l == "" {
			return nil, false, fmt.Errorf("lldb-dap is not installed.")
		}
		return []string{l}, false, nil
	case "cppdbg", "gdb", "cppvsdbg":
		g := look("gdb")
		if g == "" {
			return nil, false, fmt.Errorf("gdb is not installed.")
		}
		return []string{g, "-i=dap", "-q"}, false, nil
	}
	return nil, false, fmt.Errorf("Debugging '%s' configurations is not supported. Supported types: go, debugpy, cppdbg (gdb), lldb.", typ)
}

// launch starts a debug session from a configuration.
func (a *App) launch(raw map[string]any, noDebug bool) {
	cfg := a.substituteAny(raw).(map[string]any)
	typ, _ := cfg["type"].(string)
	name, _ := cfg["name"].(string)
	a.lastLaunch, _ = raw["name"].(string)
	cmd, reverse, err := a.adapterFor(typ, cfg)
	if err != nil {
		a.notify(NotifyError, err.Error())
		return
	}
	if v, ok := cfg["preLaunchTask"].(string); ok && v != "" {
		for _, t := range a.collectTasks() {
			if t.Label == v {
				a.runTask(t)
			}
		}
	}
	if noDebug {
		cfg["noDebug"] = true
	}
	// adapter specific argument translation
	if typ == "cppdbg" || typ == "gdb" {
		if v, ok := cfg["stopAtEntry"].(bool); ok {
			cfg["stopAtBeginningOfMainSubprogram"] = v
		}
	}
	if typ == "go" {
		if _, ok := cfg["mode"]; !ok {
			cfg["mode"] = "debug"
		}
		if cfg["mode"] == "auto" {
			cfg["mode"] = "debug"
			if p, _ := cfg["program"].(string); strings.HasSuffix(p, "_test.go") {
				cfg["mode"] = "test"
			}
		}
	}
	cwd, _ := cfg["cwd"].(string)
	if cwd == "" {
		cwd = a.root
	}
	s := &DebugSession{app: a, name: name, config: cfg, typ: typ, state: "starting", vars: map[int][]dapVar{}, expanded: map[int]bool{}}
	a.debug = s
	a.debugConsole = nil
	a.consoleAppend("console", fmt.Sprintf("Starting %s...", name))
	log := func(line string) { a.output.append("Debug Adapter", line) }
	go func() {
		var c *dap.Client
		var err error
		if reverse {
			c, err = dap.StartReverse(cmd, cwd, nil, a.Post, log)
		} else {
			c, err = dap.StartStdio(cmd, cwd, nil, a.Post, log)
		}
		a.Post(func() {
			if err != nil {
				a.notify(NotifyError, "Failed to start the debug adapter: "+err.Error())
				s.state = "terminated"
				return
			}
			if a.debug != s {
				c.Close()
				return
			}
			s.c = c
			c.OnEvent = s.onEvent
			c.OnRequest = s.onRequest
			c.OnExit = func() { s.terminated() }
			s.initialize()
		})
	}()
	a.showPanel("debug")
	a.sidebarVisible = true
	a.sidebarView = "debug"
	a.focusEditor()
}

//#endregion

//#region Session

func (s *DebugSession) configuredOnce() bool { return s.caps != nil }

func (s *DebugSession) initialize() {
	a := s.app
	args := map[string]any{
		"clientID": "tuicode", "clientName": "tuicode", "adapterID": s.typ, "pathFormat": "path",
		"linesStartAt1": true, "columnsStartAt1": true, "supportsVariableType": true,
		"supportsRunInTerminalRequest": true, "locale": "en", "supportsProgressReporting": false,
	}
	s.c.Request("initialize", args, func(r dap.Response) {
		if err := r.Err(); err != nil {
			a.notify(NotifyError, "Debug adapter failed to initialize: "+err.Error())
			s.stop()
			return
		}
		s.caps = map[string]any{}
		_ = json.Unmarshal(r.Body, &s.caps)
		req, _ := s.config["request"].(string)
		if req != "attach" {
			req = "launch"
		}
		s.c.Request(req, s.config, func(r dap.Response) {
			if err := r.Err(); err != nil {
				a.notify(NotifyError, "Failed to "+req+": "+err.Error())
				a.consoleAppend("stderr", err.Error())
				s.stop()
			}
		})
	})
}

// setBreakpoints sends the breakpoints of one file.
func (s *DebugSession) setBreakpoints(path string, done func()) {
	a := s.app
	var bps []map[string]any
	for _, bp := range a.breakpoints[path] {
		if bp.Disabled {
			continue
		}
		m := map[string]any{"line": bp.Line + 1}
		if bp.Condition != "" {
			m["condition"] = bp.Condition
		}
		if bp.LogMsg != "" {
			m["logMessage"] = bp.LogMsg
		}
		bps = append(bps, m)
	}
	if bps == nil {
		bps = []map[string]any{}
	}
	s.c.Request("setBreakpoints", map[string]any{"source": map[string]any{"path": path, "name": filepath.Base(path)}, "breakpoints": bps}, func(r dap.Response) {
		var body struct {
			Breakpoints []struct {
				Verified bool   `json:"verified"`
				Line     int    `json:"line"`
				Message  string `json:"message"`
			} `json:"breakpoints"`
		}
		_ = json.Unmarshal(r.Body, &body)
		active := []*Breakpoint{}
		for _, bp := range a.breakpoints[path] {
			if !bp.Disabled {
				active = append(active, bp)
			}
		}
		for i, b := range body.Breakpoints {
			if i < len(active) {
				active[i].Verified = b.Verified
				active[i].Message = b.Message
			}
		}
		if done != nil {
			done()
		}
	})
}

func (s *DebugSession) configurationDone() {
	a := s.app
	paths := []string{}
	for p, list := range a.breakpoints {
		if len(list) > 0 {
			paths = append(paths, p)
		}
	}
	remaining := len(paths)
	finish := func() {
		s.c.Request("setExceptionBreakpoints", map[string]any{"filters": []string{}}, func(dap.Response) {
			if s.caps["supportsConfigurationDoneRequest"] == true {
				s.c.Request("configurationDone", nil, nil)
			}
			if s.state == "starting" {
				s.state = "running"
			}
		})
	}
	if remaining == 0 {
		finish()
		return
	}
	for _, p := range paths {
		s.setBreakpoints(p, func() {
			remaining--
			if remaining == 0 {
				finish()
			}
		})
	}
}

func (s *DebugSession) onEvent(ev dap.Event) {
	a := s.app
	switch ev.Event {
	case "initialized":
		s.configurationDone()
	case "stopped":
		var b struct {
			Reason            string `json:"reason"`
			Description       string `json:"description"`
			ThreadID          int    `json:"threadId"`
			AllThreadsStopped bool   `json:"allThreadsStopped"`
			Text              string `json:"text"`
		}
		_ = json.Unmarshal(ev.Body, &b)
		s.state = "stopped"
		s.stopReason = b.Reason
		if b.ThreadID != 0 {
			s.threadID = b.ThreadID
		}
		if b.Reason == "exception" && b.Text != "" {
			a.consoleAppend("stderr", "Exception: "+b.Text)
		}
		s.refreshThreads()
	case "continued":
		s.state = "running"
		s.frames = nil
		s.scopes = nil
	case "exited":
		var b struct {
			ExitCode int `json:"exitCode"`
		}
		_ = json.Unmarshal(ev.Body, &b)
		s.exitCode = &b.ExitCode
		a.consoleAppend("console", fmt.Sprintf("Process exited with code %d.", b.ExitCode))
	case "terminated":
		s.stop()
	case "output":
		var b struct {
			Category string `json:"category"`
			Output   string `json:"output"`
		}
		_ = json.Unmarshal(ev.Body, &b)
		if b.Category == "telemetry" {
			return
		}
		a.consoleAppend(b.Category, b.Output)
	case "breakpoint":
		// verification changes are picked up on the next setBreakpoints
	}
}

func (s *DebugSession) onRequest(command string, args json.RawMessage) (any, error) {
	a := s.app
	switch command {
	case "runInTerminal":
		var p struct {
			Kind  string            `json:"kind"`
			Title string            `json:"title"`
			Cwd   string            `json:"cwd"`
			Args  []string          `json:"args"`
			Env   map[string]string `json:"env"`
		}
		if err := json.Unmarshal(args, &p); err != nil || len(p.Args) == 0 {
			return nil, fmt.Errorf("invalid runInTerminal request")
		}
		pid, err := a.terminals.startCommand(p.Title, p.Args, p.Cwd, p.Env)
		if err != nil {
			return nil, err
		}
		return map[string]any{"processId": pid}, nil
	case "startDebugging":
		return nil, fmt.Errorf("child debug sessions are not supported")
	}
	return nil, fmt.Errorf("unsupported request %s", command)
}

func (s *DebugSession) refreshThreads() {
	s.c.Request("threads", nil, func(r dap.Response) {
		var b struct {
			Threads []dapThread `json:"threads"`
		}
		_ = json.Unmarshal(r.Body, &b)
		s.threads = b.Threads
		if s.threadID == 0 && len(s.threads) > 0 {
			s.threadID = s.threads[0].ID
		}
		s.refreshStack()
	})
}

func (s *DebugSession) refreshStack() {
	a := s.app
	if s.threadID == 0 {
		return
	}
	s.c.Request("stackTrace", map[string]any{"threadId": s.threadID, "startFrame": 0, "levels": 50}, func(r dap.Response) {
		var b struct {
			StackFrames []dapFrame `json:"stackFrames"`
		}
		_ = json.Unmarshal(r.Body, &b)
		s.frames = b.StackFrames
		s.frameIdx = 0
		// skip frames without source (runtime internals)
		for i, f := range s.frames {
			if f.Source != nil && f.Source.Path != "" {
				s.frameIdx = i
				break
			}
		}
		s.selectFrame(s.frameIdx)
		a.refreshWatches()
		a.debugView.rebuild()
	})
}

func (s *DebugSession) selectFrame(i int) {
	a := s.app
	if i < 0 || i >= len(s.frames) {
		return
	}
	s.frameIdx = i
	f := s.frames[i]
	if f.Source != nil && f.Source.Path != "" {
		if v := a.OpenFile(f.Source.Path, false); v != nil {
			p := v.buf().Clamp(buffer.Pos{Line: f.Line - 1, Col: max(f.Column-1, 0)})
			v.SetCursor(p)
			v.ensurePosVisible(p, true)
		}
	}
	s.vars = map[int][]dapVar{}
	s.c.Request("scopes", map[string]any{"frameId": f.ID}, func(r dap.Response) {
		var b struct {
			Scopes []dapScope `json:"scopes"`
		}
		_ = json.Unmarshal(r.Body, &b)
		s.scopes = b.Scopes
		for i, sc := range s.scopes {
			if i == 0 && !sc.Expensive {
				s.expanded[sc.VariablesReference] = true
			}
			if s.expanded[sc.VariablesReference] {
				s.loadVars(sc.VariablesReference)
			}
		}
		a.debugView.rebuild()
	})
}

func (s *DebugSession) loadVars(ref int) {
	if ref == 0 {
		return
	}
	s.c.Request("variables", map[string]any{"variablesReference": ref}, func(r dap.Response) {
		var b struct {
			Variables []dapVar `json:"variables"`
		}
		_ = json.Unmarshal(r.Body, &b)
		s.vars[ref] = b.Variables
		s.app.debugView.rebuild()
	})
}

func (s *DebugSession) currentFrame() *dapFrame {
	if s.state != "stopped" || s.frameIdx >= len(s.frames) {
		return nil
	}
	return &s.frames[s.frameIdx]
}

func (s *DebugSession) step(command string) {
	if s.c == nil || s.state != "stopped" {
		return
	}
	s.state = "running"
	s.c.Request(command, map[string]any{"threadId": s.threadID}, func(r dap.Response) {
		if err := r.Err(); err != nil {
			s.app.consoleAppend("stderr", err.Error())
		}
	})
}

func (s *DebugSession) cont() { s.step("continue") }

func (s *DebugSession) pause() {
	if s.c == nil || s.state != "running" {
		return
	}
	s.c.Request("pause", map[string]any{"threadId": max(s.threadID, 1)}, nil)
}

func (s *DebugSession) stop() {
	if s.state == "terminated" {
		return
	}
	if s.c != nil && !s.c.Closed() {
		c := s.c
		c.Request("disconnect", map[string]any{"terminateDebuggee": true}, func(dap.Response) { c.Close() })
		afterFunc(1500, func() { c.Close() })
	}
	s.terminated()
}

func (s *DebugSession) terminated() {
	if s.state == "terminated" {
		return
	}
	s.state = "terminated"
	s.frames = nil
	s.scopes = nil
	s.app.consoleAppend("console", "Debug session ended.")
	s.app.debugView.rebuild()
}

// evaluate runs an expression in the selected frame.
func (s *DebugSession) evaluate(expr, context string, cb func(result string, err error)) {
	if s.c == nil {
		cb("", fmt.Errorf("not debugging"))
		return
	}
	args := map[string]any{"expression": expr, "context": context}
	if f := s.currentFrame(); f != nil {
		args["frameId"] = f.ID
	}
	s.c.Request("evaluate", args, func(r dap.Response) {
		if err := r.Err(); err != nil {
			cb("", err)
			return
		}
		var b struct {
			Result string `json:"result"`
			Type   string `json:"type"`
		}
		_ = json.Unmarshal(r.Body, &b)
		cb(b.Result, nil)
	})
}

//#endregion

//#region Debug console

func (a *App) consoleAppend(category, text string) {
	text = strings.TrimRight(text, "\n")
	for _, l := range strings.Split(text, "\n") {
		a.debugConsole = append(a.debugConsole, consoleLine{text: strings.TrimRight(l, "\r"), category: category})
	}
	if len(a.debugConsole) > 5000 {
		a.debugConsole = a.debugConsole[len(a.debugConsole)-5000:]
	}
	a.consoleScroll = -1
}

func (a *App) drawDebugConsole(r Rect) {
	th := a.theme
	bgc := th.C("panel.background")
	h := r.H - 1
	lines := a.debugConsole
	start := max(len(lines)-h, 0)
	if a.consoleScroll >= 0 {
		start = clampInt(a.consoleScroll, 0, max(len(lines)-h, 0))
	}
	for i := 0; i < h && start+i < len(lines); i++ {
		l := lines[start+i]
		st := styleFB(th.C("foreground"), bgc)
		switch l.category {
		case "stderr":
			st = st.Foreground(th.C("errorForeground"))
		case "console":
			st = st.Foreground(th.C("descriptionForeground"))
		case "input":
			st = st.Foreground(th.C("textLink.foreground"))
		case "result":
			st = st.Foreground(th.C("debugTokenExpression.string"))
			if !th.Has("debugTokenExpression.string") {
				st = st.Foreground(th.Token(2).FG.Tcell())
			}
		}
		a.cv.TextEllipsis(r.X+1, r.Y+i, r.W-2, l.text, st)
	}
	a.addHit(Rect{r.X, r.Y, r.W, h}, func(ev MouseEvent) bool {
		switch ev.Kind {
		case MouseWheel:
			if a.consoleScroll < 0 {
				a.consoleScroll = start
			}
			a.consoleScroll = clampInt(a.consoleScroll+ev.WheelDY*3, 0, max(len(lines)-h, 0))
		case MousePress:
			a.setFocus(FocusPanel)
		}
		return true
	})
	// REPL input
	iy := r.Y + r.H - 1
	if a.consoleInput.app == nil {
		a.consoleInput = newTextInput(a, "Evaluate expression (when stopped)")
	}
	ist := styleFB(th.C("input.foreground"), bgc)
	a.cv.Text(r.X+1, iy, r.X+3, a.Icon(IcoChevronRight), styleFB(th.C("textLink.foreground"), bgc))
	a.consoleInput.Draw(r.X+3, iy, r.W-4, ist, styleFB(th.C("input.placeholderForeground"), bgc), a.focus == FocusPanel && a.panelView == "debug")
	a.addHit(Rect{r.X, iy, r.W, 1}, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			a.setFocus(FocusPanel)
		}
		return false
	})
}

func (a *App) consoleHandleKey(key string) bool {
	switch key {
	case "enter":
		expr := strings.TrimSpace(a.consoleInput.Text())
		if expr == "" {
			return true
		}
		a.consoleInput.AddHistory(expr)
		a.consoleInput.SetText("")
		a.consoleAppend("input", "> "+expr)
		if a.debug == nil || a.debug.state == "terminated" {
			a.consoleAppend("stderr", "No active debug session.")
			return true
		}
		a.debug.evaluate(expr, "repl", func(res string, err error) {
			if err != nil {
				a.consoleAppend("stderr", err.Error())
				return
			}
			a.consoleAppend("result", res)
		})
		return true
	case "up":
		a.consoleInput.HistoryMove(-1)
		return true
	case "down":
		a.consoleInput.HistoryMove(1)
		return true
	case "escape":
		a.focusEditor()
		return true
	}
	return false
}

//#endregion

//#region Run and Debug view

func (d *DebugView) rebuild() {
	a := d.app
	d.rows = d.rows[:0]
	s := a.debug
	if s != nil && s.state != "terminated" {
		d.rows = append(d.rows, debugRow{kind: "header", label: "VARIABLES"})
		if s.state == "stopped" {
			for _, sc := range s.scopes {
				d.rows = append(d.rows, debugRow{kind: "scope", label: sc.Name, ref: sc.VariablesReference, depth: 0})
				if s.expanded[sc.VariablesReference] {
					d.addVars(s, sc.VariablesReference, 1)
				}
			}
		}
		d.rows = append(d.rows, debugRow{kind: "header", label: "WATCH"})
		for _, w := range a.watches {
			d.rows = append(d.rows, debugRow{kind: "watch", label: w})
		}
		d.rows = append(d.rows, debugRow{kind: "header", label: "CALL STACK"})
		if s.state == "stopped" {
			for i, f := range s.frames {
				loc := ""
				if f.Source != nil {
					loc = fmt.Sprintf("%s:%d", filepath.Base(f.Source.Path), f.Line)
				}
				d.rows = append(d.rows, debugRow{kind: "frame", label: f.Name, frame: i, path: loc})
			}
		} else {
			d.rows = append(d.rows, debugRow{kind: "info", label: "Running"})
		}
	}
	d.rows = append(d.rows, debugRow{kind: "header", label: "BREAKPOINTS"})
	var paths []string
	for p := range a.breakpoints {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		for _, bp := range a.breakpoints[p] {
			d.rows = append(d.rows, debugRow{kind: "bp", label: filepath.Base(p), path: p, line: bp.Line})
		}
	}
}

func (d *DebugView) addVars(s *DebugSession, ref, depth int) {
	vars, ok := s.vars[ref]
	if !ok {
		d.rows = append(d.rows, debugRow{kind: "info", label: "Loading...", depth: depth})
		return
	}
	for i := range vars {
		v := &vars[i]
		d.rows = append(d.rows, debugRow{kind: "var", label: v.Name, v: v, ref: v.VariablesReference, depth: depth})
		if v.VariablesReference > 0 && s.expanded[v.VariablesReference] && depth < 8 {
			d.addVars(s, v.VariablesReference, depth+1)
		}
	}
}

func (d *DebugView) activate(i int) {
	a := d.app
	if i < 0 || i >= len(d.rows) {
		return
	}
	r := d.rows[i]
	s := a.debug
	switch r.kind {
	case "scope", "var":
		if s == nil || r.ref == 0 {
			return
		}
		s.expanded[r.ref] = !s.expanded[r.ref]
		if s.expanded[r.ref] {
			if _, ok := s.vars[r.ref]; !ok {
				s.loadVars(r.ref)
			}
		}
		d.rebuild()
	case "frame":
		if s != nil {
			s.selectFrame(r.frame)
		}
	case "bp":
		if v := a.OpenFile(r.path, false); v != nil {
			v.SetCursor(buffer.Pos{Line: r.line})
			v.ensurePosVisible(v.Cursor(), true)
		}
	}
}

func (d *DebugView) handleKey(key, text string, isText bool) bool {
	switch key {
	case "up":
		d.selected = max(d.selected-1, 0)
	case "down":
		d.selected = max(min(d.selected+1, len(d.rows)-1), 0)
	case "enter", "space", "right", "left":
		d.activate(d.selected)
	case "delete":
		if d.selected < len(d.rows) && d.rows[d.selected].kind == "bp" {
			r := d.rows[d.selected]
			d.app.toggleBreakpoint(r.path, r.line)
			d.rebuild()
		} else if d.selected < len(d.rows) && d.rows[d.selected].kind == "watch" {
			d.app.removeWatch(d.rows[d.selected].label)
		}
	case "escape":
		d.app.focusEditor()
	default:
		return false
	}
	return true
}

func (d *DebugView) draw(r Rect) {
	a := d.app
	th := a.theme
	d.rect = r
	bgc := th.C("sideBar.background")
	fg := th.C("sideBar.foreground")
	d.rebuild()
	y := r.Y
	if a.debug == nil || a.debug.state == "terminated" {
		bst := styleFB(th.C("button.foreground"), th.C("button.background"))
		bw := min(r.W-4, 30)
		a.cv.Fill(Rect{r.X + 2, y + 1, bw, 1}, ' ', bst)
		a.cv.TextCenter(r.X+2, y+1, bw, "Run and Debug", bst)
		a.addHit(Rect{r.X + 2, y + 1, bw, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.startDebugging(false)
			}
			return true
		})
		lines := []string{"To customize Run and Debug, create a launch.json file in .vscode/.", "Supported: Go (delve), Python (debugpy), C/C++ (gdb, lldb-dap)."}
		ly := y + 3
		for _, l := range lines {
			for _, w := range wrapText(l, r.W-4) {
				a.cv.Text(r.X+2, ly, r.X+r.W, w, styleFB(th.C("descriptionForeground"), bgc))
				ly++
			}
		}
		link := "create a launch.json file"
		a.cv.Text(r.X+2, ly, r.X+r.W, link, styleFB(th.C("textLink.foreground"), bgc))
		a.addHit(Rect{r.X + 2, ly, StrWidth(link), 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.createLaunchJSON()
			}
			return true
		})
		y = ly + 2
	}
	list := Rect{r.X, y, r.W, r.Y + r.H - y}
	focused := a.focus == FocusSidebar && a.sidebarView == "debug"
	d.selected = clampInt(d.selected, 0, max(len(d.rows)-1, 0))
	if d.selected < d.scroll {
		d.scroll = d.selected
	}
	if d.selected >= d.scroll+list.H {
		d.scroll = d.selected - list.H + 1
	}
	for i := 0; i < list.H; i++ {
		idx := d.scroll + i
		if idx >= len(d.rows) {
			break
		}
		row := d.rows[idx]
		ry := list.Y + i
		rbg := bgc
		rfg := fg
		if idx == d.selected && focused && row.kind != "header" {
			rbg = th.C("list.activeSelectionBackground")
			rfg = th.C("list.activeSelectionForeground")
		}
		st := styleFB(rfg, rbg)
		a.cv.Fill(Rect{list.X, ry, list.W, 1}, ' ', st)
		x := list.X + 1 + row.depth*2
		switch row.kind {
		case "header":
			hst := styleFB(th.C("sideBarSectionHeader.foreground"), th.OverColor("sideBarSectionHeader.background", bgc)).Bold(true)
			a.cv.Fill(Rect{list.X, ry, list.W, 1}, ' ', hst)
			a.cv.Text(list.X+1, ry, list.X+list.W, a.Icon(IcoChevronDown)+" "+row.label, hst)
		case "scope":
			chev := a.Icon(IcoChevronRight)
			if a.debug != nil && a.debug.expanded[row.ref] {
				chev = a.Icon(IcoChevronDown)
			}
			a.cv.Text(x, ry, list.X+list.W, chev+" "+row.label, st)
		case "var":
			v := row.v
			if v.VariablesReference > 0 {
				chev := a.Icon(IcoChevronRight)
				if a.debug != nil && a.debug.expanded[v.VariablesReference] {
					chev = a.Icon(IcoChevronDown)
				}
				x = a.cv.Text(x, ry, list.X+list.W, chev+" ", st)
			} else {
				x += 2
			}
			nameSt := st.Foreground(th.Or("debugTokenExpression.name", "symbolIcon.variableForeground"))
			x = a.cv.Text(x, ry, list.X+list.W, v.Name, nameSt)
			x = a.cv.Text(x, ry, list.X+list.W, ": ", st)
			a.cv.TextEllipsis(x, ry, list.X+list.W-x, strings.ReplaceAll(v.Value, "\n", " "), st.Foreground(th.Or("debugTokenExpression.value", "foreground")))
		case "watch":
			val := a.watchValues[row.label]
			x = a.cv.Text(x+2, ry, list.X+list.W, row.label, st.Foreground(th.Or("debugTokenExpression.name", "symbolIcon.variableForeground")))
			a.cv.TextEllipsis(x, ry, list.X+list.W-x, ": "+val, st)
		case "frame":
			fst := st
			if a.debug != nil && row.frame == a.debug.frameIdx {
				fst = st.Bold(true)
			}
			x = a.cv.Text(x+1, ry, list.X+list.W, row.label, fst)
			a.cv.TextRight(x+1, ry, list.X+list.W-x-2, row.path, st.Foreground(th.OverColor("descriptionForeground", rbg)))
		case "bp":
			bp := a.breakpointAt(row.path, row.line)
			col := th.Or("debugIcon.breakpointForeground", "editorError.foreground")
			if bp != nil && !bp.Verified && a.debug != nil && a.debug.state != "terminated" {
				col = th.C("descriptionForeground")
			}
			x = a.cv.Text(x+1, ry, list.X+list.W, "● ", st.Foreground(col))
			x = a.cv.Text(x, ry, list.X+list.W, row.label, st)
			a.cv.Text(x+1, ry, list.X+list.W, fmt.Sprint(row.line+1), st.Foreground(th.OverColor("descriptionForeground", rbg)))
		case "info":
			a.cv.Text(x+2, ry, list.X+list.W, row.label, st.Foreground(th.C("descriptionForeground")))
		}
		rowIdx := idx
		a.addHit(Rect{list.X, ry, list.W, 1}, func(ev MouseEvent) bool {
			if ev.Kind == MousePress {
				a.setFocus(FocusSidebar)
				d.selected = rowIdx
				if ev.Button == tcell.Button2 && d.rows[rowIdx].kind == "var" {
					v := d.rows[rowIdx].v
					a.openContextMenu(ev.X, ev.Y, []MenuItem{
						{Label: "Copy Value", Run: func() { a.setClipboard(v.Value) }},
						{Label: "Add to Watch", Run: func() {
							name := v.EvaluateName
							if name == "" {
								name = v.Name
							}
							a.addWatch(name)
						}},
					})
					return true
				}
				d.activate(rowIdx)
			}
			if ev.Kind == MouseWheel {
				d.scroll = clampInt(d.scroll+ev.WheelDY*3, 0, max(len(d.rows)-list.H, 0))
			}
			return true
		})
	}
}

func (a *App) addWatch(expr string) {
	for _, w := range a.watches {
		if w == expr {
			return
		}
	}
	a.watches = append(a.watches, expr)
	a.refreshWatches()
}

func (a *App) removeWatch(expr string) {
	for i, w := range a.watches {
		if w == expr {
			a.watches = append(a.watches[:i], a.watches[i+1:]...)
			return
		}
	}
}

func (a *App) refreshWatches() {
	if a.watchValues == nil {
		a.watchValues = map[string]string{}
	}
	s := a.debug
	for _, w := range a.watches {
		expr := w
		if s == nil || s.state != "stopped" {
			a.watchValues[expr] = "not available"
			continue
		}
		s.evaluate(expr, "watch", func(res string, err error) {
			if err != nil {
				a.watchValues[expr] = err.Error()
			} else {
				a.watchValues[expr] = res
			}
		})
	}
}

func (a *App) createLaunchJSON() {
	if a.root == "" {
		a.notify(NotifyInfo, "Open a folder to create a launch.json file.")
		return
	}
	p := filepath.Join(a.root, ".vscode", "launch.json")
	if !fileExists(p) {
		cfg := a.defaultLaunchConfig()
		if cfg == nil {
			cfg = map[string]any{"name": "Launch Program", "type": "cppdbg", "request": "launch", "program": "${workspaceFolder}/a.out", "args": []any{}, "cwd": "${workspaceFolder}"}
		}
		data, _ := json.MarshalIndent(map[string]any{"version": "0.2.0", "configurations": []any{cfg}}, "", "  ")
		header := "{\n  // Use IntelliSense to learn about possible attributes.\n  // Hover to view descriptions of existing attributes.\n"
		body := strings.TrimPrefix(string(data), "{\n")
		_ = fsMkdirAll(filepath.Dir(p), 0o755)
		_ = fsWriteFile(p, []byte(header+body+"\n"), 0o644)
	}
	a.OpenFile(p, false)
	a.focusEditor()
}

//#endregion

//#region Toolbar and editor decorations

// drawDebugToolbar draws the floating toolbar in the title bar while debugging.
func (a *App) drawDebugToolbar(r Rect) bool {
	s := a.debug
	if s == nil || s.state == "terminated" {
		return false
	}
	th := a.theme
	type btn struct {
		icon string
		cmd  string
		tip  string
		key  string
	}
	contIcon := "" // continue
	if !a.nerdIcons {
		contIcon = "▶"
	}
	btns := []btn{}
	if s.state == "running" || s.state == "starting" {
		pauseIcon := ""
		if !a.nerdIcons {
			pauseIcon = "‖"
		}
		btns = append(btns, btn{pauseIcon, "workbench.action.debug.pause", "Pause", "F6"})
	} else {
		btns = append(btns, btn{contIcon, "workbench.action.debug.continue", "Continue", "F5"})
	}
	stepOver, stepInto, stepOut, restart, stop := "", "", "", "", ""
	if !a.nerdIcons {
		stepOver, stepInto, stepOut, restart, stop = "↷", "↓", "↑", "↻", "■"
	}
	btns = append(btns,
		btn{stepOver, "workbench.action.debug.stepOver", "Step Over", "F10"},
		btn{stepInto, "workbench.action.debug.stepInto", "Step Into", "F11"},
		btn{stepOut, "workbench.action.debug.stepOut", "Step Out", "Shift+F11"},
		btn{restart, "workbench.action.debug.restart", "Restart", "Ctrl+Shift+F5"},
		btn{stop, "workbench.action.debug.stop", "Stop", "Shift+F5"},
	)
	w := len(btns)*3 + 2
	x := r.X + (r.W-w)/2
	bg := th.C("debugToolBar.background")
	a.cv.Fill(Rect{x, r.Y, w, 1}, ' ', styleFB(th.C("foreground"), bg))
	a.cv.Set(x, r.Y, '⠿', styleFB(th.C("descriptionForeground"), bg))
	bx := x + 2
	colors := map[string]string{"workbench.action.debug.continue": "debugIcon.continueForeground", "workbench.action.debug.pause": "debugIcon.pauseForeground",
		"workbench.action.debug.stop": "debugIcon.stopForeground", "workbench.action.debug.restart": "debugIcon.restartForeground"}
	defaults := map[string]tcell.Color{"debugIcon.continueForeground": tcell.NewHexColor(0x75BEFF), "debugIcon.pauseForeground": tcell.NewHexColor(0x75BEFF),
		"debugIcon.stopForeground": tcell.NewHexColor(0xF48771), "debugIcon.restartForeground": tcell.NewHexColor(0x89D185)}
	for _, b := range btns {
		col := tcell.NewHexColor(0x75BEFF)
		if k, ok := colors[b.cmd]; ok {
			col = defaults[k]
			if th.Has(k) {
				col = th.C(k)
			}
		}
		a.cv.Text(bx, r.Y, bx+2, b.icon, styleFB(col, bg))
		cmd := b.cmd
		tip := b.tip + " (" + b.key + ")"
		a.addHit(Rect{bx - 1, r.Y, 3, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				a.RunCommand(cmd, nil)
			case MouseMove:
				a.showTooltipAt(ev.X, r.Y+1, tip)
			}
			return true
		})
		bx += 3
	}
	return true
}

func (a *App) registerDebugCommands() {
	a.cmd("workbench.action.debug.start", "Debug", "Start Debugging", func(any) { a.startDebugging(false) })
	a.cmd("workbench.action.debug.run", "Debug", "Run Without Debugging", func(any) {
		if len(a.launchConfigs()) == 0 && a.defaultLaunchConfig() == nil {
			a.runTaskPicker("run")
			return
		}
		a.startDebugging(true)
	})
	a.cmd("workbench.action.debug.continue", "Debug", "Continue", func(any) {
		if a.debug != nil {
			a.debug.cont()
		}
	})
	a.cmd("workbench.action.debug.pause", "Debug", "Pause", func(any) {
		if a.debug != nil {
			a.debug.pause()
		}
	})
	a.cmd("workbench.action.debug.stepOver", "Debug", "Step Over", func(any) {
		if a.debug != nil {
			a.debug.step("next")
		}
	})
	a.cmd("workbench.action.debug.stepInto", "Debug", "Step Into", func(any) {
		if a.debug != nil {
			a.debug.step("stepIn")
		}
	})
	a.cmd("workbench.action.debug.stepOut", "Debug", "Step Out", func(any) {
		if a.debug != nil {
			a.debug.step("stepOut")
		}
	})
	a.cmd("workbench.action.debug.stop", "Debug", "Stop", func(any) {
		if a.debug != nil {
			a.debug.stop()
		}
	})
	a.cmd("workbench.action.debug.restart", "Debug", "Restart", func(any) {
		if a.debug != nil {
			cfg := a.debug.config
			a.debug.stop()
			afterFunc(500, func() { a.Post(func() { a.launch(cfg, false) }) })
		}
	})
	a.cmd("workbench.action.debug.configure", "Debug", "Open 'launch.json'", func(any) { a.createLaunchJSON() })
	a.cmd("workbench.action.debug.selectandstart", "Debug", "Select and Start Debugging", func(any) {
		a.lastLaunch = ""
		a.startDebugging(false)
	})
	a.cmd("editor.debug.action.toggleBreakpoint", "Debug", "Toggle Breakpoint", a.ed(func(v *EditorView) {
		a.toggleBreakpoint(v.buf().Path, v.Cursor().Line)
	}))
	a.cmd("editor.debug.action.conditionalBreakpoint", "Debug", "Add Conditional Breakpoint...", a.ed(func(v *EditorView) {
		path, line := v.buf().Path, v.Cursor().Line
		cur := ""
		if bp := a.breakpointAt(path, line); bp != nil {
			cur = bp.Condition
		}
		a.promptInput("Conditional Breakpoint", "Expression that must be true to stop", cur, func(cond string) {
			bp := a.breakpointAt(path, line)
			if bp == nil {
				a.toggleBreakpoint(path, line)
				bp = a.breakpointAt(path, line)
			}
			if bp != nil {
				bp.Condition = cond
				a.syncBreakpoints(path)
			}
		})
	}))
	a.cmd("editor.debug.action.addLogPoint", "Debug", "Add Logpoint...", a.ed(func(v *EditorView) {
		path, line := v.buf().Path, v.Cursor().Line
		a.promptInput("Logpoint", "Message to log, use {expression} for values", "", func(msg string) {
			bp := a.breakpointAt(path, line)
			if bp == nil {
				a.toggleBreakpoint(path, line)
				bp = a.breakpointAt(path, line)
			}
			if bp != nil {
				bp.LogMsg = msg
				a.syncBreakpoints(path)
			}
		})
	}))
	a.cmd("workbench.debug.viewlet.action.removeAllBreakpoints", "Debug", "Remove All Breakpoints", func(any) {
		paths := []string{}
		for p := range a.breakpoints {
			paths = append(paths, p)
		}
		a.breakpoints = map[string][]*Breakpoint{}
		for _, p := range paths {
			a.syncBreakpoints(p)
		}
	})
	a.cmd("editor.debug.action.selectionToWatch", "Debug", "Add to Watch", a.ed(func(v *EditorView) {
		expr := v.SelectedText()
		if expr == "" {
			if wr, ok := v.WordAt(v.Cursor()); ok {
				expr = v.buf().TextRange(wr)
			}
		}
		if expr != "" {
			a.addWatch(expr)
		}
	}))
	a.cmd("workbench.debug.action.focusRepl", "Debug", "Focus on Debug Console View", func(any) { a.showPanel("debug") })
	a.cmd("workbench.view.debug", "View", "Show Run and Debug", func(any) { a.toggleSidebarView("debug") })
}

//#endregion

// startCommand runs a program in a new terminal (for runInTerminal requests).
func (tp *TerminalPanel) startCommand(title string, args []string, cwd string, env map[string]string) (int, error) {
	a := tp.app
	var envList []string
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	cols, rows := 80, 24
	if !tp.rect.Empty() {
		cols, rows = max(tp.rect.W, 10), max(tp.rect.H, 2)
	}
	if err := checkSandbox(cwd); err != nil {
		return 0, err
	}
	t, err := startTerm(args[0], args[1:], cwd, envList, cols, rows)
	if err != nil {
		return 0, err
	}
	tp.nextID++
	if title == "" {
		title = filepath.Base(args[0])
	}
	ti := &TermInstance{t: t, name: title, custom: true, id: tp.nextID}
	t.OnOutput = tp.notifyOutput
	t.OnExit = func() { a.Post(func() {}) }
	tp.groups = append(tp.groups, &TermGroup{terms: []*TermInstance{ti}})
	tp.active = len(tp.groups) - 1
	tp.rebuildFlat()
	return t.Pid(), nil
}
