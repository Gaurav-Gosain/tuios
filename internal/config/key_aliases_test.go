package config_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Issue #201: a key spelled with a modifier alias in config.toml has to match
// the key event a terminal sends, wherever the spelling enters. The leader was
// compared with its literal spelling, so leader_key = "opt+f12" never fired on
// the alt+f12 the terminal sends, and `keybinds explain alt+f12` said the key
// was free.

// forEachPlatform runs fn once as macOS and once as Linux, since the defaults,
// the normalizer and the validator all read the platform.
func forEachPlatform(t *testing.T, fn func(t *testing.T, mac bool)) {
	t.Helper()
	for _, mac := range []bool{true, false} {
		name := "linux"
		if mac {
			name = "macos"
		}
		t.Run(name, func(t *testing.T) {
			restore := config.ForceMacOSHost(mac)
			defer restore()
			fn(t, mac)
		})
	}
}

func TestCanonicalKey(t *testing.T) {
	cases := map[string]string{
		"opt+f12":           "alt+f12",
		"option+f12":        "alt+f12",
		"OPT+F12":           "alt+f12",
		" alt+f12 ":         "alt+f12",
		"cmd+f12":           "super+f12",
		"command+v":         "super+v",
		"control+b":         "ctrl+b",
		"shift+ctrl+x":      "ctrl+shift+x",
		"super+shift+opt+k": "alt+shift+super+k",
		"opt+alt+x":         "alt+x",
		"ctrl++":            "ctrl++",
		"+":                 "+",
		"M":                 "M",
		"m":                 "m",
		"é":                 "é",
		"Enter":             "enter",
		// Not a chord tuios can read: left alone for the validator to name.
		"foo+x": "foo+x",
		"ctrl+": "ctrl+",
	}
	for in, want := range cases {
		if got := config.CanonicalKey(in); got != want {
			t.Errorf("CanonicalKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLeaderAliasMatchesTheKeyEvent is the leader entry point.
//
// Negative control: have IsLeaderPress compare with strings.EqualFold and the
// opt, option and cmd rows fail.
func TestLeaderAliasMatchesTheKeyEvent(t *testing.T) {
	cases := []struct {
		leader string
		key    tea.Key
	}{
		{"opt+f12", tea.Key{Code: tea.KeyF12, Mod: tea.ModAlt}},
		{"option+f12", tea.Key{Code: tea.KeyF12, Mod: tea.ModAlt}},
		{"OPT+F12", tea.Key{Code: tea.KeyF12, Mod: tea.ModAlt}},
		{"alt+f12", tea.Key{Code: tea.KeyF12, Mod: tea.ModAlt}},
		{"cmd+f12", tea.Key{Code: tea.KeyF12, Mod: tea.ModSuper}},
		{"control+a", tea.Key{Code: 'a', Mod: tea.ModCtrl}},
		{"shift+ctrl+a", tea.Key{Code: 'a', Mod: tea.ModCtrl | tea.ModShift}},
		{"ctrl+b", tea.Key{Code: 'b', Mod: tea.ModCtrl}},
	}
	forEachPlatform(t, func(t *testing.T, _ bool) {
		for _, c := range cases {
			pressed := c.key.Keystroke()
			if !config.IsLeaderPress(pressed, c.leader) {
				t.Errorf("leader %q does not match the key event %q", c.leader, pressed)
			}
		}
		if config.IsLeaderPress("f12", "opt+f12") {
			t.Error("leader opt+f12 matches a bare f12")
		}
		if config.IsLeaderPress("ctrl+f12", "opt+f12") {
			t.Error("leader opt+f12 matches ctrl+f12")
		}
		if !config.IsLeaderPress("ctrl+b", "") {
			t.Error("an empty leader does not mean the default ctrl+b")
		}
		if config.IsLeaderPress("b", "B") {
			t.Error("leader B matches b")
		}
	})
}

// TestAliasBindingsMatchInEveryTable covers the binding tables: window mode,
// a prefix table, terminal_mode and global, each looked up with the key event
// a terminal sends.
func TestAliasBindingsMatchInEveryTable(t *testing.T) {
	forEachPlatform(t, func(t *testing.T, mac bool) {
		cfg := config.DefaultConfig()
		kb := &cfg.Keybindings
		kb.WindowManagement["new_window"] = []string{"cmd+f5"}
		kb.PrefixMode["prefix_new_window"] = []string{"shift+ctrl+f6"}
		kb.TerminalMode["terminal_exit_mode"] = []string{"control+f7"}
		kb.Global["command_palette"] = []string{"command+f8"}
		if mac {
			kb.TerminalMode["terminal_exit_mode"] = append(kb.TerminalMode["terminal_exit_mode"], "option+f9")
		}
		reg := config.NewKeybindRegistry(cfg)

		check := func(table, got, want, pressed string) {
			t.Helper()
			if got != want {
				t.Errorf("%s: key event %q runs %q, want %q", table, pressed, got, want)
			}
		}
		check("window_management", reg.GetAction("super+f5"), "new_window", "super+f5")
		check("prefix_mode", reg.GetPrefixAction("ctrl+shift+f6"), "prefix_new_window", "ctrl+shift+f6")
		check("terminal_mode", reg.GetTerminalModeAction("ctrl+f7"), "terminal_exit_mode", "ctrl+f7")
		check("global", reg.GetGlobalAction("super+f8"), "command_palette", "super+f8")
		if mac {
			check("terminal_mode", reg.GetTerminalModeAction("alt+f9"), "terminal_exit_mode", "alt+f9")
		}
	})
}

// TestExplainReadsEveryAliasTheSame is `tuios keybinds explain`: opt+f12,
// option+f12 and alt+f12 are one key, and all three show the leader.
//
// Negative control: make lookupForm lowercase only, as it did, and alt+f12
// and option+f12 are reported as free.
func TestExplainReadsEveryAliasTheSame(t *testing.T) {
	restore := config.ForceMacOSHost(true)
	defer restore()

	cfg := config.DefaultConfig()
	cfg.Keybindings.LeaderKey = "opt+f12"
	reg := config.NewKeybindRegistry(cfg)

	for _, key := range []string{"opt+f12", "option+f12", "alt+f12", "Alt+F12"} {
		fate := reg.Fate(key, config.PaneFacts{})
		if fate.Free || !fate.SwallowedInTerminal || !strings.Contains(fate.SwallowReason, "prefix chord") {
			t.Errorf("explain %s: free=%v swallowed=%v reason=%q, want the leader",
				key, fate.Free, fate.SwallowedInTerminal, fate.SwallowReason)
		}
	}
	if got := reg.Fate("opt+f12", config.PaneFacts{}).ReadAs; got != "alt+f12" {
		t.Errorf("explain opt+f12 read as %q, want alt+f12", got)
	}
	if got := reg.Fate("alt+f12", config.PaneFacts{}).ReadAs; got != "" {
		t.Errorf("explain alt+f12 read as %q, want no note", got)
	}
}

// TestFreeKeyTakesAnAliasSpelling is `tuios keybinds free`: freeing alt+f9
// takes the binding the config spells option+f9.
func TestFreeKeyTakesAnAliasSpelling(t *testing.T) {
	restore := config.ForceMacOSHost(true)
	defer restore()

	cfg := config.DefaultConfig()
	cfg.Keybindings.TerminalMode["terminal_exit_mode"] = []string{"option+f9"}
	removals := cfg.Keybindings.FreeKey("alt+f9")
	if len(removals) != 1 || removals[0].Action != "terminal_exit_mode" {
		t.Fatalf("FreeKey(alt+f9) removed %+v, want the option+f9 binding", removals)
	}
	if keys := cfg.Keybindings.TerminalMode["terminal_exit_mode"]; len(keys) != 0 {
		t.Fatalf("terminal_exit_mode still has %v after freeing alt+f9", keys)
	}
}

// TestDoctorNamesTheLeaderSpellingAndBadKeys is `tuios keybinds doctor`.
func TestDoctorNamesTheLeaderSpellingAndBadKeys(t *testing.T) {
	t.Run("alias", func(t *testing.T) {
		restore := config.ForceMacOSHost(true)
		defer restore()
		cfg := config.DefaultConfig()
		cfg.Keybindings.LeaderKey = "opt+f12"
		rep := config.NewKeybindRegistry(cfg).Report(config.PaneFacts{})
		if rep.LeaderReadAs != "alt+f12" {
			t.Errorf("doctor reads the leader opt+f12 as %q, want alt+f12", rep.LeaderReadAs)
		}
		if len(rep.KeyProblems) != 0 {
			t.Errorf("doctor finds problems in a valid config: %+v", rep.KeyProblems)
		}
	})
	t.Run("linux reads opt as alt", func(t *testing.T) {
		// Issue #556: a config.toml shared with a Mac works on Linux. opt+
		// can only mean Alt there, so the doctor and explain say so, and the
		// key is not a problem.
		restore := config.ForceMacOSHost(false)
		defer restore()
		cfg := config.DefaultConfig()
		cfg.Keybindings.LeaderKey = "opt+f12"
		cfg.Keybindings.Workspaces["switch_workspace_1"] = []string{"opt+1"}
		reg := config.NewKeybindRegistry(cfg)
		rep := reg.Report(config.PaneFacts{})
		if rep.LeaderReadAs != "alt+f12" {
			t.Errorf("doctor on Linux reads the leader opt+f12 as %q, want alt+f12", rep.LeaderReadAs)
		}
		if len(rep.KeyProblems) != 0 {
			t.Errorf("doctor on Linux lists opt+ keys as problems: %+v", rep.KeyProblems)
		}
		found := false
		for _, k := range rep.OptionKeys {
			if k.Key == "opt+1" && k.ReadAs == "alt+1" && k.Action == "switch_workspace_1" {
				found = true
			}
		}
		if !found {
			t.Errorf("doctor on Linux does not note opt+1 as read as alt+1: %+v", rep.OptionKeys)
		}
		if got := reg.Fate("opt+f12", config.PaneFacts{}).ReadAs; got != "alt+f12" {
			t.Errorf("explain opt+f12 on Linux reads as %q, want alt+f12", got)
		}
		if got := reg.Fate("cmd+f12", config.PaneFacts{}).ReadAs; got != "super+f12" {
			t.Errorf("explain cmd+f12 on Linux reads as %q, want super+f12", got)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		restore := config.ForceMacOSHost(false)
		defer restore()
		cfg := config.DefaultConfig()
		cfg.Keybindings.LeaderKey = "ctrl+nope"
		cfg.Keybindings.PrefixMode["prefix_new_window"] = []string{"hyper+x"}
		rep := config.NewKeybindRegistry(cfg).Report(config.PaneFacts{})
		got := map[string]bool{}
		for _, p := range rep.KeyProblems {
			got[p.Section+"."+p.Action+"="+p.Key] = true
		}
		for _, want := range []string{"keybindings.leader_key=ctrl+nope", "prefix_mode.prefix_new_window=hyper+x"} {
			if !got[want] {
				t.Errorf("doctor does not report %s; got %+v", want, rep.KeyProblems)
			}
		}
		if !strings.Contains(rep.Summary(), "cannot read") {
			t.Errorf("summary %q does not count the unreadable keys", rep.Summary())
		}
	})
}

// TestValidateKeyAcceptsModifierAliases: an alias is a valid modifier, and an
// alias next to the modifier it stands for is a duplicate.
func TestValidateKeyAcceptsModifierAliases(t *testing.T) {
	forEachPlatform(t, func(t *testing.T, mac bool) {
		n := config.NewKeyNormalizer()
		for _, key := range []string{"cmd+f12", "command+k", "control+b", "shift+ctrl+x"} {
			if ok, msg := n.ValidateKey(key); !ok {
				t.Errorf("ValidateKey(%q) = %s", key, msg)
			}
		}
		if ok, _ := n.ValidateKey("ctrl+control+b"); ok {
			t.Error("ctrl+control+b is not reported as a duplicate modifier")
		}
		if ok, _ := n.ValidateKey("opt+f12"); !ok {
			t.Errorf("ValidateKey(opt+f12) rejected on macOS=%v", mac)
		}
	})
}
