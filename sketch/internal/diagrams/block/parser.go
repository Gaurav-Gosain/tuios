// Ported from pele src/diagrams/block/parser.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele's statement is a block or a list of them, and it flattens the lists when it
// builds the hierarchy. Here a list of statements is flat from the start; only a
// composite's header remembers whether it was a list.

package block

import (
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type nodeInfo struct {
	id         string
	label      string
	typeStr    string
	directions []string
}

type openFrame struct {
	list   []*Block
	header []*Block
	// isList: the header was a link statement, which pele spreads into an object with no id.
	isList bool
}

const STATEMENT_START = "'NODE_ID', 'COLUMNS', 'SPACE_BLOCK', 'BLOCK_DIAGRAM_KEY', 'id-block', 'classDef', 'class', 'style'"

func parseBlock(src string, db *BlockDb) {
	toks := tokenize(src)
	typs, texts, starts := toks.types, toks.texts, toks.starts
	i := 0

	fail := func(expected string) {
		t := typs[i]
		if t == T_ERROR {
			panic(types.SyntaxError("block", src, starts[i], "", true))
		}
		panic(types.SyntaxError("block", src, starts[i], "Expecting "+expected+", got '"+TOKEN_NAMES[t]+"'", false))
	}

	expect := func(typ int) string {
		if typs[i] != typ {
			fail("'" + TOKEN_NAMES[typ] + "'")
		}
		i++
		return texts[i-1]
	}

	node := func() nodeInfo {
		id := expect(T_NODE_ID)
		if typs[i] == T_NODE_DSTART {
			open := texts[i]
			i++
			label := expect(T_STR)
			return nodeInfo{id: id, label: label, typeStr: open + expect(T_NODE_DEND)}
		}
		if typs[i] == T_BLOCK_ARROW_START {
			open := texts[i]
			i++
			label := expect(T_STR)
			directions := []string{expect(T_DIR)}
			for typs[i] == T_DIR {
				directions = append(directions, texts[i])
				i++
			}
			return nodeInfo{id: id, label: label, typeStr: open + expect(T_BLOCK_ARROW_END), directions: directions}
		}
		return nodeInfo{id: id}
	}

	// nodeStatement gives a block, or a list of them for a statement with links.
	nodeStatement := func() (stmt []*Block, isList bool) {
		first := node()
		size := 1.0
		if typs[i] == T_SIZE {
			size = js.ParseIntPrefix(texts[i], 10)
			i++
		}
		from := newBlock()
		from.id = first.id
		from.label = first.label
		from.typ = typeStr2Type(first.typeStr)
		from.directions = first.directions
		from.widthInColumns = size
		var chain []*Block
		for typs[i] == T_LINK || typs[i] == T_START_LINK {
			label := ""
			i++
			if typs[i-1] == T_START_LINK {
				expect(T_LINK_LABEL)
				label = expect(T_STR)
				expect(T_LINK)
			}
			link := texts[i-1]
			to := node()
			arrowTypeEnd := edgeStrToEdgeData(link)
			arrowTypeStart := edgeStrToEdgeStartData(link)
			thickness := edgeStrToThickness(link)
			pattern := edgeStrToPattern(link)
			// Mermaid keeps only the last link of `A --> B --> C` and loses its start. Here every link counts.
			if chain == nil {
				head := newBlock()
				head.id, head.label, head.typ, head.directions = from.id, from.label, from.typ, from.directions
				chain = []*Block{head}
			}
			from = newBlock()
			from.id, from.label, from.typ, from.directions = to.id, to.label, typeStr2Type(to.typeStr), to.directions
			last := chain[len(chain)-1].id
			edge := newBlock()
			edge.id = last + "-" + to.id
			edge.start = last
			edge.end = to.id
			edge.label = label
			edge.typ = "edge"
			edge.thickness = thickness
			edge.pattern = pattern
			edge.directions = to.directions
			edge.arrowTypeEnd = arrowTypeEnd
			edge.arrowTypeStart = arrowTypeStart
			chain = append(chain, edge, from)
		}
		if chain != nil {
			return chain, true
		}
		return []*Block{from}, false
	}

	expect(T_KEY)
	// Composites nest without recursion: each open one keeps the statement list of its parent.
	var open []openFrame
	var list []*Block
	for {
		switch typs[i] {
		case T_NODE_ID:
			stmt, _ := nodeStatement()
			list = append(list, stmt...)
		case T_COLUMNS:
			b := newBlock()
			b.typ = "column-setting"
			b.columns = js.ParseIntPrefix(texts[i], 10)
			i++
			list = append(list, b)
		case T_SPACE_BLOCK:
			width := js.ParseIntPrefix(texts[i], 10)
			i++
			b := newBlock()
			b.id = db.generateId()
			b.typ = "space"
			b.width = width
			list = append(list, b)
		case T_KEY:
			i++
			open = append(open, openFrame{list: list})
			list = nil
		case T_ID_BLOCK:
			i++
			if typs[i] != T_NODE_ID {
				fail("'NODE_ID'")
			}
			header, isList := nodeStatement()
			open = append(open, openFrame{list: list, header: header, isList: isList})
			list = nil
		case T_end:
			if len(open) == 0 || len(list) == 0 {
				fail(STATEMENT_START)
			}
			f := open[len(open)-1]
			open = open[:len(open)-1]
			i++
			id := db.generateId()
			children := list
			list = f.list
			var b *Block
			switch {
			case f.header == nil:
				b = newBlock()
				b.id = id
				b.label = ""
			case f.isList:
				b = newBlock()
			default:
				cp := *f.header[0]
				b = &cp
			}
			b.typ = "composite"
			b.children = children
			list = append(list, b)
		case T_CLASSDEF:
			i++
			id := js.Trim(expect(T_CLASSDEF_ID))
			b := newBlock()
			b.typ = "classDef"
			b.id = id
			b.css = js.Trim(expect(T_CLASSDEF_STYLEOPTS))
			list = append(list, b)
		case T_CLASS:
			i++
			id := js.Trim(expect(T_CLASSENTITY_IDS))
			b := newBlock()
			b.typ = "applyClass"
			b.id = id
			b.styleClass = js.Trim(expect(T_STYLECLASS))
			list = append(list, b)
		case T_STYLE:
			i++
			id := js.Trim(expect(T_STYLE_ENTITY_IDS))
			b := newBlock()
			b.typ = "applyStyles"
			b.id = id
			b.stylesStr = js.Trim(expect(T_STYLE_DEFINITION_DATA))
			list = append(list, b)
		case T_EOF:
			if len(open) > 0 {
				fail(STATEMENT_START + ", 'end'")
			}
			if len(list) == 0 {
				fail(STATEMENT_START)
			}
			db.setHierarchy(list)
			return
		default:
			if len(list) == 0 {
				fail(STATEMENT_START)
			}
			if len(open) > 0 {
				fail(STATEMENT_START + ", 'end'")
			}
			fail(STATEMENT_START + ", 'EOF'")
		}
	}
}
