package federation

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Options configure a Manager.
type Options struct {
	// Dial opens the transport to a host. Zero means SSHDialer("ssh").
	Dial Dialer
	// ClientName and ClientVersion identify this hub in the remote daemon's
	// log and in its handshake.
	ClientName    string
	ClientVersion string
	// VerbProtocol and MinVerbProtocol are the control protocol range this
	// build serves. A remote outside the range is reported as incompatible
	// rather than used.
	VerbProtocol    int
	MinVerbProtocol int
	// CallTimeout bounds one call on a live link. Zero means
	// DefaultCallTimeout.
	CallTimeout time.Duration
	// InitialBackoff and MaxBackoff bound the redial cycle.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// Log receives one line whenever a link drops, is kept through a failed
	// listing, or drops a stream for a reader that stopped reading. It is how
	// a person finds out which of those happened, which is the difference
	// between three different fixes. Nil logs nothing.
	Log func(format string, args ...any)

	// now is the clock, so tests can freeze it. Zero means time.Now.
	now func() time.Time
	// stallLimit is how long a full stream may hold the link's read loop
	// before it is dropped. Zero means defaultStallLimit; tests shorten it.
	stallLimit time.Duration
	// connStallLimit is the same for a relayed connection, which carries an
	// attached session rather than a listing. Zero means
	// connectionStallLimit; tests shorten it.
	connStallLimit time.Duration
	// linkQuietLimit is how long a link's pipe may carry no frame at all
	// before a failed control call is read as a dead link rather than a slow
	// one. Zero means defaultLinkQuietLimit; tests shorten it.
	linkQuietLimit time.Duration
}

// defaultLinkQuietLimit is the silence that makes a link dead rather than slow.
//
// It is set above KeepaliveWindow on purpose. ssh's own keepalives end a
// connection whose path has died within that window, so a pipe that is quiet
// for longer than it, with ssh still running, is a remote that has stopped
// talking rather than a network that has gone. Below it, this side would give
// up on links ssh was about to recover on its own.
const defaultLinkQuietLimit = KeepaliveWindow + 15*time.Second

func (o Options) withDefaults() Options {
	if o.Dial == nil {
		o.Dial = SSHDialer("ssh")
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = DefaultCallTimeout
	}
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 60 * time.Second
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.stallLimit <= 0 {
		o.stallLimit = defaultStallLimit
	}
	if o.connStallLimit <= 0 {
		o.connStallLimit = connectionStallLimit
	}
	if o.linkQuietLimit <= 0 {
		o.linkQuietLimit = defaultLinkQuietLimit
	}
	return o
}

// HostReport is one row of a host listing. It is the shape the daemon's
// list-hosts verb serialises and the shape the client's sidebar reads, so the
// CLI and the rail can never disagree about what a host's state is.
type HostReport struct {
	Host   string `json:"host"`
	Addr   string `json:"addr"`
	Status Status `json:"status"`
	// Reason is one plain sentence a user reads.
	Reason string `json:"reason,omitempty"`
	// Detail is the underlying message, usually ssh's own. It comes from
	// another machine, so it is bounded and it is data, never an instruction.
	Detail        string `json:"detail,omitempty"`
	DaemonVersion string `json:"daemon_version,omitempty"`
	Protocol      int    `json:"protocol,omitempty"`
	MinProtocol   int    `json:"min_protocol,omitempty"`
	PID           int    `json:"pid,omitempty"`
	// Command is the tuios binary the link runs on the host: the configured
	// command, or the path the link found on its last dial that reached one.
	// Empty until a dial has reached one.
	Command string `json:"command,omitempty"`
	// Sessions is what the handshake reported when the link came up. It is a
	// handshake fact, not a live count; a listing that needs the live set calls
	// Sessions on the manager.
	Sessions int `json:"sessions,omitempty"`
	// LastOK and LastTry are Unix seconds, zero for never.
	LastOK  int64 `json:"last_ok,omitempty"`
	LastTry int64 `json:"last_try,omitempty"`
	// Drops counts the times this link went down after it had been up, and
	// DropReason says why the last one happened. A link that drops once an
	// hour and a link that has never dropped both report "up", and these two
	// fields are the only thing that tells them apart.
	Drops      int    `json:"drops,omitempty"`
	DropReason string `json:"drop_reason,omitempty"`
	// Stalls counts streams this link dropped because nothing was reading
	// them. It is the one number that separates a slow client from a dead
	// machine, and both used to be reported as a lost link.
	Stalls int `json:"stalls,omitempty"`
}

// Up reports whether this host can be asked anything right now.
func (r HostReport) Up() bool { return r.Status == StatusUp }

// Answer is one host's result from a fan-out call. Exactly one of Result and
// Err is meaningful, and a host that failed is still in the list: a listing
// that dropped its unreachable hosts would be a listing that lies.
type Answer struct {
	Host   string
	Report HostReport
	Result json.RawMessage
	Err    error
}

// Manager holds the hub's links, one per configured host.
//
// Every method on it is non-blocking or bounded by the caller's context. There
// is no method that dials on demand: the supervisors own dialing, and a call
// against a host whose link is not up fails immediately with UnreachableError.
// That is what makes a powered-off machine cost a listing nothing.
type Manager struct {
	opts Options

	mu    sync.Mutex
	table *Table
	links map[string]*supervised

	// ctx is the parent of every link context, set by Start.
	ctx    context.Context
	cancel context.CancelFunc
	// started guards Start so a second call is a no-op. stopped is set by Stop,
	// after which SetTable changes nothing: a manager that is shutting down
	// must not start another supervisor.
	started bool
	stopped bool

	wg sync.WaitGroup
}

// supervised is one host's link and the handle that ends it. Each link has its
// own context, which is what lets one host be dropped or redialed while the
// others keep running.
type supervised struct {
	link *link
	stop context.CancelFunc
	done chan struct{}
}

// New builds a manager over a host table. It dials nothing until Start.
func New(t *Table, opts Options) *Manager {
	return &Manager{table: t, opts: opts.withDefaults(), links: map[string]*supervised{}}
}

// Table returns the configured hosts. The table is replaced whole by SetTable,
// so a caller that holds the returned pointer keeps reading a consistent set.
func (m *Manager) Table() *Table {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.table
}

// Start launches one supervisor per host. It returns at once; the links come up
// in the background.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	// The context is kept even for an empty table, because SetTable can add the
	// first host later and its supervisor needs a parent to run under.
	m.ctx, m.cancel = context.WithCancel(ctx)
	for _, name := range m.table.Names() {
		h, err := m.table.Lookup(name)
		if err != nil {
			continue
		}
		m.startLink(h)
	}
	m.mu.Unlock()
}

// startLink builds one link and runs its supervisor. Called with the lock held.
func (m *Manager) startLink(h Host) {
	ctx, cancel := context.WithCancel(m.ctx)
	s := &supervised{link: newLink(h, m.opts), stop: cancel, done: make(chan struct{})}
	m.links[h.Name] = s
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(s.done)
		s.link.supervise(ctx)
	}()
}

// TableChange is what SetTable did, so a caller can log or report it.
type TableChange struct {
	Added   []string
	Removed []string
	// Redialed are hosts whose settings changed, so the open link was torn down
	// and a new one opened against the new address.
	Redialed []string
}

// Changed reports whether anything moved.
func (c TableChange) Changed() bool {
	return len(c.Added) > 0 || len(c.Removed) > 0 || len(c.Redialed) > 0
}

// SetTable swaps the configured hosts on a running manager.
//
// This is what makes an edit to the [hosts] table take effect without a daemon
// restart. A host that is new gets a link, a host that is gone has its link
// closed and its supervisor ended, and a host whose address or ssh settings
// changed is torn down and dialed again, because the old link is a connection
// to the machine the user just stopped naming.
//
// A host that did not change keeps the link it has. That is what stops a save
// of an unrelated config line from dropping every session listing on the rail
// for a second, and it is why the comparison is per host rather than a swap of
// the whole set.
//
// Every ended supervisor is waited for before this returns, so repeated edits
// cannot leave goroutines or ssh children behind.
func (m *Manager) SetTable(t *Table) TableChange {
	if t == nil {
		t = &Table{}
	}
	var change TableChange
	var wait []*supervised

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return change
	}
	old := m.table
	m.table = t

	// Every host that is gone or has changed loses its supervisor. Collected
	// first and waited for outside the lock: a supervisor takes the link's own
	// mutex, and waiting under this one while it does is how a deadlock starts.
	for _, name := range old.Names() {
		prev, _ := old.Lookup(name)
		next, err := t.Lookup(name)
		switch {
		case err != nil:
			change.Removed = append(change.Removed, name)
		case sameHost(prev, next):
			continue
		default:
			change.Redialed = append(change.Redialed, name)
		}
		if s := m.links[name]; s != nil {
			delete(m.links, name)
			s.stop()
			wait = append(wait, s)
		}
	}

	for _, name := range t.Names() {
		if _, err := old.Lookup(name); err == nil {
			continue
		}
		change.Added = append(change.Added, name)
	}

	// Nothing is dialed until Start has run. Start builds a link for every name
	// in the table it finds, so an edit before then only has to change the set.
	if m.started {
		for _, name := range append(append([]string{}, change.Added...), change.Redialed...) {
			h, err := t.Lookup(name)
			if err != nil {
				continue
			}
			m.startLink(h)
		}
	}
	m.mu.Unlock()

	for _, s := range wait {
		s.link.mu.Lock()
		down := s.link.tearDown
		s.link.mu.Unlock()
		if down != nil {
			down()
		}
		<-s.done
	}
	return change
}

// sameHost reports whether two host entries would dial the same way. Only the
// fields that reach ssh count: a link is redialed because the connection it
// holds is to the wrong place, not because a name was retyped.
func sameHost(a, b Host) bool {
	if a.Addr != b.Addr || a.Command != b.Command || a.ConnectTimeout != b.ConnectTimeout {
		return false
	}
	if len(a.SSHOptions) != len(b.SSHOptions) {
		return false
	}
	for i := range a.SSHOptions {
		if a.SSHOptions[i] != b.SSHOptions[i] {
			return false
		}
	}
	return true
}

// Stop ends every link and waits for the supervisors.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopped = true
	cancel := m.cancel
	links := make([]*supervised, 0, len(m.links))
	for _, s := range m.links {
		links = append(links, s)
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, s := range links {
		s.link.mu.Lock()
		down := s.link.tearDown
		s.link.mu.Unlock()
		if down != nil {
			down()
		}
	}
	m.wg.Wait()
}

// link returns the supervised link for a host name.
func (m *Manager) link(name string) *link {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.links[name]
	if s == nil {
		return nil
	}
	return s.link
}

// Reports snapshots every host. It waits, bounded by ctx, for hosts whose first
// attempt has not settled yet, so the first listing after the daemon starts
// says up or unreachable instead of connecting. A host still unsettled when ctx
// expires is reported as connecting, which is the truth.
func (m *Manager) Reports(ctx context.Context) []HostReport {
	names := m.Table().Names()
	out := make([]HostReport, 0, len(names))
	for _, name := range names {
		l := m.link(name)
		if l == nil {
			out = append(out, HostReport{
				Host:   name,
				Status: StatusConnecting,
				Reason: "The link is not started.",
			})
			continue
		}
		select {
		case <-l.settled:
		case <-ctx.Done():
		}
		out = append(out, l.report())
	}
	return out
}

// Call runs one read verb on one host. An unknown name is ErrUnknownHost and a
// host that is not up is UnreachableError; both are final and neither waits.
func (m *Manager) Call(ctx context.Context, host, verb string, params any) (json.RawMessage, error) {
	if _, err := m.Table().Lookup(host); err != nil {
		return nil, err
	}
	l := m.link(host)
	if l == nil {
		return nil, &UnreachableError{Host: host, Status: StatusConnecting, Reason: "The link is not started."}
	}
	select {
	case <-l.settled:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return l.call(ctx, verb, params)
}

// OpenConnection opens a connection to the daemon on one host, over that host's
// link. The far side answers with a fresh connection to its own daemon socket,
// so the result is a byte pipe to that daemon and nothing more: the caller
// speaks whichever daemon protocol it likes on it, and the caller is also the
// one that bounds and distrusts what comes back.
//
// An unknown name is ErrUnknownHost, a host that is not up is UnreachableError
// and a link with no room for another stream is RefusedError. All three are
// final, and none of them waits on a machine that is not there: the only wait
// is for a host whose first attempt has not settled yet, bounded by ctx.
func (m *Manager) OpenConnection(ctx context.Context, host string) (io.ReadWriteCloser, error) {
	if _, err := m.Table().Lookup(host); err != nil {
		return nil, err
	}
	l := m.link(host)
	if l == nil {
		return nil, &UnreachableError{Host: host, Status: StatusConnecting, Reason: "The link is not started."}
	}
	select {
	case <-l.settled:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return l.openConnection()
}

// CallAll runs one read verb on every configured host at once and returns every
// answer, failures included.
//
// This is what makes an aggregated listing degrade instead of failing: the
// hosts run concurrently, each is bounded by the same context, and one dead
// machine costs the command nothing but its own row. The results come back in
// the table's sorted order, so a listing does not reshuffle between runs.
func (m *Manager) CallAll(ctx context.Context, verb string, params any) []Answer {
	names := m.Table().Names()
	out := make([]Answer, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := Answer{Host: name}
			if l := m.link(name); l != nil {
				select {
				case <-l.settled:
				case <-ctx.Done():
				}
				a.Report = l.report()
			} else {
				a.Report = HostReport{Host: name, Status: StatusConnecting}
			}
			if a.Report.Status != StatusUp {
				a.Err = &UnreachableError{Host: name, Status: a.Report.Status, Reason: a.Report.Reason}
				out[i] = a
				return
			}
			a.Result, a.Err = m.Call(ctx, name, verb, params)
			out[i] = a
		}()
	}
	wg.Wait()
	return out
}
