package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// TestDockPlanDropsNamesNothingDefines is the other half of the config warning:
// a name no built-in and no [dock.custom] table defines draws nothing rather
// than drawing a placeholder or panicking.
func TestDockPlanDropsNamesNothingDefines(t *testing.T) {
	left := []string{"mode", "not-a-component", "custom/undefined", "trail"}
	plan := buildDockPlan(&config.UserConfig{Dock: config.DockConfig{Left: &left}})
	if got := plan.Left; !slices.Equal(got, []string{"mode", "trail"}) {
		t.Fatalf("plan.Left = %v, want the two names that exist", got)
	}
	if plan.Has("not-a-component") || plan.Has("custom/undefined") {
		t.Fatal("a name nothing defines was placed")
	}
}

// TestDockEventAliasesMatchTheValidatedSet keeps the two halves of the event
// vocabulary in step. internal/config warns about a name outside its list and
// internal/app is what actually fires them, so a name in one and not the other
// is either a warning about a working contract or a contract that silently
// never fires.
func TestDockEventAliasesMatchTheValidatedSet(t *testing.T) {
	valid := config.DockEventTypes()
	for hook, hub := range dockEventAliases {
		if !slices.Contains(valid, hook) {
			t.Errorf("hook event %q fires a dock refresh but the config warns about it", hook)
		}
		if !slices.Contains(valid, hub) {
			t.Errorf("hub event %q fires a dock refresh but the config warns about it", hub)
		}
	}
	for _, event := range hooks.AllEvents() {
		// Only the daemon follows a shell's OSC 133 marks, so no client ever
		// sees a command finish and a dock component could not refresh on it.
		// Accepting the name would be a contract that never fires.
		if event == hooks.AfterCommandFinished {
			if slices.Contains(valid, string(event)) {
				t.Errorf("%q is accepted as a dock event, but no client fires it", event)
			}
			continue
		}
		if _, ok := dockEventAliases[string(event)]; !ok {
			t.Errorf("hook event %q has no dock alias, so a component cannot watch it by its hub name", event)
		}
	}
	for _, name := range valid {
		fires := false
		for hook, hub := range dockEventAliases {
			if name == hook || name == hub {
				fires = true
				break
			}
		}
		if !fires {
			t.Errorf("config accepts event %q but nothing ever fires it", name)
		}
	}
}

// TestDockFixedSideComponentsDrawWhereTheyBelong pins the placement rule for
// the four components that are not cells. Naming "windows" on the left is taken
// as "draw the minimized entries", not as "draw them on the left": the entries
// are the centre block and are measured against the room the two ends leave, so
// honouring the position would reserve width on one side and draw on another.
func TestDockFixedSideComponentsDrawWhereTheyBelong(t *testing.T) {
	left := []string{"mode", "windows", "session-controls"}
	center := []string{}
	right := []string{}
	plan := buildDockPlan(&config.UserConfig{Dock: config.DockConfig{
		Left: &left, Center: &center, Right: &right,
	}})

	if !slices.Equal(plan.Left, []string{"mode"}) {
		t.Errorf("plan.Left = %v, want only the component that goes where it is listed", plan.Left)
	}
	if !slices.Equal(plan.Center, []string{"windows"}) {
		t.Errorf("plan.Center = %v, want the minimized entries", plan.Center)
	}
	if !slices.Equal(plan.Right, []string{"session-controls"}) {
		t.Errorf("plan.Right = %v, want the session controls", plan.Right)
	}
	if !plan.Has("windows") || !plan.Has("session-controls") {
		t.Error("a misplaced component was dropped instead of being moved")
	}

	// It is a warning, not silence: the user wrote something that did not mean
	// what it looked like it meant.
	cfg := &config.UserConfig{Dock: config.DockConfig{Left: &left, Center: &center, Right: &right}}
	warnings := config.ConfigWarnings(cfg)
	found := 0
	for _, w := range warnings {
		if strings.Contains(w, "always drawn on the") {
			found++
		}
	}
	if found != 2 {
		t.Errorf("got %d placement warnings, want 2: %v", found, warnings)
	}
}
