// Ported from pele src/diagrams/wardley/model.ts at 8aff183be57021b533f319a29341a529be934b08.

package wardley

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type WardleyModel struct {
	WardleyBuildResult
	title    string
	accTitle string
	accDescr string
}

// toPercent: values up to 1 are fractions; larger ones are already percentages.
func toPercent(value float64, context string) float64 {
	normalized := value
	if value <= 1 {
		normalized = float64(value * 100)
	}
	if normalized < 0 || normalized > 100 {
		types.Throw(context+" must be between 0-1 (decimal) or 0-100 (percentage). Received: "+js.Num(value), types.CodeSemantic, types.ErrorInfo{Type: "wardley"})
	}
	return normalized
}

func toCoordinates(visibility, evolution float64, context string) (x, y float64) {
	return toPercent(evolution, context+" evolution"), toPercent(visibility, context+" visibility")
}

func flowFromPort(port string) Flow {
	switch port {
	case "+<>":
		return "bidirectional"
	case "+<":
		return "backward"
	case "+>":
		return "forward"
	}
	return ""
}

func flowFromArrow(arrow string) (Flow, string, bool) {
	if !strings.HasPrefix(arrow, "+") {
		return "", "", false
	}
	label, hasLabel := "", false
	if len(arrow) > 1 && arrow[1] == '\'' {
		if end := strings.IndexByte(arrow[2:], '\''); end >= 0 {
			label, hasLabel = arrow[2:2+end], true
		}
	}
	switch {
	case strings.Contains(arrow, "<>"):
		return "bidirectional", label, hasLabel
	case strings.Contains(arrow, "<"):
		return "backward", label, hasLabel
	case strings.Contains(arrow, ">"):
		return "forward", label, hasLabel
	}
	return "", label, hasLabel
}

func offsets(label *LabelAst) (*float64, *float64) {
	if label == nil {
		return nil, nil
	}
	x, y := label.offsetX, label.offsetY
	if label.negX {
		x = -x
	}
	if label.negY {
		y = -y
	}
	return &x, &y
}

// populate is Mermaid's populateDb: fills the builder from the syntax tree, in Mermaid's order.
func populate(ast *WardleyAst, db *WardleyBuilder) {
	if ast.size != nil {
		db.size = &[2]float64{ast.size[0], ast.size[1]}
	}

	if ast.hasEvolution {
		var stages []string
		var boundaries []float64
		for _, stage := range ast.stages {
			if stage.hasSecond && stage.secondName != "" {
				stages = append(stages, js.Trim(stage.name)+" / "+js.Trim(stage.secondName))
			} else {
				stages = append(stages, js.Trim(stage.name))
			}
			if stage.hasBoundary {
				boundaries = append(boundaries, stage.boundary)
			}
		}
		db.axes.stages = stages
		db.axes.stageBoundaries, db.axes.hasBoundaries = boundaries, true
	}

	for _, anchor := range ast.anchors {
		x, y := toCoordinates(anchor.visibility, anchor.evolution, "Anchor \""+anchor.name+"\"")
		db.addNode(nodeInput{id: anchor.name, label: anchor.name, x: x, y: y, className: "anchor"})
	}

	for _, component := range ast.components {
		x, y := toCoordinates(component.visibility, component.evolution, "Component \""+component.name+"\"")
		ox, oy := offsets(component.label)
		db.addNode(nodeInput{id: component.name, label: component.name, x: x, y: y, className: "component", labelOffsetX: ox, labelOffsetY: oy,
			setsTraits: true, inertia: component.inertia, strategy: component.strategy})
	}

	for _, note := range ast.notes {
		x, y := toCoordinates(note.visibility, note.evolution, "Note \""+note.text+"\"")
		db.notes = append(db.notes, WardleyNote{text: note.text, x: x, y: y})
	}

	for _, pipeline := range ast.pipelines {
		parent := db.getNode(pipeline.parent)
		if parent == nil {
			types.Throw("Pipeline \""+pipeline.parent+"\" must reference an existing component with coordinates.", types.CodeSemantic, types.ErrorInfo{Type: "wardley"})
		}
		y := parent.y
		db.startPipeline(pipeline.parent)
		for _, component := range pipeline.components {
			id := pipeline.parent + "_" + component.name
			ox, oy := offsets(component.label)
			x := toPercent(component.evolution, "Pipeline component \""+component.name+"\" evolution")
			db.addNode(nodeInput{id: id, label: component.name, x: x, y: y, className: "pipeline-component", labelOffsetX: ox, labelOffsetY: oy, setsTraits: true})
			db.addPipelineComponent(pipeline.parent, id)
		}
	}

	for _, link := range ast.links {
		dashed := link.arrow != "" && (strings.Contains(link.arrow, "-.->") || strings.Contains(link.arrow, ".-."))
		arrowFlow, arrowLabel, hasArrowLabel := flowFromArrow(link.arrow)
		flow := flowFromPort(link.fromPort)
		if flow == "" {
			flow = flowFromPort(link.toPort)
		}
		if flow == "" {
			flow = arrowFlow
		}
		label := link.linkLabel
		if hasArrowLabel {
			label = arrowLabel
		}
		db.addLink(WardleyLink{source: db.resolveNodeId(link.from), target: db.resolveNodeId(link.to), dashed: dashed, label: label, flow: flow})
	}

	for _, evolve := range ast.evolves {
		if node := db.getNode(evolve.component); node != nil {
			db.addTrend(WardleyTrend{nodeID: evolve.component, targetX: toPercent(evolve.target, "Evolve target for \""+evolve.component+"\""), targetY: node.y})
		}
	}

	if len(ast.annotations) > 0 {
		x, y := toCoordinates(ast.annotations[0].x, ast.annotations[0].y, "Annotations box")
		db.annotationsBox = &[2]float64{x, y}
	}

	for _, annotation := range ast.annotation {
		x, y := toCoordinates(annotation.x, annotation.y, "Annotation "+js.Num(annotation.number))
		db.annotations = append(db.annotations, WardleyAnnotation{number: annotation.number, coordinates: [][2]float64{{x, y}}, text: annotation.text})
	}

	for _, accelerator := range ast.accelerators {
		x, y := toCoordinates(accelerator.x, accelerator.y, "Accelerator \""+accelerator.name+"\"")
		db.accelerators = append(db.accelerators, WardleyForce{name: accelerator.name, x: x, y: y})
	}

	for _, deaccelerator := range ast.deaccelerators {
		x, y := toCoordinates(deaccelerator.x, deaccelerator.y, "Deaccelerator \""+deaccelerator.name+"\"")
		db.deaccelerators = append(db.deaccelerators, WardleyForce{name: deaccelerator.name, x: x, y: y})
	}
}

func buildWardley(ast *WardleyAst, title string) *WardleyModel {
	db := newWardleyBuilder()
	populate(ast, db)
	model := &WardleyModel{WardleyBuildResult: db.build(), title: ast.title}
	if model.title == "" {
		model.title = title
	}
	if ast.accTitle != "" {
		model.accTitle = js.TrimStart(ast.accTitle)
	}
	if ast.accDescr != "" {
		model.accDescr = joinLines(ast.accDescr)
	}
	return model
}

// joinLines is .replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}
