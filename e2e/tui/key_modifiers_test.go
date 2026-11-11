package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// TestModifierSpellingsMeanOneThing: every place a person or an agent writes
// a key reads a modifier's spellings the same way, from one table. meta is Alt
// everywhere.
//
//   - herdr's pane.send_keys takes opt+x and ctl+a as send-keys does.
//     Before, herdr refused opt and ctl with invalid_key, and send-keys took
//     them.
//   - The same keys through tuios send-keys give the same bytes: the positive
//     half, which held before.
//   - tuios keybinds explain meta+2 reads it as alt+2 and finds what alt+2
//     does. Before, config refused meta ("invalid modifier: meta") and
//     explain found nothing, while send-keys sent meta+2 as alt+2.
//
// The pane runs testdata/keydump, which prints every read in hex; its screen
// is the artifact.
//
// NEGATIVE CONTROL: with "opt" and "ctl" taken out of modifierSpellings
// (internal/config/keynormalizer.go), herdr's pane.send_keys refuses opt+x;
// with "meta" taken out, explain meta+2 has no read_as.
func TestModifierSpellingsMeanOneThing(t *testing.T) {
	_, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	bin := filepath.Join(t.TempDir(), "keydump")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/keydump").CombinedOutput(); err != nil {
		t.Fatalf("build keydump: %v\n%s", err, out)
	}
	const name = "mods"
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--no-focus", name, "--", "/bin/sh", "-c", "stty raw -echo; exec '"+bin+"' legacy"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	waitCapture(t, base, sess, name, "keydump legacy ready")

	pane := herdrPaneByLabel(t, base, name)["pane_id"].(string)
	herdr := func(key, want string) {
		t.Helper()
		herdrCall(t, base, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{key}})
		waitLine(t, base, sess, name, want)
	}
	sendKeys := func(key, want string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-keys", "-s", sess, "-w", name, key); err != nil {
			t.Fatalf("ASSERTION: send-keys %s: %v\n%s", key, err, out)
		}
		waitLine(t, base, sess, name, want)
	}
	herdr("opt+x", "read1=1b78")
	herdr("ctl+a", "read2=01")
	herdr("meta+y", "read3=1b79")
	sendKeys("opt+x", "read4=1b78")
	sendKeys("ctl+a", "read5=01")
	sendKeys("meta+y", "read6=1b79")

	type fate struct {
		ReadAs string `json:"read_as"`
		Acts   []any  `json:"acts"`
	}
	explain := func(key string) fate {
		t.Helper()
		out, err := tuiosCLI(t, base, "keybinds", "explain", "--json", key)
		if err != nil {
			t.Fatalf("keybinds explain %s: %v\n%s", key, err, out)
		}
		var f fate
		if err := json.Unmarshal([]byte(out), &f); err != nil {
			t.Fatalf("keybinds explain %s is not JSON: %v\n%s", key, err, out)
		}
		return f
	}
	alt, meta := explain("alt+2"), explain("meta+2")
	if len(alt.Acts) == 0 {
		t.Fatalf("alt+2 does nothing in this config, so it cannot show what meta+2 means")
	}
	if meta.ReadAs != "alt+2" || !reflect.DeepEqual(meta.Acts, alt.Acts) {
		t.Errorf("ASSERTION: keybinds explain meta+2 reads it as %q with %d acts, want alt+2 with the %d acts alt+2 has", meta.ReadAs, len(meta.Acts), len(alt.Acts))
	}

	if err := os.WriteFile(filepath.Join(artifactDir(t), "pane.txt"), []byte(capture(t, base, sess, name)), 0o644); err != nil {
		t.Errorf("save the pane: %v", err)
	}
}
