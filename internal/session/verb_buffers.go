package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"tuios.dev/vt"
)

// ErrVerbNoBuffer is the code of a call naming a buffer there is not, or of a
// call on the newest buffer when there is none.
const ErrVerbNoBuffer = "no_buffer"

const (
	bufferSampleRunes = 60
	maxSampleWidth    = 200
)

func (c *DaemonConfig) pasteBufferLimit() int {
	if c.PasteBuffersOff {
		return 0
	}
	if c.PasteBufferLimit <= 0 {
		return pastebuf.DefaultLimit
	}
	return c.PasteBufferLimit
}

func (d *Daemon) bufferStore() *pastebuf.Store {
	d.buffersOnce.Do(func() {
		if d.buffers == nil {
			d.buffers = pastebuf.New(pastebuf.DefaultLimit, pastebuf.DefaultMaxBytes)
		}
	})
	return d.buffers
}

type bufferCaller struct {
	owner pastebuf.Owner
	see   pastebuf.Filter
}

func (c bufferCaller) person() bool { return c.see == nil }

func (c bufferCaller) bare() pastebuf.Filter {
	if c.person() {
		return func(b pastebuf.Buffer) bool { return b.Owner.Person() }
	}
	return c.see
}

func (c bufferCaller) lookup(name string) pastebuf.Filter {
	if name == "" {
		return c.bare()
	}
	return c.see
}

func (d *Daemon) bufferAccess(cs *connState) bufferCaller {
	if cs != nil && cs.viaLink {
		id := "link:" + d.linkPolicy(cs).Peer
		return paneBuffers(id)
	}
	pa := d.paneAuthority(cs)
	if pa == nil || pa.grants.Has(GrantAdmin) {
		return bufferCaller{}
	}
	return paneBuffers(pa.window)
}

func paneBuffers(id string) bufferCaller {
	return bufferCaller{
		owner: pastebuf.Owner{Pane: id},
		see:   func(b pastebuf.Buffer) bool { return b.Owner.Pane == id },
	}
}

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
			Detail: "Nothing was done. Read the buffer again to see its new content.",
		})
	case errors.Is(err, pastebuf.ErrBadName), errors.Is(err, pastebuf.ErrReservedName):
		return invalidParam("name", verb+": "+err.Error())
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

func (d *Daemon) bufferRow(b pastebuf.Buffer, sampleWidth int) map[string]any {
	row := map[string]any{
		"name":      b.Name,
		"bytes":     len(b.Data),
		"created":   b.Created.UnixNano(),
		"version":   b.Version,
		"automatic": b.Automatic,
		"sample":    pastebuf.Sample(b.Data, sampleWidth),
	}
	if !b.Owner.Person() {
		row["pane"] = d.paneLabel(b.Owner.Pane)
	}
	return row
}

func (d *Daemon) paneLabel(id string) string {
	if sess := d.sessionHoldingWindow(id); sess != nil {
		if w, ok := findWindowState(sess.GetState(), id); ok {
			clean := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, windowDisplayName(w))), " ")
			if r := []rune(clean); len(r) > 64 {
				clean = string(r[:63]) + "…"
			}
			if clean != "" {
				return clean
			}
		}
	}
	return shortWindowID(id)
}

func (d *Daemon) verbListBuffers(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		SampleWidth int `json:"sample_width"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	width := bufferSampleRunes
	if p.SampleWidth > 0 {
		width = min(p.SampleWidth, maxSampleWidth)
	}
	store := d.bufferStore()
	list := store.List(d.bufferAccess(cs).see)
	rows := make([]map[string]any, 0, len(list))
	total := 0
	for _, b := range list {
		rows = append(rows, d.bufferRow(b, width))
		total += len(b.Data)
	}
	limit, maxBytes := store.Limits()
	return map[string]any{
		"type":      "buffers",
		"buffers":   rows,
		"total":     len(rows),
		"bytes":     total,
		"limit":     limit,
		"max_bytes": maxBytes,
	}, nil
}

func (d *Daemon) verbShowBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name     string `json:"name"`
		Version  uint64 `json:"version"`
		Encoding string `json:"encoding"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Encoding != "" && p.Encoding != "base64" {
		return nil, invalidParam("encoding", "encoding is base64, or left out for both data and data_b64", "base64")
	}
	b, err := d.bufferStore().Get(p.Name, p.Version, d.bufferAccess(cs).lookup(p.Name))
	if err != nil {
		return nil, d.bufferError("show-buffer", err)
	}
	row := d.bufferRow(b, bufferSampleRunes)
	row["type"] = "buffer"
	if p.Encoding == "" {
		row["data"] = b.Data
	}
	row["data_b64"] = base64.StdEncoding.EncodeToString([]byte(b.Data))
	return row, nil
}

type bufferUpload struct {
	id      string
	data    []byte
	touched time.Time
	person  bool
}

const bufferUploadTTL = time.Minute

func (d *Daemon) uploadPart(cs *connState, id string, part []byte, last bool, maxBytes int, person bool) ([]byte, *verbError) {
	d.uploadsMu.Lock()
	defer d.uploadsMu.Unlock()
	if d.uploads == nil {
		d.uploads = map[*connState]*bufferUpload{}
	}
	now := time.Now()
	total := 0
	for c, u := range d.uploads {
		if now.Sub(u.touched) > bufferUploadTTL {
			delete(d.uploads, c)
			continue
		}
		if c != cs && u.person == person {
			total += len(u.data)
		}
	}
	u := d.uploads[cs]
	if u == nil || u.id != id {
		u = &bufferUpload{id: id, person: person}
		d.uploads[cs] = u
	}
	u.touched = now
	if len(u.data)+len(part) > maxBytes {
		delete(d.uploads, cs)
		return nil, d.bufferError("set-buffer", errfmt.Errorf("%w: more than %d bytes", pastebuf.ErrTooLarge, maxBytes))
	}
	if total+len(u.data)+len(part) > maxBytes {
		delete(d.uploads, cs)
		return nil, hintedVerbError(ErrVerbInvalidParams, "set-buffer: too many uploads are in progress", &VerbHint{
			Detail: "Nothing was stored. Send the content again later.",
		})
	}
	u.data = append(u.data, part...)
	if !last {
		return nil, nil
	}
	delete(d.uploads, cs)
	return u.data, nil
}

func (d *Daemon) dropUploads(cs *connState) {
	d.uploadsMu.Lock()
	delete(d.uploads, cs)
	d.uploadsMu.Unlock()
}

type b64Bytes []byte

func (b *b64Bytes) UnmarshalJSON(data []byte) error {
	if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' && bytes.IndexByte(data, '\\') < 0 {
		src := data[1 : len(data)-1]
		out := make([]byte, base64.StdEncoding.DecodedLen(len(src)))
		n, err := base64.StdEncoding.Decode(out, src)
		if err != nil {
			return errNotBase64
		}
		*b = out[:n]
		return nil
	}
	var s string
	if err := jsonx.Unmarshal(data, &s); err != nil {
		return err
	}
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return errNotBase64
	}
	*b = out
	return nil
}

var errNotBase64 = errors.New("data_b64 is not base64")

func (d *Daemon) verbSetBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name    string   `json:"name"`
		Data    string   `json:"data"`
		DataB64 b64Bytes `json:"data_b64"`
		Append  bool     `json:"append"`
		Upload  string   `json:"upload"`
		More    bool     `json:"more"`
	}
	if len(params) > 0 {
		if err := jsonx.Unmarshal(params, &p); errors.Is(err, errNotBase64) {
			return nil, invalidParam("data_b64", "set-buffer: data_b64 is not base64")
		} else if err != nil {
			return nil, decodeParams(params, &p)
		}
	}
	part := []byte(p.Data)
	if p.DataB64 != nil {
		part = p.DataB64
	}
	store := d.bufferStore()
	var data string
	if p.Upload != "" {
		_, maxBytes := store.Limits()
		whole, verr := d.uploadPart(cs, p.Upload, part, !p.More, maxBytes, d.bufferAccess(cs).person())
		if verr != nil {
			return nil, verr
		}
		if p.More {
			return map[string]any{"type": "buffer_upload", "upload": p.Upload}, nil
		}
		data = string(whole)
	} else if p.More {
		return nil, invalidParam("more", "set-buffer: more needs upload, the id of the upload the part belongs to")
	} else {
		data = string(part)
	}
	c := d.bufferAccess(cs)
	b, err := store.Set(p.Name, data, p.Append, c.owner, c.see)
	if err != nil {
		return nil, d.bufferError("set-buffer", err)
	}
	if b.Name == "" {
		return map[string]any{"type": "buffer_set", "stored": false}, nil
	}
	row := d.bufferRow(b, bufferSampleRunes)
	row["type"] = "buffer_set"
	row["stored"] = true
	return row, nil
}

func (d *Daemon) verbDeleteBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Name    string `json:"name"`
		Version uint64 `json:"version"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	b, err := d.bufferStore().Delete(p.Name, p.Version, d.bufferAccess(cs).lookup(p.Name))
	if err != nil {
		return nil, d.bufferError("delete-buffer", err)
	}
	return map[string]any{"type": "buffer_deleted", "name": b.Name}, nil
}

func (d *Daemon) verbPasteBuffer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session      string `json:"session"`
		Window       string `json:"window"`
		Name         string `json:"name"`
		Delete       bool   `json:"delete"`
		Raw          bool   `json:"raw"`
		Version      uint64 `json:"version"`
		AllowBlocked bool   `json:"allow_blocked"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	store := d.bufferStore()
	f := d.bufferAccess(cs).lookup(p.Name)
	b, err := store.Get(p.Name, p.Version, f)
	if err != nil {
		return nil, d.bufferError("paste-buffer", err)
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
	if verr := d.recheckBlocked(cs, "paste-buffer", sess, p.Window, p.AllowBlocked); verr != nil {
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

func bufferVerbs() map[string]verbEntry {
	nameParam := func(what string) verbParam {
		return verbParam{Name: "name", Type: "string", Description: "The buffer to " + what + ". Omit for the newest buffer tuios named, as tmux does: for the person, the newest of the person's own, and for a pane without admin, the newest of its own."}
	}
	rowReturns := []verbParam{
		{Name: "name", Type: "string", Description: "The buffer's name: bufferN for one tuios named, or the name set-buffer gave it."},
		{Name: "bytes", Type: "int", Description: "How many bytes the buffer holds."},
		{Name: "created", Type: "int", Description: "Unix-nano time the content was last set."},
		{Name: "version", Type: "int", Description: "A number that changes each time the content is set. Pass it back as version to act only on this content."},
		{Name: "automatic", Type: "bool", Description: "True when tuios named the buffer."},
		{Name: "sample", Type: "string", Description: "The start of the content on one line, with control characters and bytes that are not UTF-8 shown as escapes. It never cuts a character. Print it as it is."},
		{Name: "pane", Type: "string", Description: "The pane that owns the buffer, by its window name, or its short id once it is gone. Absent for the person's buffers."},
	}
	version := verbParam{Name: "version", Type: "int", Description: "Act only while the buffer holds the content of this version, as list-buffers or show-buffer gave it. Otherwise the call answers no_buffer."}
	sampleWidth := verbParam{Name: "sample_width", Type: "int", Description: "How many characters each sample shows, at most 200.", Default: "60"}
	return map[string]verbEntry{
		"list-buffers": {
			scope:       scopeBufferRead,
			link:        needList,
			description: "List the paste buffers, newest first. A yank in copy mode adds one, and so does set-buffer. From a pane this needs the read grant, and a pane without admin sees only the buffers it set. Every total counts only the buffers listed.",
			params:      []verbParam{sampleWidth},
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
			scope:       scopeBufferRead,
			link:        needList,
			description: "Return the content of one paste buffer. A buffer can hold any bytes: data_b64 carries them all, and data carries them as text. From a pane this needs the read grant, and reaches only the buffers of the sessions the pane may read.",
			params: []verbParam{nameParam("show"), version,
				{Name: "encoding", Type: "string", Description: "base64 to get data_b64 alone, without the text in data.", Accepted: []string{"base64"}},
			},
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
			scope:       scopeBufferWrite,
			link:        needWrite,
			description: "Store content in a paste buffer and put it on top. With no name a new buffer is made, append or not, as tmux does; a name makes the buffer a named one. Empty content stores nothing and is no error. When the buffers tuios named pass the limit, the oldest of them go; past the byte cap the oldest go whatever their name. A set from outside every pane, or from a pane with admin, makes the person's buffer; a set from a pane without admin makes that pane's own. From a pane this needs the write grant, and a pane without admin may set only its own buffers, or make a new one with no name.",
			params: []verbParam{
				{Name: "data", Type: "string", Description: "The content as text. Give data or data_b64. It may not be empty or larger than the byte cap."},
				{Name: "data_b64", Type: "string", Description: "The content as base64, for any bytes. It wins over data."},
				{Name: "upload", Type: "string", Description: "An id that sends the content in parts, each in its own call on one connection. The buffer is set once, when the part without more arrives."},
				{Name: "more", Type: "bool", Description: "More parts of this upload follow.", Default: "false"},
				{Name: "name", Type: "string", Description: "The buffer to set: 1 to 64 printable characters. Omit for a new buffer."},
				{Name: "append", Type: "bool", Description: "Add the text to the end of the buffer instead of replacing it.", Default: "false"},
			},
			returns: append(append([]verbParam{}, rowReturns...),
				verbParam{Name: "stored", Type: "bool", Description: "False when the content was empty and nothing was stored. The other fields are then absent."}),
			examples: []string{
				`{"id":1,"verb":"set-buffer","params":{"data":"make test"}}`,
				`{"id":1,"verb":"set-buffer","params":{"name":"deploy","data":"kubectl rollout restart deploy/api"}}`,
			},
			handler: (*Daemon).verbSetBuffer,
		},
		"delete-buffer": {
			scope:       scopeBufferWrite,
			link:        needWrite,
			description: "Delete a paste buffer. From a pane this needs the write grant, and a pane without admin may delete only its own buffers.",
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
			scope:       scopeWrite,
			link:        needWrite,
			description: "Type a paste buffer into a window's PTY as a paste. Each line feed becomes a carriage return unless raw, as in tmux. Control characters other than tab, line feed and carriage return are removed, and the text is wrapped in the bracketed paste delimiters when the program in the pane turned bracketed paste on. From a pane this needs the read and write grants.",
			params: []verbParam{
				sessionParam,
				windowParam,
				nameParam("paste"),
				{Name: "delete", Type: "bool", Description: "Delete the buffer after the paste, unless its content was set again meanwhile.", Default: "false"},
				{Name: "raw", Type: "bool", Description: "Keep each line feed instead of turning it into a carriage return.", Default: "false"},
				version,
				typingAllowBlockedParam,
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
