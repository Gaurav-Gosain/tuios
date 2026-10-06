package federation

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Tailscale SSH can stand between ssh and the machine. Two of the things it
// does there reach tuios only as text on ssh's stderr, and both used to be
// reported as a generic failure.
//
// In "check" mode Tailscale holds the connection open until the person
// approves the login in a browser. Before it waits, it prints two lines as the
// ssh banner, and ssh writes them to stderr even with BatchMode on:
//
//	# Tailscale SSH requires an additional check.
//	# To authenticate, visit: https://login.tailscale.com/a/l1d9c7e392d3020
//
// Then nothing more arrives. ssh's ConnectTimeout does not end the wait, since
// the TCP connection and the key exchange are done, so a link waits until its
// own deadline and reports "did not answer in time". When the person approves,
// Tailscale prints "# Authentication checked with Tailscale SSH." and the
// session goes on. Every new connection gets a new link, so a command that
// gives up and tries again asks for a new approval.
//
// A policy that refuses the login ends the connection at once with a line
// that names the user: "tailnet policy does not permit you to SSH as user
// "root"". That is a wrong user in the address more often than not.
//
// All of this text comes from the far side, so it is read as data: the URL
// must look like one, and nothing else of it is repeated.

// The kinds of Tailscale gate. They are also the error kinds `hosts sync`
// reports.
const (
	// GateTailscaleCheck is a login that waits for an approval in a browser.
	GateTailscaleCheck = "tailscale_check"
	// GateTailscalePolicy is a login the tailnet policy refuses.
	GateTailscalePolicy = "tailscale_policy"
)

// SSHGate is a Tailscale gate read from ssh's stderr.
type SSHGate struct {
	// Kind is GateTailscaleCheck or GateTailscalePolicy.
	Kind string
	// URL is where the person approves a check. Empty for a policy refusal.
	URL string
	// User is the login the policy refused. Empty when the refusal does not
	// name one, and for a check.
	User string
	// Approved says Tailscale reported the check as approved after it asked.
	Approved bool
	// Refused says the banner named a sign-in address that is not a
	// Tailscale login origin. URL is then empty: the address is not shown and
	// not opened. See SignInURLAllowed.
	Refused bool
}

var (
	tailscaleCheckLine    = "Tailscale SSH requires an additional check"
	tailscaleApprovedLine = "Authentication checked with Tailscale SSH"
	// The URL is on the same line as the words. \s would cross a line break
	// and take the first word of whatever the far side printed next.
	tailscaleVisitPattern = regexp.MustCompile(`To authenticate, visit:[ \t]*(\S+)`)
	// The URL is repeated to the person, so it is held to the characters a
	// URL has. A far side that prints anything else gets no URL shown.
	tailscaleURLPattern    = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._~%/?=&+-]*)?$`)
	tailscalePolicyPattern = regexp.MustCompile(`tailnet policy does not permit you to SSH (?:as user "?([A-Za-z0-9._@+-]{1,64})"?|to this node)`)
)

// maxGateURL bounds the URL. Tailscale's are about fifty characters.
const maxGateURL = 512

// DefaultLoginOrigins are the origins Tailscale's own coordination server
// sends a check-mode sign-in to.
var DefaultLoginOrigins = []string{"https://login.tailscale.com", "https://controlplane.tailscale.com"}

// SignInURLAllowed reports whether raw is a sign-in page tuios may show and
// open: https, on one of DefaultLoginOrigins or of origins (a Headscale
// server set in [hosts.NAME] tailscale_login), matched on the exact scheme,
// host and port, with no user part.
//
// ssh's stderr also carries what the far side's shell rc files and commands
// print, so whatever runs on that machine can print a banner. Without this
// check one click would open any address it chose, a page on this machine's
// localhost included.
func SignInURLAllowed(raw string, origins ...string) bool {
	if len(raw) > maxGateURL || !tailscaleURLPattern.MatchString(raw) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Opaque != "" {
		return false
	}
	for _, o := range append(append([]string(nil), DefaultLoginOrigins...), origins...) {
		ou, err := url.Parse(strings.TrimRight(strings.TrimSpace(o), "/"))
		if err != nil || ou.Scheme != "https" || ou.User != nil || ou.Host == "" || (ou.Path != "" && ou.Path != "/") {
			continue
		}
		if strings.EqualFold(u.Host, ou.Host) {
			return true
		}
	}
	return false
}

// ParseSSHGate reads ssh's stderr for a Tailscale gate, and returns nil when
// there is none. origins are the sign-in origins allowed besides Tailscale's
// own; see SignInURLAllowed.
func ParseSSHGate(stderr string, origins ...string) *SSHGate {
	if m := tailscalePolicyPattern.FindStringSubmatch(stderr); m != nil {
		return &SSHGate{Kind: GateTailscalePolicy, User: m[1]}
	}
	if !strings.Contains(stderr, tailscaleCheckLine) {
		return nil
	}
	g := &SSHGate{Kind: GateTailscaleCheck}
	if m := tailscaleVisitPattern.FindStringSubmatch(stderr); m != nil {
		u := strings.TrimRight(m[1], "\r")
		if SignInURLAllowed(u, origins...) {
			g.URL = u
		} else {
			g.Refused = true
		}
	}
	g.Approved = strings.Contains(stderr, tailscaleApprovedLine)
	return g
}

// openWords is "Open URL" or, with no URL to show, the place to find one.
func (g SSHGate) openWords() string {
	if g.URL != "" {
		return "Open " + g.URL
	}
	if g.Refused {
		return "The sign-in link from the host is not a Tailscale address, so tuios does not show it. Run ssh to the host in a terminal to see it"
	}
	return "Run ssh to the host in a terminal to see the link"
}

// Sentence is the plain message for a command that gave up: what happened and
// what to do next.
func (g SSHGate) Sentence() string {
	switch g.Kind {
	case GateTailscaleCheck:
		return "Tailscale needs you to approve this login. " + g.openWords() + ", approve it, then run the command again."
	case GateTailscalePolicy:
		if g.User != "" {
			return fmt.Sprintf("The tailnet policy does not let you log in as the user %q. Change the user in the addr of this host in [hosts] to a user the policy allows.", g.User)
		}
		return "The tailnet policy does not let you use ssh to this host. Ask the admin of the tailnet to allow it."
	}
	return ""
}

// SignInSentence says what a host in check mode waits for. The rail's hover
// and the settings page say it too, so it is one constant.
const SignInSentence = "Tailscale SSH needs you to sign in before tuios can reach this machine."

// WaitSentence is the plain message for a link that keeps waiting for the
// sign-in.
func (g SSHGate) WaitSentence() string {
	if g.Kind != GateTailscaleCheck {
		return g.Sentence()
	}
	if g.Refused {
		return SignInSentence + " The sign-in link from the host is not a Tailscale address, so tuios does not show it. Run ssh to the host in a terminal to see it. The link continues when you sign in."
	}
	if g.URL == "" {
		return SignInSentence + " Run ssh to the host in a terminal to see the sign-in page. The link continues when you sign in."
	}
	return SignInSentence + " Open " + g.URL + " to sign in. The link continues when you do."
}

// WithoutApprovedBanner drops the Tailscale check lines from ssh's stderr once
// the check was approved. They explain nothing after that, and a later
// failure is easier to read without them in front of it.
func WithoutApprovedBanner(stderr string) string {
	if !strings.Contains(stderr, tailscaleApprovedLine) {
		return stderr
	}
	lines := strings.Split(stderr, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.Contains(l, tailscaleCheckLine) || strings.Contains(l, "To authenticate, visit:") || strings.Contains(l, tailscaleApprovedLine) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// GateError is a command on a host that a Tailscale gate stopped.
type GateError struct {
	Gate SSHGate
}

func (e *GateError) Error() string { return e.Gate.Sentence() }

// GateFromStderr is a GateError for the gate in stderr, or nil. origins are
// as for ParseSSHGate.
func GateFromStderr(stderr string, origins ...string) *GateError {
	g := ParseSSHGate(stderr, origins...)
	if g == nil || g.Approved {
		return nil
	}
	return &GateError{Gate: *g}
}
