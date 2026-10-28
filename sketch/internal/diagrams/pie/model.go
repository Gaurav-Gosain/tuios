// Ported from pele src/diagrams/pie/model.ts at 8aff183be57021b533f319a29341a529be934b08.

package pie

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type PieModel struct {
	title    string
	accTitle string
	accDescr string
	showData bool
	// A Map in pele: labels in the order they were first given.
	labels []string
	values map[string]float64
}

// .replace(/\n\s+/g, '\n')
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

func buildPie(ast *PieAst, title string) *PieModel {
	model := &PieModel{showData: ast.showData, values: map[string]float64{}}
	for _, section := range ast.sections {
		label, value := section.label, section.value
		if value < 0 {
			types.Throw(`"`+label+`" has invalid value: `+js.Num(value)+`. Negative values are not allowed in pie charts. All slice values must be >= 0.`,
				types.CodeSemantic, types.ErrorInfo{Type: "pie"})
		}
		if _, ok := model.values[label]; !ok {
			model.labels = append(model.labels, label)
			model.values[label] = value
		}
	}
	model.title = ast.title
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
