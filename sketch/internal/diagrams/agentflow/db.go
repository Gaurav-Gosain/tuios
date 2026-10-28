// Ported from pele src/diagrams/agentflow/db.ts and diagnostics.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele's AgentflowDb also keeps where each statement sits (mappings) and builds a semantic
// model for tools; sketch reads neither, so the port leaves them out, and its diagnostics
// carry no position.

package agentflow

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/lazyre"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

type AgentflowDbOptions struct {
	// MaxEdges is 0 for no limit.
	MaxEdges   int
	InheritDir bool
	// Direction is "" for none.
	Direction string
	// Lines of front matter before the text the parser sees, added to every reported line.
	LineOffset int
	// Source is the text the parser sees, where errors are located. "" locates them by LineOffset.
	Source string
}

var TOOL_SHAPES = map[string]bool{"subroutine": true, "subprocess": true, "subproc": true, "framed-rectangle": true, "tool": true}

func isToolDefinition(vertex *AgentNode) bool {
	return vertex.Type != nil && TOOL_SHAPES[*vertex.Type]
}

func labelType(typ any) string {
	if typ == "markdown" || typ == "string" || typ == "text" {
		return typ.(string)
	}
	return "markdown"
}

func unquote(text string) string {
	// A lone quote stays: substring(1, 0) swaps its ends and gives the quote back.
	if len(text) >= 2 && strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) {
		return text[1 : len(text)-1]
	}
	return text
}

// stripPrototypeKeys drops `__proto__`, `constructor` and `prototype` keys at every level, so that a consumer
// merging metadata into its own objects cannot be made to pollute a prototype.
func stripPrototypeKeys(root any) any {
	if !hasPrototypeKeys(root) {
		return root
	}
	switch v := root.(type) {
	case []any:
		out := make([]any, len(v))
		for k, e := range v {
			out[k] = stripPrototypeKeys(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			if k == "__proto__" || k == "constructor" || k == "prototype" {
				continue
			}
			out[k] = stripPrototypeKeys(e)
		}
		return out
	}
	return root
}

// hasPrototypeKeys is whether stripPrototypeKeys would drop anything; when not, the value is kept
// as it is, where pele copies it. Not in pele.
func hasPrototypeKeys(v any) bool {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if hasPrototypeKeys(e) {
				return true
			}
		}
	case map[string]any:
		for k, e := range t {
			if k == "__proto__" || k == "constructor" || k == "prototype" || hasPrototypeKeys(e) {
				return true
			}
		}
	}
	return false
}

// merge adds what a metadata block holds to an element's metadata. Mermaid spreads the parsed YAML
// whatever it is, so a list adds its indexes as keys and a string its characters.
func merge(into Metadata, doc any) Metadata {
	if m, ok := doc.(map[string]any); ok && into == nil {
		// A parsed block belongs to this element alone, so it can be the element's metadata.
		return m
	}
	if into == nil {
		into = Metadata{}
	}
	switch v := doc.(type) {
	case map[string]any:
		for k, e := range v {
			into[k] = e
		}
	case []any:
		for k, e := range v {
			into[strconv.Itoa(k)] = e
		}
	case string:
		u := utf16.Encode([]rune(v))
		for k := range u {
			into[strconv.Itoa(k)] = string(utf16.Decode(u[k : k+1]))
		}
	}
	return into
}

var (
	RE_BLOCK_SCALAR   = lazyre.New(`:[` + js.S + `]*[>|][\d+-]*[` + js.S + `]*$`)
	RE_TRAILING_COMMA = lazyre.New(`,([\t ]*)$`)
	RE_ESCAPED_COMMA  = lazyre.New(`\\,`)
)

// indentOf is line.length - line.trimStart().length, in UTF-16 units.
func indentOf(line string) int {
	trimmed := js.TrimStart(line)
	n := 0
	for _, r := range line[:len(line)-len(trimmed)] {
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// stripLineTrailingCommas removes the comma that ends a line of a multi-line metadata body, which block YAML does not allow.
// Commas inside quoted scalars, flow collections and block scalars are content and stay.
func stripLineTrailingCommas(body string) string {
	inSingle, inDouble := false, false
	flowDepth := 0
	blockScalarIndent := -1
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if blockScalarIndent >= 0 {
			indent := indentOf(line)
			if js.Trim(line) == "" || indent > blockScalarIndent {
				out = append(out, line)
				continue
			}
			blockScalarIndent = -1
		}
		commentStart := -1
		for i := 0; i < len(line); i++ {
			ch := line[i]
			switch {
			case inDouble:
				if ch == '\\' {
					i++
				} else if ch == '"' {
					inDouble = false
				}
			case inSingle:
				if ch == '\'' {
					inSingle = false
				}
			case ch == '"':
				inDouble = true
			case ch == '\'':
				inSingle = true
			case ch == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
				commentStart = i
			case ch == '[' || ch == '{':
				flowDepth++
			case ch == ']' || ch == '}':
				flowDepth = max(0, flowDepth-1)
			}
			if commentStart >= 0 {
				break
			}
		}
		if inSingle || inDouble || flowDepth > 0 {
			out = append(out, line)
			continue
		}
		code, comment := line, ""
		if commentStart >= 0 {
			code, comment = line[:commentStart], line[commentStart:]
		}
		if RE_BLOCK_SCALAR.MatchString(code) {
			blockScalarIndent = indentOf(line)
			out = append(out, line)
			continue
		}
		out = append(out, RE_TRAILING_COMMA.ReplaceAllString(code, "$1")+comment)
	}
	return strings.Join(out, "\n")
}

func edgeSemantic(typ, stroke string) string {
	switch {
	case typ == "arrow_point" && stroke == "normal":
		return "sequence"
	case typ == "arrow_cross" && stroke == "normal":
		return "failure"
	case typ == "arrow_open" && stroke == "dotted":
		return "reference"
	}
	return ""
}

func destructEndLink(raw string) (typ, stroke string, length int) {
	str := js.Trim(raw)
	line := str
	if str != "" {
		line = str[:len(str)-1]
	}
	typ = "arrow_open"
	last := ""
	if str != "" {
		last = str[len(str)-1:]
	}
	switch last {
	case "x":
		typ = "arrow_cross"
	case ">":
		typ = "arrow_point"
	case "-", ".":
		line = str
	}
	stroke = "normal"
	length = len(line) - 1
	dots := strings.Count(line, ".")
	if dots > 0 {
		stroke = "dotted"
		length = dots
	}
	return typ, stroke, length
}

// strMap is a JavaScript Map with string keys, which keeps the order keys were first set in. Not in pele.
type strMap[V any] struct {
	keys []string
	m    map[string]V
}

func newStrMap[V any]() strMap[V] { return strMap[V]{m: map[string]V{}} }

func (s *strMap[V]) get(k string) (V, bool) {
	v, ok := s.m[k]
	return v, ok
}

func (s *strMap[V]) set(k string, v V) {
	if _, ok := s.m[k]; !ok {
		s.keys = append(s.keys, k)
	}
	s.m[k] = v
}

type AgentflowDb struct {
	Direction        *string
	Nodes            strMap[*AgentNode]
	Edges            []*AgentEdge
	Subgraphs        []*AgentSubgraph
	Connectors       strMap[*AgentNode]
	Classes          strMap[*AgentClassDef]
	Tooltips         map[string]string
	DefaultEdgeStyle []string
	// DefaultInterpolate is nil for none.
	DefaultInterpolate *string
	Diagnostics        []Diagnostic
	Title              string
	AccTitle           string
	AccDescr           string
	LineOffset         int
	source             string

	subgraphLookup map[string]*AgentSubgraph
	globalNodes    map[string]bool
	members        map[string]bool
	edgeById       map[string]*AgentEdge
	pairCount      map[string]int
	subCount       int
	reported       map[string][]Diagnostic
	options        AgentflowDbOptions
	graph          *AgentGraph
}

func NewAgentflowDb(options AgentflowDbOptions) *AgentflowDb {
	return &AgentflowDb{
		Nodes:          newStrMap[*AgentNode](),
		Connectors:     newStrMap[*AgentNode](),
		Classes:        newStrMap[*AgentClassDef](),
		Tooltips:       map[string]string{},
		LineOffset:     options.LineOffset,
		source:         options.Source,
		subgraphLookup: map[string]*AgentSubgraph{},
		globalNodes:    map[string]bool{},
		members:        map[string]bool{},
		edgeById:       map[string]*AgentEdge{},
		pairCount:      map[string]int{},
		reported:       map[string][]Diagnostic{},
		options:        options,
	}
}

func (db *AgentflowDb) metadata(id string, source string, loc Loc) any {
	multiline := strings.Contains(source, "\n")
	text := "{\n" + source + "\n}"
	if multiline {
		text = source + "\n"
	}
	parsed, err := util.ParseYaml(text)
	if err != nil {
		// Block YAML rejects the comma-separated style of the single-line form, so try once more without the commas.
		stripped := source
		if multiline {
			stripped = stripLineTrailingCommas(source)
		}
		var retried any
		ok := false
		if stripped != source {
			if v, rerr := util.ParseYaml(stripped + "\n"); rerr == nil {
				retried, ok = v, true
			}
		}
		if !ok {
			at := ""
			line, column := db.errorAt(loc)
			if loc.FirstLine != 0 {
				at = " (" + strconv.Itoa(line) + ":" + strconv.Itoa(column) + ")"
			}
			panic(types.NewPeleError(`Metadata of "`+id+`" is not valid YAML. `+err.Error()+at, types.CodeSyntax,
				types.ErrorInfo{Type: "agentflow", Line: line, Column: column}))
		}
		parsed = retried
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	return stripPrototypeKeys(parsed)
}

// errorAt is the line and column of loc in the source as written. A zero Loc has none.
func (db *AgentflowDb) errorAt(loc Loc) (int, int) {
	if loc.FirstLine == 0 {
		return 0, 0
	}
	source := db.source
	if source == "" {
		return loc.FirstLine + db.LineOffset, loc.FirstColumn + 1
	}
	start := 0
	for line := 1; line < loc.FirstLine; line++ {
		if i := strings.IndexByte(source[start:], '\n'); i >= 0 {
			start += i + 1
		} else {
			start = 0
		}
	}
	// The column counts UTF-16 units; Locate takes a byte offset.
	at := start
	for units := 0; units < loc.FirstColumn && at < len(source); {
		r, size := utf8.DecodeRuneInString(source[at:])
		units += utf16.RuneLen(r)
		at += size
	}
	l := types.Locate(source, at)
	return l.Line, l.Column
}

func (db *AgentflowDb) shapeError(message string, loc Loc) {
	line, column := db.errorAt(loc)
	panic(types.NewPeleError(message, types.CodeSemantic, types.ErrorInfo{Type: "agentflow", Line: line, Column: column}))
}

func jsonMarshal(s string) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	err := enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n"), err
}

func sortStrings(s []string) { slices.SortFunc(s, js.Compare) }

// jsonOf is JSON.stringify of a YAML value. A YAML map's keys come out in sorted order, where pele
// keeps the order they were written in.
func jsonOf(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return "null"
		}
		return js.Num(t)
	case string:
		b, _ := jsonMarshal(t)
		return b
	case []any:
		parts := make([]string, len(t))
		for k, e := range t {
			parts[k] = jsonOf(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sortStrings(keys)
		parts := make([]string, len(keys))
		for k, key := range keys {
			q, _ := jsonMarshal(key)
			parts[k] = q + ":" + jsonOf(t[key])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return ""
}

func (db *AgentflowDb) AddVertex(id string, textObj *FlowText, typ *string, style []string, props map[string]string, metadata *string, metadataLoc Loc) {
	if id == "" || js.Trim(id) == "" {
		return
	}

	var doc any
	var fields map[string]any
	authoredShape := ""
	hasAuthored := false
	if metadata != nil {
		doc = db.metadata(id, *metadata, metadataLoc)
		if m, ok := doc.(map[string]any); ok {
			fields = m
			if s, ok := m["shape"].(string); ok {
				authoredShape, hasAuthored = s, true
				if alias := resolveShapeAlias(s); alias != s {
					m["shape"] = alias
				}
			}
		}
	}

	if subgraph, ok := db.subgraphLookup[id]; ok && doc != nil {
		subgraph.Metadata = merge(subgraph.Metadata, doc)
		return
	}

	if connector, ok := db.Connectors.get(id); ok && doc != nil {
		connector.Metadata = merge(connector.Metadata, doc)
		return
	}

	// Mermaid reserves this id and creates no node for it.
	if id == "connectors" {
		return
	}

	if edge, ok := db.edgeById[id]; ok {
		if v, ok := fields["animate"]; ok {
			edge.Animate = v
		}
		if v, ok := fields["animation"]; ok {
			edge.Animation = v
		}
		if v, ok := fields["curve"]; ok {
			s := curveName(v)
			edge.Interpolate = &s
		}
		if doc != nil {
			edge.Metadata = merge(edge.Metadata, doc)
		}
		return
	}

	vertex, ok := db.Nodes.get(id)
	if !ok {
		vertex = &AgentNode{ID: id, LabelType: "text", Styles: []string{}, Classes: []string{}}
		db.Nodes.set(id, vertex)
	}

	if textObj != nil {
		vertex.LabelType = textObj.Type
		t := unquote(js.Trim(textObj.Text))
		vertex.Text = &t
	} else if vertex.Text == nil {
		t := id
		vertex.Text = &t
	}
	if typ != nil {
		vertex.Type = typ
	}
	vertex.Styles = append(vertex.Styles, style...)
	if vertex.Props == nil {
		vertex.Props = props
	} else {
		for k, v := range props {
			vertex.Props[k] = v
		}
	}

	if doc == nil {
		return
	}
	vertex.Metadata = merge(vertex.Metadata, doc)
	if fields == nil {
		return
	}

	if js.Truthy(fields["shape"]) {
		authored := fields["shape"]
		if hasAuthored {
			authored = authoredShape
		}
		a, isString := authored.(string)
		if !isString {
			db.shapeError("No such shape: "+jsonOf(authored)+".", metadataLoc)
		}
		if a != strings.ToLower(a) || strings.Contains(a, "_") {
			db.shapeError("No such shape: "+a+". Shape names should be lowercase.", metadataLoc)
		}
		shape := fields["shape"].(string)
		if !isValidShape(shape) {
			db.shapeError("No such shape: "+shape+".", metadataLoc)
		}
		vertex.Type = &shape
	}
	if js.Truthy(fields["label"]) {
		t := js.String(fields["label"])
		vertex.Text = &t
		vertex.LabelType = labelType(fields["labelType"])
	}
}

// curveName is the curve an edge asks for. A value that is not a string names no curve
// RoutePath knows, as in pele, where it is compared with the names.
func curveName(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if js.Truthy(v) {
		return "\x00"
	}
	return ""
}

func (db *AgentflowDb) AddSingleLink(start, end string, link *LinkInfo, id string) {
	edge := &AgentEdge{Start: start, End: end, LabelType: "text", Classes: []string{}, Interpolate: db.DefaultInterpolate, Length: -1}
	if link != nil && link.Text != nil {
		edge.Text = unquote(js.Trim(link.Text.Text))
		edge.LabelType = labelType(link.Text.Type)
	}
	if link != nil {
		edge.Type = link.Type
		edge.Stroke = link.Stroke
		if link.Length >= 0 {
			edge.Length = min(link.Length, 10)
		}
		edge.EdgeSemantic = link.EdgeSemantic
	}
	pair := start + "\x00" + end
	existing := db.pairCount[pair]
	if _, taken := db.edgeById[id]; id != "" && !taken {
		edge.ID = &id
		edge.UserDefined = true
	} else {
		n := 0
		if existing != 0 {
			n = existing + 1
		}
		s := "L_" + start + "_" + end + "_" + strconv.Itoa(n)
		edge.ID = &s
	}

	if max := db.options.MaxEdges; max > 0 && len(db.Edges) >= max {
		types.Throw("Edge limit exceeded. The limit is "+strconv.Itoa(max)+".",
			types.CodeLimit, types.ErrorInfo{Type: "agentflow"})
	}
	db.Edges = append(db.Edges, edge)
	db.pairCount[pair] = existing + 1
	if _, ok := db.edgeById[*edge.ID]; !ok {
		db.edgeById[*edge.ID] = edge
	}
}

func (db *AgentflowDb) AddLink(starts, ends []string, link *LinkInfo) {
	id := ""
	if link != nil && link.ID != nil {
		id = strings.Replace(*link.ID, "@", "", 1)
	}
	lastStart := starts[len(starts)-1]
	firstEnd := ends[0]
	for _, start := range starts {
		for _, end := range ends {
			if start == lastStart && end == firstEnd {
				db.AddSingleLink(start, end, link, id)
			} else {
				db.AddSingleLink(start, end, link, "")
			}
		}
	}
}

func (db *AgentflowDb) UpdateLinkInterpolate(positions []string, interpolate string) {
	for _, pos := range positions {
		if pos == "default" {
			db.DefaultInterpolate = &interpolate
		} else {
			db.edgeAt(pos).Interpolate = &interpolate
		}
	}
}

func (db *AgentflowDb) UpdateLink(positions []string, style []string) {
	// pele gives every position the same list and adds fill:none to it at the first edge, so
	// that every position, the default too, ends up with it.
	if len(style) > 0 {
		for _, pos := range positions {
			if pos == "default" {
				continue
			}
			fill := false
			for _, s := range style {
				if strings.HasPrefix(s, "fill") {
					fill = true
				}
			}
			if !fill {
				style = append(style, "fill:none")
			}
			break
		}
	}
	for _, pos := range positions {
		if pos == "default" {
			db.DefaultEdgeStyle = style
			continue
		}
		db.edgeAt(pos).Style = style
	}
}

// edgeAt: Mermaid checks the bounds only for a numeric index and crashes on the strings its own parser passes.
func (db *AgentflowDb) edgeAt(pos string) *AgentEdge {
	k := js.Number(pos)
	if k >= 0 && k < float64(len(db.Edges)) && k == math.Trunc(k) {
		return db.Edges[int(k)]
	}
	types.Throw("The index "+pos+" for linkStyle is out of bounds. Valid indices for linkStyle are between 0 and "+strconv.Itoa(len(db.Edges)-1)+
		". (Help: Ensure that the index is within the range of existing edges.)", types.CodeSemantic, types.ErrorInfo{Type: "agentflow"})
	return nil
}

func (db *AgentflowDb) AddClass(ids string, styles []string) {
	joined := RE_ESCAPED_COMMA.ReplaceAllString(strings.Join(styles, ","), "§§§")
	joined = strings.ReplaceAll(joined, ",", ";")
	joined = strings.ReplaceAll(joined, "§§§", ",")
	style := strings.Split(joined, ";")
	for _, id := range strings.Split(ids, ",") {
		classNode, ok := db.Classes.get(id)
		if !ok {
			classNode = &AgentClassDef{ID: id, Styles: []string{}, TextStyles: []string{}}
			db.Classes.set(id, classNode)
		}
		for _, s := range style {
			if strings.Contains(s, "color") {
				classNode.TextStyles = append(classNode.TextStyles, strings.Replace(s, "fill", "bgFill", 1))
			}
			classNode.Styles = append(classNode.Styles, s)
		}
	}
}

func (db *AgentflowDb) SetDirection(dir string) {
	d := js.Trim(dir)
	if strings.Contains(d, "<") {
		d = "RL"
	}
	if strings.Contains(d, "^") {
		d = "BT"
	}
	if strings.Contains(d, ">") {
		d = "LR"
	}
	if strings.Contains(d, "v") {
		d = "TB"
	}
	if d == "TD" {
		d = "TB"
	}
	db.Direction = &d
}

func (db *AgentflowDb) SetClass(ids, className string) {
	for _, id := range strings.Split(ids, ",") {
		if v, ok := db.Nodes.get(id); ok {
			v.Classes = append(v.Classes, className)
		}
		if e, ok := db.edgeById[id]; ok {
			e.Classes = append(e.Classes, className)
		}
		if s, ok := db.subgraphLookup[id]; ok {
			s.Classes = append(s.Classes, className)
		}
	}
}

func (db *AgentflowDb) SetTooltip(ids string, tooltip string) {
	for _, id := range strings.Split(ids, ",") {
		db.Tooltips[id] = tooltip
	}
}

func (db *AgentflowDb) SetLink(ids, link string, target *string) {
	url := js.Trim(link)
	for _, id := range strings.Split(ids, ",") {
		if vertex, ok := db.Nodes.get(id); ok {
			vertex.Link = nil
			if url != "" {
				s := util.SafeUrl(url)
				vertex.Link = &s
			}
			vertex.LinkTarget = target
		}
	}
	db.SetClass(ids, "clickable")
}

// SetClickEvent: callbacks are never executed; the node is only marked clickable, as in Mermaid's strict mode.
func (db *AgentflowDb) SetClickEvent(ids string) {
	db.SetClass(ids, "clickable")
}

func (db *AgentflowDb) SetAccTitle(text string) {
	db.AccTitle = js.TrimStart(text)
}

func (db *AgentflowDb) SetAccDescription(text string) {
	db.AccDescr = dropIndent(text)
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
			r, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(r) {
				break
			}
			j += size
		}
		b.WriteByte('\n')
		i = j
	}
	return b.String()
}

func (db *AgentflowDb) AddSubGraph(idText *FlowText, list []DocItem, titleText *FlowText) string {
	var id *string
	if idText != nil {
		s := js.Trim(idText.Text)
		id = &s
	}
	title := ""
	if titleText != nil {
		title = titleText.Text
	}
	if idText != nil && idText == titleText && hasSpace(title) {
		id = nil
	}

	seen := map[string]bool{}
	var nodeList []string
	var dir *string
	for _, entry := range list {
		switch entry.Kind {
		case docText:
			if js.Trim(entry.Text) != "" && !seen[entry.Text] {
				seen[entry.Text] = true
				nodeList = append(nodeList, entry.Text)
			}
		case docIDs:
			for _, item := range entry.IDs {
				if js.Trim(item) != "" && !seen[item] {
					seen[item] = true
					nodeList = append(nodeList, item)
				}
			}
		default:
			d := entry.Dir
			dir = &d
		}
	}
	if dir == nil && db.options.InheritDir {
		if db.Direction != nil {
			dir = db.Direction
		} else if db.options.Direction != "" {
			d := db.options.Direction
			dir = &d
		}
	}

	if id == nil {
		s := "subGraph" + strconv.Itoa(db.subCount)
		id = &s
	}
	db.subCount++

	// A flow never contains itself, a globally scoped node, or a node another flow already holds.
	nodes := []string{}
	for _, nodeID := range nodeList {
		if nodeID != *id && !db.globalNodes[nodeID] && !db.members[nodeID] {
			nodes = append(nodes, nodeID)
		}
	}
	for _, nodeID := range nodes {
		db.members[nodeID] = true
	}

	if existing, ok := db.subgraphLookup[*id]; ok {
		// A repeated id adds its members to the first flow; its title and direction win.
		existing.Nodes = append(existing.Nodes, nodes...)
		if js.Trim(title) != "" {
			existing.Title = js.Trim(title)
		}
		if dir != nil && *dir != "" {
			existing.Dir = dir
		}
		return *id
	}
	lt := labelType(nil)
	if titleText != nil {
		lt = labelType(titleText.Type)
	}
	subgraph := &AgentSubgraph{ID: *id, Nodes: nodes, Title: js.Trim(title), Classes: []string{}, Dir: dir, LabelType: lt, Type: "flow"}
	db.Subgraphs = append(db.Subgraphs, subgraph)
	db.subgraphLookup[*id] = subgraph
	return *id
}

func hasSpace(s string) bool {
	for _, r := range s {
		if js.IsSpace(r) {
			return true
		}
	}
	return false
}

// AddGlobal: every node named inside a `global` block stays out of the flows that mention it.
func (db *AgentflowDb) AddGlobal(list []DocItem) {
	release := false
	add := func(item string) {
		id := js.Trim(item)
		if id == "" {
			return
		}
		db.globalNodes[id] = true
		if db.members[id] {
			delete(db.members, id)
			release = true
		}
	}
	for _, entry := range list {
		switch entry.Kind {
		case docText:
			add(entry.Text)
		case docIDs:
			for _, item := range entry.IDs {
				add(item)
			}
		}
	}
	if !release {
		return
	}
	for _, subgraph := range db.Subgraphs {
		kept := subgraph.Nodes[:0]
		for _, nodeID := range subgraph.Nodes {
			if !db.globalNodes[nodeID] {
				kept = append(kept, nodeID)
			}
		}
		subgraph.Nodes = kept
	}
}

func (db *AgentflowDb) AddConnector(textObj *FlowText, titleObj *FlowText) string {
	if textObj == nil {
		return ""
	}
	id := js.Trim(textObj.Text)
	if id == "" {
		return ""
	}
	title := ""
	if titleObj != nil {
		title = js.Trim(titleObj.Text)
	}
	connector, ok := db.Connectors.get(id)
	if !ok {
		text := id
		if title != "" {
			text = title
		}
		created := &AgentNode{ID: id, LabelType: "text", Styles: []string{}, Classes: []string{}, IsConnector: true, Text: &text}
		db.Connectors.set(id, created)
		db.Nodes.set(id, created)
	} else if title != "" {
		connector.Text = &title
	}
	return id
}

func (db *AgentflowDb) DestructLink(end string, start *string) *LinkInfo {
	typ, stroke, length := destructEndLink(end)
	if start != nil && *start != "" {
		// The first half of a split arrow only says whether the line is dotted.
		s := "normal"
		if strings.Contains(*start, ".") {
			s = "dotted"
		}
		if s != stroke {
			return &LinkInfo{Type: "INVALID", Stroke: "INVALID", Length: -1}
		}
	}
	return &LinkInfo{Type: typ, Stroke: stroke, Length: length, EdgeSemantic: edgeSemantic(typ, stroke)}
}

func (db *AgentflowDb) subgraph(id string) *AgentSubgraph { return db.subgraphLookup[id] }

func (db *AgentflowDb) vertexKind(vertex *AgentNode) string {
	if isToolDefinition(vertex) {
		return "tool"
	}
	shape := ""
	if vertex.Type != nil {
		shape = resolveShapeAlias(*vertex.Type)
	}
	switch shape {
	case "hexagon", "hex":
		return "action"
	case "lean-right", "lean_right":
		return "input"
	case "lin-doc", "lined-document":
		return "refdoc"
	case "diamond":
		return "decision"
	}
	return "task"
}

func (db *AgentflowDb) emit(id, severity, message, nodeID string) {
	diagnostic := Diagnostic{ID: id, Severity: severity, Message: message, NodeID: nodeID}
	// The graph may be built more than once; each finding is recorded once.
	for _, d := range db.reported[nodeID] {
		if d == diagnostic {
			return
		}
	}
	db.reported[nodeID] = append(db.reported[nodeID], diagnostic)
	db.Diagnostics = append(db.Diagnostics, diagnostic)
}

func (db *AgentflowDb) emitWarning(id, message, nodeID string) {
	db.emit(id, "warning", message, nodeID)
}

func (db *AgentflowDb) emitError(id, message, nodeID string) { db.emit(id, "error", message, nodeID) }
