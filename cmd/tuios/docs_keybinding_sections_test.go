package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsStateNoCountOfKeybindingSections: the number of sections under
// [keybindings] is whatever KeybindingsConfig holds today, and four docs once
// gave four different numbers, none of them right. The docs say "the sections
// under [keybindings]" and let the config struct and tuios keybinds list be the
// count. A number next to those words comes back as the same drift.
func TestDocsStateNoCountOfKeybindingSections(t *testing.T) {
	count := regexp.MustCompile("(?i)\\b(all |of the |one of the )?\\d+ (\\[keybindings\\] |`\\[keybindings\\]` |keybinding |keybind )?sections")
	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no docs found: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if m := count.FindString(line); m != "" && strings.Contains(strings.ToLower(line), "keybind") {
				t.Errorf("%s:%d counts the keybinding sections (%q): name them without a number", filepath.Base(f), i+1, m)
			}
		}
	}
}
