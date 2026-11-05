// Package client is the app's connection to its sessions: the tuios daemon.
//
// The app came from kero, whose client spoke to kero's own daemon. This
// package keeps that API (Client.Call, Client.OnEvent, Stream) so the app's
// code stays as it was, and implements it on the tuios daemon's control
// protocol (internal/session's VerbClient). A kero session is a tuios
// session, a kero pane a tuios window, and each pane's terminal is the
// daemon's stream-pane.
//
// The app holds one emulator per pane, MyGo's terminal plugin. The daemon
// keeps the whole history: a stream starts from a snapshot with the last 500
// rows, and older rows stay in the daemon.
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// ErrVersion is returned when the daemon running speaks a protocol this
// build does not: it has to be upgraded (tuios daemon-upgrade).
var ErrVersion = errors.New("the tuios daemon is of another version")

// Version is this build's, sent to the daemon in the hello.
var Version = "guios"

// Dialer names the daemon a connection goes to. Local is this machine's,
// Host another machine's. Other machines are reached through this machine's
// daemon and its links (open-host-connection), never by a dial of their
// own.
type Dialer interface {
	Dial() (net.Conn, error)
}

// Host is a linked machine, by the name this machine's daemon gives it
// (tuios hosts).
type Host string

// Dial is not used: connections are made through the daemon's verbs.
func (h Host) Dial() (net.Conn, error) {
	return nil, errors.New("guios reaches other machines through the tuios daemon's links")
}

// Local is this machine's daemon, which a connect starts unless NoStart.
type Local struct{ NoStart bool }

// Dial is not used: connections are made through the daemon's verbs.
func (l Local) Dial() (net.Conn, error) {
	return nil, errors.New("guios reaches the daemon through its verbs")
}

// StartDaemon, when set, starts the daemon (tuios start-server).
var StartDaemon func() error

// Client is a control connection: calls, and the events of a subscribe.
type Client struct {
	Welcome proto.Welcome

	// host is the linked machine whose daemon this is, "" for this
	// machine's. Every connection the client opens goes there.
	host string

	mu      sync.Mutex
	vc      *session.VerbClient
	onEvent func(proto.Message)
	closed  chan struct{}
	once    sync.Once

	st *stateKeeper

	// ck is the agent cockpit's connection, nil until it is used
	// (agents.go).
	ck *cockpitConn

	// viewMu guards what the person looks at: the session shown, the
	// focused pane of each session, and the open streams (stream.go).
	viewMu  sync.Mutex
	viewing string
	focused map[string]string
	streams map[*Stream]struct{}
}

// Connect opens a control connection to this machine's daemon, starting it
// unless noStart.
func Connect(noStart bool) (*Client, error) {
	vc, err := session.DialVerbClientAs(Version)
	if err != nil && !noStart && StartDaemon != nil {
		if serr := StartDaemon(); serr != nil {
			return nil, errfmt.Errorf("could not start the tuios daemon: %w", serr)
		}
		vc, err = session.DialVerbClientAs(Version)
	}
	if err != nil {
		return nil, err
	}
	return newClient(vc, ""), nil
}

func newClient(vc *session.VerbClient, host string) *Client {
	c := &Client{vc: vc, host: host, closed: make(chan struct{})}
	c.Welcome = proto.Welcome{Version: proto.Version, App: Version}
	if d := vc.Daemon(); d != nil {
		c.Welcome.PID = d.PID
		c.Welcome.App = d.DaemonVersion
	}
	c.st = newStateKeeper(c)
	return c
}

// ConnectHost opens a control connection to the daemon on a linked machine,
// through this machine's daemon. Every verb runs on that machine, and every
// stream and subscription of the client goes there too.
func ConnectHost(host string) (*Client, error) {
	vc, _, err := session.DialVerbClientThroughHost(host, Version)
	if err != nil {
		return nil, err
	}
	return newClient(vc, host), nil
}

// ConnectVia opens a control connection through d: Local, or a Host.
func ConnectVia(d Dialer) (*Client, error) {
	switch d := d.(type) {
	case Local:
		return Connect(d.NoStart)
	case Host:
		return ConnectHost(string(d))
	}
	return nil, errors.New("other machines are reached through the tuios daemon's hosts")
}

// Host is the linked machine the client's daemon runs on, "" for this one.
func (c *Client) Host() string { return c.host }

// dial opens another connection to the client's daemon: this machine's, or
// a linked machine's through this machine's daemon.
func (c *Client) dial() (*session.VerbClient, error) {
	return dialHost(c.host)
}

func dialHost(host string) (*session.VerbClient, error) {
	if host == "" {
		return session.DialVerbClientAs(Version)
	}
	vc, _, err := session.DialVerbClientThroughHost(host, Version)
	return vc, err
}

// OnEvent sets the function events go to.
func (c *Client) OnEvent(fn func(proto.Message)) {
	c.mu.Lock()
	c.onEvent = fn
	c.mu.Unlock()
}

func (c *Client) emit(m proto.Message) {
	c.mu.Lock()
	fn := c.onEvent
	c.mu.Unlock()
	if fn != nil {
		fn(m)
	}
}

// Done is closed once the connection ended.
func (c *Client) Done() <-chan struct{} { return c.closed }

// Close ends the connection and its subscription.
func (c *Client) Close() error {
	c.end()
	c.closeCockpit()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vc.Close()
}

func (c *Client) end() { c.once.Do(func() { close(c.closed) }) }

// verb calls a daemon verb on the control connection. One call at a time:
// the daemon answers in order.
func (c *Client) verb(name string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, err := c.vc.Call(name, params)
	var ve *session.VerbCallError
	if errors.As(err, &ve) && ve.Code == session.ErrVerbUnknownVerb {
		return nil, NeedsNewerDaemon(name, c.Welcome.App)
	}
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) || errors.Is(err, net.ErrClosed) {
			go c.end()
		}
	}
	return raw, err
}

// Call calls a method, decoding its result into out. The methods are kero's
// (proto), served by the daemon's verbs (methods.go).
func (c *Client) Call(method string, params, out any) error {
	res, err := c.dispatch(method, params)
	if err != nil {
		return err
	}
	if out == nil || res == nil {
		return nil
	}
	b, err := jsonx.Marshal(res)
	if err != nil {
		return err
	}
	return jsonx.Unmarshal(b, out)
}

// Go calls a method without waiting for its result, reporting an error to
// fn when set.
func (c *Client) Go(method string, params any, fn func(error)) {
	go func() {
		err := c.Call(method, params, nil)
		if fn != nil {
			fn(err)
		}
	}()
}

// decode turns a method's params, as the app built them, into T.
func decode[T any](params any) (T, error) {
	var out T
	if params == nil {
		return out, nil
	}
	if v, ok := params.(T); ok {
		return v, nil
	}
	b, err := jsonx.Marshal(params)
	if err != nil {
		return out, err
	}
	err = jsonx.Unmarshal(b, &out)
	return out, err
}

func init() {
	if v := os.Getenv("GUIOS_VERSION"); v != "" {
		Version = v
	}
}

var _ = time.Second

// OldDaemonError is a verb the running daemon does not know: it is older
// than this guios. Its message says what to do. The daemon runs the build it
// started with until it restarts, and tuios restart-server starts the newer
// of the tuios it is given and the one on PATH: from guios, the tuios guios
// carries.
type OldDaemonError struct{ Verb, Version string }

func (e *OldDaemonError) Error() string {
	return fmt.Sprintf("The running tuios daemon (%s) is too old for this: it has no %s. Restart it with Settings > tuios daemon > Restart, or run: tuios restart-server.", e.Version, e.Verb)
}

// NeedsNewerDaemon is the OldDaemonError for verb.
func NeedsNewerDaemon(verb, daemonVersion string) error {
	return &OldDaemonError{Verb: verb, Version: daemonVersion}
}

// SessionName is the daemon's name of a session, by its id.
func (c *Client) SessionName(sid string) string { return c.st.nameOf(sid) }
