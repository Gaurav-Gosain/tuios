package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestAPaneProcessStaysInItsPaneAfterTheKeeperTakesOver: when the new image
// of a live upgrade dies before it is up, the keeper starts the previous
// binary as a daemon with a new pid. The pane shells were children of the
// image that died, so their parent is now init or a subreaper, and no pane
// reaches the new daemon by its parent chain.
//
// A process in a pane with no terminal (setsid) and no tuios variables in its
// environment (env -i) is then placed only by its parent chain. It sends as
// human, which only the person may do. It is refused before the upgrade, when
// the pane shell is the daemon's child, and after it, because the daemon
// records the pane shells it adopts as processes it started.
//
// The same call from the test process, outside every pane, is served: the
// refusal is about where the caller runs, not about the call.
//
// Negative control: with the adopted shell not recorded (adoptPTY leaves
// procStart at 0), the probe after the takeover exits 0 and the message from
// human reaches the ring.
func TestAPaneProcessStaysInItsPaneAfterTheKeeperTakesOver(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("setsid(1) and the keeper path are checked on Linux")
	}
	base := t.TempDir()
	startHandoffDaemon(t, base, "TUIOS_TEST_HANDOFF_CRASH=start")
	second := copyTuios(t)
	const sess = "e2e-keeper-place"
	target := newCommandWindow(t, base, sess, "target", false, "sh", "-c", "echo pid-$$; sleep 600")
	waitHandoffPane(t, base, sess, target, regexp.MustCompile(`^pid-\d+`), 10*time.Second)

	outDir := t.TempDir()
	// Each line read runs one probe: a new session with no terminal, an
	// environment with the XDG folders and nothing of tuios, and the call.
	// setsid -w waits for the probe, so the probe stays a child of the pane.
	script := `echo pid-$$; while read l; do ` +
		`setsid -w env -i PATH="$PATH" HOME="$HOME" $(env | grep '^XDG_') ` +
		posixQuote(tuiosBin) + ` send-agent-message -s ` + sess + ` -w ` + target + ` --from human "probe-$l" ` +
		`</dev/null >` + posixQuote(outDir) + `/out-$l 2>&1; echo rc-$l-$?; done`
	prober := newCommandWindow(t, base, sess, "prober", false, "sh", "-c", script)
	waitHandoffPane(t, base, sess, prober, regexp.MustCompile(`^pid-\d+`), 10*time.Second)

	probe := func(tag string) (string, string) {
		t.Helper()
		_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": prober, "text": tag + "\n"})
		m := waitHandoffPane(t, base, sess, prober, regexp.MustCompile(`^rc-`+tag+`-(\d+)`), 20*time.Second)
		out, _ := os.ReadFile(filepath.Join(outDir, "out-"+tag))
		return m[1], string(out)
	}

	rcBefore, outBefore := probe("before")
	img := helloImage(t, base)
	_, upOut, upErr := daemonUpgrade(t, base, second)
	after := helloImage(t, base)
	rcAfter, outAfter := probe("after")
	ring, ringErr := tuiosCLI(t, base, "read-agent-messages", "-s", sess, "--peek", "--json")
	outside, outsideErr := tuiosCLI(t, base, "send-agent-message", "-s", sess, "-w", target, "--from", "human", "probe-outside")

	writeHandoffArtifact(t, map[string]any{
		"daemon_pid_before": img.PID, "daemon_pid_after": after.PID,
		"probe_before_rc": rcBefore, "probe_before_out": outBefore,
		"probe_after_rc": rcAfter, "probe_after_out": outAfter,
		"upgrade_output": upOut, "upgrade_error": errString(upErr),
		"ring": ring, "ring_error": errString(ringErr),
		"outside_out": outside, "outside_error": errString(outsideErr),
	})

	if after.PID == img.PID {
		t.Fatalf("ASSERTION: the daemon is still pid %d after the crash of the new image: the keeper did not take over\n%s", img.PID, upOut)
	}
	if rcBefore == "0" || !strings.Contains(outBefore, "only the person") {
		t.Fatalf("ASSERTION: before the upgrade, the detached probe in a pane was not refused (exit %s):\n%s", rcBefore, outBefore)
	}
	if rcAfter == "0" || !strings.Contains(outAfter, "only the person") {
		t.Errorf("ASSERTION: after the keeper took over, the detached probe in a pane was not refused (exit %s):\n%s", rcAfter, outAfter)
	}
	if strings.Contains(ring, "probe-before") || strings.Contains(ring, "probe-after") {
		t.Errorf("ASSERTION: a refused probe reached the ring:\n%s", ring)
	}
	if outsideErr != nil {
		t.Errorf("ASSERTION: the same call from outside every pane failed: %v\n%s", outsideErr, outside)
	}
}
