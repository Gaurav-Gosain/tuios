// Ported from pele src/diagrams/ishikawa/types.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

package ishikawa

type IshikawaNode struct {
	text     string
	children []*IshikawaNode
}
