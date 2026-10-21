package tuie2e

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuitest"
)

// The dock's workspace pill under appearance.dock_workspace_label_max and
// appearance.dock_workspace_tab_format, driven through the real binary with a
// workspace whose name is far longer than any pill.
//
// One test covers the four shapes the settings compose into: the default cap,
// the cap turned off on a wide dock, the cap turned off on a dock too narrow
// for even one pill, and a tab format that wraps the name. dock_compact rides
// along, because the compact row must degrade the same way.
func TestDockWorkspaceLabelCapAndFormats(t *testing.T) {
	const long = "alphabetagammadeltaepsilon"

	// renameWorkspace renames the current workspace through the prefix chord,
	// waiting for the rename dialog at each step so no keystroke lands on the
	// wrong mode.
	renameWorkspace := func(t *testing.T, term *tuitest.Terminal, name string) {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('b'), "w"); err != nil {
			t.Fatalf("open the workspace prefix: %v", err)
		}
		if err := term.SendKeys("r"); err != nil {
			t.Fatalf("open the workspace rename: %v", err)
		}
		if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
			t.Fatalf("the workspace rename dialog never opened: %v\n%s", err, term.Snapshot())
		}
		if err := term.SendKeys(name, tuitest.Enter); err != nil {
			t.Fatalf("type the name %q: %v", name, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), "alpha") && !renameDialogUp(s)
		}, uiTimeout); err != nil {
			t.Fatalf("the workspace never took the name %q: %v\n%s", name, err, term.Snapshot())
		}
	}

	// minimizeLongNames brings the session to two windows named after long
	// branches and minimizes both, so the dock row carries entries that compete
	// with the workspace pills for the bar. Minimizing the focused window
	// drops focus on the one left, so the same two keys run down the list.
	minimizeLongNames := func(t *testing.T, term *tuitest.Terminal, names ...string) {
		t.Helper()
		for i, name := range names {
			if i > 0 {
				newWindow(t, term)
			}
			renameWindow(t, term, name)
			if err := term.SendKeys("m"); err != nil {
				t.Fatalf("minimize %s: %v", name, err)
			}
			if err := term.WaitForText(name[:6], uiTimeout); err != nil {
				t.Fatalf("the minimized window %s never appeared in the dock: %v\n%s", name, err, term.Snapshot())
			}
		}
	}

	for _, tc := range []struct {
		name   string
		config string
		cols   int
		// entries skips the long-named minimized windows. The narrow case
		// asserts on the pill alone, and at 70 columns the entry names would
		// not show on the row for the commit wait to see.
		noEntries bool
		// liveMessage opens one more pane after the minimize loop, so the
		// "Window created" toast is up while the row is read.
		liveMessage bool
		// expect runs against the dock row after the client is up.
		expect func(t *testing.T, row string)
	}{
		{"default cap", "", 120, false, false, func(t *testing.T, row string) {
			if strings.Contains(row, long) {
				t.Fatalf("the dock row draws the whole name with the default cap\n%s", row)
			}
			if !strings.Contains(row, "alphabetaga") {
				t.Fatalf("the dock row draws no capped pill\n%s", row)
			}
		}},
		{"uncapped wide", "dock_workspace_label_max = 0\n", 120, false, false, func(t *testing.T, row string) {
			if !strings.Contains(row, long) {
				t.Fatalf("the dock row does not draw the whole name with the cap off\n%s", row)
			}
		}},
		{"uncapped narrow", "dock_workspace_label_max = 0\n", 70, true, false, func(t *testing.T, row string) {
			// A strip with no pill leaves the workspaces nothing to see or
			// click, so the current pill is drawn even when the name barely
			// fits: cut to the room there is, or whole when the room holds it.
			// The name's prefix is the only place "alpha" can come from.
			if !strings.Contains(row, "alpha") {
				t.Fatalf("the narrow dock draws no pill at all for the long workspace\n%s", row)
			}
		}},
		{"custom format", "dock_workspace_tab_format = \"<{name}>\"\n", 120, false, false, func(t *testing.T, row string) {
			// The format wraps the name, and the cap is applied to the
			// formatted label, so the opening bracket must survive with the
			// truncated name inside it. The cap counts the brackets, so the
			// name loses its last letter before the ellipsis does.
			if !strings.Contains(row, "<alphabetag") {
				t.Fatalf("the dock row dropped the tab format around the capped name\n%s", row)
			}
		}},
		{"compact dock", "dock_compact = true\n", 120, false, false, func(t *testing.T, row string) {
			if strings.Contains(row, long) {
				t.Fatalf("the compact dock draws the whole name with the default cap\n%s", row)
			}
			if !strings.Contains(row, "alphabetaga") {
				t.Fatalf("the compact dock draws no capped pill\n%s", row)
			}
		}},
		{"live message keeps its columns", "dock_workspace_label_max = 0\n", 140, false, true, func(t *testing.T, row string) {
			// The message is the last thing in the bar and the entries yield
			// their names to it. The yield pass counted a different width than
			// the message draw spent, and the toast paid: an entry name came
			// out one ellipsis short of a full row and the message was cut to
			// "Window… more" even though the entries had given the columns up.
			if !strings.Contains(row, "Window created (3 total)") {
				t.Fatalf("the live message lost columns its yield made room for\n%s", row)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[appearance]\n"+tc.config)
			// The rename commits through the daemon, so the client attaches to a
			// real session rather than the one a bare client bootstraps, and
			// attachIn settles it in window-management mode, where the rename
			// and minimize keys are commands instead of shell input.
			if out, err := tuiosCLI(t, base, "new", "dockcap", "--detach"); err != nil {
				t.Fatalf("create session: %v: %s", err, out)
			}
			term := attachIn(t, base, "dockcap", startOpts{cols: tc.cols, rows: 30})
			renameWorkspace(t, term, long)
			if !tc.noEntries {
				minimizeLongNames(t, term, "feature/dock-row-capacity-check", "refactor/another-quite-long-branch")
			}

			// The dock row holds at least one minimized entry before the
			// assertion, or a row missing the pill could mean the dock never
			// drew rather than the pill being gone.
			if !tc.noEntries {
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					row := dockRow(s)
					return strings.Contains(row, "feature/") || strings.Contains(row, "refactor/")
				}, uiTimeout); err != nil {
					t.Fatalf("the minimized entries never settled on the dock row: %v\n%s", err, term.Snapshot())
				}
			}
			if tc.liveMessage {
				newWindow(t, term)
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return strings.Contains(dockRow(s), "Window created (3 total)")
				}, uiTimeout); err != nil {
					t.Fatalf("the live message never drew in full on the dock row: %v\n%s", err, term.Snapshot())
				}
			}
			tc.expect(t, dockRow(term.Screen()))
		})
	}
}

// TestDockIndexTabShowsTheNameOnHover is issue 927: with a tab format that has
// no {name}, the pill shows only the number, and hovering it showed nothing.
// The hover label now says the workspace's name, and only while
// appearance.dock_workspace_tooltip is on.
func TestDockIndexTabShowsTheNameOnHover(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tooltip bool
	}{
		{"tooltip on", true},
		{"tooltip off", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			cfg := "[appearance]\ndock_workspace_tab_format = \"{index}\"\n"
			if !tc.tooltip {
				cfg += "dock_workspace_tooltip = false\n"
			}
			writeConfig(t, base, cfg)
			if out, err := tuiosCLI(t, base, "new", "dockidx", "--detach"); err != nil {
				t.Fatalf("create session: %v: %s", err, out)
			}
			term := attachIn(t, base, "dockidx", startOpts{cols: 100, rows: 24})

			// Name workspace 1, then open a pane on workspace 2, so the strip
			// shows two pills.
			if err := term.SendKeys(tuitest.Ctrl('b'), "w"); err != nil {
				t.Fatalf("open the workspace prefix: %v", err)
			}
			if err := term.SendKeys("r"); err != nil {
				t.Fatalf("open the workspace rename: %v", err)
			}
			if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
				t.Fatalf("the workspace rename dialog never opened: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys("buildbox", tuitest.Enter); err != nil {
				t.Fatalf("type the name: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return !renameDialogUp(s) }, uiTimeout); err != nil {
				t.Fatalf("the rename dialog never closed: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Alt('2')); err != nil {
				t.Fatalf("switch to workspace 2: %v", err)
			}
			newWindow(t, term)

			// The pill for workspace 1 is the first "1" between spaces on the
			// dock row.
			var col, row int
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				row = dockRowIndex(s)
				line := s.Line(row)
				at := strings.Index(line, " 1 ")
				// A column, not a byte offset: the mode pill's icon is
				// several bytes wide and one cell wide.
				col = utf8.RuneCountInString(line[:max(at, 0)])
				return at >= 0 && strings.Contains(line, " 2 ")
			}, uiTimeout); err != nil {
				t.Fatalf("the dock never showed both workspace pills: %v\n%s", err, term.Snapshot())
			}
			if strings.Contains(term.Screen().Text(), "buildbox") {
				t.Fatalf("the name is on screen before any hover, so the hover cannot be read:\n%s", term.Snapshot())
			}
			sendMouse(t, term, "hover the workspace 1 pill", tuitest.MouseEvent{
				Col: col + 1, Row: row, Button: tuitest.MouseNone, Action: tuitest.MouseMove,
			})

			err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(s.Text(), "buildbox")
			}, 3*time.Second)
			dir := artifactDir(t)
			saveArtifact(t, term, dir, "hover")
			savePNG(t, term.Screen(), hostPalette(t, ""), dir, "hover")
			switch {
			case tc.tooltip && err != nil:
				t.Fatalf("hovering an index-only pill never showed the workspace name: %v\n%s", err, term.Snapshot())
			case !tc.tooltip && err == nil:
				t.Fatalf("the name showed on hover with dock_workspace_tooltip off:\n%s", term.Snapshot())
			}
		})
	}
}
