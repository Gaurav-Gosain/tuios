package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// agentChrome is what a client shows of its agent chrome: the prefix menu's
// Inbox lines, the palette's agent entries and "@ state" hint, the help's
// Agents section, and whether the Alerts tab shows its agent rows.
type agentChrome struct {
	prefixLines, paletteEntries, stateHint, helpSection, alertRows bool
}

func readAgentChrome(m *OS) agentChrome {
	var c agentChrome
	for _, b := range m.prefixMenuBindings() {
		if config.IsAgentPrefixKeybinding(b) {
			c.prefixLines = true
		}
	}
	m.rebuildPaletteItems()
	for _, it := range m.allPaletteItems() {
		if it.Category == paletteCategoryAgents {
			c.paletteEntries = true
		}
	}
	for _, h := range m.paletteFooter() {
		if h.Key == "@" {
			c.stateHint = true
		}
	}
	for _, cat := range m.HelpCategories() {
		if cat.Name == HelpCategoryAgents {
			c.helpSection = true
		}
	}
	for _, cat := range m.settingsCategories() {
		for _, it := range cat.Items {
			if it.Path == "notifications.agent.enabled" {
				c.alertRows = true
			}
		}
	}
	return c
}

// TestAgentChromeWaitsForAnAgent: a person who has never run an agent does
// not see the agent lines of the prefix menu, the palette's agent entries and
// "@ state" hint, the help's Agents section, or the agent rows of the Alerts
// settings. The first agent state brings all of them, and the client
// remembers. Nothing is removed: the keys work throughout.
func TestAgentChromeWaitsForAnAgent(t *testing.T) {
	withSidebar(t, true, "right", config.SidebarDefaultWidth)
	m := newTestOS(&terminal.Window{ID: "w-1", Width: 40, Height: 20, Workspace: 1})
	m.Settings = config.Global
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	if got := readAgentChrome(m); got != (agentChrome{}) {
		t.Fatalf("with no agent seen the client shows %+v", got)
	}
	// The Alerts tab says what it folded.
	for _, cat := range m.settingsCategories() {
		if cat.Name != "Alerts" {
			continue
		}
		last := cat.Items[len(cat.Items)-1]
		if last.Label != "Agents" || !strings.Contains(last.value(m), "hidden") {
			t.Errorf("the Alerts tab ends in %q = %q, want the folded Agents heading", last.Label, last.value(m))
		}
	}

	m.noteAgentState(m.Windows[0], "working")
	want := agentChrome{true, true, true, true, true}
	if got := readAgentChrome(m); got != want {
		t.Errorf("with an agent seen the client shows %+v, want all of it", got)
	}
	if !m.SidebarAgentsSeen {
		t.Error("the first agent state was not remembered")
	}

	// Once seen, it stays: the agent leaving does not take the chrome away.
	m.noteAgentState(m.Windows[0], "")
	if got := readAgentChrome(m); got != want {
		t.Errorf("after the agent left the client shows %+v", got)
	}
}
