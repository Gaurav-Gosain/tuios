package tuie2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Editing config.toml in one pane has to reach the tuios running in the next
// one. These drive the real file: a running client, a save on disk, and the
// screen afterwards.
//
// The trap under it is that an editor does not write through the inode. vim
// writes a temporary file and renames it into place, so a watch on the file
// itself is dead after the first :w. saveConfigLikeAnEditor is that save.

// configPathIn is where a client started with startIn reads its config from.
func configPathIn(base string) string {
	return filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
}

// saveConfigLikeAnEditor replaces the config the way vim does: a new file
// beside it, renamed over the old one.
func saveConfigLikeAnEditor(t *testing.T, base, body string) {
	t.Helper()
	path := configPathIn(base)
	tmp := path + ".4913"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatalf("write the new config: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename the new config into place: %v", err)
	}
}

// configWatchTimeout covers the debounce plus the reload plus the frame.
const configWatchTimeout = 15 * time.Second

// TestConfigEditedOnDiskReachesTheScreen is the whole feature, driven through
// the file rather than through the settings page.
//
// The beam is what it turns on, because a beam is measurable: the marker at the
// head of the pane is at full brightness with the beam off and dimmed with it
// on, and nothing else on screen has to move for the assertion to read.
func TestConfigEditedOnDiskReachesTheScreen(t *testing.T) {
	base := spotlightConfigFile(t, spotlightNoTheme)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term,
		`printf '%sTOP\n' "$(echo INK)"; printf '\n%.0s' $(seq 1 12); printf '%sBOT\n' "$(echo INK)"`,
		"INKBOT", shellTimeout)

	at := findSpotlightMarks(t, term)
	if term.Screen().Cell(at.topCol, at.topRow).Faint {
		t.Fatalf("the marker is already faint with no beam configured; this cannot show "+
			"what the reload did\n%s", term.Snapshot())
	}

	saveConfigLikeAnEditor(t, base, spotlightNoThemeBeam(95))

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(at.topCol, at.topRow).Faint
	}, configWatchTimeout); err != nil {
		t.Fatalf("a config saved on disk never reached the screen: %v\n%s", err, term.Snapshot())
	}

	// And the other way, so this is a reload and not a one-way switch. A watch
	// on the inode is dead after the save above, so a second save proves the
	// watch survived the first.
	saveConfigLikeAnEditor(t, base, spotlightNoTheme)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !s.Cell(at.topCol, at.topRow).Faint
	}, configWatchTimeout); err != nil {
		t.Fatalf("a second config save never reached the screen, so the watch died on the "+
			"first one: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after two config reloads")
}

// TestBrokenConfigOnDiskKeepsWhatIsRunning. A file caught half written, or one
// with an unbalanced quote, must leave the session exactly as it is and say so.
// The reload used to write the failure to a log nobody reads, so a typo meant
// every later save was ignored for a reason nobody could see.
func TestBrokenConfigOnDiskKeepsWhatIsRunning(t *testing.T) {
	base := spotlightConfigFile(t, spotlightNoThemeBeam(95))
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term,
		`printf '%sTOP\n' "$(echo INK)"; printf '\n%.0s' $(seq 1 12); printf '%sBOT\n' "$(echo INK)"`,
		"INKBOT", shellTimeout)

	at := findSpotlightMarks(t, term)
	if !term.Screen().Cell(at.topCol, at.topRow).Faint {
		t.Fatalf("the beam is not on, so this cannot show that a broken file left it "+
			"alone\n%s", term.Snapshot())
	}

	saveConfigLikeAnEditor(t, base, "[appearance\ntheme = \"nord")

	if err := term.WaitForText("Config not reloaded", configWatchTimeout); err != nil {
		t.Fatalf("a config file that does not parse said nothing on screen: %v\n%s",
			err, term.Snapshot())
	}
	if !term.Screen().Cell(at.topCol, at.topRow).Faint {
		t.Errorf("a config file that does not parse turned the beam off\n%s", term.Snapshot())
	}

	// And the fix is taken. A reload that recorded the broken file as the one in
	// force would go quiet here, which is the failure that hides itself.
	saveConfigLikeAnEditor(t, base, spotlightNoTheme)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !s.Cell(at.topCol, at.topRow).Faint
	}, configWatchTimeout); err != nil {
		t.Fatalf("the config was never reloaded after the typo was fixed: %v\n%s",
			err, term.Snapshot())
	}
	alive(t, term, "after a broken config file")
}
