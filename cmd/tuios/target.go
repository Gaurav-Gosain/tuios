package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Host-qualified targets on the command line.
//
// `-s build:api` names the session api on host build, and `-w build:api:0`
// names window 0 in it. The grammar is federation.ParseSessionTarget and
// ParseWindowTarget, and it is the same for every verb. A qualified target
// makes the verb run on that host's daemon, through this machine's daemon and
// its link: the client dials its own daemon, asks for a connection to the
// host, and speaks the host's own verb protocol on it. Nothing about the
// verb's parameters changes except that the qualifier is removed before they
// are sent, so the far daemon sees exactly what a local caller would send.
//
// What comes back is the host's word about its own sessions, and it is data.
// The CLI says which host answered, in the text and as a host field in the
// JSON, so a reader can tell a listing of another machine from one of this
// machine without looking at how the command was typed.

// verbTarget is a resolved address: the daemon to talk to, which host it is,
// and the session and window to pass to it.
type verbTarget struct {
	client *session.VerbClient
	// host is the qualifier as typed, "" when the verb runs on this machine.
	// federation.LocalHostName is folded to "", so `local:` and unqualified
	// reach the same code paths.
	host    string
	session string
	window  string
}

// resolveTarget parses a session flag and a window flag together and says
// which host they name. It is pure, so the grammar is testable without a
// daemon. The two may both carry a qualifier when they agree.
func resolveTarget(sessionFlag, windowFlag string) (host, sess, window string, err error) {
	st := federation.ParseSessionTarget(sessionFlag)
	wt := federation.ParseWindowTarget(windowFlag)
	host, sess, window = st.Host, st.Session, wt.Window
	if wt.Qualified {
		switch {
		case !st.Qualified && sessionFlag == "":
			host, sess = wt.Host, wt.Session
		case !st.Qualified:
			return "", "", "", fmt.Errorf("the window names %s:%s and the session flag names %s on this machine. Name the machine in one place",
				wt.Host, wt.Session, sessionFlag)
		case st.Host != wt.Host || st.Session != wt.Session:
			return "", "", "", fmt.Errorf("the window names %s:%s and the session flag names %s:%s. Name one session",
				wt.Host, wt.Session, st.Host, st.Session)
		}
	}
	if host == federation.LocalHostName {
		host = ""
	}
	return host, sess, window, nil
}

// dialTarget resolves the flags and connects to the daemon they name. The
// caller closes the result.
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

// dialSessionTarget is dialTarget for a verb with no window.
func dialSessionTarget(sessionFlag string) (*verbTarget, error) {
	return dialTarget(sessionFlag, "")
}

// Close closes the connection.
func (t *verbTarget) Close() {
	if t != nil && t.client != nil {
		_ = t.client.Close()
	}
}

// params puts the resolved session, and the window when the verb has one,
// into a parameter map. The window is set only when the caller passed the
// key, because a verb without a window parameter must not be sent one.
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

// explain names the host a failed verb ran on, on top of the daemon's own
// hint. On this machine it is explainVerbError unchanged.
func (t *verbTarget) explain(verb string, err error) error {
	if t.host == "" {
		return explainVerbError(verb, err)
	}
	var call *session.VerbCallError
	if errors.As(err, &call) && call.Code == session.ErrVerbUnknownVerb {
		return fmt.Errorf("tuios on %s is too old for %s. Upgrade tuios on %s", t.host, verb, t.host)
	}
	explained := explainVerbError(verb, err)
	var d *diagnosticError
	if errors.As(explained, &d) {
		d.What = "On " + t.host + ": " + d.What
		return d
	}
	return fmt.Errorf("tuios on %s: %w", t.host, explained)
}

// on is the suffix a line of human output carries when the answer came from
// another machine: " on build", or nothing.
func (t *verbTarget) on() string {
	if t.host == "" {
		return ""
	}
	return " on " + t.host
}

// result adds the host to a verb result that came from another machine, so
// the JSON a script reads says where it came from. A local result is
// returned as it is.
func (t *verbTarget) result(raw json.RawMessage) json.RawMessage {
	if t.host == "" {
		return raw
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return raw
	}
	fields["host"] = t.host
	out, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}

// explainTargetConnectError is explainHostConnectError with the one thing a
// qualified target adds: an unknown host may be a session on this machine
// whose name has a colon, and the fix for that is the local: spelling.
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

// thisMachine is the name this machine gives itself when it sends something
// to another: TUIOS_HOST inside a pane, which the daemon set from the
// hostname, else the hostname. It is a claim the far side cannot check, and
// it is shown there as one.
func thisMachine() string {
	if h := os.Getenv("TUIOS_HOST"); h != "" {
		return h
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// printVerbResultOn is printVerbResult with the host added to the JSON.
func printVerbResultOn(t *verbTarget, raw json.RawMessage, jsonOutput bool) error {
	return printVerbResult(t.result(raw), jsonOutput)
}
