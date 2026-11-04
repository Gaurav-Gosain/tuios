// Package tailscale asks the Tailscale running on this machine who
// connects to kerod's network listeners, and where it can listen: its
// local API, over tailscaled's socket, else the tailscale command, which
// knows how to reach the app's on macOS and Windows.
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrNotRunning: Tailscale is not installed, or not up.
var ErrNotRunning = errors.New("tailscale is not running on this machine")

// Who is who a connection comes from: a tailnet user's login, as
// alice@example.com, or a tagged device's tags.
type Who struct {
	Login string   // "" for a tagged device
	Name  string   // the user's display name
	Node  string   // the device's name
	Tags  []string // tag:ci
}

func (w Who) String() string {
	if w.Login != "" {
		return w.Login
	}
	if len(w.Tags) > 0 {
		return strings.Join(w.Tags, ",") + " (" + w.Node + ")"
	}
	return w.Node
}

// Self is this machine on the tailnet.
type Self struct {
	IPs     []netip.Addr
	DNSName string // build.tail1234.ts.net
	// Owner is the login of the user the machine belongs to, "" for a
	// tagged one, which belongs to none.
	Owner string
	Tags  []string
}

// sockets are where tailscaled's local API listens.
var sockets = []string{
	"/var/run/tailscale/tailscaled.sock",
	"/run/tailscale/tailscaled.sock",
	"/var/run/tailscaled.socket", // macOS, tailscaled without the app
}

func socket() string {
	if s := os.Getenv("KERO_TAILSCALED_SOCKET"); s != "" {
		return s
	}
	for _, s := range sockets {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	return ""
}

// command is the tailscale command, "" for none.
func command() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range map[string][]string{
		"darwin":  {"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale"},
		"windows": {`C:\Program Files\Tailscale\tailscale.exe`},
	}[runtime.GOOS] {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// localAPI gets a path of the local API, as JSON into v.
func localAPI(ctx context.Context, path string, v any) error {
	if sock := socket(); sock != "" {
		c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}}}
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://local-tailscaled.sock/localapi/v0/"+path, nil)
		resp, err := c.Do(req)
		if err == nil {
			defer resp.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("tailscale: %s: %s", resp.Status, strings.TrimSpace(string(b)))
			}
			return json.Unmarshal(b, v)
		}
	}
	return cli(ctx, path, v)
}

// cli asks the tailscale command what the local API answers.
func cli(ctx context.Context, path string, v any) error {
	cmd := command()
	if cmd == "" {
		return ErrNotRunning
	}
	var args []string
	switch p, q, _ := strings.Cut(path, "?"); p {
	case "status":
		args = []string{"status", "--json"}
	case "whois":
		vals, _ := url.ParseQuery(q)
		args = []string{"whois", "--json", vals.Get("addr")}
	default:
		return fmt.Errorf("tailscale: no command for %s", path)
	}
	c := exec.CommandContext(ctx, cmd, args...)
	// The macOS app's binary is the CLI only when run from a shell (it
	// looks at SHLVL), else it starts the GUI and fails; an app launched
	// from Finder has no shell, so tell it outright.
	c.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1")
	hide(c)
	out, err := c.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return fmt.Errorf("tailscale %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("tailscale %s: %w", args[0], err)
	}
	return json.Unmarshal(out, v)
}

type whoisResponse struct {
	Node *struct {
		Name         string   `json:"Name"`
		ComputedName string   `json:"ComputedName"`
		Tags         []string `json:"Tags"`
	} `json:"Node"`
	UserProfile *struct {
		LoginName   string `json:"LoginName"`
		DisplayName string `json:"DisplayName"`
	} `json:"UserProfile"`
}

// WhoIs tells who connects from addr, ip:port, a tailnet peer's.
func WhoIs(ctx context.Context, addr string) (Who, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var r whoisResponse
	if err := localAPI(ctx, "whois?addr="+url.QueryEscape(addr), &r); err != nil {
		return Who{}, err
	}
	if r.Node == nil {
		return Who{}, fmt.Errorf("tailscale: %s is no tailnet peer", addr)
	}
	w := Who{Node: strings.TrimSuffix(r.Node.Name, "."), Tags: r.Node.Tags}
	if r.Node.ComputedName != "" {
		w.Node = r.Node.ComputedName
	}
	// A tagged device's profile is the "tagged-devices" user's, no one's.
	if r.UserProfile != nil && len(r.Node.Tags) == 0 {
		w.Login, w.Name = r.UserProfile.LoginName, r.UserProfile.DisplayName
	}
	return w, nil
}

type statusResponse struct {
	BackendState string `json:"BackendState"`
	Self         *struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		UserID       int64    `json:"UserID"`
		Tags         []string `json:"Tags"`
	} `json:"Self"`
	User map[string]struct {
		LoginName string `json:"LoginName"`
	} `json:"User"`
}

// Status tells this machine's addresses on the tailnet, and its owner.
func Status(ctx context.Context) (Self, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var r statusResponse
	if err := localAPI(ctx, "status", &r); err != nil {
		return Self{}, err
	}
	if r.BackendState != "Running" || r.Self == nil {
		return Self{}, ErrNotRunning
	}
	s := Self{DNSName: strings.TrimSuffix(r.Self.DNSName, "."), Tags: r.Self.Tags}
	for _, ip := range r.Self.TailscaleIPs {
		if a, err := netip.ParseAddr(ip); err == nil {
			s.IPs = append(s.IPs, a)
		}
	}
	if len(s.Tags) == 0 {
		s.Owner = r.User[strconv.FormatInt(r.Self.UserID, 10)].LoginName
	}
	return s, nil
}

// Allowed reports whether who may connect: the machine's owner, else one
// of allow, as a login (alice@example.com), a tag (tag:ci), "*" for any
// tailnet user, or "tag:*" for any tagged device.
func Allowed(who Who, owner string, allow []string) bool {
	if who.Login != "" && strings.EqualFold(who.Login, owner) {
		return true
	}
	for _, a := range allow {
		a = strings.TrimSpace(a)
		switch {
		case a == "*":
			if who.Login != "" {
				return true
			}
		case a == "tag:*":
			if len(who.Tags) > 0 {
				return true
			}
		case strings.HasPrefix(a, "tag:"):
			for _, t := range who.Tags {
				if t == a {
					return true
				}
			}
		case who.Login != "" && strings.EqualFold(a, who.Login):
			return true
		}
	}
	return false
}
