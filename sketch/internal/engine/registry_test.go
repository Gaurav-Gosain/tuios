package engine

import (
	"tuios.dev/sketch/internal/registry/common"
	"tuios.dev/sketch/internal/registry/rare"
)

func init() {
	Register(common.Diagrams...)
	Register(rare.Diagrams...)
}
