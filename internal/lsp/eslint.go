package lsp

import (
	"os"
	"path/filepath"
	"strings"

	"tuicode/internal/syntax"
)

var eslintLangs = map[string]bool{"javascript": true, "javascriptreact": true, "typescript": true, "typescriptreact": true, "vue": true, "svelte": true}

var eslintConfigFiles = []string{"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs", "eslint.config.ts", "eslint.config.mts",
	".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.json", ".eslintrc.yml", ".eslintrc.yaml"}

// eslintRoot returns the folder with the ESLint configuration that applies to path.
func eslintRoot(path string) (root string, flat bool, ok bool) {
	for d := filepath.Dir(path); filepath.Dir(d) != d; d = filepath.Dir(d) {
		for _, f := range eslintConfigFiles {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				return d, strings.HasPrefix(f, "eslint.config"), true
			}
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return "", false, false
		}
	}
	return "", false, false
}

// secondaryServers returns linters that run next to the main server, like VS Code's ESLint extension.
func (m *Manager) secondaryServers(lang *syntax.Language, path string) [][]string {
	if lang == nil || !eslintLangs[lang.ID] || m.isDenoFile(path) {
		return nil
	}
	if cfg, ok := m.config("eslint").(map[string]any); ok {
		if on, ok := cfg["enable"].(bool); ok && !on {
			return nil
		}
	}
	if _, _, ok := eslintRoot(path); !ok {
		return nil
	}
	if p, ok := m.LookPath("vscode-eslint-language-server"); ok {
		return [][]string{{p, "--stdio"}}
	}
	return nil
}

func isESLint(c *Client) bool { return strings.HasPrefix(c.Name, "vscode-eslint") }

// eslintSettings are the settings the ESLint server asks for, as VS Code's extension provides them.
func (m *Manager) eslintSettings(scopeURI string) map[string]any {
	path := URIToPath(scopeURI)
	root, flat, _ := eslintRoot(path)
	if root == "" {
		root = m.Root
	}
	settings := map[string]any{
		"validate":            "on",
		"packageManager":      "npm",
		"useESLintClass":      false,
		"useFlatConfig":       flat,
		"experimental":        map[string]any{"useFlatConfig": flat},
		"codeActionOnSave":    map[string]any{"enable": false, "mode": "all"},
		"format":              false,
		"quiet":               false,
		"onIgnoredFiles":      "off",
		"rulesCustomizations": []any{},
		"run":                 "onType",
		"problems":            map[string]any{"shortenToSingleLine": false},
		"nodePath":            nil,
		"workingDirectory":    map[string]any{"mode": "location"},
		"codeAction": map[string]any{
			"disableRuleComment": map[string]any{"enable": true, "location": "separateLine"},
			"showDocumentation":  map[string]any{"enable": true},
		},
		"workspaceFolder": map[string]any{"uri": PathToURI(root), "name": filepath.Base(root)},
	}
	// user settings like "eslint.run" or "eslint.quiet" win
	if user, ok := m.config("eslint").(map[string]any); ok {
		for k, v := range user {
			settings[k] = v
		}
	}
	return settings
}
