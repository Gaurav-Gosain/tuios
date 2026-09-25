package config

import "testing"

// AppearanceFrom is the per-session seed a server builds instead of reading the
// process globals: the config file as it is now, then the server's own flags. A
// session that connects after an edit follows the edit.
func TestAppearanceFromLayersFileThenFlags(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Appearance.BorderStyle = "double"

	if got := AppearanceFrom(cfg, Overrides{}).BorderStyle; got != "double" {
		t.Errorf("BorderStyle from the file = %q, want double", got)
	}

	// A flag wins over the file, the same order the process globals get.
	if got := AppearanceFrom(cfg, Overrides{BorderStyle: "single"}).BorderStyle; got != "single" {
		t.Errorf("BorderStyle with a flag = %q, want single", got)
	}
}
