package diffview

import (
	"strings"
	"testing"
)

func TestHighlightLeavesUnknownAndHugeTextPlain(t *testing.T) {
	if Highlight("notes.unknownext", []string{"func x"}) != nil {
		t.Error("a file of no known type was highlighted")
	}
	if Highlight("big.go", []string{strings.Repeat("x", maxHighlightLine+1)}) != nil {
		t.Error("a line past the limit was tokenised")
	}
	if Enabled && Highlight("run", []string{"#!/bin/sh", "echo hi"}) == nil {
		t.Error("a script with a #! line was not highlighted")
	}
}
