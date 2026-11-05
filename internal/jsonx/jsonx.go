// Package jsonx holds Marshal and Unmarshal, which are json.Marshal and
// json.Unmarshal kept out of line.
//
// Since Go 1.25 encoding/json runs on encoding/json/v2, and json.Marshal and
// json.Unmarshal are one-line calls into it that the compiler inlines at each
// call site, with the default options they pass. That copy costs about 100
// bytes of binary at every call, and tuios has about 630 calls. Calling
// through this package keeps one copy. Use it instead of json.Marshal and
// json.Unmarshal everywhere in the tuios binary; internal/lint checks that.
package jsonx

import "encoding/json"

// Marshal is json.Marshal.
//
//go:noinline
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal is json.Unmarshal.
//
//go:noinline
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
