package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"tuicode/internal/buffer"
	"tuicode/internal/syntax"
)

type contentChange struct {
	Range       *Range `json:"range,omitempty"`
	RangeLength *int   `json:"rangeLength,omitempty"`
	Text        string `json:"text"`
}

type document struct {
	buf      *buffer.Buffer
	client   *Client
	uri      string
	langID   string
	version  int
	pending  []contentChange
	full     bool
	listener int
	opened   bool
}

// FileDiagnostics holds diagnostics for one file along with the encoding needed to map them.
type FileDiagnostics struct {
	Path        string
	Diagnostics []Diagnostic
	Encoding    string
	Server      string
}

// Progress describes an ongoing server task.
type Progress struct {
	Server     string
	Title      string
	Message    string
	Percentage int
}

// Manager starts language servers and keeps open buffers in sync with them.
type Manager struct {
	Root     string
	Dispatch func(func())

	// Hooks into the UI. All are called on the UI goroutine.
	Log           func(server, line string)
	OnDiagnostics func(path string)
	OnMessage     func(kind int, server, message string)
	OnProgress    func()
	ApplyEdit     func(edit WorkspaceEdit) bool
	ShowDocument  func(path string, sel *Range)
	OnStatus      func()
	// Configuration answers workspace/configuration for a section.
	Configuration func(section string) any
	// OnMissing is called once per language when no server is installed for it.
	OnMissing       func(lang *syntax.Language)
	missingNotified map[string]bool

	// Overrides maps language ids to server commands (from settings). An empty slice disables.
	Overrides map[string][]string
	Disabled  bool

	clients  map[string]*Client
	failed   map[string]string
	docs     map[*buffer.Buffer]*document
	diags    map[string]map[string]*FileDiagnostics // path -> server -> diags
	progress map[string]*Progress
}

// NewManager creates a manager for a workspace root.
func NewManager(root string, dispatch func(func())) *Manager {
	return &Manager{
		Root:     root,
		Dispatch: dispatch,
		clients:  map[string]*Client{},
		failed:   map[string]string{},
		docs:     map[*buffer.Buffer]*document{},
		diags:    map[string]map[string]*FileDiagnostics{},
		progress: map[string]*Progress{},
	}
}

var extraBinDirs = func() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".deno", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".dotnet", "tools"),
		filepath.Join(home, ".config", "composer", "vendor", "bin"),
		"/usr/lib/rustup/bin",
	}
}()

// LookPath finds an executable on PATH, in common tool directories or in node_modules/.bin.
func (m *Manager) LookPath(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	dirs := append([]string{filepath.Join(m.Root, "node_modules", ".bin")}, extraBinDirs...)
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

func (m *Manager) fileExists(name string) bool {
	_, err := os.Stat(filepath.Join(m.Root, name))
	return err == nil
}

// ServerCommand picks the server command for a language, or nil.
func (m *Manager) ServerCommand(lang *syntax.Language) []string {
	if lang == nil {
		return nil
	}
	if cmd, ok := m.Overrides[lang.ID]; ok {
		if len(cmd) == 0 {
			return nil
		}
		if p, ok := m.LookPath(cmd[0]); ok {
			return append([]string{p}, cmd[1:]...)
		}
		return nil
	}
	isDeno := m.fileExists("deno.json") || m.fileExists("deno.jsonc")
	for _, cand := range lang.Servers {
		if cand[0] == "deno" && !isDeno {
			continue
		}
		if cand[0] == "typescript-language-server" && isDeno {
			continue
		}
		if p, ok := m.LookPath(cand[0]); ok {
			return append([]string{p}, cand[1:]...)
		}
	}
	// deno can serve TypeScript even without a deno.json when nothing else is available
	if lang.ID == "typescript" || lang.ID == "typescriptreact" || lang.ID == "javascript" || lang.ID == "javascriptreact" {
		if p, ok := m.LookPath("deno"); ok && !isDeno {
			if _, ok := m.LookPath("typescript-language-server"); !ok {
				return []string{p, "lsp"}
			}
		}
	}
	return nil
}

func (m *Manager) initOptions(cmd []string) any {
	base := filepath.Base(cmd[0])
	switch base {
	case "gopls":
		return map[string]any{"semanticTokens": true, "usePlaceholders": false}
	case "deno":
		return map[string]any{"enable": true, "lint": true, "unstable": true}
	case "vscode-json-language-server", "vscode-json-languageserver":
		return map[string]any{"provideFormatter": true}
	case "vscode-css-language-server", "vscode-html-language-server":
		return map[string]any{"provideFormatter": true}
	}
	return nil
}

func (m *Manager) log(server, line string) {
	if m.Log != nil {
		m.Log(server, line)
	}
}

// clientFor returns (starting if needed) the client for a command.
func (m *Manager) clientFor(cmd []string) *Client {
	key := strings.Join(cmd, " ")
	if c, ok := m.clients[key]; ok {
		return c
	}
	if _, failed := m.failed[key]; failed {
		return nil
	}
	name := filepath.Base(cmd[0])
	c, err := Start(name, cmd, m.Root, m.Dispatch, func(line string) { m.log(name, line) })
	if err != nil {
		m.failed[key] = err.Error()
		m.log(name, "Failed to start: "+err.Error())
		return nil
	}
	m.clients[key] = c
	m.log(name, "Starting "+key)
	c.OnNotification = func(method string, params json.RawMessage) { m.handleNotification(c, method, params) }
	c.OnRequest = func(method string, params json.RawMessage) (any, error) { return m.handleRequest(c, method, params) }
	c.OnExit = func() {
		m.log(name, "Server exited.")
		delete(m.clients, key)
		m.failed[key] = "exited"
		for b, d := range m.docs {
			if d.client == c {
				d.client = nil
				d.opened = false
				_ = b
			}
		}
		if m.OnStatus != nil {
			m.OnStatus()
		}
	}
	c.Initialize(m.initOptions(cmd), func(err error) {
		if err != nil {
			m.log(name, "Initialize failed: "+err.Error())
			return
		}
		srv := c.ServerName
		if srv == "" {
			srv = name
		}
		m.log(name, fmt.Sprintf("Initialized %s (position encoding %s)", srv, c.PositionEncoding))
		if cfg := m.config(""); cfg != nil {
			c.Notify("workspace/didChangeConfiguration", map[string]any{"settings": cfg})
		}
		if m.OnStatus != nil {
			m.OnStatus()
		}
	})
	return c
}

func (m *Manager) config(section string) any {
	if m.Configuration != nil {
		return m.Configuration(section)
	}
	return nil
}

func (m *Manager) handleNotification(c *Client, method string, params json.RawMessage) {
	switch method {
	case "textDocument/publishDiagnostics":
		var p PublishDiagnosticsParams
		if json.Unmarshal(params, &p) != nil {
			return
		}
		path := URIToPath(p.URI)
		if m.diags[path] == nil {
			m.diags[path] = map[string]*FileDiagnostics{}
		}
		m.diags[path][c.Name] = &FileDiagnostics{Path: path, Diagnostics: p.Diagnostics, Encoding: c.PositionEncoding, Server: c.Name}
		if m.OnDiagnostics != nil {
			m.OnDiagnostics(path)
		}
	case "window/showMessage":
		var p struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &p) == nil && m.OnMessage != nil {
			m.OnMessage(p.Type, c.Name, p.Message)
		}
	case "window/logMessage":
		var p struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &p) == nil {
			m.log(c.Name, p.Message)
		}
	case "$/progress":
		var p struct {
			Token any `json:"token"`
			Value struct {
				Kind       string `json:"kind"`
				Title      string `json:"title"`
				Message    string `json:"message"`
				Percentage int    `json:"percentage"`
			} `json:"value"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		key := fmt.Sprintf("%s:%v", c.Name, p.Token)
		switch p.Value.Kind {
		case "begin":
			m.progress[key] = &Progress{Server: c.Name, Title: p.Value.Title, Message: p.Value.Message, Percentage: p.Value.Percentage}
		case "report":
			if pr, ok := m.progress[key]; ok {
				if p.Value.Message != "" {
					pr.Message = p.Value.Message
				}
				pr.Percentage = p.Value.Percentage
			}
		case "end":
			delete(m.progress, key)
		}
		if m.OnProgress != nil {
			m.OnProgress()
		}
	}
}

func (m *Manager) handleRequest(c *Client, method string, params json.RawMessage) (any, error) {
	switch method {
	case "workspace/configuration":
		var p struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		out := make([]any, len(p.Items))
		for i, it := range p.Items {
			out[i] = m.config(it.Section)
		}
		return out, nil
	case "workspace/applyEdit":
		var p struct {
			Edit WorkspaceEdit `json:"edit"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return map[string]any{"applied": false}, nil
		}
		ok := false
		if m.ApplyEdit != nil {
			ok = m.ApplyEdit(p.Edit)
		}
		return map[string]any{"applied": ok}, nil
	case "workspace/workspaceFolders":
		return []map[string]any{{"uri": PathToURI(m.Root), "name": filepath.Base(m.Root)}}, nil
	case "window/showDocument":
		var p struct {
			URI       string `json:"uri"`
			Selection *Range `json:"selection"`
		}
		if json.Unmarshal(params, &p) == nil && m.ShowDocument != nil {
			m.ShowDocument(URIToPath(p.URI), p.Selection)
		}
		return map[string]any{"success": true}, nil
	case "window/showMessageRequest":
		var p struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &p) == nil && m.OnMessage != nil {
			m.OnMessage(p.Type, c.Name, p.Message)
		}
		return nil, nil
	}
	return nil, nil
}

// Open starts syncing a buffer with its language server.
func (m *Manager) Open(buf *buffer.Buffer) {
	if m.Disabled || buf.Path == "" {
		return
	}
	if _, ok := m.docs[buf]; ok {
		return
	}
	lang := syntax.ByID(buf.LanguageID)
	cmd := m.ServerCommand(lang)
	if cmd == nil {
		if _, overridden := m.Overrides[lang.ID]; len(lang.Servers) > 0 && !overridden && m.OnMissing != nil {
			if m.missingNotified == nil {
				m.missingNotified = map[string]bool{}
			}
			if !m.missingNotified[lang.ID] {
				m.missingNotified[lang.ID] = true
				m.OnMissing(lang)
			}
		}
		return
	}
	c := m.clientFor(cmd)
	if c == nil {
		return
	}
	d := &document{buf: buf, client: c, uri: PathToURI(buf.Path), langID: lspLanguageID(buf.LanguageID)}
	m.docs[buf] = d
	d.listener = buf.AddListener(func(ch buffer.Change) { m.onChange(d, ch) })
	c.WhenReady(func() {
		if d.client != c || m.docs[buf] != d {
			return
		}
		d.version++
		d.pending = nil
		d.full = false
		c.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{"uri": d.uri, "languageId": d.langID, "version": d.version, "text": buf.Text()},
		})
		d.opened = true
	})
}

func lspLanguageID(id string) string {
	switch id {
	case "go.mod":
		return "go.mod"
	case "dockercompose":
		return "yaml"
	}
	return id
}

// Rename updates a document's path (after Save As).
func (m *Manager) Reopen(buf *buffer.Buffer) {
	m.Close(buf)
	m.Open(buf)
}

// Close stops syncing a buffer.
func (m *Manager) Close(buf *buffer.Buffer) {
	d, ok := m.docs[buf]
	if !ok {
		return
	}
	buf.RemoveListener(d.listener)
	delete(m.docs, buf)
	if d.client != nil && d.opened {
		d.client.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": d.uri}})
	}
}

func (m *Manager) onChange(d *document, ch buffer.Change) {
	if d.client == nil || !d.opened {
		return
	}
	if d.client.SyncKind() != 2 {
		d.full = true
		return
	}
	enc := d.client.PositionEncoding
	start := toLSPPos(d.buf, ch.Old.Start, enc)
	var end Position
	removedLines := strings.Split(ch.Removed, "\n")
	if len(removedLines) == 1 {
		end = Position{start.Line, start.Character + encLen(ch.Removed, enc)}
	} else {
		end = Position{ch.Old.Start.Line + len(removedLines) - 1, encLen(removedLines[len(removedLines)-1], enc)}
	}
	r := Range{start, end}
	d.pending = append(d.pending, contentChange{Range: &r, Text: ch.Text})
}

func encLen(s string, enc string) int {
	switch enc {
	case "utf-8":
		return len(s)
	case "utf-32":
		return utf8.RuneCountInString(s)
	}
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Flush sends pending changes to the servers.
func (m *Manager) Flush() {
	for _, d := range m.docs {
		m.flushDoc(d)
	}
}

func (m *Manager) flushDoc(d *document) {
	if d.client == nil || !d.opened {
		return
	}
	if !d.full && len(d.pending) == 0 {
		return
	}
	d.version++
	var changes []contentChange
	if d.full {
		changes = []contentChange{{Text: d.buf.Text()}}
	} else {
		changes = d.pending
	}
	d.pending = nil
	d.full = false
	d.client.Notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": d.uri, "version": d.version},
		"contentChanges": changes,
	})
}

// Saved notifies servers that a buffer was saved.
func (m *Manager) Saved(buf *buffer.Buffer) {
	d, ok := m.docs[buf]
	if !ok || d.client == nil || !d.opened {
		return
	}
	m.flushDoc(d)
	params := map[string]any{"textDocument": map[string]any{"uri": d.uri}}
	if v, ok := d.client.Cap("textDocumentSync").(map[string]any); ok {
		if save, ok := v["save"].(map[string]any); ok {
			if inc, _ := save["includeText"].(bool); inc {
				params["text"] = buf.Text()
			}
		}
	}
	d.client.Notify("textDocument/didSave", params)
}

// Client returns the language client for a buffer, if any.
func (m *Manager) Client(buf *buffer.Buffer) *Client {
	d, ok := m.docs[buf]
	if !ok || d.client == nil || !d.client.Ready || !d.opened {
		return nil
	}
	return d.client
}

// HasDoc reports whether the buffer is attached to a server (possibly still starting).
func (m *Manager) HasDoc(buf *buffer.Buffer) bool {
	d, ok := m.docs[buf]
	return ok && d.client != nil
}

// ServerName returns the name of the server attached to buf.
func (m *Manager) ServerName(buf *buffer.Buffer) string {
	d, ok := m.docs[buf]
	if !ok || d.client == nil {
		return ""
	}
	return d.client.Name
}

// Clients returns the running clients.
func (m *Manager) Clients() []*Client {
	var out []*Client
	for _, c := range m.clients {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Progress returns ongoing progress items.
func (m *Manager) Progress() []*Progress {
	var out []*Progress
	for _, p := range m.progress {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// Restart kills all servers and re-opens documents.
func (m *Manager) Restart() {
	bufs := []*buffer.Buffer{}
	for b := range m.docs {
		bufs = append(bufs, b)
	}
	for _, b := range bufs {
		m.Close(b)
	}
	for _, c := range m.clients {
		c.Kill()
	}
	m.clients = map[string]*Client{}
	m.failed = map[string]string{}
	m.diags = map[string]map[string]*FileDiagnostics{}
	for _, b := range bufs {
		m.Open(b)
	}
}

// ShutdownAll stops all servers.
func (m *Manager) ShutdownAll() {
	for _, c := range m.clients {
		c.Shutdown()
	}
}

// Diagnostics returns all diagnostics for a path across servers.
func (m *Manager) Diagnostics(path string) []*FileDiagnostics {
	var out []*FileDiagnostics
	for _, fd := range m.diags[path] {
		out = append(out, fd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}

// AllDiagnostics returns diagnostics for every path that has any.
func (m *Manager) AllDiagnostics() map[string][]*FileDiagnostics {
	out := map[string][]*FileDiagnostics{}
	for p := range m.diags {
		if ds := m.Diagnostics(p); len(ds) > 0 {
			has := false
			for _, d := range ds {
				if len(d.Diagnostics) > 0 {
					has = true
				}
			}
			if has {
				out[p] = ds
			}
		}
	}
	return out
}

// ToLSP converts a buffer position for the buffer's server.
func (m *Manager) ToLSP(buf *buffer.Buffer, p buffer.Pos) Position {
	enc := "utf-16"
	if c := m.Client(buf); c != nil {
		enc = c.PositionEncoding
	}
	return toLSPPos(buf, p, enc)
}

// FromLSP converts a server position into a buffer position.
func (m *Manager) FromLSP(buf *buffer.Buffer, p Position) buffer.Pos {
	enc := "utf-16"
	if c := m.Client(buf); c != nil {
		enc = c.PositionEncoding
	}
	return FromLSPPos(buf, p, enc)
}

// RangeFromLSP converts a server range into a buffer range.
func (m *Manager) RangeFromLSP(buf *buffer.Buffer, r Range) buffer.Range {
	return buffer.Range{Start: m.FromLSP(buf, r.Start), End: m.FromLSP(buf, r.End)}
}

func toLSPPos(buf *buffer.Buffer, p buffer.Pos, enc string) Position {
	switch enc {
	case "utf-32":
		return Position{p.Line, p.Col}
	case "utf-8":
		return Position{p.Line, buf.ByteCol(p)}
	}
	return Position{p.Line, buf.PosToUTF16(p)}
}

// FromLSPPos converts an LSP position with the given encoding into a buffer position.
func FromLSPPos(buf *buffer.Buffer, p Position, enc string) buffer.Pos {
	line := p.Line
	if line >= buf.LineCount() {
		return buf.EndPos()
	}
	if line < 0 {
		return buffer.Pos{}
	}
	switch enc {
	case "utf-32":
		return buf.Clamp(buffer.Pos{Line: line, Col: p.Character})
	case "utf-8":
		l := buf.Line(line)
		n := 0
		for i, r := range l {
			if n >= p.Character {
				return buffer.Pos{Line: line, Col: i}
			}
			n += utf8.RuneLen(r)
		}
		return buffer.Pos{Line: line, Col: len(l)}
	}
	return buffer.Pos{Line: line, Col: buf.UTF16ToCol(line, p.Character)}
}

// DocParams returns {textDocument, position} params for a request.
func (m *Manager) DocParams(buf *buffer.Buffer, p buffer.Pos) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": PathToURI(buf.Path)},
		"position":     m.ToLSP(buf, p),
	}
}

// Request flushes pending changes and sends a request for the buffer's server.
// It returns false when no server is available.
func (m *Manager) Request(buf *buffer.Buffer, method string, params any, cb func(json.RawMessage, error)) (int, bool) {
	c := m.Client(buf)
	if c == nil {
		return 0, false
	}
	if d, ok := m.docs[buf]; ok {
		m.flushDoc(d)
	}
	return c.Call(method, params, cb), true
}

// ParseLocations decodes a definition-style result (Location, []Location or []LocationLink).
func ParseLocations(raw json.RawMessage) []Location {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var one Location
	if json.Unmarshal(raw, &one) == nil && one.URI != "" {
		return []Location{one}
	}
	var many []json.RawMessage
	if json.Unmarshal(raw, &many) != nil {
		return nil
	}
	var out []Location
	for _, r := range many {
		var l Location
		if json.Unmarshal(r, &l) == nil && l.URI != "" {
			out = append(out, l)
			continue
		}
		var ll LocationLink
		if json.Unmarshal(r, &ll) == nil && ll.TargetURI != "" {
			out = append(out, Location{URI: ll.TargetURI, Range: ll.TargetSelectionRange})
		}
	}
	return out
}

// ParseCompletion decodes a completion result.
func ParseCompletion(raw json.RawMessage) *CompletionList {
	if len(raw) == 0 || string(raw) == "null" {
		return &CompletionList{}
	}
	var list CompletionList
	if raw[0] == '[' {
		_ = json.Unmarshal(raw, &list.Items)
		return &list
	}
	_ = json.Unmarshal(raw, &list)
	if list.ItemDefaults != nil {
		for i := range list.Items {
			it := &list.Items[i]
			if len(it.TextEdit) == 0 && len(list.ItemDefaults.EditRange) > 0 {
				text := it.InsertText
				if it.TextEdit == nil && text == "" {
					text = it.Label
				}
				var r Range
				if json.Unmarshal(list.ItemDefaults.EditRange, &r) == nil && (r.Start != Position{} || r.End != Position{}) {
					b, _ := json.Marshal(map[string]any{"range": r, "newText": text})
					it.TextEdit = b
				} else {
					var ir struct {
						Insert  Range `json:"insert"`
						Replace Range `json:"replace"`
					}
					if json.Unmarshal(list.ItemDefaults.EditRange, &ir) == nil {
						b, _ := json.Marshal(map[string]any{"insert": ir.Insert, "replace": ir.Replace, "newText": text})
						it.TextEdit = b
					}
				}
			}
			if it.InsertTextFormat == 0 && list.ItemDefaults.InsertTextFormat != 0 {
				it.InsertTextFormat = list.ItemDefaults.InsertTextFormat
			}
		}
	}
	return &list
}

// ParseSymbols decodes a documentSymbol result in either shape.
func ParseSymbols(raw json.RawMessage) []DocumentSymbol {
	var syms []DocumentSymbol
	if json.Unmarshal(raw, &syms) != nil {
		return nil
	}
	for i := range syms {
		if syms[i].Location != nil {
			syms[i].Range = syms[i].Location.Range
			syms[i].SelectionRange = syms[i].Location.Range
		}
	}
	return syms
}

// FromLSPRangeGuess converts a range for a buffer without a known server encoding (assumes UTF-16).
func FromLSPRangeGuess(b *buffer.Buffer, r Range) buffer.Range {
	return buffer.Range{Start: FromLSPPos(b, r.Start, "utf-16"), End: FromLSPPos(b, r.End, "utf-16")}
}
