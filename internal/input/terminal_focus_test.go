package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// altArrow builds the event a terminal sends for an alt-modified arrow.
func altArrow(dir string) tea.KeyPressMsg {
	code := map[string]rune{
		"left":  tea.KeyLeft,
		"right": tea.KeyRight,
		"up":    tea.KeyUp,
		"down":  tea.KeyDown,
	}[dir]
	return tea.KeyPressMsg{Code: code, Mod: tea.ModAlt}
}

// focusLayout is the fixture the directional rule is stated against: one tall
// pane on the left, two stacked on the right, so left/right is a move between
// panes that do not line up and up/down only exists on one side.
//
//	+--------+--------+
//	|        |   b    |
//	|   a    +--------+
//	|        |   c    |
//	+--------+--------+
func focusLayout(t *testing.T, override func(*config.KeybindingsConfig)) *app.OS {
	t.Helper()
	o := osWithBindings(t, override)
	o.Width, o.Height = 120, 40
	ws := o.CurrentWorkspace
	o.Windows = []*terminal.Window{
		{ID: "a", X: 0, Y: 0, Width: 60, Height: 40, Workspace: ws},
		{ID: "b", X: 60, Y: 0, Width: 60, Height: 20, Workspace: ws},
		{ID: "c", X: 60, Y: 20, Width: 60, Height: 20, Workspace: ws},
	}
	o.Mode = app.TerminalMode
	return o
}

func focusedID(o *app.OS) string {
	if w := o.GetFocusedWindow(); w != nil {
		return w.ID
	}
	return ""
}

// TestAltArrowsMoveFocusByTheDocumentedRule pins the rule the docs state: the
// nearest pane whose facing edge lies that way and whose span overlaps this
// one's, ties to the earlier pane, and no wrap at the edge.
func TestAltArrowsMoveFocusByTheDocumentedRule(t *testing.T) {
	tests := []struct {
		name  string
		from  int
		dir   string
		want  string
		moved bool
	}{
		// Panes that do not line up: from the tall left pane, both right-hand
		// panes overlap its span at the same distance, so the earlier one wins.
		{name: "into a stack picks the earlier pane", from: 0, dir: "right", want: "b", moved: true},
		{name: "back out of the stack", from: 1, dir: "left", want: "a", moved: true},
		{name: "down the stack", from: 1, dir: "down", want: "c", moved: true},
		{name: "up the stack", from: 2, dir: "up", want: "b", moved: true},
		// Edges: nothing that way, so focus stays. No wrap.
		{name: "left edge does not wrap", from: 0, dir: "left", want: "a"},
		{name: "right edge does not wrap", from: 1, dir: "right", want: "b"},
		{name: "top edge does not wrap", from: 1, dir: "up", want: "b"},
		{name: "bottom edge does not wrap", from: 2, dir: "down", want: "c"},
		// The tall pane spans both rows, so neither up nor down leaves it.
		{name: "no pane above the full-height pane", from: 0, dir: "up", want: "a"},
		{name: "no pane below the full-height pane", from: 0, dir: "down", want: "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := focusLayout(t, func(*config.KeybindingsConfig) {})
			o.FocusedWindow = tt.from
			start := focusedID(o)

			o, _ = HandleKeyPress(altArrow(tt.dir), o)

			if got := focusedID(o); got != tt.want {
				t.Errorf("alt+%s from %s focused %q, want %q", tt.dir, start, got, tt.want)
			}
			if !tt.moved && focusedID(o) != start {
				t.Errorf("alt+%s wrapped off the edge of the layout", tt.dir)
			}
		})
	}
}

// TestDirectionalTieGoesToTheEarlierPane states the tie-break as a rule rather
// than an accident of the fixture: same fixture, the stack reordered, and the
// answer follows the order.
func TestDirectionalTieGoesToTheEarlierPane(t *testing.T) {
	o := focusLayout(t, func(*config.KeybindingsConfig) {})
	o.Windows[1], o.Windows[2] = o.Windows[2], o.Windows[1] // c now precedes b
	o.FocusedWindow = 0

	o, _ = HandleKeyPress(altArrow("right"), o)

	if got := focusedID(o); got != "c" {
		t.Errorf("alt+right focused %q, want c: tied panes go to the earlier one", got)
	}
}

// TestEachAltArrowIsDisabledIndependently is the readline bargain. A user who
// wants word movement back unbinds one direction, and that key reaches the shell
// byte for byte while the other three still move focus.
func TestEachAltArrowIsDisabledIndependently(t *testing.T) {
	for _, dir := range []string{"left", "right", "up", "down"} {
		t.Run(dir, func(t *testing.T) {
			action := "terminal_focus_" + dir

			// What the shell gets when nothing intercepts the key.
			cfg := config.DefaultConfig()
			cfg.Keybindings.TerminalMode[action] = []string{}
			o, pty := osWithFocusedPane(t, cfg, app.TerminalMode)
			o.Windows = append(o.Windows, &terminal.Window{
				ID: "other", X: 90, Y: 0, Width: 60, Height: 24, Workspace: o.CurrentWorkspace,
			})
			before := focusedID(o)

			o, _ = HandleKeyPress(altArrow(dir), o)

			if len(pty.got) == 0 {
				t.Fatalf("unbinding %s did not hand alt+%s to the shell", action, dir)
			}
			if got := focusedID(o); got != before {
				t.Errorf("unbound alt+%s still moved focus to %q", dir, got)
			}

			// The bound key is intercepted and types nothing.
			o2, pty2 := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
			o2, _ = HandleKeyPress(altArrow(dir), o2)
			if len(pty2.got) != 0 {
				t.Errorf("bound alt+%s typed %q into the shell", dir, pty2.got)
			}
			_ = o2
		})
	}
}

// TestUnboundAltArrowReachesTheShellUnchanged pins the exact bytes, so an
// unbind hands the shell the same sequence it would see with no multiplexer in
// the way rather than something merely non-empty. CSI 1;3 <final> is the xterm
// spelling of an alt-modified arrow, and it is what readline, fish and zsh read
// as word-wise cursor movement.
func TestUnboundAltArrowReachesTheShellUnchanged(t *testing.T) {
	for dir, want := range map[string]string{
		"left":  "\x1b[1;3D",
		"right": "\x1b[1;3C",
		"up":    "\x1b[1;3A",
		"down":  "\x1b[1;3B",
	} {
		cfg := config.DefaultConfig()
		cfg.Keybindings.TerminalMode["terminal_focus_"+dir] = []string{}
		o, pty := osWithFocusedPane(t, cfg, app.TerminalMode)

		if _, _ = HandleKeyPress(altArrow(dir), o); string(pty.got) != want {
			t.Errorf("unbound alt+%s sent %q, want %q", dir, pty.got, want)
		}
	}
}

// TestAltArrowsResolveInAConfigPredatingThem covers the hazard that once froze
// the rail: a config file written before a section grew an action left that
// action dead.
func TestAltArrowsResolveInAConfigPredatingThem(t *testing.T) {
	cfg := legacyConfig(t, "[keybindings]\nleader_key = \"ctrl+b\"\n\n"+
		"[keybindings.terminal_mode]\nterminal_next_window = [\"alt+n\"]\n")
	r := config.NewKeybindRegistry(cfg)

	for dir, want := range map[string]string{
		"left":  "terminal_focus_left",
		"right": "terminal_focus_right",
		"up":    "terminal_focus_up",
		"down":  "terminal_focus_down",
	} {
		if got := r.GetTerminalModeAction("alt+" + dir); got != want {
			t.Errorf("alt+%s resolved to %q, want %q", dir, got, want)
		}
	}
	// The user's own binding survives the fill.
	if got := r.GetTerminalModeAction("alt+n"); got != "terminal_next_window" {
		t.Errorf("alt+n resolved to %q, want terminal_next_window", got)
	}
}

// TestAltArrowsNavigateTheScrollingLayoutsColumns covers the branch that used
// to be a hardcoded alt+left/right block ahead of the registry. Moving it behind
// the binding is what makes the key rebindable there; the key still means "one
// pane that way", which in a strip of columns is the next column.
func TestAltArrowsNavigateTheScrollingLayoutsColumns(t *testing.T) {
	o := focusLayout(t, func(*config.KeybindingsConfig) {})
	o.EnableScrollingLayout()
	o.FocusedWindow = 0

	o, _ = HandleKeyPress(altArrow("right"), o)
	if focusedID(o) == "a" {
		t.Error("alt+right did not move off the first column")
	}
	moved := focusedID(o)

	o, _ = HandleKeyPress(altArrow("left"), o)
	if got := focusedID(o); got == moved {
		t.Errorf("alt+left did not come back from %q", moved)
	}
}

// TestAltArrowsWorkInWindowModeToo records that these are the same binds either
// side of the mode split, which is what the terminal_mode section already does
// for window cycling.
func TestAltArrowsWorkInWindowModeToo(t *testing.T) {
	o := focusLayout(t, func(*config.KeybindingsConfig) {})
	o.Mode = app.WindowManagementMode
	o.FocusedWindow = 0

	o, _ = HandleKeyPress(altArrow("right"), o)

	if got := focusedID(o); got != "b" {
		t.Errorf("alt+right in window mode focused %q, want b", got)
	}
}
