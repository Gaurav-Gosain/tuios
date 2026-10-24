// Ported from pele src/diagrams/ishikawa/types.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

package ishikawa

type IshikawaNode struct {
	text     string
	children []*IshikawaNode
}
