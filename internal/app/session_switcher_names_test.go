package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// spacedSwitcherItems are the labels a rename can now produce.
var spacedSwitcherItems = []sessiontree.Node{
	{ID: "work", Title: "Payments API"},
	{ID: "docs", Title: "café builds"},
	{ID: "logs", Title: "logs"},
}

// TestSpacedNamesStayFindable is the consequence of allowing a space that would
// bite hardest: a name is the thing the switcher searches, so a two-word name
// has to be findable by the whole thing, by either word, and across the space.
func TestSpacedNamesStayFindable(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"Payments API", "work"},
		{"payments api", "work"},
		{"ts ap", "work"},   // straddling the space
		{"API", "work"},     // the word after it
		{"café", "docs"},    // multi-byte needle
		{"CAFÉ", "docs"},    // and case-insensitively
		{" builds", "docs"}, // a leading space is part of the query
	}
	for _, c := range cases {
		got := FilterSessionItems(spacedSwitcherItems, c.query)
		if len(got) != 1 || got[0].ID != c.want {
			t.Errorf("query %q matched %v, want exactly %s", c.query, ids(got), c.want)
		}
	}

	// A query that spans two different sessions' names still matches neither.
	if got := FilterSessionItems(spacedSwitcherItems, "API café"); len(got) != 0 {
		t.Errorf("a query across two names matched %v", ids(got))
	}
}

func ids(nodes []sessiontree.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}
