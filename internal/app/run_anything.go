package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// PaletteCategoryRun is the category tag on the palette row that opens the
// launcher.
const PaletteCategoryRun = "Run"

// PathAppsMsg carries a finished scan of the launcher's sources back to the
// Update goroutine.
type PathAppsMsg struct {
	Entries []applist.Entry
}

// ScanPathApps refreshes the launcher's sources off the Update goroutine and
// delivers the merged result as a message.
//
// The scan stats every directory on $PATH and reads the ones whose mtime moved,
// and parses every .desktop file that changed, neither of which the input loop
// can afford to wait for on a network mount. Everything already found stays on
// screen while it runs, so the launcher is typeable from the moment it opens.
func (m *OS) ScanPathApps() tea.Cmd {
	cache := m.pathApps
	if cache == nil {
		// An OS built outside NewOS (tests, the fuzz target) has no cache and no
		// launcher; it must still open one.
		return nil
	}
	desktop := m.desktopApps
	return func() tea.Msg {
		return PathAppsMsg{Entries: scanLauncherSources(cache, desktop)}
	}
}

// knownPathApps is the last merged scan.
//
// It is held here rather than re-merged from the two caches on demand, because
// the merge is a pass over several thousand entries and the launcher opens more
// often than the filesystem changes.
func (m *OS) knownPathApps() []applist.Entry {
	return m.launcherSource
}

// applyPathApps files a finished scan away and rebuilds the rows from it.
//
// A scan can outlive the launcher that asked for it, since it runs elsewhere
// and the user may close the overlay before it returns. The entries are kept
// either way, so the next open still builds from them; what is skipped is
// holding a few thousand built rows for an overlay nobody is looking at.
func (m *OS) applyPathApps(entries []applist.Entry) {
	m.launcherSource = entries
	if !m.ShowLauncher {
		return
	}
	m.rebuildLauncherItems()
}

// stableSortByBoost orders items by launch history, strongest first, keeping
// the input order between equal ones.
func stableSortByBoost(items []LauncherItem, hist *applist.Frecency) {
	slices.SortStableFunc(items, func(a, b LauncherItem) int {
		return hist.Boost(b.Entry.Name) - hist.Boost(a.Entry.Name)
	})
}

// RunProgram opens a new pane whose process is e's executable.
//
// The argv is exec'd, not typed into a shell. Typed bytes are re-parsed by
// whatever shell the pane spawned, so their meaning depends on the answer:
// POSIX single-quoting is a string literal to PowerShell and nothing at all to
// cmd.exe, and in a daemon session the daemon's shell is not even knowable from
// here. Exec'ing sends the argv with the NewWindow request itself, on both the
// local and the daemon path, so there is no quoting and no pane to find after
// the fact. The trade is that the pane closes when the program exits, which is
// what every pane does when its process ends.
//
// TypeProgram is the other half of the choice, for when the command is not
// finished being written.
//
// The absolute path is what was listed, so it is what gets run: resolving the
// basename again could pick a different file if $PATH has changed since the
// scan.
func (m *OS) RunProgram(e applist.Entry) tea.Cmd {
	save := m.noteLaunch(e.Name)
	// The pane is named after the program so the dock and the rail say what is
	// running in it before the program has printed anything.
	m.AddWindow(e.Name, e.Argv()...)
	return save
}

// TypeProgram opens a new shell pane with e's command line waiting at the
// prompt, unentered, for the user to add arguments to and run themselves.
//
// The line is written to the pane's PTY without waiting for the shell to be
// ready, because there is nothing to wait for: the bytes land in the terminal's
// input queue and the shell's line editor reads them when it starts, the same
// way a keystroke that beat the prompt has always been handled. That is what
// makes this cost no timer. The earlier version of this path polled on a
// tea.Tick for a pane to appear and then guessed at a settle delay; both are
// gone.
//
// What is typed is the name rather than the listed absolute path, because the
// name is what the user would have typed and what they will now edit. The two
// agree by construction: the scan resolves $PATH by the shell's own rule, so
// the name the shell resolves is the file that was listed. A name that would
// need quoting falls back to the quoted path, which is unambiguous if uglier.
func (m *OS) TypeProgram(e applist.Entry) tea.Cmd {
	save := m.noteLaunch(e.Name)
	// A trailing space so the first thing typed is an argument, not the rest of
	// a word.
	line := e.CommandLine() + " "

	before := len(m.Windows)
	m.AddWindow(e.Name)
	// Typing it out means the user is about to keep typing, so the pane is
	// handed over ready for that. Run does not do this and should not: the two
	// keys differ in exactly this intent, and landing in window management mode
	// with a half-written command on the prompt would send the arguments to the
	// window manager instead of the shell.
	m.EnterTerminalMode()
	if len(m.Windows) > before {
		// Local: the pane exists as soon as AddWindow returns, so the line goes
		// straight into it.
		if err := m.SendToWindow(m.Windows[before].ID, []byte(line)); err != nil {
			m.ShowNotification("Could not type "+e.Name+": "+err.Error(),
				"error", config.NotificationDuration*2)
		}
		return save
	}

	// Daemon: the pane is created by the daemon and arrives with a later state
	// sync, so the line waits for it there. Waiting on the pane's arrival is an
	// event rather than a poll, which is the whole difference from the version
	// this replaces.
	m.queueSeed(e.Name, line)
	return save
}

// noteLaunch records that a program was chosen and returns the command that
// writes the history out.
func (m *OS) noteLaunch(name string) tea.Cmd {
	if m.launchHistory == nil {
		return nil
	}
	// Noted here so the next open already ranks it, written out in a command so
	// the file never lands on the Update goroutine.
	m.launchHistory.Note(name)
	return saveLaunchHistory(m.launchHistory)
}

// pendingSeed is a command line waiting for the daemon-created pane it belongs
// to.
type pendingSeed struct {
	// name is the pane's CustomName, which the daemon sets from the same
	// argument that named it here.
	name string
	line string
}

// maxPendingSeeds bounds the queue. A pane that never arrives would otherwise
// leave its line waiting for the life of the session, and typing an old one
// into an unrelated pane later is worse than dropping it.
const maxPendingSeeds = 8

// queueSeed parks a command line until its pane turns up.
func (m *OS) queueSeed(name, line string) {
	if len(m.pendingSeeds) >= maxPendingSeeds {
		m.pendingSeeds = m.pendingSeeds[1:]
	}
	m.pendingSeeds = append(m.pendingSeeds, pendingSeed{name: name, line: line})
}

// seedAdoptedWindows types any queued command line into the pane it was meant
// for. Called with the panes a state sync just created, which is the event that
// replaces the old poll.
//
// Matching is by name and each pane is claimed once, so two launches of the
// same program in quick succession fill their own panes rather than both
// filling the first.
func (m *OS) seedAdoptedWindows(created []*terminal.Window) {
	if len(m.pendingSeeds) == 0 || len(created) == 0 {
		return
	}
	for _, w := range created {
		if w == nil {
			continue
		}
		for i, p := range m.pendingSeeds {
			if p.name != w.CustomName {
				continue
			}
			m.pendingSeeds = slices.Delete(m.pendingSeeds, i, i+1)
			if err := m.SendToWindow(w.ID, []byte(p.line)); err != nil {
				m.ShowNotification("Could not type "+p.name+": "+err.Error(),
					"error", config.NotificationDuration*2)
			}
			break
		}
	}
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
