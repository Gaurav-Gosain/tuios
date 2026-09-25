package session

import "testing"

// TestListingsAgreeNoticesARename guards the cache generation. A rename moves no
// window, so comparing only names and window counts left a renamed session
// showing its old label until something unrelated forced a rebuild.
func TestListingsAgreeNoticesARename(t *testing.T) {
	before := []SessionInfo{{Name: "work"}}
	renamed := []SessionInfo{{Name: "work", DisplayName: "Payments API"}}
	recoloured := []SessionInfo{{Name: "work", Accent: "cyan"}}

	if listingsAgree(before, renamed) {
		t.Error("a display rename was reported as no change")
	}
	if listingsAgree(before, recoloured) {
		t.Error("an accent change was reported as no change")
	}
	if !listingsAgree(before, []SessionInfo{{Name: "work"}}) {
		t.Error("two identical listings were reported as different")
	}
}
