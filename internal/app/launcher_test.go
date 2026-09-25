package app

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

func runTestOS(t *testing.T) *OS {
	t.Helper()
	return &OS{
		Settings: config.Global,
		// Open, because applyPathApps declines to build rows for a launcher
		// nobody is looking at.
		ShowLauncher: true,

		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		pathApps:         applist.NewCache(),
		launchHistory:    applist.LoadFrecency(filepath.Join(t.TempDir(), "launcher.json")),
	}
}

func fakeEntries(names ...string) []applist.Entry {
	out := make([]applist.Entry, len(names))
	for i, n := range names {
		out[i] = applist.Entry{Name: n, Path: filepath.Join("/usr/bin", n), Dir: "/usr/bin", Source: applist.SourcePath}
	}
	return out
}

// seedLauncher fills the launcher's rows the way a finished scan does, without
// touching the real $PATH. It goes through applyPathApps so the rows are built
// and ordered exactly as a real scan builds them.
func seedLauncher(t *testing.T, m *OS, names ...string) {
	t.Helper()
	open := m.ShowLauncher
	m.ShowLauncher = true
	m.applyPathApps(fakeEntries(names...))
	m.ShowLauncher = open
}

func launcherNames(items []LauncherItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Entry.Name
	}
	return out
}

// TestPaletteOpensTheLauncher keeps one box findable from the other, so a user
// who only remembers ctrl+p can still get to a program.
func TestPaletteOpensTheLauncher(t *testing.T) {
	m := runTestOS(t)
	m.ShowLauncher = false
	m.PaletteItems = nil

	filtered := FilterCommandPalette(m.allPaletteItems(), "run a program")
	if len(filtered) == 0 {
		t.Fatal("the palette offers no way to reach the launcher")
	}
	if _, cmd := filtered[0].Action(m); cmd == nil && m.pathApps != nil {
		t.Error("the row did not start a scan")
	}
	if !m.ShowLauncher {
		t.Fatal("the palette row did not open the launcher")
	}
}

// TestExactProgramNameRanksFirst is the baseline the whole list rests on.
func TestExactProgramNameRanksFirst(t *testing.T) {
	m := runTestOS(t)
	seedLauncher(t, m, "gnome-calculator", "gcc")

	m.LauncherQuery = "gcc"
	got := launcherNames(m.filteredLauncherItems())
	if len(got) == 0 || got[0] != "gcc" {
		t.Fatalf("filtered = %v, want gcc first", got)
	}
}

// TestFrecencyLiftsAUsedProgram is the promise that makes a launcher worth
// opening twice: having run something once, it is above its rivals next time.
func TestFrecencyLiftsAUsedProgram(t *testing.T) {
	m := runTestOS(t)
	seedLauncher(t, m, "gnome-characters", "gnome-calculator")
	m.LauncherQuery = "gnome"

	before := launcherNames(m.filteredLauncherItems())
	if len(before) < 2 {
		t.Fatalf("filtered = %v, want both programs", before)
	}
	loser := before[1]

	for range 4 {
		m.launchHistory.Note(loser)
	}
	after := launcherNames(m.filteredLauncherItems())
	if len(after) == 0 || after[0] != loser {
		t.Fatalf("filtered = %v, want the used program %q first", after, loser)
	}
}

// TestBoostCannotOutrankAClearlyBetterMatch keeps frecency a tiebreaker. A
// program run constantly must not shove aside the one whose name was typed.
func TestBoostCannotOutrankAClearlyBetterMatch(t *testing.T) {
	m := runTestOS(t)
	seedLauncher(t, m, "gcc", "git-credential-cache")
	for range 100 {
		m.launchHistory.Note("git-credential-cache")
	}
	m.LauncherQuery = "gcc"

	got := launcherNames(m.filteredLauncherItems())
	if len(got) == 0 || got[0] != "gcc" {
		t.Fatalf("filtered = %v, want gcc first despite the other's history", got)
	}
}

// TestEmptyQueryLeadsWithHistory is what an empty launcher should open on: the
// things this person actually runs, not the alphabetical head of /usr/bin.
func TestEmptyQueryLeadsWithHistory(t *testing.T) {
	m := runTestOS(t)
	m.launchHistory.Note("ccc")
	// The ordering is applied when the rows are built, which is what an open
	// does, so the history has to be there first.
	seedLauncher(t, m, "aaa", "bbb", "ccc")

	got := launcherNames(m.filteredLauncherItems())
	if len(got) == 0 || got[0] != "ccc" {
		t.Fatalf("filtered = %v, want the used program first", got)
	}
	if !slices.Contains(got, "aaa") || len(got) != 3 {
		t.Fatalf("filtered = %v, want every program still listed", got)
	}
}

// TestFilteringDoesNotDisturbTheCachedList is what lets the empty-query filter
// hand its input straight back. The row list is cached across keystrokes, so a
// filter that reordered or rewrote it would change what the next keystroke
// filters.
func TestFilteringDoesNotDisturbTheCachedList(t *testing.T) {
	m := runTestOS(t)
	seedLauncher(t, m, "aaa", "bbb", "ccc")
	before := launcherNames(m.LauncherItems)

	m.LauncherQuery = "b"
	_ = m.filteredLauncherItems()
	m.LauncherQuery = ""
	_ = m.filteredLauncherItems()

	if got := launcherNames(m.LauncherItems); !slices.Equal(got, before) {
		t.Fatalf("the cached list changed from %v to %v", before, got)
	}
}

// TestRunProgramExecsTheListedPath pins the run half of the choice: the pane's
// process is the listed executable itself, argv exec'd with no shell in
// between. A path with a space is the canary, because it is exactly what a
// typed command would have had to quote and what a wrong quoting dialect
// (PowerShell, cmd.exe) silently breaks.
func TestRunProgramExecsTheListedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe script is a shebang script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "odd name")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := runTestOS(t)
	m.WindowExitChan = make(chan string, 4)
	defer closeWindows(m)
	e := applist.Entry{Name: "odd name", Path: path, Dir: dir}

	m.RunProgram(e)
	if len(m.Windows) != 1 {
		t.Fatalf("%d windows after a launch, want 1", len(m.Windows))
	}
	w := m.Windows[0]
	if w.Cmd == nil || len(w.Cmd.Args) != 1 || w.Cmd.Args[0] != path {
		t.Fatalf("pane process argv = %v, want [%s]", w.Cmd.Args, path)
	}
	if w.CustomName != e.Name {
		t.Errorf("CustomName = %q, want the program's name %q", w.CustomName, e.Name)
	}
	if m.launchHistory.Boost(e.Name) == 0 {
		t.Error("running a program did not record it in the launch history")
	}
}

// TestTypeProgramSpawnsAShellAndTypesIntoIt is the type half. The pane runs the
// user's shell rather than the program, so the command is theirs to finish, and
// the launch is still recorded because choosing it is still choosing it.
func TestTypeProgramSpawnsAShellAndTypesIntoIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe script is a shebang script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := runTestOS(t)
	m.WindowExitChan = make(chan string, 4)
	m.PTYDataChan = make(chan struct{}, 1)
	defer closeWindows(m)

	e := applist.Entry{Name: "ffmpeg", Path: path, Dir: dir}
	m.TypeProgram(e)

	if len(m.Windows) != 1 {
		t.Fatalf("%d windows after a type-out, want 1", len(m.Windows))
	}
	w := m.Windows[0]
	if w.Cmd != nil && len(w.Cmd.Args) > 0 && w.Cmd.Args[0] == path {
		t.Fatal("the pane runs the program itself, so there is nothing left to add arguments to")
	}
	if w.CustomName != e.Name {
		t.Errorf("CustomName = %q, want %q", w.CustomName, e.Name)
	}
	if m.launchHistory.Boost(e.Name) == 0 {
		t.Error("typing a program out did not record it in the launch history")
	}
}

// TestSeedsWaitForTheirOwnPane covers the daemon half, where the pane does not
// exist when the launch is asked for. Each queued line claims the pane that
// carries its name and no other, so two launches in flight at once cannot cross
// wires the way the old find-it-by-elimination version could.
func TestSeedsWaitForTheirOwnPane(t *testing.T) {
	m := runTestOS(t)
	m.queueSeed("ffmpeg", "ffmpeg ")
	m.queueSeed("htop", "htop ")

	// The panes arrive in the other order, which is the case that matters: the
	// queue is drained by name, not by position.
	m.seedAdoptedWindows([]*terminal.Window{{ID: "w1", CustomName: "htop"}})
	if len(m.pendingSeeds) != 1 || m.pendingSeeds[0].name != "ffmpeg" {
		t.Fatalf("queue = %+v, want only ffmpeg still waiting", m.pendingSeeds)
	}

	m.seedAdoptedWindows([]*terminal.Window{{ID: "w2", CustomName: "ffmpeg"}})
	if len(m.pendingSeeds) != 0 {
		t.Fatalf("queue = %+v, want it drained", m.pendingSeeds)
	}
}

// TestAPaneClaimsOneSeed is the two-launches-of-the-same-program case: two
// panes named alike must take one line each rather than both taking the first.
func TestAPaneClaimsOneSeed(t *testing.T) {
	m := runTestOS(t)
	m.queueSeed("ffmpeg", "ffmpeg ")
	m.queueSeed("ffmpeg", "ffmpeg ")

	m.seedAdoptedWindows([]*terminal.Window{{ID: "w1", CustomName: "ffmpeg"}})
	if len(m.pendingSeeds) != 1 {
		t.Fatalf("%d queued lines, want one pane to have claimed exactly one", len(m.pendingSeeds))
	}
}

// TestTypeProgramAsksWhichSessionItIsIn pins the fix for a real confusion. The
// two halves used to be told apart by whether a pane appeared, but AddWindow
// also returns without one when the PTY could not be created, so a local
// failure was read as "this must be the daemon" and parked the line in a queue
// nothing would ever drain.
//
// A session with the flag set but no client is exactly that shape, and it has
// to take the local path.
func TestTypeProgramAsksWhichSessionItIsIn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("spawns a shell")
	}
	m := runTestOS(t)
	m.WindowExitChan = make(chan string, 4)
	m.PTYDataChan = make(chan struct{}, 1)
	defer closeWindows(m)
	m.IsDaemonSession = true // but DaemonClient is nil, so there is no daemon

	m.TypeProgram(applist.Entry{Name: "ffmpeg", Path: "/usr/bin/ffmpeg", Dir: "/usr/bin"})

	if len(m.pendingSeeds) != 0 {
		t.Fatalf("queued %d lines for a daemon that is not there", len(m.pendingSeeds))
	}
	if len(m.Windows) != 1 {
		t.Fatalf("%d panes, want the local path to have opened one", len(m.Windows))
	}
}

// TestPendingSeedsAreBounded keeps a queue that nothing drains from growing,
// and from typing a stale line into an unrelated pane much later.
func TestPendingSeedsAreBounded(t *testing.T) {
	m := runTestOS(t)
	for range maxPendingSeeds + 4 {
		m.queueSeed("ffmpeg", "ffmpeg ")
	}
	if len(m.pendingSeeds) != maxPendingSeeds {
		t.Fatalf("%d queued lines, want the queue capped at %d", len(m.pendingSeeds), maxPendingSeeds)
	}
}

// TestMatchPositionsAreRenderable is the contract between the filter and the
// renderer: the offsets must index the string the row actually draws.
func TestMatchPositionsAreRenderable(t *testing.T) {
	m := runTestOS(t)
	seedLauncher(t, m, "ripgrep")
	m.LauncherQuery = "rg"

	filtered := m.filteredLauncherItems()
	if len(filtered) != 1 {
		t.Fatalf("ripgrep did not match rg")
	}
	row := filtered[0]
	if len(row.Match) != 2 {
		t.Fatalf("Match = %v, want two positions", row.Match)
	}
	name := row.Entry.Name
	if name[row.Match[0]] != 'r' || name[row.Match[1]] != 'g' {
		t.Fatalf("Match %v points at %q, not the matched characters", row.Match, name)
	}
}
