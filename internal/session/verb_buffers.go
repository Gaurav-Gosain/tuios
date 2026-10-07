package session

import (
	"encoding/json"
	"errors"

	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Paste buffers: list-buffers, show-buffer, set-buffer, delete-buffer and
// paste-buffer, after tmux's commands of the same names.
//
// The daemon holds one store (internal/pastebuf), so every client and every
// session sees the same buffers, as tmux's server does. A client adds a
// buffer for each yank it makes, and the person pastes one back with the
// prefix keys or the CLI. Nothing reaches disk: the buffers end with the
// daemon.
//
// A buffer can hold whatever the person copied, a secret included, so a pane
// is held to its grants (pane_grants.go): reading the buffers needs read, and
// changing them needs write. A paste needs both, because the text it types
// into the caller's own pane is the text's way back to the caller. The paste
// is also a typing verb, held to the same target rules as send-text.

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

// bufferError maps a store error to the verb error for it.
func bufferError(verb string, err error) *verbError {
	switch {
	case errors.Is(err, pastebuf.ErrNotFound), errors.Is(err, pastebuf.ErrNone):
		return hintedVerbError(ErrVerbNoBuffer, verb+": "+err.Error(), &VerbHint{
			Verb:    "list-buffers",
			Command: "tuios list-buffers",
			Detail:  "A yank in copy mode adds a buffer, and so does set-buffer. list-buffers shows the names.",
		})
	case errors.Is(err, pastebuf.ErrBadName):
		return invalidParam("name", verb+": "+err.Error())
	case errors.Is(err, pastebuf.ErrEmpty), errors.Is(err, pastebuf.ErrTooLarge):
		return invalidParam("data", verb+": "+err.Error())
	case errors.Is(err, pastebuf.ErrOff):
		return hintedVerbError(ErrVerbInvalidParams, verb+": "+err.Error(), &VerbHint{
			Detail: "Set limit under [paste_buffers] in config.toml to keep buffers.",
		})
	}
	return newVerbError(ErrVerbInternal, verb+": "+err.Error())
}

// bufferRow is one buffer in a listing.
func bufferRow(b pastebuf.Buffer) map[string]any {
	return map[string]any{
		"name":      b.Name,
		"bytes":     len(b.Data),
		"created":   b.Created.UnixNano(),
		"automatic": b.Automatic,
		"sample":    pastebuf.Sample(b.Data, bufferSampleRunes),
	}
}

// verbListBuffers lists the paste buffers, newest first.
func (d *Daemon) verbListBuffers(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct{}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	store := d.bufferStore()
	list := store.List()
	rows := make([]map[string]any, 0, len(list))
	for _, b := range list {
		rows = append(rows, bufferRow(b))
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
func (d *Daemon) verbShowBuffer(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	b, err := d.bufferStore().Get(p.Name)
	if err != nil {
		return nil, bufferError("show-buffer", err)
	}
	row := bufferRow(b)
	row["type"] = "buffer"
	row["data"] = b.Data
	return row, nil
}

// verbSetBuffer stores text in a buffer.
func (d *Daemon) verbSetBuffer(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name   string `json:"name"`
		Data   string `json:"data"`
		Append bool   `json:"append"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	b, err := d.bufferStore().Set(p.Name, p.Data, p.Append)
	if err != nil {
		return nil, bufferError("set-buffer", err)
	}
	row := bufferRow(b)
	row["type"] = "buffer_set"
	return row, nil
}

// verbDeleteBuffer removes a buffer.
func (d *Daemon) verbDeleteBuffer(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name string `json:"name"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	b, err := d.bufferStore().Delete(p.Name)
	if err != nil {
		return nil, bufferError("delete-buffer", err)
	}
	return map[string]any{"type": "buffer_deleted", "name": b.Name}, nil
}

// verbPasteBuffer types a buffer into a pane as a paste: sanitized as every
// paste is, and in the bracketed paste delimiters when the pane's program
// turned bracketed paste on.
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
	b, err := store.Get(p.Name)
	if err != nil {
		return nil, bufferError("paste-buffer", err)
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
	if p.Delete {
		_, _ = store.Delete(b.Name)
	}
	return map[string]any{
		"type":      "buffer_pasted",
		"name":      b.Name,
		"bytes":     len(b.Data),
		"bracketed": bracketed,
		"deleted":   p.Delete,
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
		{Name: "sample", Type: "string", Description: "The start of the text on one line, with control characters shown as escapes."},
	}
	return map[string]verbEntry{
		"list-buffers": {
			description: "List the paste buffers, newest first. A yank in copy mode adds one, and so does set-buffer. Every client and session shares them. From a pane this needs the read grant.",
			returns: []verbParam{
				{Name: "buffers", Type: "[]object", Description: "One entry per buffer, newest first: name, bytes, created, automatic, sample."},
				{Name: "total", Type: "int", Description: "How many buffers there are."},
				{Name: "bytes", Type: "int", Description: "How many bytes they hold together."},
				{Name: "limit", Type: "int", Description: "How many buffers the daemon keeps, from [paste_buffers] limit."},
				{Name: "max_bytes", Type: "int", Description: "How many bytes the buffers may hold together, from [paste_buffers] max_kb."},
			},
			examples: []string{`{"id":1,"verb":"list-buffers"}`},
			handler:  (*Daemon).verbListBuffers,
		},
		"show-buffer": {
			description: "Return the text of one paste buffer. From a pane this needs the read grant.",
			params:      []verbParam{nameParam("show")},
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
				{Name: "name", Type: "string", Description: "The buffer to set: 1 to 64 printable characters with no spaces. Omit for a new buffer, or for the newest with append."},
				{Name: "append", Type: "bool", Description: "Add the text to the end of the buffer instead of replacing it.", Default: "false"},
			},
			returns: rowReturns,
			examples: []string{
				`{"id":1,"verb":"set-buffer","params":{"data":"make test"}}`,
				`{"id":1,"verb":"set-buffer","params":{"name":"deploy","data":"kubectl rollout restart deploy/api"}}`,
			},
			handler: (*Daemon).verbSetBuffer,
		},
		"delete-buffer": {
			description: "Delete a paste buffer. From a pane this needs the write grant.",
			params:      []verbParam{nameParam("delete")},
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
				{Name: "delete", Type: "bool", Description: "Delete the buffer after the paste.", Default: "false"},
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
