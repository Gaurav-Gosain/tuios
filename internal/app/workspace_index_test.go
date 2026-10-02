package app

import "testing"

func TestWithWorkspaceIndex(t *testing.T) {
	cases := []struct {
		name string
		ws   int
		want string
	}{
		{"work", 2, "work [2]"},
		{"work [2]", 2, "work [2]"},
		{"", 3, ""},
		{"3", 3, "3"},
	}
	for _, c := range cases {
		if got := withWorkspaceIndex(c.name, c.ws); got != c.want {
			t.Errorf("withWorkspaceIndex(%q, %d) = %q, want %q", c.name, c.ws, got, c.want)
		}
	}
}
