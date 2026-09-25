package app

import (
	"path/filepath"
	"runtime"
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
