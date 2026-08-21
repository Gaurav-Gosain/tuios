package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// PaletteCategoryRun is the category tag on a row that runs a program.
const PaletteCategoryRun = "Run"

// paletteRunPenalty is what a $PATH program gives up against a built-in command
// or a live pane at the same match quality.
//
// Without it the curated list drowns: "new" matches "New Window" and "newgrp"
// identically, and the tiebreak on length then hands the row to newgrp. The
// penalty is smaller than one matched character (16 in pkg/fuzzy), so a program
// the query genuinely matches better still wins, and a program with enough
// launch history behind it can still climb back over a command.
const paletteRunPenalty = 12

// PathAppsMsg carries a finished $PATH scan back to the Update goroutine.
type PathAppsMsg struct {
	Entries []applist.Entry
}

// launchPollMsg checks whether the pane a launch is waiting on has appeared.
type launchPollMsg struct{}

const (
	// launchPollInterval is both the retry period and the settle time before
	// the first attempt. A shell needs a moment after its PTY opens before the
	// bytes it is sent land on a drawn prompt rather than being swallowed by
	// its own line-editor setup, which is the same reason tape playback seeds a
	// pause before it types.
	launchPollInterval = 150 * time.Millisecond
	// launchDeadline bounds the wait. In a daemon session the pane is created
	// remotely and pushed back, so it does not exist when AddWindow returns;
	// waiting forever for one that never arrives would leave a launch silently
	// pending.
	launchDeadline = 5 * time.Second
)

// pendingLaunch is a program waiting for its pane.
type pendingLaunch struct {
	// line is the shell input to send once the pane exists, newline included.
	line string
	// want is the window count that means the new pane has turned up. Counting
	// is what works in a daemon session, where the pane arrives out of band.
	want     int
	deadline time.Time
}

// ScanPathApps refreshes the $PATH scan off the Update goroutine and delivers
// the result as a message.
//
// The scan stats every directory on $PATH and reads the ones whose mtime moved,
// which on a network mount is not something the input loop can afford to wait
// for. Everything already found stays on screen while it runs, so the palette
// is typeable from the moment it opens.
func (m *OS) ScanPathApps() tea.Cmd {
	cache := m.pathApps
	if cache == nil {
		// An OS built outside NewOS (tests, the fuzz target) has no cache and no
		// launcher; it must still open a palette.
		return nil
	}
	return func() tea.Msg {
		entries, _ := cache.Refresh()
		return PathAppsMsg{Entries: entries}
	}
}

// knownPathApps is the last scan, or nothing when this OS has no launcher.
func (m *OS) knownPathApps() []applist.Entry {
	if m.pathApps == nil {
		return nil
	}
	return m.pathApps.Entries()
}

// applyPathApps turns a finished scan into palette rows.
func (m *OS) applyPathApps(entries []applist.Entry) {
	m.PaletteAppItems = m.runItems(entries)
	// The palette caches its merged list, so a scan that lands while it is open
	// has to put the new rows into that list rather than wait for a reopen.
	if m.ShowCommandPalette {
		m.rebuildPaletteItems()
	}
}

// runItems builds one palette row per program.
func (m *OS) runItems(entries []applist.Entry) []CommandPaletteItem {
	if len(entries) == 0 {
		return nil
	}
	items := make([]CommandPaletteItem, 0, len(entries))
	for _, e := range entries {
		boost := -paletteRunPenalty
		if m.launchHistory != nil {
			boost += m.launchHistory.Boost(e.Name)
		}
		items = append(items, CommandPaletteItem{
			Name:     e.Name,
			Shortcut: e.Dir,
			Category: PaletteCategoryRun,
			Boost:    boost,
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.RunProgram(e)
			},
		})
	}
	return items
}

// RunProgram opens a new pane and runs e in it.
//
// The program is typed into the pane's shell rather than exec'd as the pane's
// process. Making it the pane's process would mean threading an argv through
// terminal.NewWindow, session.NewWindowOptions and the daemon's NewWindow verb,
// a protocol change for every client; typing it works identically in a local
// and a daemon session today. It also leaves the shell in place when the
// program exits, so whatever it printed is still readable, which for a launcher
// aimed at terminal programs is the better end state anyway.
func (m *OS) RunProgram(e applist.Entry) tea.Cmd {
	if m.launchHistory != nil {
		m.launchHistory.Note(e.Name)
	}

	before := len(m.Windows)
	// The pane is named after the program so the dock and the rail say what is
	// running in it before the program has printed anything.
	m.AddWindow(e.Name)

	// The absolute path is what was listed, so it is what gets run: the shell
	// resolving the basename again could pick a different file if its own $PATH
	// differs from the one scanned.
	m.pending = &pendingLaunch{
		line:     shellSingleQuote(e.Path) + "\r",
		want:     before + 1,
		deadline: time.Now().Add(launchDeadline),
	}
	return pollLaunch()
}

func pollLaunch() tea.Cmd {
	return tea.Tick(launchPollInterval, func(time.Time) tea.Msg { return launchPollMsg{} })
}

// launchReady sends a pending launch into its pane once the pane exists, and
// reports the command to re-arm the check with, or nil when there is nothing
// left to wait for.
//
// The poll is one-shot and self-terminating: it only runs while a launch is in
// flight and stops on the first success or at the deadline, so an idle tuios
// still schedules nothing.
func (m *OS) launchReady() tea.Cmd {
	p := m.pending
	if p == nil {
		return nil
	}
	if len(m.Windows) < p.want {
		if time.Now().Before(p.deadline) {
			return pollLaunch()
		}
		m.pending = nil
		m.ShowNotification("The pane never appeared, so nothing was run", "error", config.NotificationDuration*2)
		return nil
	}

	m.pending = nil
	// AddWindow focuses what it created, and in a daemon session the sync that
	// delivers the pane focuses it too, so the focused pane is the new one.
	w := m.GetFocusedWindow()
	if w == nil {
		return nil
	}
	if err := m.SendToWindow(w.ID, []byte(p.line)); err != nil {
		m.ShowNotification("Could not run it: "+err.Error(), "error", config.NotificationDuration*2)
	}
	return nil
}
