// Ported from pele src/diagrams/usecase/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package usecase

import (
	"slices"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type DraftLabel struct {
	text string
	typ  string
	span Span
}

// DraftProperty: keySpan is also where an error about the property points. value is a
// string or a bool.
type DraftProperty struct {
	MetadataOccurrence
	value any
}

// Named is what every mention of an id carries: where it is, and whether the id was made from a label.
type Named struct {
	id        string
	label     DraftLabel
	span      Span
	generated bool
}

type DraftElement struct {
	Named
	kind       string
	parent     *DraftBoundary
	shape      string
	metadata   []DraftProperty
	stereotype string
	classes    []string
}

type DraftBoundary struct {
	Named
	classes *[]string
}

type DraftJson struct {
	id            string
	value         *jsonObject
	propertyOrder *keyOrder
	span          Span
	classes       []string
}

type DraftEndpoint struct {
	Named
	// Whether this mention declares the element, and whether it carries a `:::` suffix.
	declares bool
	classed  bool
}

type DraftRelationship struct {
	source    DraftEndpoint
	target    DraftEndpoint
	span      Span
	id        string
	hasID     bool
	idSpan    *Span
	typ       string
	arrowType int
	label     *DraftLabel
	minlen    int
}

type draftNote struct {
	target string
	span   Span
	label  DraftLabel
}

type draftMetadata struct {
	target     string
	span       Span
	properties []DraftProperty
	statement  *GraphStatement
}

type draftTarget struct {
	id   string
	span Span
}

type draftClass struct {
	targets []draftTarget
	classes []string
}

type draftStyle struct {
	target string
	span   Span
	styles []string
}

type draftClassDef struct {
	ids    []string
	styles []string
}

// Drafts is everything the parser read, in source order, before any of it is resolved.
type Drafts struct {
	elements       []*DraftElement
	boundaries     []*DraftBoundary
	jsons          []*DraftJson
	relationships  []*DraftRelationship
	notes          []draftNote
	metadata       []draftMetadata
	classes        []draftClass
	styles         []draftStyle
	classDefs      []draftClassDef
	direction      string
	accTitle       string
	accDescription string
}

func emptyDrafts() *Drafts { return &Drafts{} }

type Origin struct {
	kind      string
	span      Span
	generated bool
	// The label a generated id was derived from.
	label string
}

type ElementState struct {
	Named
	kind           string
	parent         *DraftBoundary
	shape          string
	stereotype     string
	stereotypeSpan *Span
	classes        []string
	actorType      string
	icon           string
	hasIcon        bool
	business       *bool
}

type BoundaryState struct {
	DraftBoundary
	typ     string
	members []string
}

func pushUnique(target *[]string, values []string) {
	if len(values) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, v := range *target {
		seen[v] = true
	}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		*target = append(*target, value)
	}
}

// inferKind is which kind of element the keys of a metadata block could only belong to, for the error message.
func inferKind(properties []DraftProperty) string {
	// actor, usecase, boundary, edge, in the order a Set keeps them.
	possible := []string{"actor", "usecase", "boundary", "edge"}
	remove := func(k string) {
		if i := slices.Index(possible, k); i >= 0 {
			possible = slices.Delete(possible, i, i+1)
		}
	}
	for _, p := range properties {
		switch p.key {
		case "icon":
			possible = []string{"actor"}
		case "animate", "animation":
			possible = []string{"edge"}
		case "type":
			possible = nil
			if p.value == "rect" || p.value == "package" {
				possible = []string{"boundary"}
			} else if p.value == "normal" || p.value == "hollow" || p.value == "awesome" {
				possible = []string{"actor"}
			}
		case "business":
			remove("boundary")
			remove("edge")
		default:
			return ""
		}
	}
	if len(possible) == 1 {
		return possible[0]
	}
	return ""
}

type stylable struct {
	classes *[]string
	styles  *[]string
}

// resolveUsecase resolves what the parser read into a model, once the whole diagram is read: which ids are
// actors, use cases, boundaries, JSON nodes or edges, in what order they appear, and whether the
// statements about them agree. Ports Mermaid's UsecaseModelBuilder, with its messages.
func resolveUsecase(src string, drafts *Drafts, headerSpan Span, statements []*GraphStatement, title *string) *UsecaseModel {
	model := emptyModel()
	actors, useCases, systemBoundaries := &model.actors, &model.useCases, &model.systemBoundaries
	symbols := map[string]Origin{}
	elements := newOmap[*ElementState]()
	boundaries := newOmap[*BoundaryState]()
	edges := map[string]*Relationship{}
	// The offset at which each id is first mentioned, which decides the order of the model.
	first := map[string]int{}

	where := func(span Span) string { return locationText(src, span[0], span[1]) }

	errorAt := func(message string, span Span) *types.PeleError {
		line, column := lineColumn(src, span[0])
		return types.NewPeleError(message, types.CodeSemantic, types.ErrorInfo{Type: "usecase", Line: line, Column: column})
	}

	fail := func(message string, span Span) {
		panic(errorAt(message+" at "+where(span), span))
	}

	conflict := func(message string, current, previous Span, currentLabel, previousLabel *string) {
		suffix := func(label *string) string {
			if label == nil {
				return ""
			}
			return ` (label "` + *label + `")`
		}
		panic(errorAt(message+" at "+where(current)+suffix(currentLabel)+"; previous declaration at "+where(previous)+suffix(previousLabel), current))
	}

	invalid := func(id, kind string, property DraftProperty) {
		fail("Metadata property '"+property.key+"' is invalid for "+kind+" '"+id+"'", property.keySpan)
	}

	mention := func(id string, offset int) {
		if previous, ok := first[id]; !ok || offset < previous {
			first[id] = offset
		}
	}

	firstOf := func(id string) float64 { return float64(first[id]) }

	register := func(named *Named, kind string) {
		symbols[named.id] = Origin{kind: kind, span: named.span, generated: named.generated, label: named.label.text}
	}

	// claim: an id means one thing in the whole diagram, and an id made from a label may be declared once.
	claim := func(draft *Named, kind string) {
		origin, ok := symbols[draft.id]
		if !ok {
			return
		}
		var label, originLabel *string
		if draft.generated {
			label = &draft.label.text
		}
		if origin.generated {
			originLabel = &origin.label
		}
		if origin.kind != kind {
			conflict("ID '"+draft.id+"' is declared as both "+origin.kind+" and "+kind, draft.span, origin.span, label, originLabel)
		}
		if origin.generated || draft.generated {
			conflict("Generated ID '"+draft.id+"' collides with another declaration", draft.span, origin.span, label, originLabel)
		}
	}

	unique := func(id, kind string, span Span) {
		if previous, ok := symbols[id]; ok {
			var previousLabel *string
			if previous.generated {
				previousLabel = &previous.label
			}
			conflict("ID '"+id+"' is declared more than once ("+previous.kind+" and "+kind+")", span, previous.span, nil, previousLabel)
		}
		symbols[id] = Origin{kind: kind, span: span}
	}

	// actorProperty: a metadata statement replaces what a declaration said; two declarations have to agree.
	actorProperty := func(state *ElementState, property DraftProperty, replace bool) {
		key, value := property.key, property.value
		clash := func(previous any, has bool) {
			if !replace && has && previous != value {
				conflict("Actor '"+state.id+"' has conflicting "+key+" metadata", property.keySpan, state.span, nil, nil)
			}
		}
		str, isStr := value.(string)
		b, isBool := value.(bool)
		switch {
		case key == "type" && (value == "normal" || value == "hollow" || value == "awesome"):
			clash(state.actorType, state.actorType != "")
			state.actorType = str
		case key == "icon" && isStr:
			clash(state.icon, state.hasIcon)
			state.icon, state.hasIcon = str, true
		case key == "business" && isBool:
			if state.business != nil {
				clash(*state.business, true)
			}
			state.business = &b
		default:
			invalid(state.id, "actor", property)
		}
	}

	elementMetadata := func(state *ElementState, properties []DraftProperty, replace bool) {
		for _, property := range properties {
			if state.kind == "actor" {
				actorProperty(state, property, replace)
			} else if b, ok := property.value.(bool); ok && property.key == "business" {
				if !replace && state.business != nil && *state.business != b {
					conflict("Use case '"+state.id+"' has conflicting business metadata", property.keySpan, state.span, nil, nil)
				}
				state.business = &b
			} else {
				invalid(state.id, "usecase", property)
			}
		}
	}

	publish := func(state *ElementState) {
		business := state.business != nil && *state.business
		if state.kind == "actor" {
			useCases.del(state.id)
			typ := state.actorType
			if typ == "" {
				typ = "normal"
			}
			if state.icon != "" {
				typ = "icon"
			}
			var styles []string
			if old, ok := actors.get(state.id); ok {
				styles = old.styles
			}
			actor := &Actor{id: state.id, label: state.label.text, labelType: state.label.typ, business: business,
				classes: slices.Clone(state.classes), typ: typ, styles: styles}
			if state.icon != "" {
				actor.icon = state.icon
			}
			actor.stereotype = state.stereotype
			if state.parent != nil && state.parent.id != "" {
				actor.parentID = state.parent.id
			}
			actors.set(state.id, actor)
			return
		}
		actors.del(state.id)
		var styles []string
		if old, ok := useCases.get(state.id); ok {
			styles = old.styles
		}
		shape := state.shape
		if shape == "" {
			shape = "ellipse"
		}
		element := &UseCase{id: state.id, label: state.label.text, labelType: state.label.typ, business: business,
			classes: slices.Clone(state.classes), shape: shape, styles: styles, stereotype: state.stereotype}
		if state.parent != nil && state.parent.id != "" {
			element.parentID = state.parent.id
		}
		useCases.set(state.id, element)
	}

	declareElement := func(draft *DraftElement) {
		claim(&draft.Named, draft.kind)
		var parent *DraftBoundary
		if draft.parent != nil && draft.parent.id != "" {
			parent = draft.parent
		}
		existing, ok := elements.get(draft.id)
		if !ok {
			state := &ElementState{Named: draft.Named, kind: draft.kind, classes: slices.Clone(draft.classes), parent: parent,
				shape: draft.shape, stereotype: draft.stereotype}
			if draft.stereotype != "" {
				s := draft.span
				state.stereotypeSpan = &s
			}
			elementMetadata(state, draft.metadata, false)
			elements.set(draft.id, state)
			register(&draft.Named, draft.kind)
			return
		}
		if existing.label.text != draft.label.text || existing.label.typ != draft.label.typ {
			conflict("ID '"+draft.id+"' has conflicting labels", draft.span, existing.span, nil, nil)
		}
		if draft.shape != "" && existing.shape != "" && draft.shape != existing.shape {
			conflict("Use case '"+draft.id+"' has conflicting shapes", draft.span, existing.span, nil, nil)
		}
		// A boundary whose title makes an empty id is no parent, but it still takes the place of one.
		if parent != nil && existing.parent != nil && existing.parent.id != "" && parent.id != existing.parent.id {
			conflict("Element '"+draft.id+"' belongs to more than one system boundary", parent.span, existing.parent.span, nil, nil)
		}
		if draft.stereotype != "" && existing.stereotype != "" && draft.stereotype != existing.stereotype {
			previous := existing.span
			if existing.stereotypeSpan != nil {
				previous = *existing.stereotypeSpan
			}
			conflict("Element '"+draft.id+"' has conflicting stereotypes", draft.span, previous, nil, nil)
		}
		if existing.shape == "" {
			existing.shape = draft.shape
		}
		if existing.parent == nil {
			existing.parent = draft.parent
		}
		if existing.stereotype == "" {
			existing.stereotype = draft.stereotype
		}
		if existing.stereotypeSpan == nil && draft.stereotype != "" {
			s := draft.span
			existing.stereotypeSpan = &s
		}
		pushUnique(&existing.classes, draft.classes)
		elementMetadata(existing, draft.metadata, false)
	}

	declareBoundary := func(draft *DraftBoundary) {
		claim(&draft.Named, "boundary")
		if existing, ok := boundaries.get(draft.id); ok {
			if existing.label.text != draft.label.text || existing.label.typ != draft.label.typ {
				conflict("Boundary '"+draft.id+"' has conflicting titles", draft.span, existing.span, nil, nil)
			}
			pushUnique(existing.classes, *draft.classes)
			return
		}
		classes := slices.Clone(*draft.classes)
		boundaries.set(draft.id, &BoundaryState{DraftBoundary: DraftBoundary{Named: draft.Named, classes: &classes}, members: []string{}})
		register(&draft.Named, "boundary")
	}

	// endpoint: an end of a relationship that nothing declares becomes a use case.
	endpoint := func(end *DraftEndpoint) string {
		if end.classed && !end.declares {
			fail("Relationship endpoint '"+end.id+"' uses ::: without declaring the node", end.span)
		}
		if origin, ok := symbols[end.id]; ok {
			return origin.kind
		}
		elements.set(end.id, &ElementState{Named: end.Named, kind: "usecase", shape: "ellipse", classes: []string{}})
		register(&end.Named, "usecase")
		mention(end.id, end.span[0])
		return "usecase"
	}

	stylableOf := func(id string, span Span) stylable {
		kind := ""
		if o, ok := symbols[id]; ok {
			kind = o.kind
		}
		switch kind {
		case "actor":
			if t, ok := actors.get(id); ok {
				return stylable{&t.classes, &t.styles}
			}
		case "usecase":
			if t, ok := useCases.get(id); ok {
				return stylable{&t.classes, &t.styles}
			}
		case "boundary":
			if t, ok := systemBoundaries.get(id); ok {
				return stylable{&t.classes, &t.styles}
			}
		case "json":
			if t, ok := model.jsonNodes.get(id); ok {
				return stylable{&t.classes, &t.styles}
			}
		default:
			if t, ok := edges[id]; ok {
				return stylable{&t.classes, &t.styles}
			}
		}
		fail("Class/style target '"+id+"' is unresolved or anonymous", span)
		return stylable{}
	}

	// A declaration is an element, a boundary, a JSON node or an edge id, by its index in drafts.
	type declaration struct {
		offset int
		kind   int
		index  int
	}
	declarations := make([]declaration, 0, len(drafts.elements)+len(drafts.boundaries)+len(drafts.jsons)+len(drafts.relationships))
	for _, r := range drafts.relationships {
		mention(r.source.id, r.source.span[0])
		mention(r.target.id, r.target.span[0])
	}
	for k, draft := range drafts.elements {
		mention(draft.id, draft.span[0])
		declarations = append(declarations, declaration{draft.span[0], 0, k})
	}
	for k, draft := range drafts.boundaries {
		mention(draft.id, draft.span[0])
		declarations = append(declarations, declaration{draft.span[0], 1, k})
	}
	for k, draft := range drafts.jsons {
		mention(draft.id, draft.span[0])
		declarations = append(declarations, declaration{draft.span[0], 2, k})
	}
	for k, r := range drafts.relationships {
		if r.hasID && r.idSpan != nil {
			declarations = append(declarations, declaration{r.idSpan[0], 3, k})
		}
	}
	js.Sort(declarations, func(a, b declaration) float64 { return float64(a.offset - b.offset) })
	for _, d := range declarations {
		switch d.kind {
		case 0:
			declareElement(drafts.elements[d.index])
		case 1:
			declareBoundary(drafts.boundaries[d.index])
		case 2:
			unique(drafts.jsons[d.index].id, "json", drafts.jsons[d.index].span)
		default:
			r := drafts.relationships[d.index]
			unique(r.id, "edge", *r.idSpan)
		}
	}

	stateOrder := func(list []*ElementState) []*ElementState {
		js.Sort(list, func(a, b *ElementState) float64 { return firstOf(a.id) - firstOf(b.id) })
		return list
	}
	for _, state := range stateOrder(elements.values()) {
		publish(state)
	}
	bounds := boundaries.values()
	js.Sort(bounds, func(a, b *BoundaryState) float64 { return firstOf(a.id) - firstOf(b.id) })
	for _, state := range bounds {
		systemBoundaries.set(state.id, &SystemBoundary{id: state.id, label: state.label.text, labelType: state.label.typ, typ: "rect",
			members: []string{}, classes: []string{}, styles: []string{}})
	}
	jsons := slices.Clone(drafts.jsons)
	js.Sort(jsons, func(a, b *DraftJson) float64 { return firstOf(a.id) - firstOf(b.id) })
	for _, draft := range jsons {
		model.jsonNodes.set(draft.id, &UsecaseJsonNode{id: draft.id, value: draft.value, propertyOrder: draft.propertyOrder,
			classes: slices.Clone(draft.classes), styles: []string{}})
	}

	anonymous := 0
	for _, draft := range drafts.relationships {
		source, target, typ := &draft.source, &draft.target, draft.typ
		sourceKind := endpoint(source)
		targetKind := endpoint(target)
		related := func(kind string) bool { return kind == "actor" || kind == "usecase" || kind == "json" }
		if !related(sourceKind) {
			conflict("Relationship source '"+source.id+"' cannot be "+sourceKind, source.span, symbols[source.id].span, nil, nil)
		}
		if !related(targetKind) {
			conflict("Relationship target '"+target.id+"' cannot be "+targetKind, target.span, symbols[target.id].span, nil, nil)
		}
		if (typ == "include" || typ == "extend") && (sourceKind != "usecase" || targetKind != "usecase") {
			fail(typ+" relationship requires use-case endpoints", draft.span)
		}
		if typ == "generalization" && (sourceKind == "json" || sourceKind != targetKind) {
			fail("Generalization requires actor-to-actor or use-case-to-use-case endpoints", draft.span)
		}
		if typ == "association" && (sourceKind == "json" || targetKind == "json") && draft.arrowType > 2 {
			fail("JSON relationship '"+source.id+"' to '"+target.id+"' permits only point, reversed-point, or markerless solid association", draft.span)
		}
		id := draft.id
		if !draft.hasID {
			id = "edge-" + js.Int(anonymous)
			anonymous++
		}
		relationship := &Relationship{id: id, explicitID: draft.hasID && draft.id != "", source: source.id, target: target.id, typ: typ,
			arrowType: draft.arrowType, minlen: draft.minlen, classes: []string{}, styles: []string{}}
		if draft.label != nil {
			text := draft.label.text
			relationship.label = &text
			relationship.labelType = draft.label.typ
		}
		model.relationships = append(model.relationships, relationship)
		edges[id] = relationship
	}

	// The relationships may have added use cases; put all elements back in the order of first mention.
	declaredActors := actors.clone()
	declaredUseCases := useCases.clone()
	actors.clear()
	useCases.clear()
	for _, state := range stateOrder(elements.values()) {
		if actor, ok := declaredActors.get(state.id); ok {
			actors.set(state.id, actor)
		} else if useCase, ok := declaredUseCases.get(state.id); ok {
			useCases.set(state.id, useCase)
		} else {
			publish(state)
		}
	}

	for _, m := range drafts.metadata {
		origin, ok := symbols[m.target]
		if !ok {
			inferred := inferKind(m.properties)
			if inferred != "" {
				inferred = " (metadata implies " + inferred + ")"
			}
			fail("Metadata target '"+m.target+"' is unresolved"+inferred, m.span)
		}
		kind := origin.kind
		if kind == "actor" || kind == "usecase" {
			state, _ := elements.get(m.target)
			elementMetadata(state, m.properties, true)
		} else if kind == "edge" {
			m.statement.kind = "edgeMetadata"
			span := m.span
			m.statement.edges = []EdgeOccurrence{{id: m.target, span: m.statement.span, idSpan: &span, metadata: m.statement.metadata}}
			m.statement.nodes = nil
		}
		boundary, _ := boundaries.get(m.target)
		edge := edges[m.target]
		if kind == "actor" || kind == "usecase" {
			continue
		}
		for _, property := range m.properties {
			key, value := property.key, property.value
			b, isBool := value.(bool)
			switch {
			case kind == "boundary" && boundary != nil && key == "type" && (value == "rect" || value == "package"):
				boundary.typ = value.(string)
			case kind == "edge" && edge != nil && key == "animate" && isBool:
				edge.animate = b
			case kind == "edge" && edge != nil && key == "animation" && (value == "fast" || value == "slow"):
				edge.animation = value.(string)
				edge.animate = true
			default:
				invalid(m.target, kind, property)
			}
		}
	}
	for _, relationship := range model.relationships {
		if relationship.animation != "" {
			relationship.animate = true
		}
	}

	for _, state := range elements.values() {
		if state.kind == "actor" {
			if state.icon != "" && state.actorType != "" && state.actorType != "normal" {
				fail("Actor '"+state.id+"' cannot combine icon with type '"+state.actorType+"'", state.span)
			}
			if state.business != nil && *state.business && (state.icon != "" || state.actorType == "awesome") {
				fail("Business actor '"+state.id+"' must use normal or hollow geometry", state.span)
			}
		} else if state.shape == "rect" && state.business != nil && *state.business {
			fail("Rectangular use case '"+state.id+"' cannot be a business use case", state.span)
		}
		publish(state)
	}

	sorted := slices.Clone(drafts.elements)
	js.Sort(sorted, func(a, b *DraftElement) float64 { return float64(a.span[0] - b.span[0]) })
	for _, draft := range sorted {
		if draft.parent == nil || draft.parent.id == "" {
			continue
		}
		b, _ := boundaries.get(draft.parent.id)
		if !slices.Contains(b.members, draft.id) {
			b.members = append(b.members, draft.id)
		}
	}
	for _, state := range boundaries.values() {
		boundary, _ := systemBoundaries.get(state.id)
		boundary.typ = state.typ
		if boundary.typ == "" {
			boundary.typ = "rect"
		}
		boundary.members = state.members
		boundary.classes = slices.Clone(*state.classes)
	}

	notes := 0
	for _, draft := range drafts.notes {
		origin, ok := symbols[draft.target]
		if !ok {
			fail("Note target '"+draft.target+"' is unresolved", draft.span)
		}
		if origin.kind != "actor" && origin.kind != "usecase" {
			conflict("Note target '"+draft.target+"' must be an actor or use case, not "+origin.kind, draft.span, origin.span, nil, nil)
		}
		id := "note-" + js.Int(notes)
		notes++
		model.notes.set(id, &UsecaseNote{id: id, target: draft.target, label: draft.label.text, labelType: draft.label.typ})
	}

	for _, definition := range drafts.classDefs {
		for _, id := range definition.ids {
			model.classDefs.set(id, &ClassDef{id: id, styles: slices.Clone(definition.styles)})
		}
	}
	for _, assignment := range drafts.classes {
		for _, target := range assignment.targets {
			pushUnique(stylableOf(target.id, target.span).classes, assignment.classes)
		}
	}
	for _, assignment := range drafts.styles {
		s := stylableOf(assignment.target, assignment.span).styles
		*s = append(*s, assignment.styles...)
	}

	if drafts.direction != "" {
		model.direction = drafts.direction
	}
	model.title = title
	model.accTitle = drafts.accTitle
	model.accDescription = drafts.accDescription
	model.accDescr = dropIndent(drafts.accDescription)
	model.source = src
	model.headerSpan = headerSpan
	model.statements = statements
	return model
}

// dropIndent is text.replace(/\n\s+/g, '\n').
func dropIndent(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			b.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			r, size := utf8DecodeAt(text, j)
			if !js.IsSpace(r) {
				break
			}
			j += size
		}
		if j == i+1 {
			b.WriteByte('\n')
			i++
			continue
		}
		b.WriteByte('\n')
		i = j
	}
	return b.String()
}

func utf8DecodeAt(s string, i int) (rune, int) { return utf8.DecodeRuneInString(s[i:]) }
