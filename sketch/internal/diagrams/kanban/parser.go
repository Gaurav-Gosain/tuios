// Ported from pele src/diagrams/kanban/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package kanban

import "tuios.dev/sketch/internal/diagrams/common"

func parseKanban(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, true)
}
