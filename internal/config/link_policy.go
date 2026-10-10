package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// What another machine may do here when it links in.
//
// A [hosts.NAME] table names a machine this one dials. The same table, read on
// the machine a link arrives at, also says what the machine of that name may
// do there:
//
//	[hosts.laptop]
//	addr = "laptop"                       # optional: without it nothing is dialled
//	allow = ["list", "mail", "open", "write", "files"]
//	hold_mail = true
//	hosted_grace = "10m"
//	files_roots = ["~/Downloads/tuios", "~/dev"]
//
//	[hosts."*"]                           # every machine with no table of its own
//	allow = ["list", "mail"]
//
// The name a link arrives under is the one the other machine gives for itself,
// its host name, unless the ssh key it logs in with pins one with a forced
// command (`command="tuios stdio-proxy --as laptop"`). Only the pinned name is
// a boundary: a key that can run any command can also run a shell. The daemon
// that receives the link enforces the policy on every verb and every binary
// message, whatever name it arrived under.

// Link capabilities, the values of allow.
const (
	// LinkAllowList reads: listings, captures, screenshots, agent state, the
	// Inbox, prompt peeks, waits and the event stream.
	LinkAllowList = "list"
	// LinkAllowMail sends and reads agent mail and uses the stash.
	LinkAllowMail = "mail"
	// LinkAllowOpen starts processes here: sessions, windows, panes run for
	// the other machine, worktrees and fans.
	LinkAllowOpen = "open"
	// LinkAllowWrite changes what is already here: typing into panes, closing
	// and moving windows, options, layouts, names and agent reports.
	LinkAllowWrite = "write"
	// LinkAllowRespond answers what waits for the person: prompts, held
	// approvals, and dismissing Inbox items.
	LinkAllowRespond = "respond"
	// LinkAllowFiles reads and writes files here with the file verbs: a
	// file's bytes, copies in and out, new folders, moves and removes. It is
	// apart from write, so a person can stop file writes from a machine and
	// still let it type into panes. A write that arrives over a link lands
	// only in the folders of files_roots (the receive folder unless the
	// person adds more), and never in the folders that hold keys, shell
	// start files and tuios's own config. A read never returns keys or
	// credentials. See verb_files_confine.go in internal/session.
	LinkAllowFiles = "files"
)

// LinkCapabilities is every capability, in the order they are documented.
var LinkCapabilities = []string{LinkAllowList, LinkAllowMail, LinkAllowOpen, LinkAllowWrite, LinkAllowRespond, LinkAllowFiles}

// LinkRelayCapabilities is what a machine needs to relay through this one to
// its hosts: every capability there was before files. A table written for
// v0.9 that allows all five keeps relaying. The transfer verbs, which relay
// file bytes, need files as well.
var LinkRelayCapabilities = []string{LinkAllowList, LinkAllowMail, LinkAllowOpen, LinkAllowWrite, LinkAllowRespond}

// LinkCapabilityWords says what a capability lets a machine do, for a
// refusal a person reads.
func LinkCapabilityWords(c string) string {
	switch c {
	case LinkAllowList:
		return "read listings and screens"
	case LinkAllowMail:
		return "send and read agent mail"
	case LinkAllowOpen:
		return "start programs"
	case LinkAllowWrite:
		return "type into panes and change windows"
	case LinkAllowRespond:
		return "answer for the person"
	case LinkAllowFiles:
		return "read and write files"
	}
	return c
}

// DefaultLinkAllow is what a machine may do here when nothing says otherwise.
// It is what every link could do before the policy existed, less respond:
// answering a prompt for the person is opt-in. files is on: a machine that
// may type into a shell here can already move files, and the file verbs keep
// its writes to the receive folder unless files_roots says more.
var DefaultLinkAllow = []string{LinkAllowList, LinkAllowMail, LinkAllowOpen, LinkAllowWrite, LinkAllowFiles}

// DefaultHostedGrace is how long a pane run here for another machine outlives
// a dropped link when nothing says otherwise.
const DefaultHostedGrace = 10 * time.Minute

// MaxHostedGrace bounds hosted_grace, so a typo of hours cannot leave a
// process nobody can reach running for a week.
const MaxHostedGrace = 24 * time.Hour

// LinkPolicyDefaultName is the [hosts] key whose policy applies to every
// machine without a table of its own.
const LinkPolicyDefaultName = "*"

// LinkPolicy is what one machine may do here, resolved.
type LinkPolicy struct {
	// Peer is the name the machine arrived under, empty when it gave none.
	Peer string
	// Allow is the capabilities it has, a subset of LinkCapabilities.
	Allow []string
	// HoldMail holds its mail in the Inbox until the person passes it on.
	HoldMail bool
	// HostedGrace is how long a pane run for it outlives a dropped link.
	HostedGrace time.Duration
	// FilesRoots are the folders files_roots names, as written: absolute or
	// starting with ~. Nil is the receive folder, DefaultFilesRoot. Empty is
	// no folder at all.
	FilesRoots []string
	// Source names the table the policy came from, for an error that tells
	// the caller which key to change: hosts.laptop, hosts."*", or empty for
	// the built-in default.
	Source string
}

// Allows reports whether the policy grants every capability in caps.
func (p LinkPolicy) Allows(caps ...string) bool {
	for _, c := range caps {
		if !slices.Contains(p.Allow, c) {
			return false
		}
	}
	return true
}

// DefaultLinkPolicy is the policy with nothing configured.
func DefaultLinkPolicy() LinkPolicy {
	return LinkPolicy{Allow: slices.Clone(DefaultLinkAllow), HostedGrace: DefaultHostedGrace}
}

// LinkPolicyFor resolves the policy for a machine that linked in as peer:
// the built-in default, then [hosts."*"], then [hosts.PEER]. A field an entry
// leaves unset is inherited from the one before it. Unknown capabilities and
// an unreadable hosted_grace are ignored here; ValidateConfig reports them.
func LinkPolicyFor(hosts map[string]HostConfig, peer string) LinkPolicy {
	p := DefaultLinkPolicy()
	p.Peer = peer
	apply := func(key string, h HostConfig) {
		if !h.HasLinkPolicy() {
			return
		}
		if h.Allow != nil {
			p.Allow = cleanLinkAllow(h.Allow)
		}
		if h.HoldMail != nil {
			p.HoldMail = *h.HoldMail
		}
		if h.HostedGrace != "" {
			if d, err := ParseHostedGrace(h.HostedGrace); err == nil {
				p.HostedGrace = d
			}
		}
		if h.FilesRoots != nil {
			p.FilesRoots = cleanFilesRoots(h.FilesRoots)
		}
		p.Source = "hosts." + key
	}
	if h, ok := hosts[LinkPolicyDefaultName]; ok {
		apply(`"*"`, h)
	}
	if peer != "" && peer != LinkPolicyDefaultName {
		// A host name is not case sensitive, and the name a machine gives for
		// itself is sent lowered, so the key is matched the same way.
		if h, ok := hosts[peer]; ok {
			apply(peer, h)
		} else {
			for key, h := range hosts {
				if strings.EqualFold(key, peer) {
					apply(key, h)
					break
				}
			}
		}
	}
	return p
}

// cleanLinkAllow keeps the known capabilities, once each, in documented order.
func cleanLinkAllow(in []string) []string {
	out := make([]string, 0, len(in))
	for _, c := range LinkCapabilities {
		if slices.ContainsFunc(in, func(s string) bool { return strings.TrimSpace(s) == c }) {
			out = append(out, c)
		}
	}
	return out
}

// cleanFilesRoots keeps the roots that are absolute or start with ~, once
// each, cleaned. ValidateConfig reports the others.
func cleanFilesRoots(in []string) []string {
	out := make([]string, 0, len(in))
	for _, r := range in {
		r = strings.TrimSpace(r)
		if !validFilesRoot(r) {
			continue
		}
		if r != "~" && !strings.HasPrefix(r, "~/") {
			r = filepath.Clean(r)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func validFilesRoot(r string) bool {
	return r == "~" || strings.HasPrefix(r, "~/") || filepath.IsAbs(r)
}

// DefaultFilesRoot is the receive folder: where another machine may write
// files here when files_roots says nothing. It is a tuios folder in the
// downloads folder: ~/Downloads/tuios on macOS and Windows, and on other
// systems the XDG download folder ($XDG_DOWNLOAD_DIR, or the one
// user-dirs.dirs names) or ~/Downloads. It is empty when there is no home
// folder.
func DefaultFilesRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dl := filepath.Join(home, "Downloads")
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		if d := xdgDownloadDir(home); d != "" {
			dl = d
		}
	}
	return filepath.Join(dl, "tuios")
}

// xdgDownloadDir is the XDG download folder, or empty when none is set.
func xdgDownloadDir(home string) string {
	expand := func(v string) string {
		v = strings.Trim(strings.TrimSpace(v), `"`)
		if strings.HasPrefix(v, "$HOME") {
			v = home + v[len("$HOME"):]
		}
		if !filepath.IsAbs(v) {
			return ""
		}
		return filepath.Clean(v)
	}
	if v := os.Getenv("XDG_DOWNLOAD_DIR"); v != "" {
		return expand(v)
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	data, err := os.ReadFile(filepath.Join(cfg, "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "XDG_DOWNLOAD_DIR="); ok {
			return expand(v)
		}
	}
	return ""
}

// ParseHostedGrace reads hosted_grace: a Go duration, or "0" for none. A
// negative value is an error and a value past MaxHostedGrace is cut to it.
func ParseHostedGrace(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as \"10m\" or \"0\"", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is negative", s)
	}
	return min(d, MaxHostedGrace), nil
}

// validateLinkPolicies warns about a capability that does not exist and a
// hosted_grace that cannot be read. Either is ignored at run time, so a typo
// would otherwise quietly leave the default in place.
func validateLinkPolicies(cfg *UserConfig, result *ValidationResult) {
	for name, h := range cfg.Hosts {
		for _, c := range h.Allow {
			if !slices.Contains(LinkCapabilities, strings.TrimSpace(c)) {
				result.Warnings = append(result.Warnings, ValidationError{
					Field:   "hosts." + name,
					Key:     "allow",
					Message: fmt.Sprintf("'%s' is not a capability (allowed: %s); it is ignored", c, strings.Join(LinkCapabilities, ", ")),
				})
			}
		}
		for _, r := range h.FilesRoots {
			if !validFilesRoot(strings.TrimSpace(r)) {
				result.Warnings = append(result.Warnings, ValidationError{
					Field:   "hosts." + name,
					Key:     "files_roots",
					Message: fmt.Sprintf("'%s' is not an absolute path or a path that starts with ~; it is ignored", r),
				})
			}
		}
		if h.HostedGrace != "" {
			if _, err := ParseHostedGrace(h.HostedGrace); err != nil {
				result.Warnings = append(result.Warnings, ValidationError{
					Field:   "hosts." + name,
					Key:     "hosted_grace",
					Message: err.Error() + "; the inherited value is used",
				})
			}
		}
	}
}
