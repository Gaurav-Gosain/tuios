package session

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const goneHookWait = 10 * time.Second

type goneHookNotes struct {
	mu    sync.Mutex
	first map[string]time.Time
}

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
	if jsonx.Unmarshal(params, &p) != nil {
		return
	}
	reg := d.agentMatcher.registry
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
