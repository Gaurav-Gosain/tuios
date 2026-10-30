package config

import (
	"fmt"
	"strings"
)

// ReviewConfig is the [review] section: how the review overlay (ctrl+b v)
// opens. It is read each time a review opens, so an edit to the file is in
// force the next time.
type ReviewConfig struct {
	// Focus is the side that has the keys when a review opens: diff or files
	// (default: diff). A review with no changes opens on the diff. A reload
	// of the same review keeps the side focused.
	Focus string `toml:"focus"`
}

// The sides of the review overlay that can have the keys.
const (
	// ReviewOpenOnDiff is the diff of the file under the list's cursor.
	ReviewOpenOnDiff = "diff"
	// ReviewOpenOnFiles is the list of changed files.
	ReviewOpenOnFiles = "files"
)

// ReviewFocuses is the accepted focus values.
var ReviewFocuses = []string{ReviewOpenOnDiff, ReviewOpenOnFiles}

// FocusInUse is the effective focus. An unknown value is the diff.
func (r ReviewConfig) FocusInUse() string {
	v := strings.ToLower(strings.TrimSpace(r.Focus))
	for _, f := range ReviewFocuses {
		if v == f {
			return f
		}
	}
	return ReviewOpenOnDiff
}

// defaultReviewConfig returns the section DefaultConfig carries.
func defaultReviewConfig() ReviewConfig {
	return ReviewConfig{Focus: ReviewOpenOnDiff}
}

// fillMissingReview fills an absent value with its default.
func fillMissingReview(cfg, defaultCfg *UserConfig) {
	if strings.TrimSpace(cfg.Review.Focus) == "" {
		cfg.Review.Focus = defaultCfg.Review.Focus
	}
}

// validateReview warns about a focus that falls back to the diff.
func validateReview(cfg *UserConfig, result *ValidationResult) {
	if f := strings.TrimSpace(cfg.Review.Focus); f != "" && cfg.Review.FocusInUse() != strings.ToLower(f) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "review",
			Key:     "focus",
			Message: fmt.Sprintf("The review focus %q is not diff or files. A review opens on the diff.", f),
		})
	}
}
