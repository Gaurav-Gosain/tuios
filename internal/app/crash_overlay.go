package app

import (
	tea "charm.land/bubbletea/v2"
)

// The crash overlay: the screen tuios shows when it reaches a state it should
// not have reached.
//
// It is not one of the pickers, and the difference is the point. Every other
// overlay is drawn by renderOverlays, inside composeFrame, out of the model's
// live state. This one is drawn by View before composeFrame is called at all,
// from a snapshot that was taken before anything was drawn. That is what makes
// it safe to show after the compositor itself has panicked: if it went through
// the normal path it would re-enter the code that just failed, panic again, and
// leave the user with the same blank frame it exists to replace.
//
// The same reasoning puts its keys at the top of handleMsg rather than in
// internal/input. The input package walks the sidebar, the focused window, the
// keybind registry and a dozen Show* flags to decide who owns a key. All of
// that is model state, and the model is what is in question. Three keys, read
// before any of it, cost nothing and cannot be taken away by whatever broke.

// maxRecentActions is how many keybind actions the crash report remembers.
//
// Five is what fits on one line of the report and is about as far back as a
// user can confirm from memory. It is a ring on the model, not a log: it holds
// action names only, never keystrokes and never text, so it costs one string
// store per action and carries nothing private. See crashRecentActions.
const maxRecentActions = 5

// NoteAction records that a keybind action ran. The action dispatcher calls it
// for every action it dispatches, which covers keybindings, prefix chords and
// context menu rows, since all three go through the one dispatcher.
func (m *OS) NoteAction(action string) {
	if m == nil || action == "" {
		return
	}
	// A held key repeats its action, and five rows of the same name says less
	// than one does. Collapse a repeat rather than spending the ring on it.
	if n := len(m.recentActions); n > 0 && m.recentActions[n-1] == action {
		return
	}
	m.recentActions = append(m.recentActions, action)
	if len(m.recentActions) > maxRecentActions {
		m.recentActions = m.recentActions[len(m.recentActions)-maxRecentActions:]
	}
}

// RecentActions returns the keybind actions this client last ran, oldest first.
func (m *OS) RecentActions() []string {
	if m == nil {
		return nil
	}
	return m.recentActions
}

// CrashActive reports whether the crash overlay is on screen.
func (m *OS) CrashActive() bool { return m != nil && m.crash != nil }

// Crash returns the report on screen, or nil.
func (m *OS) Crash() *CrashReport {
	if m == nil {
		return nil
	}
	return m.crash
}

// NoteCrash captures a recovered panic and puts the crash overlay on screen.
//
// where is the phrase the overlay shows: it completes "tuios hit a bug while
// ...", so it reads as "handling an event", not as a function name.
//
// It never panics. Building the report reads the model that has just failed, so
// the read is behind its own barrier and so is this; a crash overlay that
// crashes is worse than no crash overlay, because the first panic was survivable
// and the second one is inside the code meant to survive it.
func (m *OS) NoteCrash(where string, panicValue any, stack []byte) {
	if m == nil {
		return
	}
	defer func() {
		// Nothing to do but stop. The panic that brought us here is already
		// logged by the caller, and there is no third barrier under this one.
		_ = recover()
	}()

	report := NewCrashReport(where, panicValue, stack, m.crashFacts(where))
	WriteCrashLog(report)

	// The first crash is the one worth reading. A panic in the render path
	// repeats on every frame, so overwriting would leave the user reading the
	// hundredth copy of the same trace with the original's context replaced by
	// the state the overlay itself produced.
	if m.crash == nil {
		m.crash = report
	}
	// The overlay replaces the frame, so the cached one must not win.
	m.renderSkipped = false
}

// DismissCrash takes the crash overlay off the screen.
//
// It is always allowed, even for a panic in the render path, and that is safe
// rather than merely permitted: dismissing lets View try composeFrame again,
// and if the frame still cannot be drawn the render barrier catches it and puts
// the overlay straight back. The worst case is a screen that will not clear,
// which is what the user already had.
func (m *OS) DismissCrash() {
	if m == nil || m.crash == nil {
		return
	}
	m.crash = nil
	m.crashNotice = ""
	m.MarkAllDirty()
	m.renderSkipped = false
}

// CopyCrashReport puts the whole report on the clipboard.
//
// The whole of it: the clipboard has no length limit worth working around, and
// the trace this trims for the issue URL is exactly the part a maintainer
// reads. OSC 52 lands on the terminal the client is drawing into, which is the
// user's own on every deployment tuios has, so this is the one action that
// behaves identically for a local, an SSH and a web client.
func (m *OS) CopyCrashReport() tea.Cmd {
	if m == nil || m.crash == nil {
		return nil
	}
	// The confirmation goes out through ShowNotification like any other, and
	// reaches the overlay because showNotification mirrors onto it while it is
	// up. Nothing here has to know that the dock is not being drawn.
	m.ShowNotification("Copied the report. Paste it into a new issue.",
		"success", m.Settings.NotificationDuration)
	return tea.SetClipboard(m.crash.Markdown(0))
}

// OpenCrashIssue opens a new issue with the report already filled in.
//
// The per-client behaviour is OpenLink's and is not repeated here: a local
// client hands the address to the desktop, and an SSH or web client puts it on
// the clipboard and says why, because there is no way to open a browser on the
// far side of a terminal. See link_open.go, which is written around exactly
// that split.
func (m *OS) OpenCrashIssue() tea.Cmd {
	if m == nil || m.crash == nil {
		return nil
	}
	// OpenLink says what it did through ShowNotification, in words that differ
	// per client kind, and that message reaches the overlay the same way every
	// other one does. "Copied the link. A remote client can not open it for
	// you." is exactly what an SSH user needs to read, and it is not repeated
	// here so it cannot drift from the one link_open.go writes.
	return m.OpenLink(m.crash.IssueURL())
}

// CrashNotice is what the overlay says under its detail block about the last
// thing the user pressed, or "".
func (m *OS) CrashNotice() string {
	if m == nil || m.crash == nil {
		return ""
	}
	return m.crashNotice
}

// handleCrashKey answers a key while the crash overlay is up, and reports
// whether it consumed it.
//
// It consumes every key. The overlay covers the screen, so a key it let through
// would land in a pane the user cannot see, which is how a stray q ends up
// closing something. The three it acts on are the three the footer names.
func (m *OS) handleCrashKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if m == nil || m.crash == nil {
		return nil, false
	}
	switch msg.String() {
	case "c":
		return m.CopyCrashReport(), true
	case "g":
		return m.OpenCrashIssue(), true
	case "esc", "q", "enter":
		m.DismissCrash()
		return nil, true
	}
	return nil, true
}

// crashLogPath is the file the current report was written to, or "" when there
// is none. It exists so the log line can name the file without the caller
// reaching into the report.
func (m *OS) crashLogPath() string {
	if m == nil || m.crash == nil {
		return ""
	}
	return m.crash.LogPath
}

// hideGraphicsForCrash takes kitty and sixel images off the screen while the
// crash overlay is up.
//
// An image is drawn by the host terminal in its own pass, not into tuios' frame
// buffer, so a placement left standing paints straight over the overlay. Every
// other full-screen overlay hides them for the same reason (see
// flushGraphicsForView), but that function is on the render path this overlay
// exists to replace, so the call is repeated here rather than reached through
// it. Behind a recover, because it is the one model read the crash screen makes
// and it is a subsystem that has its own ways to fail.
func (m *OS) hideGraphicsForCrash() {
	defer func() { _ = recover() }()
	if m.KittyPassthrough != nil {
		m.KittyPassthrough.SetOverlayActive(true)
	}
}
