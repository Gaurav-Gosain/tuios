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
// A daemon from before those verbs answers unknown_verb, and the shim keeps
// its own buffers as files in its runtime directory, as it always did.

// Buffer backends.
const (
	bufUnknown int8 = iota
	bufDaemon
	bufFiles
)

// daemonBuffers reports whether the daemon holds the buffers. It asks once
// per shim. A refusal for the caller's grants still means the daemon holds
// them: the error is then the answer, and the shim does not keep a second
// set of buffers where the grants do not reach.
func (s *Shim) daemonBuffers() bool {
	if s.bufMode == bufUnknown {
		s.bufMode = bufFiles
		if s.Caller != nil {
			_, err := s.Caller.Call("list-buffers", map[string]any{})
			var coded interface{ ErrorCode() string }
			if err == nil || errors.As(err, &coded) && coded.ErrorCode() == "forbidden" {
				s.bufMode = bufDaemon
			}
		}
	}
	return s.bufMode == bufDaemon
}

// isNoBuffer reports whether err is the daemon's no_buffer.
func isNoBuffer(err error) bool {
	var coded interface{ ErrorCode() string }
	return errors.As(err, &coded) && coded.ErrorCode() == "no_buffer"
}

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
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", false, err
	}
	return res.Data, true, nil
}

// daemonBufferWrite sets one of the daemon's buffers. An empty name makes a
// new buffer the daemon names.
func (s *Shim) daemonBufferWrite(name, data string) error {
	params := map[string]any{"data": data}
	if name != "" {
		params["name"] = name
	}
	_, err := s.Caller.Call("set-buffer", params)
	return err
}

// daemonBufferRemove deletes one of the daemon's buffers. A buffer already
// gone is not an error.
func (s *Shim) daemonBufferRemove(name string) error {
	_, err := s.Caller.Call("delete-buffer", map[string]any{"name": name})
	if isNoBuffer(err) {
		return nil
	}
	return err
}
