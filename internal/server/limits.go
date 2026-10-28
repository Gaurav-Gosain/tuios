package server

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/ssh"
	"charm.land/wish/v2"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// limitOptions are the server options that apply limits: the handshake
// deadline, the idle deadline, and the cap on connections that have not yet
// logged in.
//
// charm.land/ssh applies HandshakeTimeout and IdleTimeout only when they are
// set, and with neither a client that connects and never finishes key exchange
// holds a goroutine, a file descriptor and its buffers for as long as it
// likes. In daemon mode the daemon runs in this process, so the same
// descriptors are the ones its panes need for their PTYs. The handshake
// deadline is cleared once the login finishes (see charm.land/ssh conn.go), so
// it never touches a session in use.
func limitOptions(l config.SSHLimits) []ssh.Option {
	opts := []ssh.Option{
		func(s *ssh.Server) error {
			s.HandshakeTimeout = l.HandshakeTimeout
			return nil
		},
	}
	if l.IdleTimeout > 0 {
		opts = append(opts, wish.WithIdleTimeout(l.IdleTimeout))
	}
	if l.MaxHandshakes > 0 {
		gate := newHandshakeGate(l.MaxHandshakes)
		opts = append(opts, func(s *ssh.Server) error {
			s.ConnCallback = gate.admit
			s.ServerConfigCallback = gate.serverConfig
			return nil
		})
	}
	return opts
}

// handshakeSlotKey is the context key of a connection's handshake slot.
type handshakeSlotKey struct{}

// handshakeGate caps the connections that have not logged in yet.
//
// A connection takes a slot when it is accepted, before any byte of the
// handshake, and gives it back when the login succeeds or the connection
// closes, whichever comes first. A connection past the cap is closed at once,
// so a flood of clients that never finish key exchange holds at most max
// descriptors, each for at most the handshake timeout.
//
// The login is seen through AuthLogCallback, which golang.org/x/crypto/ssh
// calls with a nil error only for an attempt that logged the client in: a
// public key query without a signature does not reach it. With no
// authentication the "none" attempt is that call.
type handshakeGate struct {
	max     int64
	pending atomic.Int64
	refused refusalLog
}

func newHandshakeGate(maxPending int) *handshakeGate {
	return &handshakeGate{max: int64(maxPending), refused: refusalLog{what: "connections, because too many are still logging in"}}
}

// admit is the server's ConnCallback. A nil return makes charm.land/ssh close
// the connection.
func (g *handshakeGate) admit(ctx ssh.Context, conn net.Conn) net.Conn {
	if g.pending.Add(1) > g.max {
		g.pending.Add(-1)
		g.refused.note(g.max)
		return nil
	}
	var once sync.Once
	release := func() { once.Do(func() { g.pending.Add(-1) }) }
	ctx.SetValue(handshakeSlotKey{}, release)
	return &slotConn{Conn: conn, release: release}
}

// serverConfig is the server's ServerConfigCallback. charm.land/ssh builds
// the rest of the config on top of what this returns, once per connection.
func (g *handshakeGate) serverConfig(ctx ssh.Context) *gossh.ServerConfig {
	return &gossh.ServerConfig{
		AuthLogCallback: func(_ gossh.ConnMetadata, _ string, err error) {
			if err != nil {
				return
			}
			if release, ok := ctx.Value(handshakeSlotKey{}).(func()); ok {
				release()
			}
		},
	}
}

// slotConn gives its handshake slot back when it closes, for a connection
// that never logged in.
type slotConn struct {
	net.Conn
	release func()
}

func (c *slotConn) Close() error {
	c.release()
	return c.Conn.Close()
}

// sessionLimitMiddleware refuses a session past maxSessions, counted over all
// connections. It sits outside the TUI middleware, so a refused session never
// builds a model, attaches to the daemon or opens a pane. maxSessions <= 0
// means no limit.
func sessionLimitMiddleware(maxSessions int) wish.Middleware {
	var open atomic.Int64
	refused := refusalLog{what: "sessions, because too many are open"}
	limit := int64(maxSessions)
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			if limit <= 0 {
				next(sess)
				return
			}
			if open.Add(1) > limit {
				open.Add(-1)
				refused.note(limit)
				wish.Fatalln(sess, sessionLimitMessage(maxSessions))
				return
			}
			defer open.Add(-1)
			next(sess)
		}
	}
}

// sessionLimitMessage is what a refused client sees.
func sessionLimitMessage(maxSessions int) string {
	return fmt.Sprintf("This server has the maximum number of sessions open (%d). Close a session and try again. "+
		"The server owner can change the limit with max_sessions in the [ssh] section of the config file.", maxSessions)
}

// refusalLog logs refusals at most once a minute, with the count since the
// last line, so a flood of refused clients cannot flood the log too.
type refusalLog struct {
	what  string
	mu    sync.Mutex
	last  time.Time
	count int
}

func (r *refusalLog) note(limit int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	if !r.last.IsZero() && time.Since(r.last) < time.Minute {
		return
	}
	log.Printf("SSH server refused %d %s (limit %d)", r.count, r.what, limit)
	r.last = time.Now()
	r.count = 0
}

// sshLimits is the [ssh] section with the command line limits over it.
func sshLimits(userConfig *config.UserConfig, cfg *SSHServerConfig) config.SSHLimits {
	l := config.DefaultSSHLimits()
	if userConfig != nil {
		l = userConfig.SSH.Limits()
		// A value the server cannot use reads as its default. Say so here,
		// since a headless server shows no other config warnings.
		for _, w := range config.ValidateConfig(userConfig).Warnings {
			if w.Field == "ssh" {
				log.Printf("Warning: [ssh] %s: %s", w.Key, w.Message)
			}
		}
	}
	if cfg.HandshakeTimeout != nil {
		l.HandshakeTimeout = *cfg.HandshakeTimeout
	}
	if cfg.IdleTimeout != nil {
		l.IdleTimeout = *cfg.IdleTimeout
	}
	if cfg.MaxHandshakes != nil {
		l.MaxHandshakes = *cfg.MaxHandshakes
	}
	if cfg.MaxSessions != nil {
		l.MaxSessions = *cfg.MaxSessions
	}
	return l
}

// limitDuration and limitCount write a limit for the startup log line.
func limitDuration(d time.Duration) string {
	if d <= 0 {
		return "off"
	}
	return d.String()
}

func limitCount(n int) string {
	if n <= 0 {
		return "off"
	}
	return strconv.Itoa(n)
}
