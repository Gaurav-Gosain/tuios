package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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
//
// The pane is identified by elimination rather than by index or by focus. In a
// daemon session it does not exist when AddWindow returns, so there is no id to
// hold on to; what can be held is the set of panes that existed before, and the
// new one is whichever is not in it. Focus was the obvious alternative and is
// wrong as soon as a second launch starts before the first has landed, because
// the second AddWindow takes focus with it.
type pendingLaunch struct {
	// line is the shell input to send once the pane exists, newline included.
	line string
	// known is the pane ids that existed when the launch was requested.
	known map[string]struct{}
	// name is the program, for the message if the pane never arrives.
	name     string
	deadline time.Time
}

// windowIDs snapshots the panes that exist now.
func (m *OS) windowIDs() map[string]struct{} {
	ids := make(map[string]struct{}, len(m.Windows))
	for _, w := range m.Windows {
		ids[w.ID] = struct{}{}
	}
	return ids
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
//
// A scan can outlive the palette that asked for it, since it runs elsewhere and
// the user may close the box before it returns. The cache keeps the entries
// either way, so the next open still builds from them; what is skipped is
// holding a few thousand built rows for a palette nobody is looking at.
func (m *OS) applyPathApps(entries []applist.Entry) {
	if !m.ShowCommandPalette {
		return
	}
	m.PaletteAppItems = m.runItems(entries)
	// The merged list is cached, so rows arriving mid-open have to go into it
	// rather than wait for a reopen.
	m.rebuildPaletteItems()
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
	// Recorded here so the next open already ranks it, written out in a command
	// so the file never lands on the Update goroutine.
	var save tea.Cmd
	if m.launchHistory != nil {
		m.launchHistory.Note(e.Name)
		save = saveLaunchHistory(m.launchHistory)
	}

	known := m.windowIDs()
	// The pane is named after the program so the dock and the rail say what is
	// running in it before the program has printed anything.
	m.AddWindow(e.Name)

	// The absolute path is what was listed, so it is what gets run: the shell
	// resolving the basename again could pick a different file if its own $PATH
	// differs from the one scanned.
	launch := &pendingLaunch{
		line:     shellSingleQuote(e.Path) + "\r",
		known:    known,
		name:     e.Name,
		deadline: time.Now().Add(launchDeadline),
	}

	// Launches queue rather than replace each other. Overwriting a pending one
	// left its pane sitting empty with nothing to say why, and two launches in
	// quick succession is an ordinary thing to do.
	m.pending = append(m.pending, launch)
	if len(m.pending) > 1 {
		// A poll is already armed and will walk the queue.
		return save
	}
	return tea.Batch(pollLaunch(), save)
}

func pollLaunch() tea.Cmd {
	return tea.Tick(launchPollInterval, func(time.Time) tea.Msg { return launchPollMsg{} })
}

// saveLaunchHistory writes the history off the Update goroutine. A failure is
// swallowed: the history is a convenience, and a launch that ran is not worth
// interrupting to report that its record did not persist.
func saveLaunchHistory(h *applist.Frecency) tea.Cmd {
	return func() tea.Msg {
		_ = h.Save()
		return nil
	}
}

// launchReady sends the queue's head into its pane once that pane exists, and
// reports the command to re-arm the check with, or nil when nothing is left to
// wait for.
//
// The queue is resolved strictly in order, and each pane that lands is added to
// the launches still waiting, so the one behind cannot mistake it for its own.
//
// The poll is one-shot and self-terminating: it only runs while a launch is in
// flight and stops when the queue empties, so an idle tuios schedules nothing.
func (m *OS) launchReady() tea.Cmd {
	for len(m.pending) > 0 {
		p := m.pending[0]
		w := m.newWindowFor(p)
		if w == nil {
			if time.Now().Before(p.deadline) {
				return pollLaunch()
			}
			m.pending = m.pending[1:]
			m.ShowNotification("The pane for "+p.name+" never appeared, so nothing was run",
				"error", config.NotificationDuration*2)
			continue
		}

		m.pending = m.pending[1:]
		// The pane that just landed belongs to this launch, so everything still
		// queued has to stop treating it as a candidate.
		for _, next := range m.pending {
			next.known[w.ID] = struct{}{}
		}
		if err := m.SendToWindow(w.ID, []byte(p.line)); err != nil {
			m.ShowNotification("Could not run "+p.name+": "+err.Error(),
				"error", config.NotificationDuration*2)
		}
	}
	return nil
}

// newWindowFor returns the pane that appeared since p was requested, or nil
// while none has.
func (m *OS) newWindowFor(p *pendingLaunch) *terminal.Window {
	for _, w := range m.Windows {
		if _, had := p.known[w.ID]; !had {
			return w
		}
	}
	return nil
}
