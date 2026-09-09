package federation

import (
	"strings"
)

// Finding tuios on the far side.
//
// ssh runs a command through a non-interactive shell, and that shell's PATH is
// the system default: /usr/local/bin, /usr/bin and their friends. It is not the
// PATH the person sees at their prompt, because the profile that adds
// ~/.local/bin is only read by a login shell. The project's own install script
// puts tuios in ~/.local/bin, so the default install location was one the
// default link could not find, and every such host needed --command to work.
//
// The link now finds the binary itself. The whole mechanism is one shell
// script sent as the remote command, which tries three things in order and
// then runs what it found with the arguments the link wanted to run:
//
//  1. Plain `tuios` on the PATH. Free, and right on most machines.
//  2. A fixed list of places tuios is installed. Each is one stat, nothing is
//     searched, and the list is bounded so a person can read it when it fails.
//  3. The person's login shell, asked what `tuios` means to it. This is the
//     step that reads the profile. It is last because it is the expensive one:
//     a profile can take seconds, can print, and differs between shells. The
//     fixed list resolves every install the project knows about without paying
//     that, so the login shell only runs on a machine where tuios is somewhere
//     unusual, which is exactly where its answer is worth the cost.
//
// The script prints which path it chose on a line ahead of the link preamble,
// so the hub can report it and can run it directly on the next dial. A
// configured command skips all of this: it is sent as written, exactly as it
// always was.

// RemoteBinary is the file name of the program the link runs on the far side.
const RemoteBinary = "tuios"

// remoteBinaryCandidates are the fixed places the link looks, in order, after
// the PATH. Every entry is a place one of the project's own installers puts
// the binary, or one the updater already recognises as an install origin (see
// internal/release's provenance.go). Nothing here is a search: each is one
// executable-bit test. $HOME and $USER are expanded by the remote shell.
var remoteBinaryCandidates = []string{
	// scripts/install.sh's default prefix, and the curl installer's second
	// choice. The one the report at the top of this file was about.
	"$HOME/.local/bin/" + RemoteBinary,
	// The curl installer's third choice, for a home with no ~/.local/bin.
	"$HOME/bin/" + RemoteBinary,
	// `go install` with the default GOPATH.
	"$HOME/go/bin/" + RemoteBinary,
	// The curl installer's first choice, and Homebrew's prefix on Intel macOS.
	"/usr/local/bin/" + RemoteBinary,
	// Homebrew on Apple silicon.
	"/opt/homebrew/bin/" + RemoteBinary,
	// Homebrew on Linux.
	"/home/linuxbrew/.linuxbrew/bin/" + RemoteBinary,
	// A per-user Nix profile.
	"$HOME/.nix-profile/bin/" + RemoteBinary,
	// home-manager on NixOS.
	"/etc/profiles/per-user/$USER/bin/" + RemoteBinary,
	// The default profile of a multi-user Nix install.
	"/nix/var/nix/profiles/default/bin/" + RemoteBinary,
	// The NixOS system profile.
	"/run/current-system/sw/bin/" + RemoteBinary,
	// A distribution package, such as the AUR build.
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

// linkCommandPrefix begins the line the probe prints ahead of the preamble to
// say which binary it is about to run.
const linkCommandPrefix = "TUIOS-LINK-COMMAND "

// linkNoCommand is the line the probe prints when it found nothing.
const linkNoCommand = "TUIOS-LINK-NO-COMMAND"

// linkCommandLimit bounds the path the probe reports. It comes from another
// machine, so it gets a ceiling like everything else that crosses.
const linkCommandLimit = 512

// remoteProbeScript is the sh script that finds tuios and runs it with the
// script's own positional arguments. announce is whether it prints the chosen
// path ahead of the program's output: the link wants that line, an interactive
// open does not.
//
// The script is one line, and it contains no single quote, no backslash, no
// newline and no exclamation mark. That is what lets it be sent inside single
// quotes through any login shell on the far side: sh, bash, zsh, fish and csh
// all read such a string literally. `case` is kept out of command
// substitutions, which older dash releases fail to parse there.
func remoteProbeScript(announce bool) string {
	var b strings.Builder
	write := func(s string) {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(s)
	}

	// 1. The PATH. The result is trusted only when it is an executable
	// absolute path: `command -v` can also name a function or an alias.
	write(`p=$(command -v ` + RemoteBinary + ` 2>/dev/null)`)
	write(`case $p in /*) [ -x "$p" ] || p=;; *) p=;; esac`)

	// 2. The fixed list.
	var quoted []string
	for _, c := range remoteBinaryCandidates {
		quoted = append(quoted, `"`+c+`"`)
	}
	write(`if [ -z "$p" ]; then for c in ` + strings.Join(quoted, " ") +
		`; do if [ -x "$c" ]; then p=$c; break; fi; done; fi`)

	// 3. The login shell, then plain sh as a login shell for a login shell
	// that is not POSIX. first keeps the first executable absolute path of
	// whatever the profile printed, so a greeting ahead of the answer is
	// skipped. stdin is /dev/null so a profile that reads cannot eat the
	// link's bytes.
	write(`first() { while IFS= read -r l; do case $l in /*) if [ -x "$l" ]; then printf "%s" "$l"; return; fi;; esac; done; }`)
	write(`if [ -z "$p" ]; then for s in "$SHELL" sh; do p=$("$s" -l -c "command -v ` + RemoteBinary +
		`" </dev/null 2>/dev/null | first); if [ -n "$p" ]; then break; fi; done; fi`)

	// Run it, or say that nothing was found. 127 is what a shell exits with
	// for a command it could not find, so a caller that already keys on it
	// reads this the same way.
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

// remoteCommand is the command string ssh runs on the far side to start tuios
// with args. args must already be safe for the remote shell.
//
// A configured command is sent as written, unquoted, so a "~/.local/bin/tuios"
// is expanded by the remote shell. Without one, the probe script runs as
// `sh -c '<script>' sh <args>`, and the script execs what it finds with the
// same args.
func (h Host) remoteCommand(announce bool, args ...string) string {
	if h.Command != "" {
		return strings.Join(append([]string{h.Command}, args...), " ")
	}
	parts := []string{"sh", "-c", "'" + remoteProbeScript(announce) + "'", "sh"}
	return strings.Join(append(parts, args...), " ")
}

// cacheableRemotePath reports whether a path the probe found can be sent back
// as the next dial's command without quoting. The remote shell re-parses the
// command, so only a path made of plain characters is safe to send bare; any
// other path is simply found again by the next probe.
func cacheableRemotePath(p string) bool {
	return p != "" && p[0] == '/' && remoteArgPattern.MatchString(p)
}

// preambleNote is what the hub learned from the lines ahead of the preamble.
type preambleNote struct {
	// command is the binary the probe said it would run, empty when the
	// remote command was configured and no probe ran.
	command string
	// missing is set when the probe said it found nothing.
	missing bool
}

// noteLine reads one line ahead of the preamble.
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
