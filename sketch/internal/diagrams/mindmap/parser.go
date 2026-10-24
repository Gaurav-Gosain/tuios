// Ported from pele src/diagrams/mindmap/parser.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

package mindmap

import "tuios.dev/sketch/internal/diagrams/common"

func parseMindmap(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, false)
}
