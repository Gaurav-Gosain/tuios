// Ported from pele src/detect.ts at 8aff183be57021b533f319a29341a529be934b08.

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

// MermaidType is the diagram type as Mermaid's detectType reports it, such as
// "flowchart-v2" for `graph TD` and "classDiagram" for `classDiagram-v2`.
type MermaidType = types.MermaidType

// detected is a diagram type and the name Mermaid gives it.
type detected struct {
	typ Type
	mt  MermaidType
}

// A detector is one of Mermaid's detector regexps, /^\s*keyword.../, written
// out. detect strips the leading whitespace, so each checks that the text
// starts with its keyword and that what follows is allowed.
type detector struct {
	typ Type
	mt  MermaidType
	kw  string
	// fold: the regexp has the i flag. Go's (?i) and this match a letter by
	// Unicode simple case folding, so "K" (U+212A) matches "k".
	fold bool
	// rest checks the text after the keyword; nil allows anything.
	rest func(rest string) bool
}

// Mermaid's detectors, in the order Mermaid registers them. The first match wins.
var detectors = []detector{
	{typ: "flowchart", mt: "flowchart-elk", kw: "flowchart-elk"},
	{typ: "mindmap", mt: "mindmap", kw: "mindmap"},
	{typ: "architecture", mt: "architecture", kw: "architecture"},
	{typ: "agentflow", mt: "agentflow", kw: "agentflow-beta", rest: wordEnd}, // agentflow-beta\b
	{typ: "c4", mt: "c4", kw: "C4Context"},                                   // see c4Keywords
	{typ: "kanban", mt: "kanban", kw: "kanban"},
	{typ: "class", mt: "classDiagram", kw: "classDiagram"},
	{typ: "er", mt: "er", kw: "erDiagram"},
	{typ: "gantt", mt: "gantt", kw: "gantt"},
	{typ: "info", mt: "info", kw: "info"},
	{typ: "pie", mt: "pie", kw: "pie"},
	{typ: "requirement", mt: "requirement", kw: "requirement"}, // requirement(Diagram)?
	{typ: "sequence", mt: "sequence", kw: "sequenceDiagram"},
	{typ: "swimlane", mt: "swimlane", kw: "swimlane-beta", rest: wordEnd}, // swimlane-beta\b
	{typ: "flowchart", mt: "flowchart-v2", kw: "graph"},                   // (graph|flowchart)
	{typ: "flowchart", mt: "flowchart-v2", kw: "flowchart"},
	{typ: "timeline", mt: "timeline", kw: "timeline"},
	{typ: "gitGraph", mt: "gitGraph", kw: "gitGraph"},
	{typ: "state", mt: "stateDiagram", kw: "stateDiagram"},
	{typ: "journey", mt: "journey", kw: "journey"},
	{typ: "quadrantChart", mt: "quadrantChart", kw: "quadrantChart"},
	{typ: "sankey", mt: "sankey", kw: "sankey"},    // sankey(-beta)?
	{typ: "packet", mt: "packet", kw: "packet"},    // packet(-beta)?
	{typ: "xychart", mt: "xychart", kw: "xychart"}, // xychart(-beta)?
	{typ: "block", mt: "block", kw: "block"},       // block(-beta)?
	{typ: "eventmodeling", mt: "eventmodeling", kw: "eventmodeling"},
	{typ: "treeView", mt: "treeView", kw: "treeView-beta"},
	{typ: "radar", mt: "radar", kw: "radar-beta"},
	// /ishikawa(-beta)?\b/i: "-" is not a word character, so \b holds
	// after "ishikawa" whenever "-beta" could follow it.
	{typ: "ishikawa", mt: "ishikawa", kw: "ishikawa", fold: true, rest: wordEnd},
	{typ: "treemap", mt: "treemap", kw: "treemap"},
	{typ: "railroad", mt: "railroad", kw: "railroad-beta", fold: true},
	{typ: "railroad", mt: "railroadEbnf", kw: "railroad-ebnf-beta", fold: true},
	{typ: "railroad", mt: "railroadAbnf", kw: "railroad-abnf-beta", fold: true},
	{typ: "railroad", mt: "railroadPeg", kw: "railroad-peg-beta", fold: true},
	{typ: "venn", mt: "venn", kw: "venn-beta"},
	{typ: "wardley", mt: "wardley", kw: "wardley-beta", fold: true},
	{typ: "cynefin", mt: "cynefin", kw: "cynefin-beta", rest: func(r string) bool { return spaceOrEnd(r, ':') }}, // (?:[\s:]|$)
	{typ: "usecase", mt: "usecase", kw: "usecase-beta", rest: func(r string) bool { return spaceOrEnd(r, -1) }},  // (?:\s|$)
}

// detectPrefix bounds the text a detector is run on. Every detector reads only
// the keyword after the leading whitespace.
const detectPrefix = 64

// /^\s*(?:C4Context|C4Container|...)/: the other four keywords of C4, besides
// the one in its detector.
var c4Keywords = []string{"C4Container", "C4Component", "C4Dynamic", "C4Deployment"}

func detect(cleaned string) (Type, bool) {
	d, ok := detectDiagram(cleaned)
	return d.typ, ok
}

func detectDiagram(cleaned string) (detected, bool) {
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
			return detected{d.typ, d.mt}, true
		}
		if d.typ == "c4" {
			for _, w := range c4Keywords {
				if strings.HasPrefix(head, w) {
					return detected{d.typ, d.mt}, true
				}
			}
		}
	}
	return detected{}, false
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
