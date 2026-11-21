package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *OS) keyboardEnhancements() tea.KeyboardEnhancements {
	if m.quitting {
		return m.lastKeyboardEnhancements
	}
	enhancements := m.wantedKeyboardEnhancements()
	m.lastKeyboardEnhancements = enhancements
	return enhancements
}

// QuitCmd is the command that ends the client, and the one every quit goes
// through. It also stops the view from changing what it asks the terminal for.
//
// Bubble Tea sends a query for the new flags (ESC [ ? u) with the next frame
// whenever they change, and closing the quit menu changes them. A quit starts
// by cancelling the input reader, and only then stops the renderer, whose timer
// can still flush a frame in between. The host answers the query at once, and
// nobody reads the answer, so the shell the client returns to prints the end of
// it as typed text: "5u". The last frame before the quit carries no change, so
// it carries no query.
//
// Learn mode never quits, so it keeps its flags moving.
func (m *OS) QuitCmd() tea.Cmd {
	if !m.LearnMode {
		m.quitting = true
	}
	return tea.Quit
}

func (m *OS) wantedKeyboardEnhancements() tea.KeyboardEnhancements {
	enhancements := tea.KeyboardEnhancements{
		ReportAlternateKeys: true,
	}
	paneFlags := m.PaneKeyboardFlags()
	if paneFlags&ansi.KittyReportEventTypes != 0 {
		enhancements.ReportEventTypes = true
		if paneFlags&ansi.KittyReportAllKeysAsEscapeCodes != 0 {
			enhancements.ReportAllKeysAsEscapeCodes = true
			enhancements.ReportAssociatedText = true
		}
	}
	if m.KeysGoToBindings() {
		enhancements.ReportAllKeysAsEscapeCodes = true
		enhancements.ReportAssociatedText = true
	}
	if !m.HoldModeAvailable() {
		return enhancements
	}
	enhancements.ReportEventTypes = true
	if m.HoldModeNeedsAllKeys() {
		enhancements.ReportAllKeysAsEscapeCodes = true
		enhancements.ReportAssociatedText = true
	}
	return enhancements
}

// KeysGoToBindings reports whether the next key is matched against tuios
// bindings rather than typed into the focused pane. It is true everywhere but
// terminal mode with nothing in front of the pane: no prefix pending or about to
// repeat, no rail focus, no overlay and no copy mode.
func (m *OS) KeysGoToBindings() bool {
	if m.Mode != TerminalMode {
		return true
	}
	if m.PrefixActive || m.SubMenu != SubMenuNone || m.Menu != "" || m.PrefixRepeatLive() {
		return true
	}
	if m.OverlayOwnsKeyboard() {
		return true
	}
	window := m.GetFocusedWindow()
	return window == nil || window.InCopyMode()
}

// OverlayOwnsKeyboard reports whether something in front of the panes takes
// the keys: the focused rail, an overlay, a dialog, an editor or a gesture
// over the screen. A paste then never reaches a pane, since none of them
// would get a key typed in its place.
func (m *OS) OverlayOwnsKeyboard() bool {
	return m.SidebarFocused || m.PanelOwnsKeyboard() || m.ContextMenuActive() || m.PathPickerOpen() ||
		m.ReviewOpen() || m.Renaming() || m.CaptureActive()
}

// PanelOwnsKeyboard reports whether a panel is open in front of the panes and
// the rail: any overlay, the scrollback browser, the tape panels or the cache
// stats. A focused rail gives its keys to such a panel while it is open and
// takes them back when it closes, since SidebarFocused is left as it was.
//
// The rail used to name the panels it gave way to, one by one, and every panel
// left off the list opened over the focused rail with no keys: ctrl+b / drew
// the navigator, and j and k still moved the rail's cursor behind it.
func (m *OS) PanelOwnsKeyboard() bool {
	return m.AnyOverlayOpen() || m.ShowScrollbackBrowser || m.ShowTapeReview ||
		m.ShowTapeManager || m.ShowCacheStats
}

// PaneKeyboardFlags returns the kitty keyboard protocol flags the focused pane
// has in effect, or zero when no pane is focused or none were pushed.
func (m *OS) PaneKeyboardFlags() int {
	window := m.GetFocusedWindow()
	if window == nil || window.Terminal == nil {
		return 0
	}
	return window.Terminal.KittyKeyboardFlags()
}

// HostReportsReleases reports whether the host terminal is sending key
// releases now: it answered the flag query with event types in effect. A host
// that never answered sends none, since releases exist only in the kitty
// keyboard protocol and every terminal that speaks it answers the query.
//
// The answer arrives in the same stream as the keys, after the keys the host
// sent under the old flags, so it is accurate for the key being read.
func (m *OS) HostReportsReleases() bool {
	return m.KeyboardFlags&ansi.KittyReportEventTypes != 0
}

// NoteKeyboardEnhancements records what the host answered the enhancement query
// with.
func (m *OS) NoteKeyboardEnhancements(msg tea.KeyboardEnhancementsMsg) {
	m.KeyboardFlags = msg.Flags
	m.KeyboardEnhancementsEnabled = msg.SupportsKeyDisambiguation()
	if msg.Flags&ansi.KittyReportAllKeysAsEscapeCodes != 0 {
		m.hostGrantedAllKeys = true
	}
}

// AllKeysPending reports whether tuios has asked the host for every key as an
// escape code and the host has not yet said it switched.
//
// Bubble Tea queries the flags (CSI ? u) after every change, and the answer
// marks the moment the change took effect. A key typed right after the leader
// can beat it, over ssh often, and then a "ш" arrives as bare text with no
// base-layout key behind it. The caller drops such a key rather than type it
// into the pane. It is only ever true for a host that has granted report-all
// keys before, so a terminal that never answers, or never grants the flag,
// never has a key dropped.
func (m *OS) AllKeysPending() bool {
	return m.hostGrantedAllKeys && m.KeyboardFlags&ansi.KittyReportAllKeysAsEscapeCodes == 0
}
