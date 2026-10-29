package webshell

import (
	"strconv"
	"sync"
	"time"
)

// The record each pretend agent keeps of its run: the conversation as entries
// in the shape tuios's agent-transcript verb sends, the prompt it waits on,
// and the checkpoints of its turns. The tour's fleet (internal/learn) reads
// it to answer the agents view's verbs in place of a daemon, and answers a
// prompt or sends a reply by typing into the agent's pane, as a person would.

// AgentEntry is one entry of an agent's conversation. The JSON is what
// agent-transcript sends: see internal/transcriptview.
type AgentEntry struct {
	ID     string     `json:"id"`
	Role   string     `json:"role,omitempty"`
	Kind   string     `json:"kind"`
	Text   string     `json:"text,omitempty"`
	Tool   string     `json:"tool,omitempty"`
	Target string     `json:"target,omitempty"`
	Status string     `json:"status,omitempty"`
	ToolID string     `json:"tool_id,omitempty"`
	At     int64      `json:"at"`
	Diff   *AgentDiff `json:"diff,omitempty"`
}

// AgentDiff is the change an edit made to one file.
type AgentDiff struct {
	File    string      `json:"file"`
	Added   int         `json:"added"`
	Removed int         `json:"removed"`
	Hunks   []AgentHunk `json:"hunks"`
}

// AgentHunk is one run of changed lines with their context.
type AgentHunk struct {
	OldStart int             `json:"old_start"`
	NewStart int             `json:"new_start"`
	Lines    []AgentDiffLine `json:"lines"`
}

// AgentDiffLine is one line of a hunk. Op is " ", "+" or "-".
type AgentDiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// AgentPrompt is what a blocked agent asks.
type AgentPrompt struct {
	// ID changes with every prompt, so an answer to an older one is refused.
	ID string
	// Kind is "approval" or "question".
	Kind string
	// Summary is the one line an Inbox row shows.
	Summary string
	// Lines are the prompt as the pane shows it, the options left out.
	Lines []string
	// Options are the answers, numbered from 1 in the pane.
	Options []string
	// Since is when the agent started to wait, in unix nanoseconds.
	Since int64
}

// AgentCheckpoint is a checkpoint taken at the end of a turn that changed a
// file. The JSON is what list-checkpoints sends.
type AgentCheckpoint struct {
	N     int    `json:"n"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	State string `json:"state"`
	At    int64  `json:"at"`
}

// Agent is one pretend agent's record.
type Agent struct {
	// Window is the pane it runs in, Name the command that started it
	// ("claude"), and Harness the harness it reports as ("claude-code").
	Window, Name, Harness string

	pty *Pty

	mu          sync.Mutex
	entries     []AgentEntry
	prompt      *AgentPrompt
	state       string
	message     string
	resting     bool
	rests       int
	checkpoints []AgentCheckpoint
	rev         uint64
	ids         int
}

var (
	agentsMu sync.Mutex
	agentsBy map[string]*Agent
)

// FindAgent is the pretend agent running in a pane, nil when none is.
func FindAgent(window string) *Agent {
	ready()
	agentsMu.Lock()
	defer agentsMu.Unlock()
	return agentsBy[window]
}

// Agents lists the pretend agents running now.
func Agents() []*Agent {
	ready()
	agentsMu.Lock()
	defer agentsMu.Unlock()
	out := make([]*Agent, 0, len(agentsBy))
	for _, a := range agentsBy {
		out = append(out, a)
	}
	return out
}

// AgentView is a copy of an agent's record at one moment.
type AgentView struct {
	Entries []AgentEntry
	Prompt  *AgentPrompt
	State   string
	// Message is the note of the state, such as a turn's summary.
	Message     string
	Checkpoints []AgentCheckpoint
	// Resting says the agent waits at its input line for the next task, so
	// a reply typed now starts a turn at once. Rests counts the times it
	// came to rest, so one rest takes one reply.
	Resting bool
	Rests   int
	// Rev changes with every change to the record.
	Rev uint64
}

// View copies the record.
func (a *Agent) View() AgentView {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := AgentView{
		Entries:     append([]AgentEntry(nil), a.entries...),
		State:       a.state,
		Message:     a.message,
		Rests:       a.rests,
		Checkpoints: append([]AgentCheckpoint(nil), a.checkpoints...),
		Resting:     a.resting,
		Rev:         a.rev,
	}
	if a.prompt != nil {
		p := *a.prompt
		v.Prompt = &p
	}
	return v
}

// Type writes text into the agent's pane, as if the person typed it there.
func (a *Agent) Type(text string) bool {
	_, err := a.pty.Write([]byte(text))
	return err == nil
}

// EventAgentChanged says an agent's record changed: an entry, its prompt or
// its state. It is for tuios's fleet and not for the page. Data is empty.
const EventAgentChanged = "agent.changed"

// startAgent records a new agent in t's pane.
func startAgent(t *TTY, name, harness string) *Agent {
	a := &Agent{Window: t.env["TUIOS_WINDOW_ID"], Name: name, Harness: harness, pty: t.pty}
	if a.Window != "" {
		agentsMu.Lock()
		agentsBy[a.Window] = a
		agentsMu.Unlock()
	}
	return a
}

// end drops the record: the agent has exited.
func (a *Agent) end(t *TTY) {
	agentsMu.Lock()
	if agentsBy[a.Window] == a {
		delete(agentsBy, a.Window)
	}
	agentsMu.Unlock()
	t.Emit(EventAgentChanged, nil)
}

// change runs f on the record and tells the fleet.
func (a *Agent) change(t *TTY, f func()) {
	a.mu.Lock()
	f()
	a.rev++
	a.mu.Unlock()
	t.Emit(EventAgentChanged, nil)
}

// add appends an entry and returns its id.
func (a *Agent) add(t *TTY, e AgentEntry) string {
	a.change(t, func() {
		a.ids++
		e.ID = "e" + strconv.Itoa(a.ids)
		if e.At == 0 {
			e.At = time.Now().UnixMilli()
		}
		a.entries = append(a.entries, e)
	})
	return e.ID
}

// setStatus sets the status of the entries of a tool call.
func (a *Agent) setStatus(t *TTY, toolID, status string) {
	a.change(t, func() {
		for i := range a.entries {
			if a.entries[i].ToolID == toolID {
				a.entries[i].Status = status
			}
		}
	})
}

func initAgentsBy() {
	agentsBy = map[string]*Agent{}
}
