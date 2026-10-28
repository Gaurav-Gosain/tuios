// Ported from pele src/diagrams/mindmap/parser.ts at 8aff183be57021b533f319a29341a529be934b08.

package mindmap

import "tuios.dev/sketch/internal/diagrams/common"

func parseMindmap(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, false)
}
