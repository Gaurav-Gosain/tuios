package config_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestValidateKeyAcceptsBubbleTeaKeyNames. The generated config header
// suggests binding hold_window_mode to f13, and the validator only knew f1 to
// f12, so following the header produced a config that failed to load. A
// binding is matched against the string Bubble Tea gives the key, so every
// name it gives for a function key has to validate.
func TestValidateKeyAcceptsBubbleTeaKeyNames(t *testing.T) {
	normalizer := config.NewKeyNormalizer()

	codes := []rune{
		tea.KeyInsert, tea.KeyCapsLock, tea.KeyScrollLock, tea.KeyNumLock,
		tea.KeyPrintScreen, tea.KeyPause, tea.KeyMenu,
	}
	for code := tea.KeyF1; code <= tea.KeyF63; code++ {
		codes = append(codes, code)
	}

	for _, code := range codes {
		name := tea.Key{Code: code}.Keystroke()
		for _, key := range []string{name, "shift+" + name, "ctrl+alt+" + name} {
			if valid, msg := normalizer.ValidateKey(key); !valid {
				t.Errorf("ValidateKey(%q) = false (%s), want true", key, msg)
			}
		}
	}
	if got := (tea.Key{Code: tea.KeyF13}).Keystroke(); got != "f13" {
		t.Fatalf("Bubble Tea names F13 %q, so the config header's f13 example is wrong", got)
	}
}

// TestValidateKeyRejectsNonKeys keeps the function key range closed.
func TestValidateKeyRejectsNonKeys(t *testing.T) {
	normalizer := config.NewKeyNormalizer()
	for _, key := range []string{"f0", "f64", "f013", "fx", "f1x", "ctrl+f99"} {
		if valid, _ := normalizer.ValidateKey(key); valid {
			t.Errorf("ValidateKey(%q) = true, want false", key)
		}
	}
}
