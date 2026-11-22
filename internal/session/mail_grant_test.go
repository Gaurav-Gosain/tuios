package session

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mailFixture is scopeFixture with b1 reporting as an agent, so list-agents
// and selectors see it, and a1 given grants by the person.
func mailFixture(t *testing.T, grants ...string) (d *Daemon, sp, a1, a2, b1 string) {
	t.Helper()
	d, sp, a1, a2, b1 = scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	result(t, callP(person, t, "set-agent-state", map[string]any{"session": "b", "window": b1, "state": "idle"}))
	result(t, callP(person, t, "set-agent-state", map[string]any{"session": "a", "window": a2, "state": "idle"}))
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": grants}))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	return d, sp, a1, a2, b1
}

// TestMailGrantSendsToAnySessionAsThePane: a pane held to its own session can
// still leave mail in another one with mail, always as itself. Without mail
// the same send stays refused, so write alone reaches no further than before.
func TestMailGrantSendsToAnySessionAsThePane(t *testing.T) {
	_, sp, _, _, b1 := mailFixture(t, "read", "write", "task")
	c := dialVerb(t, sp)
	wantForbidden(t, "mail to b without the mail grant", callP(c, t, "send-agent-message", map[string]any{"session": "b", "to": b1, "text": "hi"}))
	wantForbidden(t, "list-agents across sessions without the mail grant", callP(c, t, "list-agents", map[string]any{"all_sessions": true}))

	_, sp, a1, _, b1 := mailFixture(t, "read", "write", "mail", "task")
	c = dialVerb(t, sp)
	sent := result(t, callP(c, t, "send-agent-message", map[string]any{"session": "b", "to": b1, "text": "the app needs a queue"}))
	if sent["from"] != a1 || sent["session"] != "b" || sent["to"] != b1 {
		t.Errorf("mail = %v, want from %s to %s in b", sent, a1, b1)
	}
	// Its own session still works, with the own session filled in.
	if own := result(t, callP(c, t, "send-agent-message", map[string]any{"text": "notice"})); own["session"] != "a" {
		t.Errorf("mail with no session went to %v, want the pane's own a", own["session"])
	}

	// The sender is the pane, never a claim.
	wantForbidden(t, "from another pane", callP(c, t, "send-agent-message", map[string]any{"session": "b", "to": b1, "from": b1, "text": "x"}))
	wantForbidden(t, "from human", callP(c, t, "send-agent-message", map[string]any{"session": "b", "to": b1, "from": "human", "text": "x"}))
	// Another machine is not this daemon.
	wantForbidden(t, "mail to a host", callP(c, t, "send-agent-message", map[string]any{"host": "far", "session": "b", "text": "x"}))

	// mail types nothing, and reads nothing it could not read before.
	wantForbidden(t, "send-text into b", callP(c, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"}))
	wantForbidden(t, "ask-agent of b", callP(c, t, "ask-agent", map[string]any{"session": "b", "window": b1, "text": "x"}))
	wantForbidden(t, "queue-prompt into b", callP(c, t, "queue-prompt", map[string]any{"session": "b", "window": b1, "text": "x"}))
	wantForbidden(t, "capture-pane of b", callP(c, t, "capture-pane", map[string]any{"session": "b", "window": b1}))
	wantForbidden(t, "peek-prompt of b", callP(c, t, "peek-prompt", map[string]any{"session": "b", "window": b1}))
	wantForbidden(t, "read-agent-messages of b", callP(c, t, "read-agent-messages", map[string]any{"session": "b"}))
	wantForbidden(t, "stash-put into b", callP(c, t, "stash-put", map[string]any{"session": "b", "path": "/etc/hosts"}))
	wantForbidden(t, "start-agent", callP(c, t, "start-agent", map[string]any{"session": "b", "agent": "true"}))
}

// TestMailGrantListsOnlyWhatAddressingNeeds: a listing across sessions gives
// the rows of a session the pane cannot read cut to session, window, name,
// state and harness, and leaves out that session's unread count for the
// person. The pane's own session keeps its full rows.
func TestMailGrantListsOnlyWhatAddressingNeeds(t *testing.T) {
	_, sp, _, a2, b1 := mailFixture(t, "read", "write", "mail")
	c := dialVerb(t, sp)
	listed := result(t, callP(c, t, "list-agents", map[string]any{"all_sessions": true}))
	rows := listed["agents"].([]any)
	var sawA, sawB bool
	for _, r := range rows {
		row := r.(map[string]any)
		switch row["window_id"] {
		case b1:
			sawB = true
			var keys []string
			for k := range row {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if want := []string{"harness_id", "limited", "name", "session", "state", "window_id"}; !slices.Equal(keys, want) {
				t.Errorf("row of b1 carries %v, want only %v", keys, want)
			}
		case a2:
			sawA = true
			if _, cut := row["limited"]; cut || row["cwd"] == nil {
				t.Errorf("row of the pane's own session was cut: %v", row)
			}
		}
	}
	if !sawA || !sawB {
		t.Fatalf("list-agents --all-sessions rows = %v, want a2 and b1", rows)
	}

	// A selector across sessions gives the token a send by it takes.
	sel := result(t, callP(c, t, "list-agents", map[string]any{"select": "session:b"}))
	token, _ := sel["confirm"].(string)
	if token == "" || len(sel["agents"].([]any)) != 1 {
		t.Fatalf("list-agents --select session:b = %v, want b1 and a confirm token", sel)
	}
	sent := result(t, callP(c, t, "send-agent-message", map[string]any{"select": "session:b", "confirm": token, "text": "rebase on main"}))
	if sent["sent"] != float64(1) {
		t.Errorf("send by selector = %v, want one sent", sent)
	}

	// A session it cannot read, named, is cut the same way; every window of
	// it is not for mail to list.
	one := result(t, callP(c, t, "list-agents", map[string]any{"session": "b"}))
	if row := one["agents"].([]any)[0].(map[string]any); row["limited"] != true || row["cwd"] != nil {
		t.Errorf("row of b listed by session = %v, want it cut", row)
	}
	wantForbidden(t, "list-agents --all of b", callP(c, t, "list-agents", map[string]any{"session": "b", "all": true}))
}

// TestMailGrantAttachesOnlyThePanesOwnFiles: the daemon stats an attachment
// and points the recipient at it, so mail into a session the pane cannot
// write to carries no path from outside the pane's own folder.
func TestMailGrantAttachesOnlyThePanesOwnFiles(t *testing.T) {
	_, sp, _, a2, b1 := mailFixture(t, "read", "write", "mail")
	c := dialVerb(t, sp)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := callP(c, t, "send-agent-message", map[string]any{"session": "b", "to": b1, "text": "see file", "attachments": []string{outside}})
	wantForbidden(t, "attachment from outside the pane's folder", resp)
	if msg := resp["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "own folder") {
		t.Errorf("refusal %q does not name the pane's folder", msg)
	}
	// Mail into its own session, which write reaches, is held as before.
	result(t, callP(c, t, "send-agent-message", map[string]any{"to": a2, "text": "see file", "attachments": []string{outside}}))
}

// TestMailGrantWakesWithTheFixedLineOnly: the wake line holds nothing a sender
// chose, so a pane with mail lends it to a pane it could not type into. The
// same authority does not cover a queued prompt, and the wake still is not
// typed into a prompt.
func TestMailGrantWakesWithTheFixedLineOnly(t *testing.T) {
	d, _, a1, _, b1 := mailFixture(t, "read", "write", "mail")
	sess := d.manager.GetSession("b")
	target, ok := findWindowState(sess.GetState(), b1)
	if !ok {
		t.Fatal("b1 not found")
	}
	origin := queueOrigin{kind: "pane", window: a1, session: "a"}
	if why := d.queueOriginRefusal(&queueEntry{origin: origin, mailWake: true}, sess, target); why != "" {
		t.Errorf("mail wake from a pane with mail refused: %s", why)
	}
	if why := d.queueOriginRefusal(&queueEntry{origin: origin}, sess, target); why == "" {
		t.Error("a queued prompt into another session passed with mail alone")
	}
	target.AgentState = AgentStateNeedsInput
	if why := d.queueOriginRefusal(&queueEntry{origin: origin, mailWake: true}, sess, target); why == "" {
		t.Error("a mail wake into a pane on a prompt passed")
	}

	// Without mail the wake into another session is refused, as before.
	target.AgentState = AgentStateIdle
	g := GrantRead | GrantWrite
	d.manager.grants.set(a1, &g)
	if why := d.queueOriginRefusal(&queueEntry{origin: origin, mailWake: true}, sess, target); why == "" {
		t.Error("a mail wake into another session passed without the mail grant")
	}
}

// TestMailGrantIsNamedAndImpliedByAdmin: mail is a grant of its own that admin
// implies and write does not, so no existing setup gains it.
func TestMailGrantIsNamedAndImpliedByAdmin(t *testing.T) {
	g, verr := parseGrantsParam([]string{"read", "mail"})
	if verr != nil || g != GrantRead|GrantMail {
		t.Fatalf("parse read,mail = %v %v", g, verr)
	}
	if got := g.String(); got != "read,mail" {
		t.Errorf("String = %q, want read,mail", got)
	}
	if !GrantAdmin.Has(GrantMail) {
		t.Error("admin does not imply mail")
	}
	if (GrantRead | GrantWrite | GrantFan).Has(GrantMail) {
		t.Error("write or fan implies mail")
	}
	if (GrantRead | GrantWrite).Covers(GrantMail) {
		t.Error("a pane without mail can give mail")
	}
}
