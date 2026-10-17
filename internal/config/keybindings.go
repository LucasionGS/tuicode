package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Binding maps a key chord to a command.
type Binding struct {
	Keys    []string
	Command string
	Args    any
	When    *When
	WhenSrc string
	Source  string
}

var scanCodes = map[string]string{
	"semicolon": ";", "equal": "=", "comma": ",", "minus": "-", "period": ".", "slash": "/",
	"backquote": "`", "bracketleft": "[", "bracketright": "]", "backslash": "\\", "quote": "'",
	"intlbackslash": "\\", "space": "space", "enter": "enter", "tab": "tab", "escape": "escape",
	"backspace": "backspace", "delete": "delete", "insert": "insert", "home": "home", "end": "end",
	"pageup": "pageup", "pagedown": "pagedown", "arrowup": "up", "arrowdown": "down", "arrowleft": "left", "arrowright": "right",
	"numpadadd": "+", "numpadsubtract": "-", "numpadmultiply": "*", "numpaddivide": "/",
}

var keyAliases = map[string]string{
	"esc": "escape", "return": "enter", "del": "delete", "ins": "insert", "pgup": "pageup", "pgdn": "pagedown",
	"oem_1": ";", "oem_plus": "=", "oem_comma": ",", "oem_minus": "-", "oem_period": ".", "oem_2": "/",
	"oem_3": "`", "oem_4": "[", "oem_5": "\\", "oem_6": "]", "oem_7": "'", "oem_102": "\\",
	"numpad_add": "+", "numpad_subtract": "-", "numpad_multiply": "*", "numpad_divide": "/",
	"up": "up", "down": "down", "left": "left", "right": "right",
}

// NormalizeKey normalizes one chord part like "Shift+Ctrl+P" into "ctrl+shift+p".
func NormalizeKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var ctrl, shift, alt, meta bool
	parts := strings.Split(strings.ToLower(s), "+")
	// a trailing "+" means the plus key: "ctrl++"
	key := parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	if key == "" && len(parts) >= 2 {
		key = "+"
		mods = parts[:len(parts)-2]
	}
	for _, m := range mods {
		switch m {
		case "ctrl", "control":
			ctrl = true
		case "shift":
			shift = true
		case "alt", "option", "opt":
			alt = true
		case "meta", "cmd", "win", "super":
			meta = true
		}
	}
	if strings.HasPrefix(key, "[") && strings.HasSuffix(key, "]") && len(key) > 2 {
		code := key[1 : len(key)-1]
		if v, ok := scanCodes[code]; ok {
			key = v
		} else if strings.HasPrefix(code, "key") && len(code) == 4 {
			key = code[3:]
		} else if strings.HasPrefix(code, "digit") && len(code) == 6 {
			key = code[5:]
		} else if strings.HasPrefix(code, "numpad") && len(code) == 7 {
			key = code[6:]
		} else {
			key = code
		}
	}
	if v, ok := keyAliases[key]; ok {
		key = v
	}
	var sb strings.Builder
	if ctrl {
		sb.WriteString("ctrl+")
	}
	if shift {
		sb.WriteString("shift+")
	}
	if alt {
		sb.WriteString("alt+")
	}
	if meta {
		sb.WriteString("meta+")
	}
	sb.WriteString(key)
	return sb.String()
}

// ParseKeys splits a chord sequence ("ctrl+k ctrl+s") into normalized parts.
func ParseKeys(s string) []string {
	var out []string
	for _, p := range strings.Fields(s) {
		if k := NormalizeKey(p); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// FormatKey turns "ctrl+shift+p" into "Ctrl+Shift+P" for display.
func FormatKey(keys []string) string {
	var parts []string
	for _, k := range keys {
		segs := strings.Split(k, "+")
		if strings.HasSuffix(k, "++") {
			segs = append(strings.Split(strings.TrimSuffix(k, "++"), "+"), "+")
		}
		for i, s := range segs {
			switch s {
			case "ctrl":
				segs[i] = "Ctrl"
			case "shift":
				segs[i] = "Shift"
			case "alt":
				segs[i] = "Alt"
			case "meta":
				segs[i] = "Super"
			case "pageup":
				segs[i] = "PageUp"
			case "pagedown":
				segs[i] = "PageDown"
			case "escape":
				segs[i] = "Escape"
			case "up":
				segs[i] = "UpArrow"
			case "down":
				segs[i] = "DownArrow"
			case "left":
				segs[i] = "LeftArrow"
			case "right":
				segs[i] = "RightArrow"
			default:
				if len(s) > 0 {
					r := []rune(s)
					r[0] = unicode.ToUpper(r[0])
					segs[i] = string(r)
				}
			}
		}
		parts = append(parts, strings.Join(segs, "+"))
	}
	return strings.Join(parts, " ")
}

type rawBinding struct {
	Key     string `json:"key"`
	Command string `json:"command"`
	When    string `json:"when"`
	Args    any    `json:"args"`
}

// LoadKeybindings layers user keybinding files over defaults. Entries whose
// command starts with "-" remove matching earlier bindings, like VS Code.
func LoadKeybindings(defaults []Binding, importVSCode bool) ([]Binding, []string) {
	out := append([]Binding{}, defaults...)
	var errs []string
	files := []string{}
	if importVSCode {
		files = append(files, filepath.Join(VSCodeUserDir(), "keybindings.json"))
	}
	files = append(files, UserKeybindingsPath())
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var raws []rawBinding
		if err := UnmarshalJSONC(data, &raws); err != nil {
			errs = append(errs, f+": "+err.Error())
			continue
		}
		for _, r := range raws {
			keys := ParseKeys(r.Key)
			if strings.HasPrefix(r.Command, "-") {
				cmd := r.Command[1:]
				filtered := out[:0]
				for _, b := range out {
					if b.Command == cmd && (len(keys) == 0 || equalKeys(b.Keys, keys)) {
						continue
					}
					filtered = append(filtered, b)
				}
				out = filtered
				continue
			}
			if len(keys) == 0 || r.Command == "" {
				continue
			}
			w, err := ParseWhen(r.When)
			if err != nil {
				errs = append(errs, f+": "+err.Error())
				continue
			}
			out = append(out, Binding{Keys: keys, Command: r.Command, Args: r.Args, When: w, WhenSrc: r.When, Source: f})
		}
	}
	return out, errs
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// When is a parsed when-clause expression.
type When struct {
	op       string // "and", "or", "not", "key", "eq", "ne", "re", "true", "false", "in", "gt", "lt"
	children []*When
	key      string
	value    string
	re       *regexp.Regexp
}

// Context resolves context keys for when-clauses.
type Context func(key string) any

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case float64:
		return x != 0
	}
	return true
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	case int:
		return itoa(x)
	}
	return ""
}

func itoa(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// Eval evaluates the expression. A nil When is always true.
func (w *When) Eval(ctx Context) bool {
	if w == nil {
		return true
	}
	switch w.op {
	case "true":
		return true
	case "false":
		return false
	case "and":
		for _, c := range w.children {
			if !c.Eval(ctx) {
				return false
			}
		}
		return true
	case "or":
		for _, c := range w.children {
			if c.Eval(ctx) {
				return true
			}
		}
		return false
	case "not":
		return !w.children[0].Eval(ctx)
	case "key":
		return truthy(ctx(w.key))
	case "eq":
		return asString(ctx(w.key)) == w.value
	case "ne":
		return asString(ctx(w.key)) != w.value
	case "re":
		return w.re != nil && w.re.MatchString(asString(ctx(w.key)))
	case "in":
		v := ctx(w.value)
		k := asString(ctx(w.key))
		switch x := v.(type) {
		case []string:
			for _, s := range x {
				if s == k {
					return true
				}
			}
		case map[string]bool:
			return x[k]
		}
		return false
	}
	return false
}

type whenLexer struct {
	toks []string
	pos  int
}

func tokenizeWhen(s string) []string {
	var toks []string
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '(' || c == ')':
			toks = append(toks, string(c))
			i++
		case strings.HasPrefix(s[i:], "&&") || strings.HasPrefix(s[i:], "||") || strings.HasPrefix(s[i:], "==") ||
			strings.HasPrefix(s[i:], "!=") || strings.HasPrefix(s[i:], "=~") || strings.HasPrefix(s[i:], ">=") || strings.HasPrefix(s[i:], "<="):
			toks = append(toks, s[i:i+2])
			i += 2
			if toks[len(toks)-1] == "==" && i < len(s) && s[i] == '=' {
				i++
			}
			if toks[len(toks)-1] == "!=" && i < len(s) && s[i] == '=' {
				i++
			}
			if toks[len(toks)-1] == "=~" {
				// regex literal /.../flags
				for i < len(s) && s[i] == ' ' {
					i++
				}
				if i < len(s) && s[i] == '/' {
					j := i + 1
					for j < len(s) && s[j] != '/' {
						if s[j] == '\\' {
							j++
						}
						j++
					}
					end := j + 1
					for end < len(s) && unicode.IsLetter(rune(s[end])) {
						end++
					}
					toks = append(toks, s[i:min(end, len(s))])
					i = end
				}
			}
		case c == '!' || c == '>' || c == '<':
			toks = append(toks, string(c))
			i++
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(s) && s[j] != c {
				j++
			}
			toks = append(toks, s[i+1:min(j, len(s))])
			i = j + 1
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t()!&|=<>", rune(s[j])) {
				j++
			}
			if j == i {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		}
	}
	return toks
}

// ParseWhen parses a when-clause. An empty string yields nil (always true).
func ParseWhen(s string) (*When, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	lx := &whenLexer{toks: tokenizeWhen(s)}
	w := lx.parseOr()
	return w, nil
}

func (l *whenLexer) peek() string {
	if l.pos < len(l.toks) {
		return l.toks[l.pos]
	}
	return ""
}

func (l *whenLexer) next() string {
	t := l.peek()
	l.pos++
	return t
}

func (l *whenLexer) parseOr() *When {
	left := l.parseAnd()
	if l.peek() != "||" {
		return left
	}
	w := &When{op: "or", children: []*When{left}}
	for l.peek() == "||" {
		l.next()
		w.children = append(w.children, l.parseAnd())
	}
	return w
}

func (l *whenLexer) parseAnd() *When {
	left := l.parseUnary()
	if l.peek() != "&&" {
		return left
	}
	w := &When{op: "and", children: []*When{left}}
	for l.peek() == "&&" {
		l.next()
		w.children = append(w.children, l.parseUnary())
	}
	return w
}

func (l *whenLexer) parseUnary() *When {
	switch l.peek() {
	case "!":
		l.next()
		return &When{op: "not", children: []*When{l.parseUnary()}}
	case "(":
		l.next()
		w := l.parseOr()
		if l.peek() == ")" {
			l.next()
		}
		return w
	}
	key := l.next()
	switch key {
	case "true":
		return &When{op: "true"}
	case "false":
		return &When{op: "false"}
	}
	switch l.peek() {
	case "==":
		l.next()
		return &When{op: "eq", key: key, value: l.next()}
	case "!=":
		l.next()
		return &When{op: "ne", key: key, value: l.next()}
	case "=~":
		l.next()
		lit := l.next()
		pat := lit
		flags := ""
		if strings.HasPrefix(lit, "/") {
			end := strings.LastIndex(lit, "/")
			if end > 0 {
				pat = lit[1:end]
				flags = lit[end+1:]
			}
		}
		if strings.Contains(flags, "i") {
			pat = "(?i)" + pat
		}
		re, _ := regexp.Compile(pat)
		return &When{op: "re", key: key, re: re}
	case "in":
		l.next()
		return &When{op: "in", key: key, value: l.next()}
	}
	if l.peek() == "not" {
		// "a not in b"
		l.next()
		if l.peek() == "in" {
			l.next()
			return &When{op: "not", children: []*When{{op: "in", key: key, value: l.next()}}}
		}
	}
	return &When{op: "key", key: key}
}
