package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Dir returns the per-user configuration directory (~/.tuicode, %APPDATA%\tuicode on Windows).
func Dir() string {
	if runtime.GOOS == "windows" {
		if app := os.Getenv("APPDATA"); app != "" {
			return filepath.Join(app, "tuicode")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tuicode")
}

// UserSettingsPath is the user's settings.json.
func UserSettingsPath() string { return filepath.Join(Dir(), "settings.json") }

// UserKeybindingsPath is the user's keybindings.json.
func UserKeybindingsPath() string { return filepath.Join(Dir(), "keybindings.json") }

// ThemesDir holds user themes.
func ThemesDir() string { return filepath.Join(Dir(), "themes") }

// VSCodeUserDir returns the VS Code user settings directory.
func VSCodeUserDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Code", "User")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User")
	}
	return filepath.Join(home, ".config", "Code", "User")
}

// Defaults are the built-in setting values.
var Defaults = map[string]any{
	"editor.tabSize":                                          4.0,
	"editor.insertSpaces":                                     true,
	"editor.detectIndentation":                                true,
	"editor.wordWrap":                                         "off",
	"editor.wordWrapColumn":                                   80.0,
	"editor.lineNumbers":                                      "on",
	"editor.renderWhitespace":                                 "selection",
	"editor.renderControlCharacters":                          true,
	"editor.minimap.enabled":                                  true,
	"editor.minimap.maxColumn":                                120.0,
	"editor.minimap.renderCharacters":                         true,
	"editor.minimap.showSlider":                               "mouseover",
	"editor.cursorStyle":                                      "line",
	"editor.cursorBlinking":                                   "blink",
	"editor.autoClosingBrackets":                              "languageDefined",
	"editor.linkedEditing":                                    false,
	"editor.codeLens":                                         true,
	"terminal.integrated.shellIntegration.enabled":            true,
	"terminal.integrated.shellIntegration.decorationsEnabled": "both",
	"typescript.updateImportsOnFileMove.enabled":              "prompt",
	"debug.inlineValues":                                      "auto",
	"merge-conflict.codeLens.enabled":                         true,
	"merge-conflict.decorators.enabled":                       true,
	"git.blame.editorDecoration.enabled":                      false,
	"git.blame.editorDecoration.template":                     "${subject}, ${authorName} (${authorDateAgo})",
	"git.blame.statusBarItem.enabled":                         false,
	"git.blame.statusBarItem.template":                        "${authorName} (${authorDateAgo})",
	"workbench.editor.preventPinnedEditorClose":               "keyboardAndMouse",
	"html.autoClosingTags":                                    true,
	"javascript.autoClosingTags":                              true,
	"typescript.autoClosingTags":                              true,
	"editor.autoClosingQuotes":                                "languageDefined",
	"editor.autoSurround":                                     "languageDefined",
	"editor.autoIndent":                                       "full",
	"editor.formatOnSave":                                     false,
	"editor.formatOnPaste":                                    false,
	"editor.formatOnType":                                     false,
	"editor.rulers":                                           []any{},
	"editor.renderLineHighlight":                              "line",
	"editor.bracketPairColorization.enabled":                  true,
	"editor.guides.indentation":                               true,
	"editor.guides.bracketPairs":                              false,
	"editor.stickyScroll.enabled":                             true,
	"editor.stickyScroll.maxLineCount":                        5.0,
	"editor.scrollBeyondLastLine":                             true,
	"editor.cursorSurroundingLines":                           0.0,
	"editor.mouseWheelScrollSensitivity":                      1.0,
	"editor.quickSuggestions":                                 true,
	"editor.quickSuggestionsDelay":                            10.0,
	"editor.suggestOnTriggerCharacters":                       true,
	"editor.acceptSuggestionOnEnter":                          "on",
	"editor.wordBasedSuggestions":                             "matchingDocuments",
	"editor.suggestSelection":                                 "first",
	"editor.snippetSuggestions":                               "inline",
	"editor.parameterHints.enabled":                           true,
	"editor.inlayHints.enabled":                               "on",
	"editor.hover.enabled":                                    true,
	"editor.hover.delay":                                      300.0,
	"editor.occurrencesHighlight":                             "singleFile",
	"editor.selectionHighlight":                               true,
	"editor.matchBrackets":                                    "always",
	"editor.folding":                                          true,
	"editor.foldingStrategy":                                  "auto",
	"editor.showFoldingControls":                              "mouseover",
	"editor.trimAutoWhitespace":                               true,
	"editor.emptySelectionClipboard":                          true,
	"editor.copyWithSyntaxHighlighting":                       false,
	"editor.multiCursorModifier":                              "alt",
	"editor.dragAndDrop":                                      true,
	"editor.links":                                            true,
	"editor.lightbulb.enabled":                                "onCode",
	"editor.codeActionsOnSave":                                map[string]any{},
	"editor.defaultFormatter":                                 nil,
	"editor.glyphMargin":                                      true,
	"editor.semanticHighlighting.enabled":                     true,
	"editor.smoothScrolling":                                  false,
	"editor.unicodeHighlight.ambiguousCharacters":             true,
	"files.trimTrailingWhitespace":                            false,
	"files.insertFinalNewline":                                false,
	"files.trimFinalNewlines":                                 false,
	"files.autoSave":                                          "off",
	"files.autoSaveDelay":                                     1000.0,
	"files.eol":                                               "auto",
	"files.encoding":                                          "utf8",
	"files.hotExit":                                           "onExit",
	"files.exclude": map[string]any{
		"**/.git": true, "**/.svn": true, "**/.hg": true, "**/CVS": true, "**/.DS_Store": true, "**/Thumbs.db": true,
	},
	"files.watcherExclude": map[string]any{},
	"files.associations":   map[string]any{},
	"search.exclude": map[string]any{
		"**/node_modules": true, "**/bower_components": true, "**/*.code-search": true,
	},
	"search.useIgnoreFiles":                      true,
	"search.smartCase":                           false,
	"workbench.colorTheme":                       "Dark Modern",
	"workbench.iconTheme":                        "auto",
	"workbench.sideBar.location":                 "left",
	"workbench.activityBar.location":             "default",
	"workbench.statusBar.visible":                true,
	"workbench.editor.showTabs":                  "multiple",
	"workbench.editor.enablePreview":             true,
	"workbench.editor.highlightModifiedTabs":     false,
	"workbench.editor.tabActionLocation":         "right",
	"workbench.editor.wrapTabs":                  false,
	"workbench.startupEditor":                    "welcomePage",
	"workbench.tree.indent":                      8.0,
	"workbench.tree.renderIndentGuides":          "onHover",
	"workbench.panel.defaultLocation":            "bottom",
	"workbench.list.smoothScrolling":             false,
	"window.menuBarVisibility":                   "classic",
	"window.title":                               "${dirty}${activeEditorShort}${separator}${rootName}${separator}${appName}",
	"breadcrumbs.enabled":                        true,
	"explorer.confirmDelete":                     true,
	"explorer.confirmDragAndDrop":                true,
	"explorer.compactFolders":                    true,
	"explorer.sortOrder":                         "default",
	"explorer.autoReveal":                        true,
	"explorer.openEditors.visible":               0.0,
	"git.enabled":                                true,
	"git.decorations.enabled":                    true,
	"git.autorefresh":                            true,
	"git.confirmSync":                            true,
	"git.enableSmartCommit":                      false,
	"scm.diffDecorations":                        "all",
	"problems.decorations.enabled":               true,
	"terminal.integrated.shell.linux":            "",
	"terminal.integrated.defaultProfile.linux":   "",
	"terminal.integrated.cwd":                    "",
	"terminal.integrated.scrollback":             1000.0,
	"terminal.integrated.cursorStyle":            "block",
	"terminal.integrated.cursorBlinking":         false,
	"terminal.integrated.copyOnSelection":        false,
	"terminal.integrated.rightClickBehavior":     "default",
	"terminal.integrated.env.linux":              map[string]any{},
	"terminal.integrated.sendKeybindingsToShell": false,
	"terminal.integrated.commandsToSkipShell":    []any{},
	"tuicode.importVSCodeSettings":               true,
	"tuicode.importVSCodeKeybindings":            true,
	"tuicode.lsp.enabled":                        true,
	"tuicode.lsp.servers":                        map[string]any{},
	"tuicode.lsp.promptInstall":                  true,
	"tuicode.suggestKittyConfig":                 true,
	"tuicode.mouse":                              true,
	"tuicode.titleBar":                           true,
	"tuicode.truecolor":                          "auto",
	"tuicode.icons":                              "auto",
	// language defaults contributed by VS Code and its common extensions
	"[go]":         map[string]any{"editor.insertSpaces": false, "editor.formatOnSave": true, "editor.codeActionsOnSave": map[string]any{"source.organizeImports": "explicit"}},
	"[makefile]":   map[string]any{"editor.insertSpaces": false},
	"[markdown]":   map[string]any{"editor.wordWrap": "on", "editor.quickSuggestions": false},
	"[git-commit]": map[string]any{"editor.rulers": []any{72.0}},
	"[plaintext]":  map[string]any{"editor.quickSuggestions": false},
}

// Settings is a layered view over default, imported, user and workspace settings.
type Settings struct {
	layers []map[string]any // lowest priority first
	// LayerPaths are the files that were loaded, for display.
	LayerPaths []string
	Errors     []string
}

func loadJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return m, nil
	}
	if err := UnmarshalJSONC(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// LoadSettings reads all settings layers for a workspace.
func LoadSettings(workspace string) *Settings {
	return LoadWorkspaceSettings(workspace, nil)
}

// LoadWorkspaceSettings is LoadSettings with a multi-root workspace's settings layered between
// the user settings and the folder settings.
func LoadWorkspaceSettings(workspace string, ws *Workspace) *Settings {
	s := &Settings{layers: []map[string]any{Defaults}}
	add := func(path string) {
		m, err := loadJSONFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				s.Errors = append(s.Errors, path+": "+err.Error())
			}
			return
		}
		s.layers = append(s.layers, m)
		s.LayerPaths = append(s.LayerPaths, path)
	}
	user, userErr := loadJSONFile(UserSettingsPath())
	importVS := true
	if userErr == nil {
		if v, ok := user["tuicode.importVSCodeSettings"].(bool); ok {
			importVS = v
		}
	}
	if importVS {
		add(filepath.Join(VSCodeUserDir(), "settings.json"))
	}
	add(UserSettingsPath())
	if ws != nil && ws.Settings != nil {
		s.layers = append(s.layers, ws.Settings)
		s.LayerPaths = append(s.LayerPaths, ws.File)
	}
	if workspace != "" {
		add(filepath.Join(workspace, ".vscode", "settings.json"))
		add(filepath.Join(workspace, ".tuicode", "settings.json"))
	}
	return s
}

// Get returns the effective value of a setting. Like VS Code, a language-specific
// value ("[go]": {...}) from any layer wins over a plain value, then later layers win.
func (s *Settings) Get(key string, lang string) any {
	if lang != "" {
		for i := len(s.layers) - 1; i >= 0; i-- {
			if ov, ok := s.layers[i]["["+lang+"]"].(map[string]any); ok {
				if v, ok := ov[key]; ok {
					return v
				}
			}
		}
	}
	for i := len(s.layers) - 1; i >= 0; i-- {
		if v, ok := s.layers[i][key]; ok {
			return v
		}
	}
	// nested object form, e.g. "editor": {"tabSize": 2}
	if i := strings.LastIndex(key, "."); i > 0 {
		if parent, ok := s.Get(key[:i], lang).(map[string]any); ok {
			return parent[key[i+1:]]
		}
	}
	return nil
}

// Bool returns a boolean setting.
func (s *Settings) Bool(key string, lang string) bool {
	switch v := s.Get(key, lang).(type) {
	case bool:
		return v
	case string:
		return v == "on" || v == "true" || v == "always"
	}
	return false
}

// Int returns a numeric setting.
func (s *Settings) Int(key string, lang string, def int) int {
	switch v := s.Get(key, lang).(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

// String returns a string setting.
func (s *Settings) String(key string, lang string) string {
	switch v := s.Get(key, lang).(type) {
	case string:
		return v
	case bool:
		if v {
			return "on"
		}
		return "off"
	}
	return ""
}

// Map returns an object setting merged across all layers.
func (s *Settings) Map(key string) map[string]any {
	out := map[string]any{}
	for _, l := range s.layers {
		if m, ok := l[key].(map[string]any); ok {
			for k, v := range m {
				out[k] = v
			}
		}
	}
	return out
}

// List returns an array setting.
func (s *Settings) List(key string, lang string) []any {
	if v, ok := s.Get(key, lang).([]any); ok {
		return v
	}
	return nil
}

// Section returns all settings that start with prefix (for LSP workspace/configuration).
func (s *Settings) Section(prefix string) map[string]any {
	out := map[string]any{}
	for _, l := range s.layers {
		for k, v := range l {
			if prefix == "" || strings.HasPrefix(k, prefix+".") {
				rel := strings.TrimPrefix(strings.TrimPrefix(k, prefix), ".")
				setNested(out, rel, v)
			}
			if k == prefix {
				if m, ok := v.(map[string]any); ok {
					for mk, mv := range m {
						setNested(out, mk, mv)
					}
				}
			}
		}
	}
	return out
}

func setNested(m map[string]any, key string, v any) {
	parts := strings.Split(key, ".")
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

// SetUser writes a setting into the user's settings.json, preserving other content.
func SetUser(key string, value any) error {
	path := UserSettingsPath()
	m, err := loadJSONFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		m = map[string]any{}
	}
	if value == nil {
		delete(m, key)
	} else {
		m[key] = value
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// EnsureUserFiles creates empty user settings and keybindings files if missing.
func EnsureUserFiles() {
	_ = os.MkdirAll(Dir(), 0o755)
	if _, err := os.Stat(UserSettingsPath()); os.IsNotExist(err) {
		_ = os.WriteFile(UserSettingsPath(), []byte("{\n  // Place your settings here. VS Code settings are imported automatically\n  // unless \"tuicode.importVSCodeSettings\" is false.\n}\n"), 0o644)
	}
	if _, err := os.Stat(UserKeybindingsPath()); os.IsNotExist(err) {
		_ = os.WriteFile(UserKeybindingsPath(), []byte("// Place your key bindings in this file to override the defaults\n[\n]\n"), 0o644)
	}
}
