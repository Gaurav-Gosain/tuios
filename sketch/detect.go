// Ported from pele src/detect.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package sketch

import (
	"regexp"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// Type names a kind of diagram, as Mermaid does: "flowchart", "sequence" and so on.
type Type = types.DiagramType

const sp = `[` + js.S + `]*`

// Mermaid's detectors, in the order Mermaid registers them. The first match wins.
var detectors = []struct {
	typ Type
	re  *regexp.Regexp
}{
	{"flowchart", regexp.MustCompile(`^` + sp + `flowchart-elk`)},
	{"mindmap", regexp.MustCompile(`^` + sp + `mindmap`)},
	{"architecture", regexp.MustCompile(`^` + sp + `architecture`)},
	{"agentflow", regexp.MustCompile(`^` + sp + `agentflow-beta\b`)},
	{"c4", regexp.MustCompile(`^` + sp + `C4Context|C4Container|C4Component|C4Dynamic|C4Deployment`)},
	{"kanban", regexp.MustCompile(`^` + sp + `kanban`)},
	{"class", regexp.MustCompile(`^` + sp + `classDiagram`)},
	{"er", regexp.MustCompile(`^` + sp + `erDiagram`)},
	{"gantt", regexp.MustCompile(`^` + sp + `gantt`)},
	{"info", regexp.MustCompile(`^` + sp + `info`)},
	{"pie", regexp.MustCompile(`^` + sp + `pie`)},
	{"requirement", regexp.MustCompile(`^` + sp + `requirement(Diagram)?`)},
	{"sequence", regexp.MustCompile(`^` + sp + `sequenceDiagram`)},
	{"swimlane", regexp.MustCompile(`^` + sp + `swimlane-beta\b`)},
	{"flowchart", regexp.MustCompile(`^` + sp + `(graph|flowchart)`)},
	{"timeline", regexp.MustCompile(`^` + sp + `timeline`)},
	{"gitGraph", regexp.MustCompile(`^` + sp + `gitGraph`)},
	{"state", regexp.MustCompile(`^` + sp + `stateDiagram`)},
	{"journey", regexp.MustCompile(`^` + sp + `journey`)},
	{"quadrantChart", regexp.MustCompile(`^` + sp + `quadrantChart`)},
	{"sankey", regexp.MustCompile(`^` + sp + `sankey(-beta)?`)},
	{"packet", regexp.MustCompile(`^` + sp + `packet(-beta)?`)},
	{"xychart", regexp.MustCompile(`^` + sp + `xychart(-beta)?`)},
	{"block", regexp.MustCompile(`^` + sp + `block(-beta)?`)},
	{"eventmodeling", regexp.MustCompile(`^` + sp + `eventmodeling`)},
	{"treeView", regexp.MustCompile(`^` + sp + `treeView-beta`)},
	{"radar", regexp.MustCompile(`^` + sp + `radar-beta`)},
	{"ishikawa", regexp.MustCompile(`(?i)^` + sp + `ishikawa(-beta)?\b`)},
	{"treemap", regexp.MustCompile(`^` + sp + `treemap`)},
	{"railroad", regexp.MustCompile(`(?i)^` + sp + `railroad-(?:ebnf-|abnf-|peg-)?beta`)},
	{"venn", regexp.MustCompile(`^` + sp + `venn-beta`)},
	{"wardley", regexp.MustCompile(`(?i)^` + sp + `wardley-beta`)},
	{"cynefin", regexp.MustCompile(`^` + sp + `cynefin-beta(?:[` + js.S + `:]|$)`)},
	{"usecase", regexp.MustCompile(`^` + sp + `usecase-beta(?:[` + js.S + `]|$)`)},
}

func detect(cleaned string) (Type, bool) {
	for _, d := range detectors {
		if d.re.MatchString(cleaned) {
			return d.typ, true
		}
	}
	return "", false
}
