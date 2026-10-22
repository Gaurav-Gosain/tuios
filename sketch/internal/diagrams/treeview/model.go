// Ported from pele src/diagrams/treeview/model.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

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
	if len(pre.lineMap) == 0 {
		return parseTreeView(pre.text)
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
		message := err.Message
		if head := strings.IndexByte(message, '\n'); head == -1 {
			message = remapErrorLines(message, pre.lineMap)
		} else {
			message = remapErrorLines(message[:head], pre.lineMap) + message[head:]
		}
		line := err.Line
		if original, ok := pre.lineMap[err.Line]; ok {
			line = original
		}
		panic(types.NewPeleError(message, err.Code, types.ErrorInfo{Type: err.Type, Line: line, Column: err.Column, Snippet: err.Snippet}))
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
