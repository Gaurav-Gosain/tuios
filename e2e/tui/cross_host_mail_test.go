package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The mailbox across two machines, driven the way a person and an agent reach
// it: a CLI on this machine naming a session on host build, and a client
// attached on build opening its mailbox.
//
// build is real in every way that matters: a second daemon with its own
// runtime directory, its own sessions and its own shell, reached only over
// the hub daemon's link. The ssh stand-in is the one from host_attach_test.go,
// so the link runs through the real subprocess transport, framing and proxy,
// and nothing reads the developer's ssh configuration or touches a network.
//
// What would pass a weaker test and fail these: a --host that only listed, a
// message that landed in this machine's ring, a proxy that dialed the main
// socket so the message arrived unmarked, or an overlay that drew a remote
// message the way it draws a local one.

// hubWithBuild starts the hub daemon on this machine with one host, build,
// routed to the remote root, and waits for the link to be up. It returns the
// environment every CLI call against the hub needs.
func hubWithBuild(t *testing.T, base, remote string) []string {
	t.Helper()
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	listing := waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "build") && strings.Contains(s, "up")
	}, "the hub never reported build up")
	t.Logf("tuios hosts:\n%s", listing)
	return env
}

// TestMailCrossesTheLink sends a message from this machine into a session on
// build, and proves it on build's own screen: announced, listed with the mark
// for another machine, and read with its origin said in words. The reply
// from build's mailbox is then read back here, over the link, in the thread.
func TestMailCrossesTheLink(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)

	// The session lives on build, with a client attached there.
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	farTerm := startIn(t, remote, startOpts{args: []string{"attach", "far"}})
	if err := farTerm.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client on build never attached: %v\n%s", err, farTerm.Snapshot())
	}

	env := hubWithBuild(t, base, remote)
	me, _ := os.Hostname()

	// The command an agent on this machine types.
	out, err := tuiosCLIEnv(t, base, env, "send-agent-message", "-s", "build:far", "-w", "human",
		"--from", "ORCHESTRATOR", "--subject", "ship it?", "the far build is green")
	if err != nil {
		t.Fatalf("ASSERTION: send-agent-message to build failed, so the send did not reach build's daemon as a link connection: %v\n%s", err, out)
	}
	if !strings.Contains(out, "queued for human") || !strings.Contains(out, "on build") {
		t.Fatalf("ASSERTION: the send did not say it reached build:\n%s", out)
	}
	// It is build's ring and not this machine's.
	if here, _ := tuiosCLI(t, base, "read-agent-messages", "-s", "home", "--peek"); strings.Contains(here, "far build is green") {
		t.Fatalf("ASSERTION: the message landed in this machine's ring:\n%s", here)
	}

	// On build: the dock names the sender with the machine it is on.
	if err := farTerm.WaitForText("ORCHESTRATOR @ "+me+" to you: ship it?", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: build's dock never announced mail from this machine: %v\n%s", err, farTerm.Snapshot())
	}
	// The mailbox lists the thread with the mark for another machine.
	if err := farTerm.SendKeys(tuitest.Ctrl('b'), "M"); err != nil {
		t.Fatalf("open the mailbox on build: %v", err)
	}
	if err := farTerm.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "⇄") && strings.Contains(text, "ORCHESTRATOR @ "+me) && strings.Contains(text, "ship it?")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: build's mailbox does not mark the thread as from another machine: %v\n%s", err, farTerm.Snapshot())
	}
	saveFrame(t, farTerm, "mail-remote-list")
	if err := farTerm.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the thread: %v", err)
	}
	if err := farTerm.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "the far build is green") && strings.Contains(text, "over a link") && strings.Contains(text, "another machine")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the thread on build does not say the message came over a link: %v\n%s", err, farTerm.Snapshot())
	}
	saveFrame(t, farTerm, "mail-remote-thread")
	t.Logf("build's mailbox, a message from this machine:\n%s", farTerm.Snapshot())

	// The person on build answers from the overlay.
	if err := farTerm.SendKeys("r"); err != nil {
		t.Fatalf("open the reply line: %v", err)
	}
	if err := farTerm.WaitForText("reply:", uiTimeout); err != nil {
		t.Fatalf("r did not open the reply line: %v\n%s", err, farTerm.Snapshot())
	}
	if err := farTerm.SendKeys("ship it", tuitest.Enter); err != nil {
		t.Fatalf("type the reply: %v", err)
	}
	if err := farTerm.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "ship it") && !strings.Contains(s.Text(), "reply:")
	}, uiTimeout); err != nil {
		t.Fatalf("the reply never came back into the thread: %v\n%s", err, farTerm.Snapshot())
	}

	// This machine reads the thread back over the link. The fence names the
	// machine the ring is on and, for the message this machine sent, that
	// it arrived there over a link.
	out, err = tuiosCLIEnv(t, base, env, "read-agent-messages", "-s", "build:far", "--peek")
	if err != nil {
		t.Fatalf("read-agent-messages on build: %v\n%s", err, out)
	}
	for _, want := range []string{"arrived over a link", "in the ring on build", "ship it", "on " + me} {
		if !strings.Contains(out, want) {
			t.Errorf("ASSERTION: the read over the link does not say %q:\n%s", want, out)
		}
	}
	out, err = tuiosCLIEnv(t, base, env, "read-agent-messages", "-s", "build:far", "--peek", "--json")
	if err != nil {
		t.Fatalf("read-agent-messages --json on build: %v\n%s", err, out)
	}
	var ring struct {
		Host     string `json:"host"`
		Messages []struct {
			Origin     string `json:"origin"`
			OriginHost string `json:"origin_host"`
			FromLabel  string `json:"from_label"`
			From       string `json:"from"`
			Text       string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &ring); err != nil {
		t.Fatalf("read-agent-messages returned no JSON: %v\n%s", err, out)
	}
	if ring.Host != "build" {
		t.Errorf("ASSERTION: the JSON does not say which host answered: host=%q", ring.Host)
	}
	if len(ring.Messages) != 2 {
		t.Fatalf("build's ring holds %d message(s), want the question and the reply:\n%s", len(ring.Messages), out)
	}
	if q := ring.Messages[0]; q.Origin != "link" || q.OriginHost != me || q.FromLabel != "ORCHESTRATOR" || q.From != "" {
		t.Errorf("ASSERTION: the question on build is not marked as from this machine: %+v", q)
	}
	if r := ring.Messages[1]; r.Origin != "" || r.From != "human" || r.Text != "ship it" {
		t.Errorf("the reply from build's own person is marked wrong: %+v", r)
	}
	alive(t, farTerm, "after replying on build")
}

// TestVerbsTakeAHostQualifiedTarget is the reachability proof for the target
// grammar on the verbs a person types: each names build's session and gets
// build's answer, said as build's.
func TestVerbsTakeAHostQualifiedTarget(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	env := hubWithBuild(t, base, remote)
	me, _ := os.Hostname()

	out, err := tuiosCLIEnv(t, base, env, "list-windows", "-s", "build:far")
	if err != nil {
		t.Fatalf("ASSERTION: list-windows -s build:far failed, so the target did not reach build: %v\n%s", err, out)
	}
	t.Logf("list-windows -s build:far:\n%s", out)
	if !strings.Contains(out, "window(s) on build") {
		t.Fatalf("ASSERTION: list-windows did not say its answer is build's:\n%s", out)
	}
	out, _ = tuiosCLIEnv(t, base, env, "list-windows", "-s", "build:far", "--json")
	var listed struct {
		Host string `json:"host"`
	}
	if json.Unmarshal([]byte(out), &listed) != nil || listed.Host != "build" {
		t.Fatalf("ASSERTION: the JSON listing carries no host:\n%s", out)
	}

	// A program in a pane on build knows which machine it is on, and so
	// does one in a pane here: TUIOS_HOST is the daemon's own hostname on
	// every machine.
	if out, err := tuiosCLIEnv(t, base, env, "send-text", "-s", "build:far", "-w", "0", "echo H=$TUIOS_HOST\n"); err != nil {
		t.Fatalf("send-text on build: %v\n%s", err, out)
	}
	waitForCapture(t, base, env, []string{"-s", "build:far", "-w", "build:far:0"}, "H="+me)
	if out, err := tuiosCLI(t, base, "send-text", "-s", "home", "echo L=$TUIOS_HOST\n"); err != nil {
		t.Fatalf("send-text here: %v\n%s", err, out)
	}
	waitForCapture(t, base, nil, []string{"-s", "home"}, "L="+me)

	// wait-for and focus-window on build, and the agent listing.
	out, err = tuiosCLIEnv(t, base, env, "wait-for", "window-output", "-w", "build:far:0", "--pattern", "H=", "--timeout", "5000")
	if err != nil || !strings.Contains(out, "on build") {
		t.Fatalf("ASSERTION: wait-for on build: %v\n%s", err, out)
	}
	out, err = tuiosCLIEnv(t, base, env, "focus-window", "-s", "build:far", "0")
	if err != nil || !strings.Contains(out, "on build") {
		t.Fatalf("ASSERTION: focus-window on build: %v\n%s", err, out)
	}
	out, err = tuiosCLIEnv(t, base, env, "list-agents", "-s", "build:far", "--all")
	if err != nil || !strings.Contains(out, "on build") {
		t.Fatalf("ASSERTION: list-agents on build: %v\n%s", err, out)
	}

	// An unknown host is refused by name, and the refusal says how to reach
	// a session here whose name has a colon.
	out, _ = tuiosCLIEnv(t, base, env, "list-windows", "-s", "nowhere:far")
	if !strings.Contains(out, "nowhere") || !strings.Contains(out, "local:nowhere:far") {
		t.Fatalf("ASSERTION: an unknown host is not refused by name with the local: spelling:\n%s", out)
	}
	// And local: reaches this machine.
	out, err = tuiosCLI(t, base, "list-windows", "-s", "local:home")
	if err != nil || strings.Contains(out, "on build") {
		t.Fatalf("ASSERTION: local:home did not list this machine's session: %v\n%s", err, out)
	}

	// kill-session on build kills build's session and nothing here.
	out, err = tuiosCLIEnv(t, base, env, "kill-session", "build:far")
	if err != nil || !strings.Contains(out, "on build") {
		t.Fatalf("ASSERTION: kill-session on build: %v\n%s", err, out)
	}
	if far, _ := tuiosCLI(t, remote, "ls"); strings.Contains(far, "far") {
		t.Fatalf("ASSERTION: the far session survived kill-session:\n%s", far)
	}
	if here, _ := tuiosCLI(t, base, "ls"); !strings.Contains(here, "home") {
		t.Fatalf("ASSERTION: kill-session on build killed this machine's session:\n%s", here)
	}
}

// waitForCapture polls capture-pane with the given target flags until the
// pane shows want.
func waitForCapture(t *testing.T, base string, env, target []string, want string) {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	var out string
	for time.Now().Before(deadline) {
		args := append([]string{"capture-pane"}, target...)
		out, _ = tuiosCLIEnv(t, base, env, args...)
		if strings.Contains(out, want) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the pane never showed %q:\n%s", want, out)
}

// TestAFileCrossesTheLinkThroughTheStash is path in, path out: a file here
// goes into build's stash and comes back as build's path, which a message on
// build can attach, and the stored file comes back here as bytes.
func TestAFileCrossesTheLinkThroughTheStash(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	env := hubWithBuild(t, base, remote)
	me, _ := os.Hostname()

	src := filepath.Join(base, "flame.png")
	want := []byte("PNG bytes that only exist on this machine")
	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tuiosCLIEnv(t, base, env, "stash", "put", "-s", "build:far", src)
	if err != nil {
		t.Fatalf("ASSERTION: stash put -s build:far failed, so the file's bytes did not cross the link: %v\n%s", err, out)
	}
	stored := strings.SplitN(out, "\n", 2)[0]
	if !strings.HasPrefix(stored, filepath.Join(remote, "XDG_RUNTIME_DIR")) {
		t.Fatalf("ASSERTION: the stored path is not in build's runtime directory:\n%s", out)
	}
	if got, err := os.ReadFile(stored); err != nil || string(got) != string(want) {
		t.Fatalf("ASSERTION: the file on build does not hold the bytes sent: %v %q", err, got)
	}

	// build's listing says where the file came from.
	if list, _ := tuiosCLI(t, remote, "stash", "list", "-s", "far"); !strings.Contains(list, me) {
		t.Errorf("ASSERTION: build's stash listing does not name this machine as the source:\n%s", list)
	}

	// The stored path is attachable in a message on build, which is the
	// one kind of attachment a message from another machine may carry.
	out, err = tuiosCLIEnv(t, base, env, "send-agent-message", "-s", "build:far", "--attach", stored, "here is the flame graph")
	if err != nil {
		t.Fatalf("ASSERTION: the stashed path could not be attached on build: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, base, env, "send-agent-message", "-s", "build:far", "--attach", src, "and a path from here"); err == nil {
		t.Fatalf("ASSERTION: a path from this machine was accepted as an attachment on build:\n%s", out)
	}

	// And back: the bytes come here under a path this machine can open.
	back := filepath.Join(base, "back.png")
	out, err = tuiosCLIEnv(t, base, env, "stash", "get", "-s", "build:far", stored, back)
	if err != nil {
		t.Fatalf("stash get from build: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(back); err != nil || string(got) != string(want) {
		t.Fatalf("ASSERTION: the file did not come back whole: %v %q", err, got)
	}
	if !strings.Contains(out, "on build") {
		t.Errorf("stash get did not say where the file came from:\n%s", out)
	}
}

// TestMailboxFollowsTheSessionAcrossTheLink is the overlay in the other
// direction: a client here attached to a session on build opens its mailbox
// and reads build's ring, not this machine's, and its reply lands on build
// marked as arrived over a link. Before this the overlay dialed the daemon
// here, whatever session was on screen.
func TestMailboxFollowsTheSessionAcrossTheLink(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	env := hubWithBuild(t, base, remote)

	// Mail waiting in build's ring, from an agent on build.
	if out, err := tuiosCLI(t, remote, "send-agent-message", "-s", "far", "-w", "human", "--from", "0",
		"--subject", "from build itself", "the pane on build says hello"); err != nil {
		t.Fatalf("send on build: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "far"}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached through build: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "M"); err != nil {
		t.Fatalf("open the mailbox: %v", err)
	}
	if err := term.WaitForText("from build itself", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the mailbox on a session attached through build does not show build's ring: %v\n%s", err, term.Snapshot())
	}
	// A message from build's own pane is not marked as from another
	// machine: the mark is about where the message was written, and this
	// one was written where the ring is.
	if strings.Contains(term.Screen().Text(), "⇄") {
		t.Fatalf("ASSERTION: a message written on build is marked as from another machine:\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the thread: %v", err)
	}
	if err := term.WaitForText("the pane on build says hello", uiTimeout); err != nil {
		t.Fatalf("the thread never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if err := term.WaitForText("reply:", uiTimeout); err != nil {
		t.Fatalf("r did not open the reply line: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("hello from here", tuitest.Enter); err != nil {
		t.Fatalf("type the reply: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "hello from here") && !strings.Contains(s.Text(), "reply:")
	}, uiTimeout); err != nil {
		t.Fatalf("the reply never came back: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mail-through-host")

	// The reply is in build's ring, from human, and marked as arrived over
	// a link, because it was written on this machine.
	out, err := tuiosCLI(t, remote, "read-agent-messages", "-s", "far", "--peek", "--json")
	if err != nil {
		t.Fatalf("read on build: %v\n%s", err, out)
	}
	var ring struct {
		Messages []struct {
			From   string `json:"from"`
			Origin string `json:"origin"`
			Text   string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &ring); err != nil || len(ring.Messages) != 2 {
		t.Fatalf("build's ring: %v\n%s", err, out)
	}
	if r := ring.Messages[1]; r.From != "human" || r.Origin != "link" || r.Text != "hello from here" {
		t.Fatalf("ASSERTION: the reply from this client is not in build's ring as the person's, over a link: %+v", r)
	}
	if here, _ := tuiosCLI(t, base, "read-agent-messages", "-s", "home", "--peek"); strings.Contains(here, "hello from here") {
		t.Fatalf("ASSERTION: the reply landed in this machine's ring:\n%s", here)
	}
	alive(t, term, "after replying through build")
}
