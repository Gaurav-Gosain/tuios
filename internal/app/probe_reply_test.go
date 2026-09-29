package app

import (
	"os"
	"strings"
	"testing"
	"time"
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
