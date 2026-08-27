package federation

import (
	"context"
	"encoding/json"
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

	// now is the clock, so tests can freeze it. Zero means time.Now.
	now func() time.Time
}

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
	// Sessions is what the handshake reported when the link came up. It is a
	// handshake fact, not a live count; a listing that needs the live set calls
	// Sessions on the manager.
	Sessions int `json:"sessions,omitempty"`
	// LastOK and LastTry are Unix seconds, zero for never.
	LastOK  int64 `json:"last_ok,omitempty"`
	LastTry int64 `json:"last_try,omitempty"`
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
	table *Table
	opts  Options

	mu    sync.Mutex
	links map[string]*link

	cancel context.CancelFunc
	wg     sync.WaitGroup
	// started guards Start so a second call is a no-op.
	started bool
}

// New builds a manager over a host table. It dials nothing until Start.
func New(t *Table, opts Options) *Manager {
	return &Manager{table: t, opts: opts.withDefaults(), links: map[string]*link{}}
}

// Table returns the configured hosts.
func (m *Manager) Table() *Table { return m.table }

// Start launches one supervisor per host. It returns at once; the links come up
// in the background.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started || m.table.Len() == 0 {
		m.started = true
		m.mu.Unlock()
		return
	}
	m.started = true
	ctx, m.cancel = context.WithCancel(ctx)
	for _, name := range m.table.Names() {
		h, err := m.table.Lookup(name)
		if err != nil {
			continue
		}
		l := newLink(h, m.opts)
		m.links[name] = l
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			l.supervise(ctx)
		}()
	}
	m.mu.Unlock()
}

// Stop ends every link and waits for the supervisors.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	links := make([]*link, 0, len(m.links))
	for _, l := range m.links {
		links = append(links, l)
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, l := range links {
		l.mu.Lock()
		down := l.tearDown
		l.mu.Unlock()
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
	return m.links[name]
}

// Reports snapshots every host. It waits, bounded by ctx, for hosts whose first
// attempt has not settled yet, so the first listing after the daemon starts
// says up or unreachable instead of connecting. A host still unsettled when ctx
// expires is reported as connecting, which is the truth.
func (m *Manager) Reports(ctx context.Context) []HostReport {
	names := m.table.Names()
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
	if _, err := m.table.Lookup(host); err != nil {
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

// CallAll runs one read verb on every configured host at once and returns every
// answer, failures included.
//
// This is what makes an aggregated listing degrade instead of failing: the
// hosts run concurrently, each is bounded by the same context, and one dead
// machine costs the command nothing but its own row. The results come back in
// the table's sorted order, so a listing does not reshuffle between runs.
func (m *Manager) CallAll(ctx context.Context, verb string, params any) []Answer {
	names := m.table.Names()
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
