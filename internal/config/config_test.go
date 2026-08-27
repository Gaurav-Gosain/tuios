package config_test

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// =============================================================================
// Default Configuration Tests
// =============================================================================

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}

	// Check essential defaults
	if cfg.Keybindings.LeaderKey == "" {
		t.Error("Expected default leader key to be set")
	}

	if cfg.Appearance.BorderStyle == "" {
		t.Error("Expected default border style to be set")
	}

	if cfg.Appearance.DockbarPosition == "" {
		t.Error("Expected default dockbar position to be set")
	}

	if cfg.Appearance.ScrollbackLines < 100 {
		t.Errorf("Expected scrollback lines >= 100, got %d", cfg.Appearance.ScrollbackLines)
	}
}

func TestDefaultKeybindings(t *testing.T) {
	cfg := config.DefaultConfig()

	// Check window management keys exist
	windowMgmt := cfg.Keybindings.WindowManagement
	if windowMgmt == nil {
		t.Fatal("Window management keybindings are nil")
	}

	requiredActions := []string{
		"new_window",
		"close_window",
		"next_window",
		"prev_window",
	}

	for _, action := range requiredActions {
		keys, ok := windowMgmt[action]
		if !ok {
			t.Errorf("Expected %s keybinding to exist", action)
			continue
		}
		if len(keys) == 0 {
			t.Errorf("Expected %s to have at least one key bound", action)
		}
	}
}

// =============================================================================
// KeybindRegistry Tests
// =============================================================================

func TestKeybindRegistry_GetKeys(t *testing.T) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	// Test getting keys for known action
	keys := registry.GetKeys("new_window")
	if len(keys) == 0 {
		t.Error("Expected new_window to have keys")
	}
}

func TestKeybindRegistry_GetAction(t *testing.T) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	// Get the key bound to new_window
	keys := registry.GetKeys("new_window")
	if len(keys) == 0 {
		t.Skip("No keys bound to new_window")
	}

	// Verify reverse lookup
	action := registry.GetAction(keys[0])
	if action != "new_window" {
		t.Errorf("Expected action 'new_window', got %q", action)
	}
}

func TestKeybindRegistry_GetKeysForDisplay(t *testing.T) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	display := registry.GetKeysForDisplay("new_window")
	if display == "" {
		t.Error("Expected display string for new_window")
	}
}

func TestKeybindRegistry_UnknownAction(t *testing.T) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	keys := registry.GetKeys("nonexistent_action")
	if len(keys) != 0 {
		t.Errorf("Expected empty keys for nonexistent action, got %v", keys)
	}
}

func TestKeybindRegistry_UnknownKey(t *testing.T) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	action := registry.GetAction("ctrl+shift+alt+super+hyper+x")
	if action != "" {
		t.Errorf("Expected empty action for unbound key, got %q", action)
	}
}

// =============================================================================
// Key Normalizer Tests
// =============================================================================

func TestKeyNormalizer(t *testing.T) {
	normalizer := config.NewKeyNormalizer()

	tests := []struct {
		input    string
		expected string
	}{
		{"ctrl+a", "ctrl+a"},
		{"Ctrl+A", "ctrl+a"},
		{"CTRL+A", "ctrl+a"},
		{"return", "return"}, // Normalizer preserves key names
		{"escape", "escape"},
		{"enter", "enter"},
		{"esc", "esc"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizer.NormalizeKey(tc.input)
			// NormalizeKey returns a slice of possible keys
			if len(got) == 0 {
				t.Errorf("NormalizeKey(%q) returned empty slice", tc.input)
				return
			}
			// Check if expected is in the result
			if !slices.Contains(got, tc.expected) {
				t.Errorf("NormalizeKey(%q) = %v, want to contain %q", tc.input, got, tc.expected)
			}
		})
	}
}

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

func TestKeyNormalizer_ValidateKey(t *testing.T) {
	normalizer := config.NewKeyNormalizer()

	tests := []struct {
		input   string
		isValid bool
	}{
		{"ctrl+a", true},
		{"n", true},
		{"enter", true},
		{"esc", true},
		{"tab", true},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			valid, _ := normalizer.ValidateKey(tc.input)
			if valid != tc.isValid {
				t.Errorf("ValidateKey(%q) = %v, want %v", tc.input, valid, tc.isValid)
			}
		})
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

// TestKeybindRegistry_AccentedLookup verifies an accented binding survives the
// full expand/normalize path and reverse-looks-up to its action.
func TestKeybindRegistry_AccentedLookup(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Keybindings.Workspaces["switch_workspace_2"] = []string{"alt+é"}
	registry := config.NewKeybindRegistry(cfg)

	if action := registry.GetAction("alt+é"); action != "switch_workspace_2" {
		t.Errorf("GetAction(%q) = %q, want %q", "alt+é", action, "switch_workspace_2")
	}
}

// =============================================================================
// Animation Configuration Tests
// =============================================================================

func TestAnimationConfig(t *testing.T) {
	// Default should be enabled
	config.Global.AnimationsEnabled = true

	duration := config.Global.GetAnimationDuration()
	if duration == 0 {
		t.Error("Expected non-zero animation duration when enabled")
	}

	fastDuration := config.Global.GetFastAnimationDuration()
	if fastDuration == 0 {
		t.Error("Expected non-zero fast animation duration when enabled")
	}

	if fastDuration >= duration {
		t.Error("Fast animation should be shorter than normal")
	}

	// Disable animations
	config.Global.AnimationsEnabled = false

	duration = config.Global.GetAnimationDuration()
	if duration != 0 {
		t.Errorf("Expected zero duration when disabled, got %v", duration)
	}

	fastDuration = config.Global.GetFastAnimationDuration()
	if fastDuration != 0 {
		t.Errorf("Expected zero fast duration when disabled, got %v", fastDuration)
	}

	// Reset for other tests
	config.Global.AnimationsEnabled = true
}

// =============================================================================
// Action Descriptions Tests
// =============================================================================

func TestActionDescriptions(t *testing.T) {
	// Check some key actions have descriptions
	requiredDescriptions := []string{
		"new_window",
		"close_window",
		"toggle_tiling",
		"toggle_help",
		"quit",
	}

	for _, action := range requiredDescriptions {
		desc, ok := config.ActionDescriptions[action]
		if !ok {
			t.Errorf("Expected description for action %q", action)
			continue
		}
		if desc == "" {
			t.Errorf("Description for %q should not be empty", action)
		}
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkKeybindRegistry_GetAction(b *testing.B) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	b.ResetTimer()
	for b.Loop() {
		_ = registry.GetAction("n")
	}
}

func BenchmarkKeybindRegistry_GetKeys(b *testing.B) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	b.ResetTimer()
	for b.Loop() {
		_ = registry.GetKeys("new_window")
	}
}

func BenchmarkNormalizeKey(b *testing.B) {
	normalizer := config.NewKeyNormalizer()
	keys := []string{"ctrl+a", "Ctrl+Shift+B", "alt+1", "return"}

	i := 0
	b.ResetTimer()
	for b.Loop() {
		_ = normalizer.NormalizeKey(keys[i%len(keys)])
		i++
	}
}

// =============================================================================
// Override Tests
// =============================================================================

func TestApplyOverrides_ASCIIOnly(t *testing.T) {
	// Save original values
	originalASCII := config.Global.UseASCIIOnly
	defer func() { config.Global.UseASCIIOnly = originalASCII }()

	// Reset to default
	config.Global.UseASCIIOnly = false

	// Apply override
	config.ApplyOverrides(config.Overrides{ASCIIOnly: true}, &config.Global)

	if !config.Global.UseASCIIOnly {
		t.Error("Expected UseASCIIOnly to be true after override")
	}
}

func TestApplyOverrides_BorderStyle(t *testing.T) {
	// Save original value
	originalBorder := config.Global.BorderStyle
	defer func() { config.Global.BorderStyle = originalBorder }()

	// Reset to default
	config.Global.BorderStyle = "rounded"

	// Apply CLI override
	config.ApplyOverrides(config.Overrides{BorderStyle: "double"}, &config.Global)
	if config.Global.BorderStyle != "double" {
		t.Errorf("Expected BorderStyle 'double', got %q", config.Global.BorderStyle)
	}

	// CLI flag takes precedence over user config
	config.Global.BorderStyle = "rounded"
	userCfg := config.DefaultConfig()
	userCfg.Appearance.BorderStyle = "thick"
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{BorderStyle: "normal"}, &config.Global)
	if config.Global.BorderStyle != "normal" {
		t.Errorf("Expected CLI override 'normal' to take precedence, got %q", config.Global.BorderStyle)
	}

	// User config used when CLI flag not set
	config.Global.BorderStyle = "rounded"
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{}, &config.Global)
	if config.Global.BorderStyle != "thick" {
		t.Errorf("Expected user config 'thick' to be used, got %q", config.Global.BorderStyle)
	}
}

func TestApplyOverrides_DockbarPosition(t *testing.T) {
	// Save original value
	originalPos := config.Global.DockbarPosition
	defer func() { config.Global.DockbarPosition = originalPos }()

	// Reset to default
	config.Global.DockbarPosition = "bottom"

	// Apply CLI override
	config.ApplyOverrides(config.Overrides{DockbarPosition: "top"}, &config.Global)
	if config.Global.DockbarPosition != "top" {
		t.Errorf("Expected DockbarPosition 'top', got %q", config.Global.DockbarPosition)
	}

	// User config fallback
	config.Global.DockbarPosition = "bottom"
	userCfg := config.DefaultConfig()
	userCfg.Appearance.DockbarPosition = "left"
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{}, &config.Global)
	if config.Global.DockbarPosition != "left" {
		t.Errorf("Expected user config 'left', got %q", config.Global.DockbarPosition)
	}
}

func TestApplyOverrides_HideWindowButtons(t *testing.T) {
	// Save original value
	originalHide := config.Global.HideWindowButtons
	defer func() { config.Global.HideWindowButtons = originalHide }()

	// Reset to default
	config.Global.HideWindowButtons = false

	// CLI flag only
	config.ApplyOverrides(config.Overrides{HideWindowButtons: true}, &config.Global)
	if !config.Global.HideWindowButtons {
		t.Error("Expected HideWindowButtons to be true from CLI flag")
	}

	// User config only
	config.Global.HideWindowButtons = false
	userCfg := config.DefaultConfig()
	userCfg.Appearance.HideWindowButtons = true
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{}, &config.Global)
	if !config.Global.HideWindowButtons {
		t.Error("Expected HideWindowButtons to be true from user config")
	}

	// OR of both (CLI false, user config true)
	config.Global.HideWindowButtons = false
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{HideWindowButtons: false}, &config.Global)
	if !config.Global.HideWindowButtons {
		t.Error("Expected HideWindowButtons to be true (OR of CLI and user config)")
	}
}

func TestApplyOverrides_ScrollbackLines(t *testing.T) {
	// Save original value
	originalLines := config.Global.ScrollbackLines
	defer func() { config.Global.ScrollbackLines = originalLines }()

	// Reset to default
	config.Global.ScrollbackLines = 10000

	// CLI override takes precedence
	config.ApplyOverrides(config.Overrides{ScrollbackLines: 5000}, &config.Global)
	if config.Global.ScrollbackLines != 5000 {
		t.Errorf("Expected ScrollbackLines 5000, got %d", config.Global.ScrollbackLines)
	}

	// Test clamping to minimum
	config.Global.ScrollbackLines = 10000
	config.ApplyOverrides(config.Overrides{ScrollbackLines: 50}, &config.Global)
	if config.Global.ScrollbackLines != 100 {
		t.Errorf("Expected ScrollbackLines to be clamped to 100, got %d", config.Global.ScrollbackLines)
	}

	// Test clamping to maximum
	config.Global.ScrollbackLines = 10000
	config.ApplyOverrides(config.Overrides{ScrollbackLines: 2000000}, &config.Global)
	if config.Global.ScrollbackLines != 1000000 {
		t.Errorf("Expected ScrollbackLines to be clamped to 1000000, got %d", config.Global.ScrollbackLines)
	}

	// User config fallback
	config.Global.ScrollbackLines = 10000
	userCfg := config.DefaultConfig()
	userCfg.Appearance.ScrollbackLines = 20000
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{}, &config.Global)
	if config.Global.ScrollbackLines != 20000 {
		t.Errorf("Expected user config 20000, got %d", config.Global.ScrollbackLines)
	}
}

func TestApplyOverrides_NoAnimations(t *testing.T) {
	// Save original value
	originalEnabled := config.Global.AnimationsEnabled
	defer func() { config.Global.AnimationsEnabled = originalEnabled }()

	// Reset to default
	config.Global.AnimationsEnabled = true

	// Apply NoAnimations flag
	config.ApplyOverrides(config.Overrides{NoAnimations: true}, &config.Global)
	if config.Global.AnimationsEnabled {
		t.Error("Expected AnimationsEnabled to be false after NoAnimations override")
	}

	// Not setting the flag should not change the value
	config.Global.AnimationsEnabled = true
	config.ApplyOverrides(config.Overrides{NoAnimations: false}, &config.Global)
	if !config.Global.AnimationsEnabled {
		t.Error("Expected AnimationsEnabled to remain true when NoAnimations is false")
	}
}

// TestStartupPrecedence_FlagWinsOverConfig checks the startup application order:
// ApplyAppearanceConfig establishes the config baseline, then ApplyOverrides
// lets CLI flags win. This is the sequence LoadUserConfig no longer performs
// implicitly, and the fix that keeps `--no-animations` from being reverted by
// animations_enabled = true.
func TestStartupPrecedence_FlagWinsOverConfig(t *testing.T) {
	original := config.Global.AnimationsEnabled
	defer func() { config.Global.AnimationsEnabled = original }()

	enabled := true
	userCfg := config.DefaultConfig()
	userCfg.Appearance.AnimationsEnabled = &enabled

	config.ApplyAppearanceConfig(userCfg, &config.Global) // baseline: on
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{NoAnimations: true}, &config.Global) // flag wins: off

	if config.Global.AnimationsEnabled {
		t.Error("CLI --no-animations must win over config animations_enabled = true")
	}
}

// TestLoadUserConfig_Pure verifies LoadUserConfig has no package-global side
// effects, so a second load (e.g. inside NewOS or per server connection) cannot
// clobber previously applied globals or race other sessions.
func TestLoadUserConfig_Pure(t *testing.T) {
	original := config.Global.AnimationsEnabled
	defer func() { config.Global.AnimationsEnabled = original }()

	config.Global.AnimationsEnabled = false
	// Load a config of this test's own. Reading whatever the developer happens
	// to have made the result depend on the machine, and the skip that guarded
	// it turned a genuine load failure into a silent pass.
	writeConfig(t, "[appearance]\nanimations_enabled = true\n")
	if config.Global.AnimationsEnabled {
		t.Error("LoadUserConfig must not mutate appearance globals")
	}
}

func TestApplyOverrides_LeaderKey(t *testing.T) {
	// Save original value
	originalLeader := config.Global.LeaderKey
	defer func() { config.Global.LeaderKey = originalLeader }()

	// Reset to default
	config.Global.LeaderKey = "ctrl+b"

	// Leader key only comes from user config
	userCfg := config.DefaultConfig()
	userCfg.Keybindings.LeaderKey = "ctrl+a"
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{}, &config.Global)
	if config.Global.LeaderKey != "ctrl+a" {
		t.Errorf("Expected LeaderKey 'ctrl+a', got %q", config.Global.LeaderKey)
	}

	// No user config should keep default
	config.Global.LeaderKey = "ctrl+b"
	config.ApplyOverrides(config.Overrides{}, &config.Global)
	if config.Global.LeaderKey != "ctrl+b" {
		t.Errorf("Expected LeaderKey to remain 'ctrl+b', got %q", config.Global.LeaderKey)
	}
}

func TestApplyOverrides_WindowTitlePosition(t *testing.T) {
	// Save original value
	originalPos := config.Global.WindowTitlePosition
	defer func() { config.Global.WindowTitlePosition = originalPos }()

	// Reset to default
	config.Global.WindowTitlePosition = "bottom"

	// CLI override
	config.ApplyOverrides(config.Overrides{WindowTitlePosition: "top"}, &config.Global)
	if config.Global.WindowTitlePosition != "top" {
		t.Errorf("Expected WindowTitlePosition 'top', got %q", config.Global.WindowTitlePosition)
	}

	// Hidden option
	config.Global.WindowTitlePosition = "bottom"
	config.ApplyOverrides(config.Overrides{WindowTitlePosition: "hidden"}, &config.Global)
	if config.Global.WindowTitlePosition != "hidden" {
		t.Errorf("Expected WindowTitlePosition 'hidden', got %q", config.Global.WindowTitlePosition)
	}
}

func TestApplyOverrides_HideClock(t *testing.T) {
	// Save original value
	originalHide := config.Global.HideClock
	defer func() { config.Global.HideClock = originalHide }()

	// Reset to default
	config.Global.HideClock = false

	// CLI flag
	config.ApplyOverrides(config.Overrides{HideClock: true}, &config.Global)
	if !config.Global.HideClock {
		t.Error("Expected HideClock to be true from CLI flag")
	}

	// User config OR with CLI
	config.Global.HideClock = false
	userCfg := config.DefaultConfig()
	userCfg.Appearance.HideClock = true
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	config.ApplyOverrides(config.Overrides{HideClock: false}, &config.Global)
	if !config.Global.HideClock {
		t.Error("Expected HideClock to be true from user config (OR)")
	}
}

// TestApplyAppearanceConfig_ScrollLines covers the wheel scroll speed option:
// it must reach the global the input layer reads, and an unset value must not
// clobber the default.
func TestApplyAppearanceConfig_ScrollLines(t *testing.T) {
	original := config.Global.ScrollLines
	defer func() { config.Global.ScrollLines = original }()

	if cfg := config.DefaultConfig(); cfg.Appearance.ScrollLines != 3 {
		t.Errorf("default scroll_lines = %d, want 3", cfg.Appearance.ScrollLines)
	}

	userCfg := config.DefaultConfig()
	userCfg.Appearance.ScrollLines = 8
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	if config.Global.ScrollLines != 8 {
		t.Errorf("ScrollLines = %d, want 8", config.Global.ScrollLines)
	}

	// An absent value in a hand-written config must leave the current setting
	// alone rather than scrolling zero lines per notch.
	config.Global.ScrollLines = 5
	userCfg.Appearance.ScrollLines = 0
	config.ApplyAppearanceConfig(userCfg, &config.Global)
	if config.Global.ScrollLines != 5 {
		t.Errorf("ScrollLines = %d after an unset value, want it unchanged at 5", config.Global.ScrollLines)
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
