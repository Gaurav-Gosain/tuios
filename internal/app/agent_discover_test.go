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
