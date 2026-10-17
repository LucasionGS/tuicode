package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"tuicode/internal/buffer"
	"tuicode/internal/lsp"
	"tuicode/internal/syntax"
	"tuicode/internal/theme"
)

// mdSeg is a styled piece of a rendered markdown line.
type mdSeg struct {
	text string
	kind syntax.Kind // token kind for code, KText otherwise
	code bool
	bold bool
	ital bool
	link bool
	dim  bool
	hr   bool
}

type mdLine struct {
	segs   []mdSeg
	codeBg bool
	hr     bool
}

func (l mdLine) draw(a *App, x, y, maxX int, bg tcell.Color) {
	th := a.theme
	if l.hr {
		for xx := x; xx < maxX; xx++ {
			a.cv.Set(xx, y, '─', styleFB(th.OverColor("editorHoverWidget.border", bg), bg))
		}
		return
	}
	rowBg := bg
	if l.codeBg {
		rowBg = th.OverColor("textCodeBlock.background", bg)
		a.cv.Fill(Rect{x - 1, y, maxX - x + 1, 1}, ' ', styleFB(th.C("editor.foreground"), rowBg))
	}
	fg := th.C("editorHoverWidget.foreground")
	for _, s := range l.segs {
		st := styleFB(fg, rowBg)
		switch {
		case s.code && s.kind != syntax.KText:
			st = th.Token(s.kind).Apply(st)
		case s.code:
			st = st.Foreground(th.C("textPreformat.foreground"))
			if l.codeBg {
				st = st.Foreground(th.C("editor.foreground"))
			}
		}
		if s.bold {
			st = st.Bold(true)
		}
		if s.ital {
			st = st.Italic(true)
		}
		if s.link {
			st = st.Foreground(th.C("textLink.foreground"))
		}
		if s.dim {
			st = st.Foreground(th.C("descriptionForeground"))
		}
		x = a.cv.Text(x, y, maxX, s.text, st)
		if x >= maxX {
			return
		}
	}
}

// renderMarkdown converts markdown into wrapped, styled lines.
func renderMarkdown(a *App, md string, width int, defaultLang string) []mdLine {
	if width < 10 {
		width = 10
	}
	var out []mdLine
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	inCode := false
	var codeLang string
	var codeBuf []string
	flushCode := func() {
		lang := syntax.ByID(codeLang)
		if lang.ID == "plaintext" && codeLang != "" {
			for _, l := range syntax.Languages {
				if strings.EqualFold(l.Name, codeLang) || l.Lexer == codeLang {
					lang = l
					break
				}
			}
		}
		if codeLang == "" {
			lang = syntax.ByID(defaultLang)
		}
		hl := syntax.NewHighlighter(lang, "")
		spans := hl.Highlight(codeBuf)
		for i, cl := range codeBuf {
			rs := []rune(strings.ReplaceAll(cl, "\t", "    "))
			if len([]rune(cl)) != len(rs) {
				spans[i] = nil
			}
			var segs []mdSeg
			pos := 0
			for _, sp := range spans[i] {
				if sp.Start > pos && sp.Start <= len(rs) {
					segs = append(segs, mdSeg{text: string(rs[pos:sp.Start]), code: true})
				}
				if sp.End <= len(rs) && sp.Start < sp.End {
					segs = append(segs, mdSeg{text: string(rs[sp.Start:sp.End]), code: true, kind: sp.Kind})
				}
				pos = sp.End
			}
			if pos < len(rs) {
				segs = append(segs, mdSeg{text: string(rs[pos:]), code: true})
			}
			// wrap long code lines by cells
			out = append(out, wrapSegs(segs, width, true)...)
		}
		codeBuf = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				flushCode()
				inCode = false
			} else {
				inCode = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			continue
		}
		if inCode {
			codeBuf = append(codeBuf, line)
			continue
		}
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			out = append(out, mdLine{hr: true})
			continue
		}
		if trimmed == "" {
			if len(out) > 0 && !(len(out[len(out)-1].segs) == 0 && !out[len(out)-1].hr) {
				out = append(out, mdLine{})
			}
			continue
		}
		bold := false
		if strings.HasPrefix(trimmed, "#") {
			trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			bold = true
		}
		prefix := ""
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			prefix = " • "
			trimmed = trimmed[2:]
		}
		segs := parseInline(trimmed)
		if bold {
			for i := range segs {
				segs[i].bold = true
			}
		}
		if prefix != "" {
			segs = append([]mdSeg{{text: prefix}}, segs...)
		}
		out = append(out, wrapSegs(segs, width, false)...)
	}
	if inCode {
		flushCode()
	}
	// trim trailing blank lines
	for len(out) > 0 && len(out[len(out)-1].segs) == 0 && !out[len(out)-1].hr {
		out = out[:len(out)-1]
	}
	return out
}

// parseInline handles `code`, **bold**, *italic* and [links](url).
func parseInline(s string) []mdSeg {
	var segs []mdSeg
	var cur strings.Builder
	bold, ital := false, false
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, mdSeg{text: cur.String(), bold: bold, ital: ital})
			cur.Reset()
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs) && strings.ContainsRune("\\`*_{}[]()#+-.!<>", rs[i+1]):
			cur.WriteRune(rs[i+1])
			i++
		case c == '`':
			j := i + 1
			for j < len(rs) && rs[j] != '`' {
				j++
			}
			flush()
			segs = append(segs, mdSeg{text: string(rs[i+1 : min(j, len(rs))]), code: true})
			i = j
		case c == '*' && i+1 < len(rs) && rs[i+1] == '*':
			flush()
			bold = !bold
			i++
		case (c == '*' || c == '_') && (i+1 < len(rs) && rs[i+1] != ' ' || ital):
			if c == '_' && i > 0 && isWordRune(rs[i-1]) && !ital {
				cur.WriteRune(c)
				continue
			}
			flush()
			ital = !ital
		case c == '[':
			end := strings.Index(string(rs[i:]), "](")
			if end > 0 {
				close := strings.IndexRune(string(rs[i+end:]), ')')
				if close > 0 {
					flush()
					text := string(rs[i+1 : i+end])
					segs = append(segs, mdSeg{text: text, link: true})
					i = i + end + close
					continue
				}
			}
			cur.WriteRune(c)
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return segs
}

// wrapSegs wraps styled segments to width.
func wrapSegs(segs []mdSeg, width int, code bool) []mdLine {
	var out []mdLine
	cur := mdLine{codeBg: code}
	w := 0
	push := func() {
		out = append(out, cur)
		cur = mdLine{codeBg: code}
		w = 0
	}
	for _, s := range segs {
		words := []string{s.text}
		if !code {
			words = splitKeepSpaces(s.text)
		}
		for _, word := range words {
			ww := StrWidth(word)
			if w+ww > width && w > 0 {
				push()
				if !code {
					word = strings.TrimLeft(word, " ")
					ww = StrWidth(word)
				}
			}
			for ww > width {
				// hard-break long words
				rs := []rune(word)
				n := 0
				cw := 0
				for n < len(rs) && cw+RuneWidth(rs[n]) <= width-w {
					cw += RuneWidth(rs[n])
					n++
				}
				if n == 0 {
					push()
					continue
				}
				seg := s
				seg.text = string(rs[:n])
				cur.segs = append(cur.segs, seg)
				push()
				word = string(rs[n:])
				ww = StrWidth(word)
			}
			seg := s
			seg.text = word
			cur.segs = append(cur.segs, seg)
			w += ww
		}
	}
	if len(cur.segs) > 0 || len(out) == 0 {
		out = append(out, cur)
	}
	return out
}

func splitKeepSpaces(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == ' ' && i > start {
			out = append(out, s[start:i])
			start = i
		}
	}
	out = append(out, s[start:])
	return out
}

//#region Hover

// Hover is a hover tooltip in the editor.
type Hover struct {
	v       *EditorView
	anchor  buffer.Pos
	lines   []mdLine
	mouse   bool
	focused bool
	scrollY int
	rect    Rect
	md      string
}

func (h *Hover) contains(x, y int) bool { return h.rect.Contains(x, y) }

func (h *Hover) scroll(key string) {
	switch key {
	case "up":
		h.scrollY--
	case "down":
		h.scrollY++
	case "pageup":
		h.scrollY -= h.rect.H
	case "pagedown":
		h.scrollY += h.rect.H
	}
	h.scrollY = clampInt(h.scrollY, 0, max(len(h.lines)-h.rect.H, 0))
}

// showHover shows diagnostics and LSP hover info for p.
func (a *App) showHover(v *EditorView, p buffer.Pos, mouse bool) {
	if v.completion != nil && v.completion.visible() && mouse {
		return
	}
	var parts []string
	for _, d := range v.doc.Diags {
		if diagCovers(d, p, v.buf()) || (!d.Range.Empty() && posInRange(p, d.Range)) {
			src := d.Source
			if d.Code != "" {
				src += "(" + d.Code + ")"
			}
			parts = append(parts, fmt.Sprintf("%s %s", d.Message, "*"+src+"*"))
		}
	}
	// git blame-free: also show folded region preview? keep it simple
	show := func(extra string) {
		all := append([]string{}, parts...)
		if extra != "" {
			all = append(all, extra)
		}
		if len(all) == 0 {
			if !mouse {
				a.setStatusMessage("No hover information")
			}
			return
		}
		md := strings.Join(all, "\n\n---\n\n")
		v.hover = &Hover{v: v, anchor: p, mouse: mouse, md: md}
	}
	if a.lsp == nil || a.lsp.Client(v.buf()) == nil || !a.lsp.Client(v.buf()).Supports("hoverProvider") {
		show("")
		return
	}
	version := v.buf().Version()
	_, ok := a.lsp.Request(v.buf(), "textDocument/hover", a.lsp.DocParams(v.buf(), p), func(raw json.RawMessage, err error) {
		if v.buf().Version() != version {
			return
		}
		if mouse && (a.ActiveView() != v && !v.isVisible()) {
			return
		}
		text := ""
		if err == nil && string(raw) != "null" {
			var h lsp.Hover
			if json.Unmarshal(raw, &h) == nil {
				text = strings.TrimSpace(lsp.MarkupToText(h.Contents))
			}
		}
		show(text)
	})
	if !ok {
		show("")
	}
}

func (h *Hover) draw() {
	v := h.v
	a := v.app
	th := a.theme
	x, y, ok := v.screenPos(h.anchor)
	if !ok {
		v.hover = nil
		return
	}
	maxW := min(80, a.width-4)
	if len(h.lines) == 0 || h.rect.W == 0 {
		h.lines = renderMarkdown(a, h.md, maxW-2, v.doc.Lang.ID)
	}
	w := 10
	for _, l := range h.lines {
		lw := 0
		for _, s := range l.segs {
			lw += StrWidth(s.text)
		}
		if lw+2 > w {
			w = lw + 2
		}
	}
	w = min(w, maxW)
	maxH := min(15, a.height/2)
	hgt := min(len(h.lines), maxH)
	if hgt == 0 {
		return
	}
	// prefer above the line
	top := y - hgt
	if top < v.full.Y {
		top = y + 1
	}
	if top+hgt > a.height {
		top = max(a.height-hgt, 0)
	}
	left := x
	if left+w > a.width {
		left = max(a.width-w, 0)
	}
	h.rect = Rect{left, top, w, hgt}
	a.cv.clip = Rect{0, 0, a.width, a.height}
	bgc := th.C("editorHoverWidget.background")
	a.cv.Fill(h.rect, ' ', styleFB(th.C("editorHoverWidget.foreground"), bgc))
	h.scrollY = clampInt(h.scrollY, 0, max(len(h.lines)-hgt, 0))
	for i := 0; i < hgt; i++ {
		idx := h.scrollY + i
		if idx >= len(h.lines) {
			break
		}
		h.lines[idx].draw(a, left+1, top+i, left+w-1, bgc)
	}
	if len(h.lines) > hgt {
		sh := max(hgt*hgt/len(h.lines), 1)
		sy := h.scrollY * (hgt - sh) / max(len(h.lines)-hgt, 1)
		for i := sy; i < sy+sh; i++ {
			a.cv.Set(left+w-1, top+i, ' ', styleFB(bgc, th.OverColor("scrollbarSlider.background", bgc)))
		}
	}
	a.addHit(h.rect, func(ev MouseEvent) bool {
		switch ev.Kind {
		case MouseWheel:
			h.scrollY = clampInt(h.scrollY+ev.WheelDY, 0, max(len(h.lines)-hgt, 0))
		case MousePress:
			h.focused = true
		}
		return true
	})
}

//#endregion

//#region Tooltips

// Tooltip is a small label shown when hovering workbench controls.
type Tooltip struct {
	x, y int
	text string
}

func (a *App) showTooltipAt(x, y int, text string) {
	if text == "" {
		return
	}
	if a.tooltip != nil && a.tooltip.text == text {
		return
	}
	a.tooltip = nil
	if a.hoverTimer != nil {
		a.hoverTimer.Stop()
	}
	mx, my := a.mouseX, a.mouseY
	a.hoverTimer = afterFunc(600, func() {
		a.Post(func() {
			if a.mouseX == mx && a.mouseY == my {
				a.tooltip = &Tooltip{x: x, y: y, text: text}
			}
		})
	})
}

func (a *App) hideTooltip() {
	a.tooltip = nil
}

func (a *App) drawTooltip() {
	t := a.tooltip
	th := a.theme
	// tooltips disappear once the mouse moves away
	if abs(a.mouseY-t.y) > 2 {
		a.tooltip = nil
		return
	}
	w := StrWidth(t.text) + 2
	x := clampInt(t.x, 0, max(a.width-w, 0))
	y := t.y
	if y >= a.height {
		y = a.height - 1
	}
	if y < 0 {
		y = 0
	}
	st := styleFB(th.C("editorHoverWidget.foreground"), th.C("editorHoverWidget.background"))
	a.cv.Fill(Rect{x, y, w, 1}, ' ', st)
	a.cv.Text(x+1, y, x+w-1, t.text, st)
}

//#endregion

//#region Signature help

// SignatureHelpState is the parameter hints widget.
type SignatureHelpState struct {
	v      *EditorView
	help   lsp.SignatureHelp
	active int
	line   int
	anchor buffer.Pos
}

func (s *SignatureHelpState) cycle(d int) {
	n := len(s.help.Signatures)
	if n == 0 {
		return
	}
	s.active = (s.active + d + n) % n
}

func (a *App) triggerSignatureHelp(v *EditorView, trigger string) {
	if a.lsp == nil || a.lsp.Client(v.buf()) == nil || !a.lsp.Client(v.buf()).Supports("signatureHelpProvider") {
		return
	}
	p := v.Cursor()
	params := a.lsp.DocParams(v.buf(), p)
	ctx := map[string]any{"triggerKind": 1, "isRetrigger": v.sig != nil}
	if trigger != "" {
		ctx["triggerKind"] = 2
		ctx["triggerCharacter"] = trigger
	}
	if v.sig != nil {
		ctx["activeSignatureHelp"] = v.sig.help
	}
	params["context"] = ctx
	a.lsp.Request(v.buf(), "textDocument/signatureHelp", params, func(raw json.RawMessage, err error) {
		if err != nil || string(raw) == "null" {
			v.sig = nil
			return
		}
		var h lsp.SignatureHelp
		if json.Unmarshal(raw, &h) != nil || len(h.Signatures) == 0 {
			v.sig = nil
			return
		}
		active := h.ActiveSignature
		if active >= len(h.Signatures) || active < 0 {
			active = 0
		}
		v.sig = &SignatureHelpState{v: v, help: h, active: active, line: v.Cursor().Line, anchor: v.Cursor()}
	})
}

func (s *SignatureHelpState) draw() {
	v := s.v
	a := v.app
	th := a.theme
	if s.active >= len(s.help.Signatures) {
		return
	}
	sig := s.help.Signatures[s.active]
	x, y, ok := v.screenPos(v.Cursor())
	if !ok {
		return
	}
	activeParam := s.help.ActiveParameter
	if sig.ActiveParameter != nil {
		activeParam = *sig.ActiveParameter
	}
	// find the label range of the active parameter
	ps, pe := -1, -1
	if activeParam >= 0 && activeParam < len(sig.Parameters) {
		lbl := sig.Parameters[activeParam].Label
		var str string
		var offs []int
		if json.Unmarshal(lbl, &str) == nil {
			if i := strings.Index(sig.Label, str); i >= 0 {
				ps = len([]rune(sig.Label[:i]))
				pe = ps + len([]rune(str))
			}
		} else if json.Unmarshal(lbl, &offs) == nil && len(offs) == 2 {
			// offsets are UTF-16 based; treat as runes for ASCII labels
			ps, pe = offs[0], offs[1]
		}
	}
	maxW := min(a.width-2, 90)
	bgc := th.C("editorHoverWidget.background")
	label := sig.Label
	prefix := ""
	if len(s.help.Signatures) > 1 {
		prefix = fmt.Sprintf("%d/%d ", s.active+1, len(s.help.Signatures))
	}
	docText := ""
	if activeParam >= 0 && activeParam < len(sig.Parameters) {
		docText = lsp.MarkupToText(sig.Parameters[activeParam].Documentation)
	}
	sigDoc := lsp.MarkupToText(sig.Documentation)
	var docLines []mdLine
	if docText != "" || sigDoc != "" {
		docLines = renderMarkdown(a, strings.TrimSpace(strings.Join(nonEmpty(docText, sigDoc), "\n\n")), maxW-2, v.lang())
		if len(docLines) > 6 {
			docLines = docLines[:6]
		}
	}
	w := min(StrWidth(prefix+label)+2, maxW)
	for _, l := range docLines {
		lw := 2
		for _, sg := range l.segs {
			lw += StrWidth(sg.text)
		}
		w = max(w, min(lw, maxW))
	}
	hgt := 1 + len(docLines)
	if len(docLines) > 0 {
		hgt++
	}
	top := y - hgt
	if top < v.full.Y {
		top = y + 1
	}
	left := clampInt(x-StrWidth(prefix)-1, 0, max(a.width-w, 0))
	r := Rect{left, top, w, hgt}
	a.cv.clip = Rect{0, 0, a.width, a.height}
	base := styleFB(th.C("editorHoverWidget.foreground"), bgc)
	a.cv.Fill(r, ' ', base)
	px := a.cv.Text(left+1, top, left+w-1, prefix, base.Foreground(th.C("descriptionForeground")))
	rs := []rune(label)
	for i, ch := range rs {
		st := base
		if i >= ps && i < pe {
			st = base.Bold(true).Underline(true).Foreground(th.Or("editorHoverWidget.highlightForeground", "list.highlightForeground"))
		}
		if px >= left+w-1 {
			break
		}
		a.cv.Set(px, top, ch, st)
		px += RuneWidth(ch)
	}
	if len(docLines) > 0 {
		for xx := left; xx < left+w; xx++ {
			a.cv.Set(xx, top+1, '─', styleFB(th.OverColor("editorHoverWidget.border", bgc), bgc))
		}
		for i, l := range docLines {
			l.draw(a, left+1, top+2+i, left+w-1, bgc)
		}
	}
	a.addHit(r, func(ev MouseEvent) bool {
		if ev.Kind == MousePress {
			s.cycle(1)
		}
		return true
	})
}

//#endregion

// tokenStyleFor is a helper for tests and pages.
func tokenStyleFor(th *theme.Theme, k syntax.Kind) tcell.Style {
	return th.Token(k).Apply(styleFB(th.C("editor.foreground"), th.C("editor.background")))
}
