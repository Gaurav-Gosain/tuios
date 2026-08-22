package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// Tab under the Kitty keyboard protocol. A terminal that has negotiated
// disambiguation or report-all-keys sends Tab as CSI 9 u rather than as a bare
// 0x09, and adds a release event when event types are on.
const (
	kittyTab        = "\x1b[9u"
	kittyTabNoMods  = "\x1b[9;1u"
	kittyTabRelease = "\x1b[9;1:3u"
)

func tabEncodingCase(t *testing.T, name string, keys ...string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		dir := writeProbe(t)
		term, _ := start(t, startOpts{
			cols: 160, rows: 45,
			args: []string{"--standalone"},
			env:  []string{"PATH=" + dir + ":/usr/bin:/bin"},
		})
		waitBoot(t, term)
		queryProbe(t, term)

		for _, k := range keys {
			if err := term.SendKeys(tuitest.Key(k)); err != nil {
				t.Fatalf("send %q: %v", k, err)
			}
		}
		// The launcher must have closed, which is the first thing Tab does.
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !strings.Contains(s.Text(), launcherTitle)
		}, uiTimeout); err != nil {
			t.Fatalf("Tab in this encoding did nothing; the launcher is still open: %v\n%s",
				err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatalf("enter: %v", err)
		}
		if err := term.WaitForText(runAnythingMarker, shellTimeout); err != nil {
			t.Fatalf("no command was waiting at the prompt: %v\n%s", err, term.Snapshot())
		}
	})
}

func TestLauncherTabEncodings(t *testing.T) {
	tabEncodingCase(t, "raw-0x09", "\t")
	tabEncodingCase(t, "kitty-csi-u", kittyTab)
	tabEncodingCase(t, "kitty-csi-u-nomods", kittyTabNoMods)
	tabEncodingCase(t, "kitty-press-and-release", kittyTab, kittyTabRelease)
}
