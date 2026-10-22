// Ported from pele src/diagrams/mindmap/parser.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

package mindmap

import "tuios.dev/sketch/internal/diagrams/common"

func parseMindmap(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, false)
}
