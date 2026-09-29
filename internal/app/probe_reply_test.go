package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A reply that arrives after the one the probe waits for can be cut by a
// read. The probe must read it to its end, or its tail reaches the program's
// input as keys. A nested tuios typed "ost terminal does not support
// animation" into the scratch shell that way.
func TestProbeReadsALateReplyToItsEnd(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()

	head := "\x1b[?62;4c\x1b_Gi=4;ENOTSUPPORTED:h"
	tail := "ost terminal does not support animation\x1b\\"
	if _, err := w.WriteString(head); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, _ = w.WriteString(tail)
	}()

	got := readTTYResponse(r, time.Second, da1Response.MatchString)
	if got != head+tail {
		t.Fatalf("the probe read %q, want the whole late reply %q", got, head+tail)
	}
}

func TestEndsInsideSequence(t *testing.T) {
	cases := map[string]bool{
		"":                          false,
		"plain":                     false,
		"\x1b[?62;c":                false,
		"\x1b[?62;":                 true,
		"\x1b":                      true,
		"\x1b_Gi=1;OK\x1b\\":        false,
		"\x1b_Gi=1;OK":              true,
		"\x1b_Gi=1;OK\x1b":          true,
		"\x1b]11;rgb:0/0/0\x07":     false,
		"\x1b]11;rgb:0/0/0\x1b\\":   false,
		"\x1b]11;rgb:0":             true,
		"\x1bP>|kitty(0.40)\x1b\\x": false,
		"\x1bP>|kitty":              true,
	}
	for in, want := range cases {
		if got := endsInsideSequence(in); got != want {
			t.Errorf("endsInsideSequence(%q) = %v, want %v", strings.ReplaceAll(in, "\x1b", "ESC"), got, want)
		}
	}
}

// A frame edit in a daemon pane is refused by a daemon that says it refuses
// them, and by the client otherwise, so a web view beside an older daemon
// still gets its refusal. Everything else is answered as before.
func TestClientAnswersKittyCommand(t *testing.T) {
	cases := []struct {
		daemon, refuses bool
		action          vt.KittyGraphicsAction
		want            bool
	}{
		{true, true, vt.KittyActionFrame, false},
		{true, false, vt.KittyActionFrame, true},
		{false, true, vt.KittyActionFrame, true},
		{true, true, vt.KittyActionTransmit, true},
		{true, true, vt.KittyActionCompose, false},
	}
	for _, c := range cases {
		if got := clientAnswersKittyCommand(c.daemon, c.action, c.refuses); got != c.want {
			t.Errorf("daemon=%v refuses=%v action=%c: answers = %v, want %v", c.daemon, c.refuses, c.action, got, c.want)
		}
	}
	if (*session.TUIClient)(nil).DaemonRefusesKittyAnimation() {
		t.Error("no daemon client counts as a daemon that refuses")
	}
}
