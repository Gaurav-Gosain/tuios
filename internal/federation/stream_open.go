package federation

import "encoding/json"

// StreamOpen is what the hub says about a connection when it opens the stream
// for it, carried in the open frame's payload. The payload used to be empty,
// and an empty payload still decodes to the zero value, so a hub that sends
// nothing and a proxy that reads nothing keep linking exactly as before.
//
// The payload is written by the hub's mux and never by the client whose bytes
// the stream then carries: those go in data frames. So a field here is the
// hub's own statement, which a client on the hub cannot forge by what it
// sends.
type StreamOpen struct {
	// Human says the hub checked the process that asked for this connection
	// and found it is not inside one of the hub's own panes, so an attach made
	// through it may be issued the nonce that verifies a reply from human. It
	// is false for everything else: a caller in a pane, a connection the hub
	// daemon opens for itself, and every stream from a hub that predates it.
	Human bool `json:"human,omitempty"`
}

// maxStreamOpenPayload bounds what the accepting side decodes. The frame cap
// is a megabyte, and an open frame needs a few bytes.
const maxStreamOpenPayload = 1024

// encode returns the open frame's payload: nothing for the zero value, which
// is what every older peer sends and expects.
func (o StreamOpen) encode() []byte {
	if o == (StreamOpen{}) {
		return nil
	}
	b, err := json.Marshal(o)
	if err != nil {
		return nil
	}
	return b
}

// decodeStreamOpen reads an open frame's payload. Anything it cannot read, or
// anything past the bound, is the zero value: the safe reading of a statement
// that did not arrive intact is that it was not made.
func decodeStreamOpen(payload []byte) StreamOpen {
	var o StreamOpen
	if len(payload) == 0 || len(payload) > maxStreamOpenPayload {
		return o
	}
	if err := json.Unmarshal(payload, &o); err != nil {
		return StreamOpen{}
	}
	return o
}
