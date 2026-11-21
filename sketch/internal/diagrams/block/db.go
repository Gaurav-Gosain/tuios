package block

import (
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

const MAX_SPACES = 100000

func typeStr2Type(typeStr string) string {
	switch typeStr {
	case "[]":
		return "square"
	case "()":
		return "round"
	case "(())":
		return "circle"
	case ">]":
		return "rect_left_inv_arrow"
	case "{}":
		return "diamond"
	case "{{}}":
		return "hexagon"
	case "([])":
		return "stadium"
	case "[[]]":
		return "subroutine"
	case "[()]":
		return "cylinder"
	case "((()))":
		return "doublecircle"
	case "[//]":
		return "lean_right"
	case `[\\]`:
		return "lean_left"
	case `[/\]`:
		return "trapezoid"
	case `[\/]`:
		return "inv_trapezoid"
	case "<[]>":
		return "block_arrow"
	default:
		return "na"
	}
}

func edgeStrToEdgeData(typeStr string) string {
	s := js.Trim(typeStr)
	if s == "" {
		return ""
	}
	switch s[len(s)-1] {
	case 'x':
		return "arrow_cross"
	case 'o':
		return "arrow_circle"
	case '>':
		return "arrow_point"
	default:
		return ""
	}
}

func edgeStrToEdgeStartData(typeStr string) string {
	s := js.Trim(typeStr)
	if s == "" {
		return "arrow_open"
	}
	switch s[0] {
	case 'x':
		return "arrow_cross"
	case 'o':
		return "arrow_circle"
	case '<':
		return "arrow_point"
	default:
		return "arrow_open"
	}
}

func edgeStrToThickness(typeStr string) string {
	if strings.Contains(typeStr, "==") {
		return "thick"
	}
	return "normal"
}

func edgeStrToPattern(typeStr string) string {
	if strings.Contains(typeStr, ".-") {
		return "dotted"
	}
	return "solid"
}

type frame struct {
	list     []*Block
	at       int
	parent   *Block
	children []*Block
	columns  float64
}

func newFrame(list []*Block, parent *Block) *frame {
	columns := -1.0
	for _, block := range list {
		if block.typ == "column-setting" {
			columns = block.columns
			break
		}
	}
	return &frame{list: list, parent: parent, columns: columns}
}

type BlockDb struct {
	title   string
	root    *Block
	blocks  []*Block
	edges   []*Block
	classes map[string]*BlockClassDef
	byId    map[string]*Block

	pending   map[string]bool
	edgeCount map[string]int
	ids       int
	colors    int
	spaces    float64
}

func newBlockDb() *BlockDb {
	return &BlockDb{
		root:      &Block{id: "root", typ: "composite", columns: -1, widthInColumns: 1, width: 1},
		classes:   map[string]*BlockClassDef{},
		byId:      map[string]*Block{},
		pending:   map[string]bool{},
		edgeCount: map[string]int{},
	}
}

func (db *BlockDb) generateId() string {
	db.ids++
	return "id-" + strconv.Itoa(db.ids)
}

func (db *BlockDb) addStyleClass(id, styleAttributes string) {
	found := db.classes[id]
	if found == nil {
		found = &BlockClassDef{id: id}
		db.classes[id] = found
	}
	for _, attribute := range strings.Split(styleAttributes, ",") {
		fixed := attribute
		if semi := strings.IndexByte(attribute, ';'); semi != -1 {
			fixed = attribute[:semi] + attribute[semi+1:]
		}
		fixed = js.Trim(fixed)
		if strings.Contains(attribute, "color") {
			found.textStyles = append(found.textStyles, strings.Replace(strings.Replace(fixed, "fill", "bgFill", 1), "color", "fill", 1))
		}
		found.styles = append(found.styles, fixed)
	}
}

func (db *BlockDb) addStyle2Node(id, styles string) {
	db.named(id).styles = strings.Split(styles, ",")
}

func (db *BlockDb) setCssClass(itemIds, cssClassName string) {
	for _, id := range strings.Split(itemIds, ",") {
		block := db.named(js.Trim(id))
		block.classes = append(block.classes, cssClassName)
	}
}

func (db *BlockDb) named(id string) *Block {
	block := db.byId[id]
	if block == nil {
		block = &Block{id: id, typ: "na", columns: -1, widthInColumns: 1, width: 1}
		db.byId[id] = block
		db.pending[id] = true
	}
	return block
}

func (db *BlockDb) setHierarchy(statements []*Block) {
	stack := []*frame{newFrame(statements, db.root)}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		if f.at == len(f.list) {
			f.parent.children = f.children
			stack = stack[:len(stack)-1]
			continue
		}
		block := f.list[f.at]
		f.at++
		switch block.typ {
		case "classDef":
			db.addStyleClass(block.id, block.css)
			continue
		case "applyClass":
			db.setCssClass(block.id, block.styleClass)
			continue
		case "applyStyles":
			if block.stylesStr != "" {
				db.addStyle2Node(block.id, block.stylesStr)
			}
			continue
		case "column-setting":
			f.parent.columns = block.columns
			continue
		case "edge":
			count := db.edgeCount[block.id] + 1
			db.edgeCount[block.id] = count
			block.id = strconv.Itoa(count) + "-" + block.id
			db.edges = append(db.edges, block)
			continue
		}

		if block.id == "" {
			block.id = db.generateId()
		}
		if block.label == "" {
			if block.typ == "composite" {
				block.label = ""
			} else {
				block.label = block.id
			}
		}
		known := db.byId[block.id]
		fresh := known == nil || db.pending[block.id]
		if known != nil {
			delete(db.pending, block.id)
		}
		if !fresh {
			if block.typ != "na" {
				known.typ = block.typ
			}
			if block.label != block.id {
				known.label = block.label
			}
		} else {
			if known != nil {
				block.classes = known.classes
				block.styles = known.styles
			}
			if block.typ == "composite" {
				block.colorIndex = db.colors
				db.colors++
			}
			db.byId[block.id] = block
		}

		inner := block.children
		block.children = nil
		if block.typ == "space" {
			width := block.width
			if !(width <= MAX_SPACES-db.spaces) {
				types.Throw("A block diagram can hold at most "+strconv.Itoa(MAX_SPACES)+" space blocks.", types.CodeLimit, types.ErrorInfo{Type: "block"})
			}
			db.spaces += max(width, 0)
			for j := 0; float64(j) < width; j++ {
				cp := *block
				cp.id = block.id + "-" + strconv.Itoa(j)
				cp.children = nil
				db.byId[cp.id] = &cp
				f.children = append(f.children, &cp)
			}
		} else if fresh {
			f.children = append(f.children, block)
		}
		if len(inner) > 0 {
			stack = append(stack, newFrame(inner, block))
		}
	}
	db.blocks = db.root.children
}
