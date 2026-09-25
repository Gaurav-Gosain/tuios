package session

import "testing"

// TestTheMachineIsCarriedByWindow covers the Host field in
// retainDaemonExclusive. A sync that omits the machine keeps each window's own,
// matched by window rather than by position, since every layout change reorders
// the windows and carrying by index would hand one window's machine to another.
// A sync that names a machine is believed, because the daemon itself pushes
// state with the machine set.
func TestTheMachineIsCarriedByWindow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		canonical []WindowState
		incoming  []WindowState
		want      []string
	}{
		{
			"omitted, reordered",
			[]WindowState{{ID: "w1", Host: "build"}, {ID: "w2", Host: "workstation"}},
			[]WindowState{{ID: "w2"}, {ID: "w1"}},
			[]string{"workstation", "build"},
		},
		{
			"named",
			[]WindowState{{ID: "w1", Host: "build"}},
			[]WindowState{{ID: "w1", Host: "workstation"}},
			[]string{"workstation"},
		},
	} {
		incoming := &SessionState{Windows: tc.incoming}
		retainDaemonExclusive(incoming, &SessionState{Windows: tc.canonical})
		for i, want := range tc.want {
			if got := incoming.Windows[i].Host; got != want {
				t.Errorf("%s: window %s has machine %q, want %q", tc.name, incoming.Windows[i].ID, got, want)
			}
		}
	}
}
