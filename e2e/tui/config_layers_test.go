package tuie2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// More of #518: the cases a Nix or home-manager setup meets. A store file is a
// read-only file in a read-only directory, linked into the config directory.

// storeFile writes a read-only file into a read-only directory under base,
// the way the Nix store holds it, and returns its path. The directory is made
// writable again when the test ends, so the temporary directory can go.
func storeFile(t *testing.T, base, name, body string) string {
	t.Helper()
	store := filepath.Join(base, "nix-store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatalf("make the store: %v", err)
	}
	path := filepath.Join(store, name)
	if err := os.WriteFile(path, []byte(body), 0o444); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chmod(store, 0o555); err != nil {
		t.Fatalf("make the store read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o755) })
	return path
}

// linkConfigPart links rel in the config directory to target.
func linkConfigPart(t *testing.T, base, rel, target string) string {
	t.Helper()
	path := filepath.Join(configDirIn(base), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the directory of %s: %v", rel, err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("link %s: %v", rel, err)
	}
	return path
}

// addedLines is the lines of after that are not in before, and the lines of
// before that are not in after.
func addedLines(before, after string) (added, removed []string) {
	b := strings.Split(strings.TrimRight(before, "\n"), "\n")
	a := strings.Split(strings.TrimRight(after, "\n"), "\n")
	for _, l := range a {
		if i := slices.Index(b, l); i >= 0 {
			b = slices.Delete(b, i, i+1)
			continue
		}
		added = append(added, l)
	}
	return added, b
}

// TestFirstStartWithNixStyleConfigD is the issue's case on a machine that has
// never run tuios: the settings come from a read-only config.d file that
// links into a store, and there is no config.toml. The first start must write
// a config.toml that hides nothing, the store file must apply, and a change
// made in tuios must add one key to config.toml and leave the store alone.
func TestFirstStartWithNixStyleConfigD(t *testing.T) {
	base := t.TempDir()
	marker := filepath.Join(base, "nix.fired")
	nixBody := keybindConfigFor("nix", "y", marker) + "\n[appearance]\nborder_style = \"double\"\n"
	store := storeFile(t, base, "tuios-nix.toml", nixBody)
	linkConfigPart(t, base, filepath.Join("config.d", "10-nix.toml"), store)
	if _, err := os.Stat(configPathIn(base)); err == nil {
		t.Fatal("config.toml is there before the first start, so this cannot test it")
	}

	term := startIn(t, base, startOpts{args: []string{"new", "nixfirst"}})
	waitBoot(t, term)

	first := readFileString(t, configPathIn(base))
	var settings []string
	for _, l := range strings.Split(first, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			settings = append(settings, l)
		}
	}
	for _, hidden := range []string{"border_style", "keybindings"} {
		if strings.Contains(strings.Join(settings, "\n"), hidden) {
			t.Fatalf("ASSERTION: the first-start config.toml sets %s, which hides the config.d file:\n%s", hidden, first)
		}
	}
	if !strings.Contains(first, "[startup]") || !strings.Contains(first, "tiled = true") {
		t.Fatalf("ASSERTION: the first-start config.toml does not turn [startup] tiled on:\n%s", first)
	}

	newWindow(t, term)
	pressUntilFile(t, term, 'y', marker, "the binding in the read-only config.d file does not work")

	before := readFileString(t, configPathIn(base))
	setLive(t, base, "border_style", "thick")
	after := waitForFileText(t, configPathIn(base), func(s string) bool { return strings.Contains(s, `border_style = "thick"`) },
		"set-config of a key the store file holds did not go to config.toml")
	added, removed := addedLines(before, after)
	if len(added) != 1 || len(removed) != 0 {
		t.Fatalf("ASSERTION: one change must add one line to config.toml; it added %q and removed %q:\n%s", added, removed, after)
	}
	if got := readFileString(t, store); got != nixBody {
		t.Fatalf("ASSERTION: tuios wrote the store file:\n%s", got)
	}
	if err := term.WaitForText("cannot write", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not say where the change went: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "after-set-config")
	saveSyncArtifact(t, "config.toml.first", first)
	saveSyncArtifact(t, "config.toml.after", after)
}

// TestReadOnlyConfigTomlLink: config.toml itself is a link into the store, as
// home-manager makes it, and includes a writable local.toml. A new setting
// goes to local.toml, which keeps its comment. A setting the store's
// config.toml holds cannot take effect from any other file, so the save fails
// and says so. With no writable file at all, the save fails and nothing
// changes.
func TestReadOnlyConfigTomlLink(t *testing.T) {
	base := t.TempDir()
	mainBody := "include = [\"local.toml\"]\n\n[appearance]\nborder_style = \"double\"\n"
	store := storeFile(t, base, "tuios-config.toml", mainBody)
	link := linkConfigPart(t, base, "config.toml", store)
	local := writeConfigPart(t, base, "local.toml", "# settings for this machine\n")

	term := startIn(t, base, startOpts{args: []string{"new", "rolink"}, shippedLooks: true})
	waitBoot(t, term)

	setLive(t, base, "hide_window_buttons", "true")
	got := waitForFileText(t, local, func(s string) bool { return strings.Contains(s, "hide_window_buttons = true") },
		"with config.toml read-only, a new setting did not go to local.toml")
	if !strings.HasPrefix(got, "# settings for this machine\n") {
		t.Fatalf("ASSERTION: the write to local.toml lost its comment:\n%s", got)
	}

	setLive(t, base, "border_style", "thick")
	if err := term.WaitForText("cannot save", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a save of a key the read-only config.toml holds did not fail out loud: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(readFileString(t, local), "border_style") {
		t.Fatalf("ASSERTION: border_style went to local.toml, where config.toml hides it:\n%s", readFileString(t, local))
	}

	if err := os.Chmod(local, 0o444); err != nil {
		t.Fatalf("make local.toml read-only: %v", err)
	}
	lockedLocal := readFileString(t, local)
	setLive(t, base, "hide_window_buttons", "false")
	// The message before this one starts with the same words, so the test
	// waits for the words that are this one's own.
	if err := term.WaitForText("Could not save settings: tuios cannot write", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: with no writable file the save did not fail out loud: %v\n%s", err, term.Snapshot())
	}
	if got := readFileString(t, local); got != lockedLocal {
		t.Fatalf("ASSERTION: tuios wrote the read-only local.toml:\n%s", got)
	}
	if got := readFileString(t, store); got != mainBody {
		t.Fatalf("ASSERTION: tuios wrote the store config.toml:\n%s", got)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("ASSERTION: config.toml is no longer a link: %v", err)
	}
	saveArtifact(t, term, artifactDir(t), "no-writable-file")
	saveSyncArtifact(t, "local.toml", readFileString(t, local))
}

// TestSymlinkedIncludeEditedInPlace: an included file is a link to a file in
// a dotfiles checkout, in another directory. An edit in place there (a write
// to the target, the link untouched) must reach the running client.
func TestSymlinkedIncludeEditedInPlace(t *testing.T) {
	base := t.TempDir()
	dots := filepath.Join(base, "dotfiles")
	if err := os.MkdirAll(dots, 0o755); err != nil {
		t.Fatal(err)
	}
	late := filepath.Join(base, "late.fired")
	target := filepath.Join(dots, "keys.toml")
	if err := os.WriteFile(target, []byte(keybindConfigFor("late", "y", late)), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, `include = ["keys.toml"]`+"\n")
	linkConfigPart(t, base, "keys.toml", target)

	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	pressUntilFile(t, term, 'y', late, "the binding in the linked include does not work at start")

	added := filepath.Join(base, "added.fired")
	pressChord(t, term, 'h')
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(added); err == nil {
		t.Fatalf("prefix+alt+h fired before the edit, so the test proves nothing\n%s", term.Snapshot())
	}
	// In place: open the target, truncate it and write it. No rename, and the
	// link in the config directory does not change.
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(keybindConfigFor("late", "y", late) + keybindConfigFor("added", "h", added)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	pressUntilFile(t, term, 'h', added, "an edit in place to the target of a linked include never reached the client")
	saveArtifact(t, term, artifactDir(t), "after-edit")
}

// TestMixedNamedAndUnnamedCommandEntries: arrays of tables merge entry by
// entry. A later unnamed entry matches an earlier named one by key and
// changes it, a named entry the later file does not mention survives, a
// tombstone removes the entry it names, and a new entry is added.
func TestMixedNamedAndUnnamedCommandEntries(t *testing.T) {
	base := t.TempDir()
	mark := func(n string) string { return filepath.Join(base, n+".fired") }
	entry := func(name, key, marker string, extra string) string {
		s := "\n[[keybindings.command]]\n"
		if name != "" {
			s += "name = \"" + name + "\"\n"
		}
		s += "key = \"prefix+alt+" + key + "\"\ntype = \"shell\"\ncommand = \"touch " + marker + "\"\n" + extra
		return s
	}
	writeConfig(t, base, `include = ["base.toml"]`+"\n")
	writeConfigPart(t, base, "base.toml",
		entry("alpha", "y", mark("alpha-base"), "")+
			entry("beta", "h", mark("beta"), "")+
			entry("gamma", "k", mark("gamma"), ""))
	writeConfigPart(t, base, filepath.Join("config.d", "50-local.toml"),
		entry("", "y", mark("alpha-local"), "")+
			"\n[[keybindings.command]]\nname = \"beta\"\ndisabled = true\n"+
			entry("", "j", mark("new"), ""))

	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	pressUntilFile(t, term, 'k', mark("gamma"), "the named gamma entry from base.toml was lost when config.d added unnamed entries")
	pressUntilFile(t, term, 'y', mark("alpha-local"), "the unnamed entry did not change the named entry with its key")
	if _, err := os.Stat(mark("alpha-base")); err == nil {
		t.Fatalf("ASSERTION: the base command for alpha still ran, so the entries did not merge")
	}
	pressUntilFile(t, term, 'j', mark("new"), "a new unnamed entry was not added")
	pressChord(t, term, 'h')
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(mark("beta")); err == nil {
		t.Fatalf("ASSERTION: the tombstone did not remove beta\n%s", term.Snapshot())
	}
	out, err := tuiosCLI(t, base, "config", "origin", "keybindings.command")
	if err != nil {
		t.Fatalf("tuios config origin: %v\n%s", err, out)
	}
	saveSyncArtifact(t, "origin.txt", out)
	saveArtifact(t, term, artifactDir(t), "after-chords")
}

// TestMergeOrderIncludeThenConfigDThenMain: three files bind one chord, each
// to its own marker. config.toml wins. With its entry gone, config.d wins
// over the include. With the config.d file gone, the include applies.
func TestMergeOrderIncludeThenConfigDThenMain(t *testing.T) {
	base := t.TempDir()
	mark := func(n string) string { return filepath.Join(base, n+".fired") }
	mainHead := `include = ["inc.toml"]` + "\n"
	writeConfig(t, base, mainHead+keybindConfigFor("order", "y", mark("main")))
	writeConfigPart(t, base, "inc.toml", keybindConfigFor("order", "y", mark("include")))
	dropIn := writeConfigPart(t, base, filepath.Join("config.d", "50-order.toml"), keybindConfigFor("order", "y", mark("dropin")))

	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	pressUntilFile(t, term, 'y', mark("main"), "config.toml did not win over config.d and the include")
	for _, m := range []string{"include", "dropin"} {
		if _, err := os.Stat(mark(m)); err == nil {
			t.Fatalf("ASSERTION: the %s binding ran while config.toml sets the chord", m)
		}
	}

	saveConfigLikeAnEditor(t, base, mainHead)
	pressUntilFile(t, term, 'y', mark("dropin"), "with config.toml silent, config.d did not win over the include")
	if _, err := os.Stat(mark("include")); err == nil {
		t.Fatalf("ASSERTION: the include won over config.d")
	}

	if err := os.Remove(dropIn); err != nil {
		t.Fatal(err)
	}
	pressUntilFile(t, term, 'y', mark("include"), "with config.d empty, the include did not apply")
	saveArtifact(t, term, artifactDir(t), "after-order")
}

// TestConfigPruneLetsIncludesApply is the migration for a config.toml that an
// older tuios wrote in full: prune removes the keys at their default, so an
// included file's value applies, and keeps the include list, the comments, a
// value the person chose and the [startup] keys.
func TestConfigPruneLetsIncludesApply(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `# my notes about this machine
include = ["inc.toml"]

[appearance]
# rounded is what I had before
border_style = "rounded"
window_button_style = "dots"
hide_window_buttons = true

[startup]
tiled = true
daemon = true
`)
	writeConfigPart(t, base, "inc.toml", "[appearance]\nborder_style = \"double\"\n")

	out, err := tuiosCLI(t, base, "config", "origin", "appearance.border_style")
	if err != nil || !originLine(out, "appearance.border_style", "config.toml") {
		t.Fatalf("before the prune, config.toml must hide inc.toml for border_style: %v\n%s", err, out)
	}

	dry, err := tuiosCLI(t, base, "config", "prune", "--dry-run")
	if err != nil || !strings.Contains(dry, "Would remove") || !strings.Contains(dry, "appearance.border_style") {
		t.Fatalf("ASSERTION: prune --dry-run did not list border_style: %v\n%s", err, dry)
	}
	if !strings.Contains(readFileString(t, configPathIn(base)), "window_button_style") {
		t.Fatalf("ASSERTION: prune --dry-run changed config.toml")
	}

	// The prune lets inc.toml set border_style, which changes the config. A
	// run with no terminal must not do that without --yes.
	before := readFileString(t, configPathIn(base))
	refused, err := tuiosCLI(t, base, "config", "prune")
	if err == nil || !strings.Contains(refused, "--yes") {
		t.Fatalf("ASSERTION: prune with no terminal and no --yes did not refuse: %v\n%s", err, refused)
	}
	if readFileString(t, configPathIn(base)) != before {
		t.Fatalf("ASSERTION: the refused prune changed config.toml")
	}

	pruned, err := tuiosCLI(t, base, "config", "prune", "--yes")
	if err != nil {
		t.Fatalf("tuios config prune --yes: %v\n%s", err, pruned)
	}
	after := readFileString(t, configPathIn(base))
	for _, gone := range []string{"border_style", "window_button_style"} {
		if strings.Contains(after, gone+" =") {
			t.Fatalf("ASSERTION: prune kept %s, which has its default value:\n%s", gone, after)
		}
	}
	for _, kept := range []string{"# my notes about this machine", `include = ["inc.toml"]`, "hide_window_buttons = true", "tiled = true", "daemon = true"} {
		if !strings.Contains(after, kept) {
			t.Fatalf("ASSERTION: prune removed %q:\n%s", kept, after)
		}
	}
	if !strings.Contains(pruned, "take the value of another file") || !strings.Contains(pruned, "inc.toml") {
		t.Fatalf("ASSERTION: prune did not say border_style now comes from inc.toml:\n%s", pruned)
	}
	out, err = tuiosCLI(t, base, "config", "origin", "appearance.border_style")
	if err != nil || !originLine(out, "appearance.border_style", "inc.toml") {
		t.Fatalf("ASSERTION: after the prune inc.toml does not set border_style: %v\n%s", err, out)
	}
	saveSyncArtifact(t, "prune.txt", dry+"\n"+refused+"\n"+pruned+"\n--- config.toml\n"+after)
}

// TestSaveDoesNotPinValuesTheModelHasNotSeen: a save writes only what the
// person changed. Here an included file gets a value the running client has
// not loaded, because it is in a directory made after start that the watcher
// does not follow. A save of another setting must not write the client's old
// value of that key anywhere: not into config.toml, where it would hide the
// file for good, and not back into the file.
func TestSaveDoesNotPinValuesTheModelHasNotSeen(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `include = ["later/look.toml"]`+"\n")
	term := startIn(t, base, startOpts{args: []string{"new", "stale"}})
	waitBoot(t, term)

	lookBody := "[appearance]\nwindow_button_style = \"pill\"\n"
	look := writeConfigPart(t, base, filepath.Join("later", "look.toml"), lookBody)
	time.Sleep(time.Second)
	setLive(t, base, "border_style", "thick")
	main := waitForFileText(t, configPathIn(base), func(s string) bool { return strings.Contains(s, `border_style = "thick"`) },
		"set-config did not write border_style")
	if strings.Contains(main, "window_button_style") {
		t.Fatalf("ASSERTION: the save wrote the client's old window_button_style into config.toml:\n%s", main)
	}
	if got := readFileString(t, look); got != lookBody {
		t.Fatalf("ASSERTION: the save wrote the client's old window_button_style back into look.toml:\n%s", got)
	}
	out, err := tuiosCLI(t, base, "config", "origin", "appearance.window_button_style")
	if err != nil || !originLine(out, "appearance.window_button_style", "look.toml") {
		t.Fatalf("ASSERTION: look.toml no longer sets window_button_style: %v\n%s", err, out)
	}
	saveSyncArtifact(t, "config.toml", main)
}

// TestIncludeMistakesAreNamed: an optional include that is missing says
// nothing, a missing plain include warns, an include below a table header
// warns that it includes nothing, and an include of a directory fails with a
// message that says what to do.
func TestIncludeMistakesAreNamed(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `include = ["?maybe.toml", "absent.toml", "config.toml", "?loop.toml"]

[appearance]
include = ["lost.toml"]
`)
	// Two links that point at themselves: one an optional include, one in
	// config.d. Neither may stop the load.
	linkConfigPart(t, base, "loop.toml", "loop.toml")
	linkConfigPart(t, base, filepath.Join("config.d", "50-loop.toml"), "50-loop.toml")
	out, err := tuiosCLI(t, base, "config", "files")
	if err != nil {
		t.Fatalf("tuios config files: %v\n%s", err, out)
	}
	if strings.Contains(out, "maybe.toml") {
		t.Fatalf("ASSERTION: a missing optional include was reported:\n%s", out)
	}
	if !strings.Contains(out, "absent.toml, which does not exist") {
		t.Fatalf("ASSERTION: a missing include was not reported:\n%s", out)
	}
	if !strings.Contains(out, "include in [appearance], so it includes nothing") {
		t.Fatalf("ASSERTION: an include below a table header was not reported:\n%s", out)
	}
	if !strings.Contains(out, "config.toml includes itself") {
		t.Fatalf("ASSERTION: a self include was not reported as one:\n%s", out)
	}
	if !strings.Contains(out, "cannot read loop.toml") || !strings.Contains(out, "cannot read config.d/50-loop.toml") {
		t.Fatalf("ASSERTION: a link loop in an optional include or in config.d was not skipped with a warning:\n%s", out)
	}

	if err := os.MkdirAll(filepath.Join(configDirIn(base), "parts"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, `include = ["parts"]`+"\n")
	dirOut, err := tuiosCLI(t, base, "config", "files")
	if err == nil || !strings.Contains(dirOut, "which is a directory") || !strings.Contains(dirOut, "config.d") {
		t.Fatalf("ASSERTION: an include of a directory did not fail with a clear message: %v\n%s", err, dirOut)
	}
	saveSyncArtifact(t, "files.txt", out+"\n"+dirOut)
}

// TestRemovingAHostKeepsTheNextTablesComment: a host removed on the settings
// page takes its own lines out of config.toml and nothing else. The comment
// above the next table is about that table, and it stays, one blank line
// below what comes before it.
func TestRemovingAHostKeepsTheNextTablesComment(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `[screenshot]
font_family = "Mono"

[hosts.alpha]
addr = "me@alpha"

# notes about my appearance, keep me
[appearance]
border_style = "rounded"
`)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys(",", "/", "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return selectedSettingsRow(s, "alpha") != ""
	}, uiTimeout); err != nil {
		t.Fatalf("the settings search did not find the alpha host row: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	for range len("me@alpha") + 4 {
		if err := term.SendKeys(tuitest.Backspace); err != nil {
			t.Fatal(err)
		}
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	got := waitForFileText(t, configPathIn(base), func(s string) bool { return !strings.Contains(s, "hosts.alpha") },
		"clearing the host's address on the settings page did not remove it from config.toml")
	if !strings.Contains(got, "font_family = \"Mono\"\n\n# notes about my appearance, keep me\n[appearance]\n") {
		t.Fatalf("ASSERTION: the comment above [appearance] did not survive the host's removal in place:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("ASSERTION: the removal left a run of blank lines:\n%s", got)
	}
	saveSyncArtifact(t, "config.toml", got)
	saveArtifact(t, term, artifactDir(t), "after-remove")
}

// TestIncludedFileIsNeverRewrittenWhole: look.toml sets the key in an inline
// table, which the line editor cannot change in place. tuios must not write
// look.toml again from its values, which would drop its comment. The change
// goes to config.toml, and the client says so.
func TestIncludedFileIsNeverRewrittenWhole(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `include = ["look.toml"]`+"\n")
	lookBody := "# my look, by hand\nappearance = { border_style = \"double\" }\n"
	look := writeConfigPart(t, base, "look.toml", lookBody)

	term := startIn(t, base, startOpts{args: []string{"new", "inline"}})
	waitBoot(t, term)

	setLive(t, base, "border_style", "thick")
	main := waitForFileText(t, configPathIn(base), func(s string) bool { return strings.Contains(s, `border_style = "thick"`) },
		"a change tuios could not make in look.toml's lines did not go to config.toml")
	if got := readFileString(t, look); got != lookBody {
		t.Fatalf("ASSERTION: tuios wrote look.toml again:\n%s", got)
	}
	if err := term.WaitForText("cannot change the lines", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not say where the change went: %v\n%s", err, term.Snapshot())
	}
	saveSyncArtifact(t, "config.toml", main)
	saveArtifact(t, term, artifactDir(t), "notice")
}
