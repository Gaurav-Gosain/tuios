package tmuxcompat

import (
	"encoding/json"
	"errors"
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

// bufferChunk is how much of a buffer one set-buffer call carries. A request
// line is capped at 16 MiB, so a buffer near the shim's 16 MB limit goes in
// parts, each appended to the last.
const bufferChunk = 1 << 20

// isNoBuffer reports whether err is the daemon's no_buffer.
func isNoBuffer(err error) bool { return isCode(err, "no_buffer") }

// daemonBufferList lists the daemon's buffers.
func (s *Shim) daemonBufferList() ([]buffer, error) {
	raw, err := s.Caller.Call("list-buffers", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res struct {
		Buffers []struct {
			Name    string `json:"name"`
			Created int64  `json:"created"`
		} `json:"buffers"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make([]buffer, 0, len(res.Buffers))
	for _, b := range res.Buffers {
		out = append(out, buffer{name: b.Name, at: time.Unix(0, b.Created)})
	}
	return out, nil
}

// daemonBufferRead reads one of the daemon's buffers.
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
		Created int64  `json:"created"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", false, err
	}
	s.readCreated = res.Created
	return res.Data, true, nil
}

// daemonBufferWrite sets one of the daemon's buffers. An empty name makes a
// new buffer the daemon names.
func (s *Shim) daemonBufferWrite(name, data string) error {
	first := data[:min(len(data), bufferChunk)]
	params := map[string]any{"data": first}
	if name != "" {
		params["name"] = name
	}
	raw, err := s.Caller.Call("set-buffer", params)
	if err != nil || len(first) == len(data) {
		return err
	}
	var res struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	for rest := data[len(first):]; rest != ""; {
		part := rest[:min(len(rest), bufferChunk)]
		rest = rest[len(part):]
		if _, err := s.Caller.Call("set-buffer", map[string]any{"name": res.Name, "data": part, "append": true}); err != nil {
			// Half a buffer is worse than none.
			_, _ = s.Caller.Call("delete-buffer", map[string]any{"name": res.Name})
			return err
		}
	}
	return nil
}

// daemonBufferRemove deletes one of the daemon's buffers. A nonzero created
// deletes it only while its text is the one read then. A buffer already gone,
// or set again, is not an error.
func (s *Shim) daemonBufferRemove(name string, created int64) error {
	params := map[string]any{"name": name}
	if created != 0 {
		params["created"] = created
	}
	_, err := s.Caller.Call("delete-buffer", params)
	if isNoBuffer(err) {
		return nil
	}
	return err
}
