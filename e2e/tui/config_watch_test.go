package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
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

// TestAnEditBackToATuiosSaveReachesTheScreen is issue 1411, the steps as the
// report gives them. Leader Z twice has tuios save the file. A hand edit turns the
// beam on, and a hand edit back to the bytes tuios saved has to turn it off, since
// the beam is what is in force at that moment.
//
// The second half is tuios config apply, which reaches a client whose file has not
// changed under its watcher. An included file in a folder made after the client
// started gives no event, so the client has not read it until the command runs.
//
// Negative controls: with onlySelfWrites ignoring the number it has got to, the
// beam stays on after the edit back. With askClientsToApply cut, the included
// file never reaches the screen.
func TestAnEditBackToATuiosSaveReachesTheScreen(t *testing.T) {
	const lateInclude = "include = [\"late/beam.toml\"]\n"
	base := spotlightConfigFile(t, lateInclude+spotlightNoTheme)
	killDaemon(t, base)
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "e2e-edit-back"}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted: %v\n%s", err, term.Snapshot())
	}
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term,
		`printf '%sTOP\n' "$(echo INK)"; printf '\n%.0s' $(seq 1 12); printf '%sBOT\n' "$(echo INK)"`,
		"INKBOT", shellTimeout)
	at := findSpotlightMarks(t, term)
	faint := func(want bool) func(tuitest.Screen) bool {
		return func(s tuitest.Screen) bool { return s.Cell(at.topCol, at.topRow).Faint == want }
	}

	// tuios saves calm on and calm off. Each save changes the file, and the second
	// leaves calm = false in it.
	for _, want := range []string{"calm = true", "calm = false"} {
		if err := term.SendKeys(tuitest.Ctrl('b'), "Z"); err != nil {
			t.Fatalf("send leader Z: %v", err)
		}
		deadline := time.Now().Add(configWatchTimeout)
		for {
			data, err := os.ReadFile(configPathIn(base))
			if err == nil && strings.Contains(string(data), want) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("tuios never saved %q:\n%s", want, data)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	saved, err := os.ReadFile(configPathIn(base))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "[spotlight]") {
		t.Fatalf("the save holds a spotlight table, so the edit below cannot add one:\n%s", saved)
	}

	beam := strings.TrimPrefix(spotlightNoThemeBeam(95), spotlightNoTheme)
	saveConfigLikeAnEditor(t, base, string(saved)+beam)
	if err := term.WaitFor(faint(true), configWatchTimeout); err != nil {
		t.Fatalf("a hand edit after a tuios save never reached the screen: %v\n%s", err, term.Snapshot())
	}
	saveConfigLikeAnEditor(t, base, string(saved))
	if err := term.WaitFor(faint(false), configWatchTimeout); err != nil {
		t.Fatalf("an edit back to the bytes of a tuios save never reached the screen: %v\n%s", err, term.Snapshot())
	}

	late := filepath.Join(filepath.Dir(configPathIn(base)), "late")
	if err := os.Mkdir(late, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(late, "beam.toml"), []byte(beam), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(faint(true), 4*time.Second); err == nil {
		t.Fatalf("the client read the late file with no request, so this cannot show what the command does\n%s", term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "config", "apply"); err != nil {
		t.Fatalf("tuios config apply: %v\n%s", err, out)
	}
	if err := term.WaitFor(faint(true), configWatchTimeout); err != nil {
		t.Fatalf("tuios config apply never reached the client: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the edits and tuios config apply")
}

// TestAKeybindingSavedOnDiskReachesTheClient: the hot reload carries
// keybindings too. A chord that did nothing before the save fires after it,
// pressed on the same running client, and the binding the config started with
// still works.
func TestAKeybindingSavedOnDiskReachesTheClient(t *testing.T) {
	base := t.TempDir()
	late := filepath.Join(base, "late.fired")
	writeConfig(t, base, keybindConfigFor("late", "y", late))
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	pressChord(t, term, 'y')
	waitForFile(t, term, late, "the startup config's binding")

	added := filepath.Join(base, "added.fired")
	pressChord(t, term, 'h')
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(added); err == nil {
		t.Fatalf("prefix+alt+h fired with no binding behind it, so a pass after the "+
			"save would prove nothing\n%s", term.Snapshot())
	}

	saveConfigLikeAnEditor(t, base, keybindConfigFor("late", "y", late)+keybindConfigFor("added", "h", added))
	deadline := time.Now().Add(configWatchTimeout)
	for {
		pressChord(t, term, 'h')
		if _, err := os.Stat(added); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a keybinding saved on disk never reached the running client\n%s",
				term.Snapshot())
		}
		time.Sleep(300 * time.Millisecond)
	}

	// The second save drops the h binding and points y at a new marker, so
	// firing y proves the reloaded table replaced the old one instead of
	// stacking on top of it.
	after := filepath.Join(base, "after.fired")
	saveConfigLikeAnEditor(t, base, keybindConfigFor("late2", "y", after))
	deadline = time.Now().Add(configWatchTimeout)
	for {
		pressChord(t, term, 'y')
		if _, err := os.Stat(after); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the surviving binding never fired after a second reload\n%s",
				term.Snapshot())
		}
		time.Sleep(300 * time.Millisecond)
	}
	alive(t, term, "after two config reloads")
}

// keybindConfigFor is one [[keybindings.command]] entry: the chord alt+key
// touches marker. A fired chord leaves evidence on disk instead of on a
// screen the test would have to read. The name is explicit because two
// nameless entries whose command text shares a 40-character prefix slug to
// the same name and tuios keeps only the first.
func keybindConfigFor(name, key, marker string) string {
	return `
[[keybindings.command]]
name = "` + name + `"
key = "prefix+alt+` + key + `"
type = "shell"
command = "touch ` + marker + `"
`
}

func pressChord(t *testing.T, term *tuitest.Terminal, k rune) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Alt(k)); err != nil {
		t.Fatalf("send the chord alt+%c: %v", k, err)
	}
}

func waitForFile(t *testing.T, term *tuitest.Terminal, path, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not fire\n%s", what, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
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
