// Ported from pele src/diagrams/kanban/parser.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

package kanban

import "tuios.dev/sketch/internal/diagrams/common"

func parseKanban(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, true)
}
