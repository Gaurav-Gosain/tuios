package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestWhichKeyDelayIsAnOption: appearance.whichkey_delay_ms is the pause after
// the leader before the which-key panel opens. At 2500 ms the panel is not up
// 1500 ms after the leader, and is up once the delay has passed. At the default
// (500 ms) it is up well inside the first run's window.
//
// Negative control: reading the fixed 500 ms again opens the panel inside the
// 1500 ms window of the 2500 ms run and fails it.
func TestWhichKeyDelayIsAnOption(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		upBy         time.Duration // the panel is up by then
		notBefore    time.Duration // and is not up at this point; 0 for no check
	}{
		{"default", "", 1500 * time.Millisecond, 0},
		{"2500 ms", "[appearance]\nwhichkey_delay_ms = 2500\n", 6 * time.Second, 1500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, tc.config)
			term := startIn(t, base, startOpts{})
			waitBoot(t, term)
			newWindow(t, term)
			if baseline := term.Snapshot(); strings.Contains(baseline, "prefix") {
				t.Fatalf("the screen already says prefix before the leader:\n%s", baseline)
			}
			pause(t, "the guard after boot before the leader is pressed", insertGuard)
			sentAt := time.Now()
			if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
				t.Fatalf("press the leader: %v", err)
			}
			if tc.notBefore > 0 {
				stayTrue(t, "the which-key panel stays down before the delay", tc.notBefore, func() (bool, string) {
					text := term.Snapshot()
					return !strings.Contains(text, "prefix"), fmt.Sprintf("up %v after the leader:\n%s", time.Since(sentAt), text)
				})
			}
			if err := term.WaitForText("prefix", tc.upBy); err != nil {
				t.Fatalf("the which-key panel never opened: %v\n%s", err, term.Snapshot())
			}
		})
	}
}
