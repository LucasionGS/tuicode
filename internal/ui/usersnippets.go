package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"tuicode/internal/config"
	"tuicode/internal/lsp"
)

type userSnippet struct {
	name   string
	prefix string
	body   string
	desc   string
	scopes map[string]bool // nil = all languages
}

type snippetCache struct {
	byLang map[string][]userSnippet
	global []userSnippet
	loaded time.Time
	mtimes map[string]time.Time
}

func snippetDirs(root string) []string {
	dirs := []string{filepath.Join(config.VSCodeUserDir(), "snippets"), filepath.Join(config.Dir(), "snippets")}
	if root != "" {
		dirs = append(dirs, filepath.Join(root, ".vscode"))
	}
	return dirs
}

// loadUserSnippets reads VS Code style snippet files (cached, reloaded when they change).
func (a *App) loadUserSnippets() *snippetCache {
	c := a.snippets
	if c != nil && time.Since(c.loaded) < 5*time.Second {
		return c
	}
	changed := c == nil
	files := map[string]time.Time{}
	for _, dir := range snippetDirs(a.root) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if !(strings.HasSuffix(n, ".json") && !strings.Contains(dir, ".vscode")) && !strings.HasSuffix(n, ".code-snippets") {
				continue
			}
			p := filepath.Join(dir, n)
			if st, err := os.Stat(p); err == nil {
				files[p] = st.ModTime()
				if c == nil || !c.mtimes[p].Equal(st.ModTime()) {
					changed = true
				}
			}
		}
	}
	if c != nil && len(c.mtimes) != len(files) {
		changed = true
	}
	if !changed {
		c.loaded = time.Now()
		return c
	}
	nc := &snippetCache{byLang: map[string][]userSnippet{}, mtimes: files, loaded: time.Now()}
	for p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var raw map[string]struct {
			Prefix      any    `json:"prefix"`
			Body        any    `json:"body"`
			Description string `json:"description"`
			Scope       string `json:"scope"`
		}
		if config.UnmarshalJSONC(data, &raw) != nil {
			continue
		}
		lang := ""
		if strings.HasSuffix(p, ".json") {
			lang = strings.TrimSuffix(filepath.Base(p), ".json")
		}
		for name, s := range raw {
			var body string
			switch b := s.Body.(type) {
			case string:
				body = b
			case []any:
				var lines []string
				for _, l := range b {
					if ls, ok := l.(string); ok {
						lines = append(lines, ls)
					}
				}
				body = strings.Join(lines, "\n")
			}
			var prefixes []string
			switch pf := s.Prefix.(type) {
			case string:
				prefixes = []string{pf}
			case []any:
				for _, x := range pf {
					if xs, ok := x.(string); ok {
						prefixes = append(prefixes, xs)
					}
				}
			}
			var scopes map[string]bool
			if s.Scope != "" {
				scopes = map[string]bool{}
				for _, sc := range strings.Split(s.Scope, ",") {
					scopes[strings.TrimSpace(sc)] = true
				}
			}
			for _, pf := range prefixes {
				us := userSnippet{name: name, prefix: pf, body: body, desc: s.Description, scopes: scopes}
				if lang != "" {
					nc.byLang[lang] = append(nc.byLang[lang], us)
				} else {
					nc.global = append(nc.global, us)
				}
			}
		}
	}
	a.snippets = nc
	return nc
}

// snippetCompletions returns completion items for user snippets of a language.
func (a *App) snippetCompletions(lang string) []*citem {
	if a.settings.String("editor.snippetSuggestions", lang) == "none" {
		return nil
	}
	c := a.loadUserSnippets()
	var out []*citem
	add := func(s userSnippet) {
		if s.scopes != nil && !s.scopes[lang] {
			return
		}
		desc := s.desc
		if desc == "" {
			desc = s.name
		}
		item := &lsp.CompletionItem{Label: s.prefix, InsertText: s.body, InsertTextFormat: 2, Detail: desc, Kind: lsp.CIKSnippet}
		docs := "```" + lang + "\n" + s.body + "\n```"
		out = append(out, &citem{item: item, label: s.prefix, filter: s.prefix, sortText: s.prefix, kind: lsp.CIKSnippet, detail: desc, docs: docs, resolved: true})
	}
	for _, s := range c.byLang[lang] {
		add(s)
	}
	// typescriptreact also uses typescript snippets, like VS Code's language inheritance for JSX
	if lang == "typescriptreact" {
		for _, s := range c.byLang["typescript"] {
			add(s)
		}
	}
	for _, s := range c.global {
		add(s)
	}
	return out
}

func (a *App) configureSnippets() {
	lang := "global"
	if v := a.ActiveView(); v != nil {
		lang = v.doc.Lang.ID
	}
	dir := filepath.Join(config.Dir(), "snippets")
	items := []QuickItem{
		{Label: lang + ".json", Description: "New snippets for " + lang, Data: filepath.Join(dir, lang+".json")},
		{Label: "global.code-snippets", Description: "New global snippets file", Data: filepath.Join(dir, "global.code-snippets")},
	}
	vsdir := filepath.Join(config.VSCodeUserDir(), "snippets")
	if entries, err := os.ReadDir(vsdir); err == nil {
		for _, e := range entries {
			items = append(items, QuickItem{Label: e.Name(), Description: "VS Code user snippets", Data: filepath.Join(vsdir, e.Name()), Group: "existing"})
		}
	}
	a.openQuick(&QuickPick{Placeholder: "Select Snippets File or Create Snippets", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		p := it.Data.(string)
		if !fileExists(p) {
			_ = fsMkdirAll(filepath.Dir(p), 0o755)
			_ = fsWriteFile(p, []byte(snippetTemplate), 0o644)
		}
		a.OpenFile(p, false)
		a.focusEditor()
	}})
}

const snippetTemplate = `{
	// Place your snippets here. Each snippet is defined under a snippet name and has a prefix, body and
	// description. The prefix is what is used to trigger the snippet and the body will be expanded and inserted.
	// Example:
	// "Print to console": {
	// 	"prefix": "log",
	// 	"body": [
	// 		"console.log('$1');",
	// 		"$2"
	// 	],
	// 	"description": "Log output to console"
	// }
}
`

func (a *App) insertSnippetPicker(v *EditorView) {
	snips := a.snippetCompletions(v.lang())
	if len(snips) == 0 {
		a.setStatusMessage("No snippets for " + v.doc.Lang.Name + ". Use \"Snippets: Configure Snippets\" to add some.")
		return
	}
	var items []QuickItem
	for _, s := range snips {
		items = append(items, QuickItem{Label: s.label, Description: s.detail, Data: s.item.InsertText})
	}
	a.openQuick(&QuickPick{Placeholder: "Select a snippet", Items: items, MatchDescription: true, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			v.insertSnippet(v.Primary().Range(), it.Data.(string))
		}
	}})
}
