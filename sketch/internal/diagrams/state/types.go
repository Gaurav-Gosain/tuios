// Ported from pele src/diagrams/state/types.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// A statement is a Stmt: a string or a pointer to one of the statement
// structs. Fields that pele leaves undefined are "" or nil where pele never
// tells them from an empty value, and pointers where it does.

package state

type StateNote struct {
	// Position is "" for none.
	Position string
	Text     string
}

// Description is a state's `description?: string | string[]`.
type Description struct {
	// IsList tells a list from a single string.
	IsList bool
	Text   string
	List   []string
}

// truthy is `if (description)`: a list is always truthy.
func (d *Description) truthy() bool {
	return d != nil && (d.IsList || d.Text != "")
}

type StateStmt struct {
	ID string
	// Type is "" for none.
	Type         string
	Description  *Description
	Descriptions []string
	// Doc is nil for none. It is a pointer so that a document can grow after it is attached, as a JS array does.
	Doc        *[]Stmt
	Note       *StateNote
	Start      *bool
	Classes    []string
	Styles     []string
	TextStyles []string
}

type RelationStmt struct {
	State1      *StateStmt
	State2      *StateStmt
	Description *string
}

type ClassDefStmt struct {
	ID      string
	Classes string
}

type StyleStmt struct {
	ID         string
	StyleClass string
}

type ApplyClassStmt struct {
	ID         string
	StyleClass string
}

type DirectionStmt struct {
	Value string
}

type ClickStmt struct {
	ID      *StateStmt
	URL     string
	Tooltip string
}

// rootStmt is the db's `{ stmt: 'root' }`.
type rootStmt struct {
	ID  string
	Doc *[]Stmt
}

// Mermaid's grammar also leaves the text of statements that have no effect in a document
// (`hide empty description`, `scale`, a bare `state name`, a floating note); those are the strings.
//
// Stmt is a string, *StateStmt, *RelationStmt, *ClassDefStmt, *StyleStmt,
// *ApplyClassStmt, *DirectionStmt or *ClickStmt.
type Stmt = any

type StateRelation struct {
	Id1           string
	Id2           string
	RelationTitle string
}

type StateClassDef struct {
	ID         string
	Styles     []string
	TextStyles []string
}

type StateLink struct {
	URL     string
	Tooltip string
}
