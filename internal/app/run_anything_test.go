package app

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/pkg/applist"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

func runTestOS(t *testing.T) *OS {
	t.Helper()
	return &OS{
		// Open, because the palette is the only reader of the launcher rows and
		// applyPathApps declines to build them for a closed one.
		ShowCommandPalette: true,

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

// paletteNameList is paletteNames as a slice, for the order assertions.
func paletteNameList(items []CommandPaletteItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

// TestPathAppsReachThePalette is the whole feature in one assertion: a program
// on $PATH is findable in the same box that finds commands and panes.
func TestPathAppsReachThePalette(t *testing.T) {
	m := runTestOS(t)
	m.applyPathApps(fakeEntries("htop", "gcc", "ripgrep"))

	m.PaletteItems = nil
	filtered := FilterCommandPalette(m.allPaletteItems(), "htop")
	if len(filtered) == 0 || filtered[0].Name != "htop" {
		t.Fatalf("filtered = %v, want htop first", paletteNames(filtered))
	}
	if filtered[0].Category != PaletteCategoryRun {
		t.Errorf("category = %q, want %q", filtered[0].Category, PaletteCategoryRun)
	}
	if filtered[0].Action == nil {
		t.Error("a program row with no action cannot be run")
	}
}

// TestCuratedCommandsSurviveThePathTier is the dilution guard. "new" matches
// "New Window" and a program called newgrp equally well, and the length
// tiebreak alone would hand the row to newgrp; the run penalty is what keeps
// the command a user reaches for above three thousand binaries.
func TestCuratedCommandsSurviveThePathTier(t *testing.T) {
	m := runTestOS(t)
	m.applyPathApps(fakeEntries("newgrp", "newaliases", "newusers"))

	m.PaletteItems = nil
	filtered := FilterCommandPalette(m.allPaletteItems(), "new")
	if len(filtered) == 0 {
		t.Fatal("no matches for new")
	}
	if filtered[0].Category == PaletteCategoryRun {
		t.Fatalf("filtered = %v, want a built-in command first", paletteNames(filtered))
	}
	if !slices.Contains(paletteNameList(filtered), "newgrp") {
		t.Errorf("filtered = %v, want the programs still reachable below", paletteNames(filtered))
	}
}

// TestExactProgramNameBeatsTheTier checks the penalty is a nudge and not a
// wall: typing a program's exact name must still put it first.
func TestExactProgramNameBeatsTheTier(t *testing.T) {
	m := runTestOS(t)
	m.applyPathApps(fakeEntries("gcc"))

	m.PaletteItems = nil
	filtered := FilterCommandPalette(m.allPaletteItems(), "gcc")
	if len(filtered) == 0 || filtered[0].Name != "gcc" {
		t.Fatalf("filtered = %v, want gcc first", paletteNames(filtered))
	}
}

// TestFrecencyLiftsAUsedProgram is the promise that makes a launcher worth
// opening twice: having run something once, it is above its rivals next time.
func TestFrecencyLiftsAUsedProgram(t *testing.T) {
	m := runTestOS(t)
	entries := fakeEntries("gnome-characters", "gnome-calculator")

	m.applyPathApps(entries)
	m.PaletteItems = nil
	before := FilterCommandPalette(m.allPaletteItems(), "gnome")
	if len(before) < 2 {
		t.Fatalf("filtered = %v, want both programs", paletteNames(before))
	}
	loser := before[1].Name

	for range 4 {
		m.launchHistory.Note(loser)
	}
	// Rows carry the boost they were built with, so the history has to be
	// re-read the way reopening the palette does it.
	m.applyPathApps(entries)
	m.PaletteItems = nil

	after := FilterCommandPalette(m.allPaletteItems(), "gnome")
	if len(after) == 0 || after[0].Name != loser {
		t.Fatalf("filtered = %v, want the used program %q first", paletteNames(after), loser)
	}
}

// TestBoostCannotOutrankAClearlyBetterMatch keeps frecency a tiebreaker. A
// program run constantly must not shove aside the one whose name was typed.
func TestBoostCannotOutrankAClearlyBetterMatch(t *testing.T) {
	m := runTestOS(t)
	entries := fakeEntries("gcc", "git-credential-cache")
	for range 100 {
		m.launchHistory.Note("git-credential-cache")
	}
	m.applyPathApps(entries)
	m.PaletteItems = nil

	filtered := FilterCommandPalette(m.allPaletteItems(), "gcc")
	if len(filtered) == 0 || filtered[0].Name != "gcc" {
		t.Fatalf("filtered = %v, want gcc first despite the other's history", paletteNames(filtered))
	}
}

// TestScanPathAppsRunsOffTheUpdateGoroutine checks the shape the constraint
// demands: opening the palette hands back a command to run elsewhere, and the
// palette is usable before it has run.
func TestScanPathAppsRunsOffTheUpdateGoroutine(t *testing.T) {
	dir := t.TempDir()
	name := "tuios-scan-probe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	m := runTestOS(t)
	cmd := m.OpenCommandPalette()
	if cmd == nil {
		t.Fatal("OpenCommandPalette returned no scan command")
	}
	if !m.ShowCommandPalette {
		t.Error("the palette must be open before the scan runs")
	}
	if len(m.allPaletteItems()) == 0 {
		t.Error("the palette must be typeable before the scan lands")
	}

	msg, ok := cmd().(PathAppsMsg)
	if !ok {
		t.Fatalf("scan produced %T, want PathAppsMsg", msg)
	}
	m2, _ := m.Update(msg)
	m = m2.(*OS)

	if !slices.Contains(paletteNameList(m.allPaletteItems()), name) {
		t.Fatalf("the scanned program never reached the palette")
	}
}

// TestRunProgramExecsTheListedPath pins the launch semantics: the pane's
// process is the listed executable itself, argv exec'd with no shell in
// between. A path with a space is the canary, because it is exactly what the
// typed-into-a-shell version had to quote and what a wrong quoting dialect
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

// TestPaletteMatchPositionsAreRenderable is the contract between the filter and
// the renderer: the offsets must index the string the row actually draws.
func TestPaletteMatchPositionsAreRenderable(t *testing.T) {
	m := runTestOS(t)
	m.applyPathApps(fakeEntries("ripgrep"))
	m.PaletteItems = nil

	filtered := FilterCommandPalette(m.allPaletteItems(), "rg")
	var row *CommandPaletteItem
	for i := range filtered {
		if filtered[i].Name == "ripgrep" {
			row = &filtered[i]
		}
	}
	if row == nil {
		t.Fatal("ripgrep did not match rg")
	}
	name := printableTitle(row.Name)
	if len(row.Match) != 2 {
		t.Fatalf("Match = %v, want two positions", row.Match)
	}
	if name[row.Match[0]] != 'r' || name[row.Match[1]] != 'g' {
		t.Fatalf("Match %v points at %q, not the matched characters", row.Match, name)
	}
}

// TestRunItemsCarryNoBoostWithoutHistory keeps the tier penalty honest: an
// unused program is exactly one penalty below par, no more.
func TestRunItemsCarryNoBoostWithoutHistory(t *testing.T) {
	m := runTestOS(t)
	items := m.runItems(fakeEntries("neverrun"))
	if len(items) != 1 {
		t.Fatalf("runItems = %d rows, want 1", len(items))
	}
	if items[0].Boost != -paletteRunPenalty {
		t.Fatalf("Boost = %d, want %d", items[0].Boost, -paletteRunPenalty)
	}
	if items[0].Boost+applist.MaxBoost <= 0 {
		t.Error("no launch history could ever lift a program back over a command")
	}
}

// TestPaletteFilterStillRanksWithoutFuzzyImport is a canary on the shared
// matcher: the palette must be ranking through pkg/fuzzy, not a private copy.
func TestPaletteFilterStillRanksWithoutFuzzyImport(t *testing.T) {
	items := []CommandPaletteItem{
		{Name: "gnome-calculator", Category: PaletteCategoryRun},
		{Name: "gcc", Category: PaletteCategoryRun},
	}
	got := paletteNameList(FilterCommandPalette(items, "gc"))
	want := []string{"gcc", "gnome-calculator"}
	if !slices.Equal(got, want) {
		t.Fatalf("filtered = %v, want %v", got, want)
	}
	if _, ok := fuzzy.Score("gc", "gcc"); !ok {
		t.Fatal("the shared matcher no longer matches gc against gcc")
	}
}

// TestTwoLaunchesEachGetTheirOwnPane is the regression for launches crossing
// wires. When the command was typed after the fact, the pane had to be found
// again by elimination and a concurrent launch could claim the wrong one; with
// the argv riding the creation request, each pane is born running its own
// program and there is nothing left to mix up.
func TestTwoLaunchesEachGetTheirOwnPane(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe scripts are shebang scripts")
	}
	dir := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	m := runTestOS(t)
	m.WindowExitChan = make(chan string, 4)
	defer closeWindows(m)

	m.RunProgram(applist.Entry{Name: "alpha", Path: filepath.Join(dir, "alpha"), Dir: dir})
	m.RunProgram(applist.Entry{Name: "beta", Path: filepath.Join(dir, "beta"), Dir: dir})

	if len(m.Windows) != 2 {
		t.Fatalf("%d windows after two launches, want 2", len(m.Windows))
	}
	for i, want := range []string{"alpha", "beta"} {
		w := m.Windows[i]
		if w.Cmd == nil || w.Cmd.Args[0] != filepath.Join(dir, want) {
			t.Errorf("window %d runs %v, want %s", i, w.Cmd.Args, want)
		}
	}
}
