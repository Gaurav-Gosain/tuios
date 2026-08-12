package session

import "testing"

// TestSessionListingCarriesTheLabel checks the seam that lets a client show a
// label for a session it is not attached to. A state push only reaches the
// attached session, so without the label on the listing every other session in
// a switcher would fall back to its identity name forever.
func TestSessionListingCarriesTheLabel(t *testing.T) {
	sess := newTestSession(t)

	if info := sess.Info(); info.DisplayName != "" || info.Accent != "" {
		t.Fatalf("fresh session listing = {DisplayName:%q Accent:%q}, want both empty", info.DisplayName, info.Accent)
	}

	if err := sess.SetDisplayName("Payments API"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	if err := sess.SetAccent("cyan"); err != nil {
		t.Fatalf("SetAccent: %v", err)
	}

	info := sess.Info()
	if info.DisplayName != "Payments API" || info.Accent != "cyan" {
		t.Errorf("listing = {DisplayName:%q Accent:%q}, want {Payments API cyan}", info.DisplayName, info.Accent)
	}
	if info.Name != sess.Name {
		t.Errorf("listing Name = %q, want the identity %q", info.Name, sess.Name)
	}
}

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
