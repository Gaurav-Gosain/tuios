package config

// WorkspacesConfig is the [workspaces] section. The daemon reads
// return_when_empty: it owns the window set, so it is the side that sees a
// workspace lose its last pane. The client reads new_window_when_empty: it
// is the side that knows a switch came from the person and not from a
// command that brings its own panes.
type WorkspacesConfig struct {
	// ReturnWhenEmpty switches the session back to the workspace the person
	// came from when the workspace on screen loses its last pane, instead of
	// leaving the splash screen up. A pointer so an explicit false in the
	// file is told apart from a file that never mentions it (default: true).
	ReturnWhenEmpty *bool `toml:"return_when_empty"`
	// NewWindowWhenEmpty opens a window when the person switches to a
	// workspace that has no panes, as the new-window key would. The window
	// starts in the directory of the pane focused on the workspace they
	// came from (default: false).
	NewWindowWhenEmpty bool `toml:"new_window_when_empty"`
}

// ReturnsWhenEmpty reports whether return_when_empty is on. Unset means on.
func (w WorkspacesConfig) ReturnsWhenEmpty() bool {
	return w.ReturnWhenEmpty == nil || *w.ReturnWhenEmpty
}
