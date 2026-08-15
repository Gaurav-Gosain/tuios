package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// onDarwin puts the macOS-only key paths under test on whatever machine runs
// them. The glyph tables are compiled in on every platform; only the guard that
// consults them is platform-dependent.
func onDarwin(t *testing.T) {
	t.Helper()
	prev := darwinHost
	darwinHost = true
	t.Cleanup(func() { darwinHost = prev })
}

// twoWindowOS is an OS in terminal mode with two panes, focused on the first.
func twoWindowOS(t *testing.T) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = 160, 40
	o.EffectiveWidth, o.EffectiveHeight = 160, 40
	o.Windows = []*terminal.Window{
		{ID: "a", CustomName: "one", X: 0, Y: 0, Width: 60, Height: 30, Workspace: 1},
		{ID: "b", CustomName: "two", X: 60, Y: 0, Width: 60, Height: 30, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	o.Mode = app.TerminalMode
	return o
}

// The reported bug: on macOS the Option key composes a character instead of
// setting Alt, so the alt+n bound to terminal_next_window never fires and the
// composed character is typed into the pane instead.
//
// Each case below is a real encoding of Option+n or Option+p. Which one a user
// gets depends on their terminal and its settings, and all of them have to reach
// the same action.
func TestMacOptionChordsReachTheirBinding(t *testing.T) {
	onDarwin(t)

	for _, tc := range []struct {
		what string
		msg  tea.KeyPressMsg
		want string
	}{
		{
			// Option as Meta / Esc+: the terminal sends ESC n and nothing is composed.
			what: "esc-prefixed meta",
			msg:  tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt},
			want: "terminal_next_window",
		},
		{
			// No Kitty protocol and no Option-as-Meta: the dead key spills its
			// tilde with no modifier at all.
			what: "composed glyph, bare",
			msg:  tea.KeyPressMsg{Code: '˜', Text: "˜"},
			want: "terminal_next_window",
		},
		{
			// Kitty protocol, no alternate-key reporting: Ghostty and kitty set
			// the Alt bit but still report the composed codepoint.
			what: "composed glyph with alt",
			msg:  tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt},
			want: "terminal_next_window",
		},
		{
			// Kitty protocol with alternate-key reporting: the base-layout code
			// says which key it really was.
			what: "composed glyph with base code",
			msg:  tea.KeyPressMsg{Code: '˜', BaseCode: 'n', Mod: tea.ModAlt},
			want: "terminal_next_window",
		},
		{
			// Num Lock is on by default on most keyboards and the Kitty protocol
			// reports it in the modifier field.
			what: "composed glyph with a lock modifier",
			msg:  tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt | tea.ModNumLock},
			want: "terminal_next_window",
		},
		{
			what: "option+p composes pi",
			msg:  tea.KeyPressMsg{Code: 'π', Text: "π"},
			want: "terminal_prev_window",
		},
	} {
		registry := config.NewKeybindRegistry(config.DefaultConfig())
		if got := lookupAction(tc.msg, registry.GetTerminalModeAction); got != tc.want {
			t.Errorf("%s (%q): resolved to %q, want %q", tc.what, tc.msg.String(), got, tc.want)
		}
	}
}

// Option+Shift+n composes the same tilde as the Option+n dead key. When the
// terminal reports the Shift bit they are still tellable apart, and the two are
// bound to different things.
func TestShiftedOptionChordPrefersTheShiftedBinding(t *testing.T) {
	onDarwin(t)
	registry := config.NewKeybindRegistry(config.DefaultConfig())

	shifted := tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt | tea.ModShift}
	if got := lookupAction(shifted, registry.GetAction); got != "next_session" {
		t.Errorf("opt+shift+n resolved to %q, want next_session", got)
	}
	// Without the Shift bit there is nothing to tell them apart, and the
	// unshifted reading is the one that keeps working.
	bare := tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt}
	if got := lookupAction(bare, registry.GetTerminalModeAction); got != "terminal_next_window" {
		t.Errorf("opt+n resolved to %q, want terminal_next_window", got)
	}
}

// The chord has to move the focus through the real terminal-mode handler, not
// just resolve to an action name, and it must not be typed into the pane.
func TestMacOptionChordSwitchesPaneInTerminalMode(t *testing.T) {
	onDarwin(t)

	for _, msg := range []tea.KeyPressMsg{
		{Code: '˜', Text: "˜"},
		{Code: '˜', Mod: tea.ModAlt},
		{Code: 'n', Mod: tea.ModAlt},
	} {
		o := twoWindowOS(t)
		if _, _ = HandleTerminalModeKey(msg, o); o.FocusedWindow != 1 {
			t.Errorf("%q left the focus on window %d, want the next one", msg.String(), o.FocusedWindow)
		}
	}
}

// Off darwin the same glyphs are ordinary characters that belong to the shell.
func TestComposedGlyphsAreNotChordsOffDarwin(t *testing.T) {
	prev := darwinHost
	darwinHost = false
	t.Cleanup(func() { darwinHost = prev })

	registry := config.NewKeybindRegistry(config.DefaultConfig())
	for _, msg := range []tea.KeyPressMsg{
		{Code: '˜', Text: "˜"},
		{Code: 'π', Text: "π"},
		{Code: '¬', Text: "¬"},
	} {
		if got := lookupAction(msg, registry.GetTerminalModeAction); got != "" {
			t.Errorf("%q resolved to %q off darwin, want no action", msg.String(), got)
		}
	}
}

// An Option chord only stands in for a binding when Option is the only modifier
// involved. Ctrl+Alt+n is a different chord and macOS composes nothing for it.
func TestMacOptionChordIgnoresOtherModifiers(t *testing.T) {
	onDarwin(t)

	for _, msg := range []tea.KeyPressMsg{
		{Code: '˜', Mod: tea.ModAlt | tea.ModCtrl},
		{Code: '˜', Mod: tea.ModSuper},
	} {
		if chord, ok := macOptionChord(msg); ok {
			t.Errorf("%q was read as %q, want no chord", msg.String(), chord)
		}
	}
}

// The letter tables are only useful if they agree with what macOS actually
// composes, and every glyph must map back to exactly one chord.
func TestMacOptionGlyphsAreUnambiguous(t *testing.T) {
	for glyph, want := range map[rune]string{
		'˜': "alt+n", 'π': "alt+p", '¬': "alt+l", '˙': "alt+h",
		'∆': "alt+j", '˚': "alt+k", 'ø': "alt+o", 'å': "alt+a",
		'¡': "alt+1", 'ª': "alt+9", '⇥': "alt+tab",
		'Å': "alt+shift+a", 'Ø': "alt+shift+o",
	} {
		got, ok := config.MacOSOptionChord(glyph)
		if !ok || got != want {
			t.Errorf("%c resolved to %q (found %v), want %q", glyph, got, ok, want)
		}
	}
}

// A chord that only worked because tuios translated a composed glyph proves the
// Option key is not being sent as Alt, and the user is told once what to turn
// on. Once is the point: it is advice, not an error.
func TestComposedChordAdvisesOnceWhatToTurnOn(t *testing.T) {
	onDarwin(t)
	o := twoWindowOS(t)

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: '˜', Text: "˜"}, o)
	before := len(o.Notifications)
	if before == 0 {
		t.Fatal("a composed Option chord said nothing about why it had to be translated")
	}
	if !strings.Contains(o.Notifications[before-1].Message, "Option") {
		t.Errorf("the advice does not mention Option: %q", o.Notifications[before-1].Message)
	}

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'π', Text: "π"}, o)
	if len(o.Notifications) != before {
		t.Errorf("the advice repeated itself (%d notifications, want %d)", len(o.Notifications), before)
	}
}

// An Option glyph nobody bound anything to is just a character, and advising
// about it would be noise.
func TestUnboundComposedGlyphSaysNothing(t *testing.T) {
	onDarwin(t)
	o := twoWindowOS(t)

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'ß', Text: "ß"}, o)
	if len(o.Notifications) != 0 {
		t.Errorf("an unbound glyph raised %d notifications", len(o.Notifications))
	}
}

// The advice has to name the terminal the user is actually in and the chord that
// failed, or it is not advice.
func TestAdviceNamesTheHostTerminal(t *testing.T) {
	for host, want := range map[config.HostTerminal]string{
		config.HostAppleTerminal: "Use Option as Meta Key",
		config.HostITerm2:        "Esc+",
		config.HostGhostty:       "macos-option-as-alt",
		config.HostKitty:         "macos_option_as_alt",
		config.HostWezTerm:       "send_composed_key_when_left_alt_is_pressed",
		config.HostAlacritty:     "option_as_alt",
		config.HostVSCode:        "macOptionIsMeta",
		config.HostUnknown:       "Option as Meta/Alt",
	} {
		advice := config.MacOptionAdvice(host, "alt+n")
		if !strings.Contains(advice, want) {
			t.Errorf("%s advice %q does not mention %q", host, advice, want)
		}
		if !strings.Contains(advice, "alt+n") {
			t.Errorf("%s advice does not name the chord that failed: %q", host, advice)
		}
	}
}
