package guibridge

import (
	"bytes"
	"encoding/json"
	"log"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Fleet is every session on the daemon and every pane's agent state, for
// the renderer's sidebar. It is sent as a "fleet" event whenever it changes.
type Fleet struct {
	// Sessions are the rows of the list-sessions verb.
	Sessions json.RawMessage `json:"sessions"`
	// Agents are the rows of list-agents with all and all_sessions set, the
	// same rows `tuios list-agents --all --all-sessions --json` prints.
	Agents json.RawMessage `json:"agents"`
	// Saved are the sessions saved on disk, live or not, as `tuios
	// resurrect --json` lists them.
	Saved []SavedSession `json:"saved"`
}

// fleetEvents are the daemon events that can change what a fleet shows.
var fleetEvents = []string{
	session.EventAgentState, session.EventAgentMessage,
	session.EventWindowCreated, session.EventWindowClosed, session.EventWindowRetitled, session.EventWindowMoved,
	session.EventWorkspaceSwitched, session.EventSessionCreated, session.EventSessionClosed,
}

const (
	// fleetSettle lets a burst of events land before the fleet is read.
	fleetSettle = 120 * time.Millisecond
	// fleetGap is the least time between two reads, so an agent that
	// retitles its window many times a second costs a few reads at most.
	fleetGap = 400 * time.Millisecond
	// fleetRefresh reads the fleet even with no event, for the states that
	// go stale with time and send none.
	fleetRefresh = 30 * time.Second
)

// watchFleet keeps the renderer's fleet current until stop closes. It reads
// the fleet over its own daemon connection when the daemon's event stream
// says something changed, so the renderer never has to start tuios to poll.
// A lost connection is dialled again after a pause.
func watchFleet(stop <-chan struct{}, out *frameWriter, version string) {
	var last []byte
	for {
		if err := fleetOnce(stop, out, version, &last); err != nil {
			log.Printf("gui-bridge: fleet: %v", err)
		}
		select {
		case <-stop:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func fleetOnce(stop <-chan struct{}, out *frameWriter, version string, last *[]byte) error {
	query, err := session.DialVerbClientAs(version)
	if err != nil {
		return err
	}
	defer func() { _ = query.Close() }()
	events, err := session.DialVerbClientAs(version)
	if err != nil {
		return err
	}
	defer func() { _ = events.Close() }()
	if _, err := events.Call("subscribe", map[string]any{"types": fleetEvents}); err != nil {
		return err
	}

	send := func() error {
		b, err := readFleet(query)
		if err != nil {
			return err
		}
		if bytes.Equal(b, *last) {
			return nil
		}
		*last = b
		out.Frame(KindJSON, b)
		return nil
	}
	if err := send(); err != nil {
		return err
	}

	lines := make(chan error, 1)
	go func() {
		for {
			_, err := events.ReadEventLine(0)
			select {
			case lines <- err:
			case <-stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	// Closing the event connection ends the reader when the bridge stops.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			_ = events.Close()
		case <-done:
		}
	}()

	refresh := time.NewTicker(fleetRefresh)
	defer refresh.Stop()
	var settle <-chan time.Time
	lastRead := time.Now()
	for {
		select {
		case <-stop:
			return nil
		case err := <-lines:
			if err != nil {
				return err
			}
			if settle == nil {
				wait := max(fleetSettle, fleetGap-time.Since(lastRead))
				settle = time.After(wait)
			}
		case <-settle:
			settle = nil
			lastRead = time.Now()
			if err := send(); err != nil {
				return err
			}
		case <-refresh.C:
			lastRead = time.Now()
			if err := send(); err != nil {
				return err
			}
		}
	}
}

// readFleet reads the sessions and agents and encodes them as a fleet
// event.
func readFleet(c *session.VerbClient) ([]byte, error) {
	rawSessions, err := c.Call("list-sessions", nil)
	if err != nil {
		return nil, err
	}
	rawAgents, err := c.Call("list-agents", map[string]any{"all": true, "all_sessions": true})
	if err != nil {
		return nil, err
	}
	var s struct {
		Sessions json.RawMessage `json:"sessions"`
	}
	var a struct {
		Agents json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(rawSessions, &s); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawAgents, &a); err != nil {
		return nil, err
	}
	return json.Marshal(Event{Type: "fleet", Fleet: &Fleet{Sessions: orEmpty(s.Sessions), Agents: orEmpty(a.Agents), Saved: savedSessions()}})
}

// orEmpty turns a missing or null list into an empty one.
func orEmpty(b json.RawMessage) json.RawMessage {
	if len(b) == 0 || string(b) == "null" {
		return json.RawMessage("[]")
	}
	return b
}
