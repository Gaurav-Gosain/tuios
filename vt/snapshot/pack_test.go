package snapshot

import (
	"slices"
	"testing"
)

// packedGrid is cells of every kind the packed form treats apart.
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

// TestPackedRowsRejectCorruption keeps a bad blob from becoming a bad screen.
// Every truncation of a valid blob, and a blob pointing past its style table,
// must unpack to an error and no rows, and fail the check the client runs on
// receipt, so the request fails before any row of it is applied.
func TestPackedRowsRejectCorruption(t *testing.T) {
	st := &State{Screen: packedGrid()}
	st.Pack()
	good := st.PackedScreen
	if err := st.CheckPacked(); err != nil {
		t.Fatalf("a valid snapshot failed the receipt check: %v", err)
	}
	for n := range len(good) {
		rows, err := unpackRows(good[:n], st.Styles)
		if err == nil && n > 0 {
			t.Errorf("blob cut to %d of %d bytes unpacked without error", n, len(good))
		}
		if rows != nil {
			t.Errorf("blob cut to %d bytes produced %d rows", n, len(rows))
		}
		cut := &State{Styles: st.Styles, PackedScreen: good[:n]}
		if err := cut.CheckPacked(); err == nil && n > 0 {
			t.Errorf("blob cut to %d of %d bytes passed the receipt check", n, len(good))
		}
	}
	if err := (&State{Styles: st.Styles[:1], PackedScreen: good}).CheckPacked(); err == nil {
		t.Error("a style index past the table passed the receipt check")
	}
	if _, err := unpackRows(good, st.Styles[:1]); err == nil {
		t.Error("a style index past the table unpacked without error")
	}
	if _, err := unpackRows([]byte{0xff, 0xff, 0xff}, nil); err == nil {
		t.Error("a row count with no rows unpacked without error")
	}
}

// The bitset survives a trip through its own encoding.
func TestWrapBitsRoundTrip(t *testing.T) {
	flags := []bool{true, false, false, true, false, false, false, false, true, true}
	if got := wrapFlags(wrapBits(flags), len(flags)); !slices.Equal(got, flags) {
		t.Errorf("round trip %v, want %v", got, flags)
	}
	if wrapBits(make([]bool, 20)) != nil {
		t.Error("no flag set should put nothing on the wire")
	}
}
