package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// plugins/pr-status, the shipped folder, against a fake gh on PATH. The hook
// sets the cell from a focus, with no process left behind. gh runs once for
// many focuses. A click needs a real browser, so refresh stands in for it.
func TestPluginPRStatus(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	reply := filepath.Join(bin, "reply")
	fake := "#!/bin/sh\necho \"$*\" >> " + calls + "\ncat " + reply + "\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reply, []byte("#7 running\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	cwd := workDirIn(t, base)
	testutil.Git(t, cwd, "init", "-q", "-b", "work")
	testutil.Git(t, cwd, "-c", "user.name=e2e", "-c", "user.email=e2e@localhost", "commit", "-q", "--allow-empty", "-m", "x")
	if out, err := tuiosCLI(t, base, "new", "pr", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	plugin, err := filepath.Abs(filepath.Join("..", "..", "plugins", "pr-status"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tuiosCLI(t, base, "plugins", "install", plugin, "--yes")
	if err != nil || !strings.Contains(out, "ui     Shows cells") || strings.Contains(out, "startup") {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(plugin, "herdr-plugin.toml")); err == nil {
		t.Error("pr-status has a second manifest")
	}
	if out, _ := tuiosCLI(t, base, "plugins", "info", "tuios.pr-status"); !strings.Contains(out, "pane.focused") || strings.Contains(out, "warning") {
		t.Errorf("info:\n%s", out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "pr"}})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}
	// A new pane takes the focus, and the hook runs.
	if out, err := tuiosCLI(t, base, "new-window", "-s", "pr"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	if err := term.WaitForText("#7 running", uiTimeout); err != nil {
		log, _ := tuiosCLI(t, base, "plugins", "log", "tuios.pr-status")
		t.Fatalf("the hook did not set the cell: %v\n%s\n%s", err, log, term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), artifactDir(t), "pr-status")

	// More focuses reuse the saved text.
	if out, err := tuiosCLI(t, base, "new-window", "-s", "pr"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	time.Sleep(time.Second)
	b, _ := os.ReadFile(calls)
	if n := strings.Count(string(b), "pr view"); n != 1 {
		t.Errorf("gh ran %d times for two focuses in a minute, want 1:\n%s", n, b)
	}

	// Nothing stays: the hook exits, so the plugin holds no process, and stats
	// says so with the runs counted.
	if out, err := tuiosCLI(t, base, "plugins", "stats"); err != nil || !regexp.MustCompile(`tuios\.pr-status\s+0\s+0 bytes\s+[2-9]`).MatchString(out) {
		t.Errorf("plugins stats for a hook plugin: %v, want no process and two runs or more\n%s", err, out)
	}
	if out, _ := tuiosCLI(t, base, "plugins", "log", "tuios.pr-status", "--limit", "20"); strings.Contains(out, "running\n") {
		t.Errorf("a hook still runs:\n%s", out)
	}

	// Refresh reads now, and a pull request that is gone clears the cell.
	if err := os.WriteFile(reply, []byte("#7 ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "run", "tuios.pr-status", "refresh", "--wait"); err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	if err := term.WaitForText("#7 ok", uiTimeout); err != nil {
		t.Fatalf("refresh did not change the cell: %v\n%s", err, term.Snapshot())
	}
	if err := os.WriteFile(reply, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "run", "tuios.pr-status", "refresh", "--wait"); err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for strings.Contains(term.Snapshot(), "#7 ok") {
		if time.Now().After(deadline) {
			t.Fatalf("the cell stays with no pull request:\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
