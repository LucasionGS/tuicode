package ui

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
)

type outlineSym struct {
	lsp.DocumentSymbol
	pos   buffer.Pos
	end   buffer.Pos
	depth int
}

type outlineCache struct {
	version int
	syms    []lsp.DocumentSymbol
	flat    []outlineSym
	pending bool
	fromLSP bool
}

// OutlineView shows document symbols of the active editor.
type OutlineView struct {
	app       *App
	cache     map[*Document]*outlineCache
	selected  int
	scroll    int
	rect      Rect
	collapsed map[string]bool
}

func newOutlineView(a *App) *OutlineView {
	return &OutlineView{app: a, cache: map[*Document]*outlineCache{}, collapsed: map[string]bool{}}
}

func (o *OutlineView) docChanged(d *Document) {
	if c, ok := o.cache[d]; ok && c.version != d.Buf.Version() {
		// refresh lazily: breadcrumbs or the view will request again
		c.pending = false
	}
}

func (o *OutlineView) refresh() {
	v := o.app.ActiveView()
	if v == nil {
		return
	}
	o.fetch(v.doc, nil)
}

// fetch requests document symbols; done runs when fresh data arrives.
func (o *OutlineView) fetch(d *Document, done func()) {
	a := o.app
	c := o.cache[d]
	if c == nil {
		c = &outlineCache{version: -1}
		o.cache[d] = c
	}
	if c.version == d.Buf.Version() {
		if done != nil {
			done()
		}
		return
	}
	if c.pending {
		return
	}
	version := d.Buf.Version()
	if a.lsp != nil && a.lsp.Client(d.Buf) != nil && a.lsp.Client(d.Buf).Supports("documentSymbolProvider") {
		c.pending = true
		_, ok := a.lsp.Request(d.Buf, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": lsp.PathToURI(d.Buf.Path)}}, func(raw json.RawMessage, err error) {
			c.pending = false
			if err != nil {
				return
			}
			syms := lsp.ParseSymbols(raw)
			if len(syms) > 0 && syms[0].Location != nil {
				syms = nestFlatSymbols(syms)
			}
			c.syms = syms
			c.version = version
			c.fromLSP = true
			c.flat = nil
			o.flatten(d, c)
			if done != nil {
				done()
			}
		})
		if ok {
			return
		}
		c.pending = false
	}
	// regex fallback for files without a language server
	c.syms = regexSymbols(d)
	c.version = version
	c.fromLSP = false
	c.flat = nil
	o.flatten(d, c)
	if done != nil {
		done()
	}
}

// nestFlatSymbols builds a hierarchy from SymbolInformation ranges.
func nestFlatSymbols(flat []lsp.DocumentSymbol) []lsp.DocumentSymbol {
	sort.SliceStable(flat, func(i, j int) bool {
		a, b := flat[i].Range.Start, flat[j].Range.Start
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Character < b.Character
	})
	return flat
}

var symbolPatterns = []struct {
	re   *regexp.Regexp
	kind int
}{
	{regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\*?\s+([A-Za-z_$][\w$]*)`), 12},
	{regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`), 5},
	{regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_$][\w$]*)`), 11},
	{regexp.MustCompile(`^\s*(?:export\s+)?(?:const\s+)?enum\s+([A-Za-z_$][\w$]*)`), 10},
	{regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?namespace\s+([A-Za-z_$][\w$.]*)`), 3},
	{regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`), 12},
	{regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s+(?:struct|interface)`), 23},
	{regexp.MustCompile(`^\s*def\s+([A-Za-z_]\w*)`), 12},
	{regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`), 12},
	{regexp.MustCompile(`^\s*(?:pub\s+)?(?:struct|enum|trait)\s+([A-Za-z_]\w*)`), 23},
	{regexp.MustCompile(`^\s*([A-Za-z_][\w-]*)\s*\(\)\s*\{`), 12},
	{regexp.MustCompile(`^(#{1,6})\s+(.+)$`), 15},
}

func regexSymbols(d *Document) []lsp.DocumentSymbol {
	var out []lsp.DocumentSymbol
	b := d.Buf
	limit := min(b.LineCount(), 50000)
	md := d.Lang.ID == "markdown"
	for l := 0; l < limit; l++ {
		line := b.LineString(l)
		for _, p := range symbolPatterns {
			m := p.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if p.kind == 15 && !md {
				continue
			}
			name := m[len(m)-1]
			kind := p.kind
			if md {
				name = strings.Repeat("  ", len(m[1])-1) + m[2]
				kind = 15
			}
			r := lsp.Range{Start: lsp.Position{Line: l}, End: lsp.Position{Line: l, Character: len(line)}}
			out = append(out, lsp.DocumentSymbol{Name: name, Kind: kind, Range: r, SelectionRange: r})
			break
		}
	}
	return out
}

func (o *OutlineView) flatten(d *Document, c *outlineCache) {
	a := o.app
	var walk func(syms []lsp.DocumentSymbol, depth int)
	walk = func(syms []lsp.DocumentSymbol, depth int) {
		for _, s := range syms {
			var pos, end buffer.Pos
			if c.fromLSP && a.lsp != nil {
				pos = a.lsp.FromLSP(d.Buf, s.SelectionRange.Start)
				end = a.lsp.FromLSP(d.Buf, s.Range.End)
			} else {
				pos = buffer.Pos{Line: s.SelectionRange.Start.Line, Col: s.SelectionRange.Start.Character}
				end = buffer.Pos{Line: s.Range.End.Line, Col: s.Range.End.Character}
			}
			c.flat = append(c.flat, outlineSym{DocumentSymbol: s, pos: pos, end: end, depth: depth})
			if len(s.Children) > 0 {
				walk(s.Children, depth+1)
			}
		}
	}
	walk(c.syms, 0)
}

// flatSymbols returns cached symbols (nil if not loaded yet).
func (o *OutlineView) flatSymbols(d *Document) []outlineSym {
	c := o.cache[d]
	if c == nil || c.version != d.Buf.Version() {
		if c != nil && c.flat != nil && c.pending {
			return c.flat
		}
		return nil
	}
	if c.flat == nil {
		return []outlineSym{}
	}
	return c.flat
}

// symbolPath returns the chain of symbols containing p (for breadcrumbs).
func (o *OutlineView) symbolPath(d *Document, p buffer.Pos) []lsp.DocumentSymbol {
	a := o.app
	c := o.cache[d]
	if (c == nil || c.version != d.Buf.Version()) && a.lsp != nil && a.lsp.Client(d.Buf) != nil {
		o.fetch(d, nil)
		c = o.cache[d]
	}
	if c == nil {
		return nil
	}
	var out []lsp.DocumentSymbol
	syms := c.syms
	for {
		found := false
		for _, s := range syms {
			var start, end buffer.Pos
			if c.fromLSP && a.lsp != nil {
				start = a.lsp.FromLSP(d.Buf, s.Range.Start)
				end = a.lsp.FromLSP(d.Buf, s.Range.End)
			} else {
				continue
			}
			if !p.Less(start) && !end.Less(p) {
				out = append(out, s)
				syms = s.Children
				found = true
				break
			}
		}
		if !found || len(out) > 8 {
			break
		}
	}
	return out
}

func (o *OutlineView) handleKey(key, text string, isText bool) bool {
	a := o.app
	v := a.ActiveView()
	if v == nil {
		return false
	}
	syms := o.flatSymbols(v.doc)
	switch key {
	case "up":
		o.selected = max(o.selected-1, 0)
	case "down":
		o.selected = min(o.selected+1, max(len(syms)-1, 0))
	case "enter", "space":
		if o.selected < len(syms) {
			s := syms[o.selected]
			a.pushNav()
			v.SetCursor(s.pos)
			v.ensurePosVisible(s.pos, true)
			if key == "enter" {
				a.focusEditor()
			}
		}
	case "escape":
		a.focusEditor()
	default:
		return false
	}
	return true
}

func (o *OutlineView) draw(r Rect) {
	o.drawList(r)
}

func (o *OutlineView) drawList(r Rect) {
	a := o.app
	th := a.theme
	o.rect = r
	bgc := th.C("sideBar.background")
	v := a.ActiveView()
	if v == nil {
		a.cv.Text(r.X+2, r.Y, r.X+r.W, "The active editor cannot provide outline information.", styleFB(th.C("descriptionForeground"), bgc))
		return
	}
	syms := o.flatSymbols(v.doc)
	if syms == nil {
		o.fetch(v.doc, nil)
		a.cv.Text(r.X+2, r.Y, r.X+r.W, "Loading document symbols...", styleFB(th.C("descriptionForeground"), bgc))
		return
	}
	if len(syms) == 0 {
		a.cv.Text(r.X+2, r.Y, r.X+r.W, "No symbols found in document '"+v.doc.Title()+"'", styleFB(th.C("descriptionForeground"), bgc))
		return
	}
	// follow the cursor
	cur := v.Cursor()
	active := -1
	for i, s := range syms {
		if !cur.Less(s.pos) && (s.end.Line >= cur.Line || i == len(syms)-1) {
			active = i
		}
	}
	focused := a.focus == FocusSidebar && a.sidebarView == "outline"
	if o.selected >= len(syms) {
		o.selected = len(syms) - 1
	}
	if !focused && active >= 0 {
		o.selected = active
	}
	h := r.H
	if o.selected < o.scroll {
		o.scroll = o.selected
	}
	if o.selected >= o.scroll+h {
		o.scroll = o.selected - h + 1
	}
	o.scroll = clampInt(o.scroll, 0, max(len(syms)-h, 0))
	for i := 0; i < h; i++ {
		idx := o.scroll + i
		if idx >= len(syms) {
			break
		}
		s := syms[idx]
		y := r.Y + i
		rbg := bgc
		fg := th.C("sideBar.foreground")
		if idx == o.selected && focused {
			rbg = th.C("list.activeSelectionBackground")
			fg = th.C("list.activeSelectionForeground")
		} else if idx == o.selected {
			rbg = th.C("list.inactiveSelectionBackground")
		} else if a.mouseY == y && r.Contains(a.mouseX, a.mouseY) {
			rbg = th.OverColor("list.hoverBackground", bgc)
		}
		st := styleFB(fg, rbg)
		a.cv.Fill(Rect{r.X, y, r.W, 1}, ' ', st)
		x := r.X + 1 + s.depth*2
		icon, ck := a.kindIcon(s.Kind, true)
		x = a.cv.Text(x, y, r.X+r.W, icon+" ", styleFB(th.C(ck), rbg))
		x = a.cv.Text(x, y, r.X+r.W-1, s.Name, st)
		if s.Detail != "" {
			a.cv.TextEllipsis(x+1, y, r.X+r.W-x-2, strings.ReplaceAll(s.Detail, "\n", " "), st.Foreground(th.OverColor("descriptionForeground", rbg)))
		}
		sym := s
		rowIdx := idx
		a.addHit(Rect{r.X, y, r.W, 1}, func(ev MouseEvent) bool {
			switch ev.Kind {
			case MousePress:
				o.selected = rowIdx
				a.pushNav()
				v.SetCursor(sym.pos)
				v.ensurePosVisible(sym.pos, true)
				if ev.Clicks >= 2 {
					a.focusEditor()
				}
			case MouseWheel:
				o.scroll = clampInt(o.scroll+ev.WheelDY*3, 0, max(len(syms)-h, 0))
			}
			return true
		})
	}
}
