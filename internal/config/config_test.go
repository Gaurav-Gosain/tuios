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
