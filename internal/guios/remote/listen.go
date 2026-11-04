package remote

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
)

// Listen is one of kerod's network listeners, as the configuration's
// listen key and `kero daemon serve --listen` name it:
//
//   - tailnet://:7373, or tailnet:// for ListenPort: plain HTTP on this
//     machine's Tailscale addresses, which WireGuard encrypts; tailscaled
//     tells who connects.
//   - tailnet-serve:///path/to/serve.sock, or tailnet-serve:// for
//     serve.sock in kero's directory: a socket for Tailscale Serve to pass
//     HTTPS connections to (`tailscale serve --bg unix:/path/to/serve.sock`),
//     which tells who connects in its headers.
type Listen struct {
	Kind string // "tailnet" or "tailnet-serve"
	Port string // tailnet's
	Path string // tailnet-serve's socket, "" for the default
}

// ParseListen reads a listener.
func ParseListen(s string) (Listen, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return Listen{}, fmt.Errorf("%q is not a listener: %v", s, err)
	}
	switch u.Scheme {
	case "tailnet":
		if u.Hostname() != "" {
			return Listen{}, fmt.Errorf("%q: a tailnet listener takes the machine's Tailscale addresses: write tailnet://:port", s)
		}
		l := Listen{Kind: "tailnet", Port: u.Port()}
		if l.Port == "" {
			l.Port = ListenPort()
		}
		if n, err := strconv.Atoi(l.Port); err != nil || n < 1 || n > 65535 {
			return Listen{}, fmt.Errorf("%q: a port is 1 to 65535", s)
		}
		return l, nil
	case "tailnet-serve":
		if u.Host != "" {
			return Listen{}, fmt.Errorf("%q: write tailnet-serve:///path/to/socket", s)
		}
		return Listen{Kind: "tailnet-serve", Path: u.Path}, nil
	}
	return Listen{}, fmt.Errorf("%q: kerod listens on tailnet://:port or tailnet-serve:///socket", s)
}

// ListenPort is the port a tailnet listener takes when it names none:
// DefaultPort, or the next for a development build, whose kerod then
// listens beside the installed Kero's on the same machine.
func ListenPort() string {
	if mygo.IsDev() {
		return "7374"
	}
	return DefaultPort
}

func (l Listen) String() string {
	if l.Kind == "tailnet" {
		return "tailnet://:" + l.Port
	}
	return "tailnet-serve://" + l.Path
}
