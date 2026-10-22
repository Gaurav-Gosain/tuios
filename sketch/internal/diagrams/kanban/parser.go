// Ported from pele src/diagrams/kanban/parser.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

package kanban

import "tuios.dev/sketch/internal/diagrams/common"

func parseKanban(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, true)
}
