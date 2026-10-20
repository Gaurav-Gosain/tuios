// Ported from pele src/detect.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package sketch

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// Type names a kind of diagram, as Mermaid does: "flowchart", "sequence" and so on.
type Type = types.DiagramType

// A detector is one of Mermaid's detector regexps, /^\s*keyword.../, written
// out. detect strips the leading whitespace, so each checks that the text
// starts with its keyword and that what follows is allowed.
type detector struct {
	typ Type
	kw  string
	// fold: the regexp has the i flag. Go's (?i) and this match a letter by
	// Unicode simple case folding, so "K" (U+212A) matches "k".
	fold bool
	// rest checks the text after the keyword; nil allows anything.
	rest func(rest string) bool
}

// Mermaid's detectors, in the order Mermaid registers them. The first match wins.
var detectors = []detector{
	{typ: "flowchart", kw: "flowchart-elk"},
	{typ: "mindmap", kw: "mindmap"},
	{typ: "architecture", kw: "architecture"},
	{typ: "agentflow", kw: "agentflow-beta", rest: wordEnd}, // agentflow-beta\b
	{typ: "c4", kw: "C4Context"},                            // see detect
	{typ: "kanban", kw: "kanban"},
	{typ: "class", kw: "classDiagram"},
	{typ: "er", kw: "erDiagram"},
	{typ: "gantt", kw: "gantt"},
	{typ: "info", kw: "info"},
	{typ: "pie", kw: "pie"},
	{typ: "requirement", kw: "requirement"}, // requirement(Diagram)?
	{typ: "sequence", kw: "sequenceDiagram"},
	{typ: "swimlane", kw: "swimlane-beta", rest: wordEnd}, // swimlane-beta\b
	{typ: "flowchart", kw: "graph"},                       // (graph|flowchart)
	{typ: "flowchart", kw: "flowchart"},
	{typ: "timeline", kw: "timeline"},
	{typ: "gitGraph", kw: "gitGraph"},
	{typ: "state", kw: "stateDiagram"},
	{typ: "journey", kw: "journey"},
	{typ: "quadrantChart", kw: "quadrantChart"},
	{typ: "sankey", kw: "sankey"},   // sankey(-beta)?
	{typ: "packet", kw: "packet"},   // packet(-beta)?
	{typ: "xychart", kw: "xychart"}, // xychart(-beta)?
	{typ: "block", kw: "block"},     // block(-beta)?
	{typ: "eventmodeling", kw: "eventmodeling"},
	{typ: "treeView", kw: "treeView-beta"},
	{typ: "radar", kw: "radar-beta"},
	// /ishikawa(-beta)?\b/i: "-" is not a word character, so \b holds
	// after "ishikawa" whenever "-beta" could follow it.
	{typ: "ishikawa", kw: "ishikawa", fold: true, rest: wordEnd},
	{typ: "treemap", kw: "treemap"},
	{typ: "railroad", kw: "railroad-", fold: true, rest: railroadTail}, // railroad-(?:ebnf-|abnf-|peg-)?beta
	{typ: "venn", kw: "venn-beta"},
	{typ: "wardley", kw: "wardley-beta", fold: true},
	{typ: "cynefin", kw: "cynefin-beta", rest: func(r string) bool { return spaceOrEnd(r, ':') }}, // (?:[\s:]|$)
	{typ: "usecase", kw: "usecase-beta", rest: func(r string) bool { return spaceOrEnd(r, -1) }},  // (?:\s|$)
}

// detectPrefix bounds the text a detector is run on. Every detector but C4's
// reads only the keyword after the leading whitespace.
const detectPrefix = 64

// /^\s*C4Context|C4Container|.../ matches the last four anywhere.
var c4Anywhere = []string{"C4Container", "C4Component", "C4Dynamic", "C4Deployment"}

func detect(cleaned string) (Type, bool) {
	start := len(cleaned)
	for i, r := range cleaned {
		if !js.IsSpace(r) {
			start = i
			break
		}
	}
	head := cleaned[start:]
	if len(head) > detectPrefix {
		head = head[:detectPrefix]
	}
	for _, d := range detectors {
		if d.match(head) {
			return d.typ, true
		}
		if d.typ == "c4" {
			for _, w := range c4Anywhere {
				if strings.Contains(cleaned, w) {
					return d.typ, true
				}
			}
		}
	}
	return "", false
}

func (d *detector) match(head string) bool {
	var n int
	if d.fold {
		var ok bool
		if n, ok = foldPrefix(head, d.kw); !ok {
			return false
		}
	} else {
		if !strings.HasPrefix(head, d.kw) {
			return false
		}
		n = len(d.kw)
	}
	return d.rest == nil || d.rest(head[n:])
}

// foldPrefix reports whether s starts with the ASCII keyword kw under Unicode
// simple case folding, as a (?i) regexp literal matches, and how many bytes of
// s that takes.
func foldPrefix(s, kw string) (int, bool) {
	n := 0
	for i := 0; i < len(kw); i++ {
		if n >= len(s) {
			return 0, false
		}
		r, size := utf8.DecodeRuneInString(s[n:])
		if !foldEqual(r, rune(kw[i])) {
			return 0, false
		}
		n += size
	}
	return n, true
}

// foldEqual reports whether r is k or in k's simple case folding orbit.
func foldEqual(r, k rune) bool {
	if r == k {
		return true
	}
	for f := unicode.SimpleFold(k); f != k; f = unicode.SimpleFold(f) {
		if r == f {
			return true
		}
	}
	return false
}

// wordEnd is \b after a keyword that ends in a word character: the next byte
// is not an ASCII word character.
func wordEnd(rest string) bool {
	if rest == "" {
		return true
	}
	c := rest[0]
	return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_')
}

// spaceOrEnd is (?:[\s<also>]|$).
func spaceOrEnd(rest string, also rune) bool {
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return r == also || js.IsSpace(r)
}

// railroadTail is (?:ebnf-|abnf-|peg-)?beta under the i flag.
func railroadTail(rest string) bool {
	for _, kw := range [...]string{"ebnf-beta", "abnf-beta", "peg-beta", "beta"} {
		if _, ok := foldPrefix(rest, kw); ok {
			return true
		}
	}
	return false
}
