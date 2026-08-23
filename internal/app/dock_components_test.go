package app

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// TestDefaultDockListsReproduceTheDefaultBar is the promise made to everyone
// who has no [dock] table: writing the arrangement down as three lists changed
// nothing about the bar it draws.
//
// It compares the frame drawn from an explicit default plan against the one
// drawn from no config at all, cell for cell including the styling, because a
// difference in escape sequences is a difference on screen.
func TestDefaultDockListsReproduceTheDefaultBar(t *testing.T) {
	for _, width := range []int{60, 80, 120, 160} {
		t.Run(fmt.Sprintf("w%d", width), func(t *testing.T) {
			bare := dockCrowdedOS(t, width, 2, 1)
			bare.UserConfig = nil
			bare.dockPlan = dockPlan{}
			bareBar, _ := bare.renderDockString()

			left, center, right := config.DefaultDockLeft(), config.DefaultDockCenter(), config.DefaultDockRight()
			listed := dockCrowdedOS(t, width, 2, 1)
			listed.UserConfig = &config.UserConfig{Dock: config.DockConfig{
				Left: &left, Center: &center, Right: &right,
			}}
			listed.dockPlan = dockPlan{}
			listedBar, _ := listed.renderDockString()

			if bareBar != listedBar {
				t.Fatalf("the default lists draw a different bar at width %d\n bare:   %q\n listed: %q",
					width, bareBar, listedBar)
			}
		})
	}
}

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

// TestDockListsDropWhatTheyOmit checks the other direction: a component left
// out of the lists is not drawn, which is how a user hides a segment that used
// to need its own option.
func TestDockListsDropWhatTheyOmit(t *testing.T) {
	empty := []string{}
	only := []string{"mode"}
	m := dockCrowdedOS(t, 120, 2, 1)
	m.UserConfig = &config.UserConfig{Dock: config.DockConfig{
		Left: &only, Center: &empty, Right: &empty,
	}}
	m.dockPlan = dockPlan{}
	bar, _ := m.renderDockString()

	if strings.Contains(bar, "1:") {
		t.Errorf("the workspace readout is drawn while omitted from the lists: %q", bar)
	}
	if len(m.dockSessionHits) != 0 {
		t.Error("the session controls are drawn while omitted from the lists")
	}
	if len(m.dockWorkspaceHits) != 0 {
		t.Error("the workspace strip is drawn while omitted from the lists")
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

// TestDockComponentsListsWhatIsPlaced is the enumeration an agent reads: every
// placed component, in draw order, with the side it is on.
func TestDockComponentsListsWhatIsPlaced(t *testing.T) {
	left := []string{"mode", "custom/branch"}
	center := []string{"windows"}
	right := []string{"clock", "session-controls"}
	m := dockCrowdedOS(t, 120, 2, 1)
	m.UserConfig = &config.UserConfig{Dock: config.DockConfig{
		Left: &left, Center: &center, Right: &right,
		Custom: map[string]config.DockCustomConfig{
			"branch": {Command: "echo main", Refresh: "event:after-focus-change"},
		},
	}}
	m.dockPlan = dockPlan{}

	got := m.DockComponents()
	want := []string{"mode", "custom/branch", "windows", "clock", "session-controls"}
	if len(got) != len(want) {
		t.Fatalf("listed %d components, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("component %d is %q, want %q (draw order)", i, got[i].Name, w)
		}
	}
	if got[1].Source != "custom" {
		t.Errorf("custom/branch is reported as %q", got[1].Source)
	}
	if got[0].Source != "builtin" {
		t.Errorf("mode is reported as %q", got[0].Source)
	}
	if got[0].Side != "left" || got[2].Side != "center" || got[3].Side != "right" {
		t.Errorf("sides are wrong: %q %q %q", got[0].Side, got[2].Side, got[3].Side)
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
