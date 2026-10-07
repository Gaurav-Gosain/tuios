package tuie2e

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// OSC 7501, the Program Status Protocol, end to end: a plain script in a plain
// shell pane says what it is doing, and the rail, the Inbox and the daemon's
// agent state follow it. Nothing here is an agent CLI, which is the point of
// the protocol: any program can report.
//
// How this could pass wrongly, written down first:
//   - The state could come from the screen tier or the detector rather than
//     the report: every step checks get-agent-state's source is "program",
//     and a shell running a script named psdemo is no agent to either.
//   - The rail could show the message because it is on the pane's screen:
//     the script prints only its step markers, never the reported text.
//   - A record could be dropped by the wrong event. The shell here does not
//     mark its prompt, so the prompt rule is driven by a printf of OSC 133 A,
//     and before it a printf of a working report alone must leave the record
//     in place. The script's exit is what ends the root record, and the done
//     record beside it must survive that exit.
//   - A typed key could be what drops a done record: the commands here are
//     typed with send-text, which the daemon does not count as the person,
//     and the one step about typing types through the client.
//   - The hostile message could be shown inert because it was never stored:
//     the record must hold the text with the bidi override and the zero width
//     space taken out, and the report with an escape in it must change nothing.

// psState is what get-agent-state says about a pane.
type psState struct {
	State     string `json:"state"`
	Message   string `json:"message"`
	Source    string `json:"source"`
	BlockedBy string `json:"blocked_by"`
	Program   []struct {
		ID       string `json:"id"`
		State    string `json:"state"`
		Kind     string `json:"kind"`
		Progress int    `json:"progress"`
		App      string `json:"app"`
		Title    string `json:"title"`
		Msg      string `json:"msg"`
	} `json:"program_status"`
}

// records is the program_status list as "id=state" words, the root as "-".
func (p psState) records() string {
	var out []string
	for _, r := range p.Program {
		id := r.ID
		if id == "" {
			id = "-"
		}
		out = append(out, id+"="+r.State)
	}
	return strings.Join(out, " ")
}

func readPSState(t *testing.T, base, session, window string) psState {
	t.Helper()
	out, err := tuiosCLI(t, base, "get-agent-state", "-s", session, "-w", window, "--json")
	if err != nil {
		t.Fatalf("get-agent-state: %v\n%s", err, out)
	}
	var st psState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("get-agent-state json: %v\n%s", err, out)
	}
	return st
}

// waitPSState polls get-agent-state until ok holds, and fails with the last
// answer when it never does.
func waitPSState(t *testing.T, base, session, window, what string, ok func(psState) bool) psState {
	t.Helper()
	return waitPSStateWithin(t, base, session, window, what, shellTimeout, ok)
}

// waitPSStateWithin is waitPSState with its own deadline, for a step that
// must happen before something else could cause it.
func waitPSStateWithin(t *testing.T, base, session, window, what string, within time.Duration, ok func(psState) bool) psState {
	t.Helper()
	deadline := time.Now().Add(within)
	var st psState
	for {
		st = readPSState(t, base, session, window)
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: never held; last state %+v", what, st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// capturePS takes a full screen PNG into the artifacts as name. The shot
// directory is emptied first, because captureScreenTo copies the last file by
// name and capture file names do not sort by time.
func capturePS(t *testing.T, term *tuitest.Terminal, shots, artifacts, name string) {
	t.Helper()
	for _, f := range shotFiles(t, shots) {
		_ = os.Remove(f)
	}
	captureScreenTo(t, term, shots, artifacts, name)
}

func b64e(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// psDemo is the reporting script. It reports through printf and through
// tuios status, and waits for a file before each next step, so the test can
// look at every step in turn.
const psDemo = `#!/bin/sh
r() { printf '\033]7501;%s\033\\' "$1"; }
b() { printf '%s' "$1" | base64 | tr -d '\n'; }
step() { while [ ! -f "go$1" ]; do sleep 0.05; done; }
r "state=working:app=cargo:progress=40:msg=$(b 'Compiling 12 crates')"
echo PS-STEP-1
step 1
"$TUIOS_BIN" status blocked --kind permission --app cargo --msg 'Publish the crate?'
echo PS-STEP-2
step 2
r "state=working:app=cargo:progress=60"
r "state=working:id=tests:title=$(b Tests):msg=$(b 'Running 40 tests')"
r "state=blocked:kind=question:id=tests/unit:msg=$(b 'Which seed?')"
echo PS-STEP-3
step 3
r "state=clear:id=tests"
echo PS-STEP-4
step 4
r "state=working:app=cargo:msg=$(printf 'safe\342\200\256evil\342\200\213<b>x</b>' | base64 | tr -d '\n')"
r "state=error:app=cargo:msg=$(printf 'bad\033[31mred' | base64 | tr -d '\n')"
echo PS-STEP-5
step 5
r "state=done:id=report:app=cargo:msg=$(b 'Built 12 crates')"
r "state=working:app=cargo:msg=$(b 'Cleaning up')"
echo PS-STEP-6
`

// TestProgramStatusDrivesTheRailAndInbox runs psdemo in a shell pane and
// follows each of its reports to the agent state, the rail and the Inbox.
func TestProgramStatusDrivesTheRailAndInbox(t *testing.T) {
	const session, pane = "ps", "build"
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	host := buildHostTerm(t)
	shots := shotDir(t, base)
	writeConfig(t, base, "[appearance.sidebar]\nwidth = 44\n\n[screenshot]\ndirectory = \""+shots+"\"\nformat = \"png\"\n")
	work := workDirIn(t, base)
	if err := os.WriteFile(filepath.Join(work, "psdemo"), []byte(psDemo), 0o755); err != nil {
		t.Fatalf("write psdemo: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", session, "--name", pane); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	term := attachIn(t, base, session, startOpts{cols: 140, rows: 40, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	frames := artifactDir(t)
	sendText := func(text string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", pane, text); err != nil {
			t.Fatalf("send-text %q: %v\n%s", text, err, out)
		}
	}
	next := func(n string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, "go"+n), nil, 0o644); err != nil {
			t.Fatalf("release step %s: %v", n, err)
		}
	}
	rail := func(what string, markers ...string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, markers...) }, uiTimeout); err != nil {
			t.Fatalf("%s: the screen never showed %q: %v\n%s", what, markers, err, term.Snapshot())
		}
	}

	// The query: the pane's emulator answers with the one fixed reply.
	sendText(host + " query 7501\n")
	waitCapture(t, base, session, pane, "HOSTTERM-7501=?")

	sendText("TUIOS_BIN=" + tuiosBin + " ./psdemo\n")
	waitCapture(t, base, session, pane, "PS-STEP-1")

	// 1. Working, with progress. Any pane is an agent once it reports.
	st := waitPSState(t, base, session, pane, "working with progress", func(s psState) bool {
		return s.State == "working" && len(s.Program) == 1 && s.Program[0].Progress == 40
	})
	if st.Source != "program" || st.Message != "Compiling 12 crates" || st.Program[0].App != "cargo" {
		t.Errorf("ASSERTION: step 1 state = %+v, want source program, the message and app cargo", st)
	}
	rail("step 1", "cargo", "40%", "Compiling 12 crates")
	saveArtifact(t, term, frames, "1-working")
	capturePS(t, term, shots, frames, "1-working")

	// 2. Blocked on a permission, reported with tuios status.
	next("1")
	waitCapture(t, base, session, pane, "PS-STEP-2")
	st = waitPSState(t, base, session, pane, "blocked on a permission", func(s psState) bool { return s.State == "needs_input" })
	if st.BlockedBy != "approval" || st.Message != "Publish the crate?" || st.Source != "program" {
		t.Errorf("ASSERTION: step 2 state = %+v, want blocked_by approval and the message", st)
	}
	rail("step 2", "approval", "Publish the crate?")
	saveArtifact(t, term, frames, "2-blocked-rail")
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	rail("the Inbox", "Approvals", "Publish the crate?")
	saveArtifact(t, term, frames, "2-blocked-inbox")
	capturePS(t, term, shots, frames, "2-blocked-inbox")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Approvals 1") }, uiTimeout); err != nil {
		t.Logf("the Inbox did not close: %v", err)
	}

	// 3. Child records: the blocked grandchild is the most urgent, and it
	// takes its app from the root.
	next("2")
	waitCapture(t, base, session, pane, "PS-STEP-3")
	st = waitPSState(t, base, session, pane, "child records", func(s psState) bool { return len(s.Program) == 3 })
	if got := st.records(); got != "-=working tests=working tests/unit=blocked" {
		t.Errorf("ASSERTION: step 3 records = %s", got)
	}
	if st.State != "needs_input" || st.BlockedBy != "question" || st.Message != "Which seed?" {
		t.Errorf("ASSERTION: step 3 summary = %+v, want the blocked child, a question", st)
	}
	for _, r := range st.Program {
		if r.App != "cargo" {
			t.Errorf("ASSERTION: record %q has app %q, want cargo from the root", r.ID, r.App)
		}
	}
	rail("step 3", "question", "Which seed?")
	saveArtifact(t, term, frames, "3-children")

	// 4. clear removes a record and everything beneath it.
	next("3")
	waitCapture(t, base, session, pane, "PS-STEP-4")
	st = waitPSState(t, base, session, pane, "clear", func(s psState) bool { return len(s.Program) == 1 })
	if got := st.records(); got != "-=working" || st.State != "working" {
		t.Errorf("ASSERTION: step 4 records = %s, state %s", got, st.State)
	}

	// 5. A hostile message is shown inert, and a report with an escape in it
	// is discarded whole.
	next("4")
	waitCapture(t, base, session, pane, "PS-STEP-5")
	st = waitPSState(t, base, session, pane, "hostile message", func(s psState) bool {
		return len(s.Program) == 1 && strings.HasPrefix(s.Program[0].Msg, "safe")
	})
	if st.Program[0].Msg != "safeevil<b>x</b>" || st.State != "working" {
		t.Errorf("ASSERTION: step 5 record = %+v, state %s: want the text without the override and the zero width space, still working", st.Program[0], st.State)
	}
	rail("step 5", "safeevil<b>x</b>")
	if text := term.Screen().Text(); strings.ContainsAny(text, "\u202e\u200b") || strings.Contains(text, "bad") {
		t.Errorf("ASSERTION: the screen shows a format character or the discarded report\n%s", term.Snapshot())
	}
	saveArtifact(t, term, frames, "5-hostile")

	// 6. The script ends: its working root goes with it, and the done record
	// stays, finished and unread. Another pane takes the focus first, since a
	// finished turn in the focused pane is seen at once.
	if out, err := tuiosCLI(t, base, "new-window", "other", "-s", session); err != nil {
		t.Fatalf("open another pane: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, uiTimeout); err != nil {
		t.Fatalf("the second pane never showed: %v\n%s", err, term.Snapshot())
	}
	next("5")
	waitCapture(t, base, session, pane, "PS-STEP-6")
	st = waitPSState(t, base, session, pane, "the script exited", func(s psState) bool { return s.records() == "report=done" })
	if st.State != "done" || st.Message != "Built 12 crates" {
		t.Errorf("ASSERTION: after the script exited the state is %+v, want done", st)
	}
	rail("step 6", "Built 12 crates")
	// The dock, on the top row, says the unfocused pane finished, with its
	// message.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		top, _, _ := strings.Cut(s.Text(), "\n")
		return strings.Contains(top, pane) && strings.Contains(top, "Built 12 crates")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: the dock never said the pane finished: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, frames, "6-done")
	capturePS(t, term, shots, frames, "6-done")
	time.Sleep(3 * time.Second)
	if st = readPSState(t, base, session, pane); st.records() != "report=done" {
		t.Errorf("ASSERTION: the done record did not survive: %s", st.records())
	}

	// 7. The prompt mark ends working, blocked and idle records while the
	// program that reported them still runs, so the exit is not what ended
	// them, and a done record reported with them survives it. Before the
	// mark, the working record stays while the program runs.
	sendText(`sh -c 'r() { printf "\033]7501;%s\033\134" "$1"; }; r state=working:id=w; echo W""-ON; sleep 4; ` +
		`r state=blocked:id=b; r state=idle:id=i; r state=done:id=d2; printf "\033]133;A\007"; echo A""-SENT; sleep 8'` + "\n")
	waitCapture(t, base, session, pane, "W-ON")
	time.Sleep(2500 * time.Millisecond)
	if st = readPSState(t, base, session, pane); st.records() != "report=done w=working" {
		t.Fatalf("ASSERTION: while the program runs its working record should stay: %s", st.records())
	}
	waitCapture(t, base, session, pane, "A-SENT")
	// Within 3 seconds: the program sleeps 8 more, so its exit cannot be
	// what ends the records.
	st = waitPSStateWithin(t, base, session, pane, "the prompt mark", 3*time.Second, func(s psState) bool { return !strings.Contains(s.records(), "w=working") })
	if got := st.records(); got != "d2=done report=done" {
		t.Errorf("ASSERTION: after OSC 133 A the records are %s, want only the done ones", got)
	}
	time.Sleep(9 * time.Second) // the program exits
	if st = readPSState(t, base, session, pane); st.records() != "d2=done report=done" {
		t.Errorf("ASSERTION: the done records did not survive the exit: %s", st.records())
	}

	// 8. The person typing in the pane ends the done records.
	if out, err := tuiosCLI(t, base, "focus-window", pane, "-s", session); err != nil {
		t.Fatalf("focus the pane: %v\n%s", err, out)
	}
	enterTerminalMode(t, term)
	if err := term.SendKeys("true", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	st = waitPSState(t, base, session, pane, "typing", func(s psState) bool { return len(s.Program) == 0 })
	if st.State != "none" {
		t.Errorf("ASSERTION: with no record left the state is %q, want none", st.State)
	}
	alive(t, term, "after the program status run")
}

// TestProgramStatusQueryStandalone asks the feature detection question in a
// pane of tuios without a daemon, where the client's own emulator answers.
// The marker is printed by hostterm from the answer it read, and the typed
// command does not hold it.
func TestProgramStatusQueryStandalone(t *testing.T) {
	host := buildHostTerm(t)
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, host+" query 7501", "HOSTTERM-7501=?", shellTimeout)
}

// TestProgramStatusReachesTheHostTerminal attaches from a stand-in host
// terminal that supports OSC 7501, and reads what tuios reports to it.
//
// How this could pass wrongly: the report could be the pane's own sequence
// passed through. The pane reports id=evil; the host must see only tuios's
// own record for the pane, under <session>/<pane>, with the pane's app. The
// positive and negative halves share one fixture: a host that does not
// answer the query, and a config with the reports off, get nothing.
func TestProgramStatusReachesTheHostTerminal(t *testing.T) {
	host := buildHostTerm(t)
	for _, tc := range []struct {
		name    string
		answer  bool
		config  string
		reports bool
		queried bool
	}{
		{"supported", true, "", true, true},
		{"host does not answer", false, "", false, true},
		{"off in the config", true, "[agents]\nhost_program_status = \"off\"\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			if tc.config != "" {
				writeConfig(t, base, tc.config)
			}
			hostLog := filepath.Join(t.TempDir(), "host.log")
			const session = "fwd"
			if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
				t.Fatalf("create the session: %v\n%s", err, out)
			}
			if out, err := tuiosCLI(t, base, "set-window", "-s", session, "--name", "deploy"); err != nil {
				t.Fatalf("name the pane: %v\n%s", err, out)
			}
			wrap := []string{host, "run", "-log", hostLog}
			if tc.answer {
				wrap = append(wrap, "-program-status")
			}
			term := startIn(t, base, startOpts{args: []string{"attach", session}, wrap: append(wrap, "--")})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			hostLines := func() string {
				data, _ := os.ReadFile(hostLog)
				return string(data)
			}
			waitHost := func(what string, re *regexp.Regexp) string {
				t.Helper()
				deadline := time.Now().Add(uiTimeout)
				for {
					if m := re.FindString(hostLines()); m != "" {
						return m
					}
					if time.Now().After(deadline) {
						t.Fatalf("%s: the host never got %s:\n%s", what, re, hostLines())
					}
					time.Sleep(100 * time.Millisecond)
				}
			}

			if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "-w", "deploy", "needs_input",
				"--kind", "approval", "--message", "Approve deploy?"); err != nil {
				t.Fatalf("set-agent-state: %v\n%s", err, out)
			}
			// The pane's own report, which must not reach the host as it is.
			paneReport := func() {
				t.Helper()
				if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "deploy",
					`printf '\033]7501;state=working:id=evil:app=rawapp\033\\'`+"\n"); err != nil {
					t.Fatalf("send-text: %v\n%s", err, out)
				}
			}
			if !tc.reports {
				paneReport()
				time.Sleep(3 * time.Second)
				log := hostLines()
				if got := strings.Contains(log, "7501 ?"); got != tc.queried {
					t.Errorf("ASSERTION: the host was asked: %v, want %v\n%s", got, tc.queried, log)
				}
				if strings.Contains(log, "state=") {
					t.Errorf("ASSERTION: tuios reported to a host it must not report to:\n%s", log)
				}
				return
			}
			waitHost("the query", regexp.MustCompile(`7501 \?`))
			blocked := waitHost("the blocked pane", regexp.MustCompile(`7501 state=blocked:id=fwd/[A-Za-z0-9_.+-]+:kind=permission[^\n]*`))
			if !strings.Contains(blocked, "msg="+b64e("Approve deploy?")) || !strings.Contains(blocked, "title="+b64e("deploy")) {
				t.Errorf("ASSERTION: the blocked report lacks the message or the pane's name: %s", blocked)
			}
			id := regexp.MustCompile(`id=(fwd/[A-Za-z0-9_.+-]+)`).FindStringSubmatch(blocked)[1]
			// The pane's own working report becomes tuios's record for the
			// pane, with the app the pane named, once the hook's state is
			// gone: a hook's report outranks the program's.
			if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "-w", "deploy", "none"); err != nil {
				t.Fatalf("set-agent-state none: %v\n%s", err, out)
			}
			paneReport()
			waitHost("the pane's own report", regexp.MustCompile(`7501 state=working:id=`+regexp.QuoteMeta(id)+`:app=rawapp`))
			if strings.Contains(hostLines(), "id=evil") {
				t.Errorf("ASSERTION: the pane's raw report reached the host:\n%s", hostLines())
			}
			// And a pane with nothing to say is cleared on the host.
			if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "deploy",
				`printf '\033]7501;state=clear\033\\'`+"\n"); err != nil {
				t.Fatalf("send-text: %v\n%s", err, out)
			}
			waitHost("the clear", regexp.MustCompile(`7501 state=clear:id=`+regexp.QuoteMeta(id)))
			saveArtifact(t, term, artifactDir(t), "host-forwarding")

			// A client that detaches clears what it left on the host, before
			// the reset that would also remove it.
			if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "-w", "deploy", "working"); err != nil {
				t.Fatalf("set-agent-state: %v\n%s", err, out)
			}
			waitHost("the second report", regexp.MustCompile(`7501 state=working:id=`+regexp.QuoteMeta(id)+`:title=`))
			clears := func() int { return strings.Count(hostLines(), "7501 state=clear:id="+id) }
			before := clears()
			if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
				t.Fatalf("detach: %v", err)
			}
			deadline := time.Now().Add(uiTimeout)
			for clears() <= before {
				if time.Now().After(deadline) {
					t.Fatalf("ASSERTION: the detached client left its record on the host:\n%s", hostLines())
				}
				time.Sleep(100 * time.Millisecond)
			}
			if err := os.WriteFile(filepath.Join(artifactDir(t), "host.log"), []byte(hostLines()), 0o644); err != nil {
				t.Logf("save the host log: %v", err)
			}
		})
	}
}
