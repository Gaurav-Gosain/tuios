package config

import (
	"fmt"
	"strings"
	"time"
)

// SSHConfig is the [ssh] section: the limits of the `tuios ssh` server.
//
// The server reads it once, when it starts. It is file-plane config like
// [hosts]: the limits that guard the server are not for a pane to change over
// the control protocol, so the option registry does not carry it.
//
// The defaults never bother a normal user. A client gets two minutes to log
// in, the same as OpenSSH's LoginGraceTime. A connection that has logged in
// has no idle limit.
type SSHConfig struct {
	// HandshakeTimeout is how long a connection may take to finish key
	// exchange and log in, as a Go duration such as 120s. 0 turns the limit
	// off. The limit ends when the login finishes, so it never closes a
	// session that is in use (default: 120s).
	HandshakeTimeout string `toml:"handshake_timeout"`
	// IdleTimeout closes a connection after this long with no traffic in
	// either direction, as a Go duration such as 30m. 0 turns it off
	// (default: 0).
	IdleTimeout string `toml:"idle_timeout"`
	// MaxHandshakes is how many connections may be in key exchange or login at
	// the same time. The server closes a connection past it at once. 0 turns
	// the limit off (default: 32).
	MaxHandshakes int `toml:"max_handshakes"`
	// MaxSessions is how many sessions may be open at the same time, over all
	// connections. The server refuses a session past it before it starts the
	// interface, with a message. 0 turns the limit off (default: 64).
	MaxSessions int `toml:"max_sessions"`
}

// SSH server defaults.
const (
	SSHDefaultHandshakeTimeout = 120 * time.Second
	SSHDefaultIdleTimeout      = time.Duration(0)
	SSHDefaultMaxHandshakes    = 32
	SSHDefaultMaxSessions      = 64
)

// defaultSSHConfig returns the section DefaultConfig carries.
func defaultSSHConfig() SSHConfig {
	return SSHConfig{
		HandshakeTimeout: SSHDefaultHandshakeTimeout.String(),
		IdleTimeout:      "0",
		MaxHandshakes:    SSHDefaultMaxHandshakes,
		MaxSessions:      SSHDefaultMaxSessions,
	}
}

// SSHLimits is the [ssh] section read into values the server uses directly.
// A zero duration or count means no limit.
type SSHLimits struct {
	HandshakeTimeout time.Duration
	IdleTimeout      time.Duration
	MaxHandshakes    int
	MaxSessions      int
}

// DefaultSSHLimits are the limits of a server with no [ssh] section.
func DefaultSSHLimits() SSHLimits {
	return SSHLimits{
		HandshakeTimeout: SSHDefaultHandshakeTimeout,
		IdleTimeout:      SSHDefaultIdleTimeout,
		MaxHandshakes:    SSHDefaultMaxHandshakes,
		MaxSessions:      SSHDefaultMaxSessions,
	}
}

// Limits reads the section. A value that does not parse, or is negative,
// reads as its default; validation reports it.
func (s SSHConfig) Limits() SSHLimits {
	l := DefaultSSHLimits()
	if d, ok := ParseSSHDuration(s.HandshakeTimeout, l.HandshakeTimeout); ok {
		l.HandshakeTimeout = d
	}
	if d, ok := ParseSSHDuration(s.IdleTimeout, l.IdleTimeout); ok {
		l.IdleTimeout = d
	}
	if s.MaxHandshakes >= 0 {
		l.MaxHandshakes = s.MaxHandshakes
	}
	if s.MaxSessions >= 0 {
		l.MaxSessions = s.MaxSessions
	}
	return l
}

// ParseSSHDuration reads one [ssh] duration: a Go duration such as 120s, or 0
// for no limit. Empty is def. ok is false for a value that does not parse or
// is negative, and d is then def.
func ParseSSHDuration(v string, def time.Duration) (d time.Duration, ok bool) {
	v = strings.TrimSpace(v)
	switch v {
	case "":
		return def, true
	case "0":
		return 0, true
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return def, false
	}
	return d, true
}

// validateSSH warns about an [ssh] value the server cannot use. Each one falls
// back to its default, so a typo never removes a limit.
func validateSSH(cfg *UserConfig, result *ValidationResult) {
	s := cfg.SSH
	durations := []struct {
		key, value string
		def        time.Duration
	}{
		{"handshake_timeout", s.HandshakeTimeout, SSHDefaultHandshakeTimeout},
		{"idle_timeout", s.IdleTimeout, SSHDefaultIdleTimeout},
	}
	for _, d := range durations {
		if _, ok := ParseSSHDuration(d.value, d.def); !ok {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "ssh",
				Key:     d.key,
				Message: fmt.Sprintf("'%s' is not a duration such as 120s, or 0 for no limit; read as %s", d.value, d.def),
			})
		}
	}
	counts := []struct {
		key   string
		value int
		def   int
	}{
		{"max_handshakes", s.MaxHandshakes, SSHDefaultMaxHandshakes},
		{"max_sessions", s.MaxSessions, SSHDefaultMaxSessions},
	}
	for _, c := range counts {
		if c.value < 0 {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "ssh",
				Key:     c.key,
				Message: fmt.Sprintf("%d is negative. Use a count, or 0 for no limit; read as %d", c.value, c.def),
			})
		}
	}
}
