package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

type pendingCall struct {
	cb     func(json.RawMessage, error)
	method string
}

// Client talks to one language server process over stdio.
type Client struct {
	Name    string
	Command []string
	Root    string
	Folders []string // workspace folders, Root when empty

	cmd   *exec.Cmd
	stdin io.WriteCloser
	wmu   sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[int]*pendingCall
	closed  bool

	// Capabilities as returned by initialize.
	Capabilities map[string]any
	// PositionEncoding is "utf-16", "utf-8" or "utf-32".
	PositionEncoding string
	Ready            bool
	ServerName       string

	// Dispatch runs callbacks on the UI goroutine.
	Dispatch func(func())
	// OnNotification handles server notifications (on the UI goroutine).
	OnNotification func(method string, params json.RawMessage)
	// OnRequest handles server requests (on the UI goroutine) and returns a result.
	OnRequest func(method string, params json.RawMessage) (any, error)
	// Log receives stderr output and protocol logs.
	Log func(string)
	// OnExit is called when the process ends.
	OnExit func()

	queued []func()
}

// Start launches a language server.
func Start(name string, command []string, root string, dispatch func(func()), log func(string)) (*Client, error) {
	if len(command) == 0 {
		return nil, errors.New("Language server command is empty.")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{
		Name:             name,
		Command:          command,
		Root:             root,
		cmd:              cmd,
		stdin:            stdin,
		pending:          map[int]*pendingCall{},
		Dispatch:         dispatch,
		Log:              log,
		PositionEncoding: "utf-16",
	}
	go c.readLoop(stdout)
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if c.Log != nil {
				line := sc.Text()
				c.dispatch(func() { c.Log(line) })
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		c.mu.Lock()
		c.closed = true
		pend := c.pending
		c.pending = map[int]*pendingCall{}
		c.mu.Unlock()
		for _, p := range pend {
			pp := p
			c.dispatch(func() { pp.cb(nil, errors.New("Language server exited.")) })
		}
		if c.OnExit != nil {
			c.dispatch(c.OnExit)
		}
	}()
	return c, nil
}

func (c *Client) dispatch(fn func()) {
	if c.Dispatch != nil {
		c.Dispatch(fn)
	} else {
		fn()
	}
}

func (c *Client) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<16)
	for {
		length := -1
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
				length, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if length < 0 {
			continue
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(br, body); err != nil {
			return
		}
		var msg message
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		c.handle(&msg)
	}
}

func (c *Client) handle(msg *message) {
	if msg.Method != "" && msg.ID != nil {
		// request from server
		id := *msg.ID
		method, params := msg.Method, msg.Params
		c.dispatch(func() {
			var result any
			var err error
			if c.OnRequest != nil {
				result, err = c.OnRequest(method, params)
			}
			c.respond(id, result, err)
		})
		return
	}
	if msg.Method != "" {
		method, params := msg.Method, msg.Params
		c.dispatch(func() {
			if c.OnNotification != nil {
				c.OnNotification(method, params)
			}
		})
		return
	}
	if msg.ID == nil {
		return
	}
	var id int
	if json.Unmarshal(*msg.ID, &id) != nil {
		return
	}
	c.mu.Lock()
	p := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if p == nil {
		return
	}
	result := msg.Result
	var err error
	if msg.Error != nil {
		err = msg.Error
	}
	c.dispatch(func() { p.cb(result, err) })
}

func (c *Client) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = c.stdin.Write(data)
	return err
}

func (c *Client) respond(id json.RawMessage, result any, err error) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	if err != nil {
		resp["error"] = map[string]any{"code": -32603, "message": err.Error()}
	} else {
		resp["result"] = result
	}
	_ = c.write(resp)
}

// Call sends a request; cb runs on the UI goroutine with the raw result.
func (c *Client) Call(method string, params any, cb func(json.RawMessage, error)) int {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		if cb != nil {
			c.dispatch(func() { cb(nil, errors.New("Language server is not running.")) })
		}
		return 0
	}
	c.nextID++
	id := c.nextID
	if cb == nil {
		cb = func(json.RawMessage, error) {}
	}
	c.pending[id] = &pendingCall{cb: cb, method: method}
	c.mu.Unlock()
	err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		c.dispatch(func() { cb(nil, err) })
	}
	return id
}

// Cancel sends $/cancelRequest for an id.
func (c *Client) Cancel(id int) {
	if id == 0 {
		return
	}
	c.mu.Lock()
	_, ok := c.pending[id]
	c.mu.Unlock()
	if ok {
		c.Notify("$/cancelRequest", map[string]any{"id": id})
	}
}

// Notify sends a notification.
func (c *Client) Notify(method string, params any) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	_ = c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// WhenReady runs fn now if initialized, or after initialization finishes.
func (c *Client) WhenReady(fn func()) {
	if c.Ready {
		fn()
		return
	}
	c.queued = append(c.queued, fn)
}

// WorkspaceFolders returns the LSP workspace folders for a root and optional extra folders.
func WorkspaceFolders(root string, folders []string) []map[string]any {
	if len(folders) == 0 {
		folders = []string{root}
	}
	out := make([]map[string]any, 0, len(folders))
	for _, f := range folders {
		out = append(out, map[string]any{"uri": PathToURI(f), "name": baseName(f)})
	}
	return out
}

// Initialize performs the initialize handshake. done runs on the UI goroutine.
func (c *Client) Initialize(initOptions any, done func(error)) {
	params := map[string]any{
		"processId":        os.Getpid(),
		"clientInfo":       map[string]any{"name": "tuicode", "version": "0.2.0"},
		"rootUri":          PathToURI(c.Root),
		"rootPath":         c.Root,
		"workspaceFolders": WorkspaceFolders(c.Root, c.Folders),
		"capabilities":     clientCapabilities(),
		"trace":            "off",
	}
	if initOptions != nil {
		params["initializationOptions"] = initOptions
	}
	c.Call("initialize", params, func(raw json.RawMessage, err error) {
		if err != nil {
			done(err)
			return
		}
		var res struct {
			Capabilities map[string]any `json:"capabilities"`
			ServerInfo   *struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		}
		_ = json.Unmarshal(raw, &res)
		c.Capabilities = res.Capabilities
		if c.Capabilities == nil {
			c.Capabilities = map[string]any{}
		}
		if enc, ok := c.Capabilities["positionEncoding"].(string); ok {
			c.PositionEncoding = enc
		}
		if res.ServerInfo != nil {
			c.ServerName = res.ServerInfo.Name
		}
		c.Notify("initialized", map[string]any{})
		c.Ready = true
		for _, fn := range c.queued {
			fn()
		}
		c.queued = nil
		done(nil)
	})
}

func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Shutdown asks the server to exit and kills it after a timeout.
func (c *Client) Shutdown() {
	done := make(chan struct{})
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	c.nextID++
	_ = c.write(map[string]any{"jsonrpc": "2.0", "id": 999999, "method": "shutdown"})
	go func() {
		time.Sleep(300 * time.Millisecond)
		c.Notify("exit", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	time.AfterFunc(time.Second, func() {
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}

// Kill terminates the server process immediately.
func (c *Client) Kill() {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// Cap returns a capability value by dotted path, e.g. "completionProvider.triggerCharacters".
func (c *Client) Cap(path string) any {
	var cur any = c.Capabilities
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

// Supports reports whether a capability is present and not false.
func (c *Client) Supports(path string) bool {
	v := c.Cap(path)
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return true
}

// StringList returns a capability that is a list of strings.
func (c *Client) StringList(path string) []string {
	v, ok := c.Cap(path).([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range v {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// SyncKind returns the text document sync kind (0 none, 1 full, 2 incremental).
func (c *Client) SyncKind() int {
	switch v := c.Cap("textDocumentSync").(type) {
	case float64:
		return int(v)
	case map[string]any:
		if ch, ok := v["change"].(float64); ok {
			return int(ch)
		}
		return 1
	}
	return 1
}

func clientCapabilities() map[string]any {
	symbolKinds := make([]int, 26)
	for i := range symbolKinds {
		symbolKinds[i] = i + 1
	}
	completionKinds := make([]int, 25)
	for i := range completionKinds {
		completionKinds[i] = i + 1
	}
	return map[string]any{
		"general": map[string]any{
			"positionEncodings": []string{"utf-32", "utf-16"},
		},
		"workspace": map[string]any{
			"applyEdit":              true,
			"workspaceEdit":          map[string]any{"documentChanges": true, "resourceOperations": []string{"create", "rename", "delete"}},
			"didChangeConfiguration": map[string]any{"dynamicRegistration": false},
			"workspaceFolders":       true,
			"configuration":          true,
			"symbol":                 map[string]any{"symbolKind": map[string]any{"valueSet": symbolKinds}},
			"executeCommand":         map[string]any{},
			"fileOperations":         map[string]any{"willRename": true, "didRename": true},
			"diagnostics":            map[string]any{"refreshSupport": true},
		},
		"window": map[string]any{
			"workDoneProgress": true,
			"showMessage":      map[string]any{},
			"showDocument":     map[string]any{"support": true},
		},
		"textDocument": map[string]any{
			"synchronization": map[string]any{"didSave": true, "willSave": false, "willSaveWaitUntil": false},
			"completion": map[string]any{
				"completionItem": map[string]any{
					"snippetSupport":          true,
					"documentationFormat":     []string{"markdown", "plaintext"},
					"deprecatedSupport":       true,
					"preselectSupport":        true,
					"insertReplaceSupport":    true,
					"labelDetailsSupport":     true,
					"tagSupport":              map[string]any{"valueSet": []int{1}},
					"resolveSupport":          map[string]any{"properties": []string{"documentation", "detail", "additionalTextEdits"}},
					"commitCharactersSupport": true,
				},
				"completionItemKind": map[string]any{"valueSet": completionKinds},
				"contextSupport":     true,
				"completionList":     map[string]any{"itemDefaults": []string{"editRange", "insertTextFormat"}},
			},
			"hover":             map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			"signatureHelp":     map[string]any{"signatureInformation": map[string]any{"documentationFormat": []string{"markdown", "plaintext"}, "parameterInformation": map[string]any{"labelOffsetSupport": true}, "activeParameterSupport": true}, "contextSupport": true},
			"definition":        map[string]any{"linkSupport": true},
			"declaration":       map[string]any{"linkSupport": true},
			"typeDefinition":    map[string]any{"linkSupport": true},
			"implementation":    map[string]any{"linkSupport": true},
			"references":        map[string]any{},
			"callHierarchy":     map[string]any{},
			"typeHierarchy":     map[string]any{},
			"documentHighlight": map[string]any{},
			"diagnostic":        map[string]any{"dynamicRegistration": false, "relatedDocumentSupport": false},
			"documentSymbol": map[string]any{
				"hierarchicalDocumentSymbolSupport": true,
				"symbolKind":                        map[string]any{"valueSet": symbolKinds},
			},
			"formatting":      map[string]any{},
			"rangeFormatting": map[string]any{},
			"rename":          map[string]any{"prepareSupport": true},
			"codeAction": map[string]any{
				"codeActionLiteralSupport": map[string]any{
					"codeActionKind": map[string]any{"valueSet": []string{"", "quickfix", "refactor", "refactor.extract", "refactor.inline", "refactor.rewrite", "source", "source.organizeImports", "source.fixAll"}},
				},
				"isPreferredSupport": true,
				"disabledSupport":    true,
				"dataSupport":        true,
				"resolveSupport":     map[string]any{"properties": []string{"edit"}},
			},
			"publishDiagnostics": map[string]any{"relatedInformation": false, "tagSupport": map[string]any{"valueSet": []int{1, 2}}, "versionSupport": true},
			"foldingRange":       map[string]any{"lineFoldingOnly": true},
			"inlayHint":          map[string]any{},
		},
	}
}
