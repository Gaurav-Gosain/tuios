package learnssh

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// bucket is a token bucket. It is not safe for concurrent use.
type bucket struct {
	rate, burst, tokens float64
	last                time.Time
}

func newBucket(rate, burst float64, now time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now}
}

func (b *bucket) refill(now time.Time) {
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
}

// take takes n tokens if there are enough.
func (b *bucket) take(n float64, now time.Time) bool {
	b.refill(now)
	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// wait is how long until n tokens are there.
func (b *bucket) wait(n float64, now time.Time) time.Duration {
	b.refill(now)
	if b.tokens >= n {
		return 0
	}
	return time.Duration((n - b.tokens) / b.rate * float64(time.Second))
}

// ipKey is the key limits are counted by: the IPv4 address, or the /64 of an
// IPv6 address, since one IPv6 host usually holds a whole /64.
func ipKey(addr net.Addr) string {
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return addr.String()
	}
	ip := ap.Addr().Unmap()
	if ip.Is4() {
		return ip.String()
	}
	p, _ := ip.Prefix(64)
	return p.String()
}

// logIP is the address as the log shows it: the /24 of an IPv4 address or
// the /48 of an IPv6 one, never the address itself.
func logIP(addr net.Addr) string {
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return "unknown"
	}
	ip := ap.Addr().Unmap()
	bits := 48
	if ip.Is4() {
		bits = 24
	}
	p, _ := ip.Prefix(bits)
	return p.String()
}

// ipLimits counts connections and sessions per address.
type ipLimits struct {
	mu         sync.Mutex
	m          map[string]*ipState
	rate       float64 // new connections per second
	burst      float64
	maxPerIP   int
	maxEntries int
}

type ipState struct {
	conns  *bucket
	active int
	seen   time.Time
}

func newIPLimits(perMinute, burst float64, maxPerIP int) *ipLimits {
	return &ipLimits{m: map[string]*ipState{}, rate: perMinute / 60, burst: burst, maxPerIP: maxPerIP, maxEntries: 100000}
}

func (l *ipLimits) get(key string, now time.Time) *ipState {
	s := l.m[key]
	if s == nil {
		if len(l.m) >= l.maxEntries {
			l.sweepLocked(now, 0)
		}
		s = &ipState{conns: newBucket(l.rate, l.burst, now)}
		l.m[key] = s
	}
	s.seen = now
	return s
}

// allowConn takes one token from the address's connection bucket.
func (l *ipLimits) allowConn(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(key, now).conns.take(1, now)
}

// acquire counts a session for the address, if it has room.
func (l *ipLimits) acquire(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.get(key, now)
	if s.active >= l.maxPerIP {
		return false
	}
	s.active++
	return true
}

func (l *ipLimits) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.m[key]; s != nil && s.active > 0 {
		s.active--
	}
}

// sweep forgets addresses with no session that have been quiet for idle.
func (l *ipLimits) sweep(now time.Time, idle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now, idle)
}

func (l *ipLimits) sweepLocked(now time.Time, idle time.Duration) {
	for k, s := range l.m {
		if s.active == 0 && now.Sub(s.seen) >= idle {
			delete(l.m, k)
		}
	}
}

// limitedConn slows reads from the client to a byte rate, so a client
// cannot flood the server with input, window changes or requests.
type limitedConn struct {
	net.Conn
	mu      sync.Mutex
	b       *bucket
	closed  atomic.Bool
	onClose func()
}

func (c *limitedConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	d := c.b.wait(1, time.Now())
	c.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	c.mu.Lock()
	c.b.refill(time.Now())
	allow := int(c.b.tokens)
	c.mu.Unlock()
	if allow < 1 {
		allow = 1
	}
	if len(p) > allow {
		p = p[:allow]
	}
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	c.b.tokens -= float64(n)
	c.mu.Unlock()
	return n, err
}

func (c *limitedConn) Close() error {
	if c.closed.CompareAndSwap(false, true) && c.onClose != nil {
		c.onClose()
	}
	return c.Conn.Close()
}
