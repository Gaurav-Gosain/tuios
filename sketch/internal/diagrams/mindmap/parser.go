// Ported from pele src/diagrams/mindmap/parser.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

package mindmap

import "tuios.dev/sketch/internal/diagrams/common"

func parseMindmap(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, false)
}
