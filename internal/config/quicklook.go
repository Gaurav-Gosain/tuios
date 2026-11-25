package config

import (
	"strconv"

	"github.com/pelletier/go-toml/v2"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/previewspec"
)

// QuickLookConfig is the [quick_look] table: the previewers quick look runs
// for the files they match, in order, before its own views.
type QuickLookConfig struct {
	Previewers []PreviewerConfig `toml:"previewer"`
}

// PreviewerConfig is one [[quick_look.previewer]] entry. See
// internal/previewspec for what each key means.
type PreviewerConfig struct {
	Name string `toml:"name"`
	previewspec.Spec
}

// Problem says what is wrong with the previewer, or "".
func (p PreviewerConfig) Problem() string {
	return p.Check(p.Name)
}

// QuickLookInFile reads the [quick_look] table of the file at path. A
// missing file is an empty table.
func QuickLookInFile(path string) (QuickLookConfig, error) {
	data, err := readConfigMerged(path)
	if err != nil {
		return QuickLookConfig{}, err
	}
	var doc struct {
		QuickLook QuickLookConfig `toml:"quick_look"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return QuickLookConfig{}, errfmt.Errorf("config.toml has an error: %w", err)
	}
	return doc.QuickLook, nil
}

// validateQuickLook warns about a previewer that quick look skips.
func validateQuickLook(cfg *UserConfig, result *ValidationResult) {
	for i, p := range cfg.QuickLook.Previewers {
		if msg := p.Problem(); msg != "" {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "quick_look",
				Key:     "previewer[" + strconv.Itoa(i) + "]",
				Message: msg + " tuios skips it.",
			})
		}
	}
}
