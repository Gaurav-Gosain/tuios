package tuie2e

import (
	"encoding/binary"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStreamPaneSendsRowIDsOnlyWhenAsked: the H frame, the id of the first
// history row of the next snapshot, goes only to a stream that asked for it
// with row_ids. A stream that did not ask reads S first, as the protocol had
// it before H, and no H after it either, through a repaint snapshot. A stream
// that asked reads H right before S, and the id names the snapshot's first
// history row: pane-history before it ends on the row just above.
//
// How this could pass wrongly, written down first:
//   - H could come later than the first frame, on the repaint snapshot. The
//     stream that did not ask reads every frame up to and past a repaint S.
//   - The stream that asked could get no H, and the check of the first frame
//     alone would pass. It must read H, then S.
//   - The id could be off by some rows and still be a number. The rows come
//     from seq, so the row before the id must be one less than the first row
//     the snapshot draws.
//
// The frames go to the stream-pane transcript as the artifact.
func TestStreamPaneSendsRowIDsOnlyWhenAsked(t *testing.T) {
	base, _, ctl, w1, _ := limitsDaemon(t)
	tr := newSPTranscript(t)
	defer tr.save()

	// More rows than a snapshot carries (500), so rows lie above it.
	spType(t, ctl, w1, "seq 1 900; echo ROWS-$((1+1))DONE\n")
	spWait(t, ctl, w1, "ROWS-2DONE")

	viewer := startPhoneLink(t, base)

	// Did not ask: S first, and no H through a repaint.
	plain := viewer.open(t, true)
	plain.streamPane(t, tr, map[string]any{"session": streamSession, "window": w1, "repaint_on_resize": true})
	typ, _ := plain.frame(t, tr)
	if typ != 'S' {
		t.Fatalf("a stream that did not ask for row ids: the first frame is %q, want S", typ)
	}
	plain.send(t, 'L', []byte{0, 40, 0, 10})
	deadline := time.Now().Add(uiTimeout)
	for repainted := false; !repainted; {
		if time.Now().After(deadline) {
			t.Fatalf("no repaint S frame after the lease")
		}
		typ, payload, err := plain.frameWithin(time.Until(deadline))
		if err != nil {
			t.Fatalf("read a frame after the lease: %v", err)
		}
		tr.frame(typ, payload)
		switch typ {
		case 'H':
			t.Fatalf("a stream that did not ask for row ids got an H frame")
		case 'S':
			repainted = true
		}
	}
	plain.send(t, 'L', []byte{0, 0, 0, 0})

	// Asked: H, then S.
	asked := viewer.open(t, true)
	asked.streamPane(t, tr, map[string]any{"session": streamSession, "window": w1, "row_ids": true})
	typ, payload := asked.frame(t, tr)
	if typ != 'H' || len(payload) != 8 {
		t.Fatalf("a stream that asked for row ids: the first frame is %q (%d bytes), want an 8-byte H", typ, len(payload))
	}
	id := int64(binary.BigEndian.Uint64(payload))
	typ, snap := asked.frame(t, tr)
	if typ != 'S' {
		t.Fatalf("after H the frame is %q, want S", typ)
	}
	first, ok := firstNumberRow(string(snap[12:]))
	if !ok {
		t.Fatalf("the snapshot draws no seq row first:\n%q", spPlain(string(snap[12:]))[:min(400, len(spPlain(string(snap[12:]))))])
	}
	res, verr := ctl.call(t, "pane-history", map[string]any{"session": streamSession, "window": w1, "before": id, "count": 1})
	if verr != nil {
		t.Fatalf("pane-history before %d: %v", id, verr)
	}
	above, ok := firstNumberRow(res["rows"].(string))
	if !ok {
		t.Fatalf("pane-history before %d gave no seq row: %q", id, res["rows"])
	}
	if above != first-1 {
		t.Fatalf("H says the snapshot starts at row id %d; the row before it is %d, and the snapshot's first row is %d, want %d", id, above, first, above+1)
	}
}

// spEscape matches the escape sequences a snapshot or a pane-history reply
// draws with.
var spEscape = regexp.MustCompile(`\x1b\[[0-9;?<>=!]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78DEHMNOZc]`)

func spPlain(s string) string { return spEscape.ReplaceAllString(s, "") }

// firstNumberRow is the first non-blank row of s with the escapes taken
// out, as a number.
func firstNumberRow(s string) (int, bool) {
	for line := range strings.SplitSeq(spPlain(s), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		return n, err == nil
	}
	return 0, false
}
