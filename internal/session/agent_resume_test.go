package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// echoHarness installs a user manifest whose resume command is an echo, so a
// test can see the command land in a real shell without a real agent. It has
// to be in place before the daemon loads its registry.
func echoHarness(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
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
	t.Setenv("TUIOS_HARNESS_DIR", dir)
}

// savedAgentSession is the state a previous daemon wrote for a session whose
// panes ran agents: one resumable pane with its agent running, one whose
// harness has no resume command, one that ran on another machine, one with no
// conversation, and one whose agent had already exited, which keeps its id and
// has nothing live to resume.
func savedAgentSession(name string) *SessionState {
	return &SessionState{
		Name:             name,
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		Windows: []WindowState{
			{ID: "win-agent", Title: "agent", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-1",
				AgentState: AgentStateWorking, AgentHarness: "echoer",
				AgentSessionID: "5f1c-9a3d", AgentSessionHarness: "echoer"},
			{ID: "win-aider", Title: "aider", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-2",
				AgentState: AgentStateIdle, AgentHarness: "aider",
				AgentSessionID: "a1", AgentSessionHarness: "aider"},
			{ID: "win-remote", Title: "remote", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-3",
				Host: "build", AgentState: AgentStateWorking, AgentHarness: "echoer",
				AgentSessionID: "r1", AgentSessionHarness: "echoer"},
			{ID: "win-plain", Title: "plain", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-4"},
			{ID: "win-ended", Title: "ended", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-5",
				AgentSessionID: "e1-ended", AgentSessionHarness: "echoer"},
		},
	}
}

// capturePane reads what a pane shows.
func capturePane(t *testing.T, c *verbConn, session, window string) string {
	t.Helper()
	res := result(t, c.call(t, `{"id":1,"verb":"capture-pane","params":{"session":"`+session+`","window":"`+window+`"}}`))
	content, _ := res["content"].(string)
	return content
}

// waitPaneHolds polls a pane until it shows want.
func waitPaneHolds(t *testing.T, c *verbConn, session, window, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second * testDeadlineScale)
	for {
		text := capturePane(t, c, session, window)
		if strings.Contains(text, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never showed %q; it shows:\n%s", window, want, text)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestRestoreKeepsTheConversationID is the gap under the whole feature: the
// state file kept each pane's agent_session_id, and the restore dropped it,
// because the id is daemon-owned and the restore's state push took daemon
// fields from the empty session it had just created. A window from another
// machine loses it on purpose.
func TestRestoreKeepsTheConversationID(t *testing.T) {
	echoHarness(t)
	d, _ := startTestDaemon(t)
	d.resumeAgents = resumeModeOff

	sess, err := d.restoreSession(savedAgentSession("keep"))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := map[string]WindowState{}
	for _, w := range sess.GetState().Windows {
		got[w.ID] = w
	}
	if w := got["win-agent"]; w.AgentSessionID != "5f1c-9a3d" || w.AgentSessionHarness != "echoer" {
		t.Errorf("the restored pane lost its conversation: id %q harness %q", w.AgentSessionID, w.AgentSessionHarness)
	}
	if w := got["win-aider"]; w.AgentSessionID != "a1" {
		t.Errorf("a pane whose harness cannot resume lost its id %q; it is still worth keeping", w.AgentSessionID)
	}
	if w := got["win-remote"]; w.AgentSessionID != "" || w.AgentSessionHarness != "" {
		t.Errorf("a pane that ran on another machine kept that machine's conversation %q", w.AgentSessionID)
	}
	if w := got["win-ended"]; w.AgentSessionID != "e1-ended" {
		t.Errorf("a pane whose agent had exited lost its id %q; resume-agent still needs it", w.AgentSessionID)
	}
}

// TestRestoreOffersOnlyWhatWasRunning is the pane where the person quit the
// agent and went back to shell work before the restart. Its id is kept, so it
// can still be resumed by hand, but the restore does not offer it: no Resume
// row in ask mode and nothing typed in auto mode.
func TestRestoreOffersOnlyWhatWasRunning(t *testing.T) {
	echoHarness(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	if _, err := d.restoreSession(savedAgentSession("ended-ask")); err != nil {
		t.Fatalf("restore: %v", err)
	}
	waitAttention(t, c, "the running pane's offer", hasKind(AttentionResume, "win-agent"))
	if hasKind(AttentionResume, "win-ended")(mustList(t, c)) {
		t.Error("ask mode offered a conversation whose agent had already exited")
	}

	d.resumeAgents = resumeModeAuto
	if _, err := d.restoreSession(savedAgentSession("ended-auto")); err != nil {
		t.Fatalf("restore: %v", err)
	}
	waitPaneHolds(t, c, "ended-auto", "win-agent", "resumed-5f1c-9a3d")
	// The running pane's command landed, and the ended pane's would have been
	// typed 100 ms later at most; give it well past that.
	time.Sleep(time.Second)
	if text := capturePane(t, c, "ended-auto", "win-ended"); strings.Contains(text, "resumed-") {
		t.Errorf("auto mode typed into a pane whose agent had exited:\n%s", text)
	}

	res := result(t, c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"ended-auto","window":"win-ended","dry_run":true}}`))
	if res["command"] != "echo resumed-e1-ended" {
		t.Errorf("resume-agent by hand on the ended pane answered %v", res)
	}
}

// TestRestoreOffersOnce is a Resume row nobody answered: the restored pane
// holds a new shell and no agent, so the state the next save writes makes no
// offer, and a dismissed row does not come back on every later restart.
func TestRestoreOffersOnce(t *testing.T) {
	echoHarness(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	sess, err := d.restoreSession(savedAgentSession("once"))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	waitAttention(t, c, "the first restore's offer", hasKind(AttentionResume, "win-agent"))

	saved := sess.ResurrectionState()
	w, ok := findWindowState(saved, "win-agent")
	if !ok {
		t.Fatal("the next save lost the pane")
	}
	if w.AgentSessionID != "5f1c-9a3d" {
		t.Errorf("the next save lost the conversation id: %q", w.AgentSessionID)
	}
	if agentWasLive(w) {
		t.Fatalf("the next save says an agent runs in the new shell: state %q harness %q", w.AgentState, w.AgentHarness)
	}
	if o, ok := d.resumeOfferFor("once", w); ok {
		t.Errorf("a second restart would offer %v again", o)
	}
}

// mustList is every open Inbox item.
func mustList(t *testing.T, c *verbConn) []map[string]any {
	t.Helper()
	items, _ := listAttention(t, c, "")
	return items
}

// TestRestoreAsksToResumeInTheInbox is the default mode: a resume item per
// resumable pane, naming the exact command, and nothing typed.
func TestRestoreAsksToResumeInTheInbox(t *testing.T) {
	echoHarness(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)

	if _, err := d.restoreSession(savedAgentSession("ask")); err != nil {
		t.Fatalf("restore: %v", err)
	}
	items := waitAttention(t, c, "the restore's offer", hasKind(AttentionResume, "win-agent"))
	if len(items) != 1 {
		t.Fatalf("the Inbox holds %v, want one resume item: the aider, remote and plain panes have nothing to resume", items)
	}
	it := items[0]
	if it["summary"] != "echo resumed-5f1c-9a3d" || it["harness"] != "echoer" || it["session"] != "ask" {
		t.Errorf("the resume item is %v", it)
	}
	// Ask types nothing.
	time.Sleep(300 * time.Millisecond)
	if text := capturePane(t, c, "ask", "win-agent"); strings.Contains(text, "resumed-") {
		t.Errorf("ask mode typed the command:\n%s", text)
	}

	// y in the Inbox is resume-agent: it types the command and the item goes.
	res := result(t, c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"ask","window":"win-agent"}}`))
	if res["command"] != "echo resumed-5f1c-9a3d" || res["typed"] != true || res["agent_session_id"] != "5f1c-9a3d" {
		t.Errorf("resume-agent answered %v", res)
	}
	waitPaneHolds(t, c, "ask", "win-agent", "resumed-5f1c-9a3d")
	waitAttention(t, c, "resumed", isEmpty)
}

// TestRestoreResumesAutomatically is auto: the command is typed into the
// restored shell with no item opened.
func TestRestoreResumesAutomatically(t *testing.T) {
	echoHarness(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	d.resumeAgents = resumeModeAuto

	if _, err := d.restoreSession(savedAgentSession("auto")); err != nil {
		t.Fatalf("restore: %v", err)
	}
	waitPaneHolds(t, c, "auto", "win-agent", "resumed-5f1c-9a3d")
	if items, _ := listAttention(t, c, ""); len(items) != 0 {
		t.Errorf("auto mode opened %v", items)
	}
	for _, w := range []string{"win-aider", "win-plain"} {
		if text := capturePane(t, c, "auto", w); strings.Contains(text, "resumed-") || strings.Contains(text, "aider") && strings.Contains(text, "--") {
			t.Errorf("auto typed into %s, which has nothing to resume:\n%s", w, text)
		}
	}
}

// TestRestoreResumeOff opens nothing and types nothing, and keeps the id so a
// resume by hand still works.
func TestRestoreResumeOff(t *testing.T) {
	echoHarness(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	d.resumeAgents = resumeModeOff

	if _, err := d.restoreSession(savedAgentSession("off")); err != nil {
		t.Fatalf("restore: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if items, _ := listAttention(t, c, ""); len(items) != 0 {
		t.Errorf("off opened %v", items)
	}
	res := result(t, c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"off","window":"win-agent","dry_run":true}}`))
	if res["command"] != "echo resumed-5f1c-9a3d" || res["typed"] != false {
		t.Errorf("dry_run answered %v", res)
	}
}

// TestResumeAgentRefusesWhatItCannotDo covers each refusal: nothing
// recorded, a harness with no resume command, an id a shell would read as
// more than one argument, and a pane running a program.
func TestResumeAgentRefusesWhatItCannotDo(t *testing.T) {
	echoHarness(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	d.resumeAgents = resumeModeOff
	if _, err := d.restoreSession(savedAgentSession("refuse")); err != nil {
		t.Fatalf("restore: %v", err)
	}

	for _, tc := range []struct{ window, code string }{
		{"win-plain", ErrVerbNotResumable},
		{"win-aider", ErrVerbNotResumable},
		{"win-remote", ErrVerbNotResumable},
	} {
		resp := c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"refuse","window":"`+tc.window+`"}}`)
		if code := errCode(t, resp); code != tc.code {
			t.Errorf("%s: code %q, want %q", tc.window, code, tc.code)
		}
	}

	// An id reported by a pane that a shell would split is never typed.
	sess := d.manager.GetSession("refuse")
	result(t, c.call(t, `{"id":1,"verb":"set-agent-session","params":{"session":"refuse","window":"win-plain","harness":"echoer","agent_session_id":"x; touch /tmp/pwned"}}`))
	resp := c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"refuse","window":"win-plain"}}`)
	if code := errCode(t, resp); code != ErrVerbNotResumable {
		t.Errorf("an unsafe id: code %q, want %q", code, ErrVerbNotResumable)
	}

	// A pane running a program does not get the command typed into it.
	pty, err := d.resolvePTYForTarget(sess, "win-agent")
	if err != nil {
		t.Fatal(err)
	}
	if !waitShellPrompt(t.Context(), pty, 5*time.Second, 200*time.Millisecond) {
		t.Fatal("the restored shell never drew its prompt")
	}
	if _, err := pty.Write([]byte("sleep 30\r")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if pgid, ok := readForegroundPGID(pty.ShellPID()); !ok || pgid != pty.ShellPID() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sleep never took the foreground")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := readForegroundPGID(pty.ShellPID()); !ok {
		t.Skip("this platform does not say which process group holds a terminal")
	}
	resp = c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"refuse","window":"win-agent"}}`)
	if code := errCode(t, resp); code != ErrVerbNotReady {
		t.Errorf("a pane running sleep: code %q, want %q", code, ErrVerbNotReady)
	}
	if text := capturePane(t, c, "refuse", "win-agent"); strings.Contains(text, "resumed-") {
		t.Errorf("the command was typed into a running program:\n%s", text)
	}
}

// TestAutoResumeFallsBackToAsking holds the promise auto makes: a shell that
// never reaches its prompt gets the question instead of the command.
func TestAutoResumeFallsBackToAsking(t *testing.T) {
	echoHarness(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	d.resumeAgents = resumeModeOff
	if _, err := d.restoreSession(savedAgentSession("fallback")); err != nil {
		t.Fatalf("restore: %v", err)
	}
	sess := d.manager.GetSession("fallback")
	pty, err := d.resolvePTYForTarget(sess, "win-agent")
	if err != nil {
		t.Fatal(err)
	}
	if !waitShellPrompt(t.Context(), pty, 5*time.Second, 200*time.Millisecond) {
		t.Fatal("the restored shell never drew its prompt")
	}
	if _, err := pty.Write([]byte("sleep 30\r")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		pgid, ok := readForegroundPGID(pty.ShellPID())
		if !ok {
			t.Skip("this platform does not say which process group holds a terminal")
		}
		if pgid != pty.ShellPID() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sleep never took the foreground")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The offer is made from the window as it was saved, agent running.
	saved, _ := findWindowState(savedAgentSession("fallback"), "win-agent")
	o, ok := d.resumeOfferFor("fallback", saved)
	if !ok {
		t.Fatal("no offer for the resumable pane")
	}
	d.autoResume(o, 0)
	items := waitAttention(t, c, "the fallback", hasKind(AttentionResume, "win-agent"))
	if len(items) != 1 {
		t.Errorf("the Inbox holds %v", items)
	}
}

// TestResumeItemClosesWhenAnAgentWorks: a resume typed by hand, or a new
// agent, puts the pane to work, and the offer is spent.
func TestResumeItemClosesWhenAnAgentWorks(t *testing.T) {
	a, _ := recordingAttention()
	a.openResume(AttentionItem{Session: "work", Window: "w1", Summary: "claude --resume 5f1c"})
	a.openResume(AttentionItem{Session: "work", Window: "w2", Summary: "claude --resume 7a"})

	a.noteSessionEvent("work", agentEvent("w1", "none", "idle", "", "", 0, 0))
	if len(openItems(t, a)) != 2 {
		t.Fatalf("idle closed a resume offer: %v", openItems(t, a))
	}
	a.noteSessionEvent("work", agentEvent("w1", "idle", "working", "", "", 0, 0))
	a.noteSessionEvent("work", SessionEvent{Type: EventWindowClosed, Window: "w2"})
	if items := openItems(t, a); len(items) != 0 {
		t.Errorf("working and a closed window left %v open", items)
	}
}

// TestSavedResumeItemsAreNotLoaded: the restore opens resume items from the
// windows it brings back, so a saved one is dropped rather than doubled.
func TestSavedResumeItemsAreNotLoaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "items.json")
	a, _ := recordingAttention()
	a.path = path
	a.openResume(AttentionItem{Session: "work", Window: "w1", Summary: "claude --resume 5f1c"})
	a.saveNowAndFreeze()

	b, _ := recordingAttention()
	b.load(path, func(string, string) bool { return true })
	if items := openItems(t, b); len(items) != 0 {
		t.Errorf("a saved resume item came back: %v", items)
	}
}

// TestConversationHarnessIsRecorded checks both report paths record which
// harness the id belongs to, and that a client sync keeps it.
func TestConversationHarnessIsRecorded(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "rec")
	c := dialVerb(t, sp)

	result(t, c.call(t, `{"id":1,"verb":"set-agent-session","params":{"session":"rec","window":"`+a+`","harness":"qwen","agent_session_id":"q1"}}`))
	result(t, c.call(t, `{"id":1,"verb":"set-agent-state","params":{"session":"rec","window":"`+b+`","state":"working","harness":"claude-code","agent_session_id":"c1"}}`))

	check := func(when string) {
		t.Helper()
		st := sess.GetState()
		wa, _ := findWindowState(st, a)
		wb, _ := findWindowState(st, b)
		if wa.AgentSessionID != "q1" || wa.AgentSessionHarness != "qwen" {
			t.Errorf("%s: set-agent-session stored %q for %q", when, wa.AgentSessionID, wa.AgentSessionHarness)
		}
		if wb.AgentSessionID != "c1" || wb.AgentSessionHarness != "claude-code" {
			t.Errorf("%s: set-agent-state stored %q for %q", when, wb.AgentSessionID, wb.AgentSessionHarness)
		}
	}
	check("after the reports")

	// A client sync carries no daemon-owned field, and must not clear it.
	push := sess.GetState()
	for i := range push.Windows {
		push.Windows[i].AgentSessionID = ""
		push.Windows[i].AgentSessionHarness = ""
	}
	sess.UpdateState(push)
	check("after a client sync")
}

// TestResolveResumeMode: anything but auto and off asks.
func TestResolveResumeMode(t *testing.T) {
	for in, want := range map[string]string{"": resumeModeAsk, "ask": resumeModeAsk, "AUTO": resumeModeAuto, " off ": resumeModeOff, "yes": resumeModeAsk} {
		if got := resolveResumeMode(in); got != want {
			t.Errorf("resolveResumeMode(%q) = %q, want %q", in, got, want)
		}
	}
}
