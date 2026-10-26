// Ported from pele src/diagrams/mindmap/parser.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

package mindmap

import "tuios.dev/sketch/internal/diagrams/common"

func parseMindmap(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, false)
}
