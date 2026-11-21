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

type handshakeSlotKey struct{}

type handshakeGate struct {
	max     int64
	pending atomic.Int64
	refused refusalLog
}

func newHandshakeGate(maxPending int) *handshakeGate {
	return &handshakeGate{max: int64(maxPending), refused: refusalLog{what: "connections, because too many are still logging in"}}
}

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

type slotConn struct {
	net.Conn
	release func()
}

func (c *slotConn) Close() error {
	c.release()
	return c.Conn.Close()
}

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

func sessionLimitMessage(maxSessions int) string {
	return fmt.Sprintf("This server has the maximum number of sessions open (%d). Close a session and try again. "+
		"The server owner can change the limit with max_sessions in the [ssh] section of the config file.", maxSessions)
}

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

func sshLimits(userConfig *config.UserConfig, cfg *SSHServerConfig) config.SSHLimits {
	l := config.DefaultSSHLimits()
	if userConfig != nil {
		l = userConfig.SSH.Limits()
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
