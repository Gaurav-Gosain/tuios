package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// subscribeEvent is the part of a stream event that subscribe reads: the
// fields --state filters on and the fields a text line shows. Every field is
// optional on the wire, so an event of a type this build does not know still
// decodes and prints by its type.
type subscribeEvent struct {
	Type       string `json:"type"`
	Host       string `json:"host"`
	Session    string `json:"session"`
	Window     string `json:"window"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	ClientID   string `json:"client_id"`
	PID        int    `json:"pid"`
	Attached   *bool  `json:"attached"`
	Bytes      int    `json:"bytes"`
	Mode       string `json:"mode"`
	Enabled    bool   `json:"enabled"`
	State      string `json:"state"`
	Workspace  int    `json:"workspace"`
	Dropped    uint64 `json:"dropped"`
	Reason     string `json:"reason"`
	Time       int64  `json:"time"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	Cmdline    string `json:"cmdline"`
	ExitCode   *int   `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Attention  *struct {
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Summary string `json:"summary"`
		Closed  string `json:"closed"`
	} `json:"attention"`
	Entry *struct {
		Kind   string `json:"kind"`
		Tool   string `json:"tool"`
		Target string `json:"target"`
		Text   string `json:"text"`
	} `json:"entry"`
	Transfer *struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		State string `json:"state"`
		Size  int64  `json:"size"`
		Done  int64  `json:"done"`
		Error string `json:"error"`
	} `json:"transfer"`
	File *struct {
		ID    string `json:"id"`
		Rel   string `json:"rel"`
		State string `json:"state"`
		Error string `json:"error"`
	} `json:"file"`
	TML *struct {
		Surface string `json:"surface"`
		Name    string `json:"name"`
		Value   any    `json:"value"`
		Reason  string `json:"reason"`
	} `json:"tml"`
	Task *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Stage string `json:"stage"`
	} `json:"task"`
}

// paneKey names a pane across machines: a window id is unique only on the
// daemon that made it.
func paneKey(host, window string) string { return host + "\x00" + window }

// textPrinter turns stream events into the lines of subscribe --format text.
// It keeps each pane's name and last agent state, so a line can say where an
// agent came from (working -> needs_input) although the event carries only
// the new state.
type textPrinter struct {
	// host is the machine the stream comes from, empty for this one. An
	// event relayed from a linked host names its own.
	host   string
	names  map[string]string
	states map[string]string
	// lookup reads one session's panes again, for the message that came with
	// a state and for a name the stream has not shown yet. It is nil when no
	// second connection can be made, and the line then goes without them.
	lookup func(sessionName string) []agentRow
}

func newTextPrinter() *textPrinter {
	return &textPrinter{names: map[string]string{}, states: map[string]string{}}
}

// seed records the panes list-agents returned before the stream started.
func (p *textPrinter) seed(raw json.RawMessage) {
	var res struct {
		Agents []agentRow `json:"agents"`
	}
	if jsonx.Unmarshal(raw, &res) != nil {
		return
	}
	for _, a := range res.Agents {
		p.notePane("", a)
	}
}

func (p *textPrinter) notePane(host string, a agentRow) {
	key := paneKey(host, a.WindowID)
	if name := plainLine(a.Name); name != "" {
		p.names[key] = name
	}
	if a.State != "" {
		p.states[key] = a.State
	}
}

// note keeps what an event says about its pane, after its line is made. A
// filtered-out agent-state event goes through here too, so the next line
// that is printed has the right previous state.
func (p *textPrinter) note(ev *subscribeEvent) {
	if ev.Window == "" {
		return
	}
	key := paneKey(ev.Host, ev.Window)
	switch ev.Type {
	case session.EventWindowCreated, session.EventWindowRetitled:
		if name := plainLine(ev.Title); name != "" {
			p.names[key] = name
		}
	case session.EventAgentState:
		p.states[key] = ev.State
	}
}

// where names the session and pane an event is about: "work api", or
// "laptop:work api" for an event of a linked host.
func (p *textPrinter) where(ev *subscribeEvent) string {
	var parts []string
	sess := plainLine(ev.Session)
	host := ev.Host
	if host == "" {
		host = p.host
	}
	if host != "" && ev.Type != session.EventHostChanged {
		sess = plainLine(host) + ":" + sess
	}
	if sess != "" {
		parts = append(parts, sess)
	}
	if ev.Window != "" {
		name := p.names[paneKey(ev.Host, ev.Window)]
		if name == "" && ev.Type == session.EventWindowCreated {
			name = plainLine(ev.Title)
		}
		if name == "" && ev.Attention != nil {
			name = plainLine(ev.Attention.Name)
		}
		if name == "" {
			name = shortWindowID(plainLine(ev.Window))
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, " ")
}

// line is the one text line for an event: local time, where, what.
func (p *textPrinter) line(ev *subscribeEvent) string {
	when := time.Now()
	if ev.Time > 0 {
		when = time.Unix(0, ev.Time)
	}
	where, what := p.where(ev), p.what(ev)
	if where == "" {
		where = "-"
	}
	return when.Local().Format("15:04:05") + "  " + where + "  " + what
}

// what says what happened, in a few words and with the event's free text.
func (p *textPrinter) what(ev *subscribeEvent) string {
	switch ev.Type {
	case session.EventAgentState:
		return p.agentState(ev)
	case session.EventWindowCreated:
		return "window created"
	case session.EventWindowClosed:
		return "window closed"
	case session.EventWindowExit:
		return "shell exited"
	case session.EventWindowRetitled:
		if ev.Title == "" {
			return "name cleared"
		}
		return "renamed to " + plainLine(ev.Title)
	case session.EventWindowFocused:
		return "focused"
	case session.EventWindowMoved:
		return "moved to workspace " + strconv.Itoa(ev.Workspace)
	case session.EventWindowMinimized:
		return "minimized"
	case session.EventWindowRestored:
		return "restored"
	case session.EventWorkspaceSwitched:
		return "switched to workspace " + strconv.Itoa(ev.Workspace)
	case session.EventWorkspaceRenamed:
		if ev.Title == "" {
			return "workspace " + strconv.Itoa(ev.Workspace) + " name cleared"
		}
		return "workspace " + strconv.Itoa(ev.Workspace) + " renamed to " + plainLine(ev.Title)
	case session.EventLayoutChanged:
		return "layout changed on workspace " + strconv.Itoa(ev.Workspace)
	case session.EventAgentSeen:
		if ev.Action == "unread" {
			return "turn marked unread"
		}
		return "turn seen"
	case session.EventAgentMessage:
		if ev.Window == "" {
			return "new session notice"
		}
		return "new message"
	case session.EventOutput:
		return "output, " + strconv.Itoa(ev.Bytes) + " bytes"
	case session.EventBell:
		return "bell"
	case session.EventNotification:
		return joinText("notification:", ev.Title, ev.Body)
	case session.EventModeChanged:
		if ev.Enabled {
			return "mode " + plainLine(ev.Mode) + " on"
		}
		return "mode " + plainLine(ev.Mode) + " off"
	case session.EventSessionCreated:
		return "session created"
	case session.EventSessionClosed:
		return "session closed"
	case session.EventClientSessionChanged:
		verb := "left"
		if ev.Attached != nil && *ev.Attached {
			verb = "attached"
		}
		return fmt.Sprintf("client %s (pid %d) %s", shortWindowID(plainLine(ev.ClientID)), ev.PID, verb)
	case session.EventGap:
		return fmt.Sprintf("gap: %d events missed (%s). Read the current state again", ev.Dropped, orNone(plainLine(ev.Reason)))
	case session.EventAttention:
		return p.attention(ev)
	case session.EventHostChanged:
		return "host " + plainLine(ev.Host) + " " + orNone(plainLine(ev.Status))
	case session.EventPrompt:
		return "prompt"
	case session.EventCommandStarted:
		return joinText("command started:", ev.Cmdline)
	case session.EventCommandFinished:
		status := "no exit status"
		if ev.ExitCode != nil {
			status = "exit " + strconv.Itoa(*ev.ExitCode)
		}
		took := (time.Duration(ev.DurationMS) * time.Millisecond).Round(100 * time.Millisecond)
		return joinText(fmt.Sprintf("command finished, %s, %s:", status, took), ev.Cmdline)
	case session.EventAgentActivity:
		if ev.Entry == nil {
			return "activity"
		}
		return joinText("activity "+plainLine(ev.Entry.Kind), ev.Entry.Tool, ev.Entry.Target, ev.Entry.Text)
	case session.EventTranscript:
		return "transcript changed"
	case session.EventDaemonUpgraded:
		return joinText("daemon upgraded from "+orNone(plainLine(ev.Title))+" to "+orNone(plainLine(ev.Body))+",", ev.State)
	case session.EventTransfer, session.EventTransferProgress:
		if ev.Transfer == nil {
			return joinText("transfer", ev.Action)
		}
		tr := ev.Transfer
		head := "transfer " + plainLine(tr.ID) + " " + plainLine(ev.Action)
		if ev.Type == session.EventTransferProgress {
			return joinText(head, fmt.Sprintf("%d of %d bytes", tr.Done, tr.Size), tr.Name)
		}
		return joinText(head, tr.State, tr.Name, tr.Error)
	case session.EventTransferFile:
		if ev.File == nil {
			return joinText("transfer file", ev.Action)
		}
		return joinText("transfer "+plainLine(ev.File.ID)+" file", ev.File.Rel, ev.File.State, ev.File.Error)
	case session.EventTML:
		if ev.TML == nil {
			return "tml event"
		}
		value := ""
		if ev.TML.Value != nil {
			if b, err := jsonx.Marshal(ev.TML.Value); err == nil {
				value = string(b)
			}
		}
		return joinText("tml "+plainLine(ev.TML.Surface)+" "+plainLine(ev.TML.Name), value, ev.TML.Reason)
	case session.EventTaskClaimed, session.EventTaskFinished, session.EventTaskChanged:
		verb := strings.TrimPrefix(ev.Type, "task-")
		if ev.Type == session.EventTaskChanged && ev.Action != "" {
			verb = ev.Action
		}
		if ev.Task == nil {
			return joinText("task "+plainLine(verb), ev.Reason)
		}
		return joinText("task "+plainLine(ev.Task.ID)+" "+plainLine(verb)+":", ev.Task.Title, ev.Reason)
	default:
		// A type this build does not know: its name and the fields most
		// types carry.
		return joinText(plainLine(ev.Type), ev.Action, ev.State, ev.Status, ev.Title, ev.Body, ev.Reason)
	}
}

// agentState is the agent-state line: the move from the previous state when
// it is known, then the message the agent gave with the state.
func (p *textPrinter) agentState(ev *subscribeEvent) string {
	key := paneKey(ev.Host, ev.Window)
	// Read before the lookup, which records the state the pane has now.
	prev := p.states[key]
	message := ""
	if p.lookup != nil && ev.Host == "" && ev.Session != "" {
		for _, a := range p.lookup(ev.Session) {
			if a.WindowID != ev.Window {
				continue
			}
			p.notePane("", a)
			// The pane can move on before the lookup: a message that came
			// with a later state is not this state's message.
			if a.State == ev.State {
				message = a.Message
			}
		}
	}
	move := plainLine(ev.State)
	if prev != "" && prev != ev.State {
		move = plainLine(prev) + " -> " + move
	}
	if ev.Reason != "" {
		move += " (" + plainLine(ev.Reason) + ")"
	}
	if message = strings.TrimSpace(plainLine(message)); message != "" {
		return move + "  " + message
	}
	return move
}

func (p *textPrinter) attention(ev *subscribeEvent) string {
	if ev.Attention == nil {
		return joinText("inbox", ev.Action)
	}
	a := ev.Attention
	head := "inbox " + plainLine(ev.Action) + " " + plainLine(a.Kind)
	if a.Closed != "" {
		head += " (" + plainLine(a.Closed) + ")"
	}
	return joinText(head, a.Summary)
}

// joinText joins the non-empty parts with a space, each on one line and free
// of control characters.
func joinText(head string, parts ...string) string {
	out := []string{head}
	for _, part := range parts {
		if s := strings.TrimSpace(plainLine(part)); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}
