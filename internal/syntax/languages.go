package syntax

import (
	"path/filepath"
	"sort"
	"strings"
)

// Language describes a language mode, using the same ids as VS Code.
type Language struct {
	ID           string
	Name         string
	Extensions   []string
	Filenames    []string
	Lexer        string // chroma lexer name
	LineComment  string
	BlockComment [2]string
	// Servers lists candidate language server commands, tried in order.
	Servers [][]string
	// AutoClose pairs, beyond the defaults.
	ExtraPairs [][2]rune
}

// Languages is the table of known language modes.
var Languages = []*Language{
	{ID: "plaintext", Name: "Plain Text", Extensions: []string{".txt", ".text", ".log"}, Lexer: ""},
	{ID: "go", Name: "Go", Extensions: []string{".go"}, Lexer: "go", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"gopls"}}},
	{ID: "go.mod", Name: "Go Module File", Filenames: []string{"go.mod", "go.work"}, Lexer: "go", LineComment: "//",
		Servers: [][]string{{"gopls"}}},
	{ID: "go.sum", Name: "Go Checksum File", Filenames: []string{"go.sum", "go.work.sum"}, Lexer: ""},
	{ID: "typescript", Name: "TypeScript", Extensions: []string{".ts", ".mts", ".cts"}, Lexer: "typescript", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"typescript-language-server", "--stdio"}, {"deno", "lsp"}}},
	{ID: "typescriptreact", Name: "TypeScript JSX", Extensions: []string{".tsx"}, Lexer: "tsx", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"typescript-language-server", "--stdio"}, {"deno", "lsp"}}},
	{ID: "javascript", Name: "JavaScript", Extensions: []string{".js", ".mjs", ".cjs"}, Lexer: "javascript", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"typescript-language-server", "--stdio"}}},
	{ID: "javascriptreact", Name: "JavaScript JSX", Extensions: []string{".jsx"}, Lexer: "react", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"typescript-language-server", "--stdio"}}},
	{ID: "json", Name: "JSON", Extensions: []string{".json", ".webmanifest", ".map"}, Filenames: []string{".prettierrc", ".babelrc"}, Lexer: "json",
		Servers: [][]string{{"vscode-json-language-server", "--stdio"}, {"vscode-json-languageserver", "--stdio"}}},
	{ID: "jsonc", Name: "JSON with Comments", Extensions: []string{".jsonc"}, Filenames: []string{"settings.json", "keybindings.json", "tsconfig.json", "jsconfig.json", "launch.json", "tasks.json", "deno.json", ".eslintrc.json", "devcontainer.json"}, Lexer: "json", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"vscode-json-language-server", "--stdio"}, {"vscode-json-languageserver", "--stdio"}}},
	{ID: "python", Name: "Python", Extensions: []string{".py", ".pyw", ".pyi"}, Lexer: "python", LineComment: "#", BlockComment: [2]string{`"""`, `"""`},
		Servers: [][]string{{"pyright-langserver", "--stdio"}, {"basedpyright-langserver", "--stdio"}, {"pylsp"}, {"jedi-language-server"}}},
	{ID: "rust", Name: "Rust", Extensions: []string{".rs"}, Lexer: "rust", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"rust-analyzer"}}},
	{ID: "c", Name: "C", Extensions: []string{".c", ".h"}, Lexer: "c", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"clangd"}}},
	{ID: "cpp", Name: "C++", Extensions: []string{".cpp", ".cc", ".cxx", ".c++", ".hpp", ".hh", ".hxx", ".h++", ".ino"}, Lexer: "cpp", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"clangd"}}},
	{ID: "csharp", Name: "C#", Extensions: []string{".cs", ".csx"}, Lexer: "c#", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"csharp-ls"}, {"OmniSharp", "-lsp"}}},
	{ID: "java", Name: "Java", Extensions: []string{".java"}, Lexer: "java", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"jdtls"}}},
	{ID: "kotlin", Name: "Kotlin", Extensions: []string{".kt", ".kts"}, Lexer: "kotlin", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"kotlin-language-server"}}},
	{ID: "swift", Name: "Swift", Extensions: []string{".swift"}, Lexer: "swift", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"sourcekit-lsp"}}},
	{ID: "dart", Name: "Dart", Extensions: []string{".dart"}, Lexer: "dart", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"dart", "language-server", "--protocol=lsp"}}},
	{ID: "php", Name: "PHP", Extensions: []string{".php", ".phtml"}, Lexer: "php", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"intelephense", "--stdio"}, {"phpactor", "language-server"}}},
	{ID: "ruby", Name: "Ruby", Extensions: []string{".rb", ".rake", ".gemspec"}, Filenames: []string{"Gemfile", "Rakefile"}, Lexer: "ruby", LineComment: "#",
		Servers: [][]string{{"ruby-lsp"}, {"solargraph", "stdio"}}},
	{ID: "lua", Name: "Lua", Extensions: []string{".lua"}, Lexer: "lua", LineComment: "--", BlockComment: [2]string{"--[[", "]]"},
		Servers: [][]string{{"lua-language-server"}}},
	{ID: "shellscript", Name: "Shell Script", Extensions: []string{".sh", ".bash", ".zsh", ".ksh"}, Filenames: []string{".bashrc", ".bash_profile", ".profile", ".zshrc", "PKGBUILD", ".env", ".env.example"}, Lexer: "bash", LineComment: "#",
		Servers: [][]string{{"bash-language-server", "start"}}},
	{ID: "fish", Name: "Fish", Extensions: []string{".fish"}, Lexer: "fish", LineComment: "#"},
	{ID: "powershell", Name: "PowerShell", Extensions: []string{".ps1", ".psm1", ".psd1"}, Lexer: "powershell", LineComment: "#", BlockComment: [2]string{"<#", "#>"}},
	{ID: "bat", Name: "Batch", Extensions: []string{".bat", ".cmd"}, Lexer: "batch", LineComment: "@REM"},
	{ID: "html", Name: "HTML", Extensions: []string{".html", ".htm", ".xhtml"}, Lexer: "html", BlockComment: [2]string{"<!--", "-->"},
		Servers: [][]string{{"vscode-html-language-server", "--stdio"}}},
	{ID: "vue", Name: "Vue", Extensions: []string{".vue"}, Lexer: "vue", BlockComment: [2]string{"<!--", "-->"},
		Servers: [][]string{{"vue-language-server", "--stdio"}}},
	{ID: "svelte", Name: "Svelte", Extensions: []string{".svelte"}, Lexer: "svelte", BlockComment: [2]string{"<!--", "-->"},
		Servers: [][]string{{"svelteserver", "--stdio"}}},
	{ID: "css", Name: "CSS", Extensions: []string{".css"}, Lexer: "css", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"vscode-css-language-server", "--stdio"}}},
	{ID: "scss", Name: "SCSS", Extensions: []string{".scss"}, Lexer: "scss", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"vscode-css-language-server", "--stdio"}}},
	{ID: "less", Name: "Less", Extensions: []string{".less"}, Lexer: "scss", LineComment: "//", BlockComment: [2]string{"/*", "*/"},
		Servers: [][]string{{"vscode-css-language-server", "--stdio"}}},
	{ID: "markdown", Name: "Markdown", Extensions: []string{".md", ".markdown", ".mdx"}, Lexer: "markdown", BlockComment: [2]string{"<!--", "-->"},
		Servers: [][]string{{"marksman", "server"}}},
	{ID: "yaml", Name: "YAML", Extensions: []string{".yml", ".yaml"}, Filenames: []string{".clang-format"}, Lexer: "yaml", LineComment: "#",
		Servers: [][]string{{"yaml-language-server", "--stdio"}}},
	{ID: "toml", Name: "TOML", Extensions: []string{".toml"}, Filenames: []string{"Cargo.lock"}, Lexer: "toml", LineComment: "#",
		Servers: [][]string{{"taplo", "lsp", "stdio"}}},
	{ID: "xml", Name: "XML", Extensions: []string{".xml", ".xsd", ".xsl", ".svg", ".plist", ".csproj", ".xaml"}, Lexer: "xml", BlockComment: [2]string{"<!--", "-->"}},
	{ID: "sql", Name: "SQL", Extensions: []string{".sql"}, Lexer: "sql", LineComment: "--", BlockComment: [2]string{"/*", "*/"}},
	{ID: "dockerfile", Name: "Docker", Extensions: []string{".dockerfile"}, Filenames: []string{"Dockerfile", "Containerfile"}, Lexer: "docker", LineComment: "#",
		Servers: [][]string{{"docker-langserver", "--stdio"}}},
	{ID: "dockercompose", Name: "Compose", Filenames: []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}, Lexer: "yaml", LineComment: "#",
		Servers: [][]string{{"yaml-language-server", "--stdio"}}},
	{ID: "makefile", Name: "Makefile", Extensions: []string{".mk", ".mak"}, Filenames: []string{"Makefile", "makefile", "GNUmakefile"}, Lexer: "makefile", LineComment: "#"},
	{ID: "cmake", Name: "CMake", Extensions: []string{".cmake"}, Filenames: []string{"CMakeLists.txt"}, Lexer: "cmake", LineComment: "#"},
	{ID: "ini", Name: "Ini", Extensions: []string{".ini", ".cfg", ".conf", ".desktop", ".service", ".editorconfig"}, Filenames: []string{".gitconfig"}, Lexer: "ini", LineComment: ";"},
	{ID: "properties", Name: "Properties", Extensions: []string{".properties"}, Lexer: "properties", LineComment: "#"},
	{ID: "diff", Name: "Diff", Extensions: []string{".diff", ".patch", ".rej"}, Lexer: "diff"},
	{ID: "git-commit", Name: "Git Commit Message", Filenames: []string{"COMMIT_EDITMSG", "MERGE_MSG"}, Lexer: "", LineComment: "#"},
	{ID: "ignore", Name: "Ignore", Filenames: []string{".gitignore", ".dockerignore", ".npmignore", ".eslintignore", ".prettierignore"}, Lexer: "", LineComment: "#"},
	{ID: "nginx", Name: "Nginx", Filenames: []string{"nginx.conf"}, Extensions: []string{".nginx"}, Lexer: "nginx", LineComment: "#"},
	{ID: "zig", Name: "Zig", Extensions: []string{".zig"}, Lexer: "zig", LineComment: "//", Servers: [][]string{{"zls"}}},
	{ID: "haskell", Name: "Haskell", Extensions: []string{".hs"}, Lexer: "haskell", LineComment: "--", BlockComment: [2]string{"{-", "-}"},
		Servers: [][]string{{"haskell-language-server-wrapper", "--lsp"}}},
	{ID: "elixir", Name: "Elixir", Extensions: []string{".ex", ".exs"}, Lexer: "elixir", LineComment: "#", Servers: [][]string{{"elixir-ls"}}},
	{ID: "erlang", Name: "Erlang", Extensions: []string{".erl", ".hrl"}, Lexer: "erlang", LineComment: "%"},
	{ID: "ocaml", Name: "OCaml", Extensions: []string{".ml", ".mli"}, Lexer: "ocaml", BlockComment: [2]string{"(*", "*)"}, Servers: [][]string{{"ocamllsp"}}},
	{ID: "scala", Name: "Scala", Extensions: []string{".scala", ".sc"}, Lexer: "scala", LineComment: "//", BlockComment: [2]string{"/*", "*/"}, Servers: [][]string{{"metals"}}},
	{ID: "perl", Name: "Perl", Extensions: []string{".pl", ".pm"}, Lexer: "perl", LineComment: "#"},
	{ID: "r", Name: "R", Extensions: []string{".r", ".R"}, Lexer: "r", LineComment: "#"},
	{ID: "julia", Name: "Julia", Extensions: []string{".jl"}, Lexer: "julia", LineComment: "#"},
	{ID: "nix", Name: "Nix", Extensions: []string{".nix"}, Lexer: "nix", LineComment: "#", Servers: [][]string{{"nil"}, {"nixd"}}},
	{ID: "terraform", Name: "Terraform", Extensions: []string{".tf", ".tfvars", ".hcl"}, Lexer: "terraform", LineComment: "#", Servers: [][]string{{"terraform-ls", "serve"}}},
	{ID: "graphql", Name: "GraphQL", Extensions: []string{".graphql", ".gql"}, Lexer: "graphql", LineComment: "#"},
	{ID: "proto3", Name: "Protocol Buffers", Extensions: []string{".proto"}, Lexer: "protobuf", LineComment: "//", BlockComment: [2]string{"/*", "*/"}},
	{ID: "vim", Name: "Vim Script", Extensions: []string{".vim"}, Filenames: []string{".vimrc"}, Lexer: "vim", LineComment: "\""},
	{ID: "latex", Name: "LaTeX", Extensions: []string{".tex", ".sty", ".cls"}, Lexer: "tex", LineComment: "%"},
	{ID: "csv", Name: "CSV", Extensions: []string{".csv", ".tsv"}, Lexer: ""},
}

var byID = map[string]*Language{}

func init() {
	for _, l := range Languages {
		byID[l.ID] = l
	}
}

// ByID returns a language by its id, or plaintext.
func ByID(id string) *Language {
	if l, ok := byID[id]; ok {
		return l
	}
	return byID["plaintext"]
}

// Detect picks a language for a path. firstLine is used for shebang detection.
func Detect(path, firstLine string) *Language {
	base := filepath.Base(path)
	for _, l := range Languages {
		for _, f := range l.Filenames {
			if f == base {
				return l
			}
		}
	}
	if strings.HasPrefix(base, "Dockerfile") || strings.HasSuffix(base, ".Dockerfile") {
		return byID["dockerfile"]
	}
	if strings.HasPrefix(base, ".env") {
		return byID["shellscript"]
	}
	ext := filepath.Ext(base)
	if ext != "" {
		for _, l := range Languages {
			for _, e := range l.Extensions {
				if e == ext || strings.EqualFold(e, ext) {
					return l
				}
			}
		}
	}
	if dir := filepath.Base(filepath.Dir(path)); dir == ".vscode" && ext == ".json" {
		return byID["jsonc"]
	}
	if strings.HasPrefix(firstLine, "#!") {
		switch {
		case strings.Contains(firstLine, "python"):
			return byID["python"]
		case strings.Contains(firstLine, "node"), strings.Contains(firstLine, "deno"):
			return byID["javascript"]
		case strings.Contains(firstLine, "fish"):
			return byID["fish"]
		case strings.Contains(firstLine, "sh"):
			return byID["shellscript"]
		case strings.Contains(firstLine, "ruby"):
			return byID["ruby"]
		case strings.Contains(firstLine, "perl"):
			return byID["perl"]
		}
	}
	return byID["plaintext"]
}

// SortedLanguages returns languages sorted by display name.
func SortedLanguages() []*Language {
	out := append([]*Language{}, Languages...)
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
