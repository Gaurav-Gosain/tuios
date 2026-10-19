// Ported from pele src/diagrams/flowchart/shapes.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package flowchart

// Mermaid's documented shape names: the short name first, then its aliases.
var SHAPES = [][]string{
	{"rect", "proc", "process", "rectangle", "squareRect"},
	{"rounded", "event", "roundedRect"},
	{"stadium", "terminal", "pill"},
	{"fr-rect", "subprocess", "subproc", "framed-rectangle", "subroutine"},
	{"cyl", "db", "database", "cylinder"},
	{"datastore", "data-store"},
	{"folder", "directory"},
	{"bucket"},
	{"console"},
	{"browser"},
	{"person"},
	{"circle", "circ"},
	{"bang"},
	{"cloud"},
	{"diam", "decision", "diamond", "question"},
	{"hex", "hexagon", "prepare"},
	{"lean-r", "lean-right", "in-out", "lean_right"},
	{"lean-l", "lean-left", "out-in", "lean_left"},
	{"trap-b", "priority", "trapezoid-bottom", "trapezoid"},
	{"trap-t", "manual", "trapezoid-top", "inv-trapezoid", "inv_trapezoid"},
	{"dbl-circ", "double-circle", "doublecircle"},
	{"text"},
	{"notch-rect", "card", "notched-rectangle"},
	{"lin-rect", "lined-rectangle", "lined-process", "lin-proc", "shaded-process"},
	{"sm-circ", "start", "small-circle", "stateStart"},
	{"fr-circ", "stop", "framed-circle", "stateEnd"},
	{"fork", "join", "forkJoin"},
	{"hourglass", "collate"},
	{"brace", "comment", "brace-l"},
	{"brace-r"},
	{"braces"},
	{"bolt", "com-link", "lightning-bolt"},
	{"doc", "document"},
	{"delay", "half-rounded-rectangle"},
	{"h-cyl", "das", "horizontal-cylinder"},
	{"lin-cyl", "disk", "lined-cylinder"},
	{"curv-trap", "curved-trapezoid", "display"},
	{"div-rect", "div-proc", "divided-rectangle", "divided-process"},
	{"tri", "extract", "triangle"},
	{"win-pane", "internal-storage", "window-pane"},
	{"f-circ", "junction", "filled-circle"},
	{"notch-pent", "loop-limit", "notched-pentagon"},
	{"flip-tri", "manual-file", "flipped-triangle"},
	{"sl-rect", "manual-input", "sloped-rectangle"},
	{"docs", "documents", "st-doc", "stacked-document"},
	{"st-rect", "procs", "processes", "stacked-rectangle"},
	{"bow-rect", "stored-data", "bow-tie-rectangle"},
	{"cross-circ", "summary", "crossed-circle"},
	{"tag-doc", "tagged-document"},
	{"tag-rect", "tagged-rectangle", "tag-proc", "tagged-process"},
	{"flag", "paper-tape"},
	{"odd", "rect_left_inv_arrow"},
	{"lin-doc", "lined-document"},
}

// Shapes Mermaid accepts by name without documenting them.
var INTERNAL = []string{
	"state",
	"choice",
	"note",
	"composite",
	"rectWithTitle",
	"labelRect",
	"block_arrow",
	"collapsedGroup",
	"iconSquare",
	"iconCircle",
	"icon",
	"iconRounded",
	"imageSquare",
	"anchor",
	"kanbanItem",
	"mindmapCircle",
	"defaultMindmapNode",
	"classBox",
	"erBox",
	"requirementBox",
	"usecaseActor",
	"usecaseActorHollow",
	"usecaseActorAwesome",
	"usecaseActorIcon",
	"usecaseBusiness",
	"usecaseEllipse",
	"usecaseJsonTable",
}

// Shapes produced by the bracket syntax, mapped to their documented names.
var SYNTAX = map[string]string{
	"square":  "rect",
	"round":   "rounded",
	"ellipse": "ellipse",
	"hexagon": "hex",
	"diamond": "diam",
}

var canonical = func() map[string]string {
	c := map[string]string{}
	for _, names := range SHAPES {
		for _, name := range names {
			c[name] = names[0]
		}
	}
	for _, name := range INTERNAL {
		c[name] = name
	}
	return c
}()

func isValidShape(name string) bool {
	_, ok := canonical[name]
	return ok
}

// canonicalShape gives the documented name of a shape. "" is pele's undefined.
func canonicalShape(name string) string {
	if name == "" {
		return "rect"
	}
	if v, ok := SYNTAX[name]; ok {
		return v
	}
	if v, ok := canonical[name]; ok {
		return v
	}
	return "rect"
}
