package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tuicode/internal/buffer"
	"tuicode/internal/git"
	"tuicode/internal/lsp"
	"tuicode/internal/syntax"
)

// Diag is a diagnostic mapped into buffer coordinates.
type Diag struct {
	Range    buffer.Range
	Severity int
	Message  string
	Source   string
	Code     string
	Tags     []int
	Raw      lsp.Diagnostic
}

// Document is an open file shared by all editors showing it.
type Document struct {
	Buf  *buffer.Buffer
	Lang *syntax.Language

	hl        *syntax.Highlighter
	spans     [][]syntax.Span
	hlVersion int
	hlRunning bool
	hlQueued  bool

	gitBase     []string
	gitBaseCRLF bool
	hasGitBase  bool
	gitChanges  map[int]int
	gitVersion  int

	Diags []Diag

	refs     int
	listener int
	views    map[*EditorView]bool
	// Untitled documents are numbered.
	untitledN int
	// Preview documents (like the welcome page or diffs) are not saved.
	Virtual bool
	// HighlightOff disables syntax highlighting for very large files.
	HighlightOff bool

	semSpans   [][]syntax.Span
	semVersion int
	semTimer   *time.Timer

	conflicts       []mergeConflict
	conflictStarts  map[int]bool
	conflictVersion int

	tests        map[int]string // Go test functions by line
	testsVersion int

	mdFences       []bool // lines inside fenced code blocks
	mdFenceVersion int

	blame        []git.BlameInfo
	blameVersion int
	blameRunning bool
	blameAsked   time.Time
}

func (a *App) newDocument(buf *buffer.Buffer) *Document {
	d := &Document{Buf: buf, views: map[*EditorView]bool{}}
	first := ""
	if buf.LineCount() > 0 {
		first = buf.LineString(0)
	}
	if buf.LanguageID == "" || buf.LanguageID == "plaintext" {
		d.Lang = a.detectLanguage(buf.Path, first)
		buf.LanguageID = d.Lang.ID
	} else {
		d.Lang = syntax.ByID(buf.LanguageID)
	}
	d.HighlightOff = buf.LineCount() > 200000
	d.hl = syntax.NewHighlighter(d.Lang, buf.Path)
	d.listener = buf.AddListener(func(ch buffer.Change) { a.onDocChange(d, ch) })
	a.applyEditorSettings(d)
	return d
}

func (a *App) detectLanguage(path, first string) *syntax.Language {
	// files.associations: {"*.foo": "json"}
	if path != "" {
		base := filepath.Base(path)
		for pat, v := range a.settings.Map("files.associations") {
			id, _ := v.(string)
			if id == "" {
				continue
			}
			p := strings.TrimPrefix(pat, "**/")
			if ok, _ := filepath.Match(p, base); ok {
				return syntax.ByID(id)
			}
		}
	}
	return syntax.Detect(path, first)
}

func (a *App) applyEditorSettings(d *Document) {
	lang := d.Lang.ID
	d.Buf.TabSize = a.settings.Int("editor.tabSize", lang, 4)
	d.Buf.InsertSpaces = a.settings.Bool("editor.insertSpaces", lang)
	if a.settings.Bool("editor.detectIndentation", lang) {
		// detection only overrides when the content has indentation
		d.Buf.DetectIndentation()
	}
}

// SetLanguage changes the document's language mode.
func (a *App) SetLanguage(d *Document, lang *syntax.Language) {
	if a.lsp != nil {
		a.lsp.Close(d.Buf)
	}
	d.Lang = lang
	d.Buf.LanguageID = lang.ID
	d.hl = syntax.NewHighlighter(lang, d.Buf.Path)
	d.spans = nil
	d.hlVersion = -1
	a.requestHighlight(d)
	if a.lsp != nil {
		a.lsp.Open(d.Buf)
	}
}

func (a *App) onDocChange(d *Document, ch buffer.Change) {
	// shift highlight spans so stale results stay aligned until re-highlighting finishes
	oldLines := ch.Old.End.Line - ch.Old.Start.Line
	newLines := ch.NewEnd.Line - ch.Old.Start.Line
	if delta := newLines - oldLines; delta != 0 && d.spans != nil {
		at := ch.Old.Start.Line + 1
		if at <= len(d.spans) {
			if delta > 0 {
				ins := make([][]syntax.Span, delta)
				d.spans = append(d.spans[:at], append(ins, d.spans[at:]...)...)
			} else {
				end := min(at-delta, len(d.spans))
				d.spans = append(d.spans[:at], d.spans[end:]...)
			}
		}
	}
	if delta := newLines - oldLines; delta != 0 && d.semSpans != nil {
		at := ch.Old.Start.Line + 1
		if at <= len(d.semSpans) {
			if delta > 0 {
				ins := make([][]syntax.Span, delta)
				d.semSpans = append(d.semSpans[:at], append(ins, d.semSpans[at:]...)...)
			} else {
				end := min(at-delta, len(d.semSpans))
				d.semSpans = append(d.semSpans[:at], d.semSpans[end:]...)
			}
		}
	}
	if ch.Old.Start.Line < len(d.semSpans) && ch.Old.Start.Line == ch.NewEnd.Line {
		shiftSpans(d.semSpans, ch)
	}
	if ch.Old.Start.Line < len(d.spans) {
		// the edited line's spans are probably off; drop them only for single-line edits
		// to avoid flicker on large changes
		if ch.Old.Start.Line == ch.NewEnd.Line {
			shiftSpans(d.spans, ch)
		}
	}
	// shift diagnostics
	for i := range d.Diags {
		d.Diags[i].Range.Start = buffer.TransformPos(d.Diags[i].Range.Start, ch.Old, ch.NewEnd, false)
		d.Diags[i].Range.End = buffer.TransformPos(d.Diags[i].Range.End, ch.Old, ch.NewEnd, true)
	}
	a.shiftBreakpoints(d, ch)
	a.requestHighlight(d)
	a.scheduleGitDiff(d)
	a.scheduleSemanticTokens(d)
	a.docChanged(d)
}

// shiftSpans adjusts spans on a single edited line so colors follow the text.
func shiftSpans(spans [][]syntax.Span, ch buffer.Change) {
	line := ch.Old.Start.Line
	if line >= len(spans) {
		return
	}
	delta := ch.NewEnd.Col - ch.Old.End.Col
	at := ch.Old.Start.Col
	out := spans[line][:0:0]
	for _, s := range spans[line] {
		if s.End <= at {
			out = append(out, s)
			continue
		}
		if s.Start >= ch.Old.End.Col {
			s.Start += delta
			s.End += delta
			out = append(out, s)
			continue
		}
		// span overlaps the edit: extend or shrink it
		s.End += delta
		if s.End > s.Start {
			out = append(out, s)
		}
	}
	spans[line] = out
}

// requestHighlight re-tokenizes the document in the background.
func (a *App) requestHighlight(d *Document) {
	if d.HighlightOff || d.hl == nil {
		return
	}
	if d.hlRunning {
		d.hlQueued = true
		return
	}
	if d.hlVersion == d.Buf.Version() && d.spans != nil {
		return
	}
	d.hlRunning = true
	version := d.Buf.Version()
	lines := d.Buf.Lines()
	hl := d.hl
	go func() {
		spans := hl.Highlight(lines)
		a.Post(func() {
			d.hlRunning = false
			if d.hl == hl && d.Buf.Version() == version {
				d.spans = spans
				d.hlVersion = version
			} else if d.hl == hl && len(spans) == d.Buf.LineCount() && d.spans == nil {
				d.spans = spans
			}
			if d.hlQueued || d.Buf.Version() != version {
				d.hlQueued = false
				a.requestHighlight(d)
			}
		})
	}()
}

// SpansFor returns highlight spans for a line.
func (d *Document) SpansFor(line int) []syntax.Span {
	if line < len(d.spans) {
		return d.spans[line]
	}
	return nil
}

// loadGitBase loads the HEAD version of the document for gutter decorations.
func (a *App) loadGitBase(d *Document) {
	if a.gitRoot == "" || d.Buf.Path == "" || !a.settings.Bool("git.enabled", "") {
		return
	}
	rel, err := filepath.Rel(a.gitRoot, d.Buf.Path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	root := a.gitRoot
	go func() {
		// like VS Code, changes are shown against the index so staged hunks disappear
		content, err := git.Show(root, "", rel)
		a.Post(func() {
			if err != nil {
				// untracked or new file: everything is added only when tracked status says so
				d.hasGitBase = false
				d.gitBase = nil
				if st, ok := a.gitStatusFor(d.Buf.Path); ok && (st.Untracked() || st.X == 'A') {
					d.hasGitBase = true
					d.gitBase = []string{}
				}
			} else {
				d.hasGitBase = true
				d.gitBaseCRLF = strings.Contains(content, "\r\n")
				content = strings.ReplaceAll(content, "\r\n", "\n")
				d.gitBase = strings.Split(content, "\n")
			}
			d.gitVersion = -1
			a.scheduleGitDiff(d)
		})
	}()
}

func (a *App) scheduleGitDiff(d *Document) {
	if !d.hasGitBase || d.gitVersion == d.Buf.Version() {
		return
	}
	d.gitVersion = d.Buf.Version()
	if len(d.gitBase) == 0 {
		ch := map[int]int{}
		if !(d.Buf.LineCount() == 1 && d.Buf.LineLen(0) == 0) {
			for i := 0; i < d.Buf.LineCount(); i++ {
				ch[i] = git.ChangeAdded
			}
		}
		d.gitChanges = ch
		return
	}
	d.gitChanges = git.LineChanges(d.gitBase, d.Buf.Lines())
}

// setDiagnostics converts LSP diagnostics into buffer coordinates.
func (a *App) setDiagnostics(d *Document) {
	d.Diags = nil
	if d.Buf.Path == "" || a.lsp == nil {
		return
	}
	for _, fd := range a.lsp.Diagnostics(d.Buf.Path) {
		for _, ld := range fd.Diagnostics {
			r := buffer.Range{
				Start: lsp.FromLSPPos(d.Buf, ld.Range.Start, fd.Encoding),
				End:   lsp.FromLSPPos(d.Buf, ld.Range.End, fd.Encoding),
			}
			sev := ld.Severity
			if sev == 0 {
				sev = lsp.SeverityError
			}
			src := ld.Source
			if src == "" {
				src = fd.Server
			}
			d.Diags = append(d.Diags, Diag{Range: r, Severity: sev, Message: ld.Message, Source: src, Code: ld.CodeString(), Tags: ld.Tags, Raw: ld})
		}
	}
	sort.SliceStable(d.Diags, func(i, j int) bool {
		if d.Diags[i].Range.Start != d.Diags[j].Range.Start {
			return d.Diags[i].Range.Start.Less(d.Diags[j].Range.Start)
		}
		return d.Diags[i].Severity < d.Diags[j].Severity
	})
}

// Title returns the display name of the document.
func (d *Document) Title() string {
	return d.Buf.Name()
}

// checkDisk reloads the document if it changed on disk and is not dirty.
func (a *App) checkDisk(d *Document) {
	if d.Buf.Path == "" || d.Virtual {
		return
	}
	st, err := os.Stat(d.Buf.Path)
	if err != nil {
		return
	}
	if !st.ModTime().After(d.Buf.ModTime) || d.Buf.ModTime.IsZero() {
		return
	}
	if d.Buf.Dirty() {
		d.Buf.ModTime = st.ModTime()
		a.notify(NotifyWarning, "The file "+d.Buf.Name()+" has been changed on disk. Save will overwrite it; use \"File: Revert File\" to load the disk version.")
		return
	}
	_ = d.Buf.Reload()
	a.loadGitBase(d)
}
