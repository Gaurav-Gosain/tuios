// Package session provides persistent session management for TUIOS.
package session

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
)

// CodecType identifies the encoding format used for message payloads.
type CodecType uint8

const (
	// CodecGob is the default binary encoding - fast and efficient for Go-to-Go communication.
	CodecGob CodecType = iota
	// CodecJSON is optional text encoding - for external clients and debugging.
	CodecJSON
)

// String returns the string representation of the codec type.
func (c CodecType) String() string {
	switch c {
	case CodecGob:
		return "gob"
	case CodecJSON:
		return "json"
	default:
		return fmt.Sprintf("unknown(%d)", c)
	}
}

// ParseCodecType parses a string into a CodecType.
func ParseCodecType(s string) CodecType {
	switch s {
	case "json", "JSON":
		return CodecJSON
	default:
		return CodecGob
	}
}

// Codec defines the interface for message payload encoding/decoding.
type Codec interface {
	// Encode serializes a value to bytes.
	Encode(v any) ([]byte, error)
	// Decode deserializes bytes into a value.
	Decode(data []byte, v any) error
	// Type returns the codec type identifier.
	Type() CodecType
}

// GobCodec implements binary encoding using encoding/gob.
// This is the default codec for internal communication.
type GobCodec struct{}

// Encode serializes a value using gob encoding.
func (c *GobCodec) Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("gob encode: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode deserializes gob-encoded bytes into a value.
func (c *GobCodec) Decode(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("gob decode: %w", err)
	}
	return nil
}

// Type returns CodecGob.
func (c *GobCodec) Type() CodecType { return CodecGob }

// JSONCodec implements JSON encoding for external clients.
type JSONCodec struct{}

// Encode serializes a value using JSON encoding.
func (c *JSONCodec) Encode(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("json encode: %w", err)
	}
	return data, nil
}

// Decode deserializes JSON-encoded bytes into a value.
func (c *JSONCodec) Decode(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("json decode: %w", err)
	}
	return nil
}

// Type returns CodecJSON.
func (c *JSONCodec) Type() CodecType { return CodecJSON }

// Singleton codec instances for reuse.
var (
	gobCodec  = &GobCodec{}
	jsonCodec = &JSONCodec{}
)

// GetCodec returns the codec for the given type.
func GetCodec(t CodecType) Codec {
	switch t {
	case CodecJSON:
		return jsonCodec
	default:
		return gobCodec
	}
}

// DefaultCodec returns the default codec (gob).
func DefaultCodec() Codec {
	return gobCodec
}

// Register all payload types with gob.
// This must be called before any gob encoding/decoding.
func init() {
	// Protocol payloads
	gob.Register(HelloPayload{})
	gob.Register(WelcomePayload{})
	gob.Register(AttachPayload{})
	gob.Register(AttachedPayload{})
	gob.Register(NewPayload{})
	gob.Register(SessionInfo{})
	gob.Register(SessionListPayload{})
	gob.Register(KillPayload{})
	gob.Register(ResizePayload{})
	gob.Register(ErrorPayload{})
	gob.Register(PTYInfo{})
	gob.Register(PTYListPayload{})
	gob.Register(CreatePTYPayload{})
	gob.Register(PTYCreatedPayload{})
	gob.Register(ClosePTYPayload{})
	gob.Register(FocusPTYPayload{})
	gob.Register(InputPayload{})
	gob.Register(PTYOutputPayload{})
	gob.Register(ResizePTYPayload{})
	gob.Register(SubscribePTYPayload{})
	gob.Register(GetTerminalStatePayload{})
	gob.Register(TerminalStatePayload{})

	// Session state types
	gob.Register(SessionState{})
	gob.Register(WindowState{})
	gob.Register(TerminalState{})
	gob.Register(SerializedBSPTree{})
	gob.Register(SerializedBSPNode{})

	// Map types used in payloads (gob requires explicit registration)
	gob.Register(map[int]string{})
	gob.Register(map[string]int{})
	gob.Register(map[int]*SerializedBSPTree{})
	gob.Register(map[string]any{})
	// Slice types used in command result Data field
	gob.Register([]map[string]any{})
	gob.Register([]int{})
	gob.Register([]string{})

	// Remote command payloads
	gob.Register(ExecuteCommandPayload{})
	gob.Register(SendKeysPayload{})
	gob.Register(SetConfigPayload{})
	gob.Register(RemoteCommandPayload{})
	gob.Register(CommandResultPayload{})
	gob.Register(GetLogsPayload{})
	gob.Register(LogsDataPayload{})
	gob.Register(WindowListPayload{})
	gob.Register(QueryWindowsPayload{})
	gob.Register(QuerySessionPayload{})
}
