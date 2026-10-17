package ui

import (
	"regexp"
	"strings"
)

var cssEmmetLangs = map[string]bool{"css": true, "scss": true, "less": true, "sass": false, "stylus": false}

// cssKeywords are complete Emmet CSS abbreviations.
var cssKeywords = map[string]string{
	"d": "display: block", "db": "display: block", "dn": "display: none", "di": "display: inline", "dib": "display: inline-block",
	"df": "display: flex", "dif": "display: inline-flex", "dg": "display: grid", "dig": "display: inline-grid", "dt": "display: table",
	"pos": "position: relative", "posr": "position: relative", "posa": "position: absolute", "posf": "position: fixed", "poss": "position: sticky",
	"fl": "float: left", "fr": "float: right", "fn": "float: none", "cl": "clear: both",
	"jcc": "justify-content: center", "jcsb": "justify-content: space-between", "jcsa": "justify-content: space-around", "jcse": "justify-content: space-evenly",
	"jcfs": "justify-content: flex-start", "jcfe": "justify-content: flex-end", "jcs": "justify-content: start", "jce": "justify-content: end",
	"aic": "align-items: center", "aifs": "align-items: flex-start", "aife": "align-items: flex-end", "ais": "align-items: stretch", "aib": "align-items: baseline",
	"acc": "align-content: center", "acsb": "align-content: space-between", "asc": "align-self: center", "asfs": "align-self: flex-start", "asfe": "align-self: flex-end",
	"fdc": "flex-direction: column", "fdr": "flex-direction: row", "fdcr": "flex-direction: column-reverse", "fdrr": "flex-direction: row-reverse",
	"fxw": "flex-wrap: wrap", "fww": "flex-wrap: wrap", "fxwn": "flex-wrap: nowrap", "fx1": "flex: 1", "fxa": "flex: auto", "fxn": "flex: none",
	"tac": "text-align: center", "tal": "text-align: left", "tar": "text-align: right", "taj": "text-align: justify",
	"tdn": "text-decoration: none", "tdu": "text-decoration: underline", "tdl": "text-decoration: line-through",
	"ttu": "text-transform: uppercase", "ttl": "text-transform: lowercase", "ttc": "text-transform: capitalize", "ttn": "text-transform: none",
	"fwb": "font-weight: bold", "fwn": "font-weight: normal", "fsi": "font-style: italic", "fsn": "font-style: normal",
	"ovh": "overflow: hidden", "ova": "overflow: auto", "ovs": "overflow: scroll", "ovv": "overflow: visible",
	"ovxh": "overflow-x: hidden", "ovxa": "overflow-x: auto", "ovyh": "overflow-y: hidden", "ovya": "overflow-y: auto",
	"curp": "cursor: pointer", "curd": "cursor: default", "cur": "cursor: pointer", "curt": "cursor: text", "curm": "cursor: move",
	"vh": "visibility: hidden", "vv": "visibility: visible", "wsnw": "white-space: nowrap", "wsn": "white-space: normal", "wsp": "white-space: pre",
	"usn": "user-select: none", "pen": "pointer-events: none", "bxzbb": "box-sizing: border-box", "bxz": "box-sizing: border-box",
	"bdn": "border: none", "bgn": "background: none", "lisn": "list-style: none", "olnn": "outline: none", "oln": "outline: none",
	"con": "content: '$1'", "ma": "margin: auto", "mxa": "margin-inline: auto", "ofc": "object-fit: cover", "ofcn": "object-fit: contain",
	"tov": "text-overflow: ellipsis", "toe": "text-overflow: ellipsis", "vam": "vertical-align: middle", "vat": "vertical-align: top",
}

// cssProps maps Emmet property aliases to CSS properties; the value follows the alias.
var cssProps = map[string]string{
	"m": "margin", "mt": "margin-top", "mr": "margin-right", "mb": "margin-bottom", "ml": "margin-left",
	"p": "padding", "pt": "padding-top", "pr": "padding-right", "pb": "padding-bottom", "pl": "padding-left",
	"w": "width", "h": "height", "maw": "max-width", "mah": "max-height", "miw": "min-width", "mih": "min-height",
	"t": "top", "r": "right", "b": "bottom", "l": "left", "z": "z-index", "zi": "z-index", "ins": "inset",
	"g": "gap", "gap": "gap", "rg": "row-gap", "cg": "column-gap", "fx": "flex", "fxg": "flex-grow", "fxs": "flex-shrink", "fxb": "flex-basis", "ord": "order",
	"gtc": "grid-template-columns", "gtr": "grid-template-rows", "gc": "grid-column", "gr": "grid-row",
	"fz": "font-size", "fs": "font-size", "fw": "font-weight", "ff": "font-family", "lh": "line-height", "lts": "letter-spacing",
	"c": "color", "bg": "background", "bgc": "background-color", "bgi": "background-image", "bdc": "border-color",
	"bd": "border", "bdt": "border-top", "bdr": "border-right", "bdb": "border-bottom", "bdl": "border-left", "bdrs": "border-radius", "br": "border-radius", "bdw": "border-width",
	"op": "opacity", "o": "opacity", "trf": "transform", "trs": "transition", "bxsh": "box-shadow", "tsh": "text-shadow",
	"ti": "text-indent", "ol": "outline", "fil": "fill", "stk": "stroke", "ar": "aspect-ratio",
}

// unitless properties take numbers as they are.
var cssUnitless = map[string]bool{"z-index": true, "opacity": true, "flex": true, "flex-grow": true, "flex-shrink": true, "order": true, "font-weight": true, "line-height": true}

var cssValueRe = regexp.MustCompile(`^(-?\d*\.?\d+)([a-z%]*)$`)

// expandCSSEmmet expands an Emmet CSS abbreviation into a snippet, or returns false.
func expandCSSEmmet(abbr string) (string, bool) {
	important := strings.HasSuffix(abbr, "!")
	abbr = strings.TrimSuffix(abbr, "!")
	if abbr == "" {
		return "", false
	}
	finish := func(decl string) string {
		if important {
			decl += " !important"
		}
		if !strings.Contains(decl, "$") {
			decl += "$0"
		}
		return decl + ";"
	}
	if kw, ok := cssKeywords[abbr]; ok {
		return finish(kw), true
	}
	// alias:value form, like pos:a
	if prop, val, ok := strings.Cut(abbr, ":"); ok {
		if kw, ok := cssKeywords[prop+val]; ok {
			return finish(kw), true
		}
		if p, ok := cssProps[prop]; ok && val != "" {
			return finish(p + ": " + val), true
		}
		return "", false
	}
	// longest alias followed by a value
	for n := min(len(abbr), 4); n >= 1; n-- {
		prop, ok := cssProps[abbr[:n]]
		if !ok {
			continue
		}
		rest := abbr[n:]
		if rest == "" {
			return finish(prop + ": $1"), true
		}
		val, ok := cssEmmetValue(prop, rest)
		if !ok {
			continue
		}
		return finish(prop + ": " + val), true
	}
	return "", false
}

// cssEmmetValue converts "10-20", "-5", "100p", "1.5e" or "#f" into CSS values.
func cssEmmetValue(prop, s string) (string, bool) {
	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		switch len(hex) {
		case 1:
			hex = strings.Repeat(hex, 3)
		case 2:
			hex = strings.Repeat(hex, 3)
		case 3, 6, 8:
		default:
			return "", false
		}
		for _, r := range hex {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return "", false
			}
		}
		return "#" + hex, true
	}
	// split on "-" while keeping negative numbers: "10-20", "-10", "10--5"
	var parts []string
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '-' && cur != "" {
			parts = append(parts, cur)
			cur = ""
			continue
		}
		cur += string(s[i])
	}
	parts = append(parts, cur)
	var out []string
	for _, p := range parts {
		if p == "a" {
			out = append(out, "auto")
			continue
		}
		m := cssValueRe.FindStringSubmatch(p)
		if m == nil {
			return "", false
		}
		num, unit := m[1], m[2]
		switch unit {
		case "":
			if !cssUnitless[prop] && num != "0" {
				unit = "px"
			}
		case "p":
			unit = "%"
		case "e":
			unit = "em"
		case "r":
			unit = "rem"
		case "x":
			unit = "ex"
		case "px", "em", "rem", "vh", "vw", "vmin", "vmax", "fr", "ch", "deg", "s", "ms", "pt", "%":
		default:
			return "", false
		}
		out = append(out, num+unit)
	}
	return strings.Join(out, " "), true
}
