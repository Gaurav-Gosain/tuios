package config

// PathPickerConfig is the [path_picker] table: what the path picker lists.
// The picker (pick_path, alt+; in terminal mode) lists the focused pane's
// folder at the cursor and types the chosen path into the pane.
//
// It is read each time the picker opens, so an edit to the file is in force
// the next time.
type PathPickerConfig struct {
	// ShowHidden lists the names that start with a dot (default: false).
	// alt+h in the picker shows or hides them until it closes.
	ShowHidden bool `toml:"show_hidden"`
}
