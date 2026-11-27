package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// archivedBus is a bus whose archive is on and writes under a temp folder.
func archivedBus(t *testing.T) *agentBus {
	t.Helper()
	bus := newAgentBus()
	bus.archive.dir = filepath.Join(t.TempDir(), "mail")
	bus.archive.setOn(true)
	// Registered after TempDir, so it runs first: a send at the end of a test
	// can leave a write queued, and the folder must outlive that write.
	t.Cleanup(func() { settleArchive(t, &bus.archive) })
	return bus
}

// settleArchive waits until the archive's writer has written what is queued.
func settleArchive(t *testing.T, a *mailArchive) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		busy := a.writing || len(a.pending) > 0
		a.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the mail archive never finished writing")
		}
		time.Sleep(time.Millisecond)
	}
}

func archived(t *testing.T, bus *agentBus, session string) []AgentMessage {
	t.Helper()
	settleArchive(t, &bus.archive)
	msgs, _, err := bus.archive.read(session, 0, func(*AgentMessage) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

// TestMailArchiveKeepsWhatTheRingDrops: direct mail the ring evicts is in the
// archive, in order, in a file only the user can read. A notice is not kept.
// A reply to an evicted message joins the evicted message's thread.
//
// Negative control: with append's evictions not passed to the archive in
// send, the archive is empty and this fails.
func TestMailArchiveKeepsWhatTheRingDrops(t *testing.T) {
	bus := archivedBus(t)
	root := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "lead", Text: "the question"})
	reply := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "worker", Text: "the first answer", ReplyTo: root.ID})
	bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "a notice"})
	for range agentMailboxMaxMessages {
		bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "lead", Text: "filler"})
	}
	if held(bus, "s", root.ID) || held(bus, "s", reply.ID) {
		t.Fatal("the ring still holds the thread, so this test proves nothing")
	}

	got := archived(t, bus, "s")
	if len(got) < 2 || got[0].ID != root.ID || got[1].ID != reply.ID {
		t.Fatalf("the archive starts with %d message(s), want the root and the reply first", len(got))
	}
	for _, m := range got {
		if m.Kind != agentMsgDirect {
			t.Errorf("the archive kept a %s entry, want direct mail only", m.Kind)
		}
	}
	path, _ := bus.archive.path("s")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the archive file has mode %v, want 0600", st.Mode().Perm())
	}
	if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
		t.Errorf("the archive folder has mode %v, want 0700", dir.Mode().Perm())
	}

	late := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "lead", Text: "a late answer", ReplyTo: reply.ID})
	if late.ThreadID != root.ID {
		t.Errorf("a reply to an evicted reply is in thread %d, want the root's thread %d", late.ThreadID, root.ID)
	}
	if got := bus.resolveThread("s", reply.ID); got != root.ID {
		t.Errorf("the evicted reply resolves to thread %d, want %d", got, root.ID)
	}
}

// TestMailArchiveGoesWithItsSession: a rename moves the files, and the end of
// the session removes them. Off, nothing is written.
func TestMailArchiveGoesWithItsSession(t *testing.T) {
	bus := archivedBus(t)
	for range agentMailboxMaxMessages + 4 {
		bus.send("old", AgentMessage{Kind: agentMsgDirect, To: "lead", Text: "x"})
	}
	settleArchive(t, &bus.archive)
	bus.rename("old", "new")
	if n := len(archived(t, bus, "new")); n != 4 {
		t.Fatalf("after the rename the archive of new holds %d messages, want 4", n)
	}
	oldPath, _ := bus.archive.path("old")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("the archive of old is still there after the rename: %v", err)
	}

	bus.forget("new")
	newPath, _ := bus.archive.path("new")
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Errorf("the archive of an ended session is still there: %v", err)
	}

	off := newAgentBus()
	off.archive.dir = filepath.Join(t.TempDir(), "mail")
	for range agentMailboxMaxMessages + 4 {
		off.send("s", AgentMessage{Kind: agentMsgDirect, To: "lead", Text: "x"})
	}
	settleArchive(t, &off.archive)
	if _, err := os.Stat(off.archive.folder()); !os.IsNotExist(err) {
		t.Errorf("with the archive off, the folder was made: %v", err)
	}
}

// TestMailArchiveIsCutAtItsCap: one session keeps at most two files, the
// newest under the cap.
func TestMailArchiveIsCutAtItsCap(t *testing.T) {
	bus := archivedBus(t)
	big := strings.Repeat("y", agentMsgMaxText)
	n := 3*mailArchiveMaxBytes/agentMsgMaxText + agentMailboxMaxBytes/agentMsgMaxText
	for range n {
		bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "lead", Text: big})
	}
	settleArchive(t, &bus.archive)
	path, _ := bus.archive.path("s")
	for _, p := range []string{path, path + ".1"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > mailArchiveMaxBytes {
			t.Errorf("%s holds %d bytes, over the %d cap", filepath.Base(p), st.Size(), mailArchiveMaxBytes)
		}
	}
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Error("the archive keeps a third file")
	}
}

// TestReadAgentMessagesReadsTheArchive is the verb: read-agent-messages with
// archive answers the evicted mail of one inbox, and marks nothing.
func TestReadAgentMessagesReadsTheArchive(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "arch")
	d.agents.archive.dir = filepath.Join(t.TempDir(), "mail")
	d.SetMailArchive(true)
	first := d.agents.send("arch", AgentMessage{Kind: agentMsgDirect, To: a, From: b, Text: "the first report"})
	for range agentMailboxMaxMessages {
		d.agents.send("arch", AgentMessage{Kind: agentMsgDirect, To: b, From: a, Text: "filler"})
	}
	settleArchive(t, &d.agents.archive)

	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"read-agent-messages","params":{"session":"arch","to":"`+a+`","archive":true}}`))
	msgs, _ := res["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("the archive read of a's inbox answered %d messages, want 1: %v", len(msgs), res)
	}
	m := msgs[0].(map[string]any)
	if m["text"] != "the first report" || uint64(m["id"].(float64)) != first.ID {
		t.Errorf("the archive read answered %v, want the first report", m)
	}
	if res["archive"] != true {
		t.Error("the answer does not say it came from the archive")
	}
}
