package config

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// appearance.selection.copy_entry picks where copy mode puts its cursor. A
// value it does not know leaves cursor, the default.
func TestCopyEntryConfig(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"", CopyEntryCursor},
		{"[appearance.selection]\ncopy_entry = \"center\"\n", CopyEntryCenter},
		{"[appearance.selection]\ncopy_entry = \"cursor\"\n", CopyEntryCursor},
		{"[appearance.selection]\ncopy_entry = \"top\"\n", CopyEntryCursor},
	} {
		var cfg UserConfig
		if err := toml.Unmarshal([]byte(tc.src), &cfg); err != nil {
			t.Fatalf("unmarshal %q: %v", tc.src, err)
		}
		s := DefaultSettings()
		ApplyAppearanceConfig(&cfg, &s)
		if s.CopyEntry != tc.want {
			t.Errorf("%q gives %q, want %q", tc.src, s.CopyEntry, tc.want)
		}
	}
	if DefaultConfig().Appearance.Selection.CopyEntry != CopyEntryCursor {
		t.Error("the default config does not write copy_entry = cursor")
	}
	opt, ok := LookupOption("appearance.selection.copy_entry")
	if !ok || len(opt.Accepted) != len(CopyEntries) {
		t.Errorf("the option registry does not list copy_entry with its values: %+v", opt)
	}
}

// The copy mode search actions have descriptions, so the keybind manager and
// the help list them, and no default key.
func TestCopyModeSearchActionsRegistered(t *testing.T) {
	r := NewKeybindRegistry(DefaultConfig())
	for _, a := range []string{ActionCopyModeSearchForward, ActionCopyModeSearchBackward} {
		if ActionDescriptions[a] == "" {
			t.Errorf("%s has no description", a)
		}
		if keys := r.GetKeys(a); len(keys) != 0 {
			t.Errorf("%s has default keys %v, want none", a, keys)
		}
	}
}
