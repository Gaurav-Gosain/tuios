package session

import (
	"bytes"
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var fuzzReadVerbs = []string{
	"hello", "list-verbs", "list-hooks", "list-dock-components", "list-sessions", "list-clients",
	"session-info", "list-windows", "get-window", "list-workspaces", "capture-pane",
	"list-options", "get-option", "list-themes", "list-glyphs", "get-agent-state",
	"explain-agent-screen", "list-agents", "list-attention", "peek-prompt",
	"pane-grants", "list-hosts",
}

type captureConn struct {
	net.Conn
	buf bytes.Buffer
}

func (c *captureConn) Write(p []byte) (int, error)      { return c.buf.Write(p) }
func (c *captureConn) SetWriteDeadline(time.Time) error { return nil }
func (c *captureConn) SetReadDeadline(time.Time) error  { return nil }
func (c *captureConn) Close() error                     { return nil }
func (c *captureConn) RemoteAddr() net.Addr             { return &net.UnixAddr{Name: "fuzz", Net: "unix"} }
func (c *captureConn) LocalAddr() net.Addr              { return &net.UnixAddr{Name: "fuzz", Net: "unix"} }
func (c *captureConn) SetDeadline(t time.Time) error    { return nil }
func (c *captureConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (c *captureConn) String() string                   { return c.buf.String() }

var (
	fuzzDaemonOnce sync.Once
	fuzzDaemon     *Daemon
)

func sameJSON(a, b []byte) bool {
	decode := func(s []byte) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(s))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			return nil, false
		}
		return v, true
	}
	av, aok := decode(a)
	bv, bok := decode(b)
	return aok && bok && reflect.DeepEqual(av, bv)
}

func FuzzVerbDispatch(f *testing.F) {
	for _, s := range verbLineSeeds {
		f.Add([]byte(s))
	}
	for _, v := range fuzzReadVerbs {
		if _, ok := verbRegistry()[v]; !ok {
			f.Fatalf("fuzzReadVerbs names %q, which is not a verb", v)
		}
		f.Add([]byte(`{"id":7,"verb":"` + v + `","params":{}}`))
		f.Add([]byte(`{"id":"x","verb":"` + v + `","params":{"session":"nope","window":"1","nosuch":1}}`))
	}
	f.Add([]byte(`{"id":[1,{"a":null}],"verb":"new-session","params":{"name":"a","Name":"b","nme":1}}`))
	f.Add([]byte(`{"id":1e400,"verb":"get-window","params":{"session":1,"window":{"a":[]}}}`))
	f.Add([]byte(`{"id":1,"verb":"capture-pane","params":{"lines":-99999999999,"window":"\u0000"}}`))

	codes := map[string]bool{}
	for _, c := range errorCodeCatalog {
		codes[c.Code] = true
	}
	safe := map[string]bool{}
	for _, v := range fuzzReadVerbs {
		safe[v] = true
	}

	f.Fuzz(func(t *testing.T, line []byte) {
		if len(line) > 1<<16 {
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return
		}
		fuzzDaemonOnce.Do(func() { fuzzDaemon = NewDaemon(&DaemonConfig{}) })

		var req verbRequest
		decoded := json.Unmarshal(line, &req) == nil
		entry, known := verbRegistry()[req.Verb]

		if decoded && known && !safe[req.Verb] {
			verr := checkParamNames(req.Verb, entry, req.Params)
			var got map[string]json.RawMessage
			isObject := len(bytes.TrimSpace(req.Params)) > 0 && json.Unmarshal(req.Params, &got) == nil
			undeclared, hasUndeclared := "", false
			for name := range got {
				declared := false
				for _, p := range entry.params {
					declared = declared || p.Name == name
				}
				if !declared {
					undeclared, hasUndeclared = name, true
				}
			}
			switch {
			case isObject && hasUndeclared && verr == nil:
				t.Fatalf("%s accepted undeclared parameter %q", req.Verb, undeclared)
			case (!isObject || !hasUndeclared) && verr != nil:
				t.Fatalf("%s refused params %s with only declared names: %s", req.Verb, req.Params, verr.Message)
			case verr != nil && verr.Code != ErrVerbInvalidParams:
				t.Fatalf("%s refused an undeclared parameter with code %q", req.Verb, verr.Code)
			}
			return
		}

		conn := &captureConn{}
		cs := &connState{conn: conn, clientID: "fuzz", done: make(chan struct{})}
		errc := make(chan error, 1)
		go func() { errc <- fuzzDaemon.dispatchVerbLine(cs, line, nil) }()
		select {
		case err := <-errc:
			if err != nil {
				t.Fatalf("dispatch returned %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("dispatch of %q did not return", line)
		}

		out := conn.String()
		if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
			t.Fatalf("reply to %q is not exactly one line: %q", line, out)
		}
		var resp struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *verbError      `json:"error"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("reply to %q is not JSON: %v\n%s", line, err, out)
		}
		hasResult := len(resp.Result) > 0 && string(resp.Result) != "null"
		if hasResult == (resp.Error != nil) {
			t.Fatalf("reply to %q has result=%v and error=%v:\n%s", line, hasResult, resp.Error != nil, out)
		}
		if resp.Error != nil {
			if !codes[resp.Error.Code] {
				t.Fatalf("reply to %q carries error code %q, which list-verbs does not publish", line, resp.Error.Code)
			}
			if resp.Error.Message == "" {
				t.Fatalf("reply to %q carries an error with no message", line)
			}
		}
		if decoded && len(req.ID) > 0 && !sameJSON(req.ID, resp.ID) {
			t.Fatalf("request id %s came back as %s", req.ID, resp.ID)
		}
		if decoded && known && resp.Error == nil {
			if verr := checkParamNames(req.Verb, entry, req.Params); verr != nil {
				t.Fatalf("%s answered a call its own parameter check refuses: %s", req.Verb, verr.Message)
			}
		}
	})
}
