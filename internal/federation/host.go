package federation

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LocalHostName is the reserved name for the daemon a caller is already talking
// to. Section 3 of the design: unqualified always means local, and `local` is
// the alias that lets a script spell the unqualified case. It can never be
// configured as a remote host.
const LocalHostName = "local"

// DefaultConnectTimeout bounds one dial attempt. A machine that is powered off
// must be reported, not waited on, and this is the number that makes `tuios
// hosts` return promptly against a dead box. It is also passed to ssh as
// ConnectTimeout so the child process gives up on its own.
const DefaultConnectTimeout = 10 * time.Second

// DefaultCallTimeout bounds one control-plane call over a live link. It is
// short because every stage 1 call is a listing: a link that has been dialed
// and handshaken and still cannot answer a listing in this long is hung, and
// the supervisor tears it down and redials.
const DefaultCallTimeout = 8 * time.Second

// DefaultRemoteCommand is what the hub runs on the far side.
const DefaultRemoteCommand = "tuios"

// Host is one configured peer.
type Host struct {
	// Name is the qualifier the user types. It is matched exactly.
	Name string
	// Addr is anything ssh understands, ssh_config aliases included.
	Addr string
	// ConnectTimeout bounds one dial. Zero means DefaultConnectTimeout.
	ConnectTimeout time.Duration
	// Command is the remote tuios binary. Zero means DefaultRemoteCommand. It
	// exists for a machine where tuios is not on the non-interactive PATH,
	// which is common with per-user installs.
	Command string
	// SSHOptions are extra arguments placed before the address. They are the
	// escape hatch for a host that needs a flag ssh_config cannot carry.
	SSHOptions []string
}

func (h Host) connectTimeout() time.Duration {
	if h.ConnectTimeout > 0 {
		return h.ConnectTimeout
	}
	return DefaultConnectTimeout
}

func (h Host) command() string {
	if h.Command != "" {
		return h.Command
	}
	return DefaultRemoteCommand
}

// hostNamePattern is what a host name may be. It is deliberately narrow: the
// name becomes a qualifier in `host:target` addresses, so a name carrying a
// colon or a space would make an address ambiguous, and an address that can be
// read two ways is how a caller reaches the wrong machine.
var hostNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ErrUnknownHost reports a name that is not in the configured table. It is
// final: section 3 refuses fuzzy matching across hosts, because silently
// talking to the wrong machine is the worst failure this design can have.
var ErrUnknownHost = errors.New("unknown host")

// Table is the configured host set, keyed by name.
type Table struct {
	hosts map[string]Host
	names []string
}

// NewTable validates and freezes a host set. A host that does not validate is
// dropped with its reason returned, so one bad entry never costs the user the
// rest of the table.
func NewTable(hosts []Host) (*Table, []error) {
	t := &Table{hosts: make(map[string]Host, len(hosts))}
	var problems []error
	for _, h := range hosts {
		name := strings.TrimSpace(h.Name)
		switch {
		case name == "":
			problems = append(problems, errors.New("a host with no name was ignored"))
			continue
		case name == LocalHostName:
			problems = append(problems, fmt.Errorf("host %q was ignored, because %q is the reserved name for this machine", name, LocalHostName))
			continue
		case !hostNamePattern.MatchString(name):
			problems = append(problems, fmt.Errorf("host %q was ignored, because a host name accepts only letters, digits, dot, dash and underscore", name))
			continue
		case strings.TrimSpace(h.Addr) == "":
			problems = append(problems, fmt.Errorf("host %q was ignored, because it has no addr", name))
			continue
		}
		if _, dup := t.hosts[name]; dup {
			problems = append(problems, fmt.Errorf("host %q was ignored, because the name is already used", name))
			continue
		}
		h.Name = name
		h.Addr = strings.TrimSpace(h.Addr)
		t.hosts[name] = h
		t.names = append(t.names, name)
	}
	sort.Strings(t.names)
	return t, problems
}

// Names returns the configured host names in sorted order.
func (t *Table) Names() []string {
	if t == nil {
		return nil
	}
	out := make([]string, len(t.names))
	copy(out, t.names)
	return out
}

// Len reports how many hosts are configured.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.names)
}

// Lookup resolves a host name exactly. The error names every configured host,
// so a caller that guessed a name is told the real set instead of being given a
// near miss.
func (t *Table) Lookup(name string) (Host, error) {
	if t == nil || len(t.hosts) == 0 {
		return Host{}, fmt.Errorf("%w %q: no hosts are configured. Add a [hosts.%s] table to the config file", ErrUnknownHost, name, name)
	}
	h, ok := t.hosts[name]
	if !ok {
		return Host{}, fmt.Errorf("%w %q. Configured hosts: %s", ErrUnknownHost, name, strings.Join(t.names, ", "))
	}
	return h, nil
}

// SplitTarget splits a possibly qualified address into its host and target
// parts. An address with no qualifier is local, always, which is what keeps
// every existing address meaning exactly what it meant before.
//
// It is here in stage 1 so the one parser exists in one place. Stage 1 has no
// verb that takes a qualified target; stage 2 adds them and uses this.
func SplitTarget(addr string) (host, target string) {
	before, after, ok := strings.Cut(addr, ":")
	if !ok || before == "" {
		return LocalHostName, addr
	}
	return before, after
}
