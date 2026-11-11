// Ported from pele src/diagrams/mindmap/parser.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

package mindmap

import "tuios.dev/sketch/internal/diagrams/common"

func parseMindmap(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, false)
}
