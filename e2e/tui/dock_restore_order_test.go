package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// dockOrder is the order the names appear in on the dock's row, the last
// line of the screen.
func dockOrder(s tuitest.Screen, names ...string) []string {
	_, rows := s.Size()
	line := s.Line(rows - 1)
	type at struct {
		name string
		i    int
	}
	var found []at
	for _, n := range names {
		if i := strings.Index(line, n); i >= 0 {
			found = append(found, at{n, i})
		}
	}
	for i := range found {
		for j := i + 1; j < len(found); j++ {
			if found[j].i < found[i].i {
				found[i], found[j] = found[j], found[i]
			}
		}
	}
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.name)
	}
	return out
}

// TestDockPillsAndRestoreNAgree minimizes two of three panes by keys in the
// opposite order to the window order, then reads the dock on the client that
// minimized them and on a second client, and restores the second pill with
// prefix m 2. Both docks list the pills in one order, and restore_2 restores
// the pane on the second pill.
//
// How it could pass wrongly, written down first:
//   - The panes could be minimized in window order, where every order agrees.
//     They are minimized last pane first.
//   - The second client could read the order before the panes are minimized.
//     It waits for both names on its dock.
//   - The restored pane is read from the daemon, not from the screen.
//
// Negative control: on main the first client's dock lists the panes in the
// order they were minimized (gamma, alpha) while restore_2 counts in window
// order, and the order check fails.
func TestDockPillsAndRestoreNAgree(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "dk", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "dk", "-w", "0", "--name", "alpha"); err != nil {
		t.Fatalf("name the first pane: %v\n%s", err, out)
	}
	for _, n := range []string{"beta", "gamma"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "dk", n, "--no-focus"); err != nil {
			t.Fatalf("open %s: %v\n%s", n, err, out)
		}
	}
	a := startIn(t, base, startOpts{args: []string{"attach", "dk"}, cols: 140, rows: 30})
	if err := a.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 3 }, bootTimeout); err != nil {
		t.Fatalf("the first client never attached: %v\n%s", err, a.Snapshot())
	}
	windowManagementMode(t, a)

	// Last pane first: gamma, then alpha.
	for _, n := range []string{"gamma", "alpha"} {
		if out, err := tuiosCLI(t, base, "focus-window", "-s", "dk", n); err != nil {
			t.Fatalf("focus %s: %v\n%s", n, err, out)
		}
		time.Sleep(insertGuard)
		sendKeys(t, a, tuitest.Ctrl('b'), "m", "m")
		if err := a.WaitFor(func(s tuitest.Screen) bool { return len(dockOrder(s, n)) == 1 }, uiTimeout); err != nil {
			t.Fatalf("%s never reached the dock: %v\n%s", n, err, a.Snapshot())
		}
	}

	b := startIn(t, base, startOpts{args: []string{"attach", "dk"}, cols: 140, rows: 30})
	if err := b.WaitFor(func(s tuitest.Screen) bool { return len(dockOrder(s, "alpha", "gamma")) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the second client never showed both pills: %v\n%s", err, b.Snapshot())
	}
	time.Sleep(insertGuard)
	first, second := dockOrder(a.Screen(), "alpha", "gamma"), dockOrder(b.Screen(), "alpha", "gamma")
	railShot(t, a, "dock-first-client")
	railShot(t, b, "dock-second-client")
	t.Logf("first client's pills %v, second client's %v", first, second)
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("ASSERTION: the two clients list the pills in different orders: %v and %v", first, second)
	}
	if strings.Join(first, ",") != "alpha,gamma" {
		t.Fatalf("ASSERTION: the pills are not in the order restore_N counts in (window order, alpha then gamma): %v", first)
	}

	// prefix m 2 restores the second pill.
	sendKeys(t, a, tuitest.Ctrl('b'), "m", "2")
	want := first[1]
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "list-windows", "-s", "dk", "--json")
		if err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		var lw struct {
			Windows []struct {
				Name      string `json:"display_name"`
				Minimized bool   `json:"minimized"`
			} `json:"windows"`
		}
		_ = json.Unmarshal([]byte(out[strings.IndexByte(out, '{'):]), &lw)
		restored := ""
		for _, w := range lw.Windows {
			if (w.Name == "alpha" || w.Name == "gamma") && !w.Minimized {
				restored = w.Name
			}
		}
		if restored == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: prefix m 2 restored %q, not the second pill %q\n%s", restored, want, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
