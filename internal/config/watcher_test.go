package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The watcher's job is one sentence: a save in another pane reaches the running
// client, and a save that cannot be used does not. Both halves are here,
// because a watcher that delivers everything and a watcher that delivers
// nothing both pass a test that only checks one of them.

// watcherReport is what the callback saw, drained by the test.
type watcherReport struct {
	cfg *UserConfig
	err error
}

// startWatcher writes body to a config file in a fresh directory and starts a
// watcher on it, returning the path and the channel the callback feeds.
func startWatcher(t *testing.T, body string) (string, chan watcherReport) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	reports := make(chan watcherReport, 8)
	w, err := NewWatcher(path, func(cfg *UserConfig, err error) {
		reports <- watcherReport{cfg: cfg, err: err}
	})
	if err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	t.Cleanup(w.Stop)
	return path, reports
}

// nextReport waits for one delivery, or fails.
func nextReport(t *testing.T, reports chan watcherReport, why string) watcherReport {
	t.Helper()
	select {
	case r := <-reports:
		return r
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: the watcher delivered nothing", why)
	}
	return watcherReport{}
}

// noReport requires that nothing is delivered for long enough to be sure.
func noReport(t *testing.T, reports chan watcherReport, why string) {
	t.Helper()
	select {
	case r := <-reports:
		t.Fatalf("%s: the watcher delivered cfg=%v err=%v", why, r.cfg != nil, r.err)
	case <-time.After(4 * configDebounce):
	}
}

// saveLikeVim replaces the file the way an editor does: a new file beside it,
// renamed into place. The original inode is never written to again.
func saveLikeVim(t *testing.T, path, body string) {
	t.Helper()
	tmp := path + ".swp~"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename over config: %v", err)
	}
}

const watcherBase = "[appearance]\ntheme = \"\"\n\n[spotlight]\nradius = 10\ndim = 40\n"

// TestWatcherSeesAnEditorsSave is the whole feature and the trap under it.
//
// An inotify watch on a file follows the inode, and vim does not write through
// the inode: it writes a new file and renames it into place. A file watch is
// therefore dead after the first :w, which is the most common way anybody edits
// this file. The watch is on the directory for that reason.
func TestWatcherSeesAnEditorsSave(t *testing.T) {
	path, reports := startWatcher(t, watcherBase)

	saveLikeVim(t, path, "[appearance]\ntheme = \"\"\n\n[spotlight]\nradius = 10\ndim = 90\n")

	got := nextReport(t, reports, "an editor's save")
	if got.err != nil {
		t.Fatalf("an editor's save was reported as an error: %v", got.err)
	}
	if got.cfg.Spotlight.Dim != 90 {
		t.Errorf("the reloaded config carries dim %d, want 90", got.cfg.Spotlight.Dim)
	}

	// The positive half of the same rule: the watch survives the first save and
	// sees the second. A watch that died on the rename passes the assertion
	// above only if it caught the rename event itself.
	saveLikeVim(t, path, "[appearance]\ntheme = \"\"\n\n[spotlight]\nradius = 10\ndim = 20\n")
	got = nextReport(t, reports, "a second save after the file was replaced")
	if got.err != nil {
		t.Fatalf("the second save was reported as an error: %v", got.err)
	}
	if got.cfg.Spotlight.Dim != 20 {
		t.Errorf("the second reload carries dim %d, want 20", got.cfg.Spotlight.Dim)
	}
}

// TestWatcherSeesAWriteInPlace is the other way a file is saved: an editor with
// backupcopy on, and every shell redirect.
func TestWatcherSeesAWriteInPlace(t *testing.T) {
	path, reports := startWatcher(t, watcherBase)

	if err := os.WriteFile(path, []byte("[appearance]\ntheme = \"nord\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := nextReport(t, reports, "a write in place")
	if got.err != nil {
		t.Fatalf("a write in place was reported as an error: %v", got.err)
	}
	if got.cfg.Appearance.Theme != "nord" {
		t.Errorf("the reloaded config carries theme %q, want nord", got.cfg.Appearance.Theme)
	}
}

// TestWatcherKeepsTheRunningConfigWhenTheFileBreaks. A file caught half written,
// or one with an unbalanced quote in it, must not reach the client as a config.
// Rendering the defaults over a running session every time somebody saved a
// typo would be far worse than waiting for the next save.
func TestWatcherKeepsTheRunningConfigWhenTheFileBreaks(t *testing.T) {
	path, reports := startWatcher(t, watcherBase)

	saveLikeVim(t, path, "[appearance\ntheme = \"nord")

	got := nextReport(t, reports, "a file that does not parse")
	if got.err == nil {
		t.Fatalf("a file that does not parse was delivered as a config: %+v", got.cfg)
	}
	if got.cfg != nil {
		t.Errorf("a config was handed over beside the error: %+v", got.cfg)
	}

	// And the fix is seen. A watcher that recorded the broken file's hash would
	// go quiet here, which is the failure that hides itself.
	saveLikeVim(t, path, "[appearance]\ntheme = \"nord\"\n")
	got = nextReport(t, reports, "the file after it was fixed")
	if got.err != nil {
		t.Fatalf("the fixed file was still reported as an error: %v", got.err)
	}
	if got.cfg.Appearance.Theme != "nord" {
		t.Errorf("the fixed file carries theme %q, want nord", got.cfg.Appearance.Theme)
	}
}

// TestWatcherReportsEverySaveOfABrokenFile. A file that could not be used is
// not what the client is running, so its content is not recorded as being in
// force. Saving the same broken file again is a save the user has to hear
// about: the reload still did not happen, and going quiet after the first
// attempt would read as the second one working.
func TestWatcherReportsEverySaveOfABrokenFile(t *testing.T) {
	path, reports := startWatcher(t, watcherBase)
	const broken = "[appearance\ntheme = \"nord"

	for i := range 2 {
		saveLikeVim(t, path, broken)
		got := nextReport(t, reports, "save number "+string(rune('1'+i))+" of a broken file")
		if got.err == nil {
			t.Fatalf("save %d of a broken file was delivered as a config: %+v", i+1, got.cfg)
		}
	}
}

// TestWatcherDropsAFileThatSaysWhatIsAlreadyInForce. tuios writes this file
// itself: every settings row saves. Without this, one keypress on a row would
// come back through the watcher as somebody else's edit.
func TestWatcherDropsAFileThatSaysWhatIsAlreadyInForce(t *testing.T) {
	path, reports := startWatcher(t, watcherBase)

	if err := os.WriteFile(path, []byte(watcherBase), 0o600); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	noReport(t, reports, "a rewrite with the same content")

	// The positive half: a real change is still delivered, so the drop above is
	// about the content and not about the watcher being dead.
	if err := os.WriteFile(path, []byte(watcherBase+"\n[debug]\nshow_key_events = true\n"), 0o600); err != nil {
		t.Fatalf("change config: %v", err)
	}
	got := nextReport(t, reports, "a real change after an identical rewrite")
	if got.err != nil {
		t.Fatalf("the changed file was reported as an error: %v", got.err)
	}
	if !got.cfg.Debug.ShowKeyEvents {
		t.Error("the changed file did not carry the change")
	}
}

// TestWatcherDropsTuiosOwnSave. Every row on the settings page saves this file.
// A save coming back through the watcher as an edit would retile once per
// arrow-key repeat for a config that was already in force, and a save still in
// flight when the watcher read the file would put the value one keypress back
// into the model.
func TestWatcherDropsTuiosOwnSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()
	cfg.Spotlight.Dim = 40
	if err := WriteConfigFile(cfg, path); err != nil {
		t.Fatalf("write config: %v", err)
	}
	reports := make(chan watcherReport, 8)
	w, err := NewWatcher(path, func(cfg *UserConfig, err error) {
		reports <- watcherReport{cfg: cfg, err: err}
	})
	if err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	t.Cleanup(w.Stop)

	// The settings page's own save: a different config, written by tuios.
	cfg.Spotlight.Dim = 41
	if err := WriteConfigFile(cfg, path); err != nil {
		t.Fatalf("save from the settings page: %v", err)
	}
	noReport(t, reports, "tuios saving the file itself")

	// The positive half: a hand edit of the same file is still delivered, so
	// the drop above is about who wrote it and not about the watcher being dead.
	saveLikeVim(t, path, "[appearance]\ntheme = \"nord\"\n")
	got := nextReport(t, reports, "a hand edit after tuios saved")
	if got.err != nil {
		t.Fatalf("the hand edit was reported as an error: %v", got.err)
	}
	if got.cfg.Appearance.Theme != "nord" {
		t.Errorf("the hand edit carries theme %q, want nord", got.cfg.Appearance.Theme)
	}
}

// TestWatcherCollapsesOneSaveIntoOneReload. A save is several events: the
// rename, the create and the write land within milliseconds. Reloading per
// event parses the file three times, and one of those reads a half-written
// file.
func TestWatcherCollapsesOneSaveIntoOneReload(t *testing.T) {
	path, reports := startWatcher(t, watcherBase)

	for i := range 6 {
		body := watcherBase + "\n[debug]\nshow_key_events = " +
			map[bool]string{true: "true", false: "false"}[i%2 == 0] + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		time.Sleep(configDebounce / 8)
	}

	if got := nextReport(t, reports, "a burst of writes"); got.err != nil {
		t.Fatalf("the burst was reported as an error: %v", got.err)
	}
	noReport(t, reports, "a second reload for the same burst")
}

// TestWatcherStopsWatching. The watcher outlives nothing: a stopped one must
// not call back, or a closed program takes a message after it has gone.
func TestWatcherStopsWatching(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(watcherBase), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	reports := make(chan watcherReport, 8)
	w, err := NewWatcher(path, func(cfg *UserConfig, err error) {
		reports <- watcherReport{cfg: cfg, err: err}
	})
	if err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	w.Stop()
	w.Stop() // idempotent: the program's defer and an early stop both run

	if err := os.WriteFile(path, []byte("[appearance]\ntheme = \"nord\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	noReport(t, reports, "a write after Stop")
}

// TestReloadFillsEverySection is the bug that would have shipped with the live
// reload the moment anything outside [appearance] was read from it. The reload
// path filled four of the seven sections, so a file with no [spotlight] block
// came back with radius 0 and dim 0 rather than the shipped defaults.
func TestReloadFillsEverySection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[appearance]\ntheme = \"\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := ReloadConfig(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	def := DefaultConfig()
	if cfg.Spotlight.Radius != def.Spotlight.Radius || cfg.Spotlight.Dim != def.Spotlight.Dim {
		t.Errorf("a reload of a file with no [spotlight] gave radius %d dim %d, want %d and %d",
			cfg.Spotlight.Radius, cfg.Spotlight.Dim, def.Spotlight.Radius, def.Spotlight.Dim)
	}
	if cfg.Spotlight.Follow != def.Spotlight.Follow || cfg.Spotlight.Edge != def.Spotlight.Edge {
		t.Errorf("a reload gave follow %q edge %q, want %q and %q",
			cfg.Spotlight.Follow, cfg.Spotlight.Edge, def.Spotlight.Follow, def.Spotlight.Edge)
	}
	if cfg.Screenshot.Format != def.Screenshot.Format {
		t.Errorf("a reload gave screenshot format %q, want %q", cfg.Screenshot.Format, def.Screenshot.Format)
	}
}

// TestReloadRefusesAFileThatDoesNotValidate. Parsing is not enough: a file with
// a value nothing accepts must be refused with a message naming the key, not
// applied with the value silently dropped.
func TestReloadRefusesAFileThatDoesNotValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[keybindings.global]\nnew_window = \"not a key at all\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := ReloadConfig(path)
	if err == nil {
		t.Fatalf("a config that does not validate was accepted: %+v", cfg)
	}
	if cfg != nil {
		t.Errorf("a config was returned beside the error: %+v", cfg)
	}
}
