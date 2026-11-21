package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestReloadConfigTurnsOnANewHook adds an after-theme-change hook to the
// config of a running client. The save alone does not turn it on, because a
// process in a pane can write the file and a hook runs a command. The client
// says so. Reload config from the palette turns it on, and the next theme
// change runs it with no reattach.
//
// Negative control: with the client reading [hooks] only at start, the hook
// never runs and the last wait fails.
func TestReloadConfigTurnsOnANewHook(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "theme-hook.log")
	writeConfig(t, base, "[appearance]\ntheme = \"tokyonight\"\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	artifacts := artifactDir(t)
	logText := func() (string, string) {
		data, _ := os.ReadFile(out)
		return string(data), "log: " + string(data)
	}

	saveConfigLikeAnEditor(t, base, "[appearance]\ntheme = \"tokyonight\"\n\n[hooks]\n"+
		"after-theme-change = 'echo \"$TUIOS_EVENT $TUIOS_THEME\" >> "+out+"'\n")
	waitScreen(t, term, "the client did not say the new hook waits", "new or changed hook")
	saveArtifact(t, term, artifacts, "hook-waits")

	pickTheme(t, term, "dracula")
	stayTrue(t, "a hook from a file save ran with no Reload config", 2*time.Second, func() (bool, string) {
		got, state := logText()
		return got == "", state
	})

	sendKeys(t, term, legacyCtrlP)
	waitScreen(t, term, "the palette never opened", paletteTitle)
	sendKeys(t, term, "Reload config")
	waitScreen(t, term, "the palette never filtered", "Reload config")
	sendKeys(t, term, tuitest.Enter)
	waitGone(t, term, "the palette", paletteTitle)
	saveArtifact(t, term, artifacts, "reloaded")

	pickTheme(t, term, "catppuccin_latte")
	waitUntil(t, "the hook did not run after Reload config", configWatchTimeout, func() (bool, string) {
		got, state := logText()
		return strings.Contains(got, "after-theme-change catppuccin_latte"), state
	})
	got, _ := logText()
	if n := strings.Count(got, "\n"); n != 1 {
		t.Errorf("the hook ran %d times, want 1:\n%s", n, got)
	}
	saveArtifact(t, term, artifacts, "hook-ran")
}
