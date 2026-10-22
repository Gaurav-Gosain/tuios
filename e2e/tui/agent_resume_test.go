package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRestartOffersToResumeTheConversation is the resume offer end to end,
// against the real binary: a pane records a conversation, the daemon is
// killed and started again, and the restored pane is offered back. The dock
// says so, the Inbox lists it under Resume with the exact command, y types
// that command into the restored shell, and the item goes.
//
// The harness is a user manifest whose resume command is an echo, so the test
// sees the command run in a real shell without a real agent.
//
// The restart is restart-server, which has to name the agent in its plan
// before it stops anything.
//
// Negative control: with the restore's conversation ids not put back after
// its state push (daemon_resurrect.go), the Inbox still lists the offer but
// resume-agent finds no conversation on the pane, and the dry run fails.
// With addResumable not called by list-windows, the plan names no agent.
func TestRestartOffersToResumeTheConversation(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "harnesses")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema_version = 1
id             = "echoer"
display_name   = "Echoer"

[detect]
comm = ["echoer-agent"]

[resume]
argv = ["echo", "resumed-{session_id}"]
`
	if err := os.WriteFile(filepath.Join(dir, "echoer.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := tuiosCLI(t, base, "new", "e2e-resume", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	// The agent is live when the state is saved, as a hook reports it: a
	// restore offers a resume only for an agent that was running then.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-resume", "working", "--harness", "echoer", "--agent-session-id", "5f1c-9a3d"); err != nil {
		t.Fatalf("record the conversation: %v\n%s", err, out)
	}
	// restart-server names the agent before it asks, from the daemon's own
	// reading of the pane.
	plan, err := tuiosCLI(t, base, "restart-server", "--dry-run")
	if err != nil || !strings.Contains(plan, `echoer in "`) || !strings.Contains(plan, `(session e2e-resume) can resume.`) ||
		!strings.Contains(plan, "This restart starts the same version.") ||
		!strings.Contains(plan, "The Inbox offers to resume each one.") {
		t.Fatalf("ASSERTION: restart-server --dry-run does not list the agent that can resume: %v\n%s", err, plan)
	}
	// The restart starts the daemon, and the new daemon restores on start.
	if out, err := tuiosCLI(t, base, "restart-server", "--yes"); err != nil {
		t.Fatalf("restart-server: %v\n%s", err, out)
	}

	if out, err := tuiosCLI(t, base, "new", "e2e-other", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v\n%s", err, out)
	}
	waitForSessionInfo(t, base, "e2e-resume")

	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "Resume") && strings.Contains(out, "echo resumed-5f1c-9a3d") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the restore made no resume offer:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "resume-agent", "-s", "e2e-resume", "--dry-run"); err != nil || strings.TrimSpace(out) != "echo resumed-5f1c-9a3d" {
		t.Fatalf("resume-agent --dry-run: %v\n%s", err, out)
	}

	// A client attached elsewhere hears about it once, with the key.
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-other"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "can resume its conversation")
	}, bootTimeout); err != nil {
		t.Fatalf("the dock never announced the offer: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-dock")

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "1 needs you") && strings.Contains(text, "Resume") && strings.Contains(text, "echo resumed-5f1c-9a3d") &&
			strings.Contains(text, "resume")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the offer: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-inbox")

	// y goes to the pane and types the command there.
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("answer the offer: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "New agent")
	}, uiTimeout); err != nil {
		t.Fatalf("y did not close the Inbox: %v\n%s", err, term.Snapshot())
	}
	// The echo ran: its output is a line of its own, apart from the command.
	deadline = time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", "e2e-resume")
		ran := false
		for line := range strings.SplitSeq(out, "\n") {
			ran = ran || strings.TrimSpace(line) == "resumed-5f1c-9a3d"
		}
		if ran {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("y did not type the resume command into the restored shell:\n%s\n%s", out, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	saveFrame(t, term, "resume-typed")

	deadline = time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "Nothing is waiting for you.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the offer stayed open after the resume:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive(t, term, "after resuming from the Inbox")
}

// TestAutoResumeWaitsForShellReadiness checks startup, foreground safety and
// the shared deadline against real shells, including restored prompt history.
func TestAutoResumeWaitsForShellReadiness(t *testing.T) {
	t.Setenv("TUIOS_RESTORED", "")
	for _, tc := range []struct {
		mode         string
		ask          bool
		minMS, maxMS int64
	}{
		{"prompt", false, 600, 5000},
		{"a-only", false, 600, 5000},
		{"busy", false, 600, 5000},
		{"unmarked", false, 250, 2000},
		{"unmarked-busy", false, 600, 5000},
		{"unmarked-timeout", true, 9500, 12000},
		{"never-ready", true, 9500, 12000},
		{"busy-timeout", true, 9500, 12000},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			started, ready := filepath.Join(base, "started"), filepath.Join(base, "ready")
			resumed, early := filepath.Join(base, "resumed-conversation"), filepath.Join(base, "early-input")
			stamp := func(path string) time.Time {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				return info.ModTime()
			}
			busy := writeAgentScript(t, base, "startup-child", fmt.Sprintf(`#!/bin/sh
[ "$1" = none ] || printf '\033]133;%%s\007' "$1"
cat < /dev/tty > %q &
reader=$!
sleep "$2"
kill "$reader"
wait "$reader" 2>/dev/null || :
[ "$1" = none ] || printf '\033]133;C\007'
`, early))
			rc := writeConfigPart(t, base, "startup.sh", fmt.Sprintf(`set -m
if [ "$TUIOS_RESTORED" = 1 ]; then
  printf started > %q
  case %q in
    prompt) %q A 0.7 ;;
    a-only) %q A 0.7 ;;
    busy) %q B 0.7 ;;
    busy-timeout) %q B 30 ;;
    unmarked) printf 'starting\n' ;;
    unmarked-busy) printf 'starting\n'; %q none 0.7 ;;
    unmarked-timeout) printf 'starting\n'; %q none 30 ;;
    never-ready)
      printf '\033]133;C\007\033]133;D;0\007'
      while IFS= read -r line; do printf '%%s\n' "$line" > %q; done ;;
  esac
  printf ready > %q
fi
PS1=$(printf '\033]133;A\007ready> \033]133;B\007')
if [ "$TUIOS_RESTORED" = 1 ] && [ %q = a-only ]; then PS1=$(printf '\033]133;A\007ready> '); fi
case "$TUIOS_RESTORED":%q in 1:unmarked*) PS1='ready> ' ;; esac
`, started, tc.mode, busy, busy, busy, busy, busy, busy, early, ready, tc.mode, tc.mode))
			shell := writeAgentScript(t, base, "startup-shell", fmt.Sprintf("#!/bin/sh\nexport ENV=%q\nexec /bin/sh -i\n", rc))
			resume := writeAgentScript(t, base, "resume-probe", "#!/bin/sh\nprintf resumed > \"$1\"\n")
			writeConfig(t, base, fmt.Sprintf("[appearance]\npreferred_shell = %q\n[daemon]\nresume_agents = 'auto'\n", shell))
			writeConfigPart(t, base, "harnesses/probe.toml", fmt.Sprintf(
				"schema_version = 1\nid = 'resume-probe'\ndisplay_name = 'Resume probe'\n[detect]\ncomm = ['resume-probe-agent']\n[resume]\nargv = [%q, %q]\n", resume, filepath.Join(base, "resumed-{session_id}")))
			mustCLI(t, base, "new", "ready", "--detach")
			waitCapture(t, base, "ready", "", "ready>")
			mustCLI(t, base, "set-agent-state", "-s", "ready", "working", "--harness", "resume-probe", "--agent-session-id", "conversation")
			mustCLI(t, base, "restart-server", "--yes")
			deadline := time.Now().Add(12 * time.Second)
			for {
				if input, _ := os.ReadFile(early); len(input) > 0 {
					t.Fatalf("resume input reached a startup program: %q", input)
				}
				if tc.ask {
					if strings.Contains(mustCLI(t, base, "list-attention"), "Resume") {
						break
					}
				} else if content, _ := os.ReadFile(resumed); string(content) == "resumed" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("no resume outcome:\n%s", mustCLI(t, base, "list-attention"))
				}
				time.Sleep(50 * time.Millisecond)
			}
			elapsed := time.Since(stamp(started))
			if !tc.ask {
				elapsed = stamp(resumed).Sub(stamp(started))
				if stamp(resumed).Before(stamp(ready)) {
					t.Error("resumed before shell startup finished")
				}
			} else if _, err := os.Stat(resumed); !os.IsNotExist(err) {
				t.Errorf("timed-out pane ran its resume command: %v", err)
			}
			if ms := elapsed.Milliseconds(); ms < tc.minMS || ms > tc.maxMS {
				t.Errorf("resume outcome after %s; want %d..%d ms", elapsed, tc.minMS, tc.maxMS)
			}
			t.Logf("%s: ask=%v after %s", tc.mode, tc.ask, elapsed)
			term := startIn(t, base, startOpts{args: []string{"attach", "ready"}})
			if err := term.WaitForText("ready>", bootTimeout); err != nil {
				t.Fatal(err)
			}
			saveFrame(t, term, "resume-readiness-"+tc.mode)
		})
	}
}
