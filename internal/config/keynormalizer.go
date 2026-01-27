package config

import (
	"os"
	"runtime"
	"strings"
)

// optionToAltReplacer converts opt/option to alt for consistent key naming
var optionToAltReplacer = strings.NewReplacer("opt+", "alt+", "option+", "alt+")

// altToOptReplacer converts alt to opt/option variants
var altToOptReplacer = strings.NewReplacer("alt+", "opt+")
var altToOptionReplacer = strings.NewReplacer("alt+", "option+")

// KeyNormalizer handles platform-specific key normalization
// Converts user-friendly key strings (like "opt+1" on macOS) to their actual representations
type KeyNormalizer struct {
	isMacOS bool
}

// NewKeyNormalizer creates a new key normalizer with platform detection
func NewKeyNormalizer() *KeyNormalizer {
	return &KeyNormalizer{
		isMacOS: detectMacOS(),
	}
}

// IsMacOS returns whether the current platform is macOS
func (kn *KeyNormalizer) IsMacOS() bool {
	return kn.isMacOS
}

// detectMacOS checks if the current platform is macOS
func detectMacOS() bool {
	// Check GOOS first (most reliable)
	if runtime.GOOS == "darwin" {
		return true
	}
	// Fallback to environment variables
	goos := strings.ToLower(os.Getenv("GOOS"))
	ostype := strings.ToLower(os.Getenv("OSTYPE"))
	return strings.Contains(goos, "darwin") || strings.Contains(ostype, "darwin")
}

// macOS Option key mappings (opt+number produces unicode characters)
var macOptionNumberMap = map[string]string{
	"opt+1": "¡", "option+1": "¡",
	"opt+2": "™", "option+2": "™",
	"opt+3": "£", "option+3": "£",
	"opt+4": "¢", "option+4": "¢",
	"opt+5": "∞", "option+5": "∞",
	"opt+6": "§", "option+6": "§",
	"opt+7": "¶", "option+7": "¶",
	"opt+8": "•", "option+8": "•",
	"opt+9": "ª", "option+9": "ª",
}

// macOS Option+Shift key mappings
var macOptionShiftNumberMap = map[string]string{
	"opt+shift+1": "⁄", "option+shift+1": "⁄",
	"opt+shift+2": "€", "option+shift+2": "€",
	"opt+shift+3": "‹", "option+shift+3": "‹",
	"opt+shift+4": "›", "option+shift+4": "›",
	"opt+shift+5": "ﬁ", "option+shift+5": "ﬁ",
	"opt+shift+6": "ﬂ", "option+shift+6": "ﬂ",
	"opt+shift+7": "‡", "option+shift+7": "‡",
	"opt+shift+8": "°", "option+shift+8": "°",
	"opt+shift+9": "·", "option+shift+9": "·",
}

// macOS Option+Tab key mappings
var macOptionTabMap = map[string]string{
	"opt+tab": "⇥", "option+tab": "⇥",
	"opt+shift+tab": "⇤", "option+shift+tab": "⇤",
}

// NormalizeKey converts a key string to its canonical form for the current platform
// For example, on macOS: "opt+1" → "¡" or "alt+1" depending on context
func (kn *KeyNormalizer) NormalizeKey(key string) []string {
	key = strings.TrimSpace(key)

	// For single letters, preserve case (M and m are different keys in Bubbletea)
	// For everything else, normalize to lowercase
	var normalized string
	if len(key) == 1 && ((key[0] >= 'a' && key[0] <= 'z') || (key[0] >= 'A' && key[0] <= 'Z')) {
		normalized = key // Preserve case for single letters
	} else {
		normalized = strings.ToLower(key) // Lowercase for compound keys (ctrl+m, shift+tab, etc.)
	}

	keyLower := strings.ToLower(key)

	// Always include the normalized version
	result := []string{normalized}

	// On macOS, expand opt+N and option+N to unicode and alt+N
	if kn.isMacOS {
		// Check for opt+shift+number combinations first
		if unicode, ok := macOptionShiftNumberMap[keyLower]; ok {
			// Add the unicode character
			result = append(result, strings.ToLower(unicode))
			// Also map to alt+shift+N (use replacer for efficiency)
			result = append(result, optionToAltReplacer.Replace(keyLower))
		} else if unicode, ok := macOptionNumberMap[keyLower]; ok {
			// Add the unicode character
			result = append(result, strings.ToLower(unicode))
			// Also map to alt+N
			result = append(result, optionToAltReplacer.Replace(keyLower))
		} else if unicode, ok := macOptionTabMap[keyLower]; ok {
			// Add the unicode character for opt+tab variants
			result = append(result, unicode)
			// Also map to alt+tab variant
			result = append(result, optionToAltReplacer.Replace(keyLower))
		}

		// If the key starts with "alt+", also accept "opt+" and "option+" variants
		if strings.HasPrefix(keyLower, "alt+") {
			result = append(result, altToOptReplacer.Replace(keyLower))
			result = append(result, altToOptionReplacer.Replace(keyLower))
		}
	}

	// Remove duplicates
	seen := make(map[string]bool)
	unique := []string{}
	for _, k := range result {
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
		}
	}

	return unique
}

// ExpandKeys takes a slice of user-provided keys and expands them to all platform-specific variants
func (kn *KeyNormalizer) ExpandKeys(keys []string) []string {
	var expanded []string
	for _, key := range keys {
		normalized := kn.NormalizeKey(key)
		expanded = append(expanded, normalized...)
	}

	// Remove duplicates from final list
	seen := make(map[string]bool)
	unique := []string{}
	for _, k := range expanded {
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
		}
	}

	return unique
}

// ValidateKey checks if a key string is valid for the current platform
func (kn *KeyNormalizer) ValidateKey(key string) (bool, string) {
	key = strings.TrimSpace(key)
	keyLower := strings.ToLower(key)

	// Empty key
	if keyLower == "" {
		return false, "key cannot be empty"
	}

	// On non-macOS systems, error on opt/option keys
	if !kn.isMacOS {
		if strings.Contains(keyLower, "opt+") || strings.Contains(keyLower, "option+") {
			return false, "opt/option keys are only valid on macOS, use alt+ instead"
		}
	}

	// On macOS, suggest opt+ instead of alt+ for better UX
	// Note: We return true (valid) but will add a warning in validation
	// This is handled separately in validation.go as a warning, not an error

	// Check for invalid modifier combinations
	parts := strings.Split(keyLower, "+")
	if len(parts) > 1 {
		// Extract modifiers (all but last part)
		modifiers := parts[:len(parts)-1]
		actualKey := parts[len(parts)-1]

		// Check for empty actualKey
		if actualKey == "" {
			return false, "key combination incomplete (ends with +)"
		}

		// Valid modifiers (only those that work reliably in terminals)
		validModifiers := map[string]bool{
			"ctrl":   true,
			"alt":    true,
			"shift":  true,
			"opt":    kn.isMacOS, // opt only valid on macOS
			"option": kn.isMacOS, // option only valid on macOS
		}

		// Check each modifier
		for _, mod := range modifiers {
			if !validModifiers[mod] {
				if mod == "opt" || mod == "option" {
					return false, "opt/option modifiers are only valid on macOS"
				}
				return false, "invalid modifier: " + mod
			}
		}

		// Check for duplicate modifiers
		modSet := make(map[string]bool)
		for _, mod := range modifiers {
			if modSet[mod] {
				return false, "duplicate modifier: " + mod
			}
			modSet[mod] = true
		}
	}

	// Valid special keys
	validSpecialKeys := map[string]bool{
		"enter": true, "return": true, "esc": true, "escape": true,
		"tab": true, "space": true, "backspace": true, "delete": true,
		"up": true, "down": true, "left": true, "right": true,
		"home": true, "end": true, "pgup": true, "pageup": true,
		"pgdown": true, "pagedown": true,
		"f1": true, "f2": true, "f3": true, "f4": true,
		"f5": true, "f6": true, "f7": true, "f8": true,
		"f9": true, "f10": true, "f11": true, "f12": true,
	}

	// If there are modifiers, check if the actual key is valid
	parts = strings.Split(keyLower, "+")
	actualKey := parts[len(parts)-1]

	// Single character keys are always valid (a-z, 0-9, symbols)
	if len(actualKey) == 1 {
		return true, ""
	}

	// Check if it's a valid special key
	if !validSpecialKeys[actualKey] {
		return false, "unknown special key: " + actualKey
	}

	return true, ""
}
