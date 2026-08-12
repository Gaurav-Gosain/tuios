package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// bareShellOS is the case the rail was worst at: n panes in one repo, none
// named, none running anything, so every one of them reports the same title.
func bareShellOS(t *testing.T, n int) *OS {
	t.Helper()
	m := newNarrowOS(t, 120, 40)
	m.CurrentWorkspace = 1
	m.SessionName = ""
	m.Windows = nil
	for i := range n {
		w := &terminal.Window{ID: "w" + strconv.Itoa(i), Width: 40, Height: 20, Workspace: 1}
		w.SetTitle("~/dev/tuios - fish")
		m.Windows = append(m.Windows, w)
	}
	m.FocusedWindow = 0
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.SidebarOrder = nil
	return m
}

// paneRows returns the rendered rows that carry a pane label, which is every
// row indented past its session and holding want.
func paneRows(rows []string, want string) []string {
	var out []string
	for _, r := range rows {
		if strings.Contains(r, want) {
			out = append(out, strings.TrimRight(r, " "))
		}
	}
	return out
}

// TestRailRowsDistinguishBareShells is the acceptance criterion, on a rendered
// frame: five shells in one directory used to draw five rows reading
// "~/dev/tuios - fish", which cannot answer which pane is which.
func TestRailRowsDistinguishBareShells(t *testing.T) {
	m := bareShellOS(t, 5)
	rows := paneRows(railText(t, m), "tuios")

	if len(rows) != 5 {
		t.Fatalf("expected 5 pane rows, got %d:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		if seen[strings.TrimSpace(r)] {
			t.Fatalf("two pane rows read the same:\n%s", strings.Join(rows, "\n"))
		}
		seen[strings.TrimSpace(r)] = true
	}
	// The noise is gone with the ambiguity: the shell's name says nothing that
	// the pane beside it does not.
	for _, r := range rows {
		if strings.Contains(r, "fish") {
			t.Errorf("row still carries the shell name: %q", r)
		}
	}
}

// TestRailRowShowsForegroundCommand: what a pane is running is the answer to
// "which pane is this", so it outranks a title every sibling shares.
func TestRailRowShowsForegroundCommand(t *testing.T) {
	m := bareShellOS(t, 3)
	m.Windows[1].ForegroundCmd = "nvim"
	m.Windows[2].ForegroundCmd = "btop"

	rows := railText(t, m)
	for _, want := range []string{"nvim", "btop"} {
		if len(paneRows(rows, want)) != 1 {
			t.Errorf("no row reads %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	// The one remaining shell is alone now, so it carries no ordinal.
	got := paneRows(rows, "tuios")
	if len(got) != 1 {
		t.Fatalf("expected one shell row, got %d: %q", len(got), got)
	}
	if strings.Contains(got[0], "tuios 1") {
		t.Errorf("a shell with no twin was still numbered: %q", got[0])
	}
}

// TestRailRowKeepsCustomNameOverCommand: a rename is the user's answer and
// nothing detected may overrule it.
func TestRailRowKeepsCustomNameOverCommand(t *testing.T) {
	m := bareShellOS(t, 2)
	m.Windows[0].CustomName = "editor"
	m.Windows[0].ForegroundCmd = "nvim"

	rows := railText(t, m)
	if len(paneRows(rows, "editor")) != 1 {
		t.Errorf("the named row is missing:\n%s", strings.Join(rows, "\n"))
	}
	if got := paneRows(rows, "nvim"); len(got) != 0 {
		t.Errorf("the command overrode the custom name: %q", got)
	}
}

// TestRailWindowLabelPrecedence pins the order the row label is chosen in, on
// the pieces every surface has.
func TestRailWindowLabelPrecedence(t *testing.T) {
	cases := []struct {
		name, custom, cmd, title, want string
	}{
		{"custom name wins", "logs", "nvim", "~/dev/tuios - fish", "logs"},
		{"command beats the title", "", "nvim", "~/dev/tuios - fish", "nvim"},
		{"bare shell keeps the directory", "", "", "~/dev/tuios - fish", "tuios"},
		{"bash-style title", "", "", "gaurav@box:~/dev/tuios", "tuios"},
		{"plain title is left alone", "", "", "make", "make"},
		{"home directory", "", "", "~ - fish", "~"},
		{"nothing at all", "", "", "", "shell"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := railWindowLabel(c.custom, c.cmd, c.title); got != c.want {
				t.Errorf("railWindowLabel(%q, %q, %q) = %q, want %q", c.custom, c.cmd, c.title, got, c.want)
			}
		})
	}
}

// TestRailLabelsForeignSessionRows: a pane of a session this client is not
// attached to is labelled from the listing by the same rules, so peeking
// another session does not drop back to five identical rows.
func TestRailLabelsForeignSessionRows(t *testing.T) {
	m := bareShellOS(t, 1)
	m.SessionName = "attached"
	m.DaemonClient = railTitleClient()
	m.DaemonClient.UpdateSessionCache([]session.SessionInfo{
		{Name: "attached", Windows: []session.WindowSummary{{ID: "w0", Title: "~/dev/tuios - fish"}}},
		{Name: "other", Windows: []session.WindowSummary{
			{ID: "o1", Title: "~/dev/tuios - fish"},
			{ID: "o2", Title: "~/dev/tuios - fish"},
			{ID: "o3", Title: "~/dev/tuios - fish", ForegroundCmd: "nvim"},
		}},
	})
	// The terminals section shows one session's panes at a time: the attached
	// one, or a peeked one. Peeking "other" is what puts its panes on screen.
	m.SidebarPeek = "other"

	rows := railText(t, m)
	if len(paneRows(rows, "nvim")) != 1 {
		t.Errorf("the foreign pane's command is missing:\n%s", strings.Join(rows, "\n"))
	}
	shells := paneRows(rows, "tuios")
	seen := make(map[string]bool, len(shells))
	for _, r := range shells {
		if seen[strings.TrimSpace(r)] {
			t.Fatalf("two foreign shell rows read the same:\n%s", strings.Join(shells, "\n"))
		}
		seen[strings.TrimSpace(r)] = true
	}
}

// TestRailLabelRidesTheCache: the label is a render input, so a pane picking up
// or dropping a command has to rebuild the rail rather than show a stale row.
func TestRailLabelRidesTheCache(t *testing.T) {
	m := bareShellOS(t, 2)
	sidebarText(t, m)
	sig := m.sidebarCache.sig

	m.Windows[0].ForegroundCmd = "nvim"
	m.updateRailTitles()
	sidebarText(t, m)
	if m.sidebarCache.sig == sig {
		t.Fatal("a pane starting a command did not rebuild the rail")
	}
	if len(paneRows(railText(t, m), "nvim")) != 1 {
		t.Error("the command never reached the drawn row")
	}
}
