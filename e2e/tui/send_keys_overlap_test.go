package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestOverlappingSendKeysDoNotAnswerAnApproval: two send-keys commands
// whose key chains overlap on the client are both not the person. The
// client used to keep one flag for "a send-keys chain runs": the first chain
// to end cleared it while the other still delivered keys, and those keys
// passed every "did the person press this" gate. Here they type allow and
// Enter into a waiting plugin approval, which must stay unanswered.
//
// One long chain clears the field many times and then types allow and
// Enter. Short chains start and end while it runs. The person then types
// allow and Enter, which must allow the plugin, so the dialog was live the
// whole time.
//
// The negative control is in NEGATIVE_CONTROLS.md, "Overlapping send-keys".
func TestOverlappingSendKeysDoNotAnswerAnApproval(t *testing.T) {
	base := t.TempDir()
	approvalSession(t, base, 1)
	approvalPlugin(t, base, "e2e.overlap", "Overlap probe", `"write"`, `echo 0 >"$HERDR_PLUGIN_STATE_DIR/ran.code"`+"\n")
	art := artifactDir(t)

	term := startIn(t, base, startOpts{args: []string{"attach", "work"}})
	if err := term.WaitForText("Allow the plugin Overlap probe?", bootTimeout); err != nil {
		t.Fatalf("no approval dialog: %v\n%s", err, term.Snapshot())
	}
	// Past the half second the dialog ignores keys for.
	time.Sleep(700 * time.Millisecond)
	saveArtifact(t, term, art, "overlap-dialog")

	long := strings.Repeat("ctrl+u ", 400) + "a l l o w Enter"
	logs := make([]string, 0, 32)
	var mu sync.Mutex
	note := func(s string) {
		mu.Lock()
		logs = append(logs, s)
		mu.Unlock()
	}
	var wg sync.WaitGroup
	for round := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := tuiosCLI(t, base, "send-keys", "-s", "work", long)
			note("long chain " + string(rune('A'+round)) + ": " + strings.TrimSpace(out) + errText(err))
		}()
		for range 8 {
			time.Sleep(15 * time.Millisecond)
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, err := tuiosCLI(t, base, "send-keys", "-s", "work", "ctrl+u")
				note("short chain: " + strings.TrimSpace(out) + errText(err))
			}()
		}
		wg.Wait()
	}
	_ = os.WriteFile(filepath.Join(art, "overlap-send-keys.txt"), []byte(strings.Join(logs, "\n")+"\n"), 0o644)

	time.Sleep(time.Second)
	saveArtifact(t, term, art, "overlap-after-send-keys")
	if got := readPluginFile(base, "e2e.overlap", "ran.code"); got != "" {
		t.Fatalf("overlapping send-keys allowed the plugin: ran.code is %q\n%s", got, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "Allow the plugin Overlap probe?") {
		t.Fatalf("the dialog closed under keys that were not the person's:\n%s", term.Snapshot())
	}

	// The person's own keys still allow it.
	for _, k := range []string{"\x15", "a", "l", "l", "o", "w", "\r"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Allow the plugin") }, uiTimeout); err != nil {
		t.Fatalf("the person's allow did not close the dialog: %v\n%s", err, term.Snapshot())
	}
	got := strings.TrimSpace(waitPluginFile(t, base, "e2e.overlap", "ran.code", "\n"))
	saveArtifact(t, term, art, "overlap-person-allowed")
	if got != "0" {
		t.Fatalf("the plugin did not run after the person allowed it: %q", got)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return " (" + err.Error() + ")"
}
