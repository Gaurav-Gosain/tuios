package session

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

// wireCodecGob is the value of the codec byte in every frame header. gob is
// the only payload encoding. The byte stays on the wire so frames keep their
// layout: senders write 0 and readers ignore it. The value 1 once meant JSON
// and stays reserved.
const wireCodecGob byte = 0

// wireCodecName is what the welcome reports in WelcomePayload.Codec. Older
// peers inside the same protocol version still read that field.
const wireCodecName = "gob"

// encodePayload serializes a message payload with gob.
func encodePayload(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("gob encode: %w", err)
	}
	return buf.Bytes(), nil
}

// decodePayload deserializes a gob payload into v. An empty payload leaves v
// untouched. A payload that nests too deeply for gob to decode safely is
// refused before gob sees it: see wire_gobscan.go.
func decodePayload(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	if err := checkGobNesting(data); err != nil {
		return fmt.Errorf("gob decode: %w", err)
	}
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("gob decode: %w", err)
	}
	return nil
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
	gob.Register(SessionEndedPayload{})
	gob.Register(ResizePayload{})
	gob.Register(ErrorPayload{})
	gob.Register(CreatePTYPayload{})
	gob.Register(PTYCreatedPayload{})
	gob.Register(ClosePTYPayload{})
	gob.Register(ResizePTYPayload{})
	gob.Register(PTYResizedPayload{})
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
	gob.Register(RemoteCommandPayload{})
	gob.Register(CommandResultPayload{})
	gob.Register(GetLogsPayload{})
	gob.Register(LogsDataPayload{})
}
