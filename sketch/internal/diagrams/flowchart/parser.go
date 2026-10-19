// Ported from pele src/diagrams/flowchart/parser.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package flowchart

import (
	"regexp"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

func tokenSet(list ...int) [T_EOF + 1]uint8 {
	var t [T_EOF + 1]uint8
	for _, k := range list {
		t[k] = 1
	}
	return t
}

var ID = tokenSet(T_NUM, T_NODE_STRING, T_DOWN, T_MINUS, T_DEFAULT, T_COMMA, T_COLON, T_AMP, T_BRKT, T_MULT, T_UNICODE_TEXT)

var ALPHANUM = tokenSet(T_NUM, T_UNICODE_TEXT, T_NODE_STRING, T_DIR, T_DOWN, T_MINUS, T_COMMA, T_COLON, T_AMP, T_BRKT, T_MULT)

var NO_TAGS = tokenSet(T_NUM, T_NODE_STRING, T_SPACE, T_MINUS, T_AMP, T_UNICODE_TEXT, T_COLON, T_MULT, T_BRKT, T_STYLE,
	T_LINKSTYLE, T_CLASSDEF, T_CLASS, T_CLICK, T_GRAPH, T_DIR, T_subgraph, T_end, T_DOWN, T_UP, T_START_LINK)

var STYLE_PART = tokenSet(T_NUM, T_NODE_STRING, T_COLON, T_SPACE, T_BRKT, T_STYLE)

type frame struct {
	id      *FlowText
	title   *FlowText
	list    []DocItem
	collect bool
}

const TEXT_TOKENS = "'UNICODE_TEXT', 'TEXT', 'TAGSTART'"

// Mermaid trims whitespace between a closing brace and the end of its line before parsing.
var RE_BRACE_TRAIL = regexp.MustCompile(`\}[` + js.S + `]*\n`)

var DIRECTIONS = [...]string{"TB", "BT", "RL", "LR", "TD"}

const STATEMENT_START = "'SEMI', 'NEWLINE', 'SPACE', 'EOF', 'subgraph', 'acc_title', 'acc_descr', 'acc_descr_multiline_value', 'STYLE', 'LINKSTYLE', 'CLASSDEF', 'CLASS', 'CLICK', 'NODE_STRING'"

func strp(s string) *string { return &s }

func parseFlowchart(source string, db *FlowDb) {
	src := source
	if strings.Contains(source, "}") {
		src = RE_BRACE_TRAIL.ReplaceAllString(source, "}\n")
	}
	tk := tokenize(src)
	typesT, texts, starts := tk.types, tk.texts, tk.starts
	// pele reads past the last token as undefined. Padding with END keeps the port's reads in range.
	for k := 0; k < 4; k++ {
		typesT = append(typesT, T_END)
		texts = append(texts, "")
		starts = append(starts, len(src))
	}
	i := 0
	var pendingData *string

	fail := func(expected string) {
		t := typesT[i]
		if t == T_ERROR {
			panic(types.SyntaxError("flowchart", src, starts[i], "", true))
		}
		panic(types.SyntaxError("flowchart", src, starts[i], "Expecting "+expected+", got '"+TOKEN_NAMES[t]+"'", false))
	}

	expect := func(typ int, also string) string {
		if typesT[i] != typ {
			s := "'" + TOKEN_NAMES[typ] + "'"
			if also != "" {
				s += ", " + also
			}
			fail(s)
		}
		s := texts[i]
		i++
		return s
	}

	separator := func() {
		t := typesT[i]
		if t != T_NEWLINE && t != T_SEMI && t != T_EOF {
			fail("'SEMI', 'NEWLINE', 'EOF'")
		}
		i++
	}

	run := func(set *[T_EOF + 1]uint8, failWith string) string {
		if set[typesT[i]] == 0 {
			fail(failWith)
		}
		var b strings.Builder
		b.WriteString(texts[i])
		i++
		for set[typesT[i]] == 1 {
			b.WriteString(texts[i])
			i++
		}
		return b.String()
	}

	idString := func() string { return run(&ID, "'NODE_STRING'") }
	alphaNum := func() string { return run(&ALPHANUM, "'NODE_STRING'") }

	text := func() *FlowText {
		t := typesT[i]
		var typ LabelType
		switch {
		case t == T_STR:
			typ = "string"
		case t == T_MD_STR:
			typ = "markdown"
		case t == T_TEXT || t == T_TAGSTART || t == T_TAGEND || t == T_UNICODE_TEXT:
			typ = "text"
		default:
			fail(TEXT_TOKENS + ", 'TAGEND', 'STR', 'MD_STR'")
		}
		var b strings.Builder
		b.WriteString(texts[i])
		i++
		for {
			n := typesT[i]
			if n != T_TEXT && n != T_TAGSTART && n != T_TAGEND && n != T_UNICODE_TEXT {
				break
			}
			b.WriteString(texts[i])
			i++
		}
		return &FlowText{Text: b.String(), Type: typ}
	}

	closed := func(typ int) { expect(typ, TEXT_TOKENS) }

	edgeText := func() *FlowText {
		t := typesT[i]
		var typ LabelType
		switch {
		case t == T_STR:
			typ = "string"
		case t == T_MD_STR:
			typ = "markdown"
		case t == T_EDGE_TEXT || t == T_UNICODE_TEXT:
			typ = "text"
		default:
			fail("'EDGE_TEXT', 'STR', 'MD_STR'")
		}
		var b strings.Builder
		b.WriteString(texts[i])
		i++
		for typesT[i] == T_EDGE_TEXT || typesT[i] == T_UNICODE_TEXT {
			b.WriteString(texts[i])
			i++
		}
		return &FlowText{Text: b.String(), Type: typ}
	}

	textNoTags := func() *FlowText {
		t := typesT[i]
		var typ LabelType
		switch {
		case t == T_STR:
			typ = "text"
		case t == T_MD_STR:
			typ = "markdown"
		case NO_TAGS[t] == 1:
			typ = "text"
		default:
			fail("'NODE_STRING', 'STR', 'MD_STR'")
		}
		var b strings.Builder
		b.WriteString(texts[i])
		i++
		for NO_TAGS[typesT[i]] == 1 {
			b.WriteString(texts[i])
			i++
		}
		return &FlowText{Text: b.String(), Type: typ}
	}

	shapeData := func() string {
		var b strings.Builder
		b.WriteString(texts[i])
		i++
		for typesT[i] == T_SHAPE_DATA {
			b.WriteString(texts[i])
			i++
		}
		return b.String()
	}

	add := func(id string, label *FlowText, typ string) {
		db.AddVertex(id, label, &typ, nil, false, nil, nil, nil, nil)
	}

	vertex := func() string {
		id := idString()
		var label *FlowText
		switch typesT[i] {
		case T_SQS:
			i++
			label = text()
			closed(T_SQE)
			add(id, label, "square")
		case T_DOUBLECIRCLESTART:
			i++
			label = text()
			closed(T_DOUBLECIRCLEEND)
			add(id, label, "doublecircle")
		case T_PS:
			i++
			if typesT[i] == T_PS {
				i++
				label = text()
				closed(T_PE)
				expect(T_PE, "")
				add(id, label, "circle")
			} else {
				label = text()
				closed(T_PE)
				add(id, label, "round")
			}
		case T_ELLIPSE_START:
			i++
			label = text()
			closed(T_ELLIPSE_END)
			add(id, label, "ellipse")
		case T_STADIUMSTART:
			i++
			label = text()
			closed(T_STADIUMEND)
			add(id, label, "stadium")
		case T_SUBROUTINESTART:
			i++
			label = text()
			closed(T_SUBROUTINEEND)
			add(id, label, "subroutine")
		case T_VERTEX_WITH_PROPS_START:
			i++
			field := expect(T_NODE_STRING, "")
			expect(T_COLON, "")
			value := expect(T_NODE_STRING, "")
			expect(T_PIPE, "")
			label = text()
			closed(T_SQE)
			db.AddVertex(id, label, strp("rect"), nil, false, nil, nil, map[string]string{field: value}, nil)
		case T_CYLINDERSTART:
			i++
			label = text()
			closed(T_CYLINDEREND)
			add(id, label, "cylinder")
		case T_DIAMOND_START:
			i++
			if typesT[i] == T_DIAMOND_START {
				i++
				label = text()
				closed(T_DIAMOND_STOP)
				expect(T_DIAMOND_STOP, "")
				add(id, label, "hexagon")
			} else {
				label = text()
				closed(T_DIAMOND_STOP)
				add(id, label, "diamond")
			}
		case T_TAGEND:
			i++
			label = text()
			closed(T_SQE)
			add(id, label, "odd")
		case T_TRAPSTART:
			i++
			label = text()
			if typesT[i] == T_INVTRAPEND {
				i++
				add(id, label, "lean_right")
			} else {
				closed(T_TRAPEND)
				add(id, label, "trapezoid")
			}
		case T_INVTRAPSTART:
			i++
			label = text()
			if typesT[i] == T_TRAPEND {
				i++
				add(id, label, "lean_left")
			} else {
				closed(T_INVTRAPEND)
				add(id, label, "inv_trapezoid")
			}
		default:
			db.AddVertex(id, nil, nil, nil, false, nil, nil, nil, nil)
		}
		return id
	}

	styledVertex := func() string {
		id := vertex()
		if typesT[i] == T_STYLE_SEPARATOR {
			i++
			db.SetClass(id, idString())
		}
		return id
	}

	addData := func(id string, data string) {
		db.AddVertex(id, nil, nil, nil, false, nil, nil, nil, &data)
	}

	node := func() []string {
		ids := []string{styledVertex()}
		pendingData = nil
		for {
			var data *string
			if typesT[i] == T_SHAPE_DATA {
				d := shapeData()
				data = &d
			}
			if typesT[i] != T_SPACE {
				pendingData = data
				return ids
			}
			for {
				i++
				if typesT[i] != T_SPACE {
					break
				}
			}
			if typesT[i] != T_AMP {
				if data != nil {
					fail("'AMP'")
				}
				return ids
			}
			i++
			if typesT[i] != T_SPACE {
				fail("'SPACE'")
			}
			for {
				i++
				if typesT[i] != T_SPACE {
					break
				}
			}
			next := styledVertex()
			if data != nil {
				addData(ids[len(ids)-1], *data)
			}
			ids = append(ids, next)
		}
	}

	toInfo := func(p linkParts) *LinkInfo {
		return &LinkInfo{Type: p.typ, Stroke: p.stroke, Length: p.length}
	}

	link := func() *LinkInfo {
		var id *string
		if typesT[i] == T_LINK_ID {
			id = strp(texts[i])
			i++
		}
		if typesT[i] == T_LINK {
			info := toInfo(db.DestructLink(texts[i], nil))
			i++
			if id != nil {
				info.ID = id
			}
			if typesT[i] == T_PIPE {
				i++
				info.Text = text()
				closed(T_PIPE)
				if typesT[i] == T_SPACE {
					i++
				}
			}
			return info
		}
		if typesT[i] == T_START_LINK {
			start := texts[i]
			i++
			label := edgeText()
			end := expect(T_LINK, "'EDGE_TEXT'")
			info := toInfo(db.DestructLink(end, &start))
			info.Text = label
			if id != nil {
				info.ID = id
			}
			return info
		}
		fail("'LINK', 'START_LINK'")
		return nil
	}

	vertexStatement := func() []string {
		stmt := node()
		if pendingData != nil {
			addData(stmt[len(stmt)-1], *pendingData)
		}
		nodes := stmt
		for {
			t := typesT[i]
			if t != T_LINK && t != T_START_LINK && t != T_LINK_ID {
				return nodes
			}
			info := link()
			next := node()
			if pendingData != nil {
				addData(next[len(next)-1], *pendingData)
			}
			db.AddLink(stmt, next, info)
			nodes = append(append([]string{}, next...), nodes...)
			stmt = next
		}
	}

	style := func() string { return run(&STYLE_PART, "'NODE_STRING', 'NUM', 'COLON', 'BRKT'") }

	stylesOpt := func() []string {
		out := []string{style()}
		for typesT[i] == T_COMMA {
			i++
			out = append(out, style())
		}
		return out
	}

	linkStyleStatement := func() {
		i++
		expect(T_SPACE, "")
		var positions []string
		if typesT[i] == T_DEFAULT {
			positions = []string{texts[i]}
			i++
		} else {
			positions = []string{expect(T_NUM, "'DEFAULT'")}
			for typesT[i] == T_COMMA {
				i++
				positions = append(positions, expect(T_NUM, ""))
			}
		}
		expect(T_SPACE, "")
		if typesT[i] != T_INTERPOLATE {
			db.UpdateLink(positions, stylesOpt())
			return
		}
		i++
		expect(T_SPACE, "")
		interpolate := alphaNum()
		if typesT[i] == T_SPACE {
			i++
			styles := stylesOpt()
			db.UpdateLinkInterpolate(positions, interpolate)
			db.UpdateLink(positions, styles)
		} else {
			db.UpdateLinkInterpolate(positions, interpolate)
		}
	}

	linkTail := func(id string, url string) {
		if typesT[i] != T_SPACE {
			db.SetLink(id, url, nil)
			return
		}
		i++
		if typesT[i] == T_LINK_TARGET {
			db.SetLink(id, url, strp(texts[i]))
			i++
			return
		}
		tooltip := expect(T_STR, "'LINK_TARGET'")
		if typesT[i] == T_SPACE {
			i++
			db.SetLink(id, url, strp(expect(T_LINK_TARGET, "")))
		} else {
			db.SetLink(id, url, nil)
		}
		db.SetTooltip(id, &tooltip)
	}

	clickStatement := func() {
		id := texts[i]
		i++
		switch typesT[i] {
		case T_CALLBACKNAME:
			i++
			hasArgs := typesT[i] == T_CALLBACKARGS
			if hasArgs {
				i++
			}
			var tooltip *string
			if typesT[i] == T_SPACE {
				i++
				tooltip = strp(expect(T_STR, ""))
			}
			db.SetClickEvent(id)
			if tooltip != nil {
				db.SetTooltip(id, tooltip)
			}
			return
		case T_HREF:
			i++
			linkTail(id, expect(T_STR, ""))
			return
		case T_STR:
			url := texts[i]
			i++
			linkTail(id, url)
			return
		default:
			alphaNum()
			if typesT[i] == T_SPACE {
				i++
				tooltip := expect(T_STR, "")
				db.SetClickEvent(id)
				db.SetTooltip(id, &tooltip)
			} else {
				db.SetClickEvent(id)
			}
		}
	}

	subgraphHeader := func() *frame {
		i++
		t := typesT[i]
		if t == T_NEWLINE || t == T_SEMI || t == T_EOF {
			i++
			return &frame{}
		}
		expect(T_SPACE, "'SEMI', 'NEWLINE', 'EOF'")
		id := textNoTags()
		title := id
		if typesT[i] == T_SQS {
			i++
			title = text()
			closed(T_SQE)
		}
		t = typesT[i]
		if t != T_NEWLINE && t != T_SEMI && t != T_EOF {
			fail("'SEMI', 'NEWLINE', 'EOF', 'SQS'")
		}
		i++
		return &frame{id: id, title: title}
	}

	// statement returns the item it adds to an open subgraph's list, and false for none.
	statement := func() (DocItem, bool) {
		t := typesT[i]
		switch t {
		case T_STYLE:
			i++
			expect(T_SPACE, "")
			id := idString()
			expect(T_SPACE, "")
			db.AddVertex(id, nil, nil, stylesOpt(), true, nil, nil, nil, nil)
			separator()
			return DocItem{}, false
		case T_LINKSTYLE:
			linkStyleStatement()
			separator()
			return DocItem{}, false
		case T_CLASSDEF:
			i++
			expect(T_SPACE, "")
			id := idString()
			expect(T_SPACE, "")
			db.AddClass(id, stylesOpt())
			separator()
			return DocItem{}, false
		case T_CLASS:
			i++
			expect(T_SPACE, "")
			ids := idString()
			expect(T_SPACE, "")
			db.SetClass(ids, idString())
			separator()
			return DocItem{}, false
		case T_CLICK:
			clickStatement()
			separator()
			return DocItem{}, false
		case T_direction_tb, T_direction_bt, T_direction_rl, T_direction_lr, T_direction_td:
			i++
			return DocItem{IsDir: true, Value: DIRECTIONS[t-T_direction_tb]}, true
		case T_acc_title:
			i++
			value := js.Trim(expect(T_acc_title_value, ""))
			db.SetAccTitle(value)
			return DocItem{Str: value}, true
		case T_acc_descr:
			i++
			value := js.Trim(expect(T_acc_descr_value, ""))
			db.SetAccDescription(value)
			return DocItem{Str: value}, true
		case T_acc_descr_multiline_value:
			value := js.Trim(texts[i])
			i++
			db.SetAccDescription(value)
			return DocItem{Str: value}, true
		default:
			if ID[t] == 0 {
				fail(STATEMENT_START)
			}
			nodes := vertexStatement()
			separator()
			return DocItem{IsList: true, List: nodes}, len(nodes) > 0
		}
	}

	// Subgraphs nest without recursion: each open one keeps the list and collect flag of its parent.
	document := func() {
		var open []*frame
		var list []DocItem
		collect := false
		for {
			t := typesT[i]
			if t == T_END {
				break
			}
			if t == T_end {
				if len(open) == 0 {
					break
				}
				f := open[len(open)-1]
				open = open[:len(open)-1]
				i++
				id := db.AddSubGraph(f.id, list, f.title)
				list = f.list
				collect = f.collect
				if collect {
					list = append(list, DocItem{Str: id})
				}
			} else if t == T_SEMI || t == T_NEWLINE || t == T_SPACE || t == T_EOF {
				if collect {
					list = append(list, DocItem{Str: texts[i]})
				}
				i++
			} else if t == T_subgraph {
				f := subgraphHeader()
				f.list = list
				f.collect = collect
				open = append(open, f)
				list = nil
				collect = true
			} else {
				item, some := statement()
				if collect && some {
					list = append(list, item)
				}
			}
		}
		if len(open) > 0 {
			fail("'end'")
		}
	}

	for typesT[i] == T_SPACE || typesT[i] == T_NEWLINE {
		i++
	}
	expect(T_GRAPH, "'SPACE', 'NEWLINE'")
	if typesT[i] == T_NODIR {
		i++
		db.SetDirection("TB")
	} else {
		dir := expect(T_DIR, "'NODIR'")
		t := typesT[i]
		if t == T_SPACE {
			for {
				i++
				if typesT[i] != T_SPACE {
					break
				}
			}
			t = typesT[i]
			if t != T_NEWLINE {
				fail("'NEWLINE', 'SPACE'")
			}
		} else if t != T_SEMI && t != T_NEWLINE {
			fail("'SEMI', 'NEWLINE', 'SPACE'")
		}
		i++
		db.SetDirection(dir)
	}
	document()
	if typesT[i] != T_END {
		fail(STATEMENT_START)
	}
}
