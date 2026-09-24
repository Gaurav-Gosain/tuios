package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/skills"
)

// TestSkillShowsDrivingAnotherWindow keeps the recipe for the most common
// thing an agent does with another window at the top of the core: open it
// with a name, send it keys by that name, and read it back. An agent that
// could not find it sent arrows with no window, and the person's client
// handed them to the agent's own pane.
func TestSkillShowsDrivingAnotherWindow(t *testing.T) {
	core := skills.TUIOS
	recipe := strings.Index(core, "## Drive other windows")
	if recipe < 0 {
		t.Fatal("the core has no section on driving other windows")
	}
	if running := strings.Index(core, "## Running work"); running >= 0 && recipe > running {
		t.Error("the recipe for driving other windows comes after running work; keep it near the top")
	}
	section := core[recipe:]
	if end := strings.Index(section[2:], "\n## "); end >= 0 {
		section = section[:end+2]
	}
	for _, want := range []string{
		"tuios new-window",
		"--no-focus",
		"tuios send-text",
		"tuios wait-for",
		"send-keys -s \"$TUIOS_SESSION\" -w docs Down --repeat",
		"PageDown",
		"tuios capture-pane",
		"--print-id",
		"Always pass `-w`",
		"tuios --skill panes",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the recipe for driving other windows no longer shows %q", want)
		}
	}
}

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
