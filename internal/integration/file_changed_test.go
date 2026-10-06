package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A harness or the person can save its settings file while tuios edits it.
// The rename must not drop that save. These are deterministic race
// regressions: beforeRename changes the file at the moment before the last
// check, which is the window a real save lands in.
//
// Failure modes written down first: the check reads the wrong path (a
// symlink's link instead of its target) and passes; the retry re-uses the old
// read and overwrites the save; a change on every try loops for ever or
// writes anyway.

func TestInstallKeepsASaveMadeWhileItWrites(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	path := tg.Path(env)
	writeFile(t, path, `{"model": "opus"}`+"\n")
	fired := false
	beforeRename = func(string) {
		if !fired {
			fired = true
			writeFile(t, path, `{"model": "sonnet"}`+"\n")
		}
	}
	t.Cleanup(func() { beforeRename = nil })
	if _, err := tg.Install(env, "tuios"); err != nil {
		t.Fatalf("install after one change should retry and succeed: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, `"sonnet"`) || !strings.Contains(got, "agent-hook claude-code") {
		t.Fatalf("the retry lost the save or the hooks:\n%s", got)
	}
}

func TestInstallReportsAFileThatKeepsChanging(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	path := tg.Path(env)
	writeFile(t, path, `{"model": "opus"}`+"\n")
	n := 0
	beforeRename = func(string) {
		n++
		writeFile(t, path, `{"model": "save `+string(rune('a'+n))+`"}`+"\n")
	}
	t.Cleanup(func() { beforeRename = nil })
	_, err := tg.Install(env, "tuios")
	if !errors.Is(err, ErrFileChanged) {
		t.Fatalf("install = %v, want ErrFileChanged", err)
	}
	if n != 2 {
		t.Fatalf("the install tried %d times, want 2", n)
	}
	if got := readFile(t, path); strings.Contains(got, "agent-hook") || !strings.Contains(got, `"save c"`) {
		t.Fatalf("the file is not the last save:\n%s", got)
	}
}

func TestInstallChecksTheTargetOfASymlink(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	path := tg.Path(env)
	real := filepath.Join(env.Home, "dotfiles", "settings.json")
	writeFile(t, real, `{"model": "opus"}`+"\n")
	writeFile(t, filepath.Join(env.Home, ".claude", ".keep"), "")
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
	n := 0
	beforeRename = func(string) {
		n++
		writeFile(t, real, `{"model": "save `+string(rune('a'+n))+`"}`+"\n")
	}
	t.Cleanup(func() { beforeRename = nil })
	if _, err := tg.Install(env, "tuios"); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("install = %v, want ErrFileChanged for a save to the link's target", err)
	}
	if got := readFile(t, real); strings.Contains(got, "agent-hook") {
		t.Fatalf("the link's target was overwritten:\n%s", got)
	}
}

// TestInstallNamesWhatAPartialWriteChanged: Hermes is three files, and the
// last, config.yaml, keeps changing. The two plugin files are already
// written when it fails, and the error must say so rather than read as
// nothing changed.
func TestInstallNamesWhatAPartialWriteChanged(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, Hermes)
	cfg := filepath.Join(tg.ConfigDir(env), "config.yaml")
	writeFile(t, cfg, "plugins:\n  enabled:\n    - other\n")
	n := 0
	beforeRename = func(target string) {
		if filepath.Base(target) == "config.yaml" {
			n++
			writeFile(t, cfg, "plugins:\n  enabled:\n    - other\n# save "+string(rune('a'+n))+"\n")
		}
	}
	t.Cleanup(func() { beforeRename = nil })
	_, err := tg.Install(env, "tuios")
	if !errors.Is(err, ErrFileChanged) {
		t.Fatalf("install = %v, want ErrFileChanged", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "already changed") || !strings.Contains(msg, "__init__.py") ||
		!strings.Contains(msg, "did not change "+cfg) {
		t.Fatalf("the error does not name what was changed and what was not: %s", msg)
	}
}
