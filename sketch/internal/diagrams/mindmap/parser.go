// Ported from pele src/diagrams/mindmap/parser.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package mindmap

import "tuios.dev/sketch/internal/diagrams/common"

func tokenize(src string) common.OutlineTokens {
	return common.TokenizeOutline(src, false)
}

func parseMindmap(src string, db common.OutlineDb) {
	common.ParseOutline(src, db, false)
}
