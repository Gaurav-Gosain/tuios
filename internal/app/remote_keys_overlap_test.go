package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Two send-keys commands can overlap on one client: the remote command
// handler returns at once, so a second chain of RemoteKeyMsg starts while the
// first still runs, and the two goroutines that feed the loop interleave.
// The client used to keep one bool for "a chain runs", and the first chain's
// RemoteKeysDoneMsg cleared it while the second still delivered keys. Those
// keys then passed every "did the person press this" gate. These tests drive
// the real Update with the two chains interleaved one message at a time, in
// the order that broke: A1, B1, A done, B2 and on.
//
// Negative control: with RemoteKeysDoneMsg clearing ProcessingRemoteKeys
// for every chain, and RemoteKeyMsg not marking its own call, both fail. See
// e2e/tui/NEGATIVE_CONTROLS.md, "Overlapping send-keys".

// runInterleaved starts each key string as one send-keys chain and feeds
// the chains' messages to Update in turn, the head of each chain first.
func runInterleaved(t *testing.T, m *OS, chains ...string) {
	t.Helper()
	var queue []tea.Msg
	for i, keys := range chains {
		cmd, err := m.startRemoteSendKeys(keys, false, false, "", string(rune('A'+i)))
		if err != nil {
			t.Fatalf("send-keys %q: %v", keys, err)
		}
		queue = append(queue, cmd())
	}
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		_, cmd := m.Update(msg)
		if next := chainNext(cmd); next != nil {
			queue = append(queue, next)
		}
	}
}

// chainNext runs cmd and returns the chain's next message in it, or nil.
func chainNext(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	switch v := cmd().(type) {
	case RemoteKeyMsg, RemoteKeysDoneMsg:
		return v
	case tea.BatchMsg:
		for _, c := range v {
			if next := chainNext(c); next != nil {
				return next
			}
		}
	}
	return nil
}

func TestOverlappingSendKeysDoNotAllowAPlugin(t *testing.T) {
	pluginApprovalNow = func() time.Time { return time.Unix(1000, 0) }
	t.Cleanup(func() { pluginApprovalNow = time.Now })
	allowed := false
	pluginCaller = func(_ string, p map[string]any) error {
		allowed = allowed || p["allow"] == true
		return nil
	}
	t.Cleanup(func() { pluginCaller = callPluginMethod })
	prev := getInputHandler()
	SetInputHandler(func(msg tea.Msg, o *OS) (tea.Model, tea.Cmd) {
		k := msg.(tea.KeyPressMsg)
		return o, o.PluginApprovalKey(k.String(), k.Text)
	})
	t.Cleanup(func() { SetInputHandler(prev) })
	cfg := config.DefaultConfig()
	m := NewOS(OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	m.setPluginAsks([]session.PluginApprovalAsk{{ID: "p", Name: "P", Trust: "t", Grants: []string{"write"}}})
	pluginApprovalNow = func() time.Time { return time.Unix(1001, 0) }

	runInterleaved(t, m, "x", "x a l l o w enter")
	if m.pluginApproval.busy || allowed {
		t.Fatalf("keys from the second send-keys allowed the plugin (typed %q)", m.pluginApproval.typed)
	}
	if m.ProcessingRemoteKeys {
		t.Fatal("the mark stayed set after both chains ended")
	}
	// Both chains are done: the person's own keys allow it.
	for _, k := range []tea.KeyPressMsg{{Code: 'a', Text: "a"}, {Code: 'l', Text: "l"}, {Code: 'l', Text: "l"}, {Code: 'o', Text: "o"}, {Code: 'w', Text: "w"}, {Code: tea.KeyEnter}} {
		m.Update(k)
	}
	if !m.pluginApproval.busy {
		t.Fatalf("the person's allow did not answer (typed %q)", m.pluginApproval.typed)
	}
}

func TestOverlappingSendKeysDoNotAnswerAHeldApproval(t *testing.T) {
	m, r := approvalsOS(t, heldApproval("1", "r1", session.ApprovalOnce, session.ApprovalAlways, session.ApprovalDeny))
	var sent tea.Cmd
	prev := getInputHandler()
	SetInputHandler(func(msg tea.Msg, o *OS) (tea.Model, tea.Cmd) {
		switch msg.(tea.KeyPressMsg).String() {
		case "1":
			press(o, "1")
			if cmd := o.InboxNumber(1); cmd != nil {
				sent = cmd
			}
		case "enter":
			if o.InboxConfirmOpen() {
				if cmd := o.InboxConfirmKey("enter"); cmd != nil {
					sent = cmd
				}
			}
		}
		return o, nil
	})
	t.Cleanup(func() { SetInputHandler(prev) })

	// Three chains, so the count must survive two ends before the last.
	runInterleaved(t, m, "x", "x", "x x 1 enter")
	if sent != nil {
		sent()
		t.Fatalf("keys from the third send-keys answered a held approval: %v", r.replies())
	}
	if m.ProcessingRemoteKeys || m.remoteKeyChains != 0 {
		t.Fatalf("after every chain ended: mark %v, chains %d", m.ProcessingRemoteKeys, m.remoteKeyChains)
	}
	// A stray end does not take the count below zero, so the next chain is
	// still marked from its first key to its last.
	m.Update(RemoteKeysDoneMsg{})
	runInterleaved(t, m, "1 enter")
	if sent != nil {
		t.Fatal("a send-keys after a stray end answered a held approval")
	}
	m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if sent == nil {
		t.Fatalf("the person's 1 and enter did not answer: %q", lastNote(m))
	}
}
