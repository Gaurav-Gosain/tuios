package app

import (
	"encoding/json"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// shortWindowLabel is the first eight characters of a window id, which is how
// list-windows prints one.
func shortWindowLabel(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// verbDialer reaches the daemon that holds the session this client shows:
// the host's when the client is attached through one, else this machine's.
func (m *OS) verbDialer() agentMailDial { return m.agentMailDialer() }

// agentMailDialer is how a mailbox command reaches the daemon that holds the
// ring: this machine's daemon directly, or, while this client is attached to
// a session on another machine, that machine's daemon through the link. The
// ring is the session's, so it lives where the session does.
func (m *OS) agentMailDialer() agentMailDial {
	host, build := m.AttachedHost, ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() (*session.VerbClient, error) {
		if host == "" {
			return dialVerbLocal()
		}
		c, _, err := dialVerbThroughHost(host, build)
		return c, err
	}
}

// agentMailDial opens a verb connection to the daemon that holds the ring.
type agentMailDial func() (*session.VerbClient, error)

// inboxVerbCall makes one verb call to this machine's daemon.
type inboxVerbCall func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error)

// inboxCaller is how the peek reaches the daemon. Tests set InboxState.call.
func (m *OS) inboxCaller() inboxVerbCall {
	if m.Inbox.call != nil {
		return m.Inbox.call
	}
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()
		return client.CallWithTimeout(verb, params, timeout)
	}
}

// SetInboxVerbCaller replaces how the peek reaches the daemon and where it
// reads the attach nonce from. Tests use it; nil for either restores the
// default.
func (m *OS) SetInboxVerbCaller(call func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error), nonce func() string) {
	m.Inbox.call, m.Inbox.nonce = call, nonce
}

// inboxNonce is this client's attach nonce.
func (m *OS) inboxNonce() string {
	if m.Inbox.nonce != nil {
		return m.Inbox.nonce()
	}
	if m.DaemonClient == nil {
		return ""
	}
	return m.DaemonClient.HumanNonce()
}

// dialVerbThroughHost and dialVerbLocal are the two ways verbDialer reaches a
// daemon, as variables so a test can see which one a command used.
var (
	dialVerbThroughHost = session.DialVerbClientThroughHost
	dialVerbLocal       = session.DialVerbClient
)
