// Ported from pele src/diagrams/er/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package er

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

func labelType(typ string) string {
	if typ == "markdown" || typ == "string" || typ == "text" {
		return typ
	}
	return "markdown"
}

type ErDb struct {
	direction      string
	entities       *orderedMap[*ErEntity]
	relationships  []*ErRelationship
	classes        *orderedMap[*ErClassDef]
	subgraphs      []*ErSubgraph
	title          string
	accTitle       string
	accDescr       string
	subgraphLookup map[string]*ErSubgraph
	claimed        map[string]bool
}

func NewErDb() *ErDb {
	return &ErDb{
		direction:      "TB",
		entities:       newOrderedMap[*ErEntity](),
		classes:        newOrderedMap[*ErClassDef](),
		subgraphLookup: map[string]*ErSubgraph{},
		claimed:        map[string]bool{},
	}
}

func (db *ErDb) addEntity(name string, alias string) *ErEntity {
	entity, ok := db.entities.Get(name)
	if !ok {
		entity = &ErEntity{
			id:         "entity-" + name + "-" + strconv.Itoa(db.entities.Len()),
			label:      name,
			alias:      alias,
			cssClasses: "default",
		}
		db.entities.Set(name, entity)
	} else if entity.alias == "" && alias != "" {
		entity.alias = alias
	}
	return entity
}

// The grammar builds the list back to front, so the last attribute written comes first.
func (db *ErDb) addAttributes(name string, attributes []*ParsedAttribute) {
	entity := db.addEntity(name, "")
	for i := len(attributes) - 1; i >= 0; i-- {
		entity.attributes = append(entity.attributes, attributes[i])
	}
}

func (db *ErDb) addRelationship(entA string, roleA string, entB string, relSpec RelSpec) {
	entityA := entA
	if _, ok := db.subgraphLookup[entA]; !ok {
		entityA = db.addEntity(entA, "").id
	}
	entityB := entB
	if _, ok := db.subgraphLookup[entB]; !ok {
		entityB = db.addEntity(entB, "").id
	}
	db.relationships = append(db.relationships, &ErRelationship{entityA: entityA, roleA: roleA, entityB: entityB, relSpec: relSpec})
}

func (db *ErDb) setDirection(dir string) {
	db.direction = dir
}

func (db *ErDb) addCssStyles(ids []string, styles []string) {
	if styles == nil {
		return
	}
	for _, id := range ids {
		if entity, ok := db.entities.Get(id); ok {
			entity.cssStyles = append(entity.cssStyles, styles...)
		}
		if subgraph := db.subgraphLookup[id]; subgraph != nil {
			subgraph.cssStyles = append(subgraph.cssStyles, styles...)
		}
	}
}

func (db *ErDb) addClass(ids []string, styles []string) {
	for _, id := range ids {
		def, ok := db.classes.Get(id)
		if !ok {
			def = &ErClassDef{id: id}
			db.classes.Set(id, def)
		}
		if styles == nil {
			continue
		}
		for _, style := range styles {
			if strings.Contains(style, "color") {
				def.textStyles = append(def.textStyles, strings.Replace(style, "fill", "bgFill", 1))
			}
			def.styles = append(def.styles, style)
		}
	}
}

func (db *ErDb) setClass(ids []string, classNames []string) {
	for _, id := range ids {
		if entity, ok := db.entities.Get(id); ok {
			// One concatenation per entity, not a += per name: Go copies the string on each +=.
			if len(classNames) > 0 {
				entity.cssClasses += " " + strings.Join(classNames, " ")
			}
		}
		if subgraph := db.subgraphLookup[id]; subgraph != nil {
			subgraph.classes = append(subgraph.classes, classNames...)
		}
	}
}

func (db *ErDb) addSubGraph(idText string, list []DocItem, titleText string) string {
	id := js.Trim(idText)
	seen := map[string]bool{}
	var nodes []string
	dir := ""
	for _, item := range list {
		if item.dir {
			dir = item.value
			continue
		}
		trimmed := js.Trim(item.value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		// A node belongs to the first subgraph that lists it.
		if !db.claimed[item.value] {
			nodes = append(nodes, item.value)
		}
	}
	for _, node := range nodes {
		db.claimed[node] = true
	}
	subgraph := &ErSubgraph{
		id:        id,
		nodes:     nodes,
		title:     js.Trim(titleText),
		dir:       dir,
		labelType: labelType(""),
	}
	db.subgraphs = append(db.subgraphs, subgraph)
	db.subgraphLookup[id] = subgraph
	return id
}

func (db *ErDb) setAccTitle(text string) {
	db.accTitle = js.TrimStart(text)
}

// setAccDescription is text.replace(/\n\s+/g, '\n').
func (db *ErDb) setAccDescription(text string) {
	var b strings.Builder
	for i := 0; i < len(text); {
		b.WriteByte(text[i])
		if text[i] != '\n' {
			i++
			continue
		}
		i++
		for i < len(text) {
			r, size := utf8.DecodeRuneInString(text[i:])
			if r == utf8.RuneError || !js.IsSpace(r) {
				break
			}
			i += size
		}
	}
	db.accDescr = b.String()
}
