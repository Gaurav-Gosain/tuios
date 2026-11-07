package config

import (
	"fmt"
	"strings"

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
	// Setup is a shell line the daemon runs with sh -c in every new
	// worktree, in a window named setup, before the agent or shell starts.
	// Empty runs nothing. It holds no grants. See verb_worktree_setup.go in
	// internal/session.
	Setup string `toml:"setup"`
	// SetupAsync starts the agent at once, beside the setup, instead of
	// after it passes. A failed async setup keeps the worktree and the
	// branch, because the agent may already have work in them.
	SetupAsync bool `toml:"setup_async"`
}

// WorktreeSetupMaxBytes bounds [worktree] setup and the setup a call passes:
// a shell line, not a script.
const WorktreeSetupMaxBytes = 4096

func validateWorktree(cfg *UserConfig, result *ValidationResult) {
	if err := worktree.ValidatePathTemplate(cfg.Worktree.Path); err != nil {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "worktree", Key: "path",
			Message: err.Error() + "; new worktrees are refused until it is fixed",
		})
	}
	if setup := cfg.Worktree.Setup; len(setup) > WorktreeSetupMaxBytes || strings.ContainsRune(setup, 0) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "worktree", Key: "setup",
			Message: fmt.Sprintf("setup is a shell line of at most %d bytes with no NUL byte; new worktrees run no setup until it is fixed", WorktreeSetupMaxBytes),
		})
	}
}
