package config

import (
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// WorktreeConfig is the [worktree] table: where tuios worktree new, the rail's
// new worktree dialog and tuios fan put a worktree. The daemon makes every one
// of them, so it is the side that reads it, when it starts and when the file
// changes.
type WorktreeConfig struct {
	// Path is a path template, such as
	// "{{ repo_path }}/.worktrees/{{ branch | slug }}". See
	// worktree.RenderPath for what it may hold. Empty is the default,
	// $XDG_DATA_HOME/tuios/worktrees/<repo>/<branch slug>.
	Path string `toml:"path"`
}

func validateWorktree(cfg *UserConfig, result *ValidationResult) {
	if err := worktree.ValidatePathTemplate(cfg.Worktree.Path); err != nil {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "worktree", Key: "path",
			Message: err.Error() + "; new worktrees are refused until it is fixed",
		})
	}
}
