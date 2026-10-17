package syntax

import (
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Kind classifies a token for theming.
type Kind uint8

const (
	KText Kind = iota
	KComment
	KString
	KStringEscape
	KRegex
	KNumber
	KKeyword
	KKeywordControl
	KStorage // type keywords like int, const, func
	KOperator
	KPunctuation
	KFunction
	KType
	KVariable
	KProperty
	KParameter
	KConstant
	KBuiltin
	KTag
	KAttribute
	KNamespace
	KDecorator
	KHeading
	KBold
	KItalic
	KLink
	KCode
	KInvalid
	KInserted
	KDeleted
	KChanged
	KPreproc
	KBracket // colored by depth
	KLabel
	KSelf
	KCount
)

// Scopes maps kinds to TextMate scopes used to resolve theme token colors.
var Scopes = [KCount][]string{
	KText:           {"source"},
	KComment:        {"comment"},
	KString:         {"string"},
	KStringEscape:   {"constant.character.escape"},
	KRegex:          {"string.regexp"},
	KNumber:         {"constant.numeric"},
	KKeyword:        {"keyword", "storage.type"},
	KKeywordControl: {"keyword.control"},
	KStorage:        {"storage.type", "keyword"},
	KOperator:       {"keyword.operator"},
	KPunctuation:    {"punctuation"},
	KFunction:       {"entity.name.function", "support.function"},
	KType:           {"entity.name.type", "support.type", "entity.name.class"},
	KVariable:       {"variable", "variable.other.readwrite"},
	KProperty:       {"variable.other.property", "variable.other.object.property", "support.type.property-name", "variable"},
	KParameter:      {"variable.parameter", "variable"},
	KConstant:       {"variable.other.constant", "constant.language", "constant"},
	KBuiltin:        {"support.function", "entity.name.function"},
	KTag:            {"entity.name.tag"},
	KAttribute:      {"entity.other.attribute-name"},
	KNamespace:      {"entity.name.namespace", "entity.name.type"},
	KDecorator:      {"meta.decorator", "entity.name.function"},
	KHeading:        {"markup.heading", "entity.name.section"},
	KBold:           {"markup.bold"},
	KItalic:         {"markup.italic"},
	KLink:           {"markup.underline.link"},
	KCode:           {"markup.inline.raw", "string"},
	KInvalid:        {"invalid"},
	KInserted:       {"markup.inserted"},
	KDeleted:        {"markup.deleted"},
	KChanged:        {"markup.changed"},
	KPreproc:        {"keyword.control.directive", "meta.preprocessor", "keyword"},
	KBracket:        {"punctuation"},
	KLabel:          {"entity.name.label"},
	KSelf:           {"variable.language", "keyword"},
}

// Span is a highlighted run on one line. Start and End are rune columns.
type Span struct {
	Start int
	End   int
	Kind  Kind
	Depth int8 // bracket depth for KBracket
}

var controlKeywords = map[string]bool{
	"if": true, "else": true, "for": true, "while": true, "do": true, "switch": true, "case": true,
	"default": true, "break": true, "continue": true, "return": true, "goto": true, "try": true,
	"catch": true, "finally": true, "throw": true, "throws": true, "import": true, "export": true, "from": true,
	"yield": true, "await": true, "select": true, "go": true, "defer": true, "fallthrough": true,
	"range": true, "elif": true, "except": true, "raise": true, "with": true, "as": true, "pass": true,
	"loop": true, "match": true, "when": true, "unless": true, "until": true, "then": true, "fi": true,
	"done": true, "esac": true, "in": false, "require": true, "include": true, "using": true, "package": true,
	"end": true, "begin": true, "rescue": true, "ensure": true, "next": true, "redo": true, "retry": true,
	"foreach": true, "endif": true, "endfor": true, "endwhile": true, "use": true, "mod": false,
}

var selfKeywords = map[string]bool{
	"this": true, "self": true, "super": true, "Self": true,
}

var storageKeywords = map[string]bool{
	"func": true, "function": true, "var": true, "let": true, "const": true, "class": true, "struct": true,
	"interface": true, "type": true, "enum": true, "fn": true, "def": true, "impl": true, "trait": true,
	"pub": true, "public": true, "private": true, "protected": true, "static": true, "readonly": true,
	"async": true, "map": true, "chan": true, "namespace": true, "extends": true, "implements": true,
	"declare": true, "abstract": true, "final": true, "override": true, "virtual": true, "mut": true,
	"new": true, "delete": true, "typeof": true, "instanceof": true, "keyof": true, "in": true, "of": true,
	"void": true, "lambda": true, "local": true, "export": false,
}

// Highlighter tokenizes text with a chroma lexer and post-processes the result.
type Highlighter struct {
	lexer chroma.Lexer
	lang  *Language
}

// NewHighlighter returns a highlighter for a language, or nil if none applies.
func NewHighlighter(lang *Language, path string) *Highlighter {
	var lx chroma.Lexer
	if lang != nil && lang.Lexer != "" {
		lx = lexers.Get(lang.Lexer)
	}
	if lx == nil && path != "" && (lang == nil || lang.ID == "plaintext") {
		lx = lexers.Match(path)
	}
	if lx == nil {
		return &Highlighter{lang: lang}
	}
	return &Highlighter{lexer: chroma.Coalesce(lx), lang: lang}
}

type rawTok struct {
	kind  Kind
	ttype chroma.TokenType
	value string
}

// Highlight tokenizes the lines and returns spans per line.
func (h *Highlighter) Highlight(lines []string) [][]Span {
	out := make([][]Span, len(lines))
	if h == nil || h.lexer == nil {
		h.bracketsOnly(lines, out)
		return out
	}
	text := strings.Join(lines, "\n") + "\n"
	it, err := h.lexer.Tokenise(nil, text)
	if err != nil {
		h.bracketsOnly(lines, out)
		return out
	}
	toks := []rawTok{}
	for t := it(); t != chroma.EOF; t = it() {
		toks = append(toks, rawTok{kind: classify(t.Type, t.Value), ttype: t.Type, value: t.Value})
	}
	h.refine(toks)

	line, col := 0, 0
	depth := 0
	markup := h.lang != nil && (h.lang.ID == "markdown" || h.lang.ID == "plaintext")
	for _, t := range toks {
		// split value into lines
		parts := strings.Split(t.value, "\n")
		for pi, p := range parts {
			if pi > 0 {
				line++
				col = 0
			}
			if line >= len(out) {
				break
			}
			n := len([]rune(p))
			if n == 0 {
				continue
			}
			if !markup && (t.kind == KPunctuation || t.kind == KOperator || t.kind == KText) && strings.ContainsAny(p, "()[]{}") {
				// bracket pair colorization
				start := col
				for _, r := range p {
					switch r {
					case '(', '[', '{':
						if col > start {
							out[line] = appendSpan(out[line], Span{start, col, t.kind, 0})
						}
						out[line] = append(out[line], Span{col, col + 1, KBracket, int8(depth % 3)})
						depth++
						start = col + 1
					case ')', ']', '}':
						if col > start {
							out[line] = appendSpan(out[line], Span{start, col, t.kind, 0})
						}
						depth--
						if depth < 0 {
							depth = 0
							out[line] = append(out[line], Span{col, col + 1, KInvalid, 0})
						} else {
							out[line] = append(out[line], Span{col, col + 1, KBracket, int8(depth % 3)})
						}
						start = col + 1
					}
					col++
				}
				if col > start {
					out[line] = appendSpan(out[line], Span{start, col, t.kind, 0})
				}
				continue
			}
			if t.kind != KText {
				out[line] = appendSpan(out[line], Span{col, col + n, t.kind, 0})
			}
			col += n
		}
	}
	return out
}

func appendSpan(spans []Span, s Span) []Span {
	if s.Kind == KText {
		return spans
	}
	if n := len(spans); n > 0 && spans[n-1].End == s.Start && spans[n-1].Kind == s.Kind && s.Kind != KBracket {
		spans[n-1].End = s.End
		return spans
	}
	return append(spans, s)
}

func (h *Highlighter) bracketsOnly(lines []string, out [][]Span) {
	if h != nil && h.lang != nil && (h.lang.ID == "plaintext" || h.lang.ID == "markdown") {
		return
	}
	depth := 0
	for i, l := range lines {
		col := 0
		for _, r := range l {
			switch r {
			case '(', '[', '{':
				out[i] = append(out[i], Span{col, col + 1, KBracket, int8(depth % 3)})
				depth++
			case ')', ']', '}':
				if depth > 0 {
					depth--
				}
				out[i] = append(out[i], Span{col, col + 1, KBracket, int8(depth % 3)})
			}
			col++
		}
	}
}

func classify(t chroma.TokenType, value string) Kind {
	switch {
	case t == chroma.KeywordType:
		return KType
	case t == chroma.KeywordNamespace:
		if controlKeywords[value] {
			return KKeywordControl
		}
		return KKeyword
	case t == chroma.KeywordConstant:
		if selfKeywords[value] {
			return KSelf
		}
		return KKeyword
	case t == chroma.KeywordDeclaration || t == chroma.KeywordReserved || t == chroma.KeywordPseudo || t == chroma.Keyword:
		if controlKeywords[value] {
			return KKeywordControl
		}
		if selfKeywords[value] {
			return KSelf
		}
		return KKeyword
	case t.InCategory(chroma.Keyword):
		if controlKeywords[value] {
			return KKeywordControl
		}
		return KKeyword
	case t == chroma.NameBuiltinPseudo:
		if selfKeywords[value] {
			return KSelf
		}
		return KVariable
	case t == chroma.NameBuiltin:
		if len(value) > 0 && unicode.IsUpper([]rune(value)[0]) {
			return KType
		}
		return KBuiltin
	case t == chroma.NameFunction || t == chroma.NameFunctionMagic:
		return KFunction
	case t == chroma.NameClass || t == chroma.NameException:
		return KType
	case t == chroma.NameNamespace:
		return KNamespace
	case t == chroma.NameTag:
		return KTag
	case t == chroma.NameAttribute:
		return KAttribute
	case t == chroma.NameDecorator:
		return KDecorator
	case t == chroma.NameConstant:
		return KConstant
	case t == chroma.NameLabel:
		return KLabel
	case t == chroma.NameProperty:
		return KProperty
	case t == chroma.NameVariable || t == chroma.NameVariableInstance || t == chroma.NameVariableGlobal || t == chroma.NameVariableClass:
		return KVariable
	case t == chroma.NameVariableMagic:
		return KSelf
	case t == chroma.NameEntity:
		return KConstant
	case t.InCategory(chroma.Name):
		return KVariable
	case t == chroma.LiteralStringEscape || t == chroma.LiteralStringInterpol:
		return KStringEscape
	case t == chroma.LiteralStringRegex:
		return KRegex
	case t.InSubCategory(chroma.LiteralString) || t == chroma.LiteralString:
		return KString
	case t.InSubCategory(chroma.LiteralNumber):
		return KNumber
	case t == chroma.LiteralDate || t == chroma.Literal:
		return KString
	case t.InSubCategory(chroma.CommentPreproc):
		return KPreproc
	case t.InCategory(chroma.Comment):
		return KComment
	case t == chroma.OperatorWord:
		if controlKeywords[value] {
			return KKeywordControl
		}
		return KKeyword
	case t.InCategory(chroma.Operator):
		return KOperator
	case t.InCategory(chroma.Punctuation):
		return KPunctuation
	case t == chroma.GenericHeading || t == chroma.GenericSubheading:
		return KHeading
	case t == chroma.GenericStrong:
		return KBold
	case t == chroma.GenericEmph:
		return KItalic
	case t == chroma.GenericInserted:
		return KInserted
	case t == chroma.GenericDeleted:
		return KDeleted
	case t == chroma.GenericError || t == chroma.Error:
		return KInvalid
	case t == chroma.GenericPrompt || t == chroma.GenericOutput:
		return KComment
	case t == chroma.GenericUnderline:
		return KLink
	}
	return KText
}

// refine applies VS Code-like heuristics on top of the lexer output:
// calls become functions, member accesses become properties, PascalCase names become types.
func (h *Highlighter) refine(toks []rawTok) {
	isName := func(k Kind, tt chroma.TokenType) bool {
		return k == KVariable && (tt == chroma.Name || tt == chroma.NameOther || tt == chroma.NameVariable || tt == chroma.NameProperty || tt == chroma.NameAttribute || tt == chroma.NameBuiltinPseudo)
	}
	next := func(i int) int {
		for j := i + 1; j < len(toks); j++ {
			if strings.TrimSpace(toks[j].value) != "" {
				return j
			}
			if strings.Contains(toks[j].value, "\n") {
				return -1
			}
		}
		return -1
	}
	prev := func(i int) int {
		for j := i - 1; j >= 0; j-- {
			if strings.TrimSpace(toks[j].value) != "" {
				return j
			}
		}
		return -1
	}
	jsonLike := h.lang != nil && (h.lang.ID == "json" || h.lang.ID == "jsonc")
	for i := range toks {
		t := &toks[i]
		if jsonLike && t.kind == KTag {
			t.kind = KProperty
			continue
		}
		if jsonLike {
			continue
		}
		if t.kind == KProperty && t.ttype == chroma.NameProperty {
			continue
		}
		if !isName(t.kind, t.ttype) && t.kind != KProperty {
			continue
		}
		n := next(i)
		p := prev(i)
		afterDot := p >= 0 && (strings.HasSuffix(toks[p].value, ".") || strings.HasSuffix(toks[p].value, "?.") || strings.HasSuffix(toks[p].value, "->"))
		if n >= 0 && strings.HasPrefix(toks[n].value, "(") {
			t.kind = KFunction
			continue
		}
		if afterDot {
			t.kind = KProperty
			continue
		}
		rs := []rune(t.value)
		if len(rs) > 1 && unicode.IsUpper(rs[0]) {
			allCaps := true
			for _, r := range rs {
				if unicode.IsLower(r) {
					allCaps = false
					break
				}
			}
			if allCaps {
				t.kind = KConstant
				continue
			}
			// "Name string" is a declaration, not a type reference
			if n >= 0 && (isName(toks[n].kind, toks[n].ttype) || toks[n].kind == KType) {
				continue
			}
			t.kind = KType
			continue
		}
		if n >= 0 && (toks[n].value == ":" || strings.HasPrefix(toks[n].value, ": ")) && p >= 0 && (toks[p].value == "{" || toks[p].value == "," || strings.HasSuffix(toks[p].value, "{") || strings.HasSuffix(toks[p].value, ",")) {
			t.kind = KProperty
		}
	}
}
