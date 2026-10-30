package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRestartOffersToResumeTheConversation is the resume offer end to end,
// against the real binary: a pane records a conversation, the daemon is
// killed and started again, and the restored pane is offered back. The dock
// says so, the Inbox lists it under Resume with the exact command and names
// the key that resumes them all. x marks two of five offers and y resumes
// both, y alone resumes the row under the cursor, and * then y resumes the
// rest. The view stays open through all three, and the items go.
//
// The harness is a user manifest whose resume command is an echo, so the test
// sees the command run in a real shell without a real agent.
//
// The restart is restart-server, which has to name the agent in its plan
// before it stops anything.
//
// One of the five is a fake Claude Code, a script named claude on PATH that
// prints its arguments. It is started with --permission-mode plan --model
// sonnet --dangerously-skip-permissions and with one-shot flags and a prompt.
// The resume command the restore offers, and the command line the resumed
// process gets from y, must carry the three kept flags and none of the rest.
// The skip flag turns approvals off, so the dry run, list-attention and the
// Inbox row must say so.
//
// Negative control: with the restore's conversation ids not put back after
// its state push (daemon_resurrect.go), the Inbox still lists the offer but
// resume-agent finds no conversation on the pane, and the dry run fails.
// With addResumable not called by list-windows, the plan names no agent.
// With y not routed to InboxAgentsBulkResume while rows are marked
// (inbox_input.go), y with two marks resumes one. With the kept flags not
// noted by the detection poll (agent_detect.go), or not put back after the
// restore's state push (daemon_resurrect.go), the Claude offer has no flags.
// With keep_risky cut from the Claude manifest, nothing names approvals off.
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

	// The fake Claude: started fresh it prints its arguments and stays, as
	// an agent does; started on a conversation it prints them, then the line
	// the echoers print, and exits. PATH holds it and the system only, so the
	// resume never reaches a real claude.
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeClaude := "#!/bin/sh\nprintf 'claude-argv:'; printf ' %s' \"$@\"; printf '\\n'\n" +
		"if [ \"$1\" = --resume ]; then echo \"resumed-$2\"; exit 0; fi\nsleep 600\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))

	// Five agents, each with its own conversation: two are picked with x and
	// resumed with y, one is resumed alone with y, and * then y resumes the
	// last two. e2e-resume-c is the fake Claude.
	const claudeSession = "e2e-resume-c"
	convs := map[string]string{
		"e2e-resume": "5f1c-9a3d", "e2e-resume-b": "b2c4-7e10", claudeSession: "c3d5-8f21",
		"e2e-resume-d": "d4e6-9a32", "e2e-resume-e": "e5f7-0b43",
	}
	harnessOf := func(name string) string {
		if name == claudeSession {
			return "claude-code"
		}
		return "echoer"
	}
	// The command each offer types: the echo, or claude on the conversation
	// with the two kept flags and nothing else from its command line.
	claudeResume := "claude --resume " + convs[claudeSession] + " --permission-mode plan --model sonnet --dangerously-skip-permissions"
	const riskyWarning = "Resumes with approvals off (--dangerously-skip-permissions)"
	commandOf := func(name string) string {
		if name == claudeSession {
			return claudeResume
		}
		return "echo resumed-" + convs[name]
	}
	for name, conv := range convs {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create the session %s: %v\n%s", name, err, out)
		}
		// The agent is live when the state is saved, as a hook reports it: a
		// restore offers a resume only for an agent that was running then.
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", name, "working", "--harness", harnessOf(name), "--agent-session-id", conv); err != nil {
			t.Fatalf("record the conversation of %s: %v\n%s", name, err, out)
		}
	}
	// The person starts Claude with the flags they want, a one-shot flag or
	// two and a prompt. The detector reads its command line, and the resume
	// the pane would type now keeps the flags the manifest lists.
	mustCLI(t, base, "send-text", "-s", claudeSession, "claude --permission-mode plan --model sonnet --dangerously-skip-permissions -p --continue fix-the-bug\n")
	waitUnwrapped(t, base, claudeSession, "claude-argv: --permission-mode plan --model sonnet --dangerously-skip-permissions -p --continue fix-the-bug")
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "resume-agent", "-s", claudeSession, "--dry-run")
		if err == nil && strings.TrimSpace(out) == claudeResume+"\n"+riskyWarning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the running Claude's resume does not keep its flags: %v\n%s", err, out)
		}
		time.Sleep(100 * time.Millisecond)
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
	for name := range convs {
		waitForSessionInfo(t, base, name)
	}

	deadline = time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		all := strings.Contains(out, "Resume")
		for name := range convs {
			all = all && strings.Contains(out, commandOf(name))
		}
		all = all && strings.Contains(out, riskyWarning+": "+claudeResume)
		if all {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the restore did not offer every resume:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "resume-agent", "-s", "e2e-resume", "--dry-run"); err != nil || strings.TrimSpace(out) != "echo resumed-5f1c-9a3d" {
		t.Fatalf("resume-agent --dry-run: %v\n%s", err, out)
	}

	// A client attached elsewhere hears about it once, with the keys.
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-other"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "5 agent conversations can be resumed")
	}, bootTimeout); err != nil {
		t.Fatalf("the dock never announced the offers: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-dock")

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	// The footer names the one step that resumes them all.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "5 needs you") && strings.Contains(text, "Resume") && strings.Contains(text, "echo resumed-") &&
			strings.Contains(text, "* y resume all") && strings.Contains(text, "Resumes with approvals off")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Inbox never listed the offers with the resume all key and the approvals warning: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-inbox")
	// The card of the Claude row has room for the whole warning, and marks
	// the row risky. j walks to it, and k walks back to where the cursor was.
	steps := 0
	for ; steps < len(convs); steps++ {
		if term.WaitFor(func(s tuitest.Screen) bool {
			text := s.Text()
			return strings.Contains(text, riskyWarning) && riskyBadge(text)
		}, time.Second) == nil {
			break
		}
		if err := term.SendKeys("j"); err != nil {
			t.Fatalf("move down: %v", err)
		}
	}
	if steps == len(convs) {
		t.Fatalf("ASSERTION: no card names the whole approvals warning:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "resume-risky-card")
	for range steps {
		if err := term.SendKeys("k"); err != nil {
			t.Fatalf("move up: %v", err)
		}
	}

	// ran reports whether a session's pane shows the echo's output on a line
	// of its own, apart from the command.
	ran := func(name string) bool {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", name)
		for line := range strings.SplitSeq(out, "\n") {
			if strings.TrimSpace(line) == "resumed-"+convs[name] {
				return true
			}
		}
		return false
	}
	// waitRan waits until n of the agents have resumed, and says which
	// have not.
	waitRan := func(n int, what string) []string {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			var resumed, left []string
			for name := range convs {
				if ran(name) {
					resumed = append(resumed, name)
				} else {
					left = append(left, name)
				}
			}
			if len(resumed) == n {
				return left
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s: resumed %v, want %d in all\n%s", what, resumed, n, term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	// stayed waits for the view to be open on the given count, with no
	// marks.
	stayed := func(count, what string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			text := s.Text()
			return strings.Contains(text, "New agent") && strings.Contains(text, count) && !strings.Contains(text, " marked ")
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: the view did not stay open on %q: %v\n%s", what, count, err, term.Snapshot())
		}
	}

	// Pick some: x marks the first two rows, and y resumes both with the
	// view still open (#1330).
	if err := term.SendKeys("x", "x"); err != nil {
		t.Fatalf("mark two rows: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		// Only Resume rows are marked, so the footer offers no key that
		// answers an agent.
		return strings.Contains(text, "2 marked") && strings.Contains(text, "y resume marked") &&
			strings.Contains(text, "d dismiss") && !strings.Contains(text, "reply to each") &&
			!strings.Contains(text, "1 allow") && !strings.Contains(text, "3 reject")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: x did not mark two Resume rows, or the footer offers keys that do nothing for them: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-marked")
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("resume the marked rows: %v", err)
	}
	left := waitRan(2, "y with two marks")
	stayed("3 needs you", "after y with two marks")
	// The agents come back in place: their hooks report them working, and
	// their rows show it in the open view, with no reopen.
	for name := range convs {
		if slices.Contains(left, name) {
			continue
		}
		mustCLI(t, base, "set-agent-state", "-s", name, "working", "--harness", harnessOf(name), "--agent-session-id", convs[name])
	}
	stayed("2 working", "after the resumed agents reported in")
	saveFrame(t, term, "resume-marked-done")

	// One alone: y on the row under the cursor resumes it in place. The
	// focus does not move to its pane.
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("resume the row under the cursor: %v", err)
	}
	// The row says Resuming until the offer closes. That can be too quick
	// to see, so the frame is kept when it shows and not required.
	if term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Resuming") }, time.Second) == nil {
		saveFrame(t, term, "resume-one-resuming")
	}
	waitRan(3, "y on one row")
	stayed("2 needs you", "after y on one row")
	saveFrame(t, term, "resume-one-done")

	// The rest: * marks every row, the two working agents too, and y
	// resumes the two offers.
	if err := term.SendKeys("*"); err != nil {
		t.Fatalf("mark every row: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "4 marked") && strings.Contains(text, "y resume marked")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: * did not mark every row: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-all-marked")
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("resume every row: %v", err)
	}
	waitRan(5, "* then y")
	// The Claude that y started got the two kept flags and nothing else: the
	// line is matched whole, so a one-shot flag or the prompt fails it.
	resumedArgv := "claude-argv: --resume " + convs[claudeSession] + " --permission-mode plan --model sonnet --dangerously-skip-permissions"
	if out := mustCLI(t, base, "capture-pane", "--unwrap", "-s", claudeSession); !slices.ContainsFunc(strings.Split(out, "\n"), func(l string) bool {
		return strings.TrimSpace(l) == resumedArgv
	}) {
		t.Fatalf("ASSERTION: the resumed Claude did not get exactly its kept flags, want %q:\n%s", resumedArgv, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "New agent") && !strings.Contains(text, "echo resumed-") && !strings.Contains(text, " marked ")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after * then y the view did not stay open with no offer left: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-all-done")

	deadline = time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "Nothing is waiting for you.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the offers stayed open after the resumes:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive(t, term, "after resuming from the Inbox")
}

// riskyBadge reports whether a screen shows a card titled Resume with the
// risky badge at the end of its title line.
func riskyBadge(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Resume   ") && strings.HasSuffix(line, " risky") {
			return true
		}
	}
	return false
}

// waitUnwrapped waits for a pane to show text, with the rows the pane wrapped
// joined, so a long command line is found whole.
func waitUnwrapped(t *testing.T, base, session, text string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "capture-pane", "--unwrap", "-s", session)
		if strings.Contains(out, text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed %q:\n%s", text, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestAutoResumeLeavesApprovalsOffToThePerson runs two fake Claude Code
// agents under daemon.resume_agents = "auto": one started with --model, one
// with --dangerously-skip-permissions too. After a restart the first comes
// back by itself. The second must not: it waits as a Resume row that says it
// resumes with approvals off, and nothing is typed into its pane. Its card
// names the flag in full and carries the risky badge.
//
// Negative control: with the risky check cut from applyResumeOffers
// (agent_resume.go), the second agent is resumed automatically and no Resume
// row is left. With keep_risky cut from the Claude Code manifest, no row
// names approvals off.
func TestAutoResumeLeavesApprovalsOffToThePerson(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeClaude := "#!/bin/sh\nprintf 'claude-argv:'; printf ' %s' \"$@\"; printf '\\n'\n" +
		"if [ \"$1\" = --resume ]; then exit 0; fi\nsleep 600\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	writeConfig(t, base, "[daemon]\nresume_agents = 'auto'\n")

	agents := []struct{ session, conv, flags string }{
		{"e2e-auto-safe", "a1b2-c3d4", "--model sonnet"},
		{"e2e-auto-risky", "e5f6-a7b8", "--model sonnet --dangerously-skip-permissions"},
	}
	for _, a := range agents {
		mustCLI(t, base, "new", a.session, "--detach")
		mustCLI(t, base, "set-agent-state", "-s", a.session, "working", "--harness", "claude-code", "--agent-session-id", a.conv)
		mustCLI(t, base, "send-text", "-s", a.session, "claude "+a.flags+"\n")
		waitUnwrapped(t, base, a.session, "claude-argv: "+a.flags)
		want := "claude --resume " + a.conv + " " + a.flags
		deadline := time.Now().Add(uiTimeout)
		for {
			out, _ := tuiosCLI(t, base, "resume-agent", "-s", a.session, "--dry-run")
			if strings.HasPrefix(strings.TrimSpace(out), want) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the flags of %s were not kept:\n%s", a.session, out)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	mustCLI(t, base, "restart-server", "--yes")

	// The safe agent comes back by itself.
	waitUnwrapped(t, base, agents[0].session, "claude-argv: --resume "+agents[0].conv+" --model sonnet")
	// The risky one waits for the person, and says why.
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "Resumes with approvals off (--dangerously-skip-permissions): claude --resume "+agents[1].conv) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: no Resume row names approvals off for the risky agent:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Past the point the safe one was typed, and well past the auto stagger:
	// nothing reached the risky pane.
	time.Sleep(2 * time.Second)
	if out := mustCLI(t, base, "capture-pane", "-s", agents[1].session); strings.Contains(out, "claude --resume") {
		t.Fatalf("ASSERTION: auto mode resumed an agent with approvals off:\n%s", out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", agents[1].session}})
	if err := term.WaitForText("can resume its", bootTimeout); err != nil {
		t.Fatalf("the dock did not announce the offer: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Resumes with approvals off (--dangerously-skip-permissions)") && riskyBadge(text)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Inbox row does not name approvals off: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-risky-row")
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
