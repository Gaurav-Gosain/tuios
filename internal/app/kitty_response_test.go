package app

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestIsKittyResponse verifies the tightened echoed-response heuristic. It runs
// against the RAW wire payload (base64 text), not decoded bytes, so a real
// image chunk that decodes cleanly is never mistaken for a status response.
func TestIsKittyResponse(t *testing.T) {
	responses := []string{
		"OK",
		"ENOENT",
		"EINVAL",
		"EBADMSG",
		"ENOENT:No such file or directory",
		"EINVAL:bad graphics params",
	}
	for _, r := range responses {
		if !isKittyResponse(r) {
			t.Errorf("isKittyResponse(%q) = false, want true (real echoed response)", r)
		}
	}

	notResponses := []string{
		"",
		"E",                        // 'E' with no following uppercase letter
		"Elephant",                 // lowercase after first uppercase -> real data
		"OKgood",                   // not exactly "OK"
		"ENOENTdata",               // trailing lowercase, no ':' separator
		"iVBORw0KGgoAAAANSUhEUgAA", // typical base64 PNG header, mixed case
		strings.Repeat("E", 300),   // over the length cap
		"EABCDEF" + "abc",          // uppercase run then lowercase, no ':'
	}
	for _, d := range notResponses {
		if isKittyResponse(d) {
			t.Errorf("isKittyResponse(%q) = true, want false (real image data)", d)
		}
	}
}

// TestIsKittyResponseDoesNotDropBase64Chunks feeds a large number of realistic
// base64 image chunks (as produced by base64-encoding random-looking binary)
// and asserts none are misclassified as a response. The old decoded-byte
// heuristic dropped ~0.04% of raw binary chunks; the wire-text heuristic must
// drop none.
func TestIsKittyResponseDoesNotDropBase64Chunks(t *testing.T) {
	// Deterministic pseudo-random bytes so the test is reproducible.
	var seed uint32 = 0x12345678
	next := func() byte {
		seed = seed*1664525 + 1013904223
		return byte(seed >> 16)
	}
	for range 5000 {
		raw := make([]byte, 4096)
		for i := range raw {
			raw[i] = next()
		}
		payload := base64.StdEncoding.EncodeToString(raw)
		if isKittyResponse(payload) {
			t.Fatalf("isKittyResponse misclassified a base64 image chunk as a response: %q...", payload[:16])
		}
	}
}
