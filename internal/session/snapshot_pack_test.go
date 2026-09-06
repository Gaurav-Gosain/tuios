package session

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// throughWire takes a snapshot the way the socket does: gob out, gob back,
// packed first when asked. Every fidelity test that reads a snapshot goes
// through here in both forms, so the packed form is proved by the same
// comparisons as the cell form.
func throughWire(tb testing.TB, state *TerminalState, packed bool) *TerminalState {
	tb.Helper()
	if packed {
		state.Pack()
		if state.Screen != nil || state.Scrollback != nil || state.MainScreen != nil {
			tb.Fatal("Pack left cells behind")
		}
	}
	codec := GetCodec(CodecGob)
	data, err := codec.Encode(&TerminalStatePayload{PTYID: "x", State: state})
	if err != nil {
		tb.Fatalf("encode: %v", err)
	}
	var payload TerminalStatePayload
	if err := codec.Decode(data, &payload); err != nil {
		tb.Fatalf("decode: %v", err)
	}
	return payload.State
}

// packedGrid is a screen with every shape a cell can take: a wide rune and
// its continuation at the end of a row, a grapheme cluster, styled runs,
// links, an empty tail and a row that is nothing but blanks.
func packedGrid() [][]CellState {
	red := StyleState{FgColor: "a1", Attrs: 1}
	link := StyleState{LinkURL: "https://example.test", LinkParams: "id=1"}
	blank := CellState{Content: " ", Width: 1}
	return [][]CellState{
		{{Content: "h", Width: 1}, {Content: "i", Width: 1}, blank, blank},
		{{Content: "a", Width: 1, StyleState: red}, {Content: "b", Width: 1, StyleState: red}, {Content: "c", Width: 1}, blank},
		{blank, blank, {Content: "日", Width: 2}, {Content: "", Width: 0}},
		{blank, blank, blank, blank},
		{{Content: "é", Width: 1, StyleState: link}, {Content: "x", Width: 1, StyleState: link}, {Content: " ", Width: 1, StyleState: red}, blank},
		{{Content: "wide", Width: 5}, blank, blank, blank},
	}
}

func TestPackedRowsRoundTrip(t *testing.T) {
	want := packedGrid()
	st := &TerminalState{Width: 4, Height: len(want), Screen: packedGrid(), Scrollback: packedGrid()}
	got := throughWire(t, st, true)
	if got.isPacked() {
		// The wire form is unpacked on read, by ApplyTerminalState; a caller
		// that wants the cells calls it the same way.
		if err := got.unpack(); err != nil {
			t.Fatalf("unpack: %v", err)
		}
	}
	for name, rows := range map[string][][]CellState{"screen": got.Screen, "scrollback": got.Scrollback} {
		if len(rows) != len(want) {
			t.Fatalf("%s: %d rows, want %d", name, len(rows), len(want))
		}
		for y := range want {
			if len(rows[y]) != len(want[y]) {
				t.Fatalf("%s row %d: %d cells, want %d", name, y, len(rows[y]), len(want[y]))
			}
			for x := range want[y] {
				if rows[y][x] != want[y][x] {
					t.Errorf("%s row %d col %d: %+v, want %+v", name, y, x, rows[y][x], want[y][x])
				}
			}
		}
	}
	if got.MainScreen != nil {
		t.Errorf("a snapshot with no main screen came back with one")
	}
}

// TestPackedRowsAreSmaller pins the reason the form exists: the packed cells
// of a full screen are a fraction of the gob cells.
func TestPackedRowsAreSmaller(t *testing.T) {
	pty := wirePTY(t, benchWireCols, benchWireRows, 0)
	codec := GetCodec(CodecGob)
	cells, err := codec.Encode(&TerminalStatePayload{PTYID: "x", State: pty.GetTerminalState(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	st := pty.GetTerminalState(0, 0)
	st.Pack()
	packed, err := codec.Encode(&TerminalStatePayload{PTYID: "x", State: st})
	if err != nil {
		t.Fatal(err)
	}
	if len(packed)*4 > len(cells) {
		t.Fatalf("packed screen is %d bytes against %d as cells; the packed form should be at least four times smaller", len(packed), len(cells))
	}
}

// TestPackedRowsRejectCorruption keeps a bad blob from becoming a bad screen.
// Every truncation of a valid blob, and a blob pointing past its style table,
// must unpack to an error and no rows.
func TestPackedRowsRejectCorruption(t *testing.T) {
	st := &TerminalState{Screen: packedGrid()}
	st.Pack()
	good := st.PackedScreen
	for n := range len(good) {
		rows, err := unpackRows(good[:n], st.Styles)
		if err == nil && n > 0 {
			t.Errorf("blob cut to %d of %d bytes unpacked without error", n, len(good))
		}
		if rows != nil {
			t.Errorf("blob cut to %d bytes produced %d rows", n, len(rows))
		}
	}
	if _, err := unpackRows(good, st.Styles[:1]); err == nil {
		t.Error("a style index past the table unpacked without error")
	}
	if _, err := unpackRows([]byte{0xff, 0xff, 0xff}, nil); err == nil {
		t.Error("a row count with no rows unpacked without error")
	}
}

// TestSnapshotIsPackedOnlyOnRequest is the compatibility contract over a real
// daemon: a request that asks for the packed form gets it and nothing else,
// and a request that does not, which is what every older client sends, gets
// the cells it has always read.
func TestSnapshotIsPackedOnlyOnRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	rig := newE2ERig(t)
	rig.run(t, "printf 'pac''ked\\n'", "packed", 5*time.Second)
	rig.drain(200 * time.Millisecond)

	// The client asks for the packed form and unpacks it before anyone else
	// sees it, so what it returns is cells.
	st, err := rig.c.GetTerminalState(rig.ptyID, 0, 0)
	if err != nil {
		t.Fatalf("GetTerminalState: %v", err)
	}
	if st.isPacked() || len(st.Screen) == 0 {
		t.Fatalf("the client returned styles=%d screen rows=%d; every reader expects cells", len(st.Styles), len(st.Screen))
	}
	if !strings.Contains(screenText(st), "packed") {
		t.Fatalf("the snapshot did not carry the pane's text")
	}
	em := vt.NewEmulator(st.Width, st.Height)
	defer func() { _ = em.Close() }()
	ApplyTerminalState(em, st)
	if !screenContains(em, "packed") {
		t.Fatalf("the snapshot did not restore the pane's text:\n%s", em.String())
	}

	// On the wire, the same request is answered packed, and a request
	// without the field, which is what an older client sends, is not.
	for _, tc := range []struct {
		name   string
		packed bool
	}{{"asked", true}, {"not asked", false}} {
		msg, err := NewMessageWithCodec(MsgGetTerminalState, &GetTerminalStatePayload{PTYID: rig.ptyID, IncludeScrollback: true, Packed: tc.packed}, rig.c.codec)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := rig.c.sendAndWaitResponse(msg, MsgTerminalState, MsgError)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var payload TerminalStatePayload
		if err := resp.ParsePayloadWithCodec(&payload, rig.c.codec); err != nil {
			t.Fatal(err)
		}
		if got := payload.State.isPacked(); got != tc.packed {
			t.Fatalf("%s: the reply is packed=%v (styles=%d, screen rows=%d), want packed=%v", tc.name, got, len(payload.State.Styles), len(payload.State.Screen), tc.packed)
		}
		if err := payload.State.unpack(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains([]byte(screenText(payload.State)), []byte("packed")) {
			t.Fatalf("%s: the snapshot did not carry the pane's text", tc.name)
		}
	}
}

func screenContains(t vt.Terminal, s string) bool {
	return strings.Contains(t.String(), s)
}

func screenText(st *TerminalState) string {
	var b strings.Builder
	for _, row := range st.Screen {
		for _, c := range row {
			b.WriteString(c.Content)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// The shapes this package had before the packed form. gob matches struct
// fields by name and ignores the ones the reader does not have, which is the
// whole of why the new field is safe on both sides of a version skew. Kept
// here so that is tested and not argued: a rename or a retype of a field one
// of these names breaks this test rather than a user's session.
type oldGetTerminalStateRequest struct {
	PTYID              string
	IncludeScrollback  bool
	MaxScrollbackLines int
	HaveScrollback     int
}

type oldTerminalState struct {
	Width      int
	Height     int
	CursorX    int
	CursorY    int
	Screen     [][]CellState
	Scrollback [][]CellState
	MainScreen [][]CellState
}

type oldTerminalStateReply struct {
	PTYID string
	State *oldTerminalState
}

// TestOlderPeerReadsTheWire covers the two directions of version skew the
// packed form has to survive, since only one of them can be built from this
// package's own daemon and client.
//
// A newer client asks a daemon that predates the field: the daemon reads the
// request, does not see Packed, and answers with cells. A newer client reads
// that answer: the cells arrive where they always did and unpacking is a
// no-op, so the pane comes back.
func TestOlderPeerReadsTheWire(t *testing.T) {
	codec := GetCodec(CodecGob)

	// Newer client, older daemon: the request still reads.
	asked, err := codec.Encode(&GetTerminalStatePayload{
		PTYID: "p", IncludeScrollback: true, MaxScrollbackLines: 7, HaveScrollback: 3, Packed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var old oldGetTerminalStateRequest
	if err := codec.Decode(asked, &old); err != nil {
		t.Fatalf("a daemon without the Packed field could not read the request: %v", err)
	}
	if old != (oldGetTerminalStateRequest{PTYID: "p", IncludeScrollback: true, MaxScrollbackLines: 7, HaveScrollback: 3}) {
		t.Errorf("the older daemon read the request as %+v; every field it knows must survive", old)
	}

	// Older daemon, newer client: the answer still reads, as cells.
	rows := packedGrid()
	sent, err := codec.Encode(&oldTerminalStateReply{
		PTYID: "p", State: &oldTerminalState{Width: 4, Height: len(rows), Screen: rows},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got TerminalStatePayload
	if err := codec.Decode(sent, &got); err != nil {
		t.Fatalf("a client with the Packed fields could not read the older answer: %v", err)
	}
	if got.State.isPacked() {
		t.Fatalf("an answer carrying cells was read as packed (styles=%d)", len(got.State.Styles))
	}
	if err := got.State.unpack(); err != nil {
		t.Fatalf("unpacking an answer that carried cells must do nothing: %v", err)
	}
	if len(got.State.Screen) != len(rows) {
		t.Fatalf("the older answer came back with %d rows, want %d", len(got.State.Screen), len(rows))
	}
	for y := range rows {
		for x := range rows[y] {
			if got.State.Screen[y][x] != rows[y][x] {
				t.Errorf("row %d col %d: %+v, want %+v", y, x, got.State.Screen[y][x], rows[y][x])
			}
		}
	}
}
