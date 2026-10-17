package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"tuicode/internal/buffer"
)

// TestMain lets the test binary act as a fake language server.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_LSP") == "1" {
		fakeServer(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeServer(in io.Reader, out io.Writer) {
	r := bufio.NewReader(in)
	text := ""
	send := func(v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(out, "Content-Length: %d\r\n\r\n%s", len(data), data)
	}
	for {
		length := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				length, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			}
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var msg struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params"`
		}
		_ = json.Unmarshal(body, &msg)
		switch msg.Method {
		case "initialize":
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"capabilities": map[string]any{"textDocumentSync": 2, "positionEncoding": "utf-16"}}})
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					Text string `json:"text"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			text = p.TextDocument.Text
		case "textDocument/didChange":
			var p struct {
				ContentChanges []struct {
					Range *Range `json:"range"`
					Text  string `json:"text"`
				} `json:"contentChanges"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			for _, c := range p.ContentChanges {
				if c.Range == nil {
					text = c.Text
					continue
				}
				s := offsetUTF16(text, c.Range.Start)
				e := offsetUTF16(text, c.Range.End)
				text = text[:s] + c.Text + text[e:]
			}
		case "test/text":
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": text})
		case "shutdown":
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": nil})
		case "exit":
			return
		}
	}
}

// offsetUTF16 converts a UTF-16 based position into a byte offset.
func offsetUTF16(text string, p Position) int {
	lines := strings.SplitAfter(text, "\n")
	off := 0
	for i := 0; i < p.Line && i < len(lines); i++ {
		off += len(lines[i])
	}
	if p.Line >= len(lines) {
		return len(text)
	}
	line := lines[p.Line]
	units := 0
	for i, r := range line {
		if units >= p.Character || r == '\n' {
			return off + i
		}
		units += len(utf16.Encode([]rune{r}))
	}
	return off + len(strings.TrimSuffix(line, "\n"))
}

func TestIncrementalSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	_ = os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"héllo 😀\")\n}\n"), 0o644)
	exe, _ := os.Executable()
	t.Setenv("FAKE_LSP", "1")
	posted := make(chan func(), 1000)
	m := NewManager(dir, func(fn func()) { posted <- fn })
	m.Overrides = map[string][]string{"go": {exe}}
	b, err := buffer.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	b.LanguageID = "go"
	m.Open(b)
	pump := func(d time.Duration) {
		deadline := time.After(d)
		for {
			select {
			case fn := <-posted:
				fn()
			case <-deadline:
				return
			}
		}
	}
	pump(500 * time.Millisecond)
	if m.Client(b) == nil {
		t.Fatal("fake server did not initialize")
	}
	// a batch of edits across lines, with multi-byte and astral characters
	b.ApplyEdits([]buffer.Edit{
		{Range: buffer.Range{Start: buffer.Pos{Line: 3, Col: 10}, End: buffer.Pos{Line: 3, Col: 11}}, Text: "E"},
		{Range: buffer.Range{Start: buffer.Pos{Line: 3, Col: 16}, End: buffer.Pos{Line: 3, Col: 16}}, Text: "🎉 ok"},
		{Range: buffer.Range{Start: buffer.Pos{Line: 2, Col: 0}, End: buffer.Pos{Line: 2, Col: 0}}, Text: "// added\n"},
	}, nil, "")
	b.ApplyEdits([]buffer.Edit{{Range: buffer.Range{Start: buffer.Pos{Line: 0, Col: 0}, End: buffer.Pos{Line: 1, Col: 0}}, Text: ""}}, nil, "")
	b.Undo()
	b.ApplyEdits([]buffer.Edit{{Range: buffer.Range{Start: buffer.Pos{Line: 4, Col: 1}, End: buffer.Pos{Line: 4, Col: 1}}, Text: "x := \"a\nb\"\n\t"}}, nil, "")
	var got string
	done := make(chan struct{})
	m.Request(b, "test/text", map[string]any{}, func(raw json.RawMessage, err error) {
		_ = json.Unmarshal(raw, &got)
		close(done)
	})
	for {
		select {
		case fn := <-posted:
			fn()
			continue
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("no response")
		}
		break
	}
	if got != b.Text() {
		t.Fatalf("server text out of sync:\nserver: %q\nbuffer: %q", got, b.Text())
	}
	m.ShutdownAll()
}
