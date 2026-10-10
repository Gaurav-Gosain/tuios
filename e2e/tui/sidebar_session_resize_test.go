package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSidebarSessionResizeUsesRailGeometry checks the guest's actual PTY size,
// not WindowState's layout fields: a passive rail has no session layout engine
// to write those fields back to the daemon.
func TestSidebarSessionResizeUsesRailGeometry(t *testing.T) {
	const cfg = `[appearance.sidebar]
position = "right"
enabled = true
width = 36
[appearance.sidebar.right]
session = "side"
`
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, cfg)
	for _, name := range []string{"center", "side"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 100, rows: 35, args: []string{"attach", "center"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "side") && strings.Contains(s.Text(), "sh-3.2$")
	}, bootTimeout); err != nil {
		t.Fatalf("side rail did not attach: %v\n%s", err, term.Snapshot())
	}
	guestSize := func(marker string) (rows, cols int) {
		t.Helper()
		command := fmt.Sprintf(`printf '%s %%s\n' "$(stty size)"`, marker)
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "--literal", command); err != nil {
			t.Fatalf("send %s size query: %v\n%s", marker, err, out)
		}
		if out, err := tuiosCLI(t, base, "send-keys", "-s", "side", "Enter"); err != nil {
			t.Fatalf("run %s size query: %v\n%s", marker, err, out)
		}
		pattern := regexp.MustCompile(regexp.QuoteMeta(marker) + ` (\d+) (\d+)`)
		deadline := time.Now().Add(uiTimeout)
		for {
			out, err := tuiosCLI(t, base, "capture-pane", "-s", "side")
			if err == nil {
				if match := pattern.FindStringSubmatch(out); len(match) == 3 {
					rows, _ = strconv.Atoi(match[1])
					cols, _ = strconv.Atoi(match[2])
					return rows, cols
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s size not captured: %v\n%s\n%s", marker, err, out, term.Snapshot())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	beforeRows, beforeCols := guestSize("BEFORESIZE")
	if err := term.Resize(100, 43); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { _, rows := s.Size(); return rows == 43 }, uiTimeout); err != nil {
		t.Fatalf("host resize not visible: %v", err)
	}
	afterRows, afterCols := 0, 0
	deadline := time.Now().Add(uiTimeout)
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		afterRows, afterCols = guestSize(fmt.Sprintf("AFTERSIZE%d", attempt))
		if afterRows > beforeRows {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if beforeCols > 36 || beforeCols < 20 || afterCols != beforeCols || afterRows <= beforeRows {
		t.Fatalf("PTY failed to follow 36-column rail and increased height: %dx%d -> %dx%d\n%s", beforeCols, beforeRows, afterCols, afterRows, term.Snapshot())
	}
	t.Logf("rail guest PTY follows viewport: %dx%d -> %dx%d\n%s", beforeCols, beforeRows, afterCols, afterRows, term.Snapshot())
}
