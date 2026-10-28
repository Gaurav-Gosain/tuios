// Ported from pele src/diagrams/eventmodeling/model.ts at 8aff183be57021b533f319a29341a529be934b08.

package eventmodeling

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type EntityKind = string
type LaneGroup = string

type EmLane struct {
	// Mermaid's ordering key: 0 for UI/Automation, 100 for Command/Read Model, 200 for Events,
	// and the next free number after those for each namespace.
	index     int
	label     string
	group     LaneGroup
	namespace string
}

type EmBox struct {
	// The frame number as written, such as "01".
	id        string
	kind      EntityKind
	entity    string
	name      string
	namespace string
	reset     bool
	lane      *EmLane
	// Example data from the frame or from the data block it refers to.
	data     string
	dataType string
}

type EmRelation struct {
	source, target *EmBox
}

type EmNote struct {
	frame    string
	text     string
	dataType string
}

type EmSpecification struct {
	frame             string
	given, when, then []string
}

type EventModelingModel struct {
	title    string
	accTitle string
	accDescr string
	boxes    []*EmBox
	// Sorted from top to bottom.
	lanes          []*EmLane
	relations      []EmRelation
	notes          []EmNote
	specifications []EmSpecification
	entities       []string
}

func kindOf(entityType string) EntityKind {
	switch entityType {
	case "ui":
		return "ui"
	case "pcr", "processor":
		return "processor"
	case "rmo", "readmodel":
		return "readmodel"
	case "cmd", "command":
		return "command"
	}
	return "event"
}

// group is [first index, label, label prefix for a namespace].
func group(g LaneGroup) (int, string, string) {
	switch g {
	case "trigger":
		return 0, "UI/Automation", "UI/A: "
	case "state":
		return 100, "Command/Read Model", "C/RM: "
	}
	return 200, "Events", "Stream: "
}

func groupOf(kind EntityKind) LaneGroup {
	if kind == "ui" || kind == "processor" {
		return "trigger"
	}
	if kind == "event" {
		return "stream"
	}
	return "state"
}

// blockText is the text between the outer braces, without the indentation its lines share.
func blockText(raw string) string {
	open := strings.IndexByte(raw, '{')
	close := strings.LastIndexByte(raw, '}')
	inner := ""
	if open != -1 && close > open {
		inner = raw[open+1 : close]
	} else if open == -1 && js.Len(raw) >= 2 {
		// The text between its quotes, which are one unit each.
		inner = raw[1 : len(raw)-1]
	}
	lines := strings.Split(strings.ReplaceAll(inner, "\r", ""), "\n")
	for len(lines) > 0 && js.Trim(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && js.Trim(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// Leading white space is counted in characters: every one of them is one UTF-16 unit.
	indent := math.MaxInt
	for _, line := range lines {
		if js.Trim(line) == "" {
			continue
		}
		indent = min(indent, utf8.RuneCountInString(line[:len(line)-len(js.TrimStart(line))]))
	}
	var out strings.Builder
	for k, line := range lines {
		if k > 0 {
			out.WriteByte('\n')
		}
		cut := 0
		for n := 0; n < indent && cut < len(line); n++ {
			_, size := utf8.DecodeRuneInString(line[cut:])
			cut += size
		}
		out.WriteString(js.TrimEnd(line[cut:]))
	}
	return out.String()
}

func buildEventModel(ast *EventModelAst, title string) *EventModelingModel {
	ids := map[string]bool{}
	for _, frame := range ast.frames {
		if ids[frame.name] {
			line := types.Locate(ast.source, frame.offset).Line
			types.Throw("Duplicate event modeling frame ID \""+frame.name+"\" on line "+strconv.Itoa(line), types.CodeSemantic, types.ErrorInfo{Type: "eventmodeling", Line: line})
		}
		ids[frame.name] = true
	}

	data := map[string]string{}
	for _, entity := range ast.dataEntities {
		if _, ok := data[entity.name]; !ok {
			data[entity.name] = entity.dataBlockValue
		}
	}

	// One lane per group, and one more per namespace within a group.
	laneOf := map[string]*EmLane{}
	var laneOrder []*EmLane
	next := map[LaneGroup]int{}
	lane := func(kind EntityKind, namespace string, hasNamespace bool) *EmLane {
		g := groupOf(kind)
		key := g
		if hasNamespace {
			key = g + " " + namespace
		}
		found := laneOf[key]
		if found == nil {
			first, label, prefix := group(g)
			index := first
			if hasNamespace {
				if n, ok := next[g]; ok {
					index = n
				} else {
					index = first + 1
				}
				next[g] = index + 1
				label = prefix + namespace
			}
			found = &EmLane{index: index, label: label, group: g, namespace: namespace}
			laneOf[key] = found
			laneOrder = append(laneOrder, found)
		}
		return found
	}

	var boxes []*EmBox
	var relations []EmRelation
	for index, frame := range ast.frames {
		// Only a two-part name has a namespace.
		parts := strings.Split(frame.entityIdentifier, ".")
		hasNamespace := len(parts) == 2
		namespace, name := "", frame.entityIdentifier
		if hasNamespace {
			namespace, name = parts[0], parts[1]
		}
		kind := kindOf(frame.modelEntityType)
		text := ""
		if frame.hasInline {
			text = blockText(frame.dataInlineValue)
		}
		if frame.hasReference {
			if block, ok := data[frame.dataReference]; ok {
				text = blockText(block)
			}
		}
		box := &EmBox{id: frame.name, kind: kind, entity: frame.entityIdentifier, name: name, namespace: namespace, reset: frame.reset,
			lane: lane(kind, namespace, hasNamespace), data: text, dataType: frame.dataType}
		boxes = append(boxes, box)

		if len(frame.sourceFrames) > 0 {
			// Sources connect in frame order, and only frames that already have a box.
			wanted := func(id string) bool { return slices.Contains(frame.sourceFrames, id) }
			if len(frame.sourceFrames) > 8 {
				set := make(map[string]bool, len(frame.sourceFrames))
				for _, id := range frame.sourceFrames {
					set[id] = true
				}
				wanted = func(id string) bool { return set[id] }
			}
			for _, source := range boxes {
				if wanted(source.id) {
					relations = append(relations, EmRelation{source, box})
				}
			}
		} else if index > 0 && !box.reset {
			// Otherwise the nearest earlier frame in another lane feeds this one.
			for i := index - 1; i >= 0; i-- {
				if boxes[i].lane != box.lane {
					relations = append(relations, EmRelation{boxes[i], box})
					break
				}
			}
		}
	}

	model := &EventModelingModel{boxes: boxes, relations: relations, entities: ast.modelEntities}
	if ast.title != "" {
		model.title = common.TitleValue(ast.title)
	}
	if model.title == "" {
		model.title = title
	}
	if ast.accTitle != "" {
		model.accTitle = js.TrimStart(common.AccTitleValue(ast.accTitle))
	}
	if ast.accDescr != "" {
		model.accDescr = joinLines(common.AccDescrValue(ast.accDescr))
	}
	model.lanes = slices.Clone(laneOrder)
	slices.SortStableFunc(model.lanes, func(a, b *EmLane) int { return a.index - b.index })
	for _, note := range ast.noteEntities {
		model.notes = append(model.notes, EmNote{frame: note.sourceFrame, text: blockText(note.dataBlockValue), dataType: note.dataType})
	}
	for _, gwt := range ast.gwtEntities {
		model.specifications = append(model.specifications, EmSpecification{frame: gwt.sourceFrame, given: gwt.givenStatements, when: gwt.whenStatements, then: gwt.thenStatements})
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
