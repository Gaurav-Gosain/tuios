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

// =============================================================================
// Animation Configuration Tests
// =============================================================================

func TestAnimationConfig(t *testing.T) {
	// Default should be enabled
	config.AnimationsEnabled = true

	duration := config.GetAnimationDuration()
	if duration == 0 {
		t.Error("Expected non-zero animation duration when enabled")
	}

	fastDuration := config.GetFastAnimationDuration()
	if fastDuration == 0 {
		t.Error("Expected non-zero fast animation duration when enabled")
	}

	if fastDuration >= duration {
		t.Error("Fast animation should be shorter than normal")
	}

	// Disable animations
	config.AnimationsEnabled = false

	duration = config.GetAnimationDuration()
	if duration != 0 {
		t.Errorf("Expected zero duration when disabled, got %v", duration)
	}

	fastDuration = config.GetFastAnimationDuration()
	if fastDuration != 0 {
		t.Errorf("Expected zero fast duration when disabled, got %v", fastDuration)
	}

	// Reset for other tests
	config.AnimationsEnabled = true
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
	originalASCII := config.UseASCIIOnly
	defer func() { config.UseASCIIOnly = originalASCII }()

	// Reset to default
	config.UseASCIIOnly = false

	// Apply override
	config.ApplyOverrides(config.Overrides{ASCIIOnly: true}, nil)

	if !config.UseASCIIOnly {
		t.Error("Expected UseASCIIOnly to be true after override")
	}
}

func TestApplyOverrides_BorderStyle(t *testing.T) {
	// Save original value
	originalBorder := config.BorderStyle
	defer func() { config.BorderStyle = originalBorder }()

	// Reset to default
	config.BorderStyle = "rounded"

	// Apply CLI override
	config.ApplyOverrides(config.Overrides{BorderStyle: "double"}, nil)
	if config.BorderStyle != "double" {
		t.Errorf("Expected BorderStyle 'double', got %q", config.BorderStyle)
	}

	// CLI flag takes precedence over user config
	config.BorderStyle = "rounded"
	userCfg := config.DefaultConfig()
	userCfg.Appearance.BorderStyle = "thick"
	config.ApplyOverrides(config.Overrides{BorderStyle: "normal"}, userCfg)
	if config.BorderStyle != "normal" {
		t.Errorf("Expected CLI override 'normal' to take precedence, got %q", config.BorderStyle)
	}

	// User config used when CLI flag not set
	config.BorderStyle = "rounded"
	config.ApplyOverrides(config.Overrides{}, userCfg)
	if config.BorderStyle != "thick" {
		t.Errorf("Expected user config 'thick' to be used, got %q", config.BorderStyle)
	}
}

func TestApplyOverrides_DockbarPosition(t *testing.T) {
	// Save original value
	originalPos := config.DockbarPosition
	defer func() { config.DockbarPosition = originalPos }()

	// Reset to default
	config.DockbarPosition = "bottom"

	// Apply CLI override
	config.ApplyOverrides(config.Overrides{DockbarPosition: "top"}, nil)
	if config.DockbarPosition != "top" {
		t.Errorf("Expected DockbarPosition 'top', got %q", config.DockbarPosition)
	}

	// User config fallback
	config.DockbarPosition = "bottom"
	userCfg := config.DefaultConfig()
	userCfg.Appearance.DockbarPosition = "left"
	config.ApplyOverrides(config.Overrides{}, userCfg)
	if config.DockbarPosition != "left" {
		t.Errorf("Expected user config 'left', got %q", config.DockbarPosition)
	}
}

func TestApplyOverrides_HideWindowButtons(t *testing.T) {
	// Save original value
	originalHide := config.HideWindowButtons
	defer func() { config.HideWindowButtons = originalHide }()

	// Reset to default
	config.HideWindowButtons = false

	// CLI flag only
	config.ApplyOverrides(config.Overrides{HideWindowButtons: true}, nil)
	if !config.HideWindowButtons {
		t.Error("Expected HideWindowButtons to be true from CLI flag")
	}

	// User config only
	config.HideWindowButtons = false
	userCfg := config.DefaultConfig()
	userCfg.Appearance.HideWindowButtons = true
	config.ApplyOverrides(config.Overrides{}, userCfg)
	if !config.HideWindowButtons {
		t.Error("Expected HideWindowButtons to be true from user config")
	}

	// OR of both (CLI false, user config true)
	config.HideWindowButtons = false
	config.ApplyOverrides(config.Overrides{HideWindowButtons: false}, userCfg)
	if !config.HideWindowButtons {
		t.Error("Expected HideWindowButtons to be true (OR of CLI and user config)")
	}
}

func TestApplyOverrides_ScrollbackLines(t *testing.T) {
	// Save original value
	originalLines := config.ScrollbackLines
	defer func() { config.ScrollbackLines = originalLines }()

	// Reset to default
	config.ScrollbackLines = 10000

	// CLI override takes precedence
	config.ApplyOverrides(config.Overrides{ScrollbackLines: 5000}, nil)
	if config.ScrollbackLines != 5000 {
		t.Errorf("Expected ScrollbackLines 5000, got %d", config.ScrollbackLines)
	}

	// Test clamping to minimum
	config.ScrollbackLines = 10000
	config.ApplyOverrides(config.Overrides{ScrollbackLines: 50}, nil)
	if config.ScrollbackLines != 100 {
		t.Errorf("Expected ScrollbackLines to be clamped to 100, got %d", config.ScrollbackLines)
	}

	// Test clamping to maximum
	config.ScrollbackLines = 10000
	config.ApplyOverrides(config.Overrides{ScrollbackLines: 2000000}, nil)
	if config.ScrollbackLines != 1000000 {
		t.Errorf("Expected ScrollbackLines to be clamped to 1000000, got %d", config.ScrollbackLines)
	}

	// User config fallback
	config.ScrollbackLines = 10000
	userCfg := config.DefaultConfig()
	userCfg.Appearance.ScrollbackLines = 20000
	config.ApplyOverrides(config.Overrides{}, userCfg)
	if config.ScrollbackLines != 20000 {
		t.Errorf("Expected user config 20000, got %d", config.ScrollbackLines)
	}
}

func TestApplyOverrides_NoAnimations(t *testing.T) {
	// Save original value
	originalEnabled := config.AnimationsEnabled
	defer func() { config.AnimationsEnabled = originalEnabled }()

	// Reset to default
	config.AnimationsEnabled = true

	// Apply NoAnimations flag
	config.ApplyOverrides(config.Overrides{NoAnimations: true}, nil)
	if config.AnimationsEnabled {
		t.Error("Expected AnimationsEnabled to be false after NoAnimations override")
	}

	// Not setting the flag should not change the value
	config.AnimationsEnabled = true
	config.ApplyOverrides(config.Overrides{NoAnimations: false}, nil)
	if !config.AnimationsEnabled {
		t.Error("Expected AnimationsEnabled to remain true when NoAnimations is false")
	}
}

func TestApplyOverrides_LeaderKey(t *testing.T) {
	// Save original value
	originalLeader := config.LeaderKey
	defer func() { config.LeaderKey = originalLeader }()

	// Reset to default
	config.LeaderKey = "ctrl+b"

	// Leader key only comes from user config
	userCfg := config.DefaultConfig()
	userCfg.Keybindings.LeaderKey = "ctrl+a"
	config.ApplyOverrides(config.Overrides{}, userCfg)
	if config.LeaderKey != "ctrl+a" {
		t.Errorf("Expected LeaderKey 'ctrl+a', got %q", config.LeaderKey)
	}

	// No user config should keep default
	config.LeaderKey = "ctrl+b"
	config.ApplyOverrides(config.Overrides{}, nil)
	if config.LeaderKey != "ctrl+b" {
		t.Errorf("Expected LeaderKey to remain 'ctrl+b', got %q", config.LeaderKey)
	}
}

func TestApplyOverrides_WindowTitlePosition(t *testing.T) {
	// Save original value
	originalPos := config.WindowTitlePosition
	defer func() { config.WindowTitlePosition = originalPos }()

	// Reset to default
	config.WindowTitlePosition = "bottom"

	// CLI override
	config.ApplyOverrides(config.Overrides{WindowTitlePosition: "top"}, nil)
	if config.WindowTitlePosition != "top" {
		t.Errorf("Expected WindowTitlePosition 'top', got %q", config.WindowTitlePosition)
	}

	// Hidden option
	config.WindowTitlePosition = "bottom"
	config.ApplyOverrides(config.Overrides{WindowTitlePosition: "hidden"}, nil)
	if config.WindowTitlePosition != "hidden" {
		t.Errorf("Expected WindowTitlePosition 'hidden', got %q", config.WindowTitlePosition)
	}
}

func TestApplyOverrides_HideClock(t *testing.T) {
	// Save original value
	originalHide := config.HideClock
	defer func() { config.HideClock = originalHide }()

	// Reset to default
	config.HideClock = false

	// CLI flag
	config.ApplyOverrides(config.Overrides{HideClock: true}, nil)
	if !config.HideClock {
		t.Error("Expected HideClock to be true from CLI flag")
	}

	// User config OR with CLI
	config.HideClock = false
	userCfg := config.DefaultConfig()
	userCfg.Appearance.HideClock = true
	config.ApplyOverrides(config.Overrides{HideClock: false}, userCfg)
	if !config.HideClock {
		t.Error("Expected HideClock to be true from user config (OR)")
	}
}
