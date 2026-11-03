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
	// Scope is what a review shows when it opens: base, the changes since
	// the base, or uncommitted, the changes not yet committed (default:
	// base). u still switches between them. A diff of two attempts ignores
	// it.
	Scope string `toml:"scope"`
}

// The diffs a review can open on.
const (
	// ReviewScopeBase is the changes since the base.
	ReviewScopeBase = "base"
	// ReviewScopeUncommitted is the changes not yet committed.
	ReviewScopeUncommitted = "uncommitted"
)

// ReviewScopes is the accepted scope values.
var ReviewScopes = []string{ReviewScopeBase, ReviewScopeUncommitted}

// OpensUncommitted reports whether a review opens on the uncommitted
// changes. An unknown scope is the base.
func (r ReviewConfig) OpensUncommitted() bool {
	return strings.EqualFold(strings.TrimSpace(r.Scope), ReviewScopeUncommitted)
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
	return ReviewConfig{Focus: ReviewOpenOnDiff, Scope: ReviewScopeBase}
}

// fillMissingReview fills an absent value with its default.
func fillMissingReview(cfg, defaultCfg *UserConfig) {
	if strings.TrimSpace(cfg.Review.Focus) == "" {
		cfg.Review.Focus = defaultCfg.Review.Focus
	}
	if strings.TrimSpace(cfg.Review.Scope) == "" {
		cfg.Review.Scope = defaultCfg.Review.Scope
	}
}

// validateReview warns about a focus or a scope that falls back to its
// default.
func validateReview(cfg *UserConfig, result *ValidationResult) {
	if f := strings.TrimSpace(cfg.Review.Focus); f != "" && cfg.Review.FocusInUse() != strings.ToLower(f) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "review",
			Key:     "focus",
			Message: fmt.Sprintf("The review focus %q is not diff or files. A review opens on the diff.", f),
		})
	}
	if sc := strings.TrimSpace(cfg.Review.Scope); sc != "" && !cfg.Review.OpensUncommitted() && !strings.EqualFold(sc, ReviewScopeBase) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field:   "review",
			Key:     "scope",
			Message: fmt.Sprintf("The review scope %q is not base or uncommitted. A review opens on the changes since the base.", sc),
		})
	}
}
