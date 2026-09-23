package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Reports from a pane on another machine (P18), proved with two real daemons
// on the real proxy and a real process in the pane: the process is this test
// binary, started as the pane's command, calling the far daemon's socket the
// way the tuios CLI does.

// hostedHelperCall is one call the helper process makes. $PANE in params is
// replaced by TUIOS_PANE_ID. expect, when set, is a string the answer has to
// hold for the helper to print HAS.
type hostedHelperCall struct {
	Verb   string          `json:"verb"`
	Params json.RawMessage `json:"params"`
	Expect string          `json:"expect,omitempty"`
}

// TestHostedPaneHelperProcess is not a test. It is the process in the pane
// when TUIOS_HOSTED_HELPER is set, and does nothing otherwise.
//
// It prints one short token per call, R<n>:OK, R<n>:HAS or R<n>:ERR:<code>,
// because the test reads it off an 80 column screen.
func TestHostedPaneHelperProcess(t *testing.T) {
	if os.Getenv("TUIOS_HOSTED_HELPER") != "1" {
		return
	}
	var calls []hostedHelperCall
	_ = json.Unmarshal([]byte(os.Getenv("TUIOS_HOSTED_HELPER_CALLS")), &calls)
	pane := os.Getenv("TUIOS_PANE_ID")
	fmt.Printf("PANE:%t ", pane != "")
	conn, err := net.DialTimeout("unix", os.Getenv("TUIOS_HOSTED_HELPER_SOCKET"), 5*time.Second)
	if err != nil {
		fmt.Printf("DIAL:%v\n", err)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	br := bufio.NewReader(conn)
	for i, c := range calls {
		params := strings.ReplaceAll(string(c.Params), "$PANE", pane)
		req, _ := json.Marshal(verbRequest{ID: json.RawMessage(fmt.Sprint(i + 1)), Verb: c.Verb, Params: json.RawMessage(params)})
		_, _ = conn.Write(append(req, '\n'))
		line, err := br.ReadBytes('\n')
		if err != nil {
			fmt.Printf("R%d:EOF ", i)
			break
		}
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  *verbError      `json:"error"`
		}
		_ = json.Unmarshal(line, &resp)
		switch {
		case resp.Error != nil:
			fmt.Printf("R%d:ERR:%s ", i, resp.Error.Code)
		case c.Expect != "" && strings.Contains(string(resp.Result), c.Expect):
			fmt.Printf("R%d:HAS ", i)
		default:
			fmt.Printf("R%d:OK ", i)
		}
	}
	fmt.Println("DONE")
	time.Sleep(time.Minute)
	os.Exit(0)
}

// hostedHelperWindow opens a window on build in a hub session whose process is
// the helper making calls.
func hostedHelperWindow(t *testing.T, hub *Daemon, far *farSide, sessionName string, calls []hostedHelperCall) (*Session, WindowState, *PTY) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the caller's pid is not given on this platform, so nothing is forwarded")
	}
	sess, err := hub.manager.CreateSession(sessionName, &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	raw, _ := json.Marshal(calls)
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Host: "build",
		Command: []string{"/usr/bin/env",
			"TUIOS_HOSTED_HELPER=1",
			"TUIOS_HOSTED_HELPER_SOCKET=" + far.socket,
			"TUIOS_HOSTED_HELPER_CALLS=" + string(raw),
			os.Args[0], "-test.run=^TestHostedPaneHelperProcess$",
		},
	}, func(string) {})
	if err != nil {
		t.Fatalf("open a window on build: %v", err)
	}
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("the window has no pane")
	}
	var state WindowState
	for _, w := range sess.GetState().Windows {
		if w.ID == win.ID {
			state = w
		}
	}
	return sess, state, pty
}

// TestAnAgentInAPaneOnAnotherMachineReportsItsState is P18's point: a hook in
// a hosted pane reports with the ordinary verb, and the window on the owning
// machine takes the state, which puts the approval in that machine's Inbox.
//
// Negative control: without the forward in dispatchVerbLine the far daemon
// answers the call itself, with R0:ERR:session_not_found, and the window
// stays where it was.
func TestAnAgentInAPaneOnAnotherMachineReportsItsState(t *testing.T) {
	hub, far := startHubAndFar(t)
	waitForHostUp(t, hub, "build")
	sess, win, pty := hostedHelperWindow(t, hub, far, "global", []hostedHelperCall{{
		Verb:   "set-agent-state",
		Params: json.RawMessage(`{"window":"$PANE","state":"needs_input","kind":"approval","message":"approve Bash: make deploy"}`),
	}})
	text := waitForPaneText(t, pty, "DONE", paneBudget)
	if !strings.Contains(text, "PANE:true") || !strings.Contains(text, "R0:OK") {
		t.Fatalf("the pane's report did not go through: %q", text)
	}
	for _, w := range sess.GetState().Windows {
		if w.ID == win.ID && w.AgentState.Name() != "needs_input" {
			t.Fatalf("the window's state is %q, want needs_input", w.AgentState.Name())
		}
	}
	c := hubVerb(t)
	items, _ := listAttention(t, c, `{"host":"local"}`)
	if len(items) != 1 || items[0]["kind"] != AttentionApproval || items[0]["window"] != win.ID {
		t.Fatalf("the hub's Inbox holds %v, want the approval of the hosted pane", items)
	}
}

// TestAPaneOnAnotherMachineReadsAndSendsItsOwnMail covers the mail half, and
// the limits on it: the pane reads its own inbox whatever it names, sends only
// as itself, and never as the person.
func TestAPaneOnAnotherMachineReadsAndSendsItsOwnMail(t *testing.T) {
	hub, far := startHubAndFar(t)
	waitForHostUp(t, hub, "build")
	// The pane's inbox has to hold the message before the helper reads it,
	// and the window id is not known until the window exists. So the helper
	// waits for mail first.
	sess, win, pty := hostedHelperWindow(t, hub, far, "global", []hostedHelperCall{
		{Verb: "wait-for", Params: json.RawMessage(`{"condition":"agent-message","window":"$PANE","timeout":20000}`)},
		{Verb: "read-agent-messages", Params: json.RawMessage(`{"to":"$PANE"}`), Expect: "please rebase"},
		{Verb: "send-agent-message", Params: json.RawMessage(`{"from":"$PANE","to":"human","text":"rebased"}`)},
		{Verb: "send-agent-message", Params: json.RawMessage(`{"from":"human","to":"$PANE","text":"forged"}`)},
		{Verb: "wait-for", Params: json.RawMessage(`{"condition":"window-exit","window":"$PANE","timeout":100}`)},
	})
	c := hubVerb(t)
	raw, _ := json.Marshal(map[string]any{"session": sess.Name, "to": win.ID, "text": "please rebase"})
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"send-agent-message","params":%s}`, raw)))

	text := waitForPaneText(t, pty, "DONE", paneBudget)
	for _, want := range []string{"R0:OK", "R1:HAS", "R2:OK", "R4:ERR:forbidden"} {
		if !strings.Contains(text, want) {
			t.Errorf("the pane printed %q, want %s", text, want)
		}
	}
	// A message from human names no pane, so it is not forwarded at all: it
	// is answered on the machine the process is on, where it fails.
	if strings.Contains(text, "R3:OK") {
		t.Errorf("a message the pane sent as human went through: %q", text)
	}

	res := result(t, c.call(t, fmt.Sprintf(`{"id":2,"verb":"read-agent-messages","params":{"session":%q,"to":"human","peek":true}}`, sess.Name)))
	msgs, _ := res["messages"].([]any)
	found := false
	for _, m := range msgs {
		msg := m.(map[string]any)
		if msg["text"] == "rebased" {
			found = true
			if msg["from"] != win.ID {
				t.Errorf("the pane's mail is from %v, want its window %s", msg["from"], win.ID)
			}
		}
		if msg["text"] == "forged" {
			t.Errorf("a message the pane sent as human was stored: %v", msg)
		}
	}
	if !found {
		t.Fatalf("the pane's mail never reached the person: %v", msgs)
	}
}

// TestAProcessOutsideAHostedPaneCannotReportAsIt holds the far half's check:
// a call naming the pane from a process that is not in it is refused, not
// forwarded.
//
// Negative control: without the callerInHostedPane check the call goes through
// and the window's state changes.
func TestAProcessOutsideAHostedPaneCannotReportAsIt(t *testing.T) {
	hub, far := startHubAndFar(t)
	waitForHostUp(t, hub, "build")
	sess, win, pty := hostedHelperWindow(t, hub, far, "global", nil)
	waitForPaneText(t, pty, "DONE", paneBudget)

	// This test process is the daemons' own, and it is in no pane.
	farC := dialVerb(t, far.socket)
	resp := farC.call(t, fmt.Sprintf(`{"id":1,"verb":"set-agent-state","params":{"window":%q,"state":"errored"}}`, win.ID))
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil || errObj["code"] != ErrVerbForbidden {
		t.Fatalf("a report from outside the pane was answered %v, want forbidden", resp)
	}
	for _, w := range sess.GetState().Windows {
		if w.ID == win.ID && w.AgentState.Name() == "errored" {
			t.Fatal("a process outside the pane set its state")
		}
	}
}

// TestTheOwnerRunsAHostedCallAsItsOwnWindow is the owner's half, called
// directly: whatever session, window, sender or reader the request names, it
// is run as the window the pane is drawn in, with the far machine's paths and
// pids dropped.
func TestTheOwnerRunsAHostedCallAsItsOwnWindow(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess, err := d.manager.CreateSession("owner", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	other := makeSessionWithWindow(t, d, "other")
	if _, err := sess.AddDaemonWindow("mine", nil); err != nil {
		t.Fatalf("window: %v", err)
	}
	// Mark the window as on build, which is what runHostedCall requires.
	var mine string
	_ = sess.mutateState(func(st *SessionState) error {
		mine = st.Windows[0].ID
		st.Windows[0].Host = "build"
		return nil
	})

	otherWin := other.GetState().Windows[0].ID
	params := fmt.Sprintf(`{"session":"other","window":%q,"state":"errored","transcript_path":"/etc/passwd","harness_pid":1}`, otherWin)
	if _, verr := d.runHostedCall(sess, mine, "build", "set-agent-state", json.RawMessage(params), nil); verr != nil {
		t.Fatalf("the call failed: %v", verr)
	}
	if st := other.GetState().Windows[0].AgentState.Name(); st == "errored" {
		t.Error("a hosted call reached a window in another session")
	}
	if st := sess.GetState().Windows[0].AgentState.Name(); st != "errored" {
		t.Errorf("the pane's own window is %q, want errored", st)
	}

	if _, verr := d.runHostedCall(sess, mine, "build", "send-keys", json.RawMessage(`{"keys":"x"}`), nil); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("a verb outside the list was run: %v", verr)
	}
	if _, verr := d.runHostedCall(sess, mine, "elsewhere", "set-agent-state", json.RawMessage(`{"state":"idle"}`), nil); verr == nil {
		t.Error("a call from a host the window is not on was run")
	}
}

// TestAHostedPaneAttachesOnlyStashedFiles holds a hosted pane's mail to the
// link's attachment rule. Its process, and the daemon that forwarded the call,
// are on the other machine, so a path it names is not its file. A path outside
// the session's stash is refused before it is looked at, so the answer is the
// same for a file that exists and one that does not, and nothing is stored.
//
// Negative control: without the paneOnly half of the stash check in
// verbSendAgentMessage, /etc/hosts is attached and the missing file is told
// apart from it by the error.
func TestAHostedPaneAttachesOnlyStashedFiles(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, err := d.manager.CreateSession("owner", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, name := range []string{"mine", "peer"} {
		if _, err := sess.AddDaemonWindow(name, nil); err != nil {
			t.Fatalf("window: %v", err)
		}
	}
	var mine, peer string
	_ = sess.mutateState(func(st *SessionState) error {
		mine, peer = st.Windows[0].ID, st.Windows[1].ID
		st.Windows[0].Host = "build"
		return nil
	})

	send := func(path string) *verbError {
		params := fmt.Sprintf(`{"to":%q,"text":"look","attachments":[%q]}`, peer, path)
		_, verr := d.runHostedCall(sess, mine, "build", "send-agent-message", json.RawMessage(params), nil)
		return verr
	}
	var messages []string
	for _, path := range []string{"/etc/hosts", "/nonexistent/tuios-hosted-probe"} {
		verr := send(path)
		if verr == nil || verr.Code != ErrVerbInvalidParams || !strings.Contains(verr.Message, "only a stashed file") {
			t.Errorf("a hosted pane attaching %s was answered %v, want the stash refusal", path, verr)
			continue
		}
		messages = append(messages, strings.Replace(verr.Message, path, "PATH", 1))
	}
	if len(messages) == 2 && messages[0] != messages[1] {
		t.Errorf("the refusal tells a file that exists from one that does not: %q vs %q", messages[0], messages[1])
	}
	if _, ok := d.agents.firstUnread(sess.Name, peer, 0); ok {
		t.Error("a refused message was stored")
	}

	// A file the session stashed is what a hosted pane may attach.
	src := filepath.Join(t.TempDir(), "report.txt")
	writeBytes(t, src, 64, 7)
	c := dialVerb(t, sp)
	put := result(t, c.call(t, `{"id":1,"verb":"stash-put","params":{"session":"owner","path":`+quote(src)+`}}`))
	if verr := send(put["path"].(string)); verr != nil {
		t.Errorf("a hosted pane attaching a stashed file was refused: %v", verr)
	}
}

// TestADroppedChannelIsReopenedWhileAWaitIsInFlight: a pane's agent waits for
// mail, the report channel drops, and the owner opens a new one within
// hostedCallsRetry, where a report goes through again. The wait does not hold
// the owner on the dead channel.
//
// Negative control: with the owner waiting for its calls in flight before it
// redials, no channel comes back until the pane's 60 second wait runs out.
func TestADroppedChannelIsReopenedWhileAWaitIsInFlight(t *testing.T) {
	hub, far := startHubAndFar(t)
	waitForHostUp(t, hub, "build")
	sess, win, _ := hostedHelperWindow(t, hub, far, "global", []hostedHelperCall{
		{Verb: "wait-for", Params: json.RawMessage(`{"condition":"agent-message","window":"$PANE","timeout":60000}`)},
	})
	hp := far.daemon.hostedPaneByAddress(win.ID)
	if hp == nil {
		t.Fatal("the far daemon runs no pane for the window")
	}
	inFlight := func(ch *hostedCallChannel) bool {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		return ch.inFlight > 0
	}
	var first *hostedCallChannel
	waitUntil(t, func() bool {
		first = hp.calls.current(0)
		return first != nil && inFlight(first)
	}, "the pane's wait never reached the owner")

	_ = first.conn.Close()
	start := time.Now()
	var next *hostedCallChannel
	for next == nil || next == first {
		if time.Since(start) > hostedCallsRetry+3*time.Second {
			t.Fatalf("no new report channel %v after the old one dropped", time.Since(start))
		}
		time.Sleep(50 * time.Millisecond)
		next = hp.calls.current(0)
	}
	t.Logf("the channel was back after %v", time.Since(start))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, verr := next.call(ctx, "set-agent-state", json.RawMessage(`{"state":"errored"}`)); verr != nil {
		t.Fatalf("a report on the new channel failed: %v", verr)
	}
	for _, w := range sess.GetState().Windows {
		if w.ID == win.ID && w.AgentState.Name() != "errored" {
			t.Errorf("the window's state is %q, want errored", w.AgentState.Name())
		}
	}
}

// TestAForwardedWaitEndsWithItsChannel: the owner's wait-for for a hosted pane
// stops when the report channel it came on ends, rather than holding a slot
// and a subscription for the rest of the timeout the far side asked for.
//
// Negative control: without the hostedEnded case in verbWaitFor the call runs
// for its full 60 seconds.
func TestAForwardedWaitEndsWithItsChannel(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess, err := d.manager.CreateSession("owner", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := sess.AddDaemonWindow("mine", nil); err != nil {
		t.Fatalf("window: %v", err)
	}
	var mine string
	_ = sess.mutateState(func(st *SessionState) error {
		mine = st.Windows[0].ID
		st.Windows[0].Host = "build"
		return nil
	})
	ended := make(chan struct{})
	done := make(chan *verbError, 1)
	go func() {
		_, verr := d.runHostedCall(sess, mine, "build", "wait-for", json.RawMessage(`{"condition":"agent-message","timeout":60000}`), ended)
		done <- verr
	}()
	select {
	case verr := <-done:
		t.Fatalf("the wait returned before the channel ended: %v", verr)
	case <-time.After(200 * time.Millisecond):
	}
	close(ended)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the wait outlived the channel it came on")
	}
}

// TestTheOwnerCapsAForwardedWait: the owner does not take the far machine's
// word for how long a wait may run.
func TestTheOwnerCapsAForwardedWait(t *testing.T) {
	hour := int(hostedWaitMax.Milliseconds())
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{`"soon"`, 0},
		{"30000", 30000},
		{strconv.Itoa(hour), hour},
		{"9000000000", hour},
	} {
		if got := clampHostedWait(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("clampHostedWait(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// TestAReportToAnOwnerTooOldToTakeItSaysSo is the version skew: an owner that
// sent no window id opens no channel, and a report names the pane id and is
// told to update the owner rather than hanging.
func TestAReportToAnOwnerTooOldToTakeItSaysSo(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the caller's pid is not given on this platform")
	}
	d, _ := startTestDaemon(t)
	hp, err := d.registerHostedPane(hostedPaneSpec{Width: 80, Height: 24, Command: []string{"/bin/sh", "-c", "sleep 30"}})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { d.forgetHostedPane(hp.id) })
	if hp.callsToken != "" {
		t.Fatal("a pane opened without a window id was given a calls token")
	}
	cs := &connState{peerPID: hp.cmd.Process.Pid, done: make(chan struct{})}
	start := time.Now()
	_, verr, handled := d.forwardHostedCall(cs, "set-agent-state", json.RawMessage(fmt.Sprintf(`{"window":%q,"state":"idle"}`, hp.id)))
	if !handled || verr == nil || verr.Code != ErrVerbProtocolMismatch {
		t.Fatalf("the report was answered %v (handled %v), want protocol_mismatch", verr, handled)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the refusal waited %v for a channel an old owner never opens", time.Since(start))
	}
}

// TestPaneCallsNeedsTheToken keeps the channel the owner's: the pane's process
// knows its pane id and window id but never sees the token.
func TestPaneCallsNeedsTheToken(t *testing.T) {
	d, sp := startTestDaemon(t)
	hp, err := d.registerHostedPane(hostedPaneSpec{Width: 80, Height: 24, Window: "win-1", Command: []string{"/bin/sh", "-c", "sleep 30"}})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { d.forgetHostedPane(hp.id) })
	c := dialVerb(t, sp)
	resp := c.call(t, fmt.Sprintf(`{"id":1,"verb":"pane-calls","params":{"pane":%q,"token":"guess"}}`, hp.id))
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil || errObj["code"] != ErrVerbForbidden {
		t.Fatalf("pane-calls with a wrong token was answered %v, want forbidden", resp)
	}
}
