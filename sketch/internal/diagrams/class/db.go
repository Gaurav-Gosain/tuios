// Ported from pele src/diagrams/class/db.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package class

import (
	"regexp"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/util"
)

const (
	LINE_TYPE_LINE        = 0
	LINE_TYPE_DOTTED_LINE = 1
)

const (
	RELATION_TYPE_AGGREGATION = 0
	RELATION_TYPE_EXTENSION   = 1
	RELATION_TYPE_COMPOSITION = 2
	RELATION_TYPE_DEPENDENCY  = 3
	RELATION_TYPE_LOLLIPOP    = 4
)

// Mermaid looks up ids that start with a digit under this prefix, so they never match a class.
const DOM_ID_PREFIX = "classId-"

var reDescrIndent = regexp.MustCompile(`\n[` + js.S + `]+`)

func splitName(id string) (className string, typ string) {
	at := strings.IndexByte(id, '~')
	if at <= 0 {
		return id, ""
	}
	end := strings.IndexByte(id[at+1:], '~')
	if end == -1 {
		return id[:at], id[at+1:]
	}
	return id[:at], id[at+1 : at+1+end]
}

func className(id string) string {
	name, _ := splitName(id)
	return name
}

func lookupId(id string) string {
	if id != "" && id[0] >= 48 && id[0] <= 57 {
		return className(DOM_ID_PREFIX + id)
	}
	return className(id)
}

// nodeSet is a Set of classes that keeps insertion order.
type nodeSet struct {
	list []*ClassNode
	has  map[*ClassNode]bool
}

type ClassDb struct {
	Direction    string
	Classes      *orderedMap[*ClassNode]
	Relations    []*ClassRelation
	Notes        *orderedMap[*ClassNote]
	Namespaces   *orderedMap[*NamespaceNode]
	Interfaces   []ClassInterface
	StyleClasses *orderedMap[*StyleClass]
	Title        string
	AccTitle     string
	AccDescr     string

	namespaceStack []string
	withCssClass   map[string]*nodeSet
}

func NewClassDb() *ClassDb {
	return &ClassDb{
		Direction:    "TB",
		Classes:      newOrderedMap[*ClassNode](),
		Notes:        newOrderedMap[*ClassNote](),
		Namespaces:   newOrderedMap[*NamespaceNode](),
		StyleClasses: newOrderedMap[*StyleClass](),
		withCssClass: map[string]*nodeSet{},
	}
}

func (db *ClassDb) addClass(id string) {
	className, typ := splitName(id)
	if db.Classes.Has(className) {
		return
	}
	node := &ClassNode{
		ID:         className,
		Type:       typ,
		Label:      className,
		CssClasses: "default",
	}
	db.Classes.Set(className, node)
	db.tag(node, "default")
}

func (db *ClassDb) tag(node *ClassNode, cssClass string) {
	tagged := db.withCssClass[cssClass]
	if tagged == nil {
		tagged = &nodeSet{has: map[*ClassNode]bool{}}
		db.withCssClass[cssClass] = tagged
	}
	if !tagged.has[node] {
		tagged.has[node] = true
		tagged.list = append(tagged.list, node)
	}
}

// Returns the class for a name that may carry a generic type, creating it if needed.
// Mermaid throws a TypeError where this creates, as for an annotation on a class not yet declared.
func (db *ClassDb) ensure(id string) *ClassNode {
	db.addClass(id)
	node, _ := db.Classes.Get(className(id))
	return node
}

func (db *ClassDb) setClassLabel(id, label string) {
	db.ensure(id).Label = label
}

func (db *ClassDb) addRelation(relation *ClassRelation) {
	type1, type2 := relation.Relation.Type1, relation.Relation.Type2
	if type1 == RELATION_TYPE_LOLLIPOP && type2 == NONE {
		db.addClass(relation.ID2)
		relation.ID1 = db.addInterface(relation.ID1, relation.ID2)
	} else if type2 == RELATION_TYPE_LOLLIPOP && type1 == NONE {
		db.addClass(relation.ID1)
		relation.ID2 = db.addInterface(relation.ID2, relation.ID1)
	} else {
		db.addClass(relation.ID1)
		db.addClass(relation.ID2)
	}
	relation.ID1 = className(relation.ID1)
	relation.ID2 = className(relation.ID2)
	relation.RelationTitle1 = js.Trim(relation.RelationTitle1)
	relation.RelationTitle2 = js.Trim(relation.RelationTitle2)
	db.Relations = append(db.Relations, relation)
}

func (db *ClassDb) addInterface(label, classId string) string {
	id := "interface" + strconv.Itoa(len(db.Interfaces))
	db.Interfaces = append(db.Interfaces, ClassInterface{ID: id, Label: label, ClassID: classId})
	return id
}

func (db *ClassDb) addAnnotation(className, annotation string) {
	node := db.ensure(className)
	node.Annotations = append(node.Annotations, annotation)
}

func (db *ClassDb) addMember(className, member string) {
	node := db.ensure(className)
	text := js.Trim(member)
	if strings.HasPrefix(text, "<<") && strings.HasSuffix(text, ">>") {
		node.Annotations = append(node.Annotations, text[2:len(text)-2])
	} else if strings.IndexByte(text, ')') > 0 {
		parsed := NewClassMember(text, "method")
		if parsed.MemberType == "method" {
			node.Methods = append(node.Methods, parsed)
		} else {
			node.Members = append(node.Members, parsed)
		}
	} else if text != "" {
		node.Members = append(node.Members, NewClassMember(text, "attribute"))
	}
}

func (db *ClassDb) addMembers(className string, members []string) {
	for k := len(members) - 1; k >= 0; k-- {
		db.addMember(className, members[k])
	}
}

func (db *ClassDb) addNote(text string, className *string) string {
	index := db.Notes.Len()
	id := "note" + strconv.Itoa(index)
	db.Notes.Set(id, &ClassNote{ID: id, Class: className, Text: text, Index: index})
	return id
}

func (db *ClassDb) cleanupLabel(label string) string {
	return js.Trim(strings.TrimPrefix(label, ":"))
}

func (db *ClassDb) setCssClass(ids, className string) {
	for _, id := range strings.Split(ids, ",") {
		node, ok := db.Classes.Get(lookupId(id))
		if !ok {
			continue
		}
		node.CssClasses += " " + className
		for _, word := range strings.Split(className, " ") {
			db.tag(node, word)
		}
	}
}

func (db *ClassDb) defineClass(ids []string, style []string) {
	for _, id := range ids {
		styleClass, ok := db.StyleClasses.Get(id)
		if !ok {
			styleClass = &StyleClass{ID: id}
			db.StyleClasses.Set(id, styleClass)
		}
		for _, s := range style {
			if strings.Contains(s, "color") {
				styleClass.TextStyles = append(styleClass.TextStyles, strings.Replace(s, "fill", "bgFill", 1))
			}
			styleClass.Styles = append(styleClass.Styles, s)
		}
		// Mermaid tests `cssClasses.includes(id)`, which also matches part of a longer name
		// (a classDef named `def` styles every class, through `default`). Whole names are compared here.
		tagged := db.withCssClass[id]
		if tagged == nil {
			continue
		}
		var parts []string
		for _, s := range style {
			parts = append(parts, strings.Split(s, ",")...)
		}
		for _, node := range tagged.list {
			node.Styles = append(node.Styles, parts...)
		}
	}
}

func (db *ClassDb) setTooltip(ids string, tooltip string) {
	for _, id := range strings.Split(ids, ",") {
		if node, ok := db.Classes.Get(className(id)); ok {
			node.Tooltip = tooltip
		}
	}
}

func (db *ClassDb) setLink(ids, link string, target *string) {
	// Entity codes are resolved first, so that what is checked is what a browser would follow.
	url := js.Trim(text.DecodeEntities(link))
	for _, id := range strings.Split(ids, ",") {
		if node, ok := db.Classes.Get(lookupId(id)); ok {
			node.Link = ""
			if url != "" {
				node.Link = util.SanitizeUrl(url)
			}
			if target != nil {
				node.LinkTarget = *target
			} else {
				node.LinkTarget = "_blank"
			}
		}
	}
	db.setCssClass(ids, "clickable")
}

// Callbacks are never executed; the class is only marked clickable, as in Mermaid's strict mode.
func (db *ClassDb) setClickEvent(ids string) {
	for _, id := range strings.Split(ids, ",") {
		if node, ok := db.Classes.Get(className(id)); ok {
			node.HaveCallback = true
		}
	}
	db.setCssClass(ids, "clickable")
}

func (db *ClassDb) setDirection(dir string) {
	db.Direction = dir
}

func (db *ClassDb) setAccTitle(text string) {
	db.AccTitle = js.TrimStart(text)
}

func (db *ClassDb) setAccDescription(text string) {
	db.AccDescr = reDescrIndent.ReplaceAllLiteralString(text, "\n")
}

func (db *ClassDb) addNamespace(id string, label string) string {
	var prefix string
	if len(db.namespaceStack) > 0 {
		prefix = db.namespaceStack[len(db.namespaceStack)-1]
	}
	qualified := id
	if prefix != "" {
		qualified = prefix + "." + id
	}
	db.namespaceStack = append(db.namespaceStack, qualified)

	if existing, ok := db.Namespaces.Get(qualified); ok {
		existing.Explicit = true
		if label != "" {
			existing.Label = label
		}
		return qualified
	}

	// The enclosing namespace and its ancestors exist already, so only the parts of `id` are walked.
	var parentId *string
	from := 0
	if prefix != "" {
		parentId = &prefix
		from = len(prefix) + 1
	}
	for {
		dot := strings.IndexByte(qualified[from:], '.')
		leaf := dot == -1
		if !leaf {
			dot += from
		}
		current := qualified
		if !leaf {
			current = qualified[:dot]
		}
		node, ok := db.Namespaces.Get(current)
		if !ok {
			part := qualified[from:]
			if !leaf {
				part = qualified[from:dot]
			}
			nodeLabel := part
			if leaf && label != "" {
				nodeLabel = label
			}
			node = &NamespaceNode{
				ID:       current,
				Label:    nodeLabel,
				Classes:  newOrderedMap[*ClassNode](),
				Notes:    newOrderedMap[*ClassNote](),
				Children: newOrderedMap[*NamespaceNode](),
				Parent:   parentId,
				Explicit: leaf,
			}
			db.Namespaces.Set(current, node)
		} else if leaf {
			node.Explicit = true
		}
		var parent *NamespaceNode
		if parentId != nil && *parentId != "" {
			parent, _ = db.Namespaces.Get(*parentId)
		}
		if parent != nil {
			if !parent.Children.Has(current) {
				parent.Children.Set(current, node)
			}
			if node.Parent == nil {
				node.Parent = parentId
			}
		}
		if leaf {
			return qualified
		}
		c := current
		parentId = &c
		from = dot + 1
	}
}

func (db *ClassDb) popNamespace() {
	if len(db.namespaceStack) > 0 {
		db.namespaceStack = db.namespaceStack[:len(db.namespaceStack)-1]
	}
}

func (db *ClassDb) addClassesToNamespace(id string, classNames []string, noteNames []string) {
	namespace, ok := db.Namespaces.Get(id)
	if !ok {
		return
	}
	for _, name := range classNames {
		cn := className(name)
		node, ok := db.Classes.Get(cn)
		if !ok {
			continue
		}
		node.Parent = &id
		namespace.Classes.Set(cn, node)
	}
	for _, name := range noteNames {
		note, ok := db.Notes.Get(name)
		if !ok {
			continue
		}
		note.Parent = &id
		namespace.Notes.Set(name, note)
	}
}

func (db *ClassDb) setCssStyle(id string, styles []string) {
	node, ok := db.Classes.Get(id)
	if !ok {
		return
	}
	for _, s := range styles {
		node.Styles = append(node.Styles, strings.Split(s, ",")...)
	}
}
