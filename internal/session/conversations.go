package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Ended conversations.
//
// A pane's agent conversation id (agent_session_id) lives on its window, so
// it goes when the window closes, and resume-agent can only bring back a
// conversation whose pane is still open. The daemon keeps a short list of the
// conversations that ended instead: a window that closed with one, or whose
// agent left it (the same two changes that release a task card, task.go).
// list-conversations reads it, forget-conversation drops one, and resume-agent
// with conversation starts one again in a new window, by the same [resume]
// template and rules as a pane's own.
//
// The list keeps maxConversations, newest first, one entry per harness and
// id. It is saved in the state folder beside the Inbox, and read the first
// time it is used, so a daemon that never lists it never reads it. It holds
// the window title and folder, never what the person typed: replies are kept
// in memory only (reply_history.go).

// maxConversations bounds the list.
const maxConversations = 200

// Conversation is one ended agent conversation.
type Conversation struct {
	Harness string `json:"harness"`
	ID      string `json:"id"`
	Cwd     string `json:"cwd,omitempty"`
	Title   string `json:"title,omitempty"`
	Session string `json:"session,omitempty"`
	// Flags are the kept flags of the agent's command line, which the resume
	// command carries (resumeFlagsOf).
	Flags []string `json:"flags,omitempty"`
	// EndedAt is when it ended, in unix milliseconds.
	EndedAt int64 `json:"ended_at"`
}

// eventConversationEnded carries one ended conversation from the state change
// to the daemon's sink. It is internal: nothing is published and no hook fires.
const eventConversationEnded = "conversation-ended"

// conversationLog is the list. The zero value is ready.
type conversationLog struct {
	mu     sync.Mutex
	loaded bool
	list   []Conversation
}

func conversationsPath() string {
	return filepath.Join(getResurrectionDir(), "conversations", "ended.json")
}

// endedConversations returns an event for each conversation the change from
// before ended: its window closed, other than by a move to another session,
// or its agent left. Called with the state lock held.
func endedConversations(before lifecycleSnapshot, events []SessionEvent, moving, session string) []SessionEvent {
	var out []SessionEvent
	for _, ev := range events {
		if ev.Window == moving || !(ev.Type == EventWindowClosed || ev.Type == EventAgentState && ev.State == AgentStateNone.Name()) {
			continue
		}
		i, ok := before.index[ev.Window]
		if !ok || before.windows[i].agentSession == "" {
			continue
		}
		w := &before.windows[i]
		out = append(out, SessionEvent{Type: eventConversationEnded, Window: w.id, conversation: &Conversation{
			Harness: w.convHarness, ID: w.agentSession, Cwd: w.cwd, Title: w.displayTitle(),
			Session: session, Flags: w.convFlags, EndedAt: time.Now().UnixMilli(),
		}})
	}
	return out
}

// loadLocked reads the saved list the first time. mu is held.
func (l *conversationLog) loadLocked() {
	if l.loaded {
		return
	}
	l.loaded = true
	if data, err := os.ReadFile(conversationsPath()); err == nil {
		_ = jsonx.Unmarshal(data, &l.list)
	}
}

// saveLocked writes the list. mu is held.
func (l *conversationLog) saveLocked() {
	data, err := jsonx.Marshal(l.list)
	if err != nil {
		return
	}
	if err := atomicfile.WritePrivate(conversationsPath(), data, false); err != nil {
		LogError("Failed to save the ended conversations: %v", err)
	}
}

// add puts c first, replacing an older entry for the same conversation.
func (l *conversationLog) add(c Conversation) {
	if c.Harness == "" || c.ID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked()
	l.dropLocked(c.Harness, c.ID)
	l.list = append(l.list, Conversation{})
	copy(l.list[1:], l.list)
	l.list[0] = c
	l.list = l.list[:min(len(l.list), maxConversations)]
	l.saveLocked()
}

// dropLocked removes every entry for id, of harness when it is not empty,
// and reports whether there was one. A plain loop, not slices.DeleteFunc: a
// generic instantiation on Conversation costs the binary several KB. mu is
// held.
func (l *conversationLog) dropLocked(harness, id string) bool {
	kept := l.list[:0]
	for _, o := range l.list {
		if o.ID != id || harness != "" && o.Harness != harness {
			kept = append(kept, o)
		}
	}
	clear(l.list[len(kept):])
	gone := len(kept) != len(l.list)
	l.list = kept
	return gone
}

// forget drops the conversation, and reports whether the list held it.
func (l *conversationLog) forget(harness, id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked()
	if !l.dropLocked(harness, id) {
		return false
	}
	l.saveLocked()
	return true
}

// find returns the newest entry for id, of harness when it is not empty.
func (l *conversationLog) find(harness, id string) (Conversation, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked()
	for _, c := range l.list {
		if c.ID == id && (harness == "" || c.Harness == harness) {
			return c, true
		}
	}
	return Conversation{}, false
}

// verbListConversations answers list-conversations. A conversation an agent
// runs in a pane again is live, and is left out.
func (d *Daemon) verbListConversations(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Harness string `json:"harness"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	live := map[string]bool{}
	for _, s := range d.manager.AllSessions() {
		for _, w := range s.GetState().Windows {
			if w.AgentSessionID != "" && w.AgentHarness != "" {
				live[resumeHarnessOf(w)+"\x00"+w.AgentSessionID] = true
			}
		}
	}
	d.conversations.mu.Lock()
	d.conversations.loadLocked()
	out := make([]Conversation, 0, len(d.conversations.list))
	for _, c := range d.conversations.list {
		if (p.Harness == "" || c.Harness == p.Harness) && !live[c.Harness+"\x00"+c.ID] {
			out = append(out, c)
		}
	}
	d.conversations.mu.Unlock()
	return map[string]any{"type": "conversation_list", "conversations": out}, nil
}

// verbForgetConversation answers forget-conversation.
func (d *Daemon) verbForgetConversation(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Harness string `json:"harness"`
		ID      string `json:"id"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.ID == "" {
		return nil, invalidParam("id", "forget-conversation needs id: a conversation id from list-conversations.")
	}
	return map[string]any{"type": "conversation_forgotten", "id": p.ID, "forgotten": d.conversations.forget(p.Harness, p.ID)}, nil
}
