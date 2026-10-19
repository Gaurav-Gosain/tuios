// Ported from pele src/diagrams/flowchart/db.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package flowchart

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
	"tuios.dev/sketch/internal/util"
)

type FlowDbOptions struct {
	// MaxEdges is 0 for none.
	MaxEdges   int
	InheritDir bool
	Direction  string
	Warn       func(message string)
}

func labelType(typ string) LabelType {
	if typ == "markdown" || typ == "string" || typ == "text" {
		return typ
	}
	return "markdown"
}

func unquote(text string) string {
	if len(text) >= 1 && strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) {
		if len(text) == 1 {
			// substring(1, 0) swaps its arguments: the empty string.
			return ""
		}
		return text[1 : len(text)-1]
	}
	return text
}

func countChar(ch byte, str string) int {
	count := 0
	for i := 0; i < len(str); i++ {
		if str[i] == ch {
			count++
		}
	}
	return count
}

type linkParts struct {
	typ    string
	stroke string
	length *int
}

func destructStartLink(raw string) linkParts {
	str := js.Trim(raw)
	typ := "arrow_open"
	if str != "" {
		switch str[0] {
		case '<':
			typ = "arrow_point"
			str = str[1:]
		case 'x':
			typ = "arrow_cross"
			str = str[1:]
		case 'o':
			typ = "arrow_circle"
			str = str[1:]
		}
	}
	stroke := "normal"
	if strings.Contains(str, "=") {
		stroke = "thick"
	}
	if strings.Contains(str, ".") {
		stroke = "dotted"
	}
	return linkParts{typ: typ, stroke: stroke}
}

func destructEndLink(raw string) linkParts {
	str := js.Trim(raw)
	line := ""
	last := ""
	if str != "" {
		line = str[:len(str)-1]
		last = str[len(str)-1:]
	}
	typ := "arrow_open"
	switch last {
	case "x":
		typ = "arrow_cross"
		if strings.HasPrefix(str, "x") {
			typ = "double_" + typ
			line = sliceFrom1(line)
		}
	case ">":
		typ = "arrow_point"
		if strings.HasPrefix(str, "<") {
			typ = "double_" + typ
			line = sliceFrom1(line)
		}
	case "o":
		typ = "arrow_circle"
		if strings.HasPrefix(str, "o") {
			typ = "double_" + typ
			line = sliceFrom1(line)
		}
	}
	stroke := "normal"
	length := js.Len(line) - 1
	if strings.HasPrefix(line, "=") {
		stroke = "thick"
	}
	if strings.HasPrefix(line, "~") {
		stroke = "invisible"
	}
	dots := countChar('.', line)
	if dots != 0 {
		stroke = "dotted"
		length = dots
	}
	return linkParts{typ: typ, stroke: stroke, length: &length}
}

func sliceFrom1(s string) string {
	if s == "" {
		return ""
	}
	return s[1:]
}

func throwFlow(message string, code types.PeleErrorCode) {
	types.Throw(message, code, types.ErrorInfo{Type: "flowchart"})
}

type FlowDb struct {
	Direction        *string
	Nodes            *orderedMap[*FlowNode]
	Edges            []*FlowEdge
	Subgraphs        []*FlowSubgraph
	Classes          *orderedMap[*FlowClassDef]
	Tooltips         map[string]string
	DefaultEdgeStyle []string
	// HasDefaultEdgeStyle tells a default style that was set from none.
	HasDefaultEdgeStyle bool
	DefaultInterpolate  string
	Title               string
	AccTitle            string
	AccDescr            string

	subgraphLookup map[string]*FlowSubgraph
	members        map[string]bool
	edgeById       map[string]*FlowEdge
	pairCount      map[string]int
	subCount       int
	options        FlowDbOptions
}

func NewFlowDb(options FlowDbOptions) *FlowDb {
	return &FlowDb{
		Nodes:          newOrderedMap[*FlowNode](),
		Classes:        newOrderedMap[*FlowClassDef](),
		Tooltips:       map[string]string{},
		subgraphLookup: map[string]*FlowSubgraph{},
		members:        map[string]bool{},
		edgeById:       map[string]*FlowEdge{},
		pairCount:      map[string]int{},
		options:        options,
	}
}

// AddVertex takes pele's optional arguments as nil pointers and slices.
// hasStyle and hasClasses tell an empty list from none.
func (db *FlowDb) AddVertex(id string, textObj *FlowText, typ *string, style []string, hasStyle bool, classes []string, dir *string, props map[string]string, metadata *string) {
	if id == "" || js.Trim(id) == "" {
		return
	}

	var doc map[string]any
	if metadata != nil {
		yaml := "{\n" + *metadata + "\n}"
		if strings.Contains(*metadata, "\n") {
			yaml = *metadata + "\n"
		}
		parsed, err := util.ParseYaml(yaml)
		if err != nil {
			throwFlow(`Metadata of "`+id+`" is not valid YAML. `+err.Error(), types.CodeSyntax)
		}
		doc, _ = parsed.(map[string]any)
	}

	if subgraph, ok := db.subgraphLookup[id]; ok && doc != nil {
		merged := map[string]any{}
		for k, v := range subgraph.Metadata {
			merged[k] = v
		}
		for k, v := range doc {
			merged[k] = v
		}
		subgraph.Metadata = merged
		return
	}

	if edge, ok := db.edgeById[id]; ok {
		if v, ok := doc["animate"]; ok {
			b, _ := v.(bool)
			edge.Animate = &b
		}
		if v, ok := doc["animation"]; ok {
			s := js.String(v)
			edge.Animation = &s
		}
		if v, ok := doc["curve"]; ok {
			if v == nil {
				edge.Interpolate = ""
			} else {
				edge.Interpolate = js.String(v)
			}
		}
		return
	}

	vertex, ok := db.Nodes.Get(id)
	if !ok {
		if textObj == nil && typ == nil && hasStyle {
			if db.options.Warn != nil {
				db.options.Warn(`Style applied to unknown node "` + id + `". This may indicate a typo. The node will be created automatically.`)
			}
		}
		vertex = &FlowNode{ID: id, LabelType: "text", Styles: []string{}, Classes: []string{}}
		db.Nodes.Set(id, vertex)
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
		vertex.Type = *typ
	}
	if hasStyle {
		vertex.Styles = append(vertex.Styles, style...)
	}
	vertex.Classes = append(vertex.Classes, classes...)
	if dir != nil {
		vertex.Dir = *dir
	}
	if props == nil {
		props = map[string]string{}
	}
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

	if js.Truthy(doc["shape"]) {
		shape := js.String(doc["shape"])
		if shape != strings.ToLower(shape) || strings.Contains(shape, "_") {
			throwFlow("No such shape: "+shape+". Shape names should be lowercase.", types.CodeSemantic)
		}
		if !isValidShape(shape) {
			throwFlow("No such shape: "+shape+".", types.CodeSemantic)
		}
		vertex.Type = shape
	}
	label, labelIsString := doc["label"].(string)
	labelBlank := !labelIsString || js.Trim(label) == ""
	if js.Truthy(doc["label"]) {
		t := js.String(doc["label"])
		vertex.Text = &t
		lt, _ := doc["labelType"].(string)
		if s, ok := doc["labelType"]; ok && s != nil {
			lt = js.String(s)
		}
		vertex.LabelType = labelType(lt)
	}
	if js.Truthy(doc["icon"]) {
		vertex.Icon = js.String(doc["icon"])
		if labelBlank && vertex.Text != nil && *vertex.Text == id {
			e := ""
			vertex.Text = &e
		}
	}
	if js.Truthy(doc["form"]) {
		vertex.Form = js.String(doc["form"])
	}
	if js.Truthy(doc["pos"]) {
		vertex.Pos = js.String(doc["pos"])
	}
	if js.Truthy(doc["img"]) {
		vertex.Img = js.String(doc["img"])
		if labelBlank && vertex.Text != nil && *vertex.Text == id {
			e := ""
			vertex.Text = &e
		}
	}
	if js.Truthy(doc["constraint"]) {
		vertex.Constraint = js.String(doc["constraint"])
	}
	if js.Truthy(doc["w"]) {
		w := toNumber(doc["w"])
		vertex.AssetWidth = &w
	}
	if js.Truthy(doc["h"]) {
		h := toNumber(doc["h"])
		vertex.AssetHeight = &h
	}
}

// toNumber is Number(v) for a YAML value.
func toNumber(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case bool:
		if t {
			return 1
		}
		return 0
	case nil:
		return 0
	case string:
		return js.Number(t)
	case []any:
		if len(t) == 0 {
			return 0
		}
		if len(t) == 1 {
			return toNumber(js.String(t[0]))
		}
	}
	return js.Number("x")
}

func (db *FlowDb) AddSingleLink(start, end string, typ *LinkInfo, id string) {
	edge := &FlowEdge{
		Start:       start,
		End:         end,
		Text:        "",
		LabelType:   "text",
		Classes:     []string{},
		Interpolate: db.DefaultInterpolate,
	}
	if typ != nil && typ.Text != nil {
		edge.Text = unquote(js.Trim(typ.Text.Text))
		edge.LabelType = labelType(typ.Text.Type)
	}
	if typ != nil {
		t := typ.Type
		edge.Type = &t
		s := typ.Stroke
		edge.Stroke = &s
		if typ.Length != nil {
			l := *typ.Length
			if l > 10 {
				l = 10
			}
			edge.Length = &l
		}
	}
	if _, taken := db.edgeById[id]; id != "" && !taken {
		edge.ID = id
		edge.IsUserDefinedId = true
	} else {
		existing := db.pairCount[start+"\x00"+end]
		n := 0
		if existing != 0 {
			n = existing + 1
		}
		edge.ID = "L_" + start + "_" + end + "_" + strconv.Itoa(n)
	}

	if max := db.options.MaxEdges; max > 0 && len(db.Edges) >= max {
		throwFlow("Edge limit exceeded. "+strconv.Itoa(len(db.Edges))+" edges found, but the limit is "+strconv.Itoa(max)+".", types.CodeLimit)
	}
	db.Edges = append(db.Edges, edge)
	pair := start + "\x00" + end
	db.pairCount[pair]++
	if _, ok := db.edgeById[edge.ID]; !ok {
		db.edgeById[edge.ID] = edge
	}
}

func (db *FlowDb) AddLink(starts, ends []string, linkData *LinkInfo) {
	id := ""
	if linkData != nil && linkData.ID != nil {
		id = strings.Replace(*linkData.ID, "@", "", 1)
	}
	lastStart := starts[len(starts)-1]
	firstEnd := ends[0]
	for _, start := range starts {
		for _, end := range ends {
			use := ""
			if start == lastStart && end == firstEnd {
				use = id
			}
			db.AddSingleLink(start, end, linkData, use)
		}
	}
}

// A position is "default" or an index.
func (db *FlowDb) UpdateLinkInterpolate(positions []string, interpolate string) {
	for _, pos := range positions {
		if pos == "default" {
			db.DefaultInterpolate = interpolate
		} else {
			db.edgeAt(pos).Interpolate = interpolate
		}
	}
}

func (db *FlowDb) UpdateLink(positions []string, style []string) {
	// pele stores one array for every position and pushes `fill:none` onto it at the first
	// edge, so every holder of it, the default style included, ends up with the fill.
	final := style
	for _, pos := range positions {
		if pos != "default" && len(style) > 0 {
			fill := false
			for _, s := range style {
				if strings.HasPrefix(s, "fill") {
					fill = true
				}
			}
			if !fill {
				final = append(slices.Clone(style), "fill:none")
			}
			break
		}
	}
	for _, pos := range positions {
		if pos == "default" {
			db.DefaultEdgeStyle = final
			db.HasDefaultEdgeStyle = true
			continue
		}
		db.edgeAt(pos).Style = final
	}
}

func (db *FlowDb) edgeAt(pos string) *FlowEdge {
	n := js.Number(pos)
	if n == n && n >= 0 && n == float64(int(n)) && int(n) < len(db.Edges) {
		return db.Edges[int(n)]
	}
	throwFlow("The index "+pos+" for linkStyle is out of bounds. Valid indices for linkStyle are between 0 and "+
		strconv.Itoa(len(db.Edges)-1)+". (Help: Ensure that the index is within the range of existing edges.)", types.CodeSemantic)
	return nil
}

func (db *FlowDb) AddClass(ids string, styles []string) {
	joined := strings.Join(styles, ",")
	joined = strings.ReplaceAll(joined, `\,`, "§§§")
	joined = strings.ReplaceAll(joined, ",", ";")
	joined = strings.ReplaceAll(joined, "§§§", ",")
	style := strings.Split(joined, ";")
	for _, id := range strings.Split(ids, ",") {
		classNode, ok := db.Classes.Get(id)
		if !ok {
			classNode = &FlowClassDef{ID: id, Styles: []string{}, TextStyles: []string{}}
			db.Classes.Set(id, classNode)
		}
		for _, s := range style {
			if strings.Contains(s, "color") {
				classNode.TextStyles = append(classNode.TextStyles, strings.Replace(s, "fill", "bgFill", 1))
			}
			classNode.Styles = append(classNode.Styles, s)
		}
	}
}

func (db *FlowDb) SetDirection(dir string) {
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

func (db *FlowDb) SetClass(ids string, className string) {
	for _, id := range strings.Split(ids, ",") {
		if v, ok := db.Nodes.Get(id); ok {
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

// SetTooltip takes nil for pele's undefined.
func (db *FlowDb) SetTooltip(ids string, tooltip *string) {
	if tooltip == nil {
		return
	}
	for _, id := range strings.Split(ids, ",") {
		db.Tooltips[id] = *tooltip
	}
}

func (db *FlowDb) SetLink(ids string, link string, target *string) {
	url := js.Trim(link)
	for _, id := range strings.Split(ids, ",") {
		if vertex, ok := db.Nodes.Get(id); ok {
			if url != "" {
				u := util.SafeUrl(url)
				vertex.Link = &u
			} else {
				vertex.Link = nil
			}
			vertex.LinkTarget = target
		}
	}
	db.SetClass(ids, "clickable")
}

// SetClickEvent: callbacks are never executed; the node is only marked clickable, as in Mermaid's strict mode.
func (db *FlowDb) SetClickEvent(ids string) {
	db.SetClass(ids, "clickable")
}

var reDescrIndent = regexp.MustCompile(`\n[` + js.S + `]+`)

func (db *FlowDb) SetAccTitle(text string) {
	db.AccTitle = js.TrimStart(text)
}

func (db *FlowDb) SetAccDescription(text string) {
	db.AccDescr = reDescrIndent.ReplaceAllString(text, "\n")
}

// AddSubGraph takes the id and title as pointers: pele compares them by identity.
func (db *FlowDb) AddSubGraph(idText *FlowText, list []DocItem, titleText *FlowText) string {
	var id *string
	if idText != nil {
		t := js.Trim(idText.Text)
		id = &t
	}
	title := ""
	if titleText != nil {
		title = titleText.Text
	}
	if idText != nil && idText == titleText && strings.ContainsFunc(title, js.IsSpace) {
		id = nil
	}

	seen := map[string]bool{}
	var nodeList []string
	var dir *string
	for _, entry := range list {
		switch {
		case entry.IsDir:
			v := entry.Value
			dir = &v
		case entry.IsList:
			for _, item := range entry.List {
				if js.Trim(item) != "" && !seen[item] {
					seen[item] = true
					nodeList = append(nodeList, item)
				}
			}
		default:
			if js.Trim(entry.Str) != "" && !seen[entry.Str] {
				seen[entry.Str] = true
				nodeList = append(nodeList, entry.Str)
			}
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

	lt := ""
	if titleText != nil {
		lt = titleText.Type
	}
	subgraph := &FlowSubgraph{
		ID:        *id,
		Title:     js.Trim(title),
		Classes:   []string{},
		LabelType: labelType(lt),
	}
	if dir != nil {
		subgraph.Dir = *dir
	}
	// A node belongs to the first subgraph that lists it, and a subgraph never contains itself.
	subgraph.Nodes = []string{}
	for _, nodeID := range nodeList {
		if !db.members[nodeID] && nodeID != *id {
			subgraph.Nodes = append(subgraph.Nodes, nodeID)
		}
	}
	for _, nodeID := range subgraph.Nodes {
		db.members[nodeID] = true
	}

	existing, ok := db.subgraphLookup[*id]
	if ok {
		existing.Nodes = append(existing.Nodes, subgraph.Nodes...)
	} else {
		db.Subgraphs = append(db.Subgraphs, subgraph)
		existing = subgraph
	}
	db.subgraphLookup[*id] = existing
	return *id
}

func (db *FlowDb) DestructLink(end string, start *string) linkParts {
	info := destructEndLink(end)
	if start == nil || *start == "" {
		return info
	}
	startInfo := destructStartLink(*start)
	if startInfo.stroke != info.stroke {
		return linkParts{typ: "INVALID", stroke: "INVALID"}
	}
	if startInfo.typ == "arrow_open" {
		startInfo.typ = info.typ
	} else {
		if startInfo.typ != info.typ {
			return linkParts{typ: "INVALID", stroke: "INVALID"}
		}
		startInfo.typ = "double_" + startInfo.typ
	}
	if startInfo.typ == "double_arrow" {
		startInfo.typ = "double_arrow_point"
	}
	startInfo.length = info.length
	return startInfo
}

func (db *FlowDb) Subgraph(id string) (*FlowSubgraph, bool) {
	s, ok := db.subgraphLookup[id]
	return s, ok
}
