package acp

import (
	"encoding/json"
	"fmt"
)

//go:noinline
func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

//go:noinline
func marshal(v any) ([]byte, error) { return json.Marshal(v) }

//go:noinline
func unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
