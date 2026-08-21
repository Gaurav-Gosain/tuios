package app

import (
	tea "charm.land/bubbletea/v2"
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

// RunProgram opens a new pane whose process is e's executable.
//
// The argv is exec'd, not typed into a shell. Typed bytes are re-parsed by
// whatever shell the pane spawned, so their meaning depends on the answer:
// POSIX single-quoting is a string literal to PowerShell and nothing at all to
// cmd.exe, and in a daemon session the daemon's shell is not even knowable from
// here. Typing also raced the shell's own startup and left the command in its
// history. Exec'ing sends the argv with the NewWindow request itself, on both
// the local and the daemon path, so there is no quoting, no settle delay, and
// no pane to find after the fact. The trade is that the pane closes when the
// program exits, which is what every pane does when its process ends.
//
// The absolute path is what was listed, so it is what gets run: resolving the
// basename again could pick a different file if $PATH has changed since the
// scan.
func (m *OS) RunProgram(e applist.Entry) tea.Cmd {
	// Recorded here so the next open already ranks it, written out in a command
	// so the file never lands on the Update goroutine.
	var save tea.Cmd
	if m.launchHistory != nil {
		m.launchHistory.Note(e.Name)
		save = saveLaunchHistory(m.launchHistory)
	}

	// The pane is named after the program so the dock and the rail say what is
	// running in it before the program has printed anything.
	m.AddWindow(e.Name, e.Argv()...)
	return save
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
