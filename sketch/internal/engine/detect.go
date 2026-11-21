package engine

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

type detected struct {
	typ Type
	mt  MermaidType
}

type detector struct {
	typ  Type
	mt   MermaidType
	kw   string
	fold bool
	rest func(rest string) bool
}

var detectors = []detector{
	{typ: "flowchart", mt: "flowchart-elk", kw: "flowchart-elk"},
	{typ: "mindmap", mt: "mindmap", kw: "mindmap"},
	{typ: "architecture", mt: "architecture", kw: "architecture"},
	{typ: "agentflow", mt: "agentflow", kw: "agentflow-beta", rest: wordEnd},
	{typ: "c4", mt: "c4", kw: "C4Context"},
	{typ: "kanban", mt: "kanban", kw: "kanban"},
	{typ: "class", mt: "classDiagram", kw: "classDiagram"},
	{typ: "er", mt: "er", kw: "erDiagram"},
	{typ: "gantt", mt: "gantt", kw: "gantt"},
	{typ: "info", mt: "info", kw: "info"},
	{typ: "pie", mt: "pie", kw: "pie"},
	{typ: "requirement", mt: "requirement", kw: "requirement"},
	{typ: "sequence", mt: "sequence", kw: "sequenceDiagram"},
	{typ: "swimlane", mt: "swimlane", kw: "swimlane-beta", rest: wordEnd},
	{typ: "flowchart", mt: "flowchart-v2", kw: "graph"},
	{typ: "flowchart", mt: "flowchart-v2", kw: "flowchart"},
	{typ: "timeline", mt: "timeline", kw: "timeline"},
	{typ: "gitGraph", mt: "gitGraph", kw: "gitGraph"},
	{typ: "state", mt: "stateDiagram", kw: "stateDiagram"},
	{typ: "journey", mt: "journey", kw: "journey"},
	{typ: "quadrantChart", mt: "quadrantChart", kw: "quadrantChart"},
	{typ: "sankey", mt: "sankey", kw: "sankey"},
	{typ: "packet", mt: "packet", kw: "packet"},
	{typ: "xychart", mt: "xychart", kw: "xychart"},
	{typ: "block", mt: "block", kw: "block"},
	{typ: "eventmodeling", mt: "eventmodeling", kw: "eventmodeling"},
	{typ: "treeView", mt: "treeView", kw: "treeView-beta"},
	{typ: "radar", mt: "radar", kw: "radar-beta"},
	{typ: "ishikawa", mt: "ishikawa", kw: "ishikawa", fold: true, rest: wordEnd},
	{typ: "treemap", mt: "treemap", kw: "treemap"},
	{typ: "railroad", mt: "railroad", kw: "railroad-beta", fold: true},
	{typ: "railroad", mt: "railroadEbnf", kw: "railroad-ebnf-beta", fold: true},
	{typ: "railroad", mt: "railroadAbnf", kw: "railroad-abnf-beta", fold: true},
	{typ: "railroad", mt: "railroadPeg", kw: "railroad-peg-beta", fold: true},
	{typ: "venn", mt: "venn", kw: "venn-beta"},
	{typ: "wardley", mt: "wardley", kw: "wardley-beta", fold: true},
	{typ: "cynefin", mt: "cynefin", kw: "cynefin-beta", rest: func(r string) bool { return spaceOrEnd(r, ':') }},
	{typ: "usecase", mt: "usecase", kw: "usecase-beta", rest: func(r string) bool { return spaceOrEnd(r, -1) }},
}

const detectPrefix = 64

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

func wordEnd(rest string) bool {
	if rest == "" {
		return true
	}
	c := rest[0]
	return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_')
}

func spaceOrEnd(rest string, also rune) bool {
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return r == also || js.IsSpace(r)
}
