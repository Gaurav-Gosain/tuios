// Ported from pele src/diagrams/ishikawa/types.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

package ishikawa

type IshikawaNode struct {
	text     string
	children []*IshikawaNode
}
