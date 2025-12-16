package config_test

import (
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
			found := false
			for _, k := range got {
				if k == tc.expected {
					found = true
					break
				}
			}
			if !found {
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
	for i := 0; i < b.N; i++ {
		_ = registry.GetAction("n")
	}
}

func BenchmarkKeybindRegistry_GetKeys(b *testing.B) {
	cfg := config.DefaultConfig()
	registry := config.NewKeybindRegistry(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = registry.GetKeys("new_window")
	}
}

func BenchmarkNormalizeKey(b *testing.B) {
	normalizer := config.NewKeyNormalizer()
	keys := []string{"ctrl+a", "Ctrl+Shift+B", "alt+1", "return"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = normalizer.NormalizeKey(keys[i%len(keys)])
	}
}
