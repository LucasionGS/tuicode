package theme

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/config"
	"tuicode/internal/syntax"
)

//go:embed themes/*.json
var builtinFS embed.FS

// RGBA is a color with alpha.
type RGBA struct {
	R, G, B, A uint8
}

// ParseColor parses #RGB, #RGBA, #RRGGBB and #RRGGBBAA.
func ParseColor(s string) (RGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	expand := func(c byte) string { return string([]byte{c, c}) }
	switch len(s) {
	case 3:
		s = expand(s[0]) + expand(s[1]) + expand(s[2]) + "ff"
	case 4:
		s = expand(s[0]) + expand(s[1]) + expand(s[2]) + expand(s[3])
	case 6:
		s += "ff"
	case 8:
	default:
		return RGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGBA{}, false
	}
	return RGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
}

// Over blends c over base.
func (c RGBA) Over(base RGBA) RGBA {
	if c.A == 255 {
		return c
	}
	a := float64(c.A) / 255
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*a + float64(y)*(1-a) + 0.5) }
	return RGBA{mix(c.R, base.R), mix(c.G, base.G), mix(c.B, base.B), 255}
}

// Tcell converts to an opaque tcell color.
func (c RGBA) Tcell() tcell.Color {
	return tcell.NewRGBColor(int32(c.R), int32(c.G), int32(c.B))
}

// FromTcell converts a tcell color to RGBA.
func FromTcell(c tcell.Color) RGBA {
	r, g, b := c.RGB()
	if r < 0 {
		return RGBA{0, 0, 0, 255}
	}
	return RGBA{uint8(r), uint8(g), uint8(b), 255}
}

// TokenStyle is the resolved style for a syntax kind.
type TokenStyle struct {
	FG        RGBA
	HasFG     bool
	Bold      bool
	Italic    bool
	Underline bool
	Strike    bool
}

type tokenRule struct {
	selectors []string
	style     TokenStyle
	hasStyle  bool
}

// Theme is a resolved color theme.
type Theme struct {
	Name   string
	Type   string // dark, light, hc-black, hc-light
	colors map[string]RGBA
	rules  []tokenRule
	kinds  [syntax.KCount]TokenStyle
	cache  map[string]tcell.Color
}

// Info describes an available theme.
type Info struct {
	Label  string
	UIType string
	Source string // "builtin:<file>" or an absolute path
	Origin string // where the theme came from, for display
}

type themeFile struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Include     string            `json:"include"`
	Colors      map[string]string `json:"colors"`
	TokenColors json.RawMessage   `json:"tokenColors"`
}

type tokenColorJSON struct {
	Scope    any `json:"scope"`
	Settings struct {
		Foreground string `json:"foreground"`
		FontStyle  string `json:"fontStyle"`
	} `json:"settings"`
}

var builtins = []Info{
	{"Dark Modern", "vs-dark", "builtin:dark_modern.json", "Built-in"},
	{"Dark+", "vs-dark", "builtin:dark_plus.json", "Built-in"},
	{"Dark 2026", "vs-dark", "builtin:2026-dark.json", "Built-in"},
	{"Dark (Visual Studio)", "vs-dark", "builtin:dark_vs.json", "Built-in"},
	{"Dark High Contrast", "hc-black", "builtin:hc_black.json", "Built-in"},
	{"Light Modern", "vs", "builtin:light_modern.json", "Built-in"},
	{"Light+", "vs", "builtin:light_plus.json", "Built-in"},
	{"Light 2026", "vs", "builtin:2026-light.json", "Built-in"},
	{"Light (Visual Studio)", "vs", "builtin:light_vs.json", "Built-in"},
	{"Light High Contrast", "hc-light", "builtin:hc_light.json", "Built-in"},
	{"Monokai", "vs-dark", "builtin:monokai-color-theme.json", "Built-in"},
	{"Monokai Dimmed", "vs-dark", "builtin:dimmed-monokai-color-theme.json", "Built-in"},
	{"Solarized Dark", "vs-dark", "builtin:solarized-dark-color-theme.json", "Built-in"},
	{"Solarized Light", "vs", "builtin:solarized-light-color-theme.json", "Built-in"},
	{"Abyss", "vs-dark", "builtin:abyss-color-theme.json", "Built-in"},
	{"Quiet Light", "vs", "builtin:quietlight-color-theme.json", "Built-in"},
	{"Red", "vs-dark", "builtin:Red-color-theme.json", "Built-in"},
	{"Tomorrow Night Blue", "vs-dark", "builtin:tomorrow-night-blue-color-theme.json", "Built-in"},
	{"Kimbie Dark", "vs-dark", "builtin:kimbie-dark-color-theme.json", "Built-in"},
}

// Available lists built-in themes plus themes found in VS Code extension folders
// and the user's theme directory.
func Available(extraDirs ...string) []Info {
	out := append([]Info{}, builtins...)
	seen := map[string]bool{}
	for _, b := range builtins {
		seen[b.Label] = true
	}
	home, _ := os.UserHomeDir()
	extDirs := []string{
		filepath.Join(home, ".vscode", "extensions"),
		filepath.Join(home, ".vscode-oss", "extensions"),
		filepath.Join(home, ".cursor", "extensions"),
	}
	for _, dir := range extDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			extPath := filepath.Join(dir, e.Name())
			for _, inf := range extensionThemes(extPath) {
				if !seen[inf.Label] {
					seen[inf.Label] = true
					out = append(out, inf)
				}
			}
		}
	}
	for _, dir := range extraDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var tf themeFile
			if config.UnmarshalJSONC(data, &tf) != nil {
				continue
			}
			label := tf.Name
			if label == "" {
				label = strings.TrimSuffix(e.Name(), ".json")
			}
			ui := "vs-dark"
			if tf.Type == "light" {
				ui = "vs"
			}
			if !seen[label] {
				seen[label] = true
				out = append(out, Info{label, ui, p, "User"})
			}
		}
	}
	return out
}

func extensionThemes(extPath string) []Info {
	data, err := os.ReadFile(filepath.Join(extPath, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		DisplayName string `json:"displayName"`
		Contributes struct {
			Themes []struct {
				ID      string `json:"id"`
				Label   string `json:"label"`
				UITheme string `json:"uiTheme"`
				Path    string `json:"path"`
			} `json:"themes"`
		} `json:"contributes"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	var nls map[string]string
	if b, err := os.ReadFile(filepath.Join(extPath, "package.nls.json")); err == nil {
		_ = json.Unmarshal(b, &nls)
	}
	resolve := func(s string) string {
		if strings.HasPrefix(s, "%") && strings.HasSuffix(s, "%") {
			if v, ok := nls[strings.Trim(s, "%")]; ok {
				return v
			}
		}
		return s
	}
	var out []Info
	for _, t := range pkg.Contributes.Themes {
		label := resolve(t.Label)
		if label == "" {
			label = t.ID
		}
		p := filepath.Join(extPath, t.Path)
		if !strings.HasSuffix(p, ".json") {
			continue
		}
		out = append(out, Info{label, t.UITheme, p, resolve(pkg.DisplayName)})
	}
	return out
}

// Load resolves a theme by label. Unknown labels fall back to Dark Modern.
func Load(label string, extraDirs ...string) (*Theme, error) {
	var info *Info
	for _, i := range Available(extraDirs...) {
		if strings.EqualFold(i.Label, label) {
			ii := i
			info = &ii
			break
		}
	}
	if info == nil {
		info = &builtins[0]
	}
	return LoadInfo(*info)
}

// LoadInfo loads the theme described by info.
func LoadInfo(info Info) (*Theme, error) {
	t := &Theme{Name: info.Label, colors: map[string]RGBA{}, cache: map[string]tcell.Color{}}
	switch info.UIType {
	case "vs":
		t.Type = "light"
	case "hc-black":
		t.Type = "hc-black"
	case "hc-light":
		t.Type = "hc-light"
	default:
		t.Type = "dark"
	}
	if err := t.loadFile(info.Source, 0); err != nil {
		return nil, err
	}
	t.resolveKinds()
	return t, nil
}

func readSource(src string) ([]byte, error) {
	if strings.HasPrefix(src, "builtin:") {
		return builtinFS.ReadFile("themes/" + strings.TrimPrefix(src, "builtin:"))
	}
	return os.ReadFile(src)
}

func joinSource(base, rel string) string {
	if strings.HasPrefix(base, "builtin:") {
		return "builtin:" + path.Clean(path.Join(path.Dir(strings.TrimPrefix(base, "builtin:")), rel))
	}
	return filepath.Join(filepath.Dir(base), rel)
}

func (t *Theme) loadFile(src string, depth int) error {
	if depth > 8 {
		return fmt.Errorf("Theme include depth exceeded while loading %s.", src)
	}
	data, err := readSource(src)
	if err != nil {
		return err
	}
	var tf themeFile
	if err := config.UnmarshalJSONC(data, &tf); err != nil {
		return fmt.Errorf("Failed to parse theme %s: %w", src, err)
	}
	if tf.Include != "" {
		if err := t.loadFile(joinSource(src, tf.Include), depth+1); err != nil {
			return err
		}
	}
	if tf.Type != "" && depth == 0 {
		switch tf.Type {
		case "light":
			t.Type = "light"
		case "dark":
			t.Type = "dark"
		case "hc", "hcDark":
			t.Type = "hc-black"
		case "hcLight":
			t.Type = "hc-light"
		}
	}
	for k, v := range tf.Colors {
		if c, ok := ParseColor(v); ok {
			t.colors[k] = c
		}
	}
	if len(tf.TokenColors) > 0 && tf.TokenColors[0] == '[' {
		var tcs []tokenColorJSON
		if err := json.Unmarshal(tf.TokenColors, &tcs); err == nil {
			for _, tc := range tcs {
				rule := tokenRule{}
				switch s := tc.Scope.(type) {
				case string:
					for _, part := range strings.Split(s, ",") {
						rule.selectors = append(rule.selectors, strings.TrimSpace(part))
					}
				case []any:
					for _, p := range s {
						if ps, ok := p.(string); ok {
							rule.selectors = append(rule.selectors, strings.TrimSpace(ps))
						}
					}
				}
				if c, ok := ParseColor(tc.Settings.Foreground); ok {
					rule.style.FG = c
					rule.style.HasFG = true
				}
				fs := tc.Settings.FontStyle
				rule.style.Bold = strings.Contains(fs, "bold")
				rule.style.Italic = strings.Contains(fs, "italic")
				rule.style.Underline = strings.Contains(fs, "underline")
				rule.style.Strike = strings.Contains(fs, "strikethrough")
				rule.hasStyle = tc.Settings.FontStyle != ""
				if len(rule.selectors) == 0 {
					// global settings: editor foreground
					if rule.style.HasFG {
						if _, ok := t.colors["editor.foreground"]; !ok {
							t.colors["editor.foreground"] = rule.style.FG
						}
					}
					continue
				}
				t.rules = append(t.rules, rule)
			}
		}
	}
	return nil
}

// IsLight reports whether the theme has a light background.
func (t *Theme) IsLight() bool { return t.Type == "light" || t.Type == "hc-light" }

func (t *Theme) defaultsFor(key string) (string, bool) {
	if t.IsLight() {
		if v, ok := lightOverrides[key]; ok {
			return v, true
		}
	}
	v, ok := darkDefaults[key]
	return v, ok
}

// Lookup returns the raw color for key, following defaults.
func (t *Theme) Lookup(key string) (RGBA, bool) {
	for i := 0; i < 10; i++ {
		if c, ok := t.colors[key]; ok {
			return c, true
		}
		v, ok := t.defaultsFor(key)
		if !ok {
			return RGBA{}, false
		}
		if strings.HasPrefix(v, "=") {
			key = v[1:]
			continue
		}
		c, ok := ParseColor(v)
		return c, ok
	}
	return RGBA{}, false
}

// Has reports whether the theme or defaults define key.
func (t *Theme) Has(key string) bool {
	_, ok := t.Lookup(key)
	return ok
}

// Defined reports whether the theme itself (not defaults) defines key.
func (t *Theme) Defined(key string) bool {
	_, ok := t.colors[key]
	return ok
}

// RGBA returns the opaque color for key, blended over the editor background.
func (t *Theme) RGBA(key string) RGBA {
	c, ok := t.Lookup(key)
	if !ok {
		c = RGBA{255, 0, 255, 255}
	}
	if c.A != 255 {
		bg, _ := t.Lookup("editor.background")
		bg.A = 255
		c = c.Over(bg)
	}
	return c
}

// C returns the color for key as an opaque tcell color.
func (t *Theme) C(key string) tcell.Color {
	if c, ok := t.cache[key]; ok {
		return c
	}
	c := t.RGBA(key).Tcell()
	t.cache[key] = c
	return c
}

// Over returns the color for key blended over the given background color key.
func (t *Theme) Over(key, baseKey string) tcell.Color {
	ck := key + "@" + baseKey
	if c, ok := t.cache[ck]; ok {
		return c
	}
	c, ok := t.Lookup(key)
	if !ok {
		return t.C(baseKey)
	}
	res := c.Over(t.RGBA(baseKey)).Tcell()
	t.cache[ck] = res
	return res
}

// OverColor blends the color for key over an arbitrary color.
func (t *Theme) OverColor(key string, base tcell.Color) tcell.Color {
	c, ok := t.Lookup(key)
	if !ok {
		return base
	}
	return c.Over(FromTcell(base)).Tcell()
}

// Or returns key's color if defined anywhere, else the fallback key's color.
func (t *Theme) Or(key, fallback string) tcell.Color {
	if t.Has(key) {
		return t.C(key)
	}
	return t.C(fallback)
}

// matchScore returns how specifically selector matches scope (0 = no match).
func matchScore(selector, scope string) int {
	// only the last part of a descendant selector is considered
	if i := strings.LastIndex(selector, " "); i >= 0 {
		selector = selector[i+1:]
	}
	if selector == "" {
		return 0
	}
	if scope == selector || strings.HasPrefix(scope, selector+".") {
		return strings.Count(selector, ".") + 1
	}
	return 0
}

// ResolveScope returns the token style for a TextMate scope.
func (t *Theme) ResolveScope(scope string) (TokenStyle, bool) {
	best := -1
	bestFG := 0
	bestFont := 0
	var style TokenStyle
	found := false
	for _, r := range t.rules {
		for _, sel := range r.selectors {
			s := matchScore(sel, scope)
			if s == 0 {
				continue
			}
			found = true
			if r.style.HasFG && s >= bestFG {
				bestFG = s
				style.FG = r.style.FG
				style.HasFG = true
			}
			if r.hasStyle && s >= bestFont {
				bestFont = s
				style.Bold, style.Italic, style.Underline, style.Strike = r.style.Bold, r.style.Italic, r.style.Underline, r.style.Strike
			}
			if s > best {
				best = s
			}
		}
	}
	return style, found
}

func (t *Theme) resolveKinds() {
	fg := t.RGBA("editor.foreground")
	for k := syntax.Kind(0); k < syntax.KCount; k++ {
		st := TokenStyle{FG: fg, HasFG: true}
		for _, scope := range syntax.Scopes[k] {
			if s, ok := t.ResolveScope(scope); ok && s.HasFG {
				st = s
				break
			}
		}
		t.kinds[k] = st
	}
	// markup kinds should keep their font style even without a color
	if s, ok := t.ResolveScope("markup.bold"); ok {
		t.kinds[syntax.KBold].Bold = s.Bold || !s.HasFG
	} else {
		t.kinds[syntax.KBold].Bold = true
	}
	if s, ok := t.ResolveScope("markup.italic"); ok {
		t.kinds[syntax.KItalic].Italic = s.Italic || !s.HasFG
	} else {
		t.kinds[syntax.KItalic].Italic = true
	}
}

// Token returns the style for a syntax kind.
func (t *Theme) Token(k syntax.Kind) TokenStyle { return t.kinds[k] }

// Apply applies a token style's foreground and attributes to a tcell style.
func (ts TokenStyle) Apply(st tcell.Style) tcell.Style {
	if ts.HasFG {
		st = st.Foreground(ts.FG.Tcell())
	}
	if ts.Bold {
		st = st.Bold(true)
	}
	if ts.Italic {
		st = st.Italic(true)
	}
	if ts.Underline {
		st = st.Underline(true)
	}
	if ts.Strike {
		st = st.StrikeThrough(true)
	}
	return st
}

// Labels returns the sorted labels of the given infos.
func Labels(infos []Info) []string {
	out := make([]string, len(infos))
	for i, inf := range infos {
		out[i] = inf.Label
	}
	sort.Strings(out)
	return out
}
