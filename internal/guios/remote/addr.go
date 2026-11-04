// Package remote connects Kero to kerod on another machine: an Addr names
// the host and how to reach it, Dial opens one connection that carries
// every stream a client needs (proto.MuxHello), and Server answers one,
// for `kero __bridge` and kerod's network listeners.
//
// Three ways reach a host:
//
//   - ssh://[user@]host[:port], or just [user@]host: OpenSSH runs `kero
//     __bridge` on the host, with the user's ~/.ssh/config, keys and agent,
//     and nothing to set up there but Kero itself.
//   - wsl://[distro]: Kero in a WSL distribution of this Windows machine,
//     through wsl.exe, as SSH would.
//   - http://host:port and https://host[:port]: one of kerod's network
//     listeners, which Tailscale tells the caller of (see package daemon).
package remote

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DefaultPort is the port kerod's tailnet listener takes when it names
// none, as a development build's does not (ListenPort), and an http://
// address without one.
const DefaultPort = "7373"

// Addr is how to reach a host.
type Addr struct {
	Scheme string // "ssh", "wsl", "http" or "https"
	User   string // ssh's
	Host   string // the host, or for wsl the distribution, "" its default
	Port   string
	// Kero is the kero executable on the host, for ssh and wsl, when it is
	// not where the bridge looks (BridgeLocations).
	Kero string
}

// ParseAddr reads an address: a URL of a scheme above, or an SSH
// destination, [user@]host[:port], as ssh(1) takes it, an alias of
// ~/.ssh/config too.
func ParseAddr(s string) (Addr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Addr{}, errors.New("no address")
	}
	if strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return Addr{}, fmt.Errorf("%q: an address has no spaces", s)
	}
	raw := s
	if !strings.Contains(s, "://") {
		s = "ssh://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return Addr{}, fmt.Errorf("%q is not an address: %v", raw, err)
	}
	a := Addr{Scheme: strings.ToLower(u.Scheme), Host: u.Hostname(), Port: u.Port()}
	if u.User != nil {
		a.User = u.User.Username()
		if _, has := u.User.Password(); has {
			return Addr{}, fmt.Errorf("%q: Kero takes no password in an address", raw)
		}
	}
	a.Kero = u.Query().Get("kero")
	switch a.Scheme {
	case "ssh":
		if a.Host == "" {
			return Addr{}, fmt.Errorf("%q names no host", raw)
		}
	case "wsl":
		a.Port, a.User = "", ""
	case "http", "https":
		if a.Host == "" {
			return Addr{}, fmt.Errorf("%q names no host", raw)
		}
		if a.Kero != "" {
			return Addr{}, fmt.Errorf("%q: kero= is for ssh:// and wsl:// addresses", raw)
		}
	default:
		return Addr{}, fmt.Errorf("%q: Kero reaches hosts over ssh://, wsl://, http:// or https://", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return Addr{}, fmt.Errorf("%q: an address has no path", raw)
	}
	// Names of hosts and users as DNS and accounts have them, which no
	// shell, and no ssh_config's %h or %r in a ProxyCommand, reads as
	// anything else; nothing ssh(1) would take for one of its options.
	if a.Host != "" && net.ParseIP(a.Host) == nil && !plainName(a.Host, "._-") || strings.HasPrefix(a.Host, "-") {
		return Addr{}, fmt.Errorf("%q: a host is a name of letters, digits, dots and dashes, or an IP address", raw)
	}
	if a.User != "" && (!plainName(a.User, "._+-") || strings.HasPrefix(a.User, "-")) {
		return Addr{}, fmt.Errorf("%q: a user is a name of letters, digits, dots and dashes", raw)
	}
	if a.Kero != "" && !absPath(a.Kero) {
		return Addr{}, fmt.Errorf("%q: kero= is an absolute path of letters, digits, dots, dashes and slashes, as /opt/kero/kero or C:/kero/kero.exe", raw)
	}
	if a.Port != "" {
		for _, r := range a.Port {
			if r < '0' || r > '9' {
				return Addr{}, fmt.Errorf("%q: bad port", raw)
			}
		}
	}
	return a, nil
}

// String writes the address as ParseAddr reads it: an SSH destination
// without its scheme.
func (a Addr) String() string {
	var b strings.Builder
	if a.Scheme != "ssh" {
		b.WriteString(a.Scheme + "://")
	}
	if a.User != "" {
		b.WriteString(url.User(a.User).String() + "@")
	}
	host := a.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	b.WriteString(host)
	if a.Port != "" {
		b.WriteString(":" + a.Port)
	}
	if a.Kero != "" {
		b.WriteString("?kero=" + url.QueryEscape(a.Kero))
	}
	return b.String()
}

// Bridged tells an address whose host runs `kero __bridge` for the
// client: ssh and wsl.
func (a Addr) Bridged() bool { return a.Scheme == "ssh" || a.Scheme == "wsl" }

// hostPort is where an http(s) address connects.
func (a Addr) hostPort() string {
	port := a.Port
	if port == "" {
		port = DefaultPort
		if a.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(a.Host, port)
}

// Label is a short name for the host, as a new host takes by default: the
// host's first label, the distribution for WSL.
func (a Addr) Label() string {
	h := a.Host
	if a.Scheme == "wsl" && h == "" {
		return "wsl"
	}
	if net.ParseIP(h) == nil {
		h, _, _ = strings.Cut(h, ".")
	}
	return h
}

// ValidName reports whether a name may name a saved host: letters,
// digits, dots, dashes and underscores, from a letter or digit; "local"
// stands for this machine.
func ValidName(name string) bool {
	if name == "" || len(name) > 64 || strings.EqualFold(name, "local") {
		return false
	}
	for i, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if i > 0 {
			ok = ok || r == '.' || r == '-' || r == '_'
		}
		if !ok {
			return false
		}
	}
	return true
}

// plainName reports whether s holds only ASCII letters, digits and the
// characters of extra.
func plainName(s, extra string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(extra, r)) {
			return false
		}
	}
	return true
}

// absPath reports whether p is a path kero= takes: absolute, of Unix, or
// of Windows with forward slashes, as C:/kero/kero.exe, of characters no
// shell, sh, cmd or PowerShell, reads as anything else.
func absPath(p string) bool {
	rest := p
	if len(p) > 3 && p[1] == ':' && p[2] == '/' && ('A' <= p[0] && p[0] <= 'Z' || 'a' <= p[0] && p[0] <= 'z') {
		rest = p[2:]
	}
	return strings.HasPrefix(rest, "/") && plainName(rest, "._+-/")
}
