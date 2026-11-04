package config

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/i18n"
)

// TestLanguageOptionIsRegistered covers the three places the option has to
// exist for `tuios list-options`, `tuios set-config` and the settings panel to
// see it: the registry, the default config, and the field the registry resolves
// a dotted path against.
func TestLanguageOptionIsRegistered(t *testing.T) {
	opt, ok := LookupOption("ui.language")
	if !ok {
		t.Fatal("ui.language is not in the option registry")
	}
	if opt.Default != UIDefaultLanguage {
		t.Errorf("registry default = %q, want %q", opt.Default, UIDefaultLanguage)
	}
	if !slices.Contains(opt.Accepted, string(i18n.SimplifiedChinese)) {
		t.Errorf("accepted = %v, want it to offer %q", opt.Accepted, i18n.SimplifiedChinese)
	}
	if got := DefaultConfig().LanguageTag(); got != UIDefaultLanguage {
		t.Errorf("default config language = %q, want %q", got, UIDefaultLanguage)
	}
}

// TestLanguageOptionSets covers a write through the registry, which is what
// set-config and the settings panel use. The value has to land in the field the
// [ui] table is decoded into, or it is reported as applied and then ignored.
func TestLanguageOptionSets(t *testing.T) {
	cfg := DefaultConfig()
	if err := SetOptionValue(cfg, "ui.language", "zh-CN"); err != nil {
		t.Fatalf("set ui.language: %v", err)
	}
	if cfg.UI.Language != "zh-CN" {
		t.Errorf("UI.Language = %q, want zh-CN", cfg.UI.Language)
	}
	if got := cfg.LanguageTag(); got != string(i18n.SimplifiedChinese) {
		t.Errorf("LanguageTag() = %q, want %q", got, i18n.SimplifiedChinese)
	}
}

// TestLanguageOptionRejectsUnknown covers the closed set: a tag with no catalog
// is refused at the point it is set, rather than being stored and silently
// rendering English later.
func TestLanguageOptionRejectsUnknown(t *testing.T) {
	cfg := DefaultConfig()
	if err := SetOptionValue(cfg, "ui.language", "kl-GL"); err == nil {
		t.Error("set ui.language to a tag with no catalog: want an error, got none")
	}
}

// TestValidateUIWarns covers the file path, which does not go through the
// registry: a config written by hand with an unknown tag loads and starts in
// English, so it has to say so.
func TestValidateUIWarns(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UI.Language = "kl-GL"
	result := ValidateConfig(cfg)

	found := false
	for _, w := range result.Warnings {
		if w.Field == "ui" && w.Key == "language" {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for an unknown language tag; warnings: %v", result.Warnings)
	}

	// A tag with a catalog, and the region-qualified spellings a locale
	// setting produces, must not warn.
	for _, tag := range []string{"en", "zh-CN", "zh_CN", "zh-Hans", ""} {
		cfg := DefaultConfig()
		cfg.UI.Language = tag
		for _, w := range ValidateConfig(cfg).Warnings {
			if w.Field == "ui" && w.Key == "language" {
				t.Errorf("tag %q warned, and it selects a catalog", tag)
			}
		}
	}
}
