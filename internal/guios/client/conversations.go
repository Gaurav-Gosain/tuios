package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// The agents' ended conversations come from the daemon (list-conversations),
// which records one when its pane closes or its agent ends. Archiving is
// the app's own: the daemon has no archive, so the archived ids are kept in
// archived.json in the app's folder.

func (c *Client) conversations() ([]proto.Conversation, error) {
	raw, err := c.verb("list-conversations", nil)
	var old *OldDaemonError
	if errors.As(err, &old) {
		// A daemon from before the list keeps none: nothing to show.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var res struct {
		Conversations []struct {
			Harness string `json:"harness"`
			ID      string `json:"id"`
			Cwd     string `json:"cwd"`
			Title   string `json:"title"`
			Session string `json:"session"`
			EndedAt int64  `json:"ended_at"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	arch := archivedSet()
	out := make([]proto.Conversation, 0, len(res.Conversations))
	for _, cv := range res.Conversations {
		out = append(out, proto.Conversation{Agent: agentOf(cv.Harness), ID: cv.ID, Dir: cv.Cwd, Title: cv.Title, Name: cv.Session,
			Project: defaultProject, Started: cv.EndedAt, Updated: cv.EndedAt, Archived: arch[agentOf(cv.Harness)+"/"+cv.ID]})
	}
	return out, nil
}

// resumeConversation starts an ended conversation again in a new session
// of its own, in the conversation's folder.
func (c *Client) resumeConversation(p proto.ConversationParams) (proto.Session, error) {
	raw, err := c.verb("new-session", map[string]any{"window": false})
	if err != nil {
		return proto.Session{}, err
	}
	var ns struct {
		Name string `json:"session"`
		ID   string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &ns)
	if _, err := c.verb("resume-agent", map[string]any{"session": ns.Name, "conversation": p.ID, "harness": harnessOf(p.Agent)}); err != nil {
		_, _ = c.verb("kill-session", map[string]any{"session": ns.Name})
		return proto.Session{}, err
	}
	setArchived(p.Agent, p.ID, false)
	return c.sessionNamed(ns.Name, ns.ID)
}

var archivedMu sync.Mutex

func archivedPath() string { return filepath.Join(proto.Dir(), "archived.json") }

func archivedSet() map[string]bool {
	archivedMu.Lock()
	defer archivedMu.Unlock()
	m := map[string]bool{}
	if b, err := os.ReadFile(archivedPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func setArchived(agent, id string, on bool) {
	m := archivedSet()
	k := agent + "/" + id
	if m[k] == on {
		return
	}
	if on {
		m[k] = true
	} else {
		delete(m, k)
	}
	archivedMu.Lock()
	defer archivedMu.Unlock()
	if b, err := json.Marshal(m); err == nil {
		_ = os.MkdirAll(proto.Dir(), 0o700)
		_ = os.WriteFile(archivedPath(), b, 0o600)
	}
}

// agentOf is the app's name for a tuios harness id, and harnessOf the
// other way: the app names Claude Code "claude", tuios "claude-code".
func agentOf(harness string) string {
	if harness == "claude-code" {
		return "claude"
	}
	return harness
}

func harnessOf(agent string) string {
	if agent == "claude" {
		return "claude-code"
	}
	return agent
}
