package guestenv

import "testing"

func TestTermProgram(t *testing.T) {
	tests := []struct {
		name  string
		kitty bool
		sixel bool
		want  string
	}{
		{name: "no graphics", want: "TUIOS"},
		{name: "kitty", kitty: true, want: "ghostty"},
		{name: "sixel only", sixel: true, want: "WezTerm"},
		{name: "both prefers kitty", kitty: true, sixel: true, want: "ghostty"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TermProgram(tc.kitty, tc.sixel); got != tc.want {
				t.Errorf("TermProgram(%v, %v) = %q, want %q", tc.kitty, tc.sixel, got, tc.want)
			}
		})
	}
}
