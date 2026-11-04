package config

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/i18n"
)

// UIConfig is the [ui] table: how the interface speaks.
type UIConfig struct {
	// Language is the tag of the language the interface is drawn in: "en", or
	// "zh-CN". Empty means the default, which is English.
	//
	// The tag is stored and not a display name, so it is what a user writes in
	// the file and what `tuios set-config ui.language` takes. The catalog it
	// selects ships in the binary; a tag with no catalog falls back to English
	// rather than failing, because a wrong language is cosmetic and a failed
	// start is not.
	Language string `toml:"language"`
}

// UIDefaultLanguage is the language the interface speaks when nothing says
// otherwise. It is also the table every other catalog falls back to.
const UIDefaultLanguage = string(i18n.English)

// defaultUIConfig returns the section DefaultConfig carries.
func defaultUIConfig() UIConfig {
	return UIConfig{Language: UIDefaultLanguage}
}

// fillMissingUI fills an absent language with the default. An unset key means
// "whatever the default is", not "no language".
func fillMissingUI(cfg, defaultCfg *UserConfig) {
	u, d := &cfg.UI, &defaultCfg.UI
	if strings.TrimSpace(u.Language) == "" {
		u.Language = d.Language
	}
}

// LanguageTag is the language this config selects, as a tag with a catalog.
// A tag nobody has a catalog for reads as English.
func (c UserConfig) LanguageTag() string {
	return string(i18n.Parse(c.UI.Language))
}

// LanguageTags is every tag the interface can be drawn in, for the option
// registry and the settings panel. It comes from the i18n package so that
// adding a catalog there is the one edit a new language needs.
func LanguageTags() []string {
	tags := i18n.Languages()
	out := make([]string, 0, len(tags))
	for _, l := range tags {
		out = append(out, string(l))
	}
	return out
}

// validateUI warns about a language tag with no catalog. The start goes ahead
// in English either way, so without a warning a typo would look like the key
// being ignored.
func validateUI(cfg *UserConfig, result *ValidationResult) {
	tag := strings.TrimSpace(cfg.UI.Language)
	if tag == "" || speaksTag(tag) {
		return
	}
	result.Warnings = append(result.Warnings, ValidationError{
		Field: "ui", Key: "language",
		Message: "No catalog for " + tag + ". The interface uses " + UIDefaultLanguage + ".",
	})
}

// speaksTag reports whether tag selects a catalog. Parse is deliberately
// lenient, so it cannot answer this on its own: it maps every unknown tag to
// English, which is the same answer it gives for "en". The check is therefore
// two-sided: an English answer counts only for a tag that names English.
func speaksTag(tag string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(tag, "_", "-"))
	if parse := i18n.Parse(tag); parse != i18n.English {
		return true
	}
	return strings.HasPrefix(normalized, "en")
}
