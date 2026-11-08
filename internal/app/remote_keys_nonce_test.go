package app

import (
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The client signs a call with its attach nonce, human_nonce, and the
// daemon takes a signed call as the person's. The daemon cannot tell who
// pressed the key that made the call, so the client must not sign one that
// a key from send-keys or a tape's Press started. Each case below makes one
// signed call from a key handler. From the person's key it must sign, which
// shows the case reaches the call. From a RemoteKeyMsg, and from a tape's
// Press, nothing may go out signed.
//
// Negative control: with inboxNonce returning the nonce whatever the key's
// origin, every case fails for send-keys and for the tape. See
// e2e/tui/NEGATIVE_CONTROLS.md, "Signed calls from send-keys".

// signedCalls records the verbs called with a human_nonce.
type signedCalls struct{ verbs []string }

func (s *signedCalls) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	if n, _ := params["human_nonce"].(string); n != "" {
		s.verbs = append(s.verbs, verb)
	}
	return json.RawMessage(`{}`), nil
}

// runSignedCmd runs cmd and every command a batch in it holds.
func runSignedCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if b, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range b {
			runSignedCmd(c)
		}
	}
}

type signedPath struct {
	name  string
	setup func(t *testing.T, m *OS)
	act   func(m *OS) tea.Cmd
}

func signedPaths() []signedPath {
	draft := func(text string) *composer.Model {
		c := composer.New(composer.Options{})
		c.SetText(text)
		return c
	}
	return []signedPath{
		{name: "tml input", act: func(m *OS) tea.Cmd {
			return m.tmlInput(&tmlView{id: "s1"}, tmlInputReq{closing: true})
		}},
		{name: "tml place", setup: func(t *testing.T, m *OS) {
			m.tml.views = map[string]*tmlView{"s1": {id: "s1"}}
		}, act: func(m *OS) tea.Cmd {
			return m.tmlTogglePlacement(&terminal.Window{ID: "w1", Kind: session.WindowKindTML, URI: session.TMLURIPrefix + "s1"})
		}},
		{name: "dismiss", act: func(m *OS) tea.Cmd { return m.inboxDismissCmd("i1", false) }},
		{name: "seen dismiss", act: func(m *OS) tea.Cmd { return m.inboxDismissCmd("i1", true) }},
		{name: "mark", act: func(m *OS) tea.Cmd {
			return m.inboxMarkCmd(map[string]any{"id": "i1", "action": "snooze"}, "agent", "Snoozed")
		}},
		{name: "kanban stage", act: func(m *OS) tea.Cmd {
			return m.kanbanSetStage(session.TaskRow{ID: "t1", Session: "s", Stage: "todo"}, "doing")
		}},
		{name: "kanban add", setup: func(t *testing.T, m *OS) {
			m.Inbox.ck.kb.add = &kbNewCard{session: "s", stage: "todo", ed: draft("a card")}
		}, act: func(m *OS) tea.Cmd { return m.kanbanAddSend() }},
		{name: "thread page", act: func(m *OS) tea.Cmd {
			return m.threadPageCmd(&agentThread{key: "k", session: "s", window: "w"}, "")
		}},
		{name: "reply history add", act: func(m *OS) tea.Cmd {
			return m.replyHistoryAdd("s", "w", []composer.Part{{Text: "hi"}})
		}},
		{name: "spawn", setup: func(t *testing.T, m *OS) {
			m.Inbox.ck.spawn = &spawnSheet{
				task: draft("fix the build"), model: draft(""), branch: draft(""), base: draft(""),
				agents: []spawnAgent{{id: "sh", cmd: "sh", installed: true}},
				read:   true,
				card:   &spawnCard{session: "s", id: "t1"},
			}
			m.SessionName = "s"
		}, act: func(m *OS) tea.Cmd { return m.spawnStart() }},
	}
}

func TestSignedCallsRefuseKeysThePersonDidNotPress(t *testing.T) {
	for _, p := range signedPaths() {
		t.Run(p.name, func(t *testing.T) {
			for _, origin := range []string{"person", "send-keys", "tape"} {
				m := inboxOS(t, zeroSettle())
				rec := &signedCalls{}
				m.SetInboxVerbCaller(rec.call, func() string { return "nonce-1" })
				if p.setup != nil {
					p.setup(t, m)
				}
				var made tea.Cmd
				prev := getInputHandler()
				SetInputHandler(func(_ tea.Msg, o *OS) (tea.Model, tea.Cmd) {
					made = p.act(o)
					return o, nil
				})
				key := tea.KeyPressMsg{Code: 'x', Text: "x"}
				switch origin {
				case "person":
					m.Update(key)
				case "send-keys":
					m.Update(RemoteKeyMsg{Key: key})
				case "tape":
					m.pressingTapeKeys = true
					m.Update(key)
					m.pressingTapeKeys = false
				}
				SetInputHandler(prev)
				runSignedCmd(made)
				switch {
				case origin == "person" && len(rec.verbs) == 0:
					t.Fatalf("ASSERTION: the person's key made no signed call; the case does not reach it")
				case origin != "person" && len(rec.verbs) != 0:
					t.Errorf("a key from %s made signed calls %v", origin, rec.verbs)
				}
			}
		})
	}
}
