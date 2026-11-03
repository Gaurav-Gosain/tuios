package tuie2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A plugin's screen saver as a frames file: check reads it, the saver plays
// its frames at their times with no process, and a key ends it.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "Frames savers".

const saverFrames = `tuios-saver 1
size 14 3
color a #ff5f87
color b bright_cyan

frame 400
+------------+
| SAVER-ONE  |
+------------+
colors
bbbbbbbbbbbbbb
b aaaaaaaaa  b
bbbbbbbbbbbbbb

frame 400
+------------+
| SAVER-TWO  |
+------------+
`

func TestPluginFramesSaver(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "savers"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{
		"tuios-plugin.toml": "api = 1\nid = \"e2e.saver\"\nname = \"Saver\"\nversion = \"1.0.0\"\n\n[[saver]]\nid = \"box\"\nfile = \"savers/box.txt\"\n",
	})
	saver := filepath.Join(dir, "savers", "box.txt")
	base := t.TempDir()
	writeConfig(t, base, "[screensaver]\neffect = \"plugin/e2e.saver/box\"\n")
	killDaemon(t, base)

	// check names the line that is wrong.
	if err := os.WriteFile(saver, []byte(strings.Replace(saverFrames, "size 14 3", "size 14", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := tuiosCLI(t, base, "plugins", "check", dir)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !strings.Contains(out, "the saver box: line 2: size takes the columns and the rows") {
		t.Errorf("check of a broken frames file: %v\n%s", err, out)
	}
	if err := os.WriteFile(saver, []byte(saverFrames), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "check", dir); err != nil || !strings.Contains(out, "savers   box (1)") {
		t.Fatalf("check: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "install", dir, "--yes"); err != nil || strings.Contains(out, "waits for your approval") {
		t.Fatalf("install: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{cols: 100, rows: 30})
	waitBoot(t, term)
	if err := term.SendKeys("S"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("SAVER-ONE", uiTimeout); err != nil {
		t.Fatalf("the saver did not show its first frame: %v\n%s", err, term.Snapshot())
	}
	art := artifactDir(t)
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "frames-saver")
	start := time.Now()
	if err := term.WaitForText("SAVER-TWO", uiTimeout); err != nil {
		t.Fatalf("the saver did not move to its second frame: %v\n%s", err, term.Snapshot())
	}
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Errorf("the second frame came after %s, want its 400 ms", d)
	}
	if err := term.WaitForText("SAVER-ONE", uiTimeout); err != nil {
		t.Errorf("the saver did not loop: %v\n%s", err, term.Snapshot())
	}
	// A key ends it.
	if err := term.SendKeys("x"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for strings.Contains(term.Snapshot(), "SAVER-") {
		if time.Now().After(deadline) {
			t.Fatalf("a key did not end the saver:\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
