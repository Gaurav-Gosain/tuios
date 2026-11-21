package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const railNameCol = 3

const railNarrowWidth = 16

func nameColOf(s tuitest.Screen, row int, name string) int {
	if row < 0 {
		return -1
	}
	line := railLine(s, row)
	before, _, ok := strings.Cut(line, name)
	if !ok {
		return -1
	}
	return len([]rune(before))
}

func ruledAfter(s tuitest.Screen, row, fromCol int) bool {
	w, _ := s.Size()
	for col := fromCol; col < w; col++ {
		if s.Cell(col, row).Content == "\u2500" {
			return true
		}
	}
	return false
}

func boldAt(s tuitest.Screen, col, row int) bool {
	return s.Cell(col, row).Bold
}

func writeHierarchyConfig(t *testing.T, base, tuiosPath, remoteCommand string, width int) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[appearance.sidebar]\nwidth = " + strconv.Itoa(width) + "\n\n" +
		"[hosts.oci]\n" +
		"addr = \"someone@oci\"\n" +
		"command = \"" + remoteCommand + "\"\n" +
		"connect_timeout = 5\n\n" +
		"[hosts.work]\n" +
		"addr = \"someone@poweredoff\"\n" +
		"command = \"/nonexistent/tuios\"\n" +
		"connect_timeout = 2\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_ = tuiosPath
}

// TestTheRailReadsMachinesAsHeadings is the whole visual claim, on a real
// client: a machine's name is bold and its sessions step in under it, so the
// two levels differ in weight and in position rather than in one dim glyph.
//
// What would pass a weaker test and fail this one: a rail that indents the
// rows and leaves every name at the same weight (the bold check fails), or one
// that bolds the heading and leaves the rows on the heading's own column (the
// column check fails). Either alone is what the rail already had.
func TestTheRailReadsMachinesAsHeadings(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeHierarchyConfig(t, base, tuiosBin, tuiosBin, 28)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "session-0", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "tuios"}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowOf(s, hostOpen+" oci") >= 0 && railRowOf(s, "session-0") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never drew the far machine's group: %v\n%s", err, term.Snapshot())
	}

	s := term.Screen()
	head := railRowOf(s, hostOpen+" oci")
	row := railRowOf(s, "session-0")
	headCol, rowCol := nameColOf(s, head, "oci"), nameColOf(s, row, "session-0")

	if headCol != railNameCol {
		t.Errorf("ASSERTION: the machine's name starts at column %d, want the rail's own name column %d\n%s",
			headCol, railNameCol, term.Snapshot())
	}
	if rowCol != headCol+2 {
		t.Errorf("ASSERTION: the session under oci starts at column %d and its machine at %d; "+
			"a session must step in under its machine\n%s", rowCol, headCol, term.Snapshot())
	}
	if boldAt(s, headCol, head) {
		t.Errorf("ASSERTION: the machine's name is bold, which is the voice the rail keeps for an alarm\n%s",
			term.SnapshotStyled())
	}
	if boldAt(s, rowCol, row) {
		t.Errorf("ASSERTION: the session's name is bold\n%s", term.SnapshotStyled())
	}
	if !ruledAfter(s, head, headCol+len("oci")) {
		t.Errorf("ASSERTION: the machine's heading carries no rule, so nothing tells it from a session row\n%s",
			term.Snapshot())
	}
	if ruledAfter(s, row, rowCol+len("session-0")) {
		t.Errorf("ASSERTION: a session row carries a heading's rule\n%s", term.Snapshot())
	}

	local := railRowOf(s, hostOpen+" local")
	if local < 0 {
		t.Fatalf("ASSERTION: this machine has no heading on the rail\n%s", term.Snapshot())
	}
	if boldAt(s, nameColOf(s, local, "local"), local) {
		t.Errorf("ASSERTION: this machine's own heading is bold, which is the voice the rail keeps for an alarm\n%s",
			term.SnapshotStyled())
	}
	if !ruledAfter(s, local, nameColOf(s, local, "local")+len("local")) {
		t.Errorf("ASSERTION: this machine's own heading carries no rule\n%s", term.Snapshot())
	}

	down := railRowOf(s, hostOpen+" work")
	if down < 0 || !strings.Contains(s.Text(), "offline") {
		t.Fatalf("ASSERTION: the unreachable machine left the rail\n%s", term.Snapshot())
	}

	hereInk := s.Cell(nameColOf(s, local, "local"), local).Fg
	thereInk := s.Cell(headCol, head).Fg
	downInk := s.Cell(nameColOf(s, down, "work"), down).Fg
	if hereInk != thereInk {
		t.Errorf("ASSERTION: two machines that both answer read in different inks (%v and %v)\n%s",
			hereInk, thereInk, term.SnapshotStyled())
	}
	if downInk == thereInk {
		t.Errorf("ASSERTION: a machine that does not answer reads in the same ink as one that does\n%s",
			term.SnapshotStyled())
	}
	if s.Cell(rowCol, row).Fg == thereInk {
		t.Errorf("ASSERTION: a session reads in its machine's own ink, so the two levels share it\n%s",
			term.SnapshotStyled())
	}
	saveFrame(t, term, "hier-after-machines")

	mouseClick(t, term, 3, head, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowOf(s, hostShut+" oci") >= 0 && railRowOf(s, "session-0") < 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the click never folded the machine's group: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "hier-after-folded")
	mouseClick(t, term, 3, head, tuitest.MouseLeft, 0)

	alive(t, term, "after reading the rail's hierarchy")
}

// TestTheNarrowRailKeepsTheStep is the width the step has to survive. A rail
// too narrow to say which machine a session is on is worse than a rail two
// columns shorter, so the step holds at the narrow variant and the name gives
// way, exactly as it gives way to a window count.
func TestTheNarrowRailKeepsTheStep(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeHierarchyConfig(t, base, tuiosBin, tuiosBin, railNarrowWidth)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "session-0", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"new", "tuios"}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		h, r := railRowOf(s, hostOpen+" oci"), railRowOf(s, "session-0")
		return h >= 0 && r >= 0 && nameColOf(s, r, "session-0") == nameColOf(s, h, "oci")+2
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the narrow rail lost the step under the machine: %v\n%s", err, term.Snapshot())
	}
	if w := len([]rune(railLine(term.Screen(), railRowOf(term.Screen(), hostOpen+" oci")))); w > railNarrowWidth {
		t.Fatalf("ASSERTION: the rail is %d columns wide, so this is not the narrow rail\n%s", w, term.Snapshot())
	}
	saveFrame(t, term, "hier-after-narrow")
	alive(t, term, "after the narrow rail")
}

// TestTheRailOfOneMachineIsUnchanged is the other half of the promise: the
// default install has no machine groups, so it gets no headings and no step,
// and its rows sit exactly where they always did.
func TestTheRailOfOneMachineIsUnchanged(t *testing.T) {
	term, _ := railClient(t, "e2e", railConfig(28), startOpts{cols: 120, rows: 30})
	railShows(t, term, "sessions")

	s := term.Screen()
	if railRowOf(s, hostOpen+" local") >= 0 {
		t.Fatalf("ASSERTION: a rail with one machine drew a machine heading\n%s", term.Snapshot())
	}
	row := railRowOf(s, "e2e")
	if col := nameColOf(s, row, "e2e"); col != railNameCol {
		t.Errorf("ASSERTION: the session's name starts at column %d on a rail with one machine, want %d\n%s",
			col, railNameCol, term.Snapshot())
	}
	saveFrame(t, term, "hier-after-one-machine")
	alive(t, term, "after the one-machine rail")
}

// TestTheRailWithNumbersOn is the opt-in half of the switch numbers: with
// appearance.sidebar.show_numbers on, the session's row carries the number
// switch_session_N opens, and the name steps two cells past the quiet rail's
// name column to make the room.
func TestTheRailWithNumbersOn(t *testing.T) {
	cfg := strings.Replace(railConfig(28), "[appearance.sidebar]\n",
		"[appearance.sidebar]\nshow_numbers = true\n", 1)
	term, _ := railClient(t, "e2e", cfg, startOpts{cols: 120, rows: 30})
	railShows(t, term, "sessions")

	s := term.Screen()
	row := railRowOf(s, "1 e2e")
	if row < 0 {
		t.Fatalf("ASSERTION: the rail with numbers on drew no numbered session row\n%s", term.Snapshot())
	}
	if col := nameColOf(s, row, "e2e"); col != railNameCol+2 {
		t.Errorf("ASSERTION: the numbered session's name starts at column %d, want %d\n%s",
			col, railNameCol+2, term.Snapshot())
	}
	saveFrame(t, term, "hier-after-numbers-on")
	alive(t, term, "after the numbered rail")
}

// TestAMachineHeaderOffersItsMoveRows is the reachability proof for the second
// half of the report: reordering machines already worked by drag, and nothing
// on screen said so. A right-click on a machine's header now opens a menu with
// the two moves on it, and choosing one moves the machine.
//
// The pinned machine is the other half. This machine is held at the top of the
// section, and dragging it used to do nothing at all, which is what a person
// reads as "the rail cannot be reordered".
//
// What would pass a weaker test and fail this one: a menu whose rows name
// actions the dispatcher does not have. The rows below are clicked, and the
// rail is read afterwards.
func TestAMachineHeaderOffersItsMoveRows(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	writeHostsConfig(t, base, tuiosBin)

	term := startIn(t, base, startOpts{args: []string{"new", "fed-menu"}, env: []string{"TUIOS_SSH=" + ssh}})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		b, o := railRowOf(s, hostOpen+" build"), railRowOf(s, hostOpen+" offline")
		return b >= 0 && o >= 0 && b < o
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never listed build above offline: %v\n%s", err, term.Snapshot())
	}

	local := railRowOf(term.Screen(), hostOpen+" local")
	mouseDrag(t, term, 3, local, 3, local+3, tuitest.MouseLeft, 0)
	if err := term.WaitForText("This machine stays first", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: dragging this machine said nothing at all: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "hier-after-pinned-notice")

	build := railRowOf(term.Screen(), hostOpen+" build")
	mouseClick(t, term, 3, build, tuitest.MouseRight, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Move up") && strings.Contains(text, "Move down")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a machine's header opened no menu with its moves on it: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "hier-after-machine-menu")

	down := screenRowOf(term.Screen(), "Move down")
	col := strings.Index(term.Screen().Line(down), "Move down")
	mouseClick(t, term, col, down, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		b, o := railRowOf(s, hostOpen+" build"), railRowOf(s, hostOpen+" offline")
		return b >= 0 && o >= 0 && o < b
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the menu's Move down row did not move the machine: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "hier-after-menu-moved")
	alive(t, term, "after the machine menu")
}

// TestTheNarrowRailNamesAMachineWhole is issue 181 on a real client. The rail
// used to cut the name and keep the word beside it, so a 16-column rail read
// "wo… offline" and named no machine at all. It now budgets the row: the name
// is drawn whole and the word gives way.
//
// Both halves are here. On the narrow rail the machine is named and the word
// is gone. On a rail with room for both, the same row carries both, which is
// what says the word gave way to the width rather than being dropped for good.
//
// A machine that is not answering still says so on the narrow rail, in the ink
// it and its rows are muted with. The name cell is read for that, so a rail
// that kept the name by giving up the fact fails here.
func TestTheNarrowRailNamesAMachineWhole(t *testing.T) {
	for _, tc := range []struct {
		name      string
		width     int
		wantWord  bool
		frameName string
	}{
		{"narrow", railNarrowWidth, false, "rail-narrow-machine-name"},
		{"wide", 28, true, "rail-wide-machine-name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			remote := remoteMachine(t)
			ssh := writeFakeSSHTo(t, base, remote)
			writeHierarchyConfig(t, base, tuiosBin, tuiosBin, tc.width)
			env := []string{"TUIOS_SSH=" + ssh}
			if out, err := tuiosCLI(t, remote, "new", "session-0", "--detach"); err != nil {
				t.Fatalf("create the far session: %v\n%s", err, out)
			}

			term := startIn(t, base, startOpts{args: []string{"new", "tuios"}, env: env})
			waitBoot(t, term)
			toggleSidebarViaPalette(t, term)

			if err := term.WaitFor(func(s tuitest.Screen) bool {
				down, up := railRowOf(s, hostOpen+" wo"), railRowOf(s, hostOpen+" oci")
				if down < 0 || up < 0 {
					return false
				}
				return s.Cell(nameColOf(s, down, "wo"), down).Fg != s.Cell(nameColOf(s, up, "oci"), up).Fg
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the rail never named both machines, one of them muted as not answering: %v\n%s", err, term.Snapshot())
			}
			saveFrame(t, term, tc.frameName)

			s := term.Screen()
			row := railRowOf(s, hostOpen+" wo")
			line := railLine(s, row)
			if strings.Contains(line, "…") || strings.Contains(line, "wo…") {
				t.Fatalf("ASSERTION: the rail cut the machine's name at width %d: %q\n%s",
					tc.width, line, term.Snapshot())
			}
			if got := strings.Contains(line, "offline"); got != tc.wantWord {
				t.Fatalf("ASSERTION: the row at width %d says offline = %v, want %v: %q\n%s",
					tc.width, got, tc.wantWord, line, term.Snapshot())
			}
			alive(t, term, "after the budgeted machine header")
		})
	}
}
