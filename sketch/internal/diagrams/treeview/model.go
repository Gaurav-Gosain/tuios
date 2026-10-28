// Ported from pele src/diagrams/treeview/model.ts at 8aff183be57021b533f319a29341a529be934b08.

package treeview

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type NodeType string

type TreeNode struct {
	id       int
	level    int
	name     string
	nodeType NodeType
	// An icon name as written (`pack:name`, `file`, `folder`), or `none` to hide the icon.
	icon        string
	cssClass    string
	description string
	children    []*TreeNode
}

type TreeViewModel struct {
	title    string
	accTitle string
	accDescr string
	// A directory named `/` at level -1 that holds the top-level entries.
	root  *TreeNode
	count int
}

// TreeViewDb is Mermaid's treeView database: nodes arrive in source order with their indentation.
type TreeViewDb struct {
	count int
	root  *TreeNode
	stack []*TreeNode
}

func newTreeViewDb() *TreeViewDb {
	root := &TreeNode{id: 0, level: -1, name: "/", nodeType: "directory"}
	return &TreeViewDb{count: 1, root: root, stack: []*TreeNode{root}}
}

func (db *TreeViewDb) addNode(level int, name string, nodeType NodeType, cssClass, icon, description string) {
	for level <= db.stack[len(db.stack)-1].level {
		db.stack = db.stack[:len(db.stack)-1]
	}
	node := &TreeNode{id: db.count, level: level, name: name, nodeType: nodeType, icon: icon, cssClass: cssClass, description: description}
	db.count++
	parent := db.stack[len(db.stack)-1]
	parent.children = append(parent.children, node)
	db.stack = append(db.stack, node)
}

func populate(ast *TreeViewAst, db *TreeViewDb) {
	for _, node := range ast.nodes {
		level := node.indent
		name := node.name
		isDirectory := strings.HasSuffix(name, "/")
		if isDirectory {
			name = name[:len(name)-1]
		}
		icon := ""
		if node.hasIcon {
			icon = node.iconAnnotation
			if icon == "" {
				icon = "none"
			}
		}
		nodeType := NodeType("file")
		if isDirectory {
			nodeType = "directory"
		}
		db.addNode(level, name, nodeType, node.classAnnotation, icon, node.descAnnotation)
	}
}

// readTreeView reads the source to a syntax tree, accepting box-drawing input. Errors name lines of the original text.
func readTreeView(source string) *TreeViewAst {
	pre := preprocessBoxDrawing(source)
	// The parser already located errors in unchanged text. It locates them in the source
	// as written only when it reads that very string, as pele's === finds it.
	if pre.text == source {
		return parseTreeView(source)
	}
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		err, ok := r.(*types.PeleError)
		if !ok {
			panic(r)
		}
		line, ok := pre.lineMap[err.Line]
		if !ok {
			panic(r)
		}
		at := 0
		if err.Column > 0 {
			original := source[lineStart(source, line):]
			if column := pre.columns[err.Line]; column != nil {
				at = column(err.Column - 1)
			} else {
				at = unitsToBytes(original, err.Column-1)
			}
		}
		written := types.Locate(source, lineStart(source, line)+at)
		head := strings.IndexByte(err.Message, '\n')
		if head == -1 {
			head = len(err.Message)
		}
		rest := err.Message[head:]
		if err.Snippet != "" {
			rest = strings.Replace(rest, err.Snippet, written.Snippet, 1)
		}
		message := remapErrorLines(err.Message[:head], map[int]int{err.Line: written.Line}) + rest
		info := types.ErrorInfo{Type: err.Type, Line: written.Line}
		if err.Column > 0 {
			info.Column = written.Column
		}
		if err.Snippet != "" {
			info.Snippet = written.Snippet
		}
		panic(types.NewPeleError(message, err.Code, info))
	}()
	return parseTreeView(pre.text)
}

func buildTreeView(ast *TreeViewAst, title string) *TreeViewModel {
	db := newTreeViewDb()
	populate(ast, db)
	model := &TreeViewModel{title: ast.title, root: db.root, count: db.count}
	if model.title == "" {
		model.title = title
	}
	if ast.accTitle != "" {
		model.accTitle = js.TrimStart(ast.accTitle)
	}
	if ast.accDescr != "" {
		model.accDescr = joinLines(ast.accDescr)
	}
	return model
}

// joinLines is .replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}
