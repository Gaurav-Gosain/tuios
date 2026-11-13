package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// An unnamed workspace takes its active pane's icon name, the name the pane's
// program gave with OSC 1, and only that: OSC 0 sets the icon name too, but
// shells and agents send it with every prompt.
//
// How this could pass wrongly, written down first:
//   - The label could reach only a client that streams the pane. The pane
//     that names itself sits on workspace 2 while the client shows 1, so the
//     client holds no emulator fed by it and has to take the name from the
//     daemon's state push.
//   - The dock could show the name from a pane's title rather than its icon
//     name. Every name below is a word nothing else prints, and the title
//     OSC 0 sets is checked to reach list-windows, so the dock had it to show
//     and did not.
//   - OSC 0 after OSC 1 could clear the label, which a shell prompt does in
//     every real pane. Workspace 2's pane sends OSC 0 after its OSC 1, and
//     the label has to stay.
//   - A stored name could lose to the icon name, or clearing it could leave
//     the workspace on its number. Both directions are checked.
//   - An empty OSC 1 could leave the old name behind, in the daemon or on
//     the dock.

const wsIconSession = "wi"

type iconRow struct {
	ID        string `json:"window_id"`
	Workspace int    `json:"workspace"`
	Title     string `json:"title"`
	IconName  string `json:"icon_name"`
}

func iconRows(t *testing.T, base string) []iconRow {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", wsIconSession)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Windows []iconRow `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-windows json: %v\n%s", err, out)
	}
	return res.Windows
}

// waitIconRow waits for the pane id to satisfy ok in the daemon's listing.
func waitIconRow(t *testing.T, term *tuitest.Terminal, base, id, what string, ok func(iconRow) bool) {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		for _, r := range iconRows(t, base) {
			if r.ID == id && ok(r) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: listing %+v\n%s", what, iconRows(t, base), term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sendIconPane types line into pane id and runs it.
func sendIconPane(t *testing.T, base, id, line string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-text", "-s", wsIconSession, "-w", id, line+"\n"); err != nil {
		t.Fatalf("send-text %q: %v\n%s", line, err, out)
	}
}

func waitIconDock(t *testing.T, term *tuitest.Terminal, what string, ok func(tuitest.Screen) bool) {
	t.Helper()
	if err := term.WaitFor(ok, uiTimeout); err != nil {
		t.Fatalf("%s: %v\n%s", what, err, term.Snapshot())
	}
}

func TestWorkspaceLabelFollowsIconName(t *testing.T) {
	base := t.TempDir()
	term := startHome(t, base, wsIconSession)
	if out, err := tuiosCLI(t, base, "new-window", "--session", wsIconSession, "--workspace", "2", "--no-focus"); err != nil {
		t.Fatalf("new-window on 2: %v\n%s", err, out)
	}
	waitPanesOn(t, term, base, wsIconSession, 2, 1, "the pane on workspace 2")
	var home, far string
	for _, r := range iconRows(t, base) {
		switch r.Workspace {
		case 1:
			home = r.ID
		case 2:
			far = r.ID
		}
	}
	if home == "" || far == "" {
		t.Fatalf("panes home=%q far=%q, want one on each workspace: %+v", home, far, iconRows(t, base))
	}

	// OSC 1 from a pane the client does not stream.
	sendIconPane(t, base, far, `printf '\033]1;farlabel\007'`)
	waitIconRow(t, term, base, far, "list-windows carries the OSC 1 name", func(r iconRow) bool { return r.IconName == "farlabel" })
	waitIconDock(t, term, "the dock labels workspace 2 with the OSC 1 name", func(s tuitest.Screen) bool { return dockCellOK(s, "farlabel") })
	saveFrame(t, term, "workspace-icon-name-labelled")
	if ws := currentWorkspace(t, base, wsIconSession); ws != 1 {
		t.Fatalf("the session shows workspace %d, want 1: the name must come from a pane not in view", ws)
	}

	// OSC 0 on the same pane changes its title and leaves the label alone.
	sendIconPane(t, base, far, `printf '\033]0;fartitle\007'`)
	waitIconRow(t, term, base, far, "OSC 0 sets the title and keeps the OSC 1 name", func(r iconRow) bool {
		return r.Title == "fartitle" && r.IconName == "farlabel"
	})

	// OSC 0 alone on the workspace in view labels nothing.
	sendIconPane(t, base, home, `printf '\033]0;hometitle\007'`)
	waitIconRow(t, term, base, home, "OSC 0 sets the home pane's title", func(r iconRow) bool { return r.Title == "hometitle" })
	time.Sleep(insertGuard)
	s := term.Screen()
	// The pane's frame draws its title on the row above the dock, so only
	// the pill row is read.
	_, rows := s.Size()
	pills := s.Line(rows - 1)
	if strings.Contains(pills, "hometitle") || strings.Contains(pills, "fartitle") || !strings.Contains(pills, "farlabel") {
		t.Fatalf("the dock shows an OSC 0 title as a workspace label, or lost the OSC 1 one\n%s", term.Snapshot())
	}
	saveFrame(t, term, "workspace-icon-name-after-osc0")
	for _, r := range iconRows(t, base) {
		if r.ID == home && r.IconName != "" {
			t.Fatalf("OSC 0 gave the home pane icon_name %q, want none", r.IconName)
		}
	}

	// A stored name wins, and clearing it gives the label back.
	if out, err := tuiosCLI(t, base, "set-workspace-name", "-s", wsIconSession, "2", "storedname"); err != nil {
		t.Fatalf("name workspace 2: %v\n%s", err, out)
	}
	waitIconDock(t, term, "a stored name wins over the OSC 1 name", func(s tuitest.Screen) bool {
		return dockCellOK(s, "storedname") && !dockCellOK(s, "farlabel")
	})
	if out, err := tuiosCLI(t, base, "set-workspace-name", "-s", wsIconSession, "2"); err != nil {
		t.Fatalf("clear workspace 2's name: %v\n%s", err, out)
	}
	waitIconDock(t, term, "clearing the stored name gives the OSC 1 name back", func(s tuitest.Screen) bool {
		return dockCellOK(s, "farlabel") && !dockCellOK(s, "storedname")
	})

	// An empty OSC 1 clears it, in the daemon and on the dock.
	sendIconPane(t, base, far, `printf '\033]1;\007'`)
	waitIconRow(t, term, base, far, "an empty OSC 1 clears icon_name", func(r iconRow) bool { return r.IconName == "" })
	waitIconDock(t, term, "an empty OSC 1 takes the label off the dock", func(s tuitest.Screen) bool { return !dockCellOK(s, "farlabel") })
	saveFrame(t, term, "workspace-icon-name-cleared")
}
