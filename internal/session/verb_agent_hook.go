package session

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agenthook"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

type hookCall struct {
	Harness     string            `json:"harness"`
	Event       string            `json:"event"`
	Payload     *string           `json:"payload"`
	PayloadJSON json.RawMessage   `json:"payload_json"`
	Env         map[string]string `json:"env"`
	Explain     bool              `json:"explain"`
	Forwarder   bool              `json:"forwarder"`
	TurnEnd     bool              `json:"turn_end"`
}

func decodeHookCall(params json.RawMessage, what string) (hookCall, *verbError) {
	var p hookCall
	if verr := decodeParams(params, &p); verr != nil {
		return p, verr
	}
	if strings.TrimSpace(p.Harness) == "" {
		return p, invalidParam("harness", "name the harness whose "+what+" ran, such as claude-code")
	}
	for name := range p.Env {
		if !slices.Contains(integration.HookEnv, name) {
			return p, invalidParam("env", "the hook reads no variable "+echoName(name), integration.HookEnv...)
		}
	}
	if len(p.PayloadJSON) > 0 {
		if p.Payload != nil {
			return p, invalidParam("payload_json", "pass the payload once: as payload or as payload_json")
		}
		text := string(p.PayloadJSON)
		p.Payload = &text
	}
	if p.Payload != nil && len(*p.Payload) > agenthook.MaxPayload {
		return p, invalidParam("payload", "the payload is larger than a hook reads")
	}
	return p, nil
}

func (d *Daemon) peerHookIO(cs *connState, env map[string]string, forwarder bool) (agenthook.IO, *verbError) {
	pid := cs.peerPID
	if pid <= 1 {
		return agenthook.IO{}, newVerbError(ErrVerbInvalidParams, "the daemon cannot see the process that sent this event, so it cannot find its pane")
	}
	peerEnv, peerEnvOK := procinfo.Environ(pid)
	getenv := func(name string) string {
		switch name {
		case "TUIOS_PANE_ID", "TUIOS_SESSION":
			if v, ok := peerEnv.Get(name); peerEnvOK && ok {
				return v
			}
			return ""
		}
		if v, ok := env[name]; ok {
			return v
		}
		if v, ok := peerEnv.Get(name); peerEnvOK && ok {
			return v
		}
		return ""
	}
	return agenthook.IO{
		Getenv: getenv,
		Dial: func() (agenthook.Caller, error) {
			return inProcessCaller{d: d, cs: cs}, nil
		},
		Self: func() (int, []int) {
			if forwarder {
				return integration.ProcessOf(pid)
			}
			return integration.ProcessAt(pid)
		},
		HarnessPID: integration.HarnessPID,
		StampDir: func() (string, error) {
			return filepath.Dir(d.manager.SocketPath()), nil
		},
		Now: time.Now,
	}, nil
}

func writeForwarded(cs *connState, body []byte) {
	if _, _, ok := cs.verbCall.take(); ok {
		cs.sendMu.Lock()
		_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, _ = cs.conn.Write(append([]byte(forwardedReplyMark), body...))
		cs.sendMu.Unlock()
	}
}

func (d *Daemon) verbAgentHookEvent(cs *connState, params json.RawMessage) (any, *verbError) {
	p, verr := decodeHookCall(params, "hook")
	if verr != nil {
		return nil, verr
	}
	hio, verr := d.peerHookIO(cs, p.Env, p.Forwarder)
	if verr != nil {
		return nil, verr
	}
	args := []string{p.Harness}
	if p.Event != "" {
		args = append(args, p.Event)
	}
	if p.Payload != nil {
		hio.Stdin = strings.NewReader(*p.Payload)
	}
	hio.StdinTTY = hio.Stdin == nil
	if p.Forwarder {
		cs.forwardedHook.Store(true)
		defer cs.forwardedHook.Store(false)
	}
	var stdout, stderr bytes.Buffer
	hio.Stdout, hio.Stderr = &stdout, &stderr
	agenthook.Run(agenthook.Options{Explain: p.Explain}, args, hio)
	if p.Forwarder {
		writeForwarded(cs, stdout.Bytes())
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

func (d *Daemon) verbAgentStatusLineEvent(cs *connState, params json.RawMessage) (any, *verbError) {
	p, verr := decodeHookCall(params, "status line")
	if verr != nil {
		return nil, verr
	}
	if p.Event != "" {
		return nil, invalidParam("event", "a status line run names no event")
	}
	hio, verr := d.peerHookIO(cs, p.Env, p.Forwarder)
	if verr != nil {
		return nil, verr
	}
	var payload []byte
	if p.Payload != nil {
		payload = []byte(*p.Payload)
		if len(payload) > integration.StatusLineMaxPayload {
			return nil, invalidParam("payload", "the payload is larger than a status line reads")
		}
	}
	out := agenthook.ReportStatusLine(agenthook.StatusLineOptions{TurnEnd: p.TurnEnd}, p.Harness, payload, hio)
	if p.Forwarder {
		writeForwarded(cs, nil)
		return nil, nil
	}
	res := map[string]any{"sent": out.Sent, "skip": out.Skip}
	if out.Error != "" {
		res["error"] = out.Error
	}
	if p.Explain {
		res["explain"] = out
	}
	return res, nil
}

type inProcessCaller struct {
	d  *Daemon
	cs *connState
}

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

func (c inProcessCaller) CallWithTimeout(verb string, params any, _ time.Duration) (json.RawMessage, error) {
	return c.Call(verb, params)
}

const forwardedReplyMark = "TUIOS-HOOK-OK\n"
