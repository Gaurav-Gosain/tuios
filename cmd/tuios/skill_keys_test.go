package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/skills"
)

// TestSkillKeyTableListsEveryKeyName holds the key table in the panes topic to
// the names the daemon's parser accepts, so a name added to one is added to the
// other.
func TestSkillKeyTableListsEveryKeyName(t *testing.T) {
	panes, err := skills.Lookup("panes")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(panes, "### Key names")
	if start < 0 {
		t.Fatal("the panes topic has no key table")
	}
	table := panes[start:]
	if end := strings.Index(table[1:], "\n### "); end >= 0 {
		table = table[:end+1]
	}
	for _, name := range session.KeyNames() {
		if strings.HasPrefix(name, "F") && name != "F1" && name != "F12" {
			continue // listed as F1 to F12
		}
		if !strings.Contains(table, "`"+name+"`") {
			t.Errorf("the key table does not list %s", name)
		}
	}
}
