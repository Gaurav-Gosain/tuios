// Ported from pele src/diagrams/kanban/parser.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

package kanban

import "tuios.dev/sketch/internal/diagrams/common"

func parseKanban(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, true)
}
