// Ported from pele src/diagrams/ishikawa/types.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

package ishikawa

type IshikawaNode struct {
	text     string
	children []*IshikawaNode
}
