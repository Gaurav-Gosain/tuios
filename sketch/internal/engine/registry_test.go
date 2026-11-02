package engine

import (
	"tuios.dev/sketch/internal/registry/common"
	"tuios.dev/sketch/internal/registry/rare"
)

// The engine's own tests see every type, as tuios.dev/sketch does.
func init() {
	Register(common.Diagrams...)
	Register(rare.Diagrams...)
}
