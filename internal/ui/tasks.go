package ui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"tuicode/internal/config"
)

type task struct {
	Label     string
	Command   string
	Cwd       string
	Group     string
	IsDefault bool
	Source    string
	Matchers  []*problemMatcher
}

type tasksFile struct {
	Tasks []struct {
		Label    string          `json:"label"`
		TaskName string          `json:"taskName"`
		Type     string          `json:"type"`
		Command  string          `json:"command"`
		Script   string          `json:"script"`
		Args     []any           `json:"args"`
		Group    json.RawMessage `json:"group"`
		Options  struct {
			Cwd string `json:"cwd"`
		} `json:"options"`
		Linux *struct {
			Command string `json:"command"`
		} `json:"linux"`
		ProblemMatcher json.RawMessage `json:"problemMatcher"`
	} `json:"tasks"`
}

// substituteVars expands VS Code ${...} variables.
func (a *App) substituteVars(s string) string {
	v := a.ActiveView()
	file, dir, base, rel, noExt, line, sel := "", "", "", "", "", "", ""
	if v != nil && v.buf().Path != "" {
		file = v.buf().Path
		dir = filepath.Dir(file)
		base = filepath.Base(file)
		rel = a.relPath(file)
		noExt = strings.TrimSuffix(base, filepath.Ext(base))
		line = strconv.Itoa(v.Cursor().Line + 1)
		sel = v.SelectedText()
	}
	home, _ := os.UserHomeDir()
	r := strings.NewReplacer(
		"${workspaceFolder}", a.root,
		"${workspaceRoot}", a.root,
		"${workspaceFolderBasename}", filepath.Base(a.root),
		"${file}", file,
		"${fileDirname}", dir,
		"${fileBasename}", base,
		"${relativeFile}", rel,
		"${fileBasenameNoExtension}", noExt,
		"${fileExtname}", filepath.Ext(file),
		"${lineNumber}", line,
		"${selectedText}", sel,
		"${userHome}", home,
		"${cwd}", a.root,
		"${pathSeparator}", string(filepath.Separator),
	)
	s = r.Replace(s)
	envRe := regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)
	return envRe.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(envRe.FindStringSubmatch(m)[1])
	})
}

// collectTasks gathers tasks.json tasks plus auto-detected ones.
func (a *App) collectTasks() []task {
	folders := a.folders()
	var out []task
	seen := map[string]bool{}
	for _, f := range folders {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		for _, t := range a.collectTasksIn(f.Path) {
			// a folder's own ${workspaceFolder} is that folder
			t.Command = strings.ReplaceAll(t.Command, "${workspaceFolder}", f.Path)
			t.Cwd = strings.ReplaceAll(t.Cwd, "${workspaceFolder}", f.Path)
			if t.Cwd == "" {
				t.Cwd = f.Path
			}
			if len(folders) > 1 {
				t.Label += " - " + f.Name
			}
			out = append(out, t)
		}
	}
	return out
}

// collectTasksIn finds the tasks.json tasks and detected tasks of one folder.
func (a *App) collectTasksIn(root string) []task {
	var out []task
	if data, err := os.ReadFile(filepath.Join(root, ".vscode", "tasks.json")); err == nil {
		var tf tasksFile
		if err := config.UnmarshalJSONC(data, &tf); err == nil {
			for _, t := range tf.Tasks {
				label := t.Label
				if label == "" {
					label = t.TaskName
				}
				cmd := t.Command
				if t.Linux != nil && t.Linux.Command != "" {
					cmd = t.Linux.Command
				}
				if t.Type == "npm" && t.Script != "" {
					cmd = "npm run " + t.Script
					if label == "" {
						label = "npm: " + t.Script
					}
				}
				for _, arg := range t.Args {
					switch x := arg.(type) {
					case string:
						cmd += " " + shellQuote(x)
					case map[string]any:
						if v, ok := x["value"].(string); ok {
							cmd += " " + shellQuote(v)
						}
					}
				}
				tk := task{Label: label, Command: cmd, Cwd: t.Options.Cwd, Source: "tasks.json", Matchers: parseProblemMatchers(t.ProblemMatcher)}
				var gs string
				if json.Unmarshal(t.Group, &gs) == nil {
					tk.Group = gs
				} else {
					var gobj struct {
						Kind      string `json:"kind"`
						IsDefault any    `json:"isDefault"`
					}
					if json.Unmarshal(t.Group, &gobj) == nil {
						tk.Group = gobj.Kind
						if b, ok := gobj.IsDefault.(bool); ok && b {
							tk.IsDefault = true
						}
					}
				}
				if tk.Command != "" {
					out = append(out, tk)
				}
			}
		}
	}
	// package.json scripts
	if data, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			runner := "npm run"
			if fileExists(filepath.Join(root, "bun.lockb")) || fileExists(filepath.Join(root, "bun.lock")) {
				runner = "bun run"
			} else if fileExists(filepath.Join(root, "pnpm-lock.yaml")) {
				runner = "pnpm run"
			} else if fileExists(filepath.Join(root, "yarn.lock")) {
				runner = "yarn"
			}
			var names []string
			for n := range pkg.Scripts {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				g := ""
				switch n {
				case "build", "compile":
					g = "build"
				case "test":
					g = "test"
				case "start", "dev":
					g = "run"
				}
				out = append(out, task{Label: "npm: " + n, Command: runner + " " + n, Group: g, Source: "package.json"})
			}
		}
	}
	// deno.json tasks
	for _, f := range []string{"deno.json", "deno.jsonc"} {
		if data, err := os.ReadFile(filepath.Join(root, f)); err == nil {
			var dj struct {
				Tasks map[string]any `json:"tasks"`
			}
			if config.UnmarshalJSONC(data, &dj) == nil {
				var names []string
				for n := range dj.Tasks {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					g := ""
					switch n {
					case "build":
						g = "build"
					case "test":
						g = "test"
					case "dev", "start":
						g = "run"
					}
					out = append(out, task{Label: "deno: " + n, Command: "deno task " + n, Group: g, Source: f})
				}
			}
		}
	}
	// Makefile targets
	if data, err := os.ReadFile(filepath.Join(root, "Makefile")); err == nil {
		re := regexp.MustCompile(`(?m)^([A-Za-z0-9_][A-Za-z0-9_.-]*)\s*:(?:[^=]|$)`)
		seen := map[string]bool{}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			n := m[1]
			if seen[n] || strings.HasPrefix(n, ".") {
				continue
			}
			seen[n] = true
			g := ""
			switch n {
			case "build", "all":
				g = "build"
			case "test":
				g = "test"
			case "run":
				g = "run"
			}
			out = append(out, task{Label: "make: " + n, Command: "make " + n, Group: g, Source: "Makefile", Matchers: []*problemMatcher{builtinMatchers["gcc"], builtinMatchers["go"]}})
		}
	}
	if fileExists(filepath.Join(root, "go.mod")) {
		goM := []*problemMatcher{builtinMatchers["go"]}
		out = append(out,
			task{Label: "go: build", Command: "go build ./...", Group: "build", Source: "go", Matchers: goM},
			task{Label: "go: test", Command: "go test ./...", Group: "test", Source: "go", Matchers: goM},
			task{Label: "go: run", Command: "go run .", Group: "run", Source: "go", Matchers: goM},
			task{Label: "go: vet", Command: "go vet ./...", Source: "go", Matchers: goM},
		)
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		rustM := []*problemMatcher{builtinMatchers["rustc"]}
		out = append(out,
			task{Label: "cargo: build", Command: "cargo build", Group: "build", Source: "cargo", Matchers: rustM},
			task{Label: "cargo: test", Command: "cargo test", Group: "test", Source: "cargo", Matchers: rustM},
			task{Label: "cargo: run", Command: "cargo run", Group: "run", Source: "cargo", Matchers: rustM},
		)
	}
	if fileExists(filepath.Join(root, "pubspec.yaml")) {
		out = append(out,
			task{Label: "dart: run", Command: "dart run", Group: "run", Source: "dart"},
			task{Label: "dart: test", Command: "dart test", Group: "test", Source: "dart"},
		)
	}
	return out
}

func (a *App) runTask(t task) {
	a.runTaskThen(t, nil)
}

// runTaskThen runs a task and calls done with its exit code when it ends.
func (a *App) runTaskThen(t task, done func(exitCode int)) {
	cwd := a.root
	if t.Cwd != "" {
		cwd = a.substituteVars(t.Cwd)
	}
	cmd := a.substituteVars(t.Command)
	a.terminals.runTask(t, cmd, cwd, done)
	a.showPanel("terminal")
	a.output.append("Tasks", "Running task '"+t.Label+"': "+cmd)
}

// runTaskPicker shows tasks, running the default task of a group directly.
func (a *App) runTaskPicker(group string) {
	tasks := a.collectTasks()
	if group == "run" {
		// F5 with no workspace runner: run the active file
		var runners []task
		for _, t := range tasks {
			if t.Group == "run" {
				runners = append(runners, t)
			}
		}
		if len(runners) == 0 {
			if t, ok := a.runFileTask(); ok {
				a.runTask(t)
				return
			}
		}
	}
	var filtered []task
	for _, t := range tasks {
		if group == "" || t.Group == group {
			if t.IsDefault && group != "" {
				a.runTask(t)
				return
			}
			filtered = append(filtered, t)
		}
	}
	if group != "" && len(filtered) == 1 {
		a.runTask(filtered[0])
		return
	}
	if len(filtered) == 0 {
		filtered = tasks
	}
	if len(filtered) == 0 {
		a.notify(NotifyInfo, "No tasks found. Create .vscode/tasks.json to define tasks.", NotifyAction{Label: "Create tasks.json", Run: func() {
			p := filepath.Join(a.root, ".vscode", "tasks.json")
			if !fileExists(p) {
				_ = fsMkdirAll(filepath.Dir(p), 0o755)
				_ = fsWriteFile(p, []byte(tasksTemplate), 0o644)
			}
			a.OpenFile(p, false)
		}})
		return
	}
	var items []QuickItem
	lastSrc := ""
	for _, t := range filtered {
		it := QuickItem{Label: t.Label, Description: t.Command, Data: t, Icon: a.Icon(IcoTerminal)}
		if t.Source != lastSrc {
			it.Group = t.Source
			lastSrc = t.Source
		}
		items = append(items, it)
	}
	title := "Select the task to run"
	if group == "build" {
		title = "Select the build task to run"
	}
	a.openQuick(&QuickPick{Placeholder: title, Items: items, KeepOrder: true, MatchDescription: true, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			a.runTask(it.Data.(task))
		}
	}})
}

func (a *App) runFileTask() (task, bool) {
	v := a.ActiveView()
	if v == nil || v.buf().Path == "" {
		return task{}, false
	}
	p := shellQuote(v.buf().Path)
	switch v.doc.Lang.ID {
	case "go":
		return task{Label: "run " + v.doc.Title(), Command: "go run " + p}, true
	case "python":
		return task{Label: "run " + v.doc.Title(), Command: "python3 " + p}, true
	case "javascript":
		return task{Label: "run " + v.doc.Title(), Command: "node " + p}, true
	case "typescript":
		if _, err := exec.LookPath("deno"); err == nil {
			return task{Label: "run " + v.doc.Title(), Command: "deno run -A " + p}, true
		}
		return task{Label: "run " + v.doc.Title(), Command: "npx tsx " + p}, true
	case "shellscript":
		return task{Label: "run " + v.doc.Title(), Command: "bash " + p}, true
	case "rust":
		return task{Label: "cargo run", Command: "cargo run"}, true
	case "dart":
		return task{Label: "run " + v.doc.Title(), Command: "dart run " + p}, true
	case "lua":
		return task{Label: "run " + v.doc.Title(), Command: "lua " + p}, true
	case "ruby":
		return task{Label: "run " + v.doc.Title(), Command: "ruby " + p}, true
	case "php":
		return task{Label: "run " + v.doc.Title(), Command: "php " + p}, true
	}
	return task{}, false
}

const tasksTemplate = `{
  // See https://go.microsoft.com/fwlink/?LinkId=733558
  // for the documentation about the tasks.json format
  "version": "2.0.0",
  "tasks": [
    {
      "label": "build",
      "type": "shell",
      "command": "echo Hello",
      "group": {
        "kind": "build",
        "isDefault": true
      }
    }
  ]
}
`

func (a *App) pickShell() {
	var items []QuickItem
	data, _ := os.ReadFile("/etc/shells")
	seen := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		base := filepath.Base(l)
		if seen[base] {
			continue
		}
		seen[base] = true
		items = append(items, QuickItem{Label: base, Description: l, Data: l, Icon: a.Icon(IcoTerminal)})
	}
	a.openQuick(&QuickPick{Placeholder: "Select your default terminal profile", Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			_ = setUserSetting(a, "terminal.integrated.shell.linux", it.Data.(string))
		}
	}})
}
