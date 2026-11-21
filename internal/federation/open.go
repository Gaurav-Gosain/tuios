package federation

import (
	"errors"
	"os"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

// SSHBinary is the ssh program to run. TUIOS_SSH overrides it, which is what
// the tests use to put a stand-in on the far end of every link and every open.
func SSHBinary() string {
	if s := os.Getenv("TUIOS_SSH"); s != "" {
		return s
	}
	return "ssh"
}

var remoteArgPattern = lazyre.New(`^[A-Za-z0-9._@%+=:,/-]+$`)

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
	if remoteArgPattern().MatchString(arg) {
		return arg, nil
	}
	for _, r := range arg {
		if r == '\'' || r == '\\' || r < 0x20 || r == 0x7f {
			return "", errfmt.Errorf("%q %w: rename it on the host to a name without quotes or backslashes", arg, ErrUnsafeRemoteArg)
		}
	}
	return "'" + arg + "'", nil
}

// OpenArgs is the argv, after the ssh binary, that opens a terminal on this
// host and runs its tuios with remote as the arguments: `-t <options> <addr>
// <command> <remote...>`.
//
// The command is the link's own: a configured one is passed as the [hosts]
// table spells it, unquoted, so a "~/.local/bin/tuios" is expanded by the
// remote shell on both paths, and without one the same probe the link runs
// finds the binary (see remote.go). The remote arguments are quoted by
// QuoteRemoteArg.
func (h Host) OpenArgs(remote ...string) ([]string, error) {
	secs := max(int(h.connectTimeout().Seconds()), 1)
	args := []string{
		"-t",
		"-o", "ConnectTimeout=" + strconv.Itoa(secs),
	}
	args = append(args, h.SSHOptions...)
	quoted := make([]string, 0, len(remote))
	for _, r := range remote {
		q, err := QuoteRemoteArg(r)
		if err != nil {
			return nil, err
		}
		quoted = append(quoted, q)
	}
	args = append(args, "--", h.Addr, h.remoteCommand(false, quoted...))
	return args, nil
}
