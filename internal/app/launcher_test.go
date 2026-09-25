package app

import (
	"path/filepath"
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
