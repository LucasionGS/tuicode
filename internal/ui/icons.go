package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Icon names used across the workbench (VS Code codicons).
const (
	IcoExplorer = iota
	IcoSearch
	IcoSCM
	IcoDebug
	IcoExtensions
	IcoAccount
	IcoSettings
	IcoClose
	IcoChevronRight
	IcoChevronDown
	IcoChevronUp
	IcoError
	IcoWarning
	IcoInfo
	IcoBell
	IcoBellDot
	IcoBranch
	IcoSync
	IcoDirty
	IcoSplit
	IcoEllipsis
	IcoNewFile
	IcoNewFolder
	IcoRefresh
	IcoCollapseAll
	IcoTerminal
	IcoTrash
	IcoAdd
	IcoCheck
	IcoArrowUp
	IcoArrowDown
	IcoCaseSensitive
	IcoWholeWord
	IcoRegex
	IcoReplace
	IcoReplaceAll
	IcoLightbulb
	IcoDiscard
	IcoRemove
	IcoKebab
	IcoMaximize
	IcoRestore
	IcoClearAll
	IcoFolder
	IcoFolderOpen
	IcoFile
	IcoListFlat
	IcoListTree
	IcoGear
	IcoPreserveCase
	IcoSelection
	IcoPass
	IcoServer
	IcoPlay
	IcoOutline
	IcoPinned
	IcoCount
)

var nerdIcons = [IcoCount]string{
	IcoExplorer: "", IcoSearch: "", IcoSCM: "", IcoDebug: "", IcoExtensions: "",
	IcoPinned: "", IcoAccount: "", IcoSettings: "", IcoClose: "", IcoChevronRight: "", IcoChevronDown: "",
	IcoChevronUp: "", IcoError: "", IcoWarning: "", IcoInfo: "", IcoBell: "", IcoBellDot: "",
	IcoBranch: "", IcoSync: "", IcoDirty: "", IcoSplit: "", IcoEllipsis: "",
	IcoNewFile: "", IcoNewFolder: "", IcoRefresh: "", IcoCollapseAll: "", IcoTerminal: "",
	IcoTrash: "", IcoAdd: "", IcoCheck: "", IcoArrowUp: "", IcoArrowDown: "",
	IcoCaseSensitive: "", IcoWholeWord: "", IcoRegex: "", IcoReplace: "", IcoReplaceAll: "",
	IcoLightbulb: "", IcoDiscard: "", IcoRemove: "", IcoKebab: "", IcoMaximize: "",
	IcoRestore: "", IcoClearAll: "", IcoFolder: "", IcoFolderOpen: "", IcoFile: "",
	IcoListFlat: "", IcoListTree: "", IcoGear: "", IcoPreserveCase: "", IcoSelection: "",
	IcoPass: "", IcoServer: "", IcoPlay: "", IcoOutline: "",
}

var plainIcons = [IcoCount]string{
	IcoExplorer: "▤", IcoSearch: "⌕", IcoSCM: "⑂", IcoDebug: "▷", IcoExtensions: "⊞",
	IcoPinned: "⚲", IcoAccount: "◯", IcoSettings: "≡", IcoClose: "×", IcoChevronRight: "›", IcoChevronDown: "⌄",
	IcoChevronUp: "⌃", IcoError: "⊗", IcoWarning: "△", IcoInfo: "ⓘ", IcoBell: "◔", IcoBellDot: "◕",
	IcoBranch: "⎇", IcoSync: "↻", IcoDirty: "●", IcoSplit: "◫", IcoEllipsis: "…",
	IcoNewFile: "+", IcoNewFolder: "⊕", IcoRefresh: "↻", IcoCollapseAll: "⊟", IcoTerminal: "›",
	IcoTrash: "✕", IcoAdd: "+", IcoCheck: "✓", IcoArrowUp: "↑", IcoArrowDown: "↓",
	IcoCaseSensitive: "Aa", IcoWholeWord: "ab", IcoRegex: ".*", IcoReplace: "⇄", IcoReplaceAll: "⇉",
	IcoLightbulb: "✱", IcoDiscard: "↶", IcoRemove: "−", IcoKebab: "⋮", IcoMaximize: "□",
	IcoRestore: "▣", IcoClearAll: "⌧", IcoFolder: "▸", IcoFolderOpen: "▾", IcoFile: "·",
	IcoListFlat: "☰", IcoListTree: "⊢", IcoGear: "≡", IcoPreserveCase: "AB", IcoSelection: "≣",
	IcoPass: "✓", IcoServer: "⊟", IcoPlay: "▶", IcoOutline: "◇",
}

// detectNerdFont looks for an installed Nerd Font.
func detectNerdFont() bool {
	home, _ := os.UserHomeDir()
	dirs := []string{"/usr/share/fonts", "/usr/local/share/fonts", filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts"), filepath.Join(home, "Library", "Fonts")}
	found := false
	for _, d := range dirs {
		_ = filepath.WalkDir(d, func(p string, de os.DirEntry, err error) error {
			if err != nil || found {
				return filepath.SkipDir
			}
			name := strings.ToLower(de.Name())
			if !de.IsDir() && (strings.Contains(name, "nerd") || strings.Contains(name, "nfm") || strings.Contains(name, "nf-")) {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}

// Icon returns the glyph for an icon id.
func (a *App) Icon(id int) string {
	if a.nerdIcons {
		return nerdIcons[id]
	}
	return plainIcons[id]
}

// Seti colors used by VS Code's default file icon theme.
var setiColors = map[string]string{
	"blue": "#519aba", "grey": "#4d5a5e", "grey-light": "#6d8086", "green": "#8dc149", "orange": "#e37933",
	"pink": "#f55385", "purple": "#a074c4", "red": "#cc3e44", "white": "#d4d7d6", "yellow": "#cbcb41", "ignore": "#41535b",
}

type fileIcon struct {
	glyph rune
	color string
}

var iconByExt = map[string]fileIcon{
	".go": {'', "blue"}, ".mod": {'', "pink"}, ".sum": {'', "pink"},
	".ts": {'', "blue"}, ".mts": {'', "blue"}, ".cts": {'', "blue"}, ".tsx": {'', "blue"},
	".js": {'', "yellow"}, ".mjs": {'', "yellow"}, ".cjs": {'', "yellow"}, ".jsx": {'', "yellow"},
	".json": {'', "yellow"}, ".jsonc": {'', "yellow"}, ".md": {'', "blue"}, ".mdx": {'', "yellow"},
	".py": {'', "blue"}, ".rs": {'', "grey-light"}, ".c": {'', "blue"}, ".h": {'', "purple"},
	".cpp": {'', "blue"}, ".cc": {'', "blue"}, ".hpp": {'', "purple"}, ".cs": {'', "blue"},
	".java": {'', "red"}, ".kt": {'', "orange"}, ".kts": {'', "orange"}, ".swift": {'', "orange"},
	".php": {'', "purple"}, ".rb": {'', "red"}, ".lua": {'', "blue"}, ".sh": {'', "green"},
	".bash": {'', "green"}, ".zsh": {'', "green"}, ".fish": {'', "green"}, ".ps1": {'', "blue"},
	".html": {'', "orange"}, ".htm": {'', "orange"}, ".css": {'', "blue"}, ".scss": {'', "pink"},
	".sass": {'', "pink"}, ".less": {'', "blue"}, ".yml": {'', "purple"}, ".yaml": {'', "purple"},
	".toml": {'', "grey-light"}, ".ini": {'', "grey-light"}, ".conf": {'', "grey-light"}, ".cfg": {'', "grey-light"},
	".xml": {'', "orange"}, ".sql": {'', "pink"}, ".db": {'', "pink"}, ".sqlite": {'', "pink"},
	".dart": {'', "blue"}, ".vue": {'', "green"}, ".svelte": {'', "red"}, ".zig": {'', "orange"},
	".hs": {'', "purple"}, ".ex": {'', "purple"}, ".exs": {'', "purple"}, ".erl": {'', "red"},
	".ml": {'', "orange"}, ".scala": {'', "red"}, ".pl": {'', "blue"}, ".r": {'', "blue"},
	".jl": {'', "purple"}, ".tf": {'', "purple"}, ".graphql": {'', "pink"}, ".gql": {'', "pink"},
	".lock": {'', "green"}, ".txt": {'', "white"}, ".log": {'', "grey-light"}, ".csv": {'', "green"},
	".png": {'', "purple"}, ".jpg": {'', "purple"}, ".jpeg": {'', "purple"}, ".gif": {'', "purple"},
	".webp": {'', "purple"}, ".ico": {'', "yellow"}, ".svg": {'', "purple"}, ".pdf": {'', "red"},
	".zip": {'', "grey-light"}, ".gz": {'', "grey-light"}, ".tar": {'', "grey-light"}, ".xz": {'', "grey-light"},
	".mp4": {'', "pink"}, ".mp3": {'', "purple"}, ".wav": {'', "purple"}, ".ttf": {'', "red"},
	".woff": {'', "red"}, ".woff2": {'', "red"}, ".tex": {'', "blue"}, ".proto": {'', "purple"},
	".nix": {'\U000f1105', "blue"}, ".wasm": {'', "purple"}, ".twig": {'', "green"}, ".ejs": {'', "yellow"},
	".prisma": {'', "blue"}, ".env": {'', "grey-light"}, ".diff": {'', "grey-light"}, ".patch": {'', "grey-light"},
	".mk": {'', "orange"}, ".cmake": {'', "blue"}, ".gradle": {'', "blue"}, ".rasi": {'', "grey-light"},
}

var iconByName = map[string]fileIcon{
	"dockerfile": {'', "blue"}, "makefile": {'', "orange"}, "license": {'', "yellow"}, "license.md": {'', "yellow"},
	"package.json": {'', "red"}, "package-lock.json": {'', "red"}, ".gitignore": {'', "grey-light"},
	".gitattributes": {'', "grey-light"}, ".gitmodules": {'', "grey-light"}, "tsconfig.json": {'', "blue"},
	".eslintrc": {'', "purple"}, ".eslintrc.json": {'', "purple"}, "eslint.config.js": {'', "purple"},
	".editorconfig": {'', "white"}, "yarn.lock": {'', "blue"}, "readme.md": {'', "blue"},
	"docker-compose.yml": {'', "pink"}, "docker-compose.yaml": {'', "pink"}, "compose.yml": {'', "pink"},
	"cargo.toml": {'', "grey-light"}, "go.mod": {'', "pink"}, "go.sum": {'', "pink"},
	"pkgbuild": {'', "green"}, ".npmignore": {'', "red"}, ".env": {'', "grey-light"},
	".env.example": {'', "grey-light"}, "deno.json": {'', "yellow"}, "vite.config.ts": {'', "blue"},
}

// FileIcon returns the glyph and style for a file name (only used with nerd icons).
func (a *App) FileIcon(name string, isDir, open bool) (string, tcell.Color) {
	if isDir {
		if !a.nerdIcons {
			return "", 0
		}
		return "", 0
	}
	lower := strings.ToLower(name)
	fi, ok := iconByName[lower]
	if !ok {
		fi, ok = iconByExt[strings.ToLower(filepath.Ext(name))]
	}
	if !ok && strings.HasPrefix(lower, ".env") {
		fi, ok = iconByName[".env"]
	}
	if !ok && strings.HasPrefix(lower, "dockerfile") {
		fi, ok = iconByName["dockerfile"]
	}
	if !ok {
		fi = fileIcon{'', "white"}
	}
	col, _ := parseHex(setiColors[fi.color])
	if !a.nerdIcons {
		return "", col
	}
	return string(fi.glyph), col
}

func parseHex(s string) (tcell.Color, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return tcell.ColorDefault, false
	}
	var v int32
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= c - '0'
		case c >= 'a' && c <= 'f':
			v |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v |= c - 'A' + 10
		}
	}
	return tcell.NewHexColor(v), true
}

// kindIcon returns the completion/symbol kind glyph and color key.
func (a *App) kindIcon(kind int, symbol bool) (string, string) {
	type ki struct {
		nerd, plain, color string
	}
	var m map[int]ki
	if symbol {
		// LSP SymbolKind
		m = map[int]ki{
			1: {"", "f", "symbolIcon.fileForeground"}, 2: {"", "M", "symbolIcon.moduleForeground"},
			3: {"", "N", "symbolIcon.moduleForeground"}, 4: {"", "P", "symbolIcon.moduleForeground"},
			5: {"", "C", "symbolIcon.classForeground"}, 6: {"", "m", "symbolIcon.methodForeground"},
			7: {"", "p", "symbolIcon.propertyForeground"}, 8: {"", "f", "symbolIcon.fieldForeground"},
			9: {"", "c", "symbolIcon.methodForeground"}, 10: {"", "E", "symbolIcon.enumeratorForeground"},
			11: {"", "I", "symbolIcon.interfaceForeground"}, 12: {"", "ƒ", "symbolIcon.functionForeground"},
			13: {"", "v", "symbolIcon.variableForeground"}, 14: {"", "c", "symbolIcon.constantForeground"},
			15: {"", "s", "symbolIcon.textForeground"}, 16: {"", "#", "symbolIcon.constantForeground"},
			17: {"", "b", "symbolIcon.constantForeground"}, 18: {"", "a", "symbolIcon.variableForeground"},
			19: {"", "o", "symbolIcon.variableForeground"}, 20: {"", "k", "symbolIcon.keywordForeground"},
			21: {"", "n", "symbolIcon.constantForeground"}, 22: {"", "e", "symbolIcon.enumeratorMemberForeground"},
			23: {"", "S", "symbolIcon.structForeground"}, 24: {"", "e", "symbolIcon.eventForeground"},
			25: {"", "o", "symbolIcon.keywordForeground"}, 26: {"", "T", "symbolIcon.typeParameterForeground"},
		}
	} else {
		m = map[int]ki{
			1: {"", "t", "symbolIcon.textForeground"}, 2: {"", "m", "symbolIcon.methodForeground"},
			3: {"", "ƒ", "symbolIcon.functionForeground"}, 4: {"", "c", "symbolIcon.methodForeground"},
			5: {"", "f", "symbolIcon.fieldForeground"}, 6: {"", "v", "symbolIcon.variableForeground"},
			7: {"", "C", "symbolIcon.classForeground"}, 8: {"", "I", "symbolIcon.interfaceForeground"},
			9: {"", "M", "symbolIcon.moduleForeground"}, 10: {"", "p", "symbolIcon.propertyForeground"},
			11: {"", "u", "symbolIcon.constantForeground"}, 12: {"", "#", "symbolIcon.constantForeground"},
			13: {"", "E", "symbolIcon.enumeratorForeground"}, 14: {"", "k", "symbolIcon.keywordForeground"},
			15: {"", "s", "symbolIcon.snippetForeground"}, 16: {"", "c", "symbolIcon.colorForeground"},
			17: {"", "f", "symbolIcon.fileForeground"}, 18: {"", "r", "symbolIcon.fileForeground"},
			19: {"", "d", "symbolIcon.folderForeground"}, 20: {"", "e", "symbolIcon.enumeratorMemberForeground"},
			21: {"", "c", "symbolIcon.constantForeground"}, 22: {"", "S", "symbolIcon.structForeground"},
			23: {"", "e", "symbolIcon.eventForeground"}, 24: {"", "o", "symbolIcon.keywordForeground"},
			25: {"", "T", "symbolIcon.typeParameterForeground"},
		}
	}
	k, ok := m[kind]
	if !ok {
		k = ki{"", "·", "symbolIcon.textForeground"}
	}
	if a.nerdIcons {
		return k.nerd, k.color
	}
	return k.plain, k.color
}
