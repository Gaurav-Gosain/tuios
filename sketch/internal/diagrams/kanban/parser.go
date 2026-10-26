// Ported from pele src/diagrams/kanban/parser.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

package kanban

import "tuios.dev/sketch/internal/diagrams/common"

func parseKanban(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, true)
}
