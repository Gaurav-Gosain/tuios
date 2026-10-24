// Ported from pele src/diagrams/kanban/parser.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

package kanban

import "tuios.dev/sketch/internal/diagrams/common"

func parseKanban(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, true)
}
