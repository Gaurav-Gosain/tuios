package federation

import (
	"strings"
)

// RemoteBinary is the file name of the program the link runs on the far side.
const RemoteBinary = "tuios"

var remoteBinaryCandidates = []string{
	"$HOME/.local/bin/" + RemoteBinary,
	"$HOME/bin/" + RemoteBinary,
	"$HOME/go/bin/" + RemoteBinary,
	"/usr/local/bin/" + RemoteBinary,
	"/opt/homebrew/bin/" + RemoteBinary,
	"/home/linuxbrew/.linuxbrew/bin/" + RemoteBinary,
	"$HOME/.nix-profile/bin/" + RemoteBinary,
	"/etc/profiles/per-user/$USER/bin/" + RemoteBinary,
	"/nix/var/nix/profiles/default/bin/" + RemoteBinary,
	"/run/current-system/sw/bin/" + RemoteBinary,
	"/usr/bin/" + RemoteBinary,
}

// RemoteBinaryCandidates lists the fixed places the link looks for tuios, in
// order, spelled the way a person reads them. It is what a failed test prints
// so the person can see at once that their install is somewhere unusual.
func RemoteBinaryCandidates() []string {
	out := make([]string, 0, len(remoteBinaryCandidates))
	for _, c := range remoteBinaryCandidates {
		out = append(out, strings.Replace(c, "$HOME/", "~/", 1))
	}
	return out
}

const linkCommandPrefix = "TUIOS-LINK-COMMAND "

const linkNoCommand = "TUIOS-LINK-NO-COMMAND"

const linkCommandLimit = 512

func remoteProbeScript(announce bool) string {
	var b strings.Builder
	write := func(s string) {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(s)
	}
	for _, s := range findBinaryStatements() {
		write(s)
	}

	if announce {
		write(`if [ -n "$p" ]; then echo "` + linkCommandPrefix + `$p"; exec "$p" "$@"; fi`)
		write(`echo "` + linkNoCommand + `"`)
	} else {
		write(`if [ -n "$p" ]; then exec "$p" "$@"; fi`)
		write(`echo "` + RemoteBinary + ` was not found on this host" >&2`)
	}
	write(`exit 127`)
	return b.String()
}

// FindBinaryScript is the part of the link's probe that finds tuios: sh
// statements, joined by "; ", that leave the path in $p, or $p empty when
// nothing was found. It obeys the same quoting rules as the probe, so a caller
// can put it inside single quotes for any login shell. tuios hosts sync uses
// it, so it looks for the binary exactly where the link does.
func FindBinaryScript() string {
	return strings.Join(findBinaryStatements(), "; ")
}

func findBinaryStatements() []string {
	var out []string
	write := func(s string) { out = append(out, s) }

	write(`p=$(command -v ` + RemoteBinary + ` 2>/dev/null)`)
	write(`case $p in /*) [ -x "$p" ] || p=;; *) p=;; esac`)

	var quoted []string
	for _, c := range remoteBinaryCandidates {
		quoted = append(quoted, `"`+c+`"`)
	}
	write(`if [ -z "$p" ]; then for c in ` + strings.Join(quoted, " ") +
		`; do if [ -x "$c" ]; then p=$c; break; fi; done; fi`)

	write(`first() { while IFS= read -r l; do case $l in /*) if [ -x "$l" ]; then printf "%s" "$l"; return; fi;; esac; done; }`)
	write(`if [ -z "$p" ]; then for s in "$SHELL" sh; do p=$("$s" -l -c "command -v ` + RemoteBinary +
		`" </dev/null 2>/dev/null | first); if [ -n "$p" ]; then break; fi; done; fi`)
	return out
}

func (h Host) remoteCommand(announce bool, args ...string) string {
	if h.Command != "" {
		return strings.Join(append([]string{h.Command}, args...), " ")
	}
	return ShellCommand(remoteProbeScript(announce), args...)
}

// ShellCommand is the command string that runs script under sh on the far
// side with args as its positional parameters: sh -c '<script>' sh <args>.
// script must follow remoteProbeScript's rules (no single quote, backslash,
// newline or exclamation mark), and every arg must already be safe for the
// remote shell; see SafeRemoteArg.
func ShellCommand(script string, args ...string) string {
	parts := []string{"sh", "-c", "'" + script + "'", "sh"}
	return strings.Join(append(parts, args...), " ")
}

// SafeRemoteArg reports whether s can be sent to the remote shell as one
// plain word.
func SafeRemoteArg(s string) bool {
	return remoteArgPattern().MatchString(s)
}

func cacheableRemotePath(p string) bool {
	return p != "" && p[0] == '/' && remoteArgPattern().MatchString(p)
}

type preambleNote struct {
	command string
	missing bool
}

func (n *preambleNote) noteLine(line string) {
	switch {
	case line == linkNoCommand:
		n.missing = true
	case strings.HasPrefix(line, linkCommandPrefix):
		p := strings.TrimSpace(strings.TrimPrefix(line, linkCommandPrefix))
		if len(p) > linkCommandLimit {
			p = p[:linkCommandLimit]
		}
		n.command = p
	}
}
