package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestPaneViewReportsTheAgentState runs pane-view in a stand-in terminal that
// reads OSC 7501, as Rex does, and checks that the pane's agent state reaches
// that terminal as pane-view's own record.
//
// How it could pass wrongly, written down first:
//   - The record could be the pane's own sequence passed through. The pane
//     writes a report with id=evil; the terminal must never see it, and the
//     command line that writes it is split so its echo cannot match either.
//   - The state could come from the pane's bytes only. The state is set with
//     set-agent-state, as a harness hook sets it, which writes nothing into
//     the pane: only pane-view's report can carry it.
//   - A record could stay when the state is gone, or when pane-view ends. The
//     state is cleared and a clear must follow; then pane-view gets SIGHUP, as
//     when its terminal block closes, and a clear must follow the last record.
//   - A terminal that does not answer the query could get records anyway. The
//     second half runs the same steps in a terminal that does not answer.
//
// The artifact is what each terminal read, under artifactDir(t).
func TestPaneViewReportsTheAgentState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers bool
	}{
		{"terminal reads OSC 7501", true},
		{"terminal does not", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			if out, err := tuiosCLI(t, base, "new", "pvs", "--detach"); err != nil {
				t.Fatalf("create the session: %v: %s", err, out)
			}
			win := firstWindow(t, base, "pvs").ID
			ft := startFakeTerm(t, base, 90, 25, fakeTermOpts{fg: "rgb:ffff/ffff/ffff", bg: "rgb:0000/0000/0000", programStatus: tc.answers},
				"pane-view", "-s", "pvs", "-w", win)
			defer func() {
				_ = os.WriteFile(filepath.Join(artifactDir(t), "terminal.raw"), []byte(ft.output()), 0o644)
			}()
			if _, ok := ft.waitFor(regexp.MustCompile(`\x1b\]7501;\?\x1b\\`), uiTimeout); !ok {
				t.Fatalf("ASSERTION: pane-view never asked the terminal about OSC 7501:\n%q", ft.output())
			}
			setState := func(args ...string) {
				t.Helper()
				full := append([]string{"set-agent-state", "-s", "pvs", "-w", win}, args...)
				if out, err := tuiosCLI(t, base, full...); err != nil {
					t.Fatalf("set-agent-state %v: %v\n%s", args, err, out)
				}
			}
			blocked := regexp.MustCompile(`\x1b\]7501;state=blocked:kind=permission:[^\x1b]*msg=` + regexp.QuoteMeta(b64e("Approve deploy?")) + `\x1b\\`)
			setState("needs_input", "--kind", "approval", "--message", "Approve deploy?")

			if !tc.answers {
				stayTrue(t, "no record for a terminal that did not answer the query", 2*time.Second, func() (bool, string) {
					out := ft.output()
					return !strings.Contains(out, "\x1b]7501;state="), fmt.Sprintf("%q", out)
				})
				return
			}
			if _, ok := ft.waitFor(blocked, uiTimeout); !ok {
				t.Fatalf("ASSERTION: the pane's needs_input never reached the terminal:\n%q", ft.output())
			}

			// The pane's own report must not pass through.
			if out, err := tuiosCLI(t, base, "send-text", "-s", "pvs", "-w", win,
				`printf '\033]7501;state=working:id=ev''il:app=rawapp\033\\'; echo RAW-$((1+1))-SENT`+"\n"); err != nil {
				t.Fatalf("send-text: %v\n%s", err, out)
			}
			if _, ok := ft.waitFor(regexp.MustCompile(`RAW-2-SENT`), shellTimeout); !ok {
				t.Fatalf("the pane's report never ran:\n%q", ft.output())
			}
			if strings.Contains(ft.output(), "id=evil") {
				t.Fatalf("ASSERTION: the pane's own OSC 7501 reached the terminal:\n%q", ft.output())
			}

			// No state: the record is cleared.
			setState("none")
			clear := regexp.MustCompile(`\x1b\]7501;state=clear\x1b\\`)
			if _, ok := ft.waitFor(clear, uiTimeout); !ok {
				t.Fatalf("ASSERTION: the record stayed after the state was cleared:\n%q", ft.output())
			}
			clears := func() int { return len(clear.FindAllString(ft.output(), -1)) }

			// A new state, then the block closes: a clear follows the record.
			setState("working", "--message", "Deploying")
			if _, ok := ft.waitFor(regexp.MustCompile(`\x1b\]7501;state=working:[^\x1b]*msg=`+regexp.QuoteMeta(b64e("Deploying"))), uiTimeout); !ok {
				t.Fatalf("ASSERTION: the working state never reached the terminal:\n%q", ft.output())
			}
			before := clears()
			if err := ft.cmd.Process.Signal(syscall.SIGHUP); err != nil {
				t.Fatalf("SIGHUP: %v", err)
			}
			if _, ok := ft.exitCode(uiTimeout); !ok {
				t.Fatalf("pane-view did not exit on SIGHUP")
			}
			if clears() <= before {
				t.Fatalf("ASSERTION: pane-view left its record on the terminal when it ended:\n%q", ft.output())
			}
		})
	}
}
