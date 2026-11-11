// Ported from pele src/diagrams/registry.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.
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
