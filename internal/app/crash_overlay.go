package app

import (
	tea "charm.land/bubbletea/v2"
)

const maxRecentActions = 5

// NoteAction records that a keybind action ran. The action dispatcher calls it
// for every action it dispatches, which covers keybindings, prefix chords and
// context menu rows, since all three go through the one dispatcher.
func (m *OS) NoteAction(action string) {
	if m == nil || action == "" {
		return
	}
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
		_ = recover()
	}()

	report := NewCrashReport(where, panicValue, stack, m.crashFacts(where))
	WriteCrashLog(report)

	if m.crash == nil {
		m.crash = report
	}
	m.renderSkipped = false
}

// DismissCrash takes the crash overlay off the screen.
//
// It is always allowed, even for a panic in the render path, and that is safe
// rather than merely permitted: dismissing lets View try composeView again,
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

func (m *OS) crashLogPath() string {
	if m == nil || m.crash == nil {
		return ""
	}
	return m.crash.LogPath
}

func (m *OS) hideGraphicsForCrash() {
	defer func() { _ = recover() }()
	if m.KittyPassthrough != nil {
		m.KittyPassthrough.SetOverlayActive(true)
	}
}
