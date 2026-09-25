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

// TestParseSidebarSections holds the layout grammar to what it has to keep and
// what it has to drop.
//
//   - A spacer may be listed more than once. Every other name is dropped the
//     second time it appears, because a rail cannot draw one list in two
//     places. A spacer names a place rather than a list, so two of them are two
//     gaps and both have to survive, shares included, since ":10" twice over is
//     the spelling a parser keyed by name would get wrong. Negative control:
//     put the spacer back inside the seen check in ParseSidebarSections.
//   - A repeated section is still dropped: the spacer is the exception, and it
//     did not become the rule. Negative control: skip the seen check for every
//     name.
//   - A layout of nothing but spacers falls back to the shipped one, since it
//     would lay out to a column of blank lines nobody can get out of from
//     inside the rail. Negative control: count len(out) instead of sections in
//     the fallback.
//   - A layout a config already carries parses back unchanged: the grammar did
//     not change under it, only what it may additionally hold.
func TestParseSidebarSections(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   []string
	}{
		{"sessions,spacer,terminals,spacer,files", []string{"sessions", "spacer", "terminals", "spacer", "files"}},
		{"spacer:10,sessions,spacer:20", []string{"spacer:10", "sessions", "spacer:20"}},
		{"sessions,terminals,sessions:40", []string{"sessions", "terminals"}},
		{"spacer,spacer:10", names(SidebarDefaultSections)},
		{SidebarDefaultSections, strings.Split(SidebarDefaultSections, ",")},
		{"terminals,sessions", []string{"terminals", "sessions"}},
		{"files:60,sessions:20,terminals,agents:20", []string{"files:60", "sessions:20", "terminals", "agents:20"}},
		{"agents:50,files:50,sessions,terminals", []string{"agents:50", "files:50", "sessions", "terminals"}},
	} {
		if got := names(tc.source); !slices.Equal(got, tc.want) {
			t.Errorf("layout %q parsed as %v, want %v", tc.source, got, tc.want)
		}
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
