package client

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The agent cockpit (cmd/guios/cockpit*.go) reads the daemon's agents, its
// Inbox and an agent's conversation, and answers for the person. It does so
// on a connection of its own, so a slow transcript read never holds up a
// pane's split or close on the control connection, and that connection
// holds the person's presence (attach-presence): the nonce that respond,
// reply-approval, agent-transcript and the other person's verbs take. The
// connection opens the first time the cockpit asks for something, so an app
// that never opens the cockpit opens none.

// Agent is one row of list-agents.
type Agent struct {
	Window         string         `json:"window_id"`
	Session        string         `json:"session"`
	Name           string         `json:"name"`
	Harness        string         `json:"harness_id"`
	State          string         `json:"state"`
	StateAt        int64          `json:"agent_state_at"` // unix nanoseconds
	Message        string         `json:"message"`
	Reason         string         `json:"reason"`
	Cwd            string         `json:"cwd"`
	Source         string         `json:"source"`
	Confidence     string         `json:"confidence"`
	NeedsYou       bool           `json:"needs_you"`
	FinishedUnread bool           `json:"finished_unread"`
	Settled        bool           `json:"settled"`
	Unread         int            `json:"unread"`
	Queued         int            `json:"queued"`
	Subagents      int            `json:"subagents"`
	Workspace      int            `json:"workspace"`
	Group          string         `json:"group"`
	Host           string         `json:"host"`
	Meta           map[string]any `json:"meta"`
	SubagentList   []struct {
		Type  string `json:"type"`
		Label string `json:"label"`
	} `json:"subagent_list"`
}

// Key names the agent across the daemon and its hosts.
func (a Agent) Key() string { return a.Host + "\x00" + a.Session + "\x00" + a.Window }

// cockpitConn is the cockpit's connection and the presence it holds.
type cockpitConn struct {
	mu    sync.Mutex
	vc    *session.VerbClient
	nonce string
}

// ErrNotHuman is returned when the daemon would not take this app as the
// person: it runs inside a pane, for one.
var ErrNotHuman = errors.New("the daemon does not take this app as the person")

// conn returns the cockpit's connection, opening it and its presence.
func (c *Client) cockpit() (*cockpitConn, error) {
	c.mu.Lock()
	if c.ck == nil {
		c.ck = &cockpitConn{}
	}
	ck := c.ck
	c.mu.Unlock()
	ck.mu.Lock()
	defer ck.mu.Unlock()
	if ck.vc != nil {
		return ck, nil
	}
	vc, err := c.dial()
	if err != nil {
		return nil, err
	}
	raw, err := vc.Call("attach-presence", nil)
	if err != nil {
		vc.Close()
		return nil, err
	}
	var p struct {
		Nonce string `json:"human_nonce"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Nonce == "" {
		vc.Close()
		return nil, ErrNotHuman
	}
	ck.vc, ck.nonce = vc, p.Nonce
	return ck, nil
}

// Verb calls a daemon verb on the cockpit's connection. With human set, the
// call carries the person's nonce. params, when set, is a map the nonce is
// added to.
func (c *Client) Verb(name string, params map[string]any, human bool) (json.RawMessage, error) {
	ck, err := c.cockpit()
	if err != nil {
		return nil, err
	}
	ck.mu.Lock()
	vc, nonce := ck.vc, ck.nonce
	ck.mu.Unlock()
	if human {
		if params == nil {
			params = map[string]any{}
		}
		params["human_nonce"] = nonce
	}
	raw, err := vc.Call(name, params)
	if errors.Is(err, session.ErrVerbClientBroken) || err != nil && !isVerbError(err) {
		// The connection broke: the next call opens another, with a new
		// presence.
		ck.mu.Lock()
		if ck.vc == vc {
			ck.vc.Close()
			ck.vc, ck.nonce = nil, ""
		}
		ck.mu.Unlock()
	}
	return raw, err
}

func isVerbError(err error) bool {
	var ve *session.VerbCallError
	return errors.As(err, &ve)
}

// VerbInto is Verb, decoding the result into out.
func (c *Client) VerbInto(name string, params map[string]any, human bool, out any) error {
	raw, err := c.Verb(name, params, human)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Agents lists every agent of every session, and the Inbox.
func (c *Client) Agents() ([]Agent, []session.AttentionItem, error) {
	var al struct {
		Agents []Agent `json:"agents"`
	}
	if err := c.VerbInto("list-agents", map[string]any{"all_sessions": true}, false, &al); err != nil {
		return nil, nil, err
	}
	var il struct {
		Items []session.AttentionItem `json:"items"`
	}
	if err := c.VerbInto("list-attention", map[string]any{"include_snoozed": true}, false, &il); err != nil {
		return al.Agents, nil, err
	}
	return al.Agents, il.Items, nil
}

// closeCockpit ends the cockpit's connection, and its presence.
func (c *Client) closeCockpit() {
	c.mu.Lock()
	ck := c.ck
	c.mu.Unlock()
	if ck == nil {
		return
	}
	ck.mu.Lock()
	if ck.vc != nil {
		ck.vc.Close()
		ck.vc, ck.nonce = nil, ""
	}
	ck.mu.Unlock()
}

// LongVerb calls a verb on a connection of its own, which it closes after,
// waiting up to timeout for the answer. It is for the verbs that take long
// (start-agent waits for the agent's prompt, fan may clone, judge-fan waits
// for its judge), so that they never hold up the cockpit's connection. It
// carries no presence: these verbs take the caller as the person when it is
// not a pane.
func (c *Client) LongVerb(name string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	vc, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer vc.Close()
	return vc.CallWithTimeout(name, params, timeout)
}

// HumanNonce is the person's nonce of the cockpit's presence, for a verb
// that LongVerb calls on a connection of its own: the daemon takes it from
// any connection of this app.
func (c *Client) HumanNonce() (string, error) {
	ck, err := c.cockpit()
	if err != nil {
		return "", err
	}
	ck.mu.Lock()
	defer ck.mu.Unlock()
	return ck.nonce, nil
}
