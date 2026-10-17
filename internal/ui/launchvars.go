package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"tuicode/internal/config"
)

var interactiveVarRe = regexp.MustCompile(`\$\{(command:pickProcess|command:PickProcess|input:[\w.-]+)\}`)

// launchInputs reads the "inputs" of launch.json (and tasks.json) by id.
func (a *App) launchInputs() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, name := range []string{"launch.json", "tasks.json"} {
		data, err := os.ReadFile(filepath.Join(a.root, ".vscode", name))
		if err != nil {
			continue
		}
		var f struct {
			Inputs []map[string]any `json:"inputs"`
		}
		if config.UnmarshalJSONC(data, &f) != nil {
			continue
		}
		for _, in := range f.Inputs {
			if id, ok := in["id"].(string); ok {
				out[id] = in
			}
		}
	}
	return out
}

// findInteractiveVar returns the first ${input:...} or ${command:pickProcess} in a configuration.
func findInteractiveVar(v any) string {
	switch x := v.(type) {
	case string:
		if m := interactiveVarRe.FindStringSubmatch(x); m != nil {
			return m[1]
		}
	case []any:
		for _, e := range x {
			if s := findInteractiveVar(e); s != "" {
				return s
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s := findInteractiveVar(x[k]); s != "" {
				return s
			}
		}
	}
	return ""
}

// replaceVar substitutes one variable everywhere. A string that is only the variable becomes
// a number when the value is numeric (processId must be a number).
func replaceVar(v any, name, value string) any {
	token := "${" + name + "}"
	switch x := v.(type) {
	case string:
		if x == token {
			if n, err := strconv.Atoi(value); err == nil {
				return n
			}
		}
		return strings.ReplaceAll(x, token, value)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = replaceVar(e, name, value)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = replaceVar(e, name, value)
		}
		return out
	}
	return v
}

// resolveInteractiveVars asks for ${input:...} values and processes one after another, then calls done.
func (a *App) resolveInteractiveVars(cfg map[string]any, done func(map[string]any)) {
	name := findInteractiveVar(cfg)
	if name == "" {
		done(cfg)
		return
	}
	next := func(value string) {
		a.resolveInteractiveVars(replaceVar(cfg, name, value).(map[string]any), done)
	}
	if strings.EqualFold(name, "command:pickProcess") {
		a.pickProcess(next)
		return
	}
	id := strings.TrimPrefix(name, "input:")
	in, ok := a.launchInputs()[id]
	if !ok {
		a.notify(NotifyError, "Undefined input variable '"+id+"' encountered. Remove or define '"+id+"' to continue.")
		return
	}
	desc, _ := in["description"].(string)
	def, _ := in["default"].(string)
	switch in["type"] {
	case "pickString":
		var items []QuickItem
		opts, _ := in["options"].([]any)
		for _, o := range opts {
			switch x := o.(type) {
			case string:
				items = append(items, QuickItem{Label: x, Data: x})
			case map[string]any:
				label, _ := x["label"].(string)
				value, _ := x["value"].(string)
				if label == "" {
					label = value
				}
				items = append(items, QuickItem{Label: label, Description: value, Data: value})
			}
		}
		a.openQuick(&QuickPick{Placeholder: desc, Items: items, KeepOrder: true, OnAccept: func(it *QuickItem, q string) {
			if it != nil {
				next(it.Data.(string))
			}
		}})
	default: // promptString
		a.promptInput(desc, desc, def, func(s string) { next(s) })
	}
}

// pickProcess lists the user's processes like VS Code's "Attach to Process" picker.
func (a *App) pickProcess(done func(pid string)) {
	entries, _ := os.ReadDir("/proc")
	uid := os.Getuid()
	var items []QuickItem
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		if st, err := os.Stat(filepath.Join("/proc", e.Name())); err != nil || statUID(st) != uid {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || len(data) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		items = append(items, QuickItem{Label: filepath.Base(args[0]), Description: e.Name(), Detail: strings.Join(args, " "), Data: e.Name()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	a.openQuick(&QuickPick{Placeholder: "Select the process to attach to", Items: items, MatchDescription: true, OnAccept: func(it *QuickItem, q string) {
		if it != nil {
			done(it.Data.(string))
		}
	}})
}
