package input

import (
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

// The reported bug: on macOS the Option key composes a character instead of
// setting Alt, so the alt+n bound to terminal_next_window never fires and the
// composed character is typed into the pane instead.
//
// Each case below is a real encoding of an Option chord. Which one a user gets
// depends on their terminal and its settings, and all of them have to reach
// the same action. main resolves through the main section instead of the
// terminal-mode one. An empty want means the event is not an Option chord at
// all, which macOptionChord must refuse.
func TestMacOptionChordsReachTheirBinding(t *testing.T) {
	onDarwin(t)
	registry := config.NewKeybindRegistry(config.DefaultConfig())

	for _, tc := range []struct {
		what string
		msg  tea.KeyPressMsg
		main bool
		want string
	}{
		// Option as Meta / Esc+: the terminal sends ESC n and nothing is composed.
		{"esc-prefixed meta", tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt}, false, "terminal_next_window"},
		// No Kitty protocol and no Option-as-Meta: the dead key spills its
		// tilde with no modifier at all.
		{"composed glyph, bare", tea.KeyPressMsg{Code: '˜', Text: "˜"}, false, "terminal_next_window"},
		// Kitty protocol, no alternate-key reporting: Ghostty and kitty set the
		// Alt bit but still report the composed codepoint.
		{"composed glyph with alt", tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt}, false, "terminal_next_window"},
		// Kitty protocol with alternate-key reporting: the base-layout code says
		// which key it really was.
		{"composed glyph with base code", tea.KeyPressMsg{Code: '˜', BaseCode: 'n', Mod: tea.ModAlt}, false, "terminal_next_window"},
		// Num Lock is on by default on most keyboards and the Kitty protocol
		// reports it in the modifier field.
		{"composed glyph with a lock modifier", tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt | tea.ModNumLock}, false, "terminal_next_window"},
		{"option+p composes pi", tea.KeyPressMsg{Code: 'π', Text: "π"}, false, "terminal_prev_window"},
		// Option+Shift+n composes the same tilde as the Option+n dead key. When
		// the terminal reports the Shift bit they are still tellable apart, and
		// the two are bound to different things.
		{"option+shift+n", tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt | tea.ModShift}, true, "next_session"},
		// An Option chord only stands in for a binding when Option is the only
		// modifier involved. Ctrl+Alt+n is a different chord and macOS composes
		// nothing for it.
		{"ctrl+alt with a glyph", tea.KeyPressMsg{Code: '˜', Mod: tea.ModAlt | tea.ModCtrl}, false, ""},
		{"super with a glyph", tea.KeyPressMsg{Code: '˜', Mod: tea.ModSuper}, false, ""},
	} {
		if tc.want == "" {
			if chord, ok := macOptionChord(tc.msg); ok {
				t.Errorf("%s (%q) was read as %q, want no chord", tc.what, tc.msg.String(), chord)
			}
			continue
		}
		lookup := registry.GetTerminalModeAction
		if tc.main {
			lookup = registry.GetAction
		}
		if got := lookupAction(tc.msg, lookup); got != tc.want {
			t.Errorf("%s (%q): resolved to %q, want %q", tc.what, tc.msg.String(), got, tc.want)
		}
	}
}

// Off darwin the same glyphs are ordinary characters that belong to the shell.
func TestComposedGlyphsAreNotChordsOffDarwin(t *testing.T) {
	prev := darwinHost
	darwinHost = false
	t.Cleanup(func() { darwinHost = prev })
	// The defaults and the key normalizer read the config package's own
	// platform, not this one. On a macOS machine the registry would otherwise
	// still be built from the macOS defaults, whose opt+ bindings expand to
	// exactly the glyphs this test says mean nothing here.
	t.Cleanup(config.ForceMacOSHost(false))

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
