package hostsetup

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
)

// Probe is what the setup read from the far machine. It changes nothing
// there. Every string in it came from that machine, so it is cleaned before
// it is kept, and it is shown as data, never run.
type Probe struct {
	// OS and Arch are GOOS and GOARCH. Uname is what uname -s and -m said,
	// for a machine tuios does not ship for.
	OS    string `json:"os"`
	Arch  string `json:"arch"`
	Uname string `json:"uname,omitempty"`
	// System names the system for a person: PRETTY_NAME from os-release,
	// or "macOS 15.1".
	System string `json:"system,omitempty"`

	// Tuios is the binary the link would run, empty when there is none, and
	// TuiosVersion what it reported.
	Tuios        string `json:"tuios,omitempty"`
	TuiosVersion string `json:"tuios_version,omitempty"`
	// Writable says the folder of Tuios can be written, so an install can
	// replace it in place.
	Writable bool `json:"writable,omitempty"`

	// Managers are the package managers a login shell finds: brew, pacman,
	// yay, paru, nix, go, apt and dnf. GoVersion is go's own version.
	Managers []string `json:"managers,omitempty"`
	// Fetch is curl or wget, empty when the machine has neither.
	Fetch string `json:"fetch,omitempty"`
	// Service is the service manager tuios can install the daemon under:
	// "systemd", "launchd", or "" for none. ServiceInstalled says the tuios
	// service is already there.
	Service          string `json:"service,omitempty"`
	ServiceInstalled bool   `json:"service_installed,omitempty"`

	// From is the address this machine came from, as the far side sees it:
	// the first word of SSH_CONNECTION.
	From string `json:"from,omitempty"`
	// Tailscale says the far machine runs tailscale.
	Tailscale bool `json:"tailscale,omitempty"`
}

// Has reports whether the probe found the package manager m.
func (p *Probe) Has(m string) bool { return slices.Contains(p.Managers, m) }

// Platform is OS/Arch, as tuios names a release.
func (p *Probe) Platform() string { return p.OS + "/" + p.Arch }

// probeTools are the programs the probe looks for: the package managers it
// reports, then curl, wget, systemctl and tailscale.
const probeTools = "brew pacman yay paru nix go apt-get dnf curl wget systemctl tailscale"

// probeScript is the sync probe, which finds tuios the way the link does,
// with what the setup needs on top. It follows the same quoting rules: one
// line of sh, with no single quote, backslash or exclamation mark, so any
// login shell passes it to sh unchanged. $1 is "-".
func probeScript() string {
	known := strings.Join([]string{
		"/opt/homebrew/bin/brew", "/usr/local/bin/brew", "/home/linuxbrew/.linuxbrew/bin/brew",
		"$HOME/.nix-profile/bin/nix", "/nix/var/nix/profiles/default/bin/nix", "/run/current-system/sw/bin/nix",
		"/usr/local/go/bin/go", "/usr/lib/go/bin/go",
	}, " ")
	return strings.Join([]string{
		hostsync.ProbeScript(),
		// os-release is shell assignments by its own spec. It is read in a
		// subshell so nothing it sets leaks into the rest.
		`if [ -r /etc/os-release ]; then ( . /etc/os-release; echo "pretty=$PRETTY_NAME" ); fi`,
		`if command -v sw_vers >/dev/null 2>&1; then echo "macos=macOS $(sw_vers -productVersion 2>/dev/null)"; fi`,
		// A login shell's PATH has what the person's profile adds: brew
		// under /opt/homebrew, a nix profile, a go in /usr/local/go. fish
		// takes several names after command -v too. Plain sh is the fallback
		// for a login shell that says nothing.
		`q=; for s in "$SHELL" sh; do q=$("$s" -l -c "command -v ` + probeTools + `" </dev/null 2>/dev/null); if [ -n "$q" ]; then break; fi; done`,
		`echo "$q" | while IFS= read -r l; do case $l in /*) if [ -x "$l" ]; then echo "have=$l"; fi;; esac; done`,
		`for c in ` + known + `; do if [ -x "$c" ]; then echo "have=$c"; fi; done`,
		`echo "from=${SSH_CONNECTION%% *}"`,
	}, "; ")
}

// probe runs the probe over r and reads the answer.
func probe(ctx context.Context, r hostsync.Runner) (*Probe, error) {
	out, stderr, err := r.Run(ctx, probeScript(), []string{"-"}, nil)
	if err != nil {
		return nil, hostsync.RemoteRunError("could not read the machine", stderr, err)
	}
	return parseProbe(hostsync.ParseFacts(out)), nil
}

// parseProbe reads the facts the probe printed.
func parseProbe(f hostsync.Facts) *Probe {
	for _, v := range f {
		for i := range v {
			v[i] = clean(v[i])
		}
	}
	p := &Probe{
		Uname:            f.Get("os") + " " + f.Get("arch"),
		Tuios:            f.Get("path"),
		Writable:         f.Has("dirw"),
		From:             f.Get("from"),
		ServiceInstalled: f.Get("svc") == "installed",
		System:           cmp.Or(f.Get("pretty"), f.Get("macos")),
	}
	p.OS, p.Arch, _ = hostsync.GoPlatform(f.Get("os"), f.Get("arch"))
	p.TuiosVersion, _ = hostsync.ParseVersionLine(f.Get("ver"))
	p.System = cmp.Or(p.System, p.Uname)
	for _, path := range f["have"] {
		name := strings.TrimSuffix(path[strings.LastIndexByte(path, '/')+1:], "-get")
		switch name {
		case "curl", "wget":
			p.Fetch = cmp.Or(p.Fetch, name)
		case "tailscale":
			p.Tailscale = true
		case "systemctl":
			if f.Get("svc") != "unsupported" {
				p.Service = "systemd"
			}
		default:
			if !slices.Contains(p.Managers, name) {
				p.Managers = append(p.Managers, name)
			}
		}
	}
	if p.OS == "darwin" {
		p.Service = "launchd"
	}
	return p
}

// clean holds a string from the far machine to one short line of printable
// text.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
