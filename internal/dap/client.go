package dap

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Response is a DAP response.
type Response struct {
	RequestSeq int             `json:"request_seq"`
	Success    bool            `json:"success"`
	Command    string          `json:"command"`
	Message    string          `json:"message"`
	Body       json.RawMessage `json:"body"`
}

// Err returns an error for unsuccessful responses.
func (r Response) Err() error {
	if r.Success {
		return nil
	}
	msg := r.Message
	var b struct {
		Error *struct {
			Format string `json:"format"`
		} `json:"error"`
	}
	if json.Unmarshal(r.Body, &b) == nil && b.Error != nil && b.Error.Format != "" {
		msg = b.Error.Format
	}
	if msg == "" {
		msg = r.Command + " failed"
	}
	return errors.New(msg)
}

// Event is a DAP event.
type Event struct {
	Event string          `json:"event"`
	Body  json.RawMessage `json:"body"`
}

type message struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Command    string          `json:"command,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    bool            `json:"success,omitempty"`
	Message    string          `json:"message,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Event      string          `json:"event,omitempty"`
}

// Client talks to a debug adapter.
type Client struct {
	conn    io.ReadWriteCloser
	proc    *exec.Cmd
	wmu     sync.Mutex
	mu      sync.Mutex
	seq     int
	pending map[int]func(Response)
	closed  bool

	// Dispatch runs callbacks on the UI goroutine.
	Dispatch func(func())
	// OnEvent receives adapter events.
	OnEvent func(Event)
	// OnRequest answers reverse requests (runInTerminal, startDebugging).
	OnRequest func(command string, args json.RawMessage) (any, error)
	// OnExit is called when the adapter goes away.
	OnExit func()
	// Log receives adapter stderr.
	Log func(string)
}

type stdio struct {
	io.Reader
	io.WriteCloser
}

// StartStdio launches an adapter that speaks DAP on stdin/stdout.
func StartStdio(command []string, dir string, env []string, dispatch func(func()), log func(string)) (*Client, error) {
	if len(command) == 0 {
		return nil, errors.New("Debug adapter command is empty.")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	errp, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := newClient(stdio{out, in}, cmd, dispatch, log)
	if errp != nil {
		go c.pipeLog(errp)
	}
	return c, nil
}

// StartReverse listens on a local port and launches an adapter that dials in.
// The {addr} placeholder in command is replaced with the listen address.
func StartReverse(command []string, dir string, env []string, dispatch func(func()), log func(string)) (*Client, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	addr := ln.Addr().String()
	args := make([]string, len(command))
	for i, a := range command {
		args[i] = strings.ReplaceAll(a, "{addr}", addr)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, _ := cmd.StdoutPipe()
	errp, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		ch <- result{conn, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			_ = cmd.Process.Kill()
			return nil, r.err
		}
		c := newClient(r.conn, cmd, dispatch, log)
		if out != nil {
			go c.pipeLog(out)
		}
		if errp != nil {
			go c.pipeLog(errp)
		}
		return c, nil
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("The debug adapter %s did not connect within 15 seconds.", args[0])
	}
}

func newClient(conn io.ReadWriteCloser, proc *exec.Cmd, dispatch func(func()), log func(string)) *Client {
	c := &Client{conn: conn, proc: proc, pending: map[int]func(Response){}, Dispatch: dispatch, Log: log}
	go c.readLoop()
	if proc != nil {
		go func() {
			_ = proc.Wait()
			c.shutdown()
		}()
	}
	return c
}

func (c *Client) pipeLog(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if c.Log != nil {
			c.dispatch(func() { c.Log(line) })
		}
	}
}

func (c *Client) dispatch(fn func()) {
	if c.Dispatch != nil {
		c.Dispatch(fn)
	} else {
		fn()
	}
}

func (c *Client) shutdown() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pend := c.pending
	c.pending = map[int]func(Response){}
	c.mu.Unlock()
	for _, cb := range pend {
		cb := cb
		c.dispatch(func() { cb(Response{Success: false, Message: "Debug adapter exited."}) })
	}
	if c.OnExit != nil {
		c.dispatch(c.OnExit)
	}
}

func (c *Client) readLoop() {
	br := bufio.NewReaderSize(c.conn, 1<<16)
	defer c.shutdown()
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
		var m message
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch m.Type {
		case "response":
			c.mu.Lock()
			cb := c.pending[m.RequestSeq]
			delete(c.pending, m.RequestSeq)
			c.mu.Unlock()
			if cb != nil {
				resp := Response{RequestSeq: m.RequestSeq, Success: m.Success, Command: m.Command, Message: m.Message, Body: m.Body}
				c.dispatch(func() { cb(resp) })
			}
		case "event":
			ev := Event{Event: m.Event, Body: m.Body}
			c.dispatch(func() {
				if c.OnEvent != nil {
					c.OnEvent(ev)
				}
			})
		case "request":
			seq, cmd, args := m.Seq, m.Command, m.Arguments
			c.dispatch(func() {
				var body any
				var err error
				if c.OnRequest != nil {
					body, err = c.OnRequest(cmd, args)
				} else {
					err = errors.New("unsupported")
				}
				resp := map[string]any{"type": "response", "request_seq": seq, "command": cmd, "success": err == nil}
				if err != nil {
					resp["message"] = err.Error()
				} else if body != nil {
					resp["body"] = body
				}
				c.send(resp)
			})
		}
	}
}

func (c *Client) send(m map[string]any) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	c.seq++
	m["seq"] = c.seq
	c.mu.Unlock()
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.conn, "Content-Length: %d\r\n\r\n%s", len(data), data)
}

// Request sends a request; cb runs on the UI goroutine.
func (c *Client) Request(command string, args any, cb func(Response)) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		if cb != nil {
			c.dispatch(func() { cb(Response{Message: "Debug adapter is not running."}) })
		}
		return
	}
	c.mu.Unlock()
	c.wmu.Lock()
	c.mu.Lock()
	c.seq++
	seq := c.seq
	if cb != nil {
		c.pending[seq] = cb
	} else {
		c.pending[seq] = func(Response) {}
	}
	c.mu.Unlock()
	m := map[string]any{"seq": seq, "type": "request", "command": command}
	if args != nil {
		m["arguments"] = args
	}
	data, _ := json.Marshal(m)
	_, err := fmt.Fprintf(c.conn, "Content-Length: %d\r\n\r\n%s", len(data), data)
	c.wmu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		if cb != nil {
			c.dispatch(func() { cb(Response{Message: err.Error()}) })
		}
	}
}

// Close kills the adapter.
func (c *Client) Close() {
	_ = c.conn.Close()
	if c.proc != nil && c.proc.Process != nil {
		_ = c.proc.Process.Kill()
	}
}

// Closed reports whether the adapter connection is gone.
func (c *Client) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
