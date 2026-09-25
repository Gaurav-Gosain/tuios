package main

import (
	"strings"
	"testing"
)

// TestStatusLineStampName: a session or window name must not climb out of the
// stamp directory.
func TestStatusLineStampName(t *testing.T) {
	if got := statusLineStampName("../work", "a/b c"); got != "statusline-___work-a_b_c.json" || strings.ContainsAny(got, "/\\") {
		t.Errorf("name = %q", got)
	}
}
