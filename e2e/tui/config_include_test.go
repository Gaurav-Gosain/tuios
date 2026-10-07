package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A config split over several files (#518): config.toml names more files in a
// top-level include list, and every *.toml file in config.d beside it is read
// too. These drive the real binary against real files: what loads at start,
// what a save to an included file does to a running client, and where each
// command that writes the config puts its change.

// configDirIn is the tuios config directory of the isolation root.
func configDirIn(base string) string {
	return filepath.Dir(configPathIn(base))
}

// writeConfigPart writes one more config file, relative to the config
// directory, the way an editor saves: a temporary file renamed into place.
func writeConfigPart(t *testing.T, base, rel, body string) string {
	t.Helper()
	path := filepath.Join(configDirIn(base), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the directory of %s: %v", rel, err)
	}
	tmp := path + ".4913"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename %s into place: %v", rel, err)
	}
	return path
}

// readFileString reads a file the test made, failing the test when it cannot.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a file of this test's own
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// waitForFileText polls path until want accepts its content.
func waitForFileText(t *testing.T, path string, want func(string) bool, why string) string {
	t.Helper()
	deadline := time.Now().Add(configWatchTimeout)
	var got string
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil { //nolint:gosec // a file of this test's own
			got = string(data)
			if want(got) {
				return got
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s\n%s now holds:\n%s", why, path, got)
	return got
}

// pressUntilFile presses the chord until its marker appears.
func pressUntilFile(t *testing.T, term *tuitest.Terminal, k rune, marker, why string) {
	t.Helper()
	deadline := time.Now().Add(configWatchTimeout)
	for {
		pressChord(t, term, k)
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: %s\n%s", why, term.Snapshot())
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// TestIncludedConfigFilesLoadAndReload: an included file applies at start, a
// save to it reaches the running client, a config.d directory made after
// start is followed, and an include that named a missing file applies once
// the file is there. Each step adds a binding whose chord touches a marker,
// so a fired chord is the proof the file was read.
func TestIncludedConfigFilesLoadAndReload(t *testing.T) {
	base := t.TempDir()
	late := filepath.Join(base, "late.fired")
	writeConfig(t, base, `include = ["keys.toml", "later.toml"]`+"\n")
	writeConfigPart(t, base, "keys.toml", keybindConfigFor("late", "y", late))

	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	// At start: the binding is in keys.toml and nowhere else.
	pressChord(t, term, 'y')
	waitForFile(t, term, late, "the binding in the included keys.toml")

	// The positive half of the reload steps: h is not bound yet.
	added := filepath.Join(base, "added.fired")
	pressChord(t, term, 'h')
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(added); err == nil {
		t.Fatalf("prefix+alt+h fired with no binding behind it, so a pass after the "+
			"save would prove nothing\n%s", term.Snapshot())
	}

	// A save to the included file, not to config.toml.
	writeConfigPart(t, base, "keys.toml",
		keybindConfigFor("late", "y", late)+keybindConfigFor("added", "h", added))
	pressUntilFile(t, term, 'h', added,
		"a binding saved in an included file never reached the running client")

	// config.d did not exist at start. A file in it arrives with the
	// directory, and its binding merges with the included file's by name.
	dropped := filepath.Join(base, "dropped.fired")
	writeConfigPart(t, base, filepath.Join("config.d", "50-more.toml"), keybindConfigFor("dropped", "j", dropped))
	pressUntilFile(t, term, 'j', dropped,
		"a binding in a config.d file made after start never reached the running client")

	// later.toml was missing at start, which is a warning and not an error.
	// The file appearing is a change like any other.
	later := filepath.Join(base, "later.fired")
	writeConfigPart(t, base, "later.toml", keybindConfigFor("later", "k", later))
	pressUntilFile(t, term, 'k', later,
		"an included file that appeared after start never reached the running client")

	// The first binding survived every merge.
	_ = os.Remove(late)
	pressUntilFile(t, term, 'y', late, "the binding from keys.toml was lost in a later merge")

	saveArtifact(t, term, artifactDir(t), "after-four-files")
	alive(t, term, "after four config files reloaded")
}

// TestSetConfigWritesTheFileThatHoldsTheKey: a setting changed in a running
// client is written to the included file that sets it, and config.toml is not
// flattened. A setting a read-only file holds goes to config.toml instead, the
// read-only file is not touched, and the client says so on screen.
func TestSetConfigWritesTheFileThatHoldsTheKey(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `include = ["look.toml", "locked.toml"]`+"\n")
	look := writeConfigPart(t, base, "look.toml", "[appearance]\nborder_style = \"double\"\n")
	lockedBody := "[appearance]\nhide_window_buttons = false\n"
	locked := writeConfigPart(t, base, "locked.toml", lockedBody)
	// A Nix store link is read-only. The file mode is what tuios can see.
	if err := os.Chmod(locked, 0o444); err != nil {
		t.Fatalf("make locked.toml read-only: %v", err)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "inc"}})
	waitBoot(t, term)

	setLive(t, base, "border_style", "rounded")
	waitForFileText(t, look, func(s string) bool { return strings.Contains(s, "border_style = 'rounded'") },
		"set-config did not write border_style to look.toml, the file that sets it")
	main := readFileString(t, configPathIn(base))
	if strings.Contains(main, "border_style") {
		t.Fatalf("ASSERTION: set-config wrote border_style into config.toml, which flattens "+
			"the include:\n%s", main)
	}
	if !strings.Contains(main, "look.toml") {
		t.Fatalf("ASSERTION: config.toml lost its include list:\n%s", main)
	}

	setLive(t, base, "hide_window_buttons", "true")
	waitForFileText(t, configPathIn(base), func(s string) bool { return strings.Contains(s, "hide_window_buttons = true") },
		"a setting held by a read-only file did not go to config.toml")
	if got := readFileString(t, locked); got != lockedBody {
		t.Fatalf("ASSERTION: tuios wrote the read-only locked.toml:\n%s", got)
	}
	if err := term.WaitForText("cannot write", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not say the change went to config.toml: %v\n%s", err, term.Snapshot())
	}

	out, err := tuiosCLI(t, base, "config", "origin", "appearance")
	if err != nil {
		t.Fatalf("tuios config origin: %v\n%s", err, out)
	}
	if !originLine(out, "appearance.border_style", "look.toml") {
		t.Fatalf("ASSERTION: config origin does not name look.toml for border_style:\n%s", out)
	}
	if !originLine(out, "appearance.hide_window_buttons", "config.toml") || !strings.Contains(out, "locked.toml") {
		t.Fatalf("ASSERTION: config origin does not show config.toml over locked.toml:\n%s", out)
	}

	dir := artifactDir(t)
	saveArtifact(t, term, dir, "notice")
	saveSyncArtifact(t, "origin.txt", out)
	saveSyncArtifact(t, "config.toml", readFileString(t, configPathIn(base)))
	saveSyncArtifact(t, "look.toml", readFileString(t, look))
}

// originLine reports whether the origin listing has key on a line that names
// file as the source.
func originLine(out, key, file string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == key && strings.HasSuffix(fields[1], file) {
			return true
		}
	}
	return false
}

// TestConfigCommandsWriteTheFileThatHoldsTheKey drives the commands that edit
// the config from a shell: hosts add, hosts remove and keybinds unbind. Each
// writes the included file that holds the key, keeps that file's comments
// where the command edits lines, and never writes a read-only file. config
// files lists every file and says what was skipped.
func TestConfigCommandsWriteTheFileThatHoldsTheKey(t *testing.T) {
	base := t.TempDir()
	env := []string{"TUIOS_SSH=/bin/false"}
	writeConfig(t, base, `include = ["hosts.toml", "keys.toml", "locked.toml", "loop.toml", "absent.toml"]`+"\n")
	hosts := writeConfigPart(t, base, "hosts.toml", "# hosts for this machine only\n[hosts.alpha]\naddr = \"me@alpha\"\n")
	keys := writeConfigPart(t, base, "keys.toml", "[keybindings.window_management]\nclose_window = [\"w\"]\n")
	lockedBody := "[hosts.beta]\naddr = \"me@beta\"\n"
	locked := writeConfigPart(t, base, "locked.toml", lockedBody)
	if err := os.Chmod(locked, 0o444); err != nil {
		t.Fatalf("make locked.toml read-only: %v", err)
	}
	writeConfigPart(t, base, "loop.toml", `include = ["config.toml"]`+"\n")

	var transcript strings.Builder
	run := func(args ...string) (string, error) {
		out, err := tuiosCLIEnv(t, base, env, args...)
		transcript.WriteString("$ tuios " + strings.Join(args, " ") + "\n" + out + "\n")
		return out, err
	}

	out, err := run("config", "files")
	if err != nil {
		t.Fatalf("tuios config files: %v\n%s", err, out)
	}
	for _, want := range []string{"hosts.toml (include)", "locked.toml (include, read-only)", "includes it again", "absent.toml, which does not exist"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ASSERTION: config files does not say %q:\n%s", want, out)
		}
	}

	// A host the included hosts.toml sets is changed there, in place.
	if out, err := run("hosts", "add", "alpha", "me@alpha2"); err != nil {
		t.Fatalf("tuios hosts add alpha: %v\n%s", err, out)
	}
	got := readFileString(t, hosts)
	if !strings.Contains(got, `addr = "me@alpha2"`) || !strings.Contains(got, "# hosts for this machine only") {
		t.Fatalf("ASSERTION: hosts add did not edit hosts.toml in place:\n%s", got)
	}
	if strings.Contains(readFileString(t, configPathIn(base)), "alpha") {
		t.Fatalf("ASSERTION: hosts add wrote alpha into config.toml:\n%s", readFileString(t, configPathIn(base)))
	}

	// A host a read-only file sets: config.toml gets the change, and the
	// command says so.
	out, err = run("hosts", "add", "beta", "me@beta2")
	if err != nil {
		t.Fatalf("tuios hosts add beta: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cannot write") {
		t.Fatalf("ASSERTION: hosts add did not say locked.toml is read-only:\n%s", out)
	}
	if !strings.Contains(readFileString(t, configPathIn(base)), "me@beta2") {
		t.Fatalf("ASSERTION: the change to beta is not in config.toml:\n%s", readFileString(t, configPathIn(base)))
	}
	if got := readFileString(t, locked); got != lockedBody {
		t.Fatalf("ASSERTION: tuios wrote the read-only locked.toml:\n%s", got)
	}

	// A remove cannot take beta out of locked.toml, so it changes nothing
	// and names the file.
	out, err = run("hosts", "remove", "beta")
	if err == nil || !strings.Contains(out, "locked.toml") {
		t.Fatalf("ASSERTION: hosts remove of a host in a read-only file did not refuse and name it: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, configPathIn(base)), "me@beta2") {
		t.Fatalf("ASSERTION: the refused remove still changed config.toml:\n%s", readFileString(t, configPathIn(base)))
	}

	// A keybinding the included keys.toml sets is unbound there.
	if out, err := run("keybinds", "unbind", "close_window"); err != nil {
		t.Fatalf("tuios keybinds unbind: %v\n%s", err, out)
	}
	if got := readFileString(t, keys); !strings.Contains(got, "close_window = []") {
		t.Fatalf("ASSERTION: keybinds unbind did not write keys.toml:\n%s", got)
	}
	if main := readFileString(t, configPathIn(base)); strings.Contains(main, "close_window") {
		t.Fatalf("ASSERTION: keybinds unbind wrote close_window into config.toml:\n%s", main)
	}

	out, err = run("config", "origin")
	if err != nil {
		t.Fatalf("tuios config origin: %v\n%s", err, out)
	}
	for _, want := range [][2]string{
		{"hosts.alpha.addr", "hosts.toml"},
		{"hosts.beta.addr", "config.toml"},
		{"keybindings.window_management.close_window", "keys.toml"},
	} {
		if !originLine(out, want[0], want[1]) {
			t.Fatalf("ASSERTION: config origin does not name %s for %s:\n%s", want[1], want[0], out)
		}
	}

	transcript.WriteString("--- config.toml\n" + readFileString(t, configPathIn(base)))
	transcript.WriteString("--- hosts.toml\n" + readFileString(t, hosts))
	transcript.WriteString("--- keys.toml\n" + readFileString(t, keys))
	transcript.WriteString("--- locked.toml\n" + readFileString(t, locked))
	if err := os.WriteFile(filepath.Join(artifactDir(t), "transcript.txt"), []byte(transcript.String()), 0o644); err != nil { //nolint:gosec // a test artifact
		t.Logf("save the transcript: %v", err)
	}
}
