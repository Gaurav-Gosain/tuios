package federation

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestProbeScriptIsOneSafeLine pins what lets the script cross any login
// shell: it is one line, and it holds none of the characters a shell could
// read inside single quotes. It also has to parse, in sh and in dash, which
// is the shell most remote machines run it in.
func TestProbeScriptIsOneSafeLine(t *testing.T) {
	for _, announce := range []bool{true, false} {
		script := remoteProbeScript(announce)
		for _, bad := range []string{"'", `\`, "\n", "!"} {
			if strings.Contains(script, bad) {
				t.Errorf("ASSERTION: the probe script contains %q, which a remote login shell could read inside single quotes:\n%s", bad, script)
			}
		}
		for _, c := range remoteBinaryCandidates {
			if !strings.Contains(script, `"`+c+`"`) {
				t.Errorf("ASSERTION: the probe script does not test %s", c)
			}
		}
		for _, sh := range []string{"sh", "dash", "bash"} {
			if _, err := exec.LookPath(sh); err != nil {
				continue
			}
			out, err := exec.Command(sh, "-n", "-c", script).CombinedOutput() //nolint:gosec // the script under test, parsed only
			if err != nil {
				t.Errorf("ASSERTION: %s does not parse the probe script: %v\n%s", sh, err, out)
			}
		}
	}
}

// waitReport polls the one host's report until want accepts it.
func waitReport(t *testing.T, m *Manager, want func(HostReport) bool, why string) HostReport {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var r HostReport
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r = m.Reports(ctx)[0]
		cancel()
		if want(r) {
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s. Last report: %s / %s / %s / command %q", why, r.Status, r.Reason, r.Detail, r.Command)
	return r
}

// waitStatus polls the one host's report until it has the wanted status.
func waitStatus(t *testing.T, m *Manager, want Status) HostReport {
	t.Helper()
	return waitReport(t, m, func(r HostReport) bool { return r.Status == want },
		"the host never reached "+string(want))
}
