package app

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
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
	if guest := m.guestApps; guest != nil {
		return func() tea.Msg { return PathAppsMsg{Entries: guest} }
	}
	cache := m.pathApps
	if cache == nil {
		return nil
	}
	desktop := m.desktopApps
	return func() tea.Msg {
		holdScanForTest()
		return PathAppsMsg{Entries: scanLauncherSources(cache, desktop)}
	}
}

func holdScanForTest() {
	hold := testknob.String("hold_scan")
	if hold == "" {
		return
	}
	testknob.WaitWhile(hold, 2*time.Millisecond, time.Hour)
}

func (m *OS) knownPathApps() []applist.Entry {
	return m.launcherSource
}

func (m *OS) applyPathApps(entries []applist.Entry) {
	m.launcherSource = entries
	if !m.ShowLauncher {
		return
	}
	m.rebuildLauncherItems()
}

func stableSortByBoost(items []LauncherItem, hist *applist.Frecency) {
	sortx.SortStableFunc(items, func(a, b LauncherItem) int {
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
	if cmd, ok := m.runGUIProgram(e); ok {
		return cmd
	}
	save := m.noteLaunch(e.Name)
	if m.UserConfig != nil && m.UserConfig.Launcher.KeepOpen && m.IsDaemonSession && m.DaemonClient != nil {
		m.addDaemonWindowWith("", e.Name, "", e.Argv(), true)
		return save
	}
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
// makes this cost no timer: no poll for the pane to appear and no guessed
// settle delay.
//
// What is typed is the name rather than the listed absolute path, because the
// name is what the user would have typed and what they will now edit. The two
// agree by construction: the scan resolves $PATH by the shell's own rule, so
// the name the shell resolves is the file that was listed. A name that would
// need quoting falls back to the quoted path, which is unambiguous if uglier.
func (m *OS) TypeProgram(e applist.Entry) tea.Cmd {
	save := m.noteLaunch(e.Name)
	line := e.CommandLine() + " "

	daemon := m.IsDaemonSession && m.DaemonClient != nil

	before := len(m.Windows)
	m.AddWindow(e.Name)

	if daemon {
		m.queueSeed(e.Name, line)
		m.EnterTerminalMode()
		return save
	}

	if len(m.Windows) == before {
		m.ShowNotification("Could not open a pane for "+e.Name, "error", m.Settings.NotificationDuration*2)
		return save
	}
	m.EnterTerminalMode()
	if err := m.SendToWindow(m.Windows[before].ID, []byte(line)); err != nil {
		m.ShowNotification("Could not type "+e.Name+": "+err.Error(),
			"error", m.Settings.NotificationDuration*2)
	}
	return save
}

func (m *OS) noteLaunch(name string) tea.Cmd {
	if m.launchHistory == nil {
		return nil
	}
	m.launchHistory.Note(name)
	return saveLaunchHistory(m.launchHistory)
}

type pendingSeed struct {
	name string
	line string
}

const maxPendingSeeds = 8

func (m *OS) queueSeed(name, line string) {
	if len(m.pendingSeeds) >= maxPendingSeeds {
		m.pendingSeeds = m.pendingSeeds[1:]
	}
	m.pendingSeeds = append(m.pendingSeeds, pendingSeed{name: name, line: line})
}

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
					"error", m.Settings.NotificationDuration*2)
			}
			break
		}
	}
}

func saveLaunchHistory(h *applist.Frecency) tea.Cmd {
	return func() tea.Msg {
		_ = h.Save()
		return nil
	}
}
