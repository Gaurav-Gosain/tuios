package tmuxcompat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// The paste buffers in the daemon.
//
// A daemon that has the paste buffer verbs (list-buffers, show-buffer,
// set-buffer, delete-buffer) holds the buffers for the shim too, so `tmux
// paste-buffer` pastes what the person yanked in copy mode, and a yank shows
// in `tmux list-buffers`. The daemon then holds the caller to its pane grants
// as it holds every call: reading the buffers needs read, and changing them
// needs write.
//
// A daemon from before those verbs answers unknown_verb, and only then does
// the shim keep its own buffers as files in its runtime directory, as it
// always did. Any other failure, a timeout say, is the answer to the command:
// a second set of buffers the daemon does not know about would be worse.

// Buffer backends.
const (
	bufUnknown int8 = iota
	bufDaemon
	bufFiles
)

// daemonBuffers reports whether the daemon holds the buffers. It asks once
// per shim. Only unknown_verb sends the shim to its own files: a refusal for
// the caller's grants, or a daemon that did not answer, still means the
// daemon holds them, and the next call returns that error.
func (s *Shim) daemonBuffers() bool {
	if s.bufMode == bufUnknown {
		s.bufMode = bufDaemon
		if s.Caller == nil {
			s.bufMode = bufFiles
		} else if _, err := s.Caller.Call("list-buffers", map[string]any{}); isCode(err, "unknown_verb") {
			s.bufMode = bufFiles
		}
	}
	return s.bufMode == bufDaemon
}

// isCode reports whether err carries the daemon error code code.
func isCode(err error, code string) bool {
	var coded interface{ ErrorCode() string }
	return errors.As(err, &coded) && coded.ErrorCode() == code
}

// bufferChunk is how many bytes of a buffer one set-buffer call carries. A
// request line is capped at 16 MiB, so a buffer near the shim's 16 MB limit
// goes in parts of an upload.
const bufferChunk = 768 << 10

// isNoBuffer reports whether err is the daemon's no_buffer.
func isNoBuffer(err error) bool { return isCode(err, "no_buffer") }

// daemonBufferList lists the daemon's buffers, with each one's size and a
// sample, and no buffer's whole content.
func (s *Shim) daemonBufferList() ([]buffer, error) {
	raw, err := s.Caller.Call("list-buffers", map[string]any{"sample_width": sampleWidth})
	if err != nil {
		return nil, err
	}
	var res struct {
		Buffers []struct {
			Name      string `json:"name"`
			Created   int64  `json:"created"`
			Bytes     int    `json:"bytes"`
			Sample    string `json:"sample"`
			Automatic bool   `json:"automatic"`
		} `json:"buffers"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make([]buffer, 0, len(res.Buffers))
	for _, b := range res.Buffers {
		sample := b.Sample
		if s, ok := strings.CutSuffix(sample, "…"); ok {
			// The daemon cut it; tmux shows dots.
			sample = s + "..."
		}
		out = append(out, buffer{name: b.Name, at: time.Unix(0, b.Created), size: b.Bytes, sample: sample, sampled: true, auto: b.Automatic})
	}
	return out, nil
}

// sampleWidth is how many characters of a buffer list-buffers shows, as in
// tmux.
const sampleWidth = 200

// daemonBufferRead reads one of the daemon's buffers, every byte: data_b64
// carries them, where data would turn a byte that is not UTF-8 into U+FFFD.
func (s *Shim) daemonBufferRead(name string) (string, bool, error) {
	raw, err := s.Caller.Call("show-buffer", map[string]any{"name": name})
	if isNoBuffer(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var res struct {
		Data    string `json:"data"`
		DataB64 string `json:"data_b64"`
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", false, err
	}
	data := res.Data
	if res.DataB64 != "" {
		b, err := base64.StdEncoding.DecodeString(res.DataB64)
		if err != nil {
			return "", false, err
		}
		data = string(b)
	}
	s.readVersion = res.Version
	return data, true, nil
}

// daemonBufferWrite sets one of the daemon's buffers, every byte, as base64.
// An empty name makes a new buffer the daemon names. Content larger than one
// part goes as an upload: the daemon sets the buffer once, when the last part
// arrives, so a half-sent buffer is never there to paste. A part is cut from
// the bytes before they are encoded, so no cut can split a character.
func (s *Shim) daemonBufferWrite(name, data string) error {
	if len(data) <= bufferChunk {
		params := map[string]any{"data_b64": base64.StdEncoding.EncodeToString([]byte(data))}
		if name != "" {
			params["name"] = name
		}
		return s.daemonSet(params, data)
	}
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	for rest := data; rest != ""; {
		part := rest[:min(len(rest), bufferChunk)]
		rest = rest[len(part):]
		params := map[string]any{"data_b64": base64.StdEncoding.EncodeToString([]byte(part)), "upload": id}
		if rest != "" {
			params["more"] = true
		} else if name != "" {
			params["name"] = name
		}
		if _, err := s.Caller.Call("set-buffer", params); err != nil {
			return err
		}
	}
	return nil
}

// daemonSet makes one set-buffer call. A daemon that does not read data_b64
// sees no content and answers invalid_params; it gets the content as text,
// which is all it can hold.
func (s *Shim) daemonSet(params map[string]any, data string) error {
	_, err := s.Caller.Call("set-buffer", params)
	if isCode(err, "invalid_params") && strings.Contains(err.Error(), "empty") {
		delete(params, "data_b64")
		params["data"] = data
		_, err = s.Caller.Call("set-buffer", params)
	}
	return err
}

// daemonBufferRemove deletes one of the daemon's buffers. A nonzero version
// deletes it only while its content is the one read then. A buffer already
// gone, or set again, is not an error.
func (s *Shim) daemonBufferRemove(name string, version uint64) error {
	params := map[string]any{"name": name}
	if version != 0 {
		params["version"] = version
	}
	_, err := s.Caller.Call("delete-buffer", params)
	if isNoBuffer(err) {
		return nil
	}
	return err
}
