package tuie2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCopyShowsAFarNameWithEscapesAsText copies a folder from build that
// holds a file whose name carries an OSC that sets the terminal title. A file
// name may hold any byte but NUL and /, so the person who controls build can
// name a file this way. The name reaches the person in the summary of what was
// skipped, and on a terminal in the conflict question.
//
// Without a terminal, stdout and stderr must hold no ESC and no BEL, and the
// name must show as the visible text \x1b ... \x07. With --json the rel must
// decode to the exact name and the bytes must hold no raw ESC. In a pane, the
// question and the summary must show the same visible text, and the pane's
// title must not change.
//
// Negative control: with shown returning its input, the plain run prints the
// raw OSC on stdout and the pane's title becomes PWNED.
func TestCopyShowsAFarNameWithEscapesAsText(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	const evil = "a\x1b]0;PWNED\x07b.txt"
	const visible = `a\x1b]0;PWNED\x07b.txt`
	far := filepath.Join(farHome(remote), "dir")
	if err := os.MkdirAll(far, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{evil: "new from build\n", "ok.txt": "plain\n"} {
		if err := os.WriteFile(filepath.Join(far, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A file of the same name that differs here makes the name a conflict,
	// which every output names.
	conflictAt := func(dst string) {
		t.Helper()
		dir := filepath.Join(dst, "dir")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, evil), []byte("old here\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	artifact := map[string]any{}
	defer func() { saveTransferArtifact(t, "cp-far-names", artifact) }()
	noControls := func(what, s string) {
		t.Helper()
		if i := strings.IndexAny(s, "\x1b\x07"); i >= 0 {
			t.Fatalf("ASSERTION: %s holds a raw control byte %q at %d:\n%q", what, s[i], i, s)
		}
	}

	// Plain: not a terminal, so skip is the default and the run exits 3.
	plain := filepath.Join(base, "plain")
	conflictAt(plain)
	r := runTuios(t, base, "cp", "build:dir", plain)
	artifact["plain"] = r
	if r.Code != 3 {
		t.Fatalf("ASSERTION: a copy with a skipped file exited %d, want 3\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	noControls("stdout", r.Stdout)
	noControls("stderr", r.Stderr)
	if !strings.Contains(r.Stdout, visible) {
		t.Fatalf("ASSERTION: the summary does not name the skipped file as visible text %s:\n%s", visible, r.Stdout)
	}

	// JSON: the exact name, escaped by JSON.
	r = runTuios(t, base, "cp", "--json", "build:dir", plain)
	artifact["json"] = r
	noControls("--json stdout", r.Stdout)
	found := false
	for _, ev := range eventsNamed(cpEvents(t, r), "file") {
		if ev["rel"] == evil {
			found = true
		}
	}
	if !found {
		t.Fatalf("ASSERTION: no --json file event has the exact name as its rel:\n%s", r.Stdout)
	}

	// A pane: a terminal that acts on what it is sent.
	pane, cwd := copyTestPane(t, base, "work")
	conflictAt(filepath.Join(cwd, "dstp"))
	codeFile := filepath.Join(base, "pane.code")
	typeInPane(t, base, "work", pane, "clear; "+tuiosBin+" cp build:dir ./dstp; echo $? >"+codeFile)
	// A long path wraps, so the screen is read with its lines joined.
	waitJoined := func(marker string) string {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		var s string
		for time.Now().Before(deadline) {
			s = paneScreen(t, base, "work", pane)
			if strings.Contains(strings.ReplaceAll(s, "\n", ""), marker) {
				return s
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("ASSERTION: the pane never showed %q. The screen:\n%s", marker, s)
		return s
	}
	waitPaneScreen(t, base, "work", pane, 30*time.Second, "Replace it, keep both, or skip?")
	artifact["pane_ask"] = waitJoined(visible)
	pressInPane(t, base, "work", pane, "s")
	if c := waitPaneExit(t, base, "work", pane, codeFile, 30*time.Second); c != 3 {
		t.Fatalf("ASSERTION: cp in the pane after skip exited %d, want 3\n%s", c, paneScreen(t, base, "work", pane))
	}
	artifact["pane_done"] = paneScreen(t, base, "work", pane)

	out, err := tuiosCLI(t, base, "list-windows", "-s", "work", "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
			Title    string `json:"title"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("list-windows --json: %v\n%s", err, out)
	}
	for _, w := range listing.Windows {
		artifact["pane_title"] = w.Title
		if w.WindowID == pane && strings.Contains(w.Title, "PWNED") {
			t.Fatalf("ASSERTION: a far file name set the pane's title to %q", w.Title)
		}
	}
}

// TestEventsCarryAFarNameWithC1ControlsEscaped copies a folder from build
// whose file names hold U+009B (CSI) and U+009D (OSC), while tuios subscribe
// prints the copy's events. encoding/json leaves those runes raw, and a
// terminal reads them as the start of a sequence. Both the call's --json
// output and the daemon's event lines must hold them only as \u escapes, and each name
// must decode to the exact name on build.
//
// Negative control: with escapeC1 returning its input, the event lines carry
// the raw bytes C2 9B and C2 9D.
func TestEventsCarryAFarNameWithC1ControlsEscaped(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	names := []string{"csi\u009b31mred.txt", "osc\u009d0;PWNED\u009cend.txt"}
	far := filepath.Join(farHome(remote), "c1")
	if err := os.MkdirAll(far, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(far, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The daemon's own event lines, read from its socket, so the test sees
	// what every consumer gets and not only what the CLI cleans.
	sub := dialFileVerbs(t, base)
	sub.must("subscribe", map[string]any{"types": []string{"transfer", "transfer-file"}}, nil)
	var events bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_ = sub.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			line, err := sub.br.ReadBytes('\n')
			events.Write(line)
			if err != nil {
				return
			}
		}
	}()
	dst := filepath.Join(base, "here")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	r := runTuios(t, base, "cp", "--json", "--conflict", "replace", "build:~/c1", dst+"/")
	time.Sleep(time.Second)
	_ = sub.conn.Close()
	<-done
	saveTransferArtifact(t, "cp-far-names-c1", map[string]any{"cp": r, "events": events.String()})
	if r.Code != 0 {
		t.Fatalf("the copy failed: %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	for what, out := range map[string][]byte{"tuios cp --json": []byte(r.Stdout), "the daemon's events": events.Bytes()} {
		if bytes.Contains(out, []byte("\u009b")) || bytes.Contains(out, []byte("\u009d")) {
			t.Fatalf("ASSERTION: %s printed a raw C1 control:\n%q", what, out)
		}
	}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(events.String(), "\n") {
		var ev struct {
			File *struct {
				Rel string `json:"rel"`
			} `json:"file"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.File != nil {
			seen[ev.File.Rel] = true
		}
	}
	for _, n := range names {
		if !seen[n] {
			t.Fatalf("ASSERTION: no event named %q exactly; the events:\n%s", n, events.String())
		}
		if _, err := os.Stat(filepath.Join(dst, "c1", n)); err != nil {
			t.Fatalf("ASSERTION: %q did not land: %v", n, err)
		}
	}
}
