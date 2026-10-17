package ui

import (
	"strconv"
	"strings"

	"tuicode/internal/lsp"
)

var emmetLangs = map[string]bool{"html": true, "php": true, "vue": true, "svelte": true, "javascriptreact": true, "typescriptreact": true, "xml": false}

var voidTags = map[string]bool{"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true, "area": true, "base": true, "col": true, "source": true, "track": true, "wbr": true, "embed": true}

// defaultAttrs mirrors Emmet's common snippets for bare tags.
var defaultAttrs = map[string]string{
	"a": `href="$1"`, "img": `src="$1" alt="$2"`, "link": `rel="stylesheet" href="$1"`, "script": `src="$1"`,
	"input": `type="$1"`, "form": `action="$1"`, "label": `for="$1"`, "iframe": `src="$1" frameborder="0"`,
}

var inlineTags = map[string]bool{"a": true, "span": true, "b": true, "i": true, "em": true, "strong": true, "code": true, "small": true, "label": true, "abbr": true, "sub": true, "sup": true, "mark": true, "u": true, "time": true, "img": true, "input": true, "br": true}

// inlineChildren reports whether children can be rendered on the parent's line, like Emmet does.
func inlineChildren(n *emmetNode) bool {
	if len(n.children) == 0 {
		return false
	}
	for _, c := range n.children {
		if !inlineTags[c.tag] || len(c.children) > 0 || c.repeat > 1 {
			return false
		}
	}
	return true
}

type emmetNode struct {
	tag      string
	id       string
	classes  []string
	attrs    []string
	text     string
	repeat   int
	children []*emmetNode
}

// parseEmmet parses a small subset of Emmet: tag#id.class[attr=v]{text}*N with > + and ^.
func parseEmmet(abbr string) ([]*emmetNode, bool) {
	var roots []*emmetNode
	stack := [][]*emmetNode{} // parent chains
	siblings := &roots
	var last *emmetNode
	i := 0
	rs := []rune(abbr)
	readName := func() string {
		s := i
		for i < len(rs) && (isWordRune(rs[i]) || rs[i] == '-' || rs[i] == ':' || rs[i] == '$') {
			i++
		}
		return string(rs[s:i])
	}
	parents := []*emmetNode{}
	for i < len(rs) {
		c := rs[i]
		switch {
		case c == '>':
			if last == nil {
				return nil, false
			}
			parents = append(parents, last)
			stack = append(stack, *siblings)
			siblings = &last.children
			last = nil
			i++
		case c == '+':
			last = nil
			i++
		case c == '^':
			if len(parents) == 0 {
				return nil, false
			}
			parents = parents[:len(parents)-1]
			if len(parents) == 0 {
				siblings = &roots
			} else {
				siblings = &parents[len(parents)-1].children
			}
			last = nil
			i++
		default:
			n := &emmetNode{repeat: 1}
			n.tag = readName()
			for i < len(rs) {
				switch rs[i] {
				case '#':
					i++
					n.id = readName()
					continue
				case '.':
					i++
					n.classes = append(n.classes, readName())
					continue
				case '[':
					end := strings.IndexRune(string(rs[i:]), ']')
					if end < 0 {
						return nil, false
					}
					for _, a := range strings.Fields(string(rs[i+1 : i+end])) {
						if k, v, ok := strings.Cut(a, "="); ok {
							n.attrs = append(n.attrs, k+`="`+strings.Trim(v, `"'`)+`"`)
						} else {
							n.attrs = append(n.attrs, a+`=""`)
						}
					}
					i += end + 1
					continue
				case '{':
					end := strings.IndexRune(string(rs[i:]), '}')
					if end < 0 {
						return nil, false
					}
					n.text = string(rs[i+1 : i+end])
					i += end + 1
					continue
				case '*':
					i++
					s := i
					for i < len(rs) && rs[i] >= '0' && rs[i] <= '9' {
						i++
					}
					cnt, err := strconv.Atoi(string(rs[s:i]))
					if err != nil || cnt < 1 || cnt > 100 {
						return nil, false
					}
					n.repeat = cnt
					continue
				}
				break
			}
			if n.tag == "" {
				if n.id == "" && len(n.classes) == 0 {
					return nil, false
				}
				n.tag = "div"
			}
			*siblings = append(*siblings, n)
			last = n
			if i < len(rs) && !strings.ContainsRune(">+^", rs[i]) {
				return nil, false
			}
		}
	}
	_ = stack
	return roots, len(roots) > 0
}

// expandEmmet renders nodes as a snippet with tab stops.
func expandEmmet(nodes []*emmetNode, jsx bool) string {
	stop := 0
	nextStop := func() string {
		stop++
		return "$" + strconv.Itoa(stop)
	}
	var render func(n []*emmetNode, depth int) string
	render = func(nodes []*emmetNode, depth int) string {
		var sb strings.Builder
		indent := strings.Repeat("\t", depth)
		for _, n := range nodes {
			for r := 1; r <= n.repeat; r++ {
				num := strconv.Itoa(r)
				attrs := []string{}
				if n.id != "" {
					attrs = append(attrs, `id="`+strings.ReplaceAll(n.id, "$", num)+`"`)
				}
				if len(n.classes) > 0 {
					name := "class"
					if jsx {
						name = "className"
					}
					attrs = append(attrs, name+`="`+strings.ReplaceAll(strings.Join(n.classes, " "), "$", num)+`"`)
				}
				for _, a := range n.attrs {
					attrs = append(attrs, strings.ReplaceAll(a, `""`, `"`+nextStop()+`"`))
				}
				if len(attrs) == 0 && len(n.attrs) == 0 {
					if d, ok := defaultAttrs[n.tag]; ok {
						d = strings.ReplaceAll(d, "$2", "${STOP2}")
						d = strings.ReplaceAll(d, "$1", nextStop())
						if strings.Contains(d, "${STOP2}") {
							d = strings.ReplaceAll(d, "${STOP2}", nextStop())
						}
						attrs = append(attrs, d)
					}
				}
				open := "<" + n.tag
				if len(attrs) > 0 {
					open += " " + strings.Join(attrs, " ")
				}
				if voidTags[n.tag] {
					if jsx {
						sb.WriteString(indent + open + " />\n")
					} else {
						sb.WriteString(indent + open + ">\n")
					}
					continue
				}
				open += ">"
				text := strings.ReplaceAll(n.text, "$", num)
				if inlineChildren(n) {
					sb.WriteString(indent + open + text + strings.TrimSpace(render(n.children, 0)) + "</" + n.tag + ">\n")
				} else if len(n.children) > 0 {
					sb.WriteString(indent + open + text + "\n")
					sb.WriteString(render(n.children, depth+1))
					sb.WriteString(indent + "</" + n.tag + ">\n")
				} else {
					inner := text
					if inner == "" {
						inner = nextStop()
					}
					sb.WriteString(indent + open + inner + "</" + n.tag + ">\n")
				}
			}
		}
		return sb.String()
	}
	out := strings.TrimSuffix(render(nodes, 0), "\n")
	return out + "$0"
}

// emmetAbbreviationBefore returns the abbreviation text ending at the cursor.
func (v *EditorView) emmetAbbreviationBefore() (string, int) {
	p := v.Cursor()
	l := v.buf().Line(p.Line)
	s := p.Col
	depth := 0
	for s > 0 {
		r := l[s-1]
		if r == ']' || r == '}' {
			depth++
		} else if (r == '[' || r == '{') && depth > 0 {
			depth--
		} else if depth == 0 && !(isWordRune(r) || strings.ContainsRune("#.>+^*$-:", r)) {
			break
		}
		s--
	}
	return string(l[s:p.Col]), s
}

// emmetCompletion returns a completion item for the abbreviation before the cursor.
func (a *App) emmetCompletion(v *EditorView) *citem {
	if !emmetLangs[v.lang()] {
		return nil
	}
	if v.inStringOrComment(v.Cursor()) {
		return nil
	}
	abbr, _ := v.emmetAbbreviationBefore()
	if len(abbr) < 1 || !strings.ContainsAny(abbr, ".#>*+[{") && !knownTag(abbr) {
		return nil
	}
	nodes, ok := parseEmmet(abbr)
	if !ok {
		return nil
	}
	jsx := v.lang() == "javascriptreact" || v.lang() == "typescriptreact"
	body := expandEmmet(nodes, jsx)
	item := &lsp.CompletionItem{Label: abbr, InsertText: body, InsertTextFormat: 2, Detail: "Emmet Abbreviation", Kind: lsp.CIKSnippet}
	preview := strings.NewReplacer("$0", "", "$1", "", "$2", "", "$3", "").Replace(body)
	return &citem{item: item, label: abbr, filter: abbr, sortText: "\x00" + abbr, kind: lsp.CIKSnippet, detail: "Emmet Abbreviation", docs: "```html\n" + preview + "\n```", resolved: true, emmet: true}
}

var htmlTags = strings.Fields("a abbr address article aside audio b blockquote body button canvas caption code dd details dialog div dl dt em fieldset figcaption figure footer form h1 h2 h3 h4 h5 h6 head header hr html i iframe img input label legend li link main mark meta nav ol option p pre progress script section select small source span strong style sub summary sup table tbody td template textarea tfoot th thead time title tr u ul video br")

func knownTag(s string) bool {
	for _, t := range htmlTags {
		if t == s {
			return true
		}
	}
	return false
}
