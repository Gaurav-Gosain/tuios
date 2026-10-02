package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A graphics app started with `tuios new-window NAME -- prog` takes the
// alternate screen after the client last pushed its state, so the daemon's copy
// of the window still says normal screen. Every state broadcast the daemon
// made after that (a rename, an agent report, any daemon-side change) handed
// the client that stale flag, and the client adopted it over what its own
// emulator had seen. The passthrough then read the pane as having left the
// alternate screen and deleted the app's image. An app that draws only on
// damage, as a compositor does, never placed it again.
//
// The stand-in in ./altonce takes the alternate screen after a delay, draws one
// frame and waits. A rename through the CLI is the daemon-side change.
func TestKittyAltScreenImageSurvivesStateSync(t *testing.T) {
	bin := buildAltOnce(t)
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "kitty", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	host := newKittyHost()
	term := attachIn(t, base, "kitty", startOpts{
		cols: 120, rows: 40,
		env: []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out: host,
	})
	host.answerProbe(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("the session's pane never showed: %v\n%s", err, term.Snapshot())
	}

	// The delay puts the screen switch after the client has adopted the new
	// pane and pushed its state, which is where a slow-starting app puts it.
	if out, err := tuiosCLI(t, base, "new-window", "img", "-s", "kitty", "--", bin, "2s"); err != nil {
		t.Fatalf("open the graphics pane: %v\n%s", err, out)
	}
	if err := term.WaitForText("ALTONCE-DRAWN", shellTimeout); err != nil {
		t.Fatalf("the graphics app never drew: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)

	before := imageHistory(host.bytes(), "")
	if len(before) == 0 || !strings.HasPrefix(before[len(before)-1], "a=p") {
		t.Fatalf("the image was not on screen before the rename, so nothing here tests it:\n  %s",
			strings.Join(before, "\n  "))
	}

	host.mark("rename")
	if out, err := tuiosCLI(t, base, "set-window", "-s", "kitty", "-w", "img", "--name", "renamed"); err != nil {
		t.Fatalf("rename the pane: %v\n%s", err, out)
	}
	if err := term.WaitForText("renamed", uiTimeout); err != nil {
		t.Fatalf("the rename never reached the client: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(2 * time.Second)

	after := imageHistory(host.bytes(), "rename")
	t.Logf("after the rename, commands naming the image:\n  %s", strings.Join(after, "\n  "))
	for _, c := range after {
		if strings.HasPrefix(c, "a=d") {
			t.Fatalf("a daemon state broadcast took the image down: %s", c)
		}
	}
}

// imageHistory lists the placements and deletes of the 64x64 image in the
// host stream, from the named phase on, or from the start when phase is empty.
func imageHistory(stream []byte, phase string) []string {
	cmds := wireCmds(stream)
	hostID := 0
	for _, c := range cmds {
		if (c.action == "t" || c.action == "T") && c.pixW == 64 && c.pixH == 64 {
			hostID = c.image
		}
	}
	var out []string
	for _, c := range cmds {
		if phase != "" && c.phase != phase {
			continue
		}
		if hostID != 0 && c.image == hostID && (c.action == "p" || c.action == "d") {
			out = append(out, fmt.Sprintf("a=%s %s", c.action, c.params))
		}
	}
	return out
}

// buildAltOnce compiles the alternate-screen stand-in once per test binary.
func buildAltOnce(t *testing.T) string {
	t.Helper()
	altOnceOnce.Do(func() {
		dir, err := os.MkdirTemp("", "altonce")
		if err != nil {
			altOnceErr = err
			return
		}
		bin := filepath.Join(dir, "altonce")
		build := exec.Command("go", "build", "-o", bin, "./altonce")
		if out, err := build.CombinedOutput(); err != nil {
			altOnceErr = fmt.Errorf("build altonce: %v\n%s", err, out)
			return
		}
		altOnceBin = bin
	})
	if altOnceErr != nil {
		t.Fatalf("%v", altOnceErr)
	}
	return altOnceBin
}

var (
	altOnceOnce sync.Once
	altOnceBin  string
	altOnceErr  error
)
