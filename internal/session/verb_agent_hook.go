package session

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agenthook"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// agent-hook-event runs one harness hook event in the daemon, the way
// `tuios agent-hook` runs it in a process of its own (internal/agenthook),
// so a harness's hook does not have to start tuios for each event.
//
// The caller hands the harness, the event and the payload, and gets back what
// the hook would print. Where the caller runs is never taken from the call:
// the pane is found from the process at the other end of the socket, as the
// hook finds it from its own process. TUIOS_PANE_ID and TUIOS_SESSION are
// read from that process's environment as the kernel reports it, and when
// that cannot be read (macOS keeps its own binaries' environment private) the
// daemon matches the process's session and ancestors against its panes'
// shells. Every report the run makes goes through this connection's own
// verbs, so it is checked exactly as the hook's own calls are.

// verbAgentHookEvent handles agent-hook-event.
func (d *Daemon) verbAgentHookEvent(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Harness     string            `json:"harness"`
		Event       string            `json:"event"`
		Payload     *string           `json:"payload"`
		PayloadJSON json.RawMessage   `json:"payload_json"`
		Env         map[string]string `json:"env"`
		Explain     bool              `json:"explain"`
		Forwarder   bool              `json:"forwarder"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Harness) == "" {
		return nil, invalidParam("harness", "name the harness whose hook fired, such as claude-code")
	}
	for name := range p.Env {
		if !slices.Contains(integration.HookEnv, name) {
			return nil, invalidParam("env", "the hook reads no variable "+echoName(name), integration.HookEnv...)
		}
	}
	if len(p.PayloadJSON) > 0 {
		if p.Payload != nil {
			return nil, invalidParam("payload_json", "pass the payload once: as payload or as payload_json")
		}
		text := string(p.PayloadJSON)
		p.Payload = &text
	}
	if p.Payload != nil && len(*p.Payload) > agenthook.MaxPayload {
		return nil, invalidParam("payload", "the payload is larger than a hook reads")
	}
	pid := cs.peerPID
	if pid <= 1 {
		return nil, newVerbError(ErrVerbInvalidParams, "the daemon cannot see the process that sent this event, so it cannot find its pane")
	}
	peerEnv, peerEnvOK := procinfo.Environ(pid)
	getenv := func(name string) string {
		switch name {
		case "TUIOS_PANE_ID", "TUIOS_SESSION":
			// Where the event comes from: only the process's own
			// environment, never the call.
			if v, ok := peerEnv.Get(name); peerEnvOK && ok {
				return v
			}
			return ""
		}
		if v, ok := p.Env[name]; ok {
			return v
		}
		if v, ok := peerEnv.Get(name); peerEnvOK && ok {
			return v
		}
		return ""
	}

	args := []string{p.Harness}
	if p.Event != "" {
		args = append(args, p.Event)
	}
	var stdin io.Reader
	if p.Payload != nil {
		stdin = strings.NewReader(*p.Payload)
	}
	if p.Forwarder {
		cs.forwardedHook.Store(true)
		defer cs.forwardedHook.Store(false)
	}
	var stdout, stderr bytes.Buffer
	agenthook.Run(agenthook.Options{Explain: p.Explain}, args, agenthook.IO{
		Stdin:    stdin,
		StdinTTY: stdin == nil,
		Stdout:   &stdout,
		Stderr:   &stderr,
		Getenv:   getenv,
		Dial: func() (agenthook.Caller, error) {
			return inProcessCaller{d: d, cs: cs}, nil
		},
		Self:       func() (int, []int) { return integration.ProcessOf(pid) },
		HarnessPID: integration.HarnessPID,
		StampDir: func() (string, error) {
			return filepath.Dir(d.manager.SocketPath()), nil
		},
		Now: time.Now,
	})
	if p.Forwarder {
		// A forwarder is a line of shell that cannot decode JSON. It gets
		// the marker line and the hook's stdout as it is, and the
		// connection ends.
		if _, _, ok := cs.verbCall.take(); ok {
			cs.sendMu.Lock()
			_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_, _ = cs.conn.Write(append([]byte(forwardedReplyMark), stdout.Bytes()...))
			cs.sendMu.Unlock()
		}
		return nil, nil
	}
	res := map[string]any{"stdout": stdout.String()}
	if p.Explain {
		var explain any
		if err := jsonx.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &explain); err == nil {
			res["explain"] = explain
		}
	}
	return res, nil
}

// inProcessCaller is a verb client that calls the daemon's own handlers on
// one connection, so a hook event run here is admitted, scoped and answered
// exactly as the same calls made over that connection would be.
type inProcessCaller struct {
	d  *Daemon
	cs *connState
}

// Call runs one verb on the caller's connection.
func (c inProcessCaller) Call(verb string, params any) (json.RawMessage, error) {
	result, verr := c.d.callVerb(c.cs, verb, params)
	if verr != nil {
		return nil, &VerbCallError{Code: verr.Code, Message: verr.Message, Hint: verr.Hint}
	}
	raw, err := jsonx.Marshal(result)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// CallWithTimeout is Call. A verb run here is bounded by its own params, as
// request-approval's hold is, not by a read deadline on a socket.
func (c inProcessCaller) CallWithTimeout(verb string, params any, _ time.Duration) (json.RawMessage, error) {
	return c.Call(verb, params)
}

// forwardedReplyMark starts a forwarder's reply. A forwarder prints what
// follows it, and runs tuios agent-hook when a reply lacks it: an older
// daemon answers the call with a JSON error line instead.
const forwardedReplyMark = "TUIOS-HOOK-OK\n"
