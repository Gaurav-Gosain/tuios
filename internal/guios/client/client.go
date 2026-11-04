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

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// ErrVersion is returned when the daemon running speaks a protocol this
// build does not: it has to be upgraded (tuios daemon-upgrade).
var ErrVersion = errors.New("the tuios daemon is of another version")

// Version is this build's, sent to the daemon in the hello.
var Version = "guios"

// Dialer names the daemon a connection goes to. Local is this machine's.
// Other machines are reached through this machine's daemon and its links,
// so a remote Dialer is not used here.
type Dialer interface {
	Dial() (net.Conn, error)
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

	mu      sync.Mutex
	vc      *session.VerbClient
	onEvent func(proto.Message)
	closed  chan struct{}
	once    sync.Once

	st *stateKeeper
}

// Connect opens a control connection to this machine's daemon, starting it
// unless noStart.
func Connect(noStart bool) (*Client, error) {
	vc, err := session.DialVerbClientAs(Version)
	if err != nil && !noStart && StartDaemon != nil {
		if serr := StartDaemon(); serr != nil {
			return nil, fmt.Errorf("could not start the tuios daemon: %w", serr)
		}
		vc, err = session.DialVerbClientAs(Version)
	}
	if err != nil {
		return nil, err
	}
	c := &Client{vc: vc, closed: make(chan struct{})}
	c.Welcome = proto.Welcome{Version: proto.Version, App: Version}
	if d := vc.Daemon(); d != nil {
		c.Welcome.PID = d.PID
		c.Welcome.App = d.DaemonVersion
	}
	c.st = newStateKeeper(c)
	return c, nil
}

// ConnectVia opens a control connection through d. Only Local is served:
// other machines' sessions come through this machine's daemon.
func ConnectVia(d Dialer) (*Client, error) {
	if l, ok := d.(Local); ok {
		return Connect(l.NoStart)
	}
	return nil, errors.New("other machines are reached through the tuios daemon's hosts")
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
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
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
	b, err := json.Marshal(params)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

func init() {
	if v := os.Getenv("GUIOS_VERSION"); v != "" {
		Version = v
	}
}

var _ = time.Second
