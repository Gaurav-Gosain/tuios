package acp

import (
	"encoding/json"
	"fmt"
)

// The calls below are fmt.Errorf, json.Marshal and json.Unmarshal kept out of
// line. The compiler inlines each of them into every caller, which costs about
// 100 bytes of binary per call site; one copy each, called from every site,
// keeps a program that links this package smaller.

//go:noinline
func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

//go:noinline
func marshal(v any) ([]byte, error) { return json.Marshal(v) }

//go:noinline
func unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
