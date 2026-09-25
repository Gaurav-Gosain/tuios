package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestALayoutStringIsForgivingAndSaysWhy. The rail parses the layout on every
// frame it rebuilds, so an unusable string has to leave it drawing something;
// the validator is where the person who typed it is told what happened.
//
// Negative control, confirmed red: return the parsed list unchanged when it is
// empty, and the "nonsense" case draws a rail with no sections at all.
func TestALayoutStringIsForgivingAndSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		spec      string
		wantNames []string
		wantWhy   string
	}{
		{"sessions,nosuchsection,files", []string{"sessions", "files"}, "no rail section"},
		{"sessions,sessions,files", []string{"sessions", "files"}, "listed twice"},
		{"sessions:abc,files", []string{"sessions", "files"}, "not a whole number"},
		{"sessions:400,files", []string{"sessions", "files"}, "clamped"},
		{"nonsense", nil, "no rail section"},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			got := config.ParseSidebarSections(tc.spec)
			if tc.wantNames != nil {
				var names []string
				for _, e := range got {
					names = append(names, e.Name)
				}
				if strings.Join(names, ",") != strings.Join(tc.wantNames, ",") {
					t.Errorf("parsed %v, want %v", names, tc.wantNames)
				}
			} else if len(got) == 0 {
				t.Error("an unusable layout left the rail with no sections at all")
			}
			why := strings.Join(config.SidebarSectionProblems(tc.spec), "\n")
			if !strings.Contains(why, tc.wantWhy) {
				t.Errorf("the validator said %q, which does not mention %q", why, tc.wantWhy)
			}
		})
	}

	// A share out of range is clamped rather than dropped, so the section keeps
	// its place in the order.
	got := config.ParseSidebarSections("sessions:400,files")
	if got[0].Share != 100 {
		t.Errorf("a share of 400 percent came through as %d", got[0].Share)
	}
	if got[1].Share != 0 {
		t.Errorf("a section with no share is not flexible: %d", got[1].Share)
	}
}

// TestAStaleListingIsDropped is the generation guard, which is the whole reason
// the read can be asynchronous at all.
//
// A read of a directory on a mount that has stopped answering can come back
// long after the user moved on, and applying it would replace the listing they
// are looking at with one they left. Comparing the path would not be enough:
// walking out of a folder and straight back into it is the same path twice.
//
// Negative control, confirmed red: drop the generation test from HandleFileList
// and the late reply overwrites the listing.
func TestAStaleListingIsDropped(t *testing.T) {
	root := fileViewTree(t)
	m := &OS{Settings: config.Global}
	m.filesView.Show = 1

	// A read is asked for and its answer is held back.
	slow := m.requestFileList(root, "", true)
	if slow == nil {
		t.Fatal("no read was scheduled")
	}
	stale := slow().(fileListMsg)

	// The user walks somewhere else, and that answer lands first.
	m.loadFileViewNow(t, root+"/apple")
	want := m.FileViewDir()

	m.HandleFileList(stale)
	if got := m.FileViewDir(); got != want {
		t.Errorf("a late reply moved the listing to %q; it was showing %q", got, want)
	}
	if len(m.filesView.Entries) != 0 {
		t.Errorf("a late reply put %d names from the old directory on screen", len(m.filesView.Entries))
	}
}
