package tmuxcompat

import (
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const (
	bufUnknown int8 = iota
	bufDaemon
	bufFiles
)

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

func isCode(err error, code string) bool {
	var coded interface{ ErrorCode() string }
	return errors.As(err, &coded) && coded.ErrorCode() == code
}

const bufferChunk = 768 << 10

func isNoBuffer(err error) bool { return isCode(err, "no_buffer") }

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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make([]buffer, 0, len(res.Buffers))
	for _, b := range res.Buffers {
		out = append(out, buffer{name: b.Name, at: time.Unix(0, b.Created), size: b.Bytes, sample: b.Sample, sampled: true, auto: b.Automatic})
	}
	return out, nil
}

const sampleWidth = 200

func (s *Shim) daemonBufferRead(name string) (string, bool, error) {
	raw, err := s.Caller.Call("show-buffer", map[string]any{"name": name, "encoding": "base64"})
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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
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

func (s *Shim) daemonBufferWrite(name, data string, appendTo bool) error {
	id := ""
	if len(data) > bufferChunk {
		id = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	for rest := data; ; {
		part := rest[:min(len(rest), bufferChunk)]
		rest = rest[len(part):]
		params := map[string]any{"data_b64": base64.StdEncoding.EncodeToString([]byte(part))}
		if id != "" {
			params["upload"] = id
		}
		if rest != "" {
			params["more"] = true
		} else {
			if name != "" {
				params["name"] = name
			}
			if appendTo {
				params["append"] = true
			}
		}
		if _, err := s.Caller.Call("set-buffer", params); err != nil {
			return err
		}
		if rest == "" {
			return nil
		}
	}
}

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
