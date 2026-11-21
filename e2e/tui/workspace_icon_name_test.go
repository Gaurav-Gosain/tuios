package tuie2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

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

func waitIconRow(t *testing.T, term *tuitest.Terminal, base, id, what string, ok func(iconRow) bool) {
	t.Helper()
	waitUntil(t, what, shellTimeout, func() (bool, string) {
		rows := iconRows(t, base)
		for _, r := range rows {
			if r.ID == id && ok(r) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("listing %+v\n%s", rows, term.Snapshot())
	})
}

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

	sendIconPane(t, base, far, `printf '\033]1;farlabel\007'`)
	waitIconRow(t, term, base, far, "list-windows carries the OSC 1 name", func(r iconRow) bool { return r.IconName == "farlabel" })
	waitIconDock(t, term, "the dock labels workspace 2 with the OSC 1 name", func(s tuitest.Screen) bool { return dockCellOK(s, "farlabel") })
	saveFrame(t, term, "workspace-icon-name-labelled")
	if ws := currentWorkspace(t, base, wsIconSession); ws != 1 {
		t.Fatalf("the session shows workspace %d, want 1: the name must come from a pane not in view", ws)
	}

	sendIconPane(t, base, far, `printf '\033]0;fartitle\007'`)
	waitIconRow(t, term, base, far, "OSC 0 sets the title and keeps the OSC 1 name", func(r iconRow) bool {
		return r.Title == "fartitle" && r.IconName == "farlabel"
	})

	sendIconPane(t, base, home, `printf '\033]0;hometitle\007'`)
	waitIconRow(t, term, base, home, "OSC 0 sets the home pane's title", func(r iconRow) bool { return r.Title == "hometitle" })
	stayTrue(t, "the dock shows no OSC 0 title as a workspace label and keeps the OSC 1 one", insertGuard, func() (bool, string) {
		s := term.Screen()
		_, rows := s.Size()
		pills := s.Line(rows - 1)
		ok := !strings.Contains(pills, "hometitle") && !strings.Contains(pills, "fartitle") && strings.Contains(pills, "farlabel")
		return ok, term.Snapshot()
	})
	saveFrame(t, term, "workspace-icon-name-after-osc0")
	for _, r := range iconRows(t, base) {
		if r.ID == home && r.IconName != "" {
			t.Fatalf("OSC 0 gave the home pane icon_name %q, want none", r.IconName)
		}
	}

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

	sendIconPane(t, base, far, `printf '\033]1;\007'`)
	waitIconRow(t, term, base, far, "an empty OSC 1 clears icon_name", func(r iconRow) bool { return r.IconName == "" })
	waitIconDock(t, term, "an empty OSC 1 takes the label off the dock", func(s tuitest.Screen) bool { return !dockCellOK(s, "farlabel") })
	saveFrame(t, term, "workspace-icon-name-cleared")
}
