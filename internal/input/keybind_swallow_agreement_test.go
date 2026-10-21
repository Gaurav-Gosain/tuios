package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The keybind report tells users which keys never reach the program in their
// pane, and it works that out from its own copy of two predicates the input
// path owns, because config cannot import input. A report that disagrees with
// the router is worse than no report: it is a confident wrong answer about
// whether the user's editor is going to keep working.
//
// So the agreement is checked against what the router actually does with the
// key rather than against the predicates directly. handleTerminalModeBinds is
// the function terminal mode consults before forwarding anything, and its
// return value is exactly "this key does not reach the pane".
func TestSwallowSetMatchesTheInputPath(t *testing.T) {
	// A spread that puts both predicates either side of their boundary: an
	// action that survives into terminal mode on a reserved chord, the same
	// action on a bare key, and an action that never survives on either.
	o := osWithBindings(t, func(kb *config.KeybindingsConfig) {
		kb.Workspaces["switch_workspace_1"] = []string{"alt+1", "f1"}
		kb.Workspaces["switch_workspace_2"] = []string{"ctrl+alt+2"}
		kb.WindowManagement["next_session"] = []string{"alt+shift+n"}
		// A window verb on a reserved chord: reserved, but not terminal-safe,
		// so it must reach the shell.
		kb.WindowManagement["close_window"] = []string{"alt+q"}
	})

	report := o.KeybindRegistry.Report(config.PaneFacts{})
	// The report lists a key once, in the spelling of the first action that
	// binds it, so the two sides are compared in canonical form: the default
	// send_to_workspace_2 also holds ctrl+alt+2, spelt ctrl+opt+2 on macOS.
	swallowed := map[string]bool{}
	for _, s := range report.Swallowed {
		swallowed[config.CanonicalKey(s.Key)] = true
	}

	cases := []struct {
		key string
		msg tea.KeyPressMsg
	}{
		{"alt+1", tea.KeyPressMsg{Code: '1', Mod: tea.ModAlt}},
		{"ctrl+alt+2", tea.KeyPressMsg{Code: '2', Mod: tea.ModCtrl | tea.ModAlt}},
		{"alt+shift+n", tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt | tea.ModShift}},
		// Bound to a workspace switch, but with no reserved modifier, so the
		// router leaves it to the shell.
		{"f1", tea.KeyPressMsg{Code: tea.KeyF1}},
		// Reserved chord, but a window verb, so the router leaves it alone.
		{"alt+q", tea.KeyPressMsg{Code: 'q', Mod: tea.ModAlt}},
		// Plain typing, which must always reach the pane.
		{"j", tea.KeyPressMsg{Code: 'j', Text: "j"}},
		{"1", tea.KeyPressMsg{Code: '1', Text: "1"}},
	}

	for _, tc := range cases {
		// A fresh model per case: dispatching an action mutates state, and a
		// workspace switch changes what the next dispatch does.
		fresh := osWithBindings(t, func(kb *config.KeybindingsConfig) {
			kb.Workspaces["switch_workspace_1"] = []string{"alt+1", "f1"}
			kb.Workspaces["switch_workspace_2"] = []string{"ctrl+alt+2"}
			kb.WindowManagement["next_session"] = []string{"alt+shift+n"}
			kb.WindowManagement["close_window"] = []string{"alt+q"}
		})
		router := handleTerminalModeBinds(tc.msg, fresh)
		if got := swallowed[config.CanonicalKey(tc.key)]; got != router {
			t.Errorf("key %q: the report says the pane loses it (%v), the router says (%v)",
				tc.key, got, router)
		}
	}
}

// Every key the report calls built-in must really be one the input path spells
// inline rather than one it could have read from the config. A key that drifted
// into the terminal_mode table would be reported twice, once with the wrong
// origin.
func TestBuiltInSwallowsAreNotConfigurable(t *testing.T) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)
	for _, s := range registry.TerminalModeSwallowed() {
		if s.Origin != "built-in" || s.Action == "leader" {
			continue
		}
		if action := registry.GetTerminalModeAction(s.Key); action != "" {
			t.Errorf("%s is reported as built-in but the terminal_mode table binds it to %q",
				s.Key, action)
		}
	}
}

// The leader is the one key the report must always list, whatever it is bound
// to: terminal mode takes it before any of the other checks run.
func TestRebindingTheLeaderMovesWhatThePaneLoses(t *testing.T) {
	for _, leader := range []string{"ctrl+b", "ctrl+a", "ctrl+g"} {
		o := osWithBindings(t, func(kb *config.KeybindingsConfig) { kb.LeaderKey = leader })
		var found bool
		for _, s := range o.KeybindRegistry.Report(config.PaneFacts{}).Swallowed {
			if strings.EqualFold(s.Key, leader) {
				found = true
			}
		}
		if !found {
			t.Errorf("leader %q must be reported as never reaching the pane", leader)
		}
	}
}

// The report's own claim about a key, as the recorder states it, has to agree
// with the swallow list it came from.
func TestFateAgreesWithTheSwallowList(t *testing.T) {
	o := osWithBindings(t, func(kb *config.KeybindingsConfig) {
		kb.TerminalMode["terminal_next_window"] = []string{"alt+n"}
	})
	registry := o.KeybindRegistry
	facts := config.PaneFacts{}

	if fate := registry.Fate("alt+n", facts); !fate.SwallowedInTerminal {
		t.Error("a terminal_mode binding never reaches the pane and Fate must say so")
	}
	if fate := registry.Fate("ctrl+alt+f12", facts); fate.SwallowedInTerminal {
		t.Error("an unbound key is forwarded; Fate must not claim otherwise")
	}
}

// Windows reports AltGr as Ctrl+Alt, so a character typed with AltGr arrives as
// a Ctrl+Alt chord carrying its text and the US key at its position. Read by
// position, UK AltGr+4 ("€") was ctrl+alt+4 and moved the pane to workspace 4.
// Those keys are typing and must reach the pane; the chord pressed with no
// text still moves it.
func TestAltGrTextReachesThePane(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want bool
	}{
		{"UK AltGr+4 types €", tea.KeyPressMsg{Code: '€', BaseCode: '4', Text: "€", Mod: tea.ModCtrl | tea.ModAlt}, false},
		{"German AltGr+2 types ²", tea.KeyPressMsg{Code: '²', BaseCode: '2', Text: "²", Mod: tea.ModCtrl | tea.ModAlt}, false},
		{"Swiss AltGr+5 types §", tea.KeyPressMsg{Code: '§', BaseCode: '5', Text: "§", Mod: tea.ModCtrl | tea.ModAlt}, false},
		{"ctrl+alt+4 with no text", tea.KeyPressMsg{Code: '4', Mod: tea.ModCtrl | tea.ModAlt}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := osWithBindings(t, func(*config.KeybindingsConfig) {})
			if got := handleTerminalModeBinds(tc.msg, o); got != tc.want {
				t.Errorf("handleTerminalModeBinds took the key: %v, want %v", got, tc.want)
			}
		})
	}
}
