package mindmap

import "tuios.dev/sketch/internal/diagrams/common"

func parseMindmap(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, false)
}
