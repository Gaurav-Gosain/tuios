package session

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Paste buffers: list-buffers, show-buffer, set-buffer, delete-buffer and
// paste-buffer, after tmux's commands of the same names.
//
// The daemon holds one store (internal/pastebuf), so every client and every
// session can share the buffers, as tmux's server does. A client adds a
// buffer for each yank it makes, and the person pastes one back with the
// prefix keys or the CLI. Nothing reaches disk: the buffers end with the
// daemon.
//
// Each buffer records the session it came from and, when a process in a pane
// set it, that pane. A buffer can hold whatever the person copied, a secret
// included, so a pane is held to its grants (pane_grants.go): reading needs
// read, and changing needs write. A pane without admin sees only the buffers
// of the sessions it may read (sessionInScope), and a buffer from no session,
// which the person set from outside every pane, is not one of them. A paste
// needs read and write, because the text it types into the caller's own pane
// is the text's way back to the caller. The paste is also a typing verb, held
// to the same target rules as send-text.

// ErrVerbNoBuffer is the code of a call naming a buffer there is not, or of a
// call on the newest buffer when there is none.
const ErrVerbNoBuffer = "no_buffer"

// bufferSampleRunes is how much of a buffer a listing shows.
const bufferSampleRunes = 60

// pasteBufferLimit is the count limit a daemon starts with: the default
// when the config leaves it at zero, and 0 only when the file turned the
// buffers off.
func (c *DaemonConfig) pasteBufferLimit() int {
	if c.PasteBuffersOff {
		return 0
	}
	if c.PasteBufferLimit <= 0 {
		return pastebuf.DefaultLimit
	}
	return c.PasteBufferLimit
}

// bufferStore is the daemon's store. A daemon made without NewDaemon, in a
// test, has none until the first call.
func (d *Daemon) bufferStore() *pastebuf.Store {
	d.buffersOnce.Do(func() {
		if d.buffers == nil {
			d.buffers = pastebuf.New(pastebuf.DefaultLimit, pastebuf.DefaultMaxBytes)
		}
	})
	return d.buffers
}

// bufferAccess says who the caller on cs is to the buffers: the owner a
// buffer it sets gets, and the filter of the buffers it may see. session is
// the session a caller outside every pane names, which a yank from a client
// does; a pane's own session is used for a pane whatever it names.
func (d *Daemon) bufferAccess(cs *connState, session string) (pastebuf.Owner, pastebuf.Filter) {
	if cs != nil && cs.viaLink {
		// The link policy already held the call (list to read, write to
		// change). The buffer is marked as the link's.
		return pastebuf.Owner{Pane: "link"}, nil
	}
	pa := d.paneAuthority(cs)
	if pa == nil {
		owner := pastebuf.Owner{}
		if session != "" {
			if s, _ := d.manager.ResolveSession(session); s != nil {
				owner.Session = s.ID
			}
		}
		return owner, nil
	}
	owner := pastebuf.Owner{Session: pa.sessionID, Pane: pa.window}
	if pa.grants.Has(GrantAdmin) {
		return owner, nil
	}
	own := pa.session
	if pa.sessionID != "" {
		own = d.sessionNameByID(pa.sessionID)
	}
	return owner, func(b pastebuf.Buffer) bool {
		name := d.sessionNameByID(b.Owner.Session)
		return name != "" && d.sessionInScope(own, name)
	}
}

// forSession narrows f to what the paste key takes for session: the
// person's own buffers, and the ones a pane of that session set. A buffer a
// pane of another session set is left out, so no pane can plant what the
// person pastes somewhere else.
func (d *Daemon) forSession(f pastebuf.Filter, session string) pastebuf.Filter {
	s, _ := d.manager.ResolveSession(session)
	id := ""
	if s != nil {
		id = s.ID
	}
	return func(b pastebuf.Buffer) bool {
		if f != nil && !f(b) {
			return false
		}
		return b.Owner.Pane == "" || (id != "" && b.Owner.Session == id)
	}
}

// bufferError maps a store error to the verb error for it.
func (d *Daemon) bufferError(verb string, err error) *verbError {
	switch {
	case errors.Is(err, pastebuf.ErrNotFound), errors.Is(err, pastebuf.ErrNone):
		return hintedVerbError(ErrVerbNoBuffer, verb+": "+err.Error(), &VerbHint{
			Verb:    "list-buffers",
			Command: "tuios list-buffers",
			Detail:  "A yank in copy mode adds a buffer, and so does set-buffer. list-buffers shows the names this caller may see.",
		})
	case errors.Is(err, pastebuf.ErrChanged):
		return hintedVerbError(ErrVerbNoBuffer, verb+": "+err.Error(), &VerbHint{
			Detail: "Nothing was deleted. Read the buffer again to see its new text.",
		})
	case errors.Is(err, pastebuf.ErrBadName):
		return invalidParam("name", verb+": "+err.Error())
	case errors.Is(err, pastebuf.ErrEmpty):
		return invalidParam("data", verb+": "+err.Error())
	case errors.Is(err, pastebuf.ErrTooLarge):
		_, maxBytes := d.bufferStore().Limits()
		return hintedVerbError(ErrVerbInvalidParams, verb+": "+err.Error(), &VerbHint{
			Param: "data",
			Detail: "Nothing was stored. All paste buffers together hold at most " + strconv.Itoa(maxBytes>>10) +
				" KiB, so one buffer can hold no more. Set max_kb under [paste_buffers] in config.toml to keep larger text.",
		})
	case errors.Is(err, pastebuf.ErrOff):
		return hintedVerbError(ErrVerbInvalidParams, verb+": "+err.Error(), &VerbHint{
			Detail: "Nothing was stored. Set limit under [paste_buffers] in config.toml to keep buffers.",
		})
	}
	return newVerbError(ErrVerbInternal, verb+": "+err.Error())
}

// bufferRow is one buffer in a listing.
func (d *Daemon) bufferRow(b pastebuf.Buffer) map[string]any {
	row := map[string]any{
		"name":      b.Name,
		"bytes":     len(b.Data),
		"created":   b.Created.UnixNano(),
		"automatic": b.Automatic,
		"sample":    pastebuf.Sample(b.Data, bufferSampleRunes),
		"session":   d.sessionNameByID(b.Owner.Session),
	}
	if b.Owner.Pane != "" {
		row["pane"] = shortWindowID(b.Owner.Pane)
	}
	return row
}

// verbListBuffers lists the paste buffers the caller may see, newest first.
func (d *Daemon) verbListBuffers(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		ForSession string `json:"for_session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	_, f := d.bufferAccess(cs, "")
	if p.ForSession != "" {
		f = d.forSession(f, p.ForSession)
	}
	store := d.bufferStore()
	list := store.List(f)
	rows := make([]map[string]any, 0, len(list))
	for _, b := range list {
		rows = append(rows, d.bufferRow(b))
	}
	limit, maxBytes := store.Limits()
	return map[string]any{
		"type":      "buffers",
		"buffers":   rows,
		"total":     len(rows),
		"bytes":     store.Bytes(),
		"limit":     limit,
		"max_bytes": maxBytes,
	}, nil
}

// verbShowBuffer returns one buffer's text.
func (d *Daemon) verbShowBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name       string `json:"name"`
		ForSession string `json:"for_session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	_, f := d.bufferAccess(cs, "")
	if p.ForSession != "" {
		f = d.forSession(f, p.ForSession)
	}
	b, err := d.bufferStore().Get(p.Name, f)
	if err != nil {
		return nil, d.bufferError("show-buffer", err)
	}
	row := d.bufferRow(b)
	row["type"] = "buffer"
	row["data"] = b.Data
	return row, nil
}

// verbSetBuffer stores text in a buffer.
func (d *Daemon) verbSetBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name    string `json:"name"`
		Data    string `json:"data"`
		Append  bool   `json:"append"`
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	owner, f := d.bufferAccess(cs, p.Session)
	b, err := d.bufferStore().Set(p.Name, p.Data, p.Append, owner, f)
	if err != nil {
		return nil, d.bufferError("set-buffer", err)
	}
	row := d.bufferRow(b)
	row["type"] = "buffer_set"
	return row, nil
}

// verbDeleteBuffer removes a buffer.
func (d *Daemon) verbDeleteBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name    string `json:"name"`
		Created int64  `json:"created"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	var created time.Time
	if p.Created != 0 {
		created = time.Unix(0, p.Created)
	}
	_, f := d.bufferAccess(cs, "")
	b, err := d.bufferStore().Delete(p.Name, created, f)
	if err != nil {
		return nil, d.bufferError("delete-buffer", err)
	}
	return map[string]any{"type": "buffer_deleted", "name": b.Name}, nil
}

// verbPasteBuffer types a buffer into a pane as a paste: sanitized as every
// paste is, and in the bracketed paste delimiters when the pane's program
// turned bracketed paste on. With delete it removes the buffer it pasted, and
// not a newer text set under the same name meanwhile.
func (d *Daemon) verbPasteBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Name    string `json:"name"`
		Delete  bool   `json:"delete"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	store := d.bufferStore()
	_, f := d.bufferAccess(cs, "")
	b, err := store.Get(p.Name, f)
	if err != nil {
		return nil, d.bufferError("paste-buffer", err)
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, rerr := d.resolvePTYForTarget(sess, p.Window)
	if rerr != nil {
		return nil, mapResolveErr(rerr, sess)
	}
	if verr := d.recheckTyping(cs, "paste-buffer", sess, p.Window); verr != nil {
		return nil, verr
	}
	text := vt.SanitizePaste(b.Data)
	bracketed := false
	if text != "" && pty.BracketedPasteOn() {
		text = bracketedPasteStart + text + bracketedPasteEnd
		bracketed = true
	}
	if _, err := pty.Write([]byte(text)); err != nil {
		return nil, ptyWriteError(err)
	}
	deleted := false
	if p.Delete {
		_, derr := store.Delete(b.Name, b.Created, f)
		deleted = derr == nil
	}
	return map[string]any{
		"type":      "buffer_pasted",
		"name":      b.Name,
		"bytes":     len(b.Data),
		"bracketed": bracketed,
		"deleted":   deleted,
	}, nil
}

// bufferVerbs are the paste buffer verbs, for the registry.
func bufferVerbs() map[string]verbEntry {
	nameParam := func(what string) verbParam {
		return verbParam{Name: "name", Type: "string", Description: "The buffer to " + what + ". Omit for the newest."}
	}
	rowReturns := []verbParam{
		{Name: "name", Type: "string", Description: "The buffer's name: bufferNNNN for one tuios named, or the name set-buffer gave it."},
		{Name: "bytes", Type: "int", Description: "How many bytes the buffer holds."},
		{Name: "created", Type: "int", Description: "Unix-nano time the text was last set."},
		{Name: "automatic", Type: "bool", Description: "True when tuios named the buffer."},
		{Name: "sample", Type: "string", Description: "The start of the text on one line, with control characters shown as escapes. Print it as it is."},
		{Name: "session", Type: "string", Description: "The session the text was copied or set in. Empty when the person set it from outside every pane."},
		{Name: "pane", Type: "string", Description: "The pane whose process set the buffer. Absent when the person set it, with a yank or from outside every pane."},
	}
	forSession := verbParam{Name: "for_session", Type: "string", Description: "Keep only the buffers the paste key takes in this session: the person's own, and the ones a pane of this session set."}
	return map[string]verbEntry{
		"list-buffers": {
			description: "List the paste buffers, newest first. A yank in copy mode adds one, and so does set-buffer. From a pane this needs the read grant, and a pane without admin sees only the buffers of the sessions it may read.",
			params:      []verbParam{forSession},
			returns: []verbParam{
				{Name: "buffers", Type: "[]object", Description: "One entry per buffer, newest first: name, bytes, created, automatic, sample, session, pane."},
				{Name: "total", Type: "int", Description: "How many buffers there are."},
				{Name: "bytes", Type: "int", Description: "How many bytes they hold together."},
				{Name: "limit", Type: "int", Description: "How many buffers the daemon keeps, from [paste_buffers] limit."},
				{Name: "max_bytes", Type: "int", Description: "How many bytes the buffers may hold together, from [paste_buffers] max_kb."},
			},
			examples: []string{`{"id":1,"verb":"list-buffers"}`},
			handler:  (*Daemon).verbListBuffers,
		},
		"show-buffer": {
			description: "Return the text of one paste buffer. From a pane this needs the read grant, and reaches only the buffers of the sessions the pane may read.",
			params:      []verbParam{nameParam("show"), forSession},
			returns: append(append([]verbParam{}, rowReturns...),
				verbParam{Name: "data", Type: "string", Description: "The whole text."}),
			examples: []string{
				`{"id":1,"verb":"show-buffer"}`,
				`{"id":1,"verb":"show-buffer","params":{"name":"buffer0003"}}`,
			},
			handler: (*Daemon).verbShowBuffer,
		},
		"set-buffer": {
			description: "Store text in a paste buffer and put it on top. With no name a new buffer is made, unless append is set. When the buffers pass the limit or the byte cap, the oldest go. From a pane this needs the write grant.",
			params: []verbParam{
				{Name: "data", Type: "string", Required: true, Description: "The text. It may not be empty or larger than the byte cap."},
				{Name: "name", Type: "string", Description: "The buffer to set: 1 to 64 printable characters. Omit for a new buffer, or for the newest with append."},
				{Name: "append", Type: "bool", Description: "Add the text to the end of the buffer instead of replacing it.", Default: "false"},
				{Name: "session", Type: "string", Description: "The session the text comes from, for a caller outside every pane such as a client's yank. A pane's own session is used for a pane."},
			},
			returns: rowReturns,
			examples: []string{
				`{"id":1,"verb":"set-buffer","params":{"data":"make test"}}`,
				`{"id":1,"verb":"set-buffer","params":{"name":"deploy","data":"kubectl rollout restart deploy/api"}}`,
			},
			handler: (*Daemon).verbSetBuffer,
		},
		"delete-buffer": {
			description: "Delete a paste buffer. From a pane this needs the write grant, and reaches only the buffers of the sessions the pane may read.",
			params: []verbParam{
				nameParam("delete"),
				{Name: "created", Type: "int", Description: "Delete only when the buffer's text is still the one set at this Unix-nano time, as show-buffer gave it."},
			},
			returns: []verbParam{
				{Name: "name", Type: "string", Description: "The buffer that was deleted."},
			},
			examples: []string{`{"id":1,"verb":"delete-buffer","params":{"name":"buffer0001"}}`},
			handler:  (*Daemon).verbDeleteBuffer,
		},
		"paste-buffer": {
			description: "Type a paste buffer into a window's PTY as a paste. Control characters other than tab, line feed and carriage return are removed, and the text is wrapped in the bracketed paste delimiters when the program in the pane turned bracketed paste on. From a pane this needs the read and write grants.",
			params: []verbParam{
				sessionParam,
				windowParam,
				nameParam("paste"),
				{Name: "delete", Type: "bool", Description: "Delete the buffer after the paste, unless its text was set again meanwhile.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "name", Type: "string", Description: "The buffer that was pasted."},
				{Name: "bytes", Type: "int", Description: "How many bytes the buffer holds."},
				{Name: "bracketed", Type: "bool", Description: "True when the paste went in the bracketed paste delimiters."},
				{Name: "deleted", Type: "bool", Description: "True when the buffer was deleted after the paste."},
			},
			examples: []string{
				`{"id":1,"verb":"paste-buffer","params":{"session":"work","window":"build"}}`,
				`{"id":1,"verb":"paste-buffer","params":{"session":"work","name":"deploy","delete":true}}`,
			},
			handler: (*Daemon).verbPasteBuffer,
		},
	}
}
