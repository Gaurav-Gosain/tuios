package session

import (
	"bytes"
	"io"
	"testing"
	"testing/iotest"
)

// TestRelayCountsOnlyThePersonsInput: the hub's link for a host follows the
// person using the host, and the relay hears of use from the bytes a client
// sends through it. A client also sends on its own: a state push when the
// far session changes, a ping. Those are not the person, and counting them
// moved the link back to a client nobody was typing in, the moment after
// another client attached. Only input counts.
//
// NEGATIVE CONTROL: usedReader as it was, which counted every read, fails
// "a client's own pushes" and "a split frame header".
func TestRelayCountsOnlyThePersonsInput(t *testing.T) {
	frame := func(typ MessageType, payload string) []byte {
		var b bytes.Buffer
		if err := WriteMessage(&b, &Message{Type: typ, Payload: []byte(payload)}); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	cases := []struct {
		name   string
		stream []byte
		split  bool
		want   bool
	}{
		{"a client's own pushes", cat(frame(MsgUpdateState, "state"), frame(MsgPing, ""), frame(MsgUpdateState, "again")), false, false},
		{"keys after a push", cat(frame(MsgUpdateState, "state"), frame(MsgInput, "ls\r")), false, true},
		{"a resize the client sends again", cat(frame(MsgResize, "80x24"), frame(MsgUpdateState, "state")), false, false},
		{"a split frame header", cat(frame(MsgUpdateState, "a payload of some length"), frame(MsgPing, "")), true, false},
		{"keys in a split frame", cat(frame(MsgUpdateState, "a payload of some length"), frame(MsgInput, "x")), true, true},
		{"a JSON stream", []byte("{\"id\":1,\"verb\":\"list-windows\"}\n"), false, true},
	}
	for _, c := range cases {
		used := 0
		var r io.Reader = bytes.NewReader(c.stream)
		if c.split {
			r = iotest.OneByteReader(r)
		}
		u := &usedReader{r: r, used: func() { used++ }}
		if _, err := io.Copy(io.Discard, u); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := used > 0; got != c.want {
			t.Errorf("ASSERTION: %s: counted as use %v (%d calls), want %v", c.name, got, used, c.want)
		}
	}
}
