package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// keyboardEnhancements is what tuios asks the host terminal for through the
// Kitty keyboard protocol. Bubble Tea already requests key disambiguation on
// every view; these are the additions tuios has a use for.
//
// The request is made from the view because that is where Bubble Tea reads it:
// changing what is asked for re-runs the negotiation and the answer arrives as a
// [tea.KeyboardEnhancementsMsg]. Terminals that speak none of this ignore the
// sequence, so there is nothing to fall back from.
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

// wantedKeyboardEnhancements is what the focus and the mode call for now.
func (m *OS) wantedKeyboardEnhancements() tea.KeyboardEnhancements {
	enhancements := tea.KeyboardEnhancements{
		// Alternate-key reporting adds the PC-101 key behind whatever character
		// the layout produced. That is what lets a chord be recognised when the
		// OS composed something else out of it, which is the whole of the macOS
		// Option problem, and it fixes the same class of miss on any non-US
		// layout. It only ever adds subparameters to a report tuios already
		// parses.
		ReportAlternateKeys: true,
	}
	// A pane that pushed the event-type flag is asking to be told when a key
	// comes up, and tuios cannot pass on what it never receives: unless the host
	// is asked for releases too, the pane sees an endless press. That is fatal
	// for a compositor in a pane, whose Wayland clients hold the key down and let
	// xkb repeat it until a release arrives, and it is why the request tracks the
	// focused pane rather than being fixed at startup.
	paneFlags := m.PaneKeyboardFlags()
	if paneFlags&ansi.KittyReportEventTypes != 0 {
		enhancements.ReportEventTypes = true
		// A terminal only reports the release of a key it sends as an escape
		// code, so Enter and Tab and every plain character come up silently
		// unless all keys are asked for as well. Associated text comes with that
		// or the character the user typed is lost on its way to the pane. Both
		// ride on the pane's own request, so a session with no such pane focused
		// is left exactly as it was.
		if paneFlags&ansi.KittyReportAllKeysAsEscapeCodes != 0 {
			enhancements.ReportAllKeysAsEscapeCodes = true
			enhancements.ReportAssociatedText = true
		}
	}
	// Alternate-key reporting only adds the base key to a key the terminal
	// already sends as an escape code, and a plain letter is sent as text. So
	// with a Ukrainian layout the I key after the leader arrives as a bare "ш"
	// with nothing behind it, and no binding can answer. While tuios itself
	// reads the next key (window mode, a prefix, the rail, an overlay), every
	// key is asked for as an escape code, with its text alongside so a field
	// being typed into still gets the character. A pane with the keyboard is
	// left as it was: it gets text as text.
	if m.KeysGoToBindings() {
		enhancements.ReportAllKeysAsEscapeCodes = true
		enhancements.ReportAssociatedText = true
	}
	if !m.HoldModeAvailable() {
		return enhancements
	}
	// Release events are the only honest way to know a key is still down.
	enhancements.ReportEventTypes = true
	if m.HoldModeNeedsAllKeys() {
		// A modifier key is only reported as a key of its own in this mode. It
		// also stops text being sent as text, so associated-text reporting comes
		// with it: without that pair, composed and IME input would be lost on
		// its way to a pane.
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
	if m.PrefixActive || m.WorkspacePrefixActive || m.MinimizePrefixActive ||
		m.TilingPrefixActive || m.DebugPrefixActive || m.TapePrefixActive ||
		m.LayoutPrefixActive || m.TuiosPrefixActive || m.Menu != "" || m.PrefixRepeatLive() {
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
