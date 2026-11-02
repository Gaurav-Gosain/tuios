// Package core is tuios.dev/sketch with the eight common diagram types:
// flowchart, sequence, state, class, er, gantt, pie and mindmap. It is the
// same API, and about 2.7 MB smaller in a binary. Detect still knows every
// type, so a caller can tell a type this package does not draw (Detect is
// true, Supports is false) from text that is no diagram. tuios.dev/sketch
// draws every type.
package core

import (
	"tuios.dev/sketch/internal/engine"
	"tuios.dev/sketch/internal/registry/common"
)

func init() { engine.Register(common.Diagrams...) }
