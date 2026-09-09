package federation

import "strings"

// A target that names a machine.
//
// Every verb that takes a session takes it as `[host:]session`, and every verb
// that takes a window takes it as `[host:session:]window`. The qualifier is
// the name from the [hosts] table, or `local` for this machine. What follows
// the qualifier is passed to that machine's daemon exactly as written, so a
// session or window name with a colon in it is still reachable.
//
// The rule that decides whether a colon is a qualifier is fixed here and does
// not depend on which hosts are configured. That is deliberate: if `foo:bar`
// meant "session foo:bar here" until someone added a host called foo, adding a
// host would silently move an address to another machine. Instead the word
// before the first colon is a host whenever it could be one, an unknown host
// is refused by name, and `local:` is the spelling for a session on this
// machine whose name carries a colon.

// Target is a parsed address.
type Target struct {
	// Host is the machine named, or LocalHostName. Qualified says whether a
	// host was written at all, because "" and "local:" both mean this machine
	// and only one of them was chosen by the caller.
	Host      string
	Qualified bool
	// Session is the session name, "" for the machine's default. Window is
	// the window target, "" when none was written.
	Session string
	Window  string
}

// Remote reports whether the target names another machine.
func (t Target) Remote() bool {
	return t.Host != "" && t.Host != LocalHostName
}

// IsHostName reports whether s could be a host name: what the [hosts] table
// accepts, plus the reserved name for this machine.
func IsHostName(s string) bool {
	return s == LocalHostName || hostNamePattern.MatchString(s)
}

// ParseSessionTarget reads `[host:]session`.
//
// The part before the first colon is the host when it is `local` or could be
// a configured host name. Everything after it is the session, colons and all.
// A prefix that could not be a host name, such as one with a space, is not a
// qualifier, so the whole string is a session on this machine.
func ParseSessionTarget(s string) Target {
	before, after, ok := strings.Cut(s, ":")
	if !ok || !IsHostName(before) {
		return Target{Host: LocalHostName, Session: s}
	}
	return Target{Host: before, Qualified: true, Session: after}
}

// ParseWindowTarget reads `[host:session:]window`.
//
// A window is qualified only in the full three-part form, split at the first
// two colons: the host, then the session, then the window with any further
// colons kept. The session part must not be empty unless the host is `local`,
// so a window title like `https://example.com`, whose middle part is empty,
// stays a plain window on this machine. `local::NAME` is the spelling for a
// window on this machine whose own name looks qualified.
func ParseWindowTarget(s string) Target {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 3 || !IsHostName(parts[0]) {
		return Target{Host: LocalHostName, Window: s}
	}
	if parts[1] == "" && parts[0] != LocalHostName {
		return Target{Host: LocalHostName, Window: s}
	}
	return Target{Host: parts[0], Qualified: true, Session: parts[1], Window: parts[2]}
}

// SplitTarget is ParseSessionTarget for callers that want only the pair. It
// is kept so nothing that used it has to change.
func SplitTarget(addr string) (host, target string) {
	t := ParseSessionTarget(addr)
	return t.Host, t.Session
}
