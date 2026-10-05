package config

import (
	"fmt"
	"strings"
	"unicode"
)

// PanesConfig is the [panes] section: how display_panes labels the panes on
// the screen. display_panes puts a large label on every pane the workspace
// shows, and typing a label focuses that pane.
//
// It is read each time the labels open, so an edit to the file is in force
// the next time.
type PanesConfig struct {
	// LabelKeys is the keys the labels are made of, first pane first
	// (default: 1234567890). Letters a to z and digits only. With more panes
	// than keys, the labels take two keys.
	LabelKeys string `toml:"label_keys"`
}

// PanesDefaultLabelKeys is the default label keys. Digits, as tmux's
// display-panes uses, so the first nine labels are the numbers
// select_window_1 to select_window_9 already give the same panes.
const PanesDefaultLabelKeys = "1234567890"

// defaultPanesConfig returns the section DefaultConfig carries.
func defaultPanesConfig() PanesConfig {
	return PanesConfig{LabelKeys: PanesDefaultLabelKeys}
}

// fillMissingPanes fills an absent value with its default.
func fillMissingPanes(cfg, defaultCfg *UserConfig) {
	if strings.TrimSpace(cfg.Panes.LabelKeys) == "" {
		cfg.Panes.LabelKeys = defaultCfg.Panes.LabelKeys
	}
}

// NormalizePaneLabelKeys makes a key list usable for pane labels: letters a
// to z (upper case counts as lower case) and digits, each once, in the order
// given. It returns the default when fewer than two keys are left, because
// one key cannot tell two panes apart.
func NormalizePaneLabelKeys(keys string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(keys) {
		if !paneLabelKey(r) || strings.ContainsRune(b.String(), r) {
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() < 2 {
		return PanesDefaultLabelKeys
	}
	return b.String()
}

// paneLabelKey reports whether r can be a key of a pane label.
func paneLabelKey(r rune) bool {
	return r <= unicode.MaxASCII && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
}

// LabelKeysInUse is the effective label keys.
func (p PanesConfig) LabelKeysInUse() string { return NormalizePaneLabelKeys(p.LabelKeys) }

// validatePanes warns about label keys that fall back to the default or
// lose characters. Without a warning the labels would look as if they
// ignored the config.
func validatePanes(cfg *UserConfig, result *ValidationResult) {
	keys := strings.TrimSpace(cfg.Panes.LabelKeys)
	if keys == "" {
		return
	}
	// Count the keys a label can use here rather than compare with the
	// default: "1234567890!" keeps the default's keys and drops one.
	var kept strings.Builder
	for _, r := range strings.ToLower(keys) {
		if paneLabelKey(r) && !strings.ContainsRune(kept.String(), r) {
			kept.WriteRune(r)
		}
	}
	used := NormalizePaneLabelKeys(keys)
	if kept.Len() < 2 {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "panes",
			Key:     "label_keys",
			Message: fmt.Sprintf("The label keys %q have fewer than two letters or digits. Pane labels use %q.", keys, PanesDefaultLabelKeys),
		})
		return
	}
	var dropped strings.Builder
	for _, r := range keys {
		if !paneLabelKey(unicode.ToLower(r)) {
			dropped.WriteRune(r)
		}
	}
	if dropped.Len() > 0 {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "panes",
			Key:   "label_keys",
			Message: fmt.Sprintf("Pane labels use only the letters a to z and the digits. They do not use %q. They use %q.",
				dropped.String(), used),
		})
	}
}
