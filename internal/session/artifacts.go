package session

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Artifacts: what a pane has shown the person, kept per pane so the agents
// view can list it beside the agent and show it again. A TML surface the pane
// opened is one, open or closed; a screenshot the pane took is another.
//
// The log lives in daemon memory only. It is bounded per pane and in the
// number of panes it remembers, so an agent that opens surfaces in a loop
// costs a fixed amount. A daemon restart forgets it, as it forgets the
// surfaces themselves. A client reads it with artifact-list, so a client that
// attaches again sees the same list.

// The kinds of artifact.
const (
	ArtifactTML        = "tml"
	ArtifactScreenshot = "screenshot"
)

// Bounds of the log: the newest artifacts of each pane, and the panes it
// remembers, the pane that showed something least recently forgotten first.
const (
	artifactsPerPane = 20
	artifactPanes    = 128
)

// artifactRec is one artifact as the daemon keeps it.
type artifactRec struct {
	id, kind, title string
	at              time.Time
	window          string
	// For a TML surface: the surface id is the record's id. doc and state
	// are the tree and the state when it closed; an open surface is read
	// live from the TML host instead.
	target   string
	open     bool
	reason   string
	value    any
	hasValue bool
	doc      json.RawMessage
	state    json.RawMessage
	// For a screenshot: the file and its format.
	path, format string
}

// artifactLog is the daemon's log. The zero value is ready.
type artifactLog struct {
	mu       sync.Mutex
	byWindow map[string][]*artifactRec
	// touched is when each pane last showed something, for the pane bound.
	touched map[string]time.Time
}

// add records rec for its pane, newest last, and keeps the bounds.
func (l *artifactLog) add(rec *artifactRec) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byWindow == nil {
		l.byWindow, l.touched = map[string][]*artifactRec{}, map[string]time.Time{}
	}
	list := append(l.byWindow[rec.window], rec)
	if over := len(list) - artifactsPerPane; over > 0 {
		list = slices.Delete(list, 0, over)
	}
	l.byWindow[rec.window] = list
	l.touched[rec.window] = rec.at
	for len(l.byWindow) > artifactPanes {
		oldest, at := "", time.Time{}
		for w, t := range l.touched {
			if oldest == "" || t.Before(at) {
				oldest, at = w, t
			}
		}
		delete(l.byWindow, oldest)
		delete(l.touched, oldest)
	}
}

// update runs fn on the record with id in window's log, if it is still kept.
func (l *artifactLog) update(window, id string, fn func(*artifactRec)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.byWindow[window] {
		if r.id == id {
			fn(r)
			return
		}
	}
}

// recordTMLOpened logs a surface a pane of this daemon opened. A surface the
// person opened from outside the panes, or a linked machine, belongs to no
// agent and is not logged.
func (d *Daemon) recordTMLOpened(s *tmlSurface, target string) {
	if s.caller == "" || d.sessionHoldingWindow(s.caller) == nil {
		return
	}
	d.artifacts.add(&artifactRec{id: s.id, kind: ArtifactTML, title: s.title, at: time.Now(), window: s.caller,
		target: target, open: true})
}

// recordTMLClosed keeps a closed surface's last tree and state in its record,
// so the agents view can show what the person saw and answered. The caller
// holds the TML host's lock.
func (d *Daemon) recordTMLClosed(s *tmlSurface) {
	if s.caller == "" {
		return
	}
	state, _ := json.Marshal(s.store.State())
	d.artifacts.update(s.caller, s.id, func(r *artifactRec) {
		r.open, r.reason, r.value, r.hasValue = false, s.reason, s.value, s.hasValue
		r.title, r.doc, r.state = s.title, s.docJSON, state
	})
}

// recordScreenshot logs a screenshot a pane took.
func (d *Daemon) recordScreenshot(cs *connState, path, format string) {
	ref := d.callerPaneRef(cs)
	if ref == nil {
		return
	}
	d.artifacts.add(&artifactRec{id: "shot-" + newTMLSurfaceID()[4:], kind: ArtifactScreenshot, title: filepath.Base(path),
		at: time.Now(), window: ref.Window, path: path, format: format})
}

// verbArtifactList lists what one pane has shown, newest first.
func (d *Daemon) verbArtifactList(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Window string `json:"window"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Window == "" {
		return nil, invalidParam("window", "window is the id of the pane whose artifacts to list")
	}
	if pa := d.paneAuthority(cs); pa != nil && pa.window != p.Window && !pa.grants.Has(GrantAdmin) {
		if !pa.grants.Has(GrantRead) {
			return nil, grantForbidden("artifact-list", pa, "artifact-list for another pane needs the read grant")
		}
		if sess := d.sessionHoldingWindow(p.Window); sess != nil && !d.sessionInScope(pa.session, sess.Name()) {
			return nil, grantForbidden("artifact-list", pa, "the pane is in session "+echoName(sess.Name())+", which is not the pane's own session or in its fan group")
		}
	}
	d.artifacts.mu.Lock()
	recs := make([]artifactRec, 0, len(d.artifacts.byWindow[p.Window]))
	for _, r := range slices.Backward(d.artifacts.byWindow[p.Window]) {
		recs = append(recs, *r)
	}
	d.artifacts.mu.Unlock()
	list := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		item := map[string]any{"id": r.id, "kind": r.kind, "title": r.title, "at": r.at.UnixNano(), "window_id": r.window}
		switch r.kind {
		case ArtifactTML:
			item["surface"], item["target"], item["open"] = r.id, r.target, r.open
			doc, state := r.doc, r.state
			if r.open {
				// An open surface is read as it is now.
				d.tml.mu.Lock()
				if s := d.tml.surfaces[r.id]; s != nil && !s.closed {
					doc = s.docJSON
					state, _ = json.Marshal(s.store.State())
					item["title"] = s.title
				} else {
					item["open"] = false
				}
				d.tml.mu.Unlock()
			} else {
				item["reason"] = r.reason
				if r.hasValue {
					item["value"] = r.value
				}
			}
			if doc != nil {
				item["doc"], item["state"] = doc, state
			}
		case ArtifactScreenshot:
			item["path"], item["format"] = r.path, r.format
		}
		list = append(list, item)
	}
	return map[string]any{"type": "artifact_list", "window_id": p.Window, "artifacts": list}, nil
}
