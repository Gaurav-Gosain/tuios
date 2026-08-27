package config

import (
	"slices"
	"strings"
	"testing"
)

// names is the layout's entries, in order, for a test that cares what the
// parser made of a string.
func names(source string) []string {
	var out []string
	for _, e := range ParseSidebarSections(source) {
		out = append(out, e.String())
	}
	return out
}

// TestSpacerMayBeListedMoreThanOnce is the one rule the spacer needed that the
// grammar did not already have.
//
// Every other name is dropped the second time it appears, because a rail cannot
// draw one list in two places. A spacer names a place rather than a list, so
// two of them are two gaps and both have to survive the parse. Without this the
// layout could not say "sessions, gap, terminals, gap, files" at all, which is
// the layout that decided the spacer had to be an ordered-list entry rather
// than a boolean per section.
//
// Negative control, confirmed red: put the spacer back inside the seen check in
// ParseSidebarSections. This fails with 4 entries and one spacer.
func TestSpacerMayBeListedMoreThanOnce(t *testing.T) {
	got := names("sessions,spacer,terminals,spacer,files")
	want := []string{"sessions", "spacer", "terminals", "spacer", "files"}
	if !slices.Equal(got, want) {
		t.Errorf("layout = %v, want %v", got, want)
	}

	// And with shares on them, since ":10" twice over is the spelling that
	// would have been ambiguous to a parser keyed by name.
	got = names("spacer:10,sessions,spacer:20")
	want = []string{"spacer:10", "sessions", "spacer:20"}
	if !slices.Equal(got, want) {
		t.Errorf("layout with shares = %v, want %v", got, want)
	}
}

// TestSectionsStillDropARepeatedSection is the other half: the spacer is the
// exception, and it did not become the rule.
//
// Negative control, confirmed red: skip the seen check for every name rather
// than for the spacer alone. This fails with sessions listed twice.
func TestSectionsStillDropARepeatedSection(t *testing.T) {
	if got := names("sessions,terminals,sessions:40"); !slices.Equal(got, []string{"sessions", "terminals"}) {
		t.Errorf("layout = %v, want the second sessions dropped", got)
	}
}

// TestLayoutOfNothingButSpacersFallsBack keeps a rail that draws something.
//
// A layout with no section in it parses fine and lays out to a column of blank
// lines, which is a state nobody meant to ask for and cannot be got out of from
// inside the rail. The fallback used to count entries; it counts sections now,
// because a spacer is an entry that is not a section.
//
// Negative control, confirmed red: count len(out) instead of sections in the
// fallback. This fails with a two-entry layout of spacers.
func TestLayoutOfNothingButSpacersFallsBack(t *testing.T) {
	got := names("spacer,spacer:10")
	want := names(SidebarDefaultSections)
	if !slices.Equal(got, want) {
		t.Errorf("a layout of only spacers parsed as %v, want the shipped %v", got, want)
	}
}

// TestSectionProblemsSaySpacerIsFine is the validator's half of the same rule:
// a repeated spacer is not a problem and a repeated section still is.
//
// Negative control, confirmed red: drop the spacer's exemption in
// SidebarSectionProblems. This fails on the first case with a "listed twice"
// complaint about a layout that works.
func TestSectionProblemsSaySpacerIsFine(t *testing.T) {
	if got := SidebarSectionProblems("sessions,spacer,terminals,spacer"); len(got) != 0 {
		t.Errorf("a layout with two spacers was reported as %v, want no problems", got)
	}
	got := SidebarSectionProblems("sessions,spacer,sessions")
	if len(got) != 1 || !strings.Contains(got[0], "sessions is listed twice") {
		t.Errorf("problems = %v, want one complaint about sessions", got)
	}
	// A name that is neither a section nor the spacer is still a typo, and the
	// list a person is offered names the spacer, or the message would send them
	// back to a set that does not hold what they typed.
	got = SidebarSectionProblems("sessions,spacers")
	if len(got) != 1 || !strings.Contains(got[0], "spacer") {
		t.Errorf("problems = %v, want one complaint naming the spacer", got)
	}
}

// TestSidebarSectionsWithoutKeepsTheRest is the migration's one moving part: a
// section comes out and the order and shares of everything else survive.
//
// Negative control, confirmed red: drop the share from String(). This fails
// with sessions and files back at auto.
func TestSidebarSectionsWithoutKeepsTheRest(t *testing.T) {
	got := SidebarSectionsWithout(SidebarDefaultSections, "terminals")
	if got != "sessions:25,files:25,agents:34" {
		t.Errorf("layout without terminals = %q", got)
	}
	// And a layout that never named the section is left exactly as it was.
	if got := SidebarSectionsWithout("sessions,files", "agents"); got != "sessions,files" {
		t.Errorf("layout = %q, want it untouched", got)
	}
}

// TestOldLayoutStringStillParses is the compatibility claim this branch owes
// anybody whose config already carries a layout: the grammar did not change
// under them, only what it may additionally hold.
func TestOldLayoutStringStillParses(t *testing.T) {
	for _, source := range []string{
		SidebarDefaultSections,
		"terminals,sessions",
		"files:60,sessions:20,terminals,agents:20",
		"agents:50,files:50,sessions,terminals",
	} {
		if got := strings.Join(names(source), ","); got != source {
			t.Errorf("layout %q parsed back as %q", source, got)
		}
	}
}
