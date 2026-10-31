package session

import (
	"encoding/json"
	"sync"
	"time"
)

// A shared server's hooks can name a pane that closed.
//
// A harness with shared_server runs every conversation's hooks in one server
// process, and that process keeps the TUIOS_PANE_ID of the pane that started it.
// When that pane closes, every hook names a window that is gone, so the pane that
// runs the conversation hears nothing and the reports fail where nobody looks. The
// hookless search still joins the transcript, and the screen rules still give the
// state, so the only thing missing is why. This puts the why in the Inbox. The
// notice closes on the next report of that harness that reaches a pane.
//
// The first report only starts a clock. A pane that has just closed can leave a
// hook or two in flight, and those end at once. A server that outlives its pane
// goes on reporting, so a second report goneHookWait later says which this is.

// goneHookWait is how long after the first report that names a closed pane a later
// one raises the notice.
const goneHookWait = 10 * time.Second

// goneHookNotes holds when each harness first reported for a closed pane.
type goneHookNotes struct {
	mu    sync.Mutex
	first map[string]time.Time
}

// noteSharedServerHook reads the outcome of one set-agent-state call. A call that
// nothing is waiting on costs one lock and returns, because only a failed window
// lookup or a harness already noted reads the parameters again.
func (d *Daemon) noteSharedServerHook(params json.RawMessage, verr *verbError) {
	gone := verr != nil && (verr.Code == ErrVerbWindowNotFound || verr.Code == ErrVerbSessionNotFound)
	d.goneHooks.mu.Lock()
	noted := len(d.goneHooks.first) > 0
	d.goneHooks.mu.Unlock()
	if !gone && (verr != nil || !noted) {
		return
	}
	var p struct {
		Harness        string `json:"harness"`
		AgentSessionID string `json:"agent_session_id"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	reg := d.agentMatcher.registry
	// Only a hook sends a session id, so this is not a person who mistyped -w.
	if p.AgentSessionID == "" || !reg.SharedServer(p.Harness) {
		return
	}
	label := p.Harness
	if m := reg.Lookup(p.Harness); m != nil && m.DisplayName != "" {
		label = m.DisplayName
	}
	name := label + " hooks"
	d.goneHooks.mu.Lock()
	defer d.goneHooks.mu.Unlock()
	if !gone {
		if _, ok := d.goneHooks.first[p.Harness]; ok {
			delete(d.goneHooks.first, p.Harness)
			d.attention.closeConfigNotice(name)
		}
		return
	}
	at, ok := d.goneHooks.first[p.Harness]
	if !ok {
		if d.goneHooks.first == nil {
			d.goneHooks.first = make(map[string]time.Time, 1)
		}
		d.goneHooks.first[p.Harness] = time.Now()
		return
	}
	if time.Since(at) >= goneHookWait {
		d.attention.noteConfigNotice(name, "The hooks name a pane that closed, so panes get no hook reports. Restart the "+
			label+" server from outside tuios.")
	}
}
