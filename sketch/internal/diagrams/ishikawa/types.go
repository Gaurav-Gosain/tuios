// Ported from pele src/diagrams/ishikawa/types.ts at 8aff183be57021b533f319a29341a529be934b08.

package ishikawa

type IshikawaNode struct {
	text     string
	children []*IshikawaNode
}
