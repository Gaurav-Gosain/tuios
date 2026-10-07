package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPasteBufferKeysLiveInTheirFile runs [paste_buffers] through the layered
// config: the daemon reads limit from an included file, set-config writes the
// new limit back to that file and not to config.toml, and config prune drops a
// max_kb in config.toml that only repeats the default.
//
// How it could pass wrongly: the limit could read 3 from a default, so the
// file says 3 and the default is 20. set-config could write both files, so
// config.toml is checked for the key as well. prune could drop every key, so
// the included file must keep its limit.
//
// Negative control: with [paste_buffers] left out of DefaultConfig, prune
// --dry-run does not list paste_buffers.max_kb.
func TestPasteBufferKeysLiveInTheirFile(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, `include = ["buffers.toml"]`+"\n"+copyCursorConfig+"\n[paste_buffers]\nmax_kb = 16384\n")
	part := writeConfigPart(t, base, "buffers.toml", "[paste_buffers]\nlimit = 3\n")
	if out, err := tuiosCLI(t, base, "new", pbSession, "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", pbSession}, env: copyColorOpts.env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	if l := listBuffers(t, base); l.Limit != 3 {
		t.Fatalf("the daemon reads limit %d, want 3 from buffers.toml", l.Limit)
	}

	setLive(t, base, "paste_buffers.limit", "5")
	waitForFileText(t, part, func(s string) bool { return strings.Contains(s, "limit = 5") },
		"set-config did not write paste_buffers.limit to buffers.toml, the file that sets it")
	if main := readFileString(t, configPathIn(base)); strings.Contains(main, "limit =") {
		t.Fatalf("set-config wrote paste_buffers.limit into config.toml as well:\n%s", main)
	}
	deadline := time.Now().Add(configWatchTimeout)
	for listBuffers(t, base).Limit != 5 {
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never read limit 5 from buffers.toml")
		}
		time.Sleep(150 * time.Millisecond)
	}

	dry, err := tuiosCLI(t, base, "config", "prune", "--dry-run")
	if err != nil || !strings.Contains(dry, "paste_buffers.max_kb") {
		t.Fatalf("prune --dry-run does not list paste_buffers.max_kb, which repeats the default: %v\n%s", err, dry)
	}
	if strings.Contains(dry, "paste_buffers.limit") {
		t.Fatalf("prune --dry-run lists paste_buffers.limit, which config.toml does not set:\n%s", dry)
	}
	if out, err := tuiosCLI(t, base, "config", "prune"); err != nil {
		t.Fatalf("config prune: %v\n%s", err, out)
	}
	if main := readFileString(t, configPathIn(base)); strings.Contains(main, "max_kb") {
		t.Fatalf("config prune left max_kb in config.toml:\n%s", main)
	}
	if got := readFileString(t, part); !strings.Contains(got, "limit = 5") {
		t.Fatalf("config prune changed buffers.toml:\n%s", got)
	}
	if l := listBuffers(t, base); l.Limit != 5 || l.MaxBytes != 16<<20 {
		t.Fatalf("after the prune the daemon has limit %d and max_bytes %d, want 5 and %d", l.Limit, l.MaxBytes, 16<<20)
	}
	alive(t, term, "after the paste buffer config checks")
}
