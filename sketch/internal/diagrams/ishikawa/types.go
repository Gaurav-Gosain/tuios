// Ported from pele src/diagrams/ishikawa/types.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

package ishikawa

type IshikawaNode struct {
	text     string
	children []*IshikawaNode
}
