package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPaintLinkRoundTripsThroughState paints a chip over the wire and reads it
// back out of the session's state, which is the road a snapshot and a restore
// both take.
func TestPaintLinkRoundTripsThroughState(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "chip")
	other, err := sess.AddDaemonWindow("Logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"paint-link","params":{"session":"chip","window":"Window","target":"`+other.ID+`","label":"open logs"}}`))
	if res["window_id"] != sess.GetState().Windows[0].ID {
		t.Errorf("window_id = %v, want the pane the chip was painted on", res["window_id"])
	}

	w := sess.GetState().Windows[0]
	if w.LinkChip == nil {
		t.Fatal("painted chip is missing from the session state")
	}
	if w.LinkChip.Label != "open logs" || w.LinkChip.Target != other.ID {
		t.Errorf("chip = %+v, want label open logs targeting %s", *w.LinkChip, other.ID)
	}

	// A chip has to survive the JSON leg too: it is what a snapshot writes to
	// disk and what an older client reads off the wire.
	blob, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var back WindowState
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	if back.LinkChip == nil || back.LinkChip.Target != other.ID {
		t.Errorf("chip did not survive the JSON round trip: %s", blob)
	}

	// Painting again replaces the chip.
	result(t, c.call(t, `{"id":1,"verb":"paint-link","params":{"session":"chip","window":"Window","target":"`+other.ID+`","label":"jump"}}`))
	if got := sess.GetState().Windows[0].LinkChip; got == nil || got.Label != "jump" {
		t.Errorf("repaint left chip %+v, want label jump", got)
	}

	// An empty label clears it.
	result(t, c.call(t, `{"id":1,"verb":"paint-link","params":{"session":"chip","window":"Window","label":""}}`))
	if got := sess.GetState().Windows[0].LinkChip; got != nil {
		t.Errorf("empty label left chip %+v, want none", got)
	}
}

// TestPaintLinkRefusesAMissingTarget: a chip whose jump lands nowhere is a
// typo made chrome, so the verb refuses it.
func TestPaintLinkRefusesAMissingTarget(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "chip")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"paint-link","params":{"session":"chip","target":"nope","label":"gone"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("missing target answered %s, want %s", code, ErrVerbInvalidParams)
	}
}

// TestPaintLinkLabelCapped: a label the pane sends is cut, never stored whole.
func TestPaintLinkLabelCapped(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "chip")
	target := sess.GetState().Windows[0].ID
	c := dialVerb(t, sp)

	long := strings.Repeat("x", 100)
	result(t, c.call(t, `{"id":1,"verb":"paint-link","params":{"session":"chip","target":"`+target+`","label":"`+long+`"}}`))
	got := sess.GetState().Windows[0].LinkChip
	if got == nil || len([]rune(got.Label)) != linkChipLabelMax {
		t.Errorf("label = %v, want %d runes", got, linkChipLabelMax)
	}
}
