// Ported from pele src/diagrams/agentflow/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package agentflow

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

func tokenSet(ts ...int) (set [T_EOF + 1]uint8) {
	for _, t := range ts {
		set[t] = 1
	}
	return
}

var (
	ID       = tokenSet(T_NUM, T_NODE_STRING, T_DOWN, T_MINUS, T_DEFAULT, T_COMMA, T_COLON, T_AMP, T_BRKT, T_MULT, T_UNICODE_TEXT)
	ALPHANUM = tokenSet(T_NUM, T_UNICODE_TEXT, T_NODE_STRING, T_DIR, T_DOWN, T_MINUS, T_COMMA, T_COLON, T_AMP, T_BRKT, T_MULT)
	NO_TAGS  = tokenSet(T_NUM, T_NODE_STRING, T_SPACE, T_MINUS, T_AMP, T_UNICODE_TEXT, T_COLON, T_MULT, T_BRKT, T_STYLE, T_LINKSTYLE,
		T_CLASSDEF, T_CLASS, T_CLICK, T_GRAPH, T_DIR, T_flow, T_connector, T_global, T_end, T_DOWN, T_UP, T_START_LINK)
	STYLE_PART = tokenSet(T_NUM, T_NODE_STRING, T_COLON, T_SPACE, T_BRKT, T_STYLE)
	SEPARATOR  = tokenSet(T_NEWLINE, T_SEMI, T_EOF, T_COMMENT)
)

type Frame struct {
	global bool
	// Token index of the `flow` keyword.
	at       int
	id       *FlowText
	title    *FlowText
	data     *string
	dataFrom int
	dataTo   int
	list     []DocItem
	collect  bool
}

const (
	TEXT_TOKENS = "'UNICODE_TEXT', 'TEXT', 'TAGSTART'"
	SEPARATORS  = "'COMMENT', 'SEMI', 'NEWLINE', 'EOF'"
)

// trimBraceTrail is source.replace(/}[^\S\n]*\n/g, '}\n'): Mermaid trims the whitespace between a closing
// brace and the end of its line before parsing. The source is copied only where there is some.
// edits, when not nil, gets each run it trims, as replacements(source, RE_BRACE_TRAIL, 2) lists
// them; a brace right before its line break is left out, as an edit that changes nothing.
func trimBraceTrail(source string, edits *[]types.Edit) string {
	var b strings.Builder
	from := 0
	for i := 0; i < len(source); i++ {
		if source[i] != '}' {
			continue
		}
		j := i + 1
		for j < len(source) {
			r, size := utf8.DecodeRuneInString(source[j:])
			if r == '\n' || !js.IsSpace(r) {
				break
			}
			j += size
		}
		if j == i+1 || j >= len(source) || source[j] != '\n' {
			continue
		}
		if edits != nil {
			*edits = append(*edits, types.Edit{From: i, To: j + 1, Length: 2})
		}
		b.WriteString(source[from : i+1])
		from = j
		i = j - 1
	}
	if from == 0 {
		return source
	}
	b.WriteString(source[from:])
	return b.String()
}

var DIRECTIONS = [...]string{"TB", "BT", "RL", "LR", "TD"}

const STATEMENT_START = "'COMMENT', 'SEMI', 'NEWLINE', 'SPACE', 'EOF', 'flow', 'global', 'connector', 'acc_title', 'acc_descr', 'acc_descr_multiline_value', 'STYLE', 'LINKSTYLE', 'CLASSDEF', 'CLASS', 'CLICK', 'NODE_STRING'"

func strp(s string) *string { return &s }

// parseAgentflow: `raw` parses the text as it is, the way Mermaid's generated parser does without its wrapper.
func parseAgentflow(source string, db *AgentflowDb) {
	src := source
	if strings.Contains(source, "}") {
		src = trimBraceTrail(source, nil)
	}
	tk := tokenize(src)
	typesT, texts, starts, locs := tk.types, tk.texts, tk.starts, tk.locs
	// pele reads past the last token as undefined. Padding with END keeps the port's reads in range.
	last := len(typesT) - 1
	for k := 0; k < 4; k++ {
		typesT = append(typesT, T_END)
		texts = append(texts, "")
		starts = append(starts, len(src))
		locs = append(locs, locs[4*last+2], locs[4*last+3], locs[4*last+2], locs[4*last+3])
	}
	i := 0
	var pendingData *string
	dataFrom, dataTo := 0, 0

	trailing := func(t int) bool { return t == T_COMMENT || t == T_NEWLINE || t == T_SPACE || t == T_EOF }
	fail := func(expected string) {
		t := typesT[i]
		at := starts[i]
		if t == T_EOF || t == T_END {
			// Comments are tokens here, so skip trailing ones.
			k := i
			for k > 0 && trailing(typesT[k-1]) {
				k--
			}
			at = len(js.TrimEnd(src[:starts[k]]))
		}
		if src != source {
			var edits []types.Edit
			trimBraceTrail(source, &edits)
			at = types.InputOffset([][]types.Edit{edits}, at)
		}
		if t == T_ERROR {
			panic(types.SyntaxError("agentflow", source, at, "", true))
		}
		panic(types.SyntaxError("agentflow", source, at, "Expecting "+expected+", got '"+TOKEN_NAMES[t]+"'", false))
	}

	// span is the span from the start of token `a` to the end of token `b`.
	span := func(a, b int) Loc {
		return Loc{FirstLine: locs[4*a], FirstColumn: locs[4*a+1], LastLine: locs[4*b+2], LastColumn: locs[4*b+3]}
	}

	expect := func(typ int, also string) string {
		if typesT[i] != typ {
			s := "'" + TOKEN_NAMES[typ] + "'"
			if also != "" {
				s += ", " + also
			}
			fail(s)
		}
		i++
		return texts[i-1]
	}

	separator := func() {
		if SEPARATOR[typesT[i]] == 0 {
			fail(SEPARATORS)
		}
		i++
	}

	// join is the texts of the tokens from i on that more allows, after the first.
	join := func(more func(t int) bool) string {
		first := i
		i++
		for more(typesT[i]) {
			i++
		}
		if i == first+1 {
			return texts[first]
		}
		var b strings.Builder
		for k := first; k < i; k++ {
			b.WriteString(texts[k])
		}
		return b.String()
	}

	idString := func() string {
		if ID[typesT[i]] == 0 {
			fail("'NODE_STRING'")
		}
		return join(func(t int) bool { return ID[t] == 1 })
	}

	alphaNum := func() string {
		if ALPHANUM[typesT[i]] == 0 {
			fail("'NODE_STRING'")
		}
		return join(func(t int) bool { return ALPHANUM[t] == 1 })
	}

	text := func() *FlowText {
		t := typesT[i]
		var typ string
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
		s := join(func(n int) bool { return n == T_TEXT || n == T_TAGSTART || n == T_TAGEND || n == T_UNICODE_TEXT })
		return &FlowText{Text: s, Type: typ}
	}

	closed := func(typ int) { expect(typ, TEXT_TOKENS) }

	edgeText := func() *FlowText {
		t := typesT[i]
		var typ string
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
		s := join(func(n int) bool { return n == T_EDGE_TEXT || n == T_UNICODE_TEXT })
		return &FlowText{Text: s, Type: typ}
	}

	textNoTags := func() *FlowText {
		t := typesT[i]
		var typ string
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
		s := join(func(n int) bool { return NO_TAGS[n] == 1 })
		return &FlowText{Text: s, Type: typ}
	}

	shapeData := func() string {
		dataFrom = i
		s := join(func(n int) bool { return n == T_SHAPE_DATA })
		dataTo = i - 1
		return s
	}

	attach := func(id string, data string, from, to int) {
		db.AddVertex(id, nil, nil, nil, nil, &data, span(from, to))
	}

	attachData := func(id string, frame *Frame) {
		if frame.data == nil {
			return
		}
		db.AddVertex(id, nil, nil, nil, nil, frame.data, span(frame.dataFrom, frame.dataTo))
	}

	vertex := func() string {
		id := idString()
		var label *FlowText
		typ := ""
		var props map[string]string
		switch typesT[i] {
		case T_SQS:
			i++
			label = text()
			closed(T_SQE)
			typ = "square"
		case T_DOUBLECIRCLESTART:
			i++
			label = text()
			closed(T_DOUBLECIRCLEEND)
			typ = "doublecircle"
		case T_PS:
			i++
			if typesT[i] == T_PS {
				i++
				label = text()
				closed(T_PE)
				expect(T_PE, "")
				typ = "circle"
			} else {
				label = text()
				closed(T_PE)
				typ = "round"
			}
		case T_ELLIPSE_START:
			i++
			label = text()
			closed(T_ELLIPSE_END)
			typ = "ellipse"
		case T_STADIUMSTART:
			i++
			label = text()
			closed(T_STADIUMEND)
			typ = "stadium"
		case T_SUBROUTINESTART:
			i++
			label = text()
			closed(T_SUBROUTINEEND)
			typ = "subroutine"
		case T_VERTEX_WITH_PROPS_START:
			i++
			field := expect(T_NODE_STRING, "")
			expect(T_COLON, "")
			value := expect(T_NODE_STRING, "")
			expect(T_PIPE, "")
			label = text()
			closed(T_SQE)
			typ = "rect"
			props = map[string]string{field: value}
		case T_CYLINDERSTART:
			i++
			label = text()
			closed(T_CYLINDEREND)
			typ = "cylinder"
		case T_DIAMOND_START:
			i++
			if typesT[i] == T_DIAMOND_START {
				i++
				label = text()
				closed(T_DIAMOND_STOP)
				expect(T_DIAMOND_STOP, "")
				typ = "hexagon"
			} else {
				label = text()
				closed(T_DIAMOND_STOP)
				typ = "diamond"
			}
		case T_TAGEND:
			i++
			label = text()
			closed(T_SQE)
			typ = "odd"
		case T_TRAPSTART:
			i++
			label = text()
			if typesT[i] == T_INVTRAPEND {
				i++
				typ = "lean_right"
			} else {
				closed(T_TRAPEND)
				typ = "trapezoid"
			}
		case T_INVTRAPSTART:
			i++
			label = text()
			if typesT[i] == T_TRAPEND {
				i++
				typ = "lean_left"
			} else {
				closed(T_INVTRAPEND)
				typ = "inv_trapezoid"
			}
		}
		switch {
		case props != nil:
			t := typ
			db.AddVertex(id, label, &t, nil, props, nil, Loc{})
		case typ != "":
			t := typ
			db.AddVertex(id, label, &t, nil, nil, nil, Loc{})
		default:
			db.AddVertex(id, nil, nil, nil, nil, nil, Loc{})
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

	node := func() []string {
		ids := []string{styledVertex()}
		pendingData = nil
		for {
			var data *string
			if typesT[i] == T_SHAPE_DATA {
				d := shapeData()
				data = &d
			}
			from, to := dataFrom, dataTo
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
				attach(ids[len(ids)-1], *data, from, to)
			}
			ids = append(ids, next)
		}
	}

	link := func() *LinkInfo {
		var id *string
		if typesT[i] == T_LINK_ID {
			id = &texts[i]
			i++
		}
		if typesT[i] == T_LINK {
			i++
			info := db.DestructLink(texts[i-1], nil)
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
			info := db.DestructLink(end, &start)
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
			attach(stmt[len(stmt)-1], *pendingData, dataFrom, dataTo)
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
				attach(next[len(next)-1], *pendingData, dataFrom, dataTo)
			}
			db.AddLink(stmt, next, info)
			nodes = append(append(make([]string, 0, len(next)+len(nodes)), next...), nodes...)
			stmt = next
		}
	}

	style := func() string {
		if STYLE_PART[typesT[i]] == 0 {
			fail("'NODE_STRING', 'NUM', 'COLON', 'BRKT'")
		}
		return join(func(t int) bool { return STYLE_PART[t] == 1 })
	}

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

	linkTail := func(id, url string) {
		if typesT[i] != T_SPACE {
			db.SetLink(id, url, nil)
			return
		}
		i++
		if typesT[i] == T_LINK_TARGET {
			i++
			db.SetLink(id, url, strp(texts[i-1]))
			return
		}
		tooltip := expect(T_STR, "'LINK_TARGET'")
		if typesT[i] == T_SPACE {
			i++
			db.SetLink(id, url, strp(expect(T_LINK_TARGET, "")))
		} else {
			db.SetLink(id, url, nil)
		}
		db.SetTooltip(id, tooltip)
	}

	clickStatement := func() {
		id := texts[i]
		i++
		switch typesT[i] {
		case T_CALLBACKNAME:
			i++
			if typesT[i] == T_CALLBACKARGS {
				i++
			}
			tooltip := ""
			hasTooltip := false
			if typesT[i] == T_SPACE {
				i++
				tooltip, hasTooltip = expect(T_STR, ""), true
			}
			db.SetClickEvent(id)
			if hasTooltip {
				db.SetTooltip(id, tooltip)
			}
		case T_HREF:
			i++
			linkTail(id, expect(T_STR, ""))
		case T_STR:
			i++
			linkTail(id, texts[i-1])
		default:
			alphaNum()
			if typesT[i] == T_SPACE {
				i++
				tooltip := expect(T_STR, "")
				db.SetClickEvent(id)
				db.SetTooltip(id, tooltip)
			} else {
				db.SetClickEvent(id)
			}
		}
	}

	// header reads `flow` or `connector` up to the separator: an id, an optional bracketed title, optional metadata.
	header := func(at int) *Frame {
		expect(T_SPACE, SEPARATORS)
		id := textNoTags()
		title := &FlowText{Text: "", Type: "text"}
		if typesT[i] == T_SQS {
			i++
			title = text()
			closed(T_SQE)
		}
		var data *string
		if typesT[i] == T_SHAPE_DATA {
			d := shapeData()
			data = &d
		}
		if SEPARATOR[typesT[i]] == 0 {
			fail(SEPARATORS + ", 'SHAPE_DATA'")
		}
		return &Frame{at: at, id: id, title: title, data: data, dataFrom: dataFrom, dataTo: dataTo}
	}

	statement := func() DocItem {
		t := typesT[i]
		switch t {
		case T_STYLE:
			i++
			expect(T_SPACE, "")
			id := idString()
			expect(T_SPACE, "")
			db.AddVertex(id, nil, nil, stylesOpt(), nil, nil, Loc{})
			separator()
			return DocItem{Kind: docIDs}
		case T_LINKSTYLE:
			linkStyleStatement()
			separator()
			return DocItem{Kind: docIDs}
		case T_CLASSDEF:
			i++
			expect(T_SPACE, "")
			id := idString()
			expect(T_SPACE, "")
			db.AddClass(id, stylesOpt())
			separator()
			return DocItem{Kind: docIDs}
		case T_CLASS:
			i++
			expect(T_SPACE, "")
			ids := idString()
			expect(T_SPACE, "")
			db.SetClass(ids, idString())
			separator()
			return DocItem{Kind: docIDs}
		case T_CLICK:
			clickStatement()
			separator()
			return DocItem{Kind: docIDs}
		case T_connector:
			at := i
			i++
			frame := header(at)
			i++
			id := db.AddConnector(frame.id, frame.title)
			attachData(id, frame)
			return DocItem{Kind: docText, Text: id}
		case T_direction_tb, T_direction_bt, T_direction_rl, T_direction_lr, T_direction_td:
			i++
			return DocItem{Kind: docDir, Dir: DIRECTIONS[t-T_direction_tb]}
		case T_acc_title:
			i++
			value := js.Trim(expect(T_acc_title_value, ""))
			db.SetAccTitle(value)
			return DocItem{Kind: docText, Text: value}
		case T_acc_descr:
			i++
			value := js.Trim(expect(T_acc_descr_value, ""))
			db.SetAccDescription(value)
			return DocItem{Kind: docText, Text: value}
		case T_acc_descr_multiline_value:
			value := js.Trim(texts[i])
			i++
			db.SetAccDescription(value)
			return DocItem{Kind: docText, Text: value}
		default:
			if ID[t] == 0 {
				fail(STATEMENT_START)
			}
			nodes := vertexStatement()
			separator()
			return DocItem{Kind: docIDs, IDs: nodes}
		}
	}

	// document: blocks nest without recursion: each open one keeps the list and collect flag of its parent.
	document := func() {
		var open []*Frame
		var list []DocItem
		collect := false
		for {
			t := typesT[i]
			if t == T_END {
				break
			}
			switch {
			case t == T_end:
				if len(open) == 0 {
					goto done
				}
				frame := open[len(open)-1]
				open = open[:len(open)-1]
				i++
				if frame.global {
					db.AddGlobal(list)
					list = frame.list
					collect = frame.collect
					continue
				}
				id := db.AddSubGraph(frame.id, list, frame.title)
				attachData(id, frame)
				list = frame.list
				collect = frame.collect
				if collect {
					list = append(list, DocItem{Kind: docText, Text: id})
				}
			case t == T_SEMI || t == T_NEWLINE || t == T_SPACE || t == T_EOF:
				// A blank separator would be dropped as a member anyway; only `;` is kept.
				if collect && js.Trim(texts[i]) != "" {
					list = append(list, DocItem{Kind: docText, Text: texts[i]})
				}
				i++
			case t == T_COMMENT:
				i++
			case t == T_flow || t == T_global:
				at := i
				i++
				var frame *Frame
				if t == T_global || SEPARATOR[typesT[i]] == 1 {
					frame = &Frame{global: t == T_global, at: at, list: list, collect: collect}
					separator()
				} else {
					frame = header(at)
					frame.list = list
					frame.collect = collect
					i++
				}
				open = append(open, frame)
				list = nil
				collect = true
			default:
				item := statement()
				if collect && (item.Kind != docIDs || len(item.IDs) > 0) {
					list = append(list, item)
				}
			}
		}
	done:
		if len(open) > 0 {
			fail("'end'")
		}
	}

	for typesT[i] == T_SPACE || typesT[i] == T_NEWLINE || typesT[i] == T_COMMENT {
		i++
	}
	expect(T_GRAPH, "'SPACE', 'NEWLINE', 'COMMENT'")
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
		} else if t != T_SEMI && t != T_NEWLINE && t != T_COMMENT {
			fail("'COMMENT', 'SEMI', 'NEWLINE', 'SPACE'")
		}
		i++
		db.SetDirection(dir)
	}
	document()
	if typesT[i] != T_END {
		fail(STATEMENT_START)
	}
}
