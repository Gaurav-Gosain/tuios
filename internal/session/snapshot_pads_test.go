//go:build !ghostty

package session

// A row that wrapped a column early, because a double-width character did not
// fit in its last column, ends in padding and not in a typed space. The pure
// emulator records that as a flag beside the wrap flag, and a reflow drops the
// padding when it joins the row to the next. The flag has to travel with the
// rows: in a snapshot to a client, and in the history a daemon saves across a
// restart. Without it the far side keeps the padding as a space, and once
// both sides widen, the client shows a line the daemon does not have.

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// padLine wraps at 9 columns before the wide rune, leaving column 9 as
// padding.
const padLine = "abcdefgh世xyz"

// textOf is the history and the screen as the lines the guest printed.
func textOf(t vt.Terminal) string {
	var b strings.Builder
	row := func(cells func(x int) string, n int, wrapped bool) {
		for x := range n {
			b.WriteString(cells(x))
		}
		if !wrapped {
			b.WriteByte('\n')
		}
	}
	for i := range t.ScrollbackLen() {
		line := t.ScrollbackLine(i)
		w, _ := t.ScrollbackSoftWrapped(i)
		row(func(x int) string { return line[x].Content }, len(line), w)
	}
	for y := range t.Height() {
		w, _ := t.RowSoftWrapped(y)
		row(func(x int) string { return t.CellAt(x, y).Content }, t.Width(), w)
	}
	var out []string
	for l := range strings.SplitSeq(b.String(), "\n") {
		if l = strings.TrimRight(l, " "); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestSnapshotCarriesPadding(t *testing.T) {
	for _, packed := range []bool{false, true} {
		for _, inHistory := range []bool{false, true} {
			src := vt.NewWithScrollback(9, 4, 1000)
			stream := padLine + "\r\n"
			if inHistory {
				stream += "1\r\n2\r\n3\r\n4\r\n"
			}
			_, _ = src.Write([]byte(stream))
			dst := vt.NewWithScrollback(9, 4, 1000)
			ApplyTerminalState(dst, throughWire(t, TerminalStateOf(src, 9, 4, 200, 0), packed))
			// Taller as well as wider: the screen takes the history rows
			// back, which is where a history row is laid out again.
			src.Resize(20, 12)
			dst.Resize(20, 12)
			// The positive half: the daemon's own emulator joins the line.
			if got := textOf(src); !strings.Contains(got, padLine) {
				t.Fatalf("packed=%v history=%v: the daemon's line is not whole after widening:\n%s", packed, inHistory, got)
			}
			if a, b := textOf(src), textOf(dst); a != b {
				t.Errorf("packed=%v history=%v: client and daemon differ after widening\ndaemon:\n%s\nclient:\n%s", packed, inHistory, a, b)
			}
		}
	}
}

func TestSavedHistoryCarriesPadding(t *testing.T) {
	src := vt.NewWithScrollback(9, 4, 1000)
	_, _ = src.Write([]byte(padLine + "\r\n1\r\n2\r\n3\r\n4\r\n$ "))
	h := &savedHistory{Version: historyVersion, SavedAt: time.Now(), State: captureHistoryRows(src, 1000).state(1000)}
	h.State.Pack()
	data, err := encodeHistory(h)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeHistory(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	restored := newRestoredEmulator(9, 4, 1000, "", back)
	restored.Resize(20, 12)
	// The positive half: the daemon that saved it joins the line too.
	src.Resize(20, 12)
	if got := textOf(src); !strings.Contains(got, padLine) {
		t.Fatalf("the saving emulator's line is not whole after widening:\n%s", got)
	}
	if got := textOf(restored); !strings.Contains(got, padLine) {
		t.Errorf("the restored history does not hold the line whole at 20 columns:\n%s", got)
	}
}

// TestPaddingFieldsAreOptionalOnTheWire: a peer from before the padding
// fields decodes a snapshot that has them, and a snapshot from such a peer
// decodes here with no padding.
func TestPaddingFieldsAreOptionalOnTheWire(t *testing.T) {
	type oldState struct {
		Width, Height   int
		ScreenWraps     []byte
		ScrollbackWraps []byte
	}
	type oldPayload struct {
		PTYID string
		State *oldState
	}
	src := vt.NewWithScrollback(9, 4, 1000)
	_, _ = src.Write([]byte(padLine))
	state := TerminalStateOf(src, 9, 4, 200, 0)
	if len(state.ScreenPads) == 0 {
		t.Fatal("the snapshot carries no padding flag, so this tests nothing")
	}
	data, err := encodePayload(&TerminalStatePayload{PTYID: "x", State: state})
	if err != nil {
		t.Fatal(err)
	}
	var old oldPayload
	if err := decodePayload(data, &old); err != nil {
		t.Fatalf("an old peer cannot decode a snapshot with padding: %v", err)
	}
	if old.State == nil || !bytes.Equal(old.State.ScreenWraps, state.ScreenWraps) {
		t.Fatalf("an old peer lost the wrap flags: %+v", old.State)
	}

	data, err = encodePayload(&oldPayload{PTYID: "x", State: &oldState{Width: 9, Height: 4, ScreenWraps: state.ScreenWraps}})
	if err != nil {
		t.Fatal(err)
	}
	var cur TerminalStatePayload
	if err := decodePayload(data, &cur); err != nil {
		t.Fatalf("a snapshot from an old peer does not decode: %v", err)
	}
	if cur.State == nil || cur.State.ScreenPads != nil || cur.State.ScrollbackPads != nil {
		t.Fatalf("a snapshot from an old peer decoded with padding: %+v", cur.State)
	}
}
