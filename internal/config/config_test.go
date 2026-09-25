package config_test

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestKeyNormalizerAcceptsBothSpellingsOfAShiftedKey pins the rule that a
// binding written one way still matches when the terminal reports the other:
// terminals disagree about whether Shift+1 arrives as "!" or as "shift+1", and
// a binding that only matches one spelling works on one terminal and silently
// does nothing on the next.
func TestKeyNormalizerAcceptsBothSpellingsOfAShiftedKey(t *testing.T) {
	normalizer := config.NewKeyNormalizer()

	tests := []struct {
		input string
		want  []string
	}{
		{"shift+1", []string{"shift+1", "!"}},
		{"!", []string{"!", "shift+1"}},
		{"shift+9", []string{"shift+9", "("}},
		{"shift+m", []string{"shift+m", "M"}},
		{"M", []string{"M", "shift+m"}},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizer.NormalizeKey(tc.input)
			for _, want := range tc.want {
				if !slices.Contains(got, want) {
					t.Errorf("NormalizeKey(%q) = %v, want to contain %q", tc.input, got, want)
				}
			}
		})
	}

	// Keys that are not shifted spellings must not grow spurious aliases.
	for _, key := range []string{"shift+tab", "ctrl+a", "esc", "m"} {
		got := normalizer.NormalizeKey(key)
		if len(got) != 1 {
			t.Errorf("NormalizeKey(%q) = %v, want exactly one spelling", key, got)
		}
	}
}

// TestKeyNormalizer_AccentedKeys covers AZERTY accented letters (issue #51).
// These are multi-byte but single-rune, so a byte-length validator rejected them
// and aborted config load. They must validate and round-trip through normalize
// and registry lookup.
func TestKeyNormalizer_AccentedKeys(t *testing.T) {
	normalizer := config.NewKeyNormalizer()

	validKeys := []string{
		"é", "è", "à", "ç",
		"alt+é", "alt+è", "alt+à", "alt+ç",
		"alt+shift+é",
	}
	for _, k := range validKeys {
		t.Run("validate/"+k, func(t *testing.T) {
			valid, msg := normalizer.ValidateKey(k)
			if !valid {
				t.Errorf("ValidateKey(%q) = false (%q), want true", k, msg)
			}
		})
	}

	roundTrip := []struct {
		input string
		want  string
	}{
		{"é", "é"},
		{"alt+é", "alt+é"},
		{"alt+shift+é", "alt+shift+é"},
	}
	for _, tc := range roundTrip {
		t.Run("normalize/"+tc.input, func(t *testing.T) {
			got := normalizer.NormalizeKey(tc.input)
			if !slices.Contains(got, tc.want) {
				t.Errorf("NormalizeKey(%q) = %v, want to contain %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestApplyAppearanceConfig_CoversTheWholeFile guards the gap that made the
// settings page look like it did not save anything.
//
// Every option below is written to config.toml by the settings page and read
// back from a package global. They used to be applied only by ApplyOverrides,
// which cmd/tuios calls and nothing else does, so a session that loaded its
// config through ApplyAppearanceConfig alone (`tuios tape`, the pkg/tuios
// embed, and every live reload through ConfigReloadedMsg) came back with the
// defaults and the change looked lost.
func TestApplyAppearanceConfig_CoversTheWholeFile(t *testing.T) {
	orig := struct {
		border, dock            string
		buttons, scrollbar      bool
		clock, cpu, ram, revScr bool
		scrollback, fps         int
		leader                  string
		clickToType             string
		buttonStyle             string
		buttonPos               string
	}{
		config.Global.BorderStyle, config.Global.DockbarPosition,
		config.Global.HideWindowButtons, config.Global.HideScrollbar,
		config.Global.ShowClock, config.Global.ShowCPU, config.Global.ShowRAM, config.Global.NiriReverseScroll,
		config.Global.ScrollbackLines, config.Global.NormalFPS,
		config.Global.LeaderKey,
		config.Global.ClickToType,
		config.Global.WindowButtonStyle,
		config.Global.WindowButtonPosition,
	}
	defer func() {
		config.Global.BorderStyle, config.Global.DockbarPosition = orig.border, orig.dock
		config.Global.HideWindowButtons, config.Global.HideScrollbar = orig.buttons, orig.scrollbar
		config.Global.ShowClock, config.Global.ShowCPU, config.Global.ShowRAM = orig.clock, orig.cpu, orig.ram
		config.Global.NiriReverseScroll = orig.revScr
		config.Global.ScrollbackLines, config.Global.NormalFPS = orig.scrollback, orig.fps
		config.Global.LeaderKey = orig.leader
		config.Global.ClickToType = orig.clickToType
		config.Global.WindowButtonStyle = orig.buttonStyle
		config.Global.WindowButtonPosition = orig.buttonPos
	}()

	cfg := config.DefaultConfig()
	cfg.Appearance.BorderStyle = "double"
	cfg.Appearance.DockbarPosition = "top"
	cfg.Appearance.HideWindowButtons = true
	cfg.Appearance.HideScrollbar = true
	cfg.Appearance.ShowClock = true
	cfg.Appearance.ShowCPU = true
	cfg.Appearance.ShowRAM = true
	cfg.Appearance.NiriReverseScroll = true
	cfg.Appearance.ScrollbackLines = 12345
	cfg.Appearance.MaxFPS = 30
	cfg.Keybindings.LeaderKey = "ctrl+a"
	cfg.Appearance.ClickToType = config.ClickToTypeDouble
	cfg.Appearance.WindowButtonStyle = config.WindowButtonStyleDots
	cfg.Appearance.WindowButtonPosition = config.WindowButtonPositionLeft

	config.ApplyAppearanceConfig(cfg, &config.Global)

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"BorderStyle", config.Global.BorderStyle, "double"},
		{"DockbarPosition", config.Global.DockbarPosition, "top"},
		{"HideWindowButtons", config.Global.HideWindowButtons, true},
		{"HideScrollbar", config.Global.HideScrollbar, true},
		{"ShowClock", config.Global.ShowClock, true},
		{"ShowCPU", config.Global.ShowCPU, true},
		{"ShowRAM", config.Global.ShowRAM, true},
		{"NiriReverseScroll", config.Global.NiriReverseScroll, true},
		{"ScrollbackLines", config.Global.ScrollbackLines, 12345},
		{"NormalFPS", config.Global.NormalFPS, 30},
		{"LeaderKey", config.Global.LeaderKey, "ctrl+a"},
		{"ClickToType", config.Global.ClickToType, config.ClickToTypeDouble},
		{"WindowButtonStyle", config.Global.WindowButtonStyle, config.WindowButtonStyleDots},
		{"WindowButtonPosition", config.Global.WindowButtonPosition, config.WindowButtonPositionLeft},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	// Turning a toggle back off has to survive too: these are plain bools with
	// no unset state, so a conditional assignment would make "off" unsaveable.
	cfg.Appearance.HideWindowButtons = false
	cfg.Appearance.ShowClock = false
	config.ApplyAppearanceConfig(cfg, &config.Global)
	if config.Global.HideWindowButtons || config.Global.ShowClock {
		t.Error("clearing hide_window_buttons/show_clock did not reach the globals")
	}
}

// TestSidebarKeybindsFilledForOlderConfig pins the trap that shipped with the
// rail scope: a config written before the sidebar section existed loaded with an
// empty rail keymap, so every rail key resolved to nothing. Because the scope
// swallows unbound keys, the keyboard was stuck in the rail with no way out.
func TestSidebarKeybindsFilledForOlderConfig(t *testing.T) {
	// A pre-rail config: it has a keybindings table, but no [keybindings.sidebar].
	cfg := writeConfig(t, "[keybindings]\nleader_key = \"ctrl+b\"\n\n[keybindings.window_management]\nclose_window = [\"x\"]\n")

	r := config.NewKeybindRegistry(cfg)
	// The file bound close_window to "x" alone, where the default binds both "w"
	// and "x". Checking "w" came loose is what proves the fixture was read at
	// all: every rail assertion below is also true of the defaults, so without
	// this the test passes just as well on a config it never loaded.
	if got := r.GetAction("w"); got == "close_window" {
		t.Fatal("close_window still answers to w, so the fixture was never loaded")
	}
	if len(cfg.Keybindings.Sidebar) == 0 {
		t.Fatal("an older config loaded with an empty rail keymap; every rail key would be swallowed")
	}
	for key, want := range map[string]string{"j": "cursor_down", "enter": "activate", "esc": "exit"} {
		if got := r.GetSidebarAction(key); got != want {
			t.Fatalf("GetSidebarAction(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestSidebarKeybindsDoNotLeakToPanes checks the rail scope is exclusive by
// construction: its keys resolve through GetSidebarAction but never through the
// global keymap, so j/k/h/l/enter cannot fire on a pane.
func TestSidebarKeybindsDoNotLeakToPanes(t *testing.T) {
	r := config.NewKeybindRegistry(config.DefaultConfig())

	if got := r.GetSidebarAction("j"); got != "cursor_down" {
		t.Fatalf("GetSidebarAction(j) = %q, want cursor_down", got)
	}
	if got := r.GetSidebarAction("J"); got != "reorder_down" {
		t.Fatalf("GetSidebarAction(J) = %q, want reorder_down (case matters)", got)
	}
	// The rail's cursor keys must not resolve to a rail action through the global
	// keymap; if they did, pressing j on a pane would run a rail action.
	probes := map[string]string{"j": "cursor_down", "k": "cursor_up", "h": "collapse", "l": "expand", "enter": "activate"}
	for key, railAction := range probes {
		if a := r.GetAction(key); a == railAction {
			t.Fatalf("global keymap leaked rail action %q via key %q", railAction, key)
		}
	}
	// focus_sidebar, by contrast, IS a global window-mode action (the entry key).
	if got := r.GetAction("s"); got != "focus_sidebar" {
		t.Fatalf("GetAction(s) = %q, want focus_sidebar", got)
	}
}

// TestAgentsKeybindsFilledForAPreExistingSidebarSection is the same trap one
// level down: a config that already has a [keybindings.sidebar] table, written
// before the agents section grew its two controls, must still resolve them.
// fillMapDefaults fills per key, not per section, and this pins that.
func TestAgentsKeybindsFilledForAPreExistingSidebarSection(t *testing.T) {
	// A rail-era config: it names the section, and the rail keys it knew about,
	// but nothing about the agents section's filter or sort.
	cfg := writeConfig(t, "[keybindings]\nleader_key = \"ctrl+b\"\n\n[keybindings.sidebar]\ncursor_down = [\"j\"]\nexit = [\"esc\"]\n")

	r := config.NewKeybindRegistry(cfg)
	// The file narrowed both keys it named: cursor_down loses "down" and exit
	// loses "s", where the defaults carry the second binding for each. That is
	// the half of the fixture the defaults cannot imitate, so it is what says
	// the file was loaded rather than skipped over.
	for key, gone := range map[string]string{"down": "cursor_down", "s": "exit"} {
		if got := r.GetSidebarAction(key); got == gone {
			t.Fatalf("GetSidebarAction(%q) still answers %q, so the fixture was never loaded", key, gone)
		}
	}
	// Filling is per key, not per section: a section the file already names
	// still gains the keys it predates.
	for key, want := range map[string]string{"f": "agents_filter", "o": "agents_sort"} {
		if got := r.GetSidebarAction(key); got != want {
			t.Fatalf("GetSidebarAction(%q) = %q, want %q: the new keys never reached an existing section", key, got, want)
		}
	}
	// The keys the file did set are still its own.
	if got := r.GetSidebarAction("j"); got != "cursor_down" {
		t.Fatalf("GetSidebarAction(j) = %q, want cursor_down", got)
	}
}

// TestSidebarFileKeybindsFilledForOlderConfig is the same claim for the files
// section's own keys: a config written before they existed has no
// [keybindings.sidebar_files], and left unfilled the listing would have no way
// to create, rename or delete anything.
func TestSidebarFileKeybindsFilledForOlderConfig(t *testing.T) {
	cfg := writeConfig(t, "[keybindings]\nleader_key = \"ctrl+b\"\n\n[keybindings.window_management]\nclose_window = [\"x\"]\n")

	r := config.NewKeybindRegistry(cfg)
	// The fixture check, so the assertions below cannot pass on a config that was
	// never read: the file binds close_window to "x" alone, where the default
	// binds both "w" and "x".
	if got := r.GetAction("w"); got == "close_window" {
		t.Fatal("close_window still answers to w, so the fixture was never loaded")
	}
	for key, want := range map[string]string{
		"a": "file_create",
		"r": "file_rename",
		"d": "file_delete",
		"D": "file_delete_forever",
		"y": "file_copy",
		"x": "file_cut",
		"p": "file_paste",
	} {
		if got := r.GetSidebarFilesAction(key); got != want {
			t.Errorf("GetSidebarFilesAction(%q) = %q, want %q", key, got, want)
		}
	}

	// And the rail's own keys still mean what they meant. The two sections share
	// r and x, and the rail's meaning is what a row outside the listing gets.
	if got := r.GetSidebarAction("r"); got != "rename" {
		t.Errorf("GetSidebarAction(r) = %q, want rename", got)
	}
	if got := r.GetSidebarAction("x"); got != "kill" {
		t.Errorf("GetSidebarAction(x) = %q, want kill", got)
	}
	// The file keys must not leak onto a pane either.
	if got := r.GetAction("d"); got == "file_delete" {
		t.Error("file_delete resolves through the global keymap; it would fire on a pane")
	}
}
