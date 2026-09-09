package federation

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// Opening a session on another machine, the way a person does it by hand.
//
// Nothing here crosses the daemon's link. The link carries listings and refuses
// writes, and that rule is unchanged. What this file builds is the argv of a
// plain interactive ssh that runs tuios on the far side, so the session is
// attached by the remote tuios in a local terminal or pane. That is the
// tmux-over-ssh a person already types, spelled from the [hosts] table so the
// address, the options and the remote binary are typed once.
//
// Two things are deliberately not the link's: the ssh has a tty, so the far
// side can draw, and BatchMode is off, so ssh may ask a question. A pane can
// answer a prompt. A daemon cannot, which is why the link forbids them.

// SSHBinary is the ssh program to run. TUIOS_SSH overrides it, which is what
// the tests use to put a stand-in on the far end of every link and every open.
func SSHBinary() string {
	if s := os.Getenv("TUIOS_SSH"); s != "" {
		return s
	}
	return "ssh"
}

// remoteArgPattern is what a remote argument may be without quoting. It is
// the set every shell on the far side reads literally.
var remoteArgPattern = regexp.MustCompile(`^[A-Za-z0-9._@%+=:,/-]+$`)

// ErrUnsafeRemoteArg reports an argument the remote shell could read as more
// than one word or as a command.
var ErrUnsafeRemoteArg = errors.New("cannot be sent to a remote shell")

// QuoteRemoteArg makes one argument safe for the far side's shell. ssh joins
// its command arguments with spaces and hands the string to the login shell
// there, which re-parses it, so a session name with a space would arrive as
// two names.
//
// A plain word passes as is. A word with spaces or other characters is wrapped
// in single quotes, which every login shell tuios can be attached from reads
// literally. A word holding a single quote or a backslash is refused: sh and
// fish escape those differently inside single quotes, and there is no spelling
// that is right on both.
func QuoteRemoteArg(arg string) (string, error) {
	if arg == "" {
		return "''", nil
	}
	if remoteArgPattern.MatchString(arg) {
		return arg, nil
	}
	for _, r := range arg {
		if r == '\'' || r == '\\' || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%q %w: rename it on the host to a name without quotes or backslashes", arg, ErrUnsafeRemoteArg)
		}
	}
	return "'" + arg + "'", nil
}

// OpenArgs is the argv, after the ssh binary, that opens a terminal on this
// host and runs its tuios with remote as the arguments: `-t <options> <addr>
// <command> <remote...>`.
//
// The command is passed as the [hosts] table spells it, unquoted, exactly as
// the link passes it, so a "~/.local/bin/tuios" is expanded by the remote
// shell on both paths. The remote arguments are quoted by QuoteRemoteArg.
func (h Host) OpenArgs(remote ...string) ([]string, error) {
	secs := int(h.connectTimeout().Seconds())
	if secs < 1 {
		secs = 1
	}
	args := []string{
		"-t",
		"-o", "ConnectTimeout=" + strconv.Itoa(secs),
	}
	args = append(args, h.SSHOptions...)
	args = append(args, h.Addr, h.command())
	for _, r := range remote {
		q, err := QuoteRemoteArg(r)
		if err != nil {
			return nil, err
		}
		args = append(args, q)
	}
	return args, nil
}
