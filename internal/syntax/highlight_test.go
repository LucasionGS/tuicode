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
