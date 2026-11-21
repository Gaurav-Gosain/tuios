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
