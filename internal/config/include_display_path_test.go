package config

import (
	"path/filepath"
	"testing"
)

// A file in a folder whose name starts with two dots, such as "..foo", is
// inside the config folder. displayPath once tested the relative path with
// strings.HasPrefix(rel, ".."), which took "..foo" for a step up and printed
// the full path instead.
func TestDisplayPathKeepsADotDotNameInside(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "config.toml")
	for p, want := range map[string]string{
		filepath.Join(dir, "..foo", "keys.toml"): filepath.Join("..foo", "keys.toml"),
		filepath.Join(dir, "..keys.toml"):        "..keys.toml",
		filepath.Join(dir, "keys.toml"):          "keys.toml",
		main:                                     "config.toml",
	} {
		if got := displayPath(main, p); got != want {
			t.Errorf("displayPath(%q) = %q, want %q", p, got, want)
		}
	}
	outside := filepath.Join(filepath.Dir(dir), "elsewhere.toml")
	if got := displayPath(main, outside); got == "elsewhere.toml" || got == filepath.Join("..", "elsewhere.toml") {
		t.Errorf("a file outside the config folder showed as %q", got)
	}
}
