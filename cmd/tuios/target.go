package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type verbTarget struct {
	client  *session.VerbClient
	host    string
	session string
	window  string
}

func resolveTarget(sessionFlag, windowFlag string) (host, sess, window string, err error) {
	st := federation.ParseSessionTarget(sessionFlag)
	wt := federation.ParseWindowTarget(windowFlag)
	host, sess, window = st.Host, st.Session, wt.Window
	if wt.Qualified {
		switch {
		case !st.Qualified && sessionFlag == "":
			host, sess = wt.Host, wt.Session
		case !st.Qualified:
			return "", "", "", errfmt.Errorf("the window names %s:%s and the session flag names %s on this machine. Name the machine in one place",
				wt.Host, wt.Session, sessionFlag)
		case st.Host != wt.Host || st.Session != wt.Session:
			return "", "", "", errfmt.Errorf("the window names %s:%s and the session flag names %s:%s. Name one session",
				wt.Host, wt.Session, st.Host, st.Session)
		}
	}
	if host == federation.LocalHostName {
		host = ""
	}
	return host, sess, window, nil
}

func dialTarget(sessionFlag, windowFlag string) (*verbTarget, error) {
	host, sess, window, err := resolveTarget(sessionFlag, windowFlag)
	if err != nil {
		return nil, err
	}
	t := &verbTarget{host: host, session: sess, window: window}
	if host == "" {
		t.client, err = dialVerb()
		if err != nil {
			return nil, err
		}
		return t, nil
	}
	if err := ensureDaemon(); err != nil {
		return nil, err
	}
	t.client, _, err = session.DialVerbClientThroughHost(host, version)
	if err != nil {
		return nil, explainTargetConnectError(host, sessionFlag, windowFlag, err)
	}
	return t, nil
}

func dialSessionTarget(sessionFlag string) (*verbTarget, error) {
	return dialTarget(sessionFlag, "")
}

func (t *verbTarget) Close() {
	if t != nil && t.client != nil {
		_ = t.client.Close()
	}
}

//go:noinline
func (t *verbTarget) params(p map[string]any) map[string]any {
	if p == nil {
		p = map[string]any{}
	}
	p["session"] = t.session
	if _, has := p["window"]; has {
		p["window"] = t.window
	}
	return p
}

func (t *verbTarget) explain(verb string, err error) error {
	if t.host == "" {
		return explainVerbError(verb, err)
	}
	var call *session.VerbCallError
	if errors.As(err, &call) && call.Code == session.ErrVerbUnknownVerb {
		return &hostError{host: t.host, err: errfmt.Errorf("tuios on %s is too old for %s. Upgrade tuios on %s", t.host, verb, t.host)}
	}
	explained := explainVerbError(verb, err)
	if d, ok := errors.AsType[*diagnosticError](explained); ok {
		d.What = "On " + t.host + ": " + d.What
		return &hostError{host: t.host, err: d}
	}
	return &hostError{host: t.host, err: errfmt.Errorf("tuios on %s: %w", t.host, explained)}
}

type hostError struct {
	host string
	err  error
}

func (e *hostError) Error() string { return e.err.Error() }
func (e *hostError) Unwrap() error { return e.err }

func (t *verbTarget) on() string {
	if t.host == "" {
		return ""
	}
	return " on " + t.host
}

func (t *verbTarget) result(raw json.RawMessage) json.RawMessage {
	if t.host == "" {
		return raw
	}
	var fields map[string]any
	if err := jsonx.Unmarshal(raw, &fields); err != nil {
		return raw
	}
	markUntrusted(fields, t.host)
	out, err := jsonx.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}

func markUntrusted(fields map[string]any, host string) {
	fields["host"] = host
	fields["untrusted"] = true
}

func markHostRows(raw json.RawMessage) json.RawMessage {
	var fields map[string]any
	if err := jsonx.Unmarshal(raw, &fields); err != nil {
		return raw
	}
	hosts, ok := fields["hosts"].([]any)
	if !ok {
		return raw
	}
	for _, h := range hosts {
		entry, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := entry["host"].(string); name != "" && name != federation.LocalHostName {
			entry["untrusted"] = true
		}
	}
	out, err := jsonx.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}

func explainTargetConnectError(host, sessionFlag, windowFlag string, err error) error {
	explained := explainHostConnectError(host, err)
	var connect *session.HostConnectError
	if !errors.As(err, &connect) || connect.Code != session.ErrVerbUnknownHost {
		return explained
	}
	var d *diagnosticError
	if !errors.As(explained, &d) {
		return explained
	}
	switch {
	case strings.HasPrefix(sessionFlag, host+":"):
		d.Extra = append(d.Extra, fmt.Sprintf("For a session on this machine named %q, write -s %q.", sessionFlag, federation.LocalHostName+":"+sessionFlag))
	case strings.HasPrefix(windowFlag, host+":"):
		d.Extra = append(d.Extra, fmt.Sprintf("For a window on this machine named %q, write -w %q.", windowFlag, federation.LocalHostName+"::"+windowFlag))
	}
	return d
}

func printVerbResultOn(t *verbTarget, raw json.RawMessage, jsonOutput bool) error {
	return printVerbResult(t.result(raw), jsonOutput)
}
