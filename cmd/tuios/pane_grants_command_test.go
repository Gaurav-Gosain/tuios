package main

import (
	"strings"
	"testing"
)

func TestDescribePaneGrants(t *testing.T) {
	tests := []struct {
		name     string
		pane     bool
		grants   []string
		explicit bool
		want     []string
	}{
		{"outside every pane", false, nil, false, []string{"no pane of tuios", "Mode strict", "read, write, fan"}},
		{"a pane on the default", true, []string{"read", "write"}, false, []string{"Pane a1b2c3d4 in session work holds read, write", "the default of [agents.permissions], mode strict"}},
		{"a pane given its own", true, []string{"read"}, true, []string{"holds read (given to this pane)"}},
		{"a pane given none", true, []string{}, true, []string{"holds no grants"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describePaneGrants(tt.pane, "a1b2c3d4-0000", "work", tt.grants, tt.explicit, "strict", []string{"read", "write", "fan"})
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("%q does not say %q", got, w)
				}
			}
		})
	}
}
