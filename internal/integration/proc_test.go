package integration

import "testing"

func TestHarnessPIDSkipsShells(t *testing.T) {
	names := map[int]string{10: "sh", 11: "-zsh", 12: "claude", 13: "env", 14: "node", 20: "bash"}
	name := func(pid int) string { return names[pid] }
	cases := []struct {
		name      string
		ancestors []int
		want      int
	}{
		{"the shell exec'd the hook", []int{12, 20}, 12},
		{"through a shell", []int{10, 12, 20}, 12},
		{"through a shell and env", []int{10, 13, 14, 20}, 14},
		{"a login shell name", []int{11, 12}, 12},
		{"a name that cannot be read", []int{99, 12}, 99},
		{"only shells up to init", []int{10, 20, 1}, 0},
		{"no ancestors", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := harnessPID(tc.ancestors, name); got != tc.want {
				t.Fatalf("harnessPID(%v) = %d, want %d", tc.ancestors, got, tc.want)
			}
		})
	}
}
