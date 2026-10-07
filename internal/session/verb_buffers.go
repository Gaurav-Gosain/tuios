package session

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// bufferSampleRunes is how much of a buffer a listing shows, and
// maxSampleWidth the most a caller may ask for with sample_width.
const (
	bufferSampleRunes = 60
	maxSampleWidth    = 200
)

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
		"version":   b.Version,
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
	if verr := buffersOff(); verr != nil {
		return nil, verr
	}
	var p struct {
		ForSession  string `json:"for_session"`
		SampleWidth int    `json:"sample_width"`
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
		row := d.bufferRow(b)
		if p.SampleWidth > 0 {
			row["sample"] = pastebuf.Sample(b.Data, min(p.SampleWidth, maxSampleWidth))
		}
		rows = append(rows, row)
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

// buffersOff reports whether this daemon answers the buffer verbs as if it
// had none, the way a daemon from before them does. Only the e2e suite asks
// for it, to drive a client's fallback to its own store.
func buffersOff() *verbError {
	if os.Getenv("TUIOS_E2E") == "1" && os.Getenv("TUIOS_E2E_NO_BUFFER_VERBS") == "1" {
		return newVerbError(ErrVerbUnknownVerb, "unknown verb (the e2e suite turned the paste buffer verbs off)")
	}
	return nil
}

// verbShowBuffer returns one buffer's content: as text in data, and as
// base64 in data_b64, which keeps every byte.
func (d *Daemon) verbShowBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	if verr := buffersOff(); verr != nil {
		return nil, verr
	}
	var p struct {
		Name       string `json:"name"`
		ForSession string `json:"for_session"`
		Version    uint64 `json:"version"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	_, f := d.bufferAccess(cs, "")
	if p.ForSession != "" {
		f = d.forSession(f, p.ForSession)
	}
	b, err := d.bufferStore().Get(p.Name, p.Version, f)
	if err != nil {
		return nil, d.bufferError("show-buffer", err)
	}
	row := d.bufferRow(b)
	row["type"] = "buffer"
	row["data"] = b.Data
	row["data_b64"] = base64.StdEncoding.EncodeToString([]byte(b.Data))
	return row, nil
}

// bufferUpload is a buffer being sent in parts. Nothing reaches the store
// until the last part, so a half-sent buffer is never pasted, and a part is
// never added to some older buffer.
type bufferUpload struct {
	data    []byte
	touched time.Time
}

// bufferUploadTTL is how long an upload waits for its next part.
const bufferUploadTTL = time.Minute

// maxBufferUploads bounds the uploads in progress across every connection.
const maxBufferUploads = 16

// uploadPart adds part to the upload id on cs, and returns the whole content
// when this is the last part. The key holds cs, so one connection cannot add
// to another's upload.
func (d *Daemon) uploadPart(cs *connState, id string, part []byte, last bool, maxBytes int) ([]byte, *verbError) {
	key := fmt.Sprintf("%p/%s", cs, id)
	d.uploadsMu.Lock()
	defer d.uploadsMu.Unlock()
	if d.uploads == nil {
		d.uploads = map[string]*bufferUpload{}
	}
	now := time.Now()
	for k, u := range d.uploads {
		if now.Sub(u.touched) > bufferUploadTTL {
			delete(d.uploads, k)
		}
	}
	u := d.uploads[key]
	if u == nil {
		if len(d.uploads) >= maxBufferUploads {
			return nil, newVerbError(ErrVerbInvalidParams, "set-buffer: too many uploads in progress; try again in a minute")
		}
		u = &bufferUpload{}
		d.uploads[key] = u
	}
	u.touched = now
	if len(u.data)+len(part) > maxBytes {
		delete(d.uploads, key)
		return nil, d.bufferError("set-buffer", fmt.Errorf("%w: more than %d bytes", pastebuf.ErrTooLarge, maxBytes))
	}
	u.data = append(u.data, part...)
	if !last {
		return nil, nil
	}
	delete(d.uploads, key)
	return u.data, nil
}

// verbSetBuffer stores content in a buffer. The content comes as text in
// data or, for any bytes, as base64 in data_b64. With upload it comes in
// parts: every part but the last has more, and the buffer is set once, from
// the whole content, when the last part arrives.
func (d *Daemon) verbSetBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	if verr := buffersOff(); verr != nil {
		return nil, verr
	}
	var p struct {
		Name    string `json:"name"`
		Data    string `json:"data"`
		DataB64 string `json:"data_b64"`
		Append  bool   `json:"append"`
		Session string `json:"session"`
		Upload  string `json:"upload"`
		More    bool   `json:"more"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	data := p.Data
	if p.DataB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(p.DataB64)
		if err != nil {
			return nil, invalidParam("data_b64", "set-buffer: data_b64 is not base64")
		}
		data = string(raw)
	}
	store := d.bufferStore()
	if p.Upload != "" {
		_, maxBytes := store.Limits()
		whole, verr := d.uploadPart(cs, p.Upload, []byte(data), !p.More, maxBytes)
		if verr != nil {
			return nil, verr
		}
		if p.More {
			return map[string]any{"type": "buffer_upload", "upload": p.Upload}, nil
		}
		data = string(whole)
	} else if p.More {
		return nil, invalidParam("more", "set-buffer: more needs upload, the id of the upload the part belongs to")
	}
	owner, f := d.bufferAccess(cs, p.Session)
	b, err := store.Set(p.Name, data, p.Append, owner, f)
	if err != nil {
		return nil, d.bufferError("set-buffer", err)
	}
	row := d.bufferRow(b)
	row["type"] = "buffer_set"
	return row, nil
}

// verbDeleteBuffer removes a buffer.
func (d *Daemon) verbDeleteBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	if verr := buffersOff(); verr != nil {
		return nil, verr
	}
	var p struct {
		Name    string `json:"name"`
		Version uint64 `json:"version"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	_, f := d.bufferAccess(cs, "")
	b, err := d.bufferStore().Delete(p.Name, p.Version, f)
	if err != nil {
		return nil, d.bufferError("delete-buffer", err)
	}
	return map[string]any{"type": "buffer_deleted", "name": b.Name}, nil
}

// verbPasteBuffer types a buffer into a pane as a paste: each line feed
// turned into a carriage return unless raw, as tmux does, then sanitized as
// every paste is, and in the bracketed paste delimiters when the pane's
// program turned bracketed paste on. With delete it removes the buffer it
// pasted, and not a newer content set under the same name meanwhile.
func (d *Daemon) verbPasteBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	if verr := buffersOff(); verr != nil {
		return nil, verr
	}
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Name    string `json:"name"`
		Delete  bool   `json:"delete"`
		Raw     bool   `json:"raw"`
		Version uint64 `json:"version"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	store := d.bufferStore()
	_, f := d.bufferAccess(cs, "")
	b, err := store.Get(p.Name, p.Version, f)
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
	text := vt.SanitizePaste(pastebuf.PasteText(b.Data, p.Raw))
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
		_, derr := store.Delete(b.Name, b.Version, f)
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
		return verbParam{Name: "name", Type: "string", Description: "The buffer to " + what + ". Omit for the newest buffer tuios named, as tmux does."}
	}
	rowReturns := []verbParam{
		{Name: "name", Type: "string", Description: "The buffer's name: bufferN for one tuios named, or the name set-buffer gave it."},
		{Name: "bytes", Type: "int", Description: "How many bytes the buffer holds."},
		{Name: "created", Type: "int", Description: "Unix-nano time the content was last set."},
		{Name: "version", Type: "int", Description: "A number that changes each time the content is set. Pass it back as version to act only on this content."},
		{Name: "automatic", Type: "bool", Description: "True when tuios named the buffer."},
		{Name: "sample", Type: "string", Description: "The start of the content on one line, with control characters and bytes that are not UTF-8 shown as escapes. It never cuts a character. Print it as it is."},
		{Name: "session", Type: "string", Description: "The session the text was copied or set in. Empty when the person set it from outside every pane."},
		{Name: "pane", Type: "string", Description: "The pane whose process set the buffer. Absent when the person set it, with a yank or from outside every pane."},
	}
	version := verbParam{Name: "version", Type: "int", Description: "Act only while the buffer holds the content of this version, as list-buffers or show-buffer gave it. Otherwise the call answers no_buffer."}
	sampleWidth := verbParam{Name: "sample_width", Type: "int", Description: "How many characters each sample shows, at most 200.", Default: "60"}
	forSession := verbParam{Name: "for_session", Type: "string", Description: "Keep only the buffers the paste key takes in this session: the person's own, and the ones a pane of this session set."}
	return map[string]verbEntry{
		"list-buffers": {
			description: "List the paste buffers, newest first. A yank in copy mode adds one, and so does set-buffer. From a pane this needs the read grant, and a pane without admin sees only the buffers of the sessions it may read.",
			params:      []verbParam{forSession, sampleWidth},
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
			description: "Return the content of one paste buffer. A buffer can hold any bytes: data_b64 carries them all, and data carries them as text. From a pane this needs the read grant, and reaches only the buffers of the sessions the pane may read.",
			params:      []verbParam{nameParam("show"), forSession, version},
			returns: append(append([]verbParam{}, rowReturns...),
				verbParam{Name: "data", Type: "string", Description: "The whole content as text. A byte that is not UTF-8 reads as U+FFFD here."},
				verbParam{Name: "data_b64", Type: "string", Description: "The whole content, every byte, as base64."}),
			examples: []string{
				`{"id":1,"verb":"show-buffer"}`,
				`{"id":1,"verb":"show-buffer","params":{"name":"buffer3"}}`,
			},
			handler: (*Daemon).verbShowBuffer,
		},
		"set-buffer": {
			description: "Store content in a paste buffer and put it on top. With no name a new buffer is made, append or not, as tmux does. When the buffers tuios named pass the limit, the oldest of them go; past the byte cap the oldest go whatever their name. From a pane this needs the write grant.",
			params: []verbParam{
				{Name: "data", Type: "string", Description: "The content as text. Give data or data_b64. It may not be empty or larger than the byte cap."},
				{Name: "data_b64", Type: "string", Description: "The content as base64, for any bytes. It wins over data."},
				{Name: "upload", Type: "string", Description: "An id that sends the content in parts, each in its own call on one connection. The buffer is set once, when the part without more arrives."},
				{Name: "more", Type: "bool", Description: "More parts of this upload follow.", Default: "false"},
				{Name: "name", Type: "string", Description: "The buffer to set: 1 to 64 printable characters. Omit for a new buffer."},
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
				version,
			},
			returns: []verbParam{
				{Name: "name", Type: "string", Description: "The buffer that was deleted."},
			},
			examples: []string{`{"id":1,"verb":"delete-buffer","params":{"name":"buffer1"}}`},
			handler:  (*Daemon).verbDeleteBuffer,
		},
		"paste-buffer": {
			description: "Type a paste buffer into a window's PTY as a paste. Each line feed becomes a carriage return unless raw, as in tmux. Control characters other than tab, line feed and carriage return are removed, and the text is wrapped in the bracketed paste delimiters when the program in the pane turned bracketed paste on. From a pane this needs the read and write grants.",
			params: []verbParam{
				sessionParam,
				windowParam,
				nameParam("paste"),
				{Name: "delete", Type: "bool", Description: "Delete the buffer after the paste, unless its content was set again meanwhile.", Default: "false"},
				{Name: "raw", Type: "bool", Description: "Keep each line feed instead of turning it into a carriage return.", Default: "false"},
				version,
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
