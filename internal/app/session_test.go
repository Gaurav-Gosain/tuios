package app

import (
	"testing"
)

func TestExtractAPCSequences(t *testing.T) {
	// No APC
	if got := extractAPCSequences([]byte("hello world")); got != nil {
		t.Errorf("expected nil, got %q", got)
	}

	// Just APC
	apc := []byte("\x1b_Gf=24;data\x1b\\")
	if got := extractAPCSequences(apc); string(got) != string(apc) {
		t.Errorf("expected %q, got %q", apc, got)
	}

	// APC mixed with regular text - should extract only APC
	mixed := []byte("before\x1b_Gf=24;data\x1b\\after")
	expected := []byte("\x1b_Gf=24;data\x1b\\")
	if got := extractAPCSequences(mixed); string(got) != string(expected) {
		t.Errorf("expected %q, got %q", expected, got)
	}

	// Multiple APCs
	multi := []byte("text\x1b_G1\x1b\\middle\x1b_G2\x1b\\end")
	expectedMulti := []byte("\x1b_G1\x1b\\\x1b_G2\x1b\\")
	if got := extractAPCSequences(multi); string(got) != string(expectedMulti) {
		t.Errorf("expected %q, got %q", expectedMulti, got)
	}
}
