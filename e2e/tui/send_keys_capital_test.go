package tuie2e

import (
	"strings"
	"testing"
)

// TestSendKeysCapitalAfterPrefix sends "PREFIX P" with send-keys. P after the
// leader opens the command palette, and p goes to the previous window. The
// client's key parser lowercased a capital letter, so send-keys ran the binding
// for p and showed a toast instead of the palette.
//
// The positive half: "PREFIX p" from the same client does not open the
// palette, so the palette opening is the capital's doing.
func TestSendKeysCapitalAfterPrefix(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "home"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "the first pane")

	if out, err := tuiosCLI(t, base, "send-keys", "-s", "home", "PREFIX p"); err != nil {
		t.Fatalf("send-keys PREFIX p: %v\n%s", err, out)
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	if strings.Contains(term.Screen().Text(), paletteTitle) {
		t.Fatalf("PREFIX p opened the command palette\n%s", term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "send-keys", "-s", "home", "PREFIX P"); err != nil {
		t.Fatalf("send-keys PREFIX P: %v\n%s", err, out)
	}
	waitPaletteOpen(t, term, "after send-keys PREFIX P")
	saveArtifact(t, term, artifactDir(t), "palette")
}
