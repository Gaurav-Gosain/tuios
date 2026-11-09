package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestLayoutTemplateSaveAndLoadInTheDaemon saves a three-pane split layout
// with tuios layout save, closes two of the panes, and loads the layout back
// with tuios layout load, with no client attached. The daemon reopens the two
// panes in their folders with their names, and lays the workspace out as it
// was: every pane has the rectangle it had when the layout was saved, which a
// rebuild from the scheme would not give (the second split is down, not the
// scheme's next).
//
// How it could pass wrongly, written down first:
//   - The panes could be the old ones, never closed. The test checks they are
//     gone before the load, and that the load says it opened two.
//   - The folder could be the daemon's, which may be the same as the test's.
//     Each pane gets a folder of its own.
//   - The file could be written somewhere else and the load read a stale one.
//     The file is read from the test's own config folder.
//
// Negative control: on main there is no layout-template verb and tuios layout
// has no save, so the save step fails.
func TestLayoutTemplateSaveAndLoadInTheDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "tmpl"
	dirs := map[string]string{}
	for _, n := range []string{"left", "right", "below"} {
		d := filepath.Join(base, "work-"+n)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		// A pane reports its folder with symlinks resolved (/private/var on
		// macOS), so compare against the resolved path.
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		dirs[n] = d
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	run("new", sess, "--detach", "--cwd", dirs["left"])
	run("set-window", "-s", sess, "-w", "0", "--name", "left")
	run("split-window", "right", "-s", sess, "-w", "left", "--name", "right", "--cwd", dirs["right"], "--no-focus")
	run("split-window", "down", "-s", sess, "-w", "right", "--name", "below", "--cwd", dirs["below"], "--ratio", "30", "--no-focus")

	type win struct {
		ID   string `json:"window_id"`
		Name string `json:"display_name"`
		Cwd  string `json:"cwd"`
		X    int    `json:"x"`
		Y    int    `json:"y"`
		W    int    `json:"width"`
		H    int    `json:"height"`
		Min  bool   `json:"minimized"`
	}
	windows := func() map[string]win {
		t.Helper()
		out := run("list-windows", "-s", sess, "--json")
		var lw struct {
			Windows []win `json:"windows"`
		}
		if err := json.Unmarshal([]byte(out[strings.IndexByte(out, '{'):]), &lw); err != nil {
			t.Fatalf("list-windows json: %v\n%s", err, out)
		}
		m := map[string]win{}
		for _, w := range lw.Windows {
			m[w.Name] = w
		}
		return m
	}
	before := windows()
	if len(before) != 3 {
		t.Fatalf("want three panes before the save, got %v", before)
	}
	t.Logf("before: %+v", before)

	out := run("layout", "save", "three", "-s", sess)
	t.Logf("save: %s", out)
	file := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "layouts", "three.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ASSERTION: the save wrote no %s: %v", file, err)
	}
	if !strings.Contains(string(data), `"tree"`) || !strings.Contains(string(data), `"version": 3`) {
		t.Fatalf("ASSERTION: the saved layout has no split tree or is not version 3:\n%s", data)
	}

	run("close-window", "-s", sess, "right")
	run("close-window", "-s", sess, "below")
	if got := windows(); len(got) != 1 {
		t.Fatalf("want one pane after closing two, got %v", got)
	}

	out = run("layout", "load", "three", "-s", sess, "--json")
	t.Logf("load: %s", out)
	var res struct {
		Panes, Opened, Reused, Minimized int
	}
	_ = json.Unmarshal([]byte(out[strings.IndexByte(out, '{'):]), &res)
	if res.Panes != 3 || res.Opened != 2 || res.Reused != 1 {
		t.Fatalf("ASSERTION: the load should show 3 panes, open 2 and reuse 1: %+v", res)
	}

	after := windows()
	t.Logf("after: %+v", after)
	for _, n := range []string{"left", "right", "below"} {
		b, a := before[n], after[n]
		if a.ID == "" {
			t.Fatalf("ASSERTION: the load did not bring back %q: %+v", n, after)
		}
		if n != "left" && a.Cwd != dirs[n] {
			t.Errorf("ASSERTION: %s opened in %q, not its folder %q", n, a.Cwd, dirs[n])
		}
		if a.X != b.X || a.Y != b.Y || a.W != b.W || a.H != b.H {
			t.Errorf("ASSERTION: %s is at %d,%d %dx%d, not where it was saved, %d,%d %dx%d", n, a.X, a.Y, a.W, a.H, b.X, b.Y, b.W, b.H)
		}
	}

	out = run("call", "layout-template", `{"action":"list"}`)
	if !strings.Contains(out, `"three"`) {
		t.Fatalf("ASSERTION: list does not name the layout: %s", out)
	}
}

// TestTheTUISavesItsLayoutThroughTheDaemon saves a layout from an attached
// client with prefix L s. The daemon writes it, so the file is the daemon's
// version 3 with the workspace's split tree, which the client's own writer
// never stored, and a second load from the CLI reads the same file.
//
// Negative control: with the client's save on its own writer, as before this
// change, the file is version 2 with no tree.
func TestTheTUISavesItsLayoutThroughTheDaemon(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "tuisave"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)

	sendKeys(t, term, tuitest.Ctrl('b'), "L", "s")
	sendKeys(t, term, "d", "u", "o", tuitest.Enter)
	if err := term.WaitForText("Layout saved", uiTimeout); err != nil {
		t.Fatalf("the layout was never saved: %v\n%s", err, term.Snapshot())
	}
	file := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "layouts", "duo.json")
	var data []byte
	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ = os.ReadFile(file)
		if len(data) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(data) == 0 {
		t.Fatalf("ASSERTION: the save wrote no %s", file)
	}
	if !strings.Contains(string(data), `"version": 3`) || !strings.Contains(string(data), `"tree"`) {
		t.Fatalf("ASSERTION: the TUI's save did not go through the daemon (no version 3 file with a tree):\n%s", data)
	}
	out, err := tuiosCLI(t, base, "layout", "load", "duo", "-s", "tuisave", "--json")
	if err != nil || !strings.Contains(out, `"reused": 2`) {
		t.Fatalf("ASSERTION: the CLI load of the TUI's layout should reuse its two panes: %v\n%s", err, out)
	}
	alive(t, term, "after the load")
}
