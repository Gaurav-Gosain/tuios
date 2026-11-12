package acp

import (
	"encoding/json"
	"fmt"
)

// The calls below are fmt.Errorf, json.Marshal and json.Unmarshal kept out of
// line. The compiler copies each of them into every caller, which costs about
// 100 bytes of binary per call site; tuios keeps one copy of each in
// internal/errfmt and internal/jsonx, and internal/lint holds the binary's
// packages to them. This package imports nothing from tuios, so it keeps its
// own copies here, the one file of the package the lint does not read.

//go:noinline
func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

//go:noinline
func marshal(v any) ([]byte, error) { return json.Marshal(v) }

//go:noinline
func unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
