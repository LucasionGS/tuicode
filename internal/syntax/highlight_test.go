package syntax

import (
	"fmt"
	"strings"
	"testing"
)

func TestHighlightDump(t *testing.T) {
	src := `package main

import "fmt"

// Server does things
type Server struct {
	Name string
}

func (s *Server) Start(port int) error {
	if port == 0 {
		return fmt.Errorf("bad port %d", port)
	}
	s.Name = "x"
	return nil
}`
	lang := Detect("x.go", "")
	h := NewHighlighter(lang, "x.go")
	lines := strings.Split(src, "\n")
	res := h.Highlight(lines)
	for i, l := range lines {
		rs := []rune(l)
		var parts []string
		for _, sp := range res[i] {
			parts = append(parts, fmt.Sprintf("%s:%d", string(rs[sp.Start:sp.End]), sp.Kind))
		}
		t.Log(l, " => ", strings.Join(parts, " | "))
	}
	for _, name := range []string{"go", "typescript", "tsx", "javascript", "react", "json", "python", "rust", "c", "cpp", "c#", "java", "kotlin", "swift", "php", "ruby", "lua", "bash", "fish", "html", "css", "scss", "markdown", "yaml", "toml", "xml", "sql", "docker", "makefile", "dart", "ini", "properties", "diff", "vue", "svelte", "zig", "haskell", "elixir", "erlang", "ocaml", "scala", "perl", "r", "powershell", "batch", "nginx", "cmake", "julia", "nix", "terraform", "graphql", "protobuf", "vim", "tex"} {
		if NewHighlighter(&Language{Lexer: name}, "").lexer == nil {
			t.Errorf("missing lexer %s", name)
		}
	}
}

func TestBracketsInsideNameTokens(t *testing.T) {
	// chroma's TSX lexer returns "render() {" as one name token
	src := "class Button extends React.Component<Props> {\n  render() {\n    return (\n      <button {...props}>\n      </button>\n    )\n  }\n}\n"
	h := NewHighlighter(ByID("typescriptreact"), "Button.tsx")
	spans := h.Highlight(strings.Split(src, "\n"))
	for line, ss := range spans {
		for _, sp := range ss {
			if sp.Kind == KInvalid {
				t.Fatalf("unexpected bracket on line %d col %d", line+1, sp.Start)
			}
		}
	}
	found := false
	for _, sp := range spans[1] {
		if sp.Kind == KFunction && sp.Start == 2 && sp.End == 8 {
			found = true
		}
	}
	if !found {
		t.Fatalf("render not colored as a function: %+v", spans[1])
	}
}
