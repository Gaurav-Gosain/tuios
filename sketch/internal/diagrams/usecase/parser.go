// Ported from pele src/diagrams/usecase/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package usecase

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// Mermaid parses use case diagrams with a Chevrotain parser, which decides each choice by looking
// at most three tokens ahead and reports the first token that fits nowhere. This parser makes the
// same decisions from the same lookahead, so it accepts the same text and names the same token in
// its errors. It builds the model directly, where Mermaid builds a syntax tree and then visits it.

const (
	fLINE           = 1
	fRELATION       = 2
	fSTYLE_PART     = 4
	fFOLLOWS_NAME   = 8
	fBOUNDARY_LINE  = 16
	fCLOSES_LABEL   = 32
	fMETADATA_FIRST = 64
)

const (
	ARROWS      = "DEPENDENCY_ARROW|GENERALIZATION|FORWARD_SOLID|BACKWARD_SOLID|MARKERLESS_SOLID|FORWARD_CIRCLE|BACKWARD_CIRCLE|FORWARD_CROSS|BACKWARD_CROSS"
	NAMES       = "IDENTIFIER|PLAIN_STRING|MARKDOWN_STRING"
	LABELS      = "PLAIN_STRING|MARKDOWN_STRING|LABEL_TEXT"
	STATEMENTS  = "ACC_TITLE_LINE|ACC_DESCR_LINE|ACC_DESCR_BLOCK|DIRECTION|ACTOR|SYSTEM_BOUNDARY|NOTE|JSON_DECLARATION_START|CLASS_DEF|CLASS|STYLE"
	STYLE_PARTS = "WORD|PLAIN_STRING|NUMBER|HASH_COLOR|CSS_IDENTIFIER|CSS_ESCAPED_COMMA|DASH|DOT|PERCENT|CSS_PUNCTUATION|COLON|LPAREN|RPAREN|LBRACKET|RBRACKET|LBRACE|RBRACE|AT|MARKERLESS_SOLID"
)

var FLAGS = func() (flags [T_RPAREN + 1]uint8) {
	flag := func(names string, bits uint8) {
		for name := range strings.SplitSeq(names, "|") {
			// WORD is a category: every keyword and IDENTIFIER.
			if name == "WORD" {
				for t := T_USECASE; t <= T_IDENTIFIER; t++ {
					flags[t] |= bits
				}
			} else {
				flags[slices.Index(TOKEN_NAMES, name)] |= bits
			}
		}
	}
	flag("NEWLINE|COMMENT|"+STATEMENTS+"|"+NAMES, fLINE)
	flag("IDENTIFIER|"+ARROWS, fRELATION)
	flag(STYLE_PARTS, fSTYLE_PART)
	flag("STEREOTYPE_START|CLASS_SEPARATOR|IDENTIFIER|"+ARROWS+"|NEWLINE|EOF", fFOLLOWS_NAME)
	flag("NEWLINE|COMMENT|ACTOR|"+NAMES, fBOUNDARY_LINE)
	flag("FORWARD_SOLID|MARKERLESS_SOLID|FORWARD_CIRCLE|FORWARD_CROSS", fCLOSES_LABEL)
	flag("NEWLINE|IDENTIFIER|PLAIN_STRING|RBRACE", fMETADATA_FIRST)
	return
}()

var FORBIDDEN = [...]string{"allowmixing", "newpage", "package", "rectangle", "skinparam"}

type Item struct {
	id             string
	label          DraftLabel
	span           Span
	generated      bool
	shape          string
	metadata       []DraftProperty
	hasMetadata    bool
	stereotype     string
	stereotypeSpan *Span
	classes        []string
	classSpans     []Span
	// Whether this mention says something about the element, and so declares it.
	explicit bool
	nodeSpan Span
}

type Tail struct {
	explicitID     string
	hasExplicitID  bool
	explicitIDSpan *Span
	typ            string
	arrowType      int
	label          *DraftLabel
	minlen         int
	target         Item
	span           Span
}

type parser struct {
	src    string
	types  []int
	starts []int
	ends   []int
	p      int
	// Where the last token before the most recent line end stops, which is where its statement stops.
	lastEnd     int
	drafts      *Drafts
	parent      *DraftBoundary
	pendingJson []pendingJson
	edgeCount   int
	noteCount   int
}

type pendingJson struct {
	json    *DraftJson
	literal int
}

func (ps *parser) image(k int) string { return ps.src[ps.starts[k]:ps.ends[k]] }

func (ps *parser) span(k int) Span { return Span{ps.starts[k], ps.ends[k]} }

func (ps *parser) fail(detail string) {
	line, column := lineColumn(ps.src, ps.starts[ps.p])
	panic(types.NewPeleError("Error parsing usecase diagram: "+detail+" at "+locationText(ps.src, ps.starts[ps.p], ps.ends[ps.p]), types.CodeSyntax, types.ErrorInfo{Type: "usecase", Line: line, Column: column}))
}

func (ps *parser) mismatch(name string) {
	ps.fail("Expecting token of type --> " + name + " <-- but found --> '" + ps.image(ps.p) + "' <--")
}

func (ps *parser) expect(typ int) int {
	if ps.types[ps.p] != typ {
		ps.mismatch(TOKEN_NAMES[typ])
	}
	ps.p++
	return ps.p - 1
}

// noAlternative: Chevrotain lists every token sequence that would have been accepted.
func (ps *parser) noAlternative(expected string) {
	var b strings.Builder
	i := 0
	for sequence := range strings.SplitSeq(expected, "|") {
		if i > 0 {
			b.WriteByte('\n')
		}
		i++
		b.WriteString("  " + strconv.Itoa(i) + ". [" + sequence + "]")
	}
	ps.fail("Expecting: one of these possible Token sequences:\n" + b.String() + "\nbut found: '" + ps.image(ps.p) + "'")
}

func statementSequences() string {
	names := strings.Split(NAMES, "|")
	var metadata []string
	for _, name := range names {
		for _, next := range []string{"NEWLINE", "IDENTIFIER", "PLAIN_STRING", "RBRACE"} {
			metadata = append(metadata, name+", METADATA_START, "+next)
		}
	}
	follows := strings.Split("STEREOTYPE_START|CLASS_SEPARATOR|IDENTIFIER|"+ARROWS+"|NEWLINE|EOF", "|")
	var entities []string
	for i, name := range names {
		list := follows
		if i == 0 {
			list = append([]string{"LPAREN", "LBRACKET"}, follows...)
		}
		for _, next := range list {
			entities = append(entities, name+", "+next)
		}
	}
	all := append([]string{STATEMENTS}, metadata...)
	all = append(all, entities...)
	all = append(all, metadata...)
	return strings.Join(all, "|")
}

func isWordToken(typ int) bool { return typ >= T_USECASE && typ <= T_IDENTIFIER }

func isLabelText(typ int) bool { return typ >= T_USECASE && typ <= T_LABEL_SYMBOL }

func (ps *parser) lineEnd() {
	ps.lastEnd = ps.ends[ps.p-1]
	if ps.types[ps.p] == T_NEWLINE {
		ps.p++
	} else if ps.types[ps.p] != T_EOF {
		ps.noAlternative("NEWLINE|EOF")
	}
}

// content is the text of a name or string token, without its quotes.
func (ps *parser) content(k int) Span {
	trim := 0
	switch ps.types[k] {
	case T_MARKDOWN_STRING:
		trim = 2
	case T_PLAIN_STRING:
		trim = 1
	}
	return Span{ps.starts[k] + trim, ps.ends[k] - trim}
}

func (ps *parser) tokenLabel(k int) DraftLabel {
	at := ps.content(k)
	typ := "text"
	if ps.types[k] == T_MARKDOWN_STRING {
		typ = "markdown"
	}
	return DraftLabel{text: ps.src[at[0]:at[1]], typ: typ, span: at}
}

// generatedID is label.replace(/\W/g, '_'): every UTF-16 unit that is not a word character becomes an underscore.
func generatedID(label string) string {
	var b strings.Builder
	for _, r := range label {
		switch {
		case r < utf8.RuneSelf && isWord(int(r)):
			b.WriteRune(r)
		case r > 0xffff:
			b.WriteString("__")
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// label: an unquoted label runs to the next delimiter and is taken from the source as written.
func (ps *parser) label() DraftLabel {
	typ := ps.types[ps.p]
	if typ == T_PLAIN_STRING || typ == T_MARKDOWN_STRING {
		ps.p++
		return ps.tokenLabel(ps.p - 1)
	}
	if !isLabelText(typ) {
		ps.noAlternative(LABELS)
	}
	first := ps.p
	for isLabelText(ps.types[ps.p]) {
		ps.p++
	}
	at := Span{ps.starts[first], ps.ends[ps.p-1]}
	return DraftLabel{text: ps.src[at[0]:at[1]], typ: "text", span: at}
}

func isKey(typ int) bool { return typ == T_IDENTIFIER || typ == T_PLAIN_STRING }

func (ps *parser) property() DraftProperty {
	key := ps.p
	if !isKey(ps.types[ps.p]) {
		ps.noAlternative("IDENTIFIER|PLAIN_STRING")
	}
	ps.p++
	ps.expect(T_COLON)
	value := ps.p
	typ := ps.types[ps.p]
	if !isKey(typ) && typ != T_TRUE && typ != T_FALSE {
		ps.noAlternative("IDENTIFIER|PLAIN_STRING|TRUE|FALSE")
	}
	ps.p++
	keySpan := ps.content(key)
	valueSpan := ps.content(value)
	var v any
	switch typ {
	case T_TRUE:
		v = true
	case T_FALSE:
		v = false
	default:
		v = ps.src[valueSpan[0]:valueSpan[1]]
	}
	return DraftProperty{MetadataOccurrence{key: ps.src[keySpan[0]:keySpan[1]], span: Span{ps.starts[key], ps.ends[value]}, keySpan: keySpan, valueSpan: valueSpan}, v}
}

// separatorAhead is whether another property follows. A comma or line breaks before the closing brace do not count,
// but Chevrotain looks only three tokens ahead, so a comma and two line breaks, or three line
// breaks, are taken for a separator and the brace after them is then an error.
func (ps *parser) separatorAhead() bool {
	a := ps.types[ps.p]
	b := ps.types[ps.p+1]
	c := ps.types[ps.p+2]
	if a == T_COMMA {
		return isKey(b) || (b == T_NEWLINE && (c == T_NEWLINE || isKey(c)))
	}
	if a != T_NEWLINE {
		return false
	}
	return b == T_COMMA || isKey(b) || (b == T_NEWLINE && (c == T_NEWLINE || c == T_COMMA || isKey(c)))
}

func (ps *parser) metadata() []DraftProperty {
	ps.expect(T_METADATA_START)
	properties := []DraftProperty{}
	for ps.types[ps.p] == T_NEWLINE {
		ps.p++
	}
	if isKey(ps.types[ps.p]) {
		properties = append(properties, ps.property())
		for ps.separatorAhead() {
			if ps.types[ps.p] == T_COMMA {
				ps.p++
			} else {
				for ps.types[ps.p] == T_NEWLINE {
					ps.p++
				}
				if ps.types[ps.p] == T_COMMA {
					ps.p++
				}
			}
			for ps.types[ps.p] == T_NEWLINE {
				ps.p++
			}
			properties = append(properties, ps.property())
		}
		if ps.types[ps.p] == T_COMMA {
			ps.p++
		}
		for ps.types[ps.p] == T_NEWLINE {
			ps.p++
		}
	}
	ps.expect(T_RBRACE)
	return properties
}

func occurrences(properties []DraftProperty) []MetadataOccurrence {
	out := make([]MetadataOccurrence, len(properties))
	for k, p := range properties {
		out[k] = p.MetadataOccurrence
	}
	return out
}

func (ps *parser) classSuffix(classes *[]string, spans *[]Span) {
	for {
		ps.p++
		id := ps.expect(T_IDENTIFIER)
		*classes = append(*classes, ps.image(id))
		*spans = append(*spans, ps.span(id))
		if ps.types[ps.p] != T_COMMA {
			break
		}
	}
}

// name is an id with an optional label in parentheses or brackets, or a string that stands for both.
func (ps *parser) name(brackets bool) Item {
	k := ps.p
	typ := ps.types[ps.p]
	item := Item{span: ps.content(k), generated: typ != T_IDENTIFIER, classes: []string{}}
	switch typ {
	case T_IDENTIFIER:
		ps.p++
		item.id = ps.image(k)
		open := ps.types[ps.p]
		if open == T_LPAREN || (brackets && open == T_LBRACKET) {
			ps.p++
			item.label = ps.label()
			if open == T_LPAREN {
				ps.expect(T_RPAREN)
				item.shape = "ellipse"
			} else {
				ps.expect(T_RBRACKET)
				item.shape = "rect"
			}
		} else {
			item.label = ps.tokenLabel(k)
		}
	case T_PLAIN_STRING, T_MARKDOWN_STRING:
		ps.p++
		item.label = ps.tokenLabel(k)
		item.id = generatedID(item.label.text)
	default:
		ps.noAlternative(NAMES)
	}
	return item
}

func (ps *parser) element(actor bool) Item {
	first := ps.p
	item := ps.name(!actor)
	if actor {
		item.shape = ""
	}
	if ps.types[ps.p] == T_METADATA_START {
		item.metadata = ps.metadata()
		item.hasMetadata = true
	}
	if ps.types[ps.p] == T_STEREOTYPE_START {
		ps.p++
		text := ps.expect(T_STEREOTYPE_TEXT)
		ps.expect(T_STEREOTYPE_END)
		item.stereotype = js.Trim(ps.image(text))
		s := ps.span(text)
		item.stereotypeSpan = &s
	}
	if ps.types[ps.p] == T_CLASS_SEPARATOR {
		ps.classSuffix(&item.classes, &item.classSpans)
	}
	item.explicit = item.shape != "" || item.hasMetadata || item.stereotype != ""
	item.nodeSpan = Span{ps.starts[first], ps.ends[ps.p-1]}
	return item
}

func (ps *parser) draft(item *Item, kind string) *DraftElement {
	var metadata []DraftProperty
	if item.hasMetadata {
		metadata = item.metadata
	}
	return &DraftElement{Named: Named{id: item.id, label: item.label, span: item.span, generated: item.generated}, kind: kind,
		parent: ps.parent, shape: item.shape, metadata: metadata, stereotype: item.stereotype, classes: item.classes}
}

func endpointOf(item *Item, declares bool) DraftEndpoint {
	return DraftEndpoint{Named: Named{id: item.id, label: item.label, span: item.span, generated: item.generated}, declares: declares, classed: len(item.classes) > 0}
}

func occurrence(item *Item, defines bool) NodeOccurrence {
	out := NodeOccurrence{id: item.id, span: item.nodeSpan, idSpan: item.span, labelSpan: item.label.span, hasLabel: true, defines: defines, stereotypeSpan: item.stereotypeSpan}
	if item.hasMetadata {
		out.metadata = occurrences(item.metadata)
	}
	if len(item.classSpans) > 0 {
		out.classSpans = item.classSpans
	}
	return out
}

// labeledRight is whether a label and then one of the operators that can close it come next.
func (ps *parser) labeledRight(closers uint8) bool {
	if ps.types[ps.p] < T_USECASE || ps.types[ps.p] > T_MARKDOWN_STRING {
		return false
	}
	for k := ps.p + 1; ; k++ {
		typ := ps.types[k]
		if typ == T_MARKERLESS_SOLID || FLAGS[typ]&closers != 0 {
			return true
		}
		if typ < T_USECASE || typ > T_MARKDOWN_STRING {
			return false
		}
	}
}

// minimumLength: each dash beyond the second asks for one more rank between the two ends.
func (ps *parser) minimumLength(k int) int {
	dashes := ps.ends[k] - ps.starts[k]
	if ps.types[k] != T_MARKERLESS_SOLID {
		dashes--
	}
	return max(1, dashes-1)
}

func (ps *parser) relationTail() *Tail {
	first := ps.p
	tail := &Tail{typ: "association", arrowType: SOLID_ARROW, minlen: 1}
	if ps.types[ps.p] == T_IDENTIFIER && ps.types[ps.p+1] == T_AT {
		tail.explicitID = ps.image(ps.p)
		tail.hasExplicitID = true
		s := ps.span(ps.p)
		tail.explicitIDSpan = &s
		ps.p += 2
	}
	k := ps.p
	typ := ps.types[ps.p]
	ps.p++
	switch typ {
	case T_DEPENDENCY_ARROW:
		ps.expect(T_COLON)
		if ps.types[ps.p] != T_INCLUDE && ps.types[ps.p] != T_EXTEND {
			ps.noAlternative("INCLUDE|EXTEND")
		}
		tail.typ = "extend"
		if ps.types[ps.p] == T_INCLUDE {
			tail.typ = "include"
		}
		tail.label = &DraftLabel{text: tail.typ, typ: "text", span: ps.span(ps.p)}
		ps.p++
	case T_GENERALIZATION:
		tail.typ = "generalization"
	case T_FORWARD_SOLID:
		tail.minlen = ps.minimumLength(k)
	case T_BACKWARD_SOLID:
		tail.arrowType = BACK_ARROW
		// Only the shortest operator can open a label: `A <-- label -- B`.
		if ps.ends[k]-ps.starts[k] == 3 && ps.labeledRight(0) {
			label := ps.label()
			tail.label = &label
			tail.minlen = ps.minimumLength(ps.expect(T_MARKERLESS_SOLID))
		} else {
			tail.minlen = ps.minimumLength(k)
		}
	case T_MARKERLESS_SOLID:
		tail.arrowType = LINE_SOLID
		if ps.ends[k]-ps.starts[k] == 2 && ps.labeledRight(fCLOSES_LABEL) {
			label := ps.label()
			tail.label = &label
			switch ps.types[ps.p] {
			case T_FORWARD_SOLID:
				tail.arrowType = SOLID_ARROW
			case T_FORWARD_CIRCLE:
				tail.arrowType = CIRCLE_ARROW
			case T_FORWARD_CROSS:
				tail.arrowType = CROSS_ARROW
			case T_MARKERLESS_SOLID:
			default:
				ps.noAlternative("FORWARD_SOLID|MARKERLESS_SOLID|FORWARD_CIRCLE|FORWARD_CROSS")
			}
			if tail.arrowType == SOLID_ARROW || tail.arrowType == LINE_SOLID {
				tail.minlen = ps.minimumLength(ps.p)
			}
			ps.p++
		} else {
			tail.minlen = ps.minimumLength(k)
		}
	case T_FORWARD_CIRCLE:
		tail.arrowType = CIRCLE_ARROW
	case T_FORWARD_CROSS:
		tail.arrowType = CROSS_ARROW
	case T_BACKWARD_CIRCLE, T_BACKWARD_CROSS:
		tail.arrowType = CROSS_ARROW_REVERSED
		if typ == T_BACKWARD_CIRCLE {
			tail.arrowType = CIRCLE_ARROW_REVERSED
		}
		if ps.labeledRight(0) {
			label := ps.label()
			tail.label = &label
			ps.expect(T_MARKERLESS_SOLID)
		}
	default:
		ps.p--
		ps.noAlternative(ARROWS)
	}
	tail.target = ps.element(false)
	tail.span = Span{ps.starts[first], ps.ends[ps.p-1]}
	return tail
}

func statement(kind string) *GraphStatement {
	return &GraphStatement{kind: kind}
}

func (ps *parser) relationship(source *Item, declaration bool, tail *Tail, nodes []NodeOccurrence) *GraphStatement {
	target := &tail.target
	if target.explicit {
		ps.drafts.elements = append(ps.drafts.elements, ps.draft(target, "usecase"))
	}
	ps.drafts.relationships = append(ps.drafts.relationships, &DraftRelationship{
		source: endpointOf(source, declaration), target: endpointOf(target, target.explicit), span: tail.span,
		id: tail.explicitID, hasID: tail.hasExplicitID, idSpan: tail.explicitIDSpan, typ: tail.typ, arrowType: tail.arrowType,
		label: tail.label, minlen: tail.minlen,
	})
	nodes = append(nodes, occurrence(target, target.explicit))
	out := statement("edge")
	out.nodes = nodes
	id := tail.explicitID
	if !tail.hasExplicitID {
		id = "edge-" + strconv.Itoa(ps.edgeCount)
		ps.edgeCount++
	}
	edge := EdgeOccurrence{id: id, span: out.span, idSpan: tail.explicitIDSpan}
	if tail.label != nil {
		s := tail.label.span
		edge.labelSpan = &s
	}
	out.edges = []EdgeOccurrence{edge}
	return out
}

func declaration(nodes []NodeOccurrence) *GraphStatement {
	out := statement("node")
	out.nodes = nodes
	return out
}

// actors: `actor A, B` declares several actors; at the top level `actor A --> B` declares one and relates it.
func (ps *parser) actors(top bool) *GraphStatement {
	ps.p++
	items := []Item{ps.element(true)}
	var tail *Tail
	if ps.types[ps.p] == T_COMMA {
		for ps.types[ps.p] == T_COMMA {
			ps.p++
			items = append(items, ps.element(true))
		}
	} else if top && FLAGS[ps.types[ps.p]]&fRELATION != 0 {
		tail = ps.relationTail()
	}
	ps.lineEnd()
	for k := range items {
		ps.drafts.elements = append(ps.drafts.elements, ps.draft(&items[k], "actor"))
	}
	nodes := make([]NodeOccurrence, 0, len(items)+1)
	for k := range items {
		nodes = append(nodes, occurrence(&items[k], true))
	}
	if tail != nil {
		return ps.relationship(&items[0], true, tail, nodes)
	}
	return declaration(nodes)
}

func (ps *parser) entity(top bool) *GraphStatement {
	element := ps.element(false)
	source := &element
	var tail *Tail
	if top && FLAGS[ps.types[ps.p]]&fRELATION != 0 {
		tail = ps.relationTail()
	}
	ps.lineEnd()
	if tail == nil {
		source.explicit = true
		if source.shape == "" {
			source.shape = "ellipse"
		}
	}
	if source.explicit {
		ps.drafts.elements = append(ps.drafts.elements, ps.draft(source, "usecase"))
	}
	nodes := make([]NodeOccurrence, 1, 2)
	nodes[0] = occurrence(source, source.explicit)
	if tail != nil {
		return ps.relationship(source, source.explicit, tail, nodes)
	}
	return declaration(nodes)
}

func (ps *parser) boundary() *GraphStatement {
	ps.p++
	item := ps.name(true)
	named := &item
	group := &DraftBoundary{Named: Named{id: named.id, label: named.label, span: named.span, generated: named.generated}, classes: &named.classes}
	var inline []DraftProperty
	hasInline := false
	if ps.types[ps.p] == T_METADATA_START {
		inline = ps.metadata()
		hasInline = true
	}
	if ps.types[ps.p] == T_CLASS_SEPARATOR {
		ps.classSuffix(&named.classes, &named.classSpans)
	}
	ps.lineEnd()
	ps.drafts.boundaries = append(ps.drafts.boundaries, group)
	ps.parent = group
	var children []*GraphStatement
	for FLAGS[ps.types[ps.p]]&fBOUNDARY_LINE != 0 {
		children = append(children, ps.line(false))
	}
	ps.parent = nil
	end := ps.expect(T_END)
	ps.lineEnd()
	out := statement("group")
	out.group = group.id
	idSpan, titleSpan, endSpan := group.span, group.label.span, ps.span(end)
	out.idSpan, out.titleSpan, out.endSpan = &idSpan, &titleSpan, &endSpan
	out.classSpans = named.classSpans
	if len(children) > 0 {
		out.children = children
	}
	if hasInline {
		out.metadata = occurrences(inline)
		ps.drafts.metadata = append(ps.drafts.metadata, draftMetadata{target: group.id, span: group.span, properties: inline, statement: out})
	}
	return out
}

func (ps *parser) style() string {
	first := ps.p
	typ := ps.types[ps.p]
	switch {
	case isWordToken(typ) || typ == T_CSS_IDENTIFIER:
		ps.p++
	case typ == T_MARKERLESS_SOLID:
		ps.p++
		if !isWordToken(ps.types[ps.p]) {
			ps.mismatch("WORD")
		}
		ps.p++
	default:
		ps.noAlternative("WORD|CSS_IDENTIFIER|MARKERLESS_SOLID")
	}
	ps.expect(T_COLON)
	if FLAGS[ps.types[ps.p]]&fSTYLE_PART == 0 {
		ps.fail("Expecting: expecting at least one iteration which starts with one of these possible Token sequences::\n  <[" +
			strings.ReplaceAll(STYLE_PARTS, "|", "] ,[") + "]>\nbut found: '" + ps.image(ps.p) + "'")
	}
	for FLAGS[ps.types[ps.p]]&fSTYLE_PART != 0 {
		ps.p++
	}
	// The value is taken from the source, so the spaces inside `1px solid red` survive.
	return strings.ReplaceAll(ps.src[ps.starts[first]:ps.ends[ps.p-1]], "\\,", ",")
}

func (ps *parser) styles() []string {
	out := []string{ps.style()}
	for ps.types[ps.p] == T_COMMA {
		ps.p++
		out = append(out, ps.style())
	}
	return out
}

func (ps *parser) identifiers() []int {
	out := []int{ps.expect(T_IDENTIFIER)}
	for ps.types[ps.p] == T_COMMA {
		ps.p++
		out = append(out, ps.expect(T_IDENTIFIER))
	}
	return out
}

func (ps *parser) reference(k int) NodeOccurrence {
	at := ps.content(k)
	return NodeOccurrence{id: ps.image(k), span: at, idSpan: at}
}

func (ps *parser) topStatement() *GraphStatement {
	k := ps.p
	typ := ps.types[ps.p]
	var out *GraphStatement
	switch {
	case typ == T_ACC_TITLE_LINE || typ == T_ACC_DESCR_LINE || typ == T_ACC_DESCR_BLOCK:
		ps.p++
		text := ps.image(k)
		switch typ {
		case T_ACC_DESCR_BLOCK:
			ps.drafts.accDescription = js.Trim(text[strings.IndexByte(text, '{')+1 : strings.LastIndexByte(text, '}')])
		case T_ACC_DESCR_LINE:
			ps.drafts.accDescription = js.Trim(text[strings.IndexByte(text, ':')+1:])
		default:
			ps.drafts.accTitle = js.Trim(text[strings.IndexByte(text, ':')+1:])
		}
		if typ == T_ACC_TITLE_LINE {
			out = statement("accTitle")
		} else {
			out = statement("accDescr")
		}
	case typ == T_DIRECTION:
		ps.p++
		value := ps.types[ps.p]
		if value < T_TD || value > T_RL {
			ps.noAlternative("TD|TB|BT|LR|RL")
		}
		if value == T_TD {
			ps.drafts.direction = "TB"
		} else {
			ps.drafts.direction = ps.image(ps.p)
		}
		ps.p++
		out = statement("direction")
	case typ == T_ACTOR:
		return ps.actors(true)
	case typ == T_SYSTEM_BOUNDARY:
		return ps.boundary()
	case typ == T_NOTE:
		ps.p++
		ps.expect(T_FOR)
		target := ps.expect(T_IDENTIFIER)
		text := ps.label()
		ps.drafts.notes = append(ps.drafts.notes, draftNote{target: ps.image(target), span: ps.span(target), label: text})
		out = statement("note")
		out.ref = "note-" + strconv.Itoa(ps.noteCount)
		ps.noteCount++
		refSpan := text.span
		out.refSpan = &refSpan
		out.nodes = []NodeOccurrence{ps.reference(target)}
	case typ == T_JSON_DECLARATION_START:
		ps.p++
		literal := ps.expect(T_JSON_OBJECT_LITERAL)
		classes := []string{}
		var classSpans []Span
		if ps.types[ps.p] == T_CLASS_SEPARATOR {
			ps.classSuffix(&classes, &classSpans)
		}
		from := ps.starts[k] + 4
		for at(ps.src, from) == 32 || at(ps.src, from) == 9 {
			from++
		}
		to := from
		for isWord(at(ps.src, to)) {
			to++
		}
		json := &DraftJson{id: ps.src[from:to], value: &jsonObject{Values: map[string]any{}}, propertyOrder: newKeyOrder(), span: Span{from, to}, classes: classes}
		ps.drafts.jsons = append(ps.drafts.jsons, json)
		ps.pendingJson = append(ps.pendingJson, pendingJson{json, literal})
		out = statement("json")
		out.nodes = []NodeOccurrence{{id: json.id, span: json.span, idSpan: json.span, defines: true, classSpans: classSpans}}
		out.classSpans = classSpans
	case typ == T_CLASS_DEF:
		ps.p++
		ids := ps.identifiers()
		names := make([]string, len(ids))
		for i, id := range ids {
			names[i] = ps.image(id)
		}
		ps.drafts.classDefs = append(ps.drafts.classDefs, draftClassDef{ids: names, styles: ps.styles()})
		out = statement("classDef")
		out.ref = ps.image(ids[0])
		refSpan := ps.span(ids[0])
		out.refSpan = &refSpan
	case typ == T_CLASS:
		ps.p++
		targets := ps.identifiers()
		classes := ps.identifiers()
		assignment := draftClass{}
		for _, id := range targets {
			assignment.targets = append(assignment.targets, draftTarget{id: ps.image(id), span: ps.span(id)})
		}
		for _, id := range classes {
			assignment.classes = append(assignment.classes, ps.image(id))
		}
		ps.drafts.classes = append(ps.drafts.classes, assignment)
		out = statement("classAssign")
		out.ref = ps.image(classes[0])
		refSpan := ps.span(classes[0])
		out.refSpan = &refSpan
		for _, id := range targets {
			out.nodes = append(out.nodes, ps.reference(id))
		}
	case typ == T_STYLE:
		ps.p++
		target := ps.expect(T_IDENTIFIER)
		ps.drafts.styles = append(ps.drafts.styles, draftStyle{target: ps.image(target), span: ps.span(target), styles: ps.styles()})
		out = statement("style")
		out.nodes = []NodeOccurrence{ps.reference(target)}
	default:
		next := ps.types[ps.p+1]
		if next == T_METADATA_START {
			if FLAGS[ps.types[ps.p+2]]&fMETADATA_FIRST == 0 {
				ps.noAlternative(statementSequences())
			}
			ps.p++
			target := ps.reference(k)
			if typ != T_IDENTIFIER {
				target.id = generatedID(ps.src[target.span[0]:target.span[1]])
			}
			assigned := ps.metadata()
			out = statement("metadata")
			out.nodes = []NodeOccurrence{target}
			out.metadata = occurrences(assigned)
			ps.drafts.metadata = append(ps.drafts.metadata, draftMetadata{target: target.id, span: target.span, properties: assigned, statement: out})
		} else {
			follows := FLAGS[next]&fFOLLOWS_NAME != 0 || (typ == T_IDENTIFIER && (next == T_LPAREN || next == T_LBRACKET))
			// Mermaid turns away the PlantUML statements people are most likely to try.
			if !follows || (typ == T_IDENTIFIER && slices.Contains(FORBIDDEN[:], strings.ToLower(ps.image(k)))) {
				ps.noAlternative(statementSequences())
			}
			return ps.entity(true)
		}
	}
	ps.lineEnd()
	return out
}

func (ps *parser) line(top bool) *GraphStatement {
	k := ps.p
	typ := ps.types[ps.p]
	if typ == T_NEWLINE {
		ps.p++
		return &GraphStatement{kind: "blank", span: ps.span(k)}
	}
	if typ == T_COMMENT {
		ps.p++
		ps.lineEnd()
		return &GraphStatement{kind: "comment", span: ps.span(k)}
	}
	var out *GraphStatement
	switch {
	case top:
		out = ps.topStatement()
	case typ == T_ACTOR:
		out = ps.actors(false)
	default:
		out = ps.entity(false)
	}
	out.span = Span{ps.starts[k], ps.lastEnd}
	for i := range out.edges {
		out.edges[i].span = out.span
	}
	return out
}

func parseUsecase(source string, title *string) *UsecaseModel {
	tk := tokenize(source)
	ps := &parser{src: source, types: tk.types, starts: tk.starts, ends: tk.ends, drafts: emptyDrafts()}
	header := ps.expect(T_USECASE)
	ps.lineEnd()
	var statements []*GraphStatement
	for FLAGS[ps.types[ps.p]]&fLINE != 0 {
		statements = append(statements, ps.line(true))
	}
	if ps.types[ps.p] != T_EOF {
		ps.fail("Redundant input, expecting EOF but found: " + ps.image(ps.p))
	}
	// The whole text has to be well formed before the JSON in it is looked at, as in Mermaid.
	for _, pending := range ps.pendingJson {
		literal := pending.literal
		parsed := parseOrderedJson(ps.image(literal), func() (int, int) { return lineColumn(source, ps.starts[literal]) })
		pending.json.value = parsed.value
		pending.json.propertyOrder = parsed.propertyOrder
	}
	return resolveUsecase(source, ps.drafts, ps.span(header), statements, title)
}
