// Ported from pele src/diagrams/treemap/model.ts at 8aff183be57021b533f319a29341a529be934b08.

package treemap

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

type TreemapNode struct {
	name string
	// Sections have a list, which may be empty. Leaves have none.
	section  bool
	children []*TreemapNode
	// value is NaN when a leaf has none.
	value             float64
	classSelector     string
	cssCompiledStyles []string
}

type TreemapItem struct {
	level             int
	name              string
	leaf              bool
	value             float64
	classSelector     string
	cssCompiledStyles []string
}

type TreemapModel struct {
	title    string
	accTitle string
	accDescr string
	roots    []*TreemapNode
}

// buildHierarchy: a row belongs to the nearest section above it that is indented less.
func buildHierarchy(items []TreemapItem) []*TreemapNode {
	var roots []*TreemapNode
	type entry struct {
		node  *TreemapNode
		level int
	}
	var stack []entry
	for _, item := range items {
		node := &TreemapNode{name: item.name, section: !item.leaf, classSelector: item.classSelector, cssCompiledStyles: item.cssCompiledStyles, value: item.value}
		for len(stack) > 0 && stack[len(stack)-1].level >= item.level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, node)
		} else {
			parent := stack[len(stack)-1].node
			parent.children = append(parent.children, node)
		}
		if !item.leaf {
			stack = append(stack, entry{node, item.level})
		}
	}
	return roots
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

func buildTreemap(ast *TreemapAst, title string) *TreemapModel {
	classes := map[string][]string{}
	for _, row := range ast.TreemapRows {
		if !row.classDef {
			continue
		}
		// An escaped comma stays in the value; the others separate declarations.
		text := strings.ReplaceAll(row.styleText, `\,`, "§§§")
		text = strings.ReplaceAll(text, ",", ";")
		text = strings.ReplaceAll(text, "§§§", ",")
		classes[row.className] = append(classes[row.className], strings.Split(text, ";")...)
	}

	items := make([]TreemapItem, 0, len(ast.TreemapRows))
	for _, row := range ast.TreemapRows {
		if row.classDef {
			continue
		}
		item := row.item
		var styles []string
		if item.classSelector != "" {
			styles = classes[item.classSelector]
		}
		items = append(items, TreemapItem{
			level:             row.indent,
			name:              item.name,
			leaf:              item.leaf,
			value:             item.value,
			classSelector:     item.classSelector,
			cssCompiledStyles: styles,
		})
	}

	model := &TreemapModel{title: ast.title, roots: buildHierarchy(items)}
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
