// Ported from pele src/diagrams/registry.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// Only the types ported so far are listed.

package sketch

import (
	"tuios.dev/sketch/internal/engine"
	"tuios.dev/sketch/internal/registry/common"
	"tuios.dev/sketch/internal/registry/rare"
)

func init() {
	engine.Register(common.Diagrams...)
	engine.Register(rare.Diagrams...)
}
