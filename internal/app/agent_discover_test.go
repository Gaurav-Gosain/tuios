package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestPaletteFindsAgentActionsByWord: typing "agent" lists the agent actions
// first. It found "Window management mode", which fuzzy-matches the letters.
func TestPaletteFindsAgentActionsByWord(t *testing.T) {
	got := FilterCommandPalette(GetCommandPaletteItems(&config.Global), "agent")
	if len(got) < 4 {
		t.Fatalf("\"agent\" found %d entries", len(got))
	}
	for i, it := range got[:4] {
		if !strings.HasPrefix(it.Name, "Agents: ") {
			t.Errorf("result %d for \"agent\" is %q, want an agent action", i, it.Name)
		}
	}
	// The overlays' own words still find them.
	for _, q := range []string{"inbox", "mailbox"} {
		if res := FilterCommandPalette(GetCommandPaletteItems(&config.Global), q); len(res) == 0 || !strings.HasPrefix(res[0].Name, "Agents: ") {
			t.Errorf("%q no longer finds its agent entry first: %v", q, res)
		}
	}
}

// TestHelpHasAnAgentsSection: one section gathers the agent keys: the prefix
// chords, the rail's controls, the palette's @ filter and the Inbox's own
// keys. They were split over Prefix and Rail, and the Inbox's were nowhere.
func TestHelpHasAnAgentsSection(t *testing.T) {
	cfg := config.DefaultConfig()
	reg := config.NewKeybindRegistry(cfg)
	s := config.Global
	s.LeaderKey = cfg.Keybindings.LeaderKey
	var agents *HelpCategory
	cats := GetHelpCategories(reg, &s)
	for i := range cats {
		if cats[i].Name == HelpCategoryAgents {
			agents = &cats[i]
		}
	}
	if agents == nil {
		t.Fatal("help has no Agents section")
	}
	var text []string
	for _, b := range agents.Bindings {
		text = append(text, strings.Join(b.Keys, ",")+" "+b.Description)
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{"ctrl+b i Open the Inbox", "ctrl+b o", "@n needs you", "rail f", "space Inbox: read", "1-9 Inbox: answer"} {
		if !strings.Contains(all, want) {
			t.Errorf("the Agents section lacks %q:\n%s", want, all)
		}
	}
}
