package session

import (
	"bytes"
	"encoding/gob"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// wireCodecGob is the value of the codec byte in every frame header. gob is
// the only payload encoding. The byte stays on the wire so frames keep their
// layout: senders write 0 and readers ignore it. The value 1 once meant JSON
// and stays reserved.
const wireCodecGob byte = 0

// wireCodecGobTagged marks a frame whose gob payload is preceded by an 8-byte
// big-endian request id: the id a client put on a request, and the id the
// daemon puts on its answer to it. See Message.ReqID. The id is never 0: a
// message with no id goes out untagged, and a reader refuses a tagged 0.
//
// A reader from before the value existed ignores the codec byte and would take
// the id for the start of the payload, so neither side writes one to a peer
// that has not said it reads them. A client writes tagged requests only to a
// daemon whose welcome set RequestIDs, and the daemon tags only the answer to
// a request that was itself tagged.
const wireCodecGobTagged byte = 2

// reqIDLen is the size of the request id in a tagged frame.
const reqIDLen = 8

// wireCodecName is what the welcome reports in WelcomePayload.Codec. Older
// peers inside the same protocol version still read that field.
const wireCodecName = "gob"

// encodePayload serializes a message payload with gob.
func encodePayload(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, errfmt.Errorf("gob encode: %w", err)
	}
	return buf.Bytes(), nil
}

// withoutCardBodies returns the payload a state push encodes. No client reads
// a card's prompt, history or verify output from a pushed state: the board
// lists cards with list-tasks and reads one with get-task. A push is the whole
// state, so every change to the session would otherwise send every card's
// prompt, history and verify output again, and a session with many long cards
// could pass the frame limit and not be attachable. So a state sync or an
// attach reply whose state holds a card is encoded from a copy that keeps each
// card's other fields and drops those three bodies. The state itself, the
// payload passed in and every verify record stay as they were. A payload with
// no card, or of another type, is returned as it is, with nothing copied.
func withoutCardBodies(payload any) any {
	switch p := payload.(type) {
	case *StateSyncPayload:
		if p != nil && p.State != nil && len(p.State.Tasks) > 0 {
			c := *p
			c.State = stateWithoutCardBodies(p.State)
			return &c
		}
	case *AttachedPayload:
		if p != nil && p.State != nil && len(p.State.Tasks) > 0 {
			c := *p
			c.State = stateWithoutCardBodies(p.State)
			return &c
		}
	}
	return payload
}

// stateWithoutCardBodies copies st with each card replaced by its push copy.
func stateWithoutCardBodies(st *SessionState) *SessionState {
	c := *st
	c.Tasks = make([]TaskState, len(st.Tasks))
	for i, tk := range st.Tasks {
		c.Tasks[i] = tk.pushCopy()
	}
	return &c
}

// decodePayload deserializes a gob payload into v. An empty payload leaves v
// untouched. A payload that nests too deeply for gob to decode safely is
// refused before gob sees it: see wire_gobscan.go.
func decodePayload(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	if err := checkGobNesting(data); err != nil {
		return errfmt.Errorf("gob decode: %w", err)
	}
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return errfmt.Errorf("gob decode: %w", err)
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
	// Under the name gob.Register gave it while it was declared in this
	// package: the package path and the type name. gob sends the name of a
	// value held in an interface, so a peer of an older build and this one
	// have to agree on it.
	gob.RegisterName("github.com/Gaurav-Gosain/tuios/internal/session.TerminalState", TerminalState{})
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
