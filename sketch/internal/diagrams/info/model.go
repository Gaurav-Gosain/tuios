// Ported from pele src/diagrams/info/model.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

package info

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

type InfoModel struct {
	title    string
	accTitle string
	accDescr string
}

func buildInfo(ast *InfoAst, title string) *InfoModel {
	model := &InfoModel{title: ast.title}
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
