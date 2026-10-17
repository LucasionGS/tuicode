package lsp

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
)

// Position is an LSP position. Character units depend on the negotiated encoding.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range in a document.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LocationLink is the richer form of a definition result.
type LocationLink struct {
	OriginSelectionRange *Range `json:"originSelectionRange,omitempty"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// Diagnostic severities.
const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

// Diagnostic is a problem reported by a server.
type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity,omitempty"`
	Code     any             `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
	Tags     []int           `json:"tags,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// CodeString returns the diagnostic code as a string.
func (d Diagnostic) CodeString() string {
	switch c := d.Code.(type) {
	case string:
		return c
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(jsonNumber(c), ".0"), ".")
	}
	return ""
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// PublishDiagnosticsParams is the payload of textDocument/publishDiagnostics.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// TextEdit replaces a range with text.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// InsertReplaceEdit is an alternative completion edit form.
type InsertReplaceEdit struct {
	NewText string `json:"newText"`
	Insert  Range  `json:"insert"`
	Replace Range  `json:"replace"`
}

// MarkupContent is formatted documentation.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Command is an LSP command.
type Command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

// Completion item kinds.
const (
	CIKText          = 1
	CIKMethod        = 2
	CIKFunction      = 3
	CIKConstructor   = 4
	CIKField         = 5
	CIKVariable      = 6
	CIKClass         = 7
	CIKInterface     = 8
	CIKModule        = 9
	CIKProperty      = 10
	CIKUnit          = 11
	CIKValue         = 12
	CIKEnum          = 13
	CIKKeyword       = 14
	CIKSnippet       = 15
	CIKColor         = 16
	CIKFile          = 17
	CIKReference     = 18
	CIKFolder        = 19
	CIKEnumMember    = 20
	CIKConstant      = 21
	CIKStruct        = 22
	CIKEvent         = 23
	CIKOperator      = 24
	CIKTypeParameter = 25
)

// CompletionItem is a single completion proposal.
type CompletionItem struct {
	Label        string `json:"label"`
	LabelDetails *struct {
		Detail      string `json:"detail,omitempty"`
		Description string `json:"description,omitempty"`
	} `json:"labelDetails,omitempty"`
	Kind                int             `json:"kind,omitempty"`
	Detail              string          `json:"detail,omitempty"`
	Documentation       json.RawMessage `json:"documentation,omitempty"`
	SortText            string          `json:"sortText,omitempty"`
	FilterText          string          `json:"filterText,omitempty"`
	InsertText          string          `json:"insertText,omitempty"`
	InsertTextFormat    int             `json:"insertTextFormat,omitempty"`
	TextEdit            json.RawMessage `json:"textEdit,omitempty"`
	AdditionalTextEdits []TextEdit      `json:"additionalTextEdits,omitempty"`
	CommitCharacters    []string        `json:"commitCharacters,omitempty"`
	Command             *Command        `json:"command,omitempty"`
	Data                json.RawMessage `json:"data,omitempty"`
	Preselect           bool            `json:"preselect,omitempty"`
	Deprecated          bool            `json:"deprecated,omitempty"`
	Tags                []int           `json:"tags,omitempty"`
}

// Edit returns the item's text edit as a plain TextEdit (using the replace range for insert/replace edits).
func (c *CompletionItem) Edit() (*TextEdit, bool) {
	if len(c.TextEdit) == 0 || string(c.TextEdit) == "null" {
		return nil, false
	}
	var te struct {
		Range   *Range `json:"range"`
		NewText string `json:"newText"`
		Insert  *Range `json:"insert"`
		Replace *Range `json:"replace"`
	}
	if json.Unmarshal(c.TextEdit, &te) != nil {
		return nil, false
	}
	if te.Range != nil {
		return &TextEdit{Range: *te.Range, NewText: te.NewText}, true
	}
	if te.Replace != nil {
		return &TextEdit{Range: *te.Replace, NewText: te.NewText}, true
	}
	if te.Insert != nil {
		return &TextEdit{Range: *te.Insert, NewText: te.NewText}, true
	}
	return nil, false
}

// CompletionList is a list of completion items.
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
	ItemDefaults *struct {
		EditRange        json.RawMessage `json:"editRange,omitempty"`
		InsertTextFormat int             `json:"insertTextFormat,omitempty"`
	} `json:"itemDefaults,omitempty"`
}

// Hover is a hover result.
type Hover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range,omitempty"`
}

// SignatureHelp describes callable signatures.
type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature"`
	ActiveParameter int                    `json:"activeParameter"`
}

// SignatureInformation is one signature.
type SignatureInformation struct {
	Label           string                 `json:"label"`
	Documentation   json.RawMessage        `json:"documentation,omitempty"`
	Parameters      []ParameterInformation `json:"parameters,omitempty"`
	ActiveParameter *int                   `json:"activeParameter,omitempty"`
}

// ParameterInformation is one parameter. Label is a string or an [start, end] offset pair.
type ParameterInformation struct {
	Label         json.RawMessage `json:"label"`
	Documentation json.RawMessage `json:"documentation,omitempty"`
}

// DocumentSymbol is a hierarchical symbol.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
	// Location is set when the server returned SymbolInformation instead.
	Location      *Location `json:"location,omitempty"`
	ContainerName string    `json:"containerName,omitempty"`
}

// SymbolInformation is a flat symbol (workspace symbols).
type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// DocumentHighlight marks occurrences of a symbol.
type DocumentHighlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind,omitempty"`
}

// WorkspaceEdit is a set of changes across documents.
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []json.RawMessage     `json:"documentChanges,omitempty"`
}

// TextDocumentEdit is an edit to a versioned document.
type TextDocumentEdit struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version *int   `json:"version"`
	} `json:"textDocument"`
	Edits []TextEdit `json:"edits"`
}

// AllEdits flattens a workspace edit into per-URI text edits. File operations
// (create/rename/delete) are returned separately.
func (w *WorkspaceEdit) AllEdits() (map[string][]TextEdit, []map[string]any) {
	out := map[string][]TextEdit{}
	var ops []map[string]any
	for uri, edits := range w.Changes {
		out[uri] = append(out[uri], edits...)
	}
	for _, raw := range w.DocumentChanges {
		var probe map[string]any
		if json.Unmarshal(raw, &probe) != nil {
			continue
		}
		if _, ok := probe["kind"]; ok {
			ops = append(ops, probe)
			continue
		}
		var tde TextDocumentEdit
		if json.Unmarshal(raw, &tde) == nil {
			out[tde.TextDocument.URI] = append(out[tde.TextDocument.URI], tde.Edits...)
		}
	}
	return out, ops
}

// CodeAction is a quick fix or refactoring.
type CodeAction struct {
	Title       string          `json:"title"`
	Kind        string          `json:"kind,omitempty"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
	IsPreferred bool            `json:"isPreferred,omitempty"`
	Edit        *WorkspaceEdit  `json:"edit,omitempty"`
	Command     json.RawMessage `json:"command,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	Disabled    *struct {
		Reason string `json:"reason"`
	} `json:"disabled,omitempty"`
}

// InlayHint is an inline annotation.
type InlayHint struct {
	Position     Position        `json:"position"`
	Label        json.RawMessage `json:"label"`
	Kind         int             `json:"kind,omitempty"`
	PaddingLeft  bool            `json:"paddingLeft,omitempty"`
	PaddingRight bool            `json:"paddingRight,omitempty"`
}

// LabelText returns the inlay hint label as plain text.
func (h InlayHint) LabelText() string {
	var s string
	if json.Unmarshal(h.Label, &s) == nil {
		return s
	}
	var parts []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(h.Label, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			sb.WriteString(p.Value)
		}
		return sb.String()
	}
	return ""
}

// FoldingRange is a foldable region.
type FoldingRange struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Kind      string `json:"kind,omitempty"`
}

// MarkupToText converts hover/documentation content (any of the LSP shapes) into markdown text.
func MarkupToText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var mc struct {
		Kind     string `json:"kind"`
		Value    string `json:"value"`
		Language string `json:"language"`
	}
	if json.Unmarshal(raw, &mc) == nil && (mc.Value != "" || mc.Kind != "") {
		if mc.Language != "" {
			return "```" + mc.Language + "\n" + mc.Value + "\n```"
		}
		return mc.Value
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		var parts []string
		for _, a := range arr {
			if t := MarkupToText(a); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n\n---\n\n")
	}
	return ""
}

// PathToURI converts a filesystem path to a file:// URI.
func PathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

// URIToPath converts a file:// URI to a filesystem path.
func URIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return strings.TrimPrefix(uri, "file://")
	}
	return filepath.FromSlash(u.Path)
}

// Symbol kinds.
var SymbolKindNames = map[int]string{
	1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class", 6: "method", 7: "property",
	8: "field", 9: "constructor", 10: "enum", 11: "interface", 12: "function", 13: "variable",
	14: "constant", 15: "string", 16: "number", 17: "boolean", 18: "array", 19: "object",
	20: "key", 21: "null", 22: "enumMember", 23: "struct", 24: "event", 25: "operator", 26: "typeParameter",
}
