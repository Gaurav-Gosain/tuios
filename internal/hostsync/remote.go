// Package hostsync is the machine side of `tuios hosts sync`: the scripts it
// runs on a machine, the runner that carries them there, the Tailscale SSH
// gate on the way, and the source of the binaries it installs. The tuios
// command orchestrates a sync with it, and anything else that sets up a
// machine can call it the same way.
package hostsync

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Every script here is sent the way the link sends its own probe: one line of
// sh inside single quotes, with no single quote, backslash, newline or
// exclamation mark in it, so any login shell on the far side passes it to sh
// unchanged. See federation.ShellCommand. The arguments are checked words
// (federation.SafeRemoteArg) and never text from the far side.

// Runner runs one sh script on a machine.
type Runner interface {
	Run(ctx context.Context, script string, args []string, stdin io.Reader) (stdout, stderr string, err error)
}

// SSHRunner runs a script on a host over ssh, with the options the link
// uses, through TUIOS_SSH when that names a stand-in. Desk, when set, decides
// what to do when Tailscale SSH holds the login (see gate.go). Without
// one, such a call fails at once with the URL.
type SSHRunner struct {
	Host federation.Host
	Desk *ApprovalDesk
}

// Run runs script with args on the host, with stdin as its input.
func (r SSHRunner) Run(ctx context.Context, script string, args []string, stdin io.Reader) (string, string, error) {
	for _, a := range args {
		if !federation.SafeRemoteArg(a) {
			return "", "", errfmt.Errorf("the argument %q is not safe to send to the remote shell", a)
		}
	}
	cmd := exec.Command(federation.SSHBinary(), federation.SSHArgs(r.Host, federation.ShellCommand(script, args...))...)
	return RunGatedSSH(ctx, cmd, stdin, r.Host.Name, r.Desk)
}

// LocalRunner runs a script on this machine.
type LocalRunner struct{}

// Run runs script with args on this machine, with stdin as its input.
func (LocalRunner) Run(ctx context.Context, script string, args []string, stdin io.Reader) (string, string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{"-c", script, "sh"}, args...)...)
	return runSyncCmd(cmd, stdin)
}

// syncOutputLimit bounds what a script's output may grow to. The far side is
// another machine, so its output gets a ceiling like everything else that
// crosses.
const syncOutputLimit = 256 << 10

func runSyncCmd(cmd *exec.Cmd, stdin io.Reader) (string, string, error) {
	var out, errb limitedBuffer
	out.limit, errb.limit = syncOutputLimit, 8<<10
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != nil {
		cmd.Stdin = stdin
	}
	// A child that outlives a cancelled context is killed, and its pipes
	// are not waited on for long after that.
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return out.String(), strings.TrimSpace(errb.String()), err
}

// limitedBuffer keeps the first limit bytes written to it.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }

// Facts reads key=value lines. A key that repeats keeps every value in
// order.
type Facts map[string][]string

// ParseFacts reads the key=value lines of a script's output.
func ParseFacts(out string) Facts {
	f := Facts{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			continue
		}
		f[k] = append(f[k], v)
	}
	return f
}

// Get is the first value of k, or "" when the output has none.
func (f Facts) Get(k string) string {
	if v := f[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Has reports whether k has a value that is not empty.
func (f Facts) Has(k string) bool { return f.Get(k) != "" }

// syncShaFunc defines sum, which prints a file's sha256 with whichever tool the
// machine has, or nothing when it has none. Linux has sha256sum and macOS has
// shasum.
const syncShaFunc = `sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d " " -f 1; elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d " " -f 1; fi; }`

// syncFirstFunc keeps the first executable absolute path of what a login
// shell printed, so a greeting from a profile is skipped.
const syncFirstFunc = `first() { while IFS= read -r l; do case $l in /*) if [ -x "$l" ]; then printf "%s" "$l"; return; fi;; esac; done; }`

// syncTargetDir turns the first argument of the install and probe scripts
// into a path: "-" is ~/.local/bin, "H:rel" is a path under the home, and
// anything else is used as written.
const syncResolveArg = `case $1 in -) a=$HOME/.local/bin/tuios;; H:*) a=$HOME/${1#H:};; *) a=$1;; esac`

// syncServiceProbe says whether the machine has the daemon service of the
// default server (tuios daemon install-service): svc=installed, svc=none, or
// svc=unsupported where it has no service manager tuios knows. It reads the
// files the service is written to, as service.Get does.
const syncServiceProbe = `case $(uname -s) in Linux) if command -v systemctl >/dev/null 2>&1; then if [ -f "$HOME/.config/systemd/user/tuios-daemon.service" ] || [ -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/tuios-daemon.service" ]; then echo "svc=installed"; else echo "svc=none"; fi; else echo "svc=unsupported"; fi;; Darwin) if [ -f "$HOME/Library/LaunchAgents/dev.tuios.tuios-daemon.plist" ]; then echo "svc=installed"; else echo "svc=none"; fi;; *) echo "svc=unsupported";; esac`

// ServiceScript installs the daemon service with the binary in $1, as
// syncResolveArg reads it. It prints what install-service said, one out=
// line each, then svcok=1 or svcfail=1, and up=1 when a daemon answers.
func ServiceScript() string {
	return strings.Join([]string{
		syncResolveArg,
		`if o=$("$a" daemon install-service </dev/null 2>&1); then r=svcok; else r=svcfail; fi`,
		`echo "$o" | head -n 12 | while IFS= read -r l; do echo "out=$l"; done`,
		`echo "$r=1"`,
		`if "$a" daemon service-status --json </dev/null 2>/dev/null | grep -q '"daemon_pid"'; then echo "up=1"; fi`,
	}, "; ")
}

// ProbeScript reports what a machine has. $1 is the configured command
// as CommandArg spells it, or "-" to look for tuios the way the link does.
func ProbeScript() string {
	return strings.Join([]string{
		`echo "os=$(uname -s)"`,
		`echo "arch=$(uname -m)"`,
		`echo "home=$HOME"`,
		syncShaFunc,
		`if [ "$1" = - ]; then ` + federation.FindBinaryScript() + `; else ` + syncResolveArg + `; p=$a; echo "configured=$p"; if [ -x "$p" ]; then :; else p=; fi; fi`,
		`echo "path=$p"`,
		syncServiceProbe,
		`if [ -n "$p" ]; then if [ -L "$p" ]; then echo "symlink=1"; fi; if [ -w "$(dirname "$p")" ]; then echo "dirw=1"; fi; "$p" --version </dev/null 2>/dev/null | head -n 2 | while IFS= read -r l; do echo "ver=$l"; done; echo "sha=$(sum "$p")"; fi`,
	}, "; ")
}

// syncStopFunc defines stopd BINARY, which stops the daemon with BINARY.
// kill-server --restart tells the attached clients to wait for the new daemon
// and attach to it again. A binary older than the flag refuses it with
// "unknown flag" (or "Unknown flag" from fang before v0.8.0), and then stops the daemon without it, as runRestart does.
const syncStopFunc = `stopd() { if so=$("$1" kill-server --restart </dev/null 2>&1); then return 0; fi; case $so in *[Uu]"nknown flag"*) "$1" kill-server </dev/null >/dev/null 2>&1 ;; *) return 1 ;; esac; }`

// InstallScript receives a binary on stdin and puts it in place.
//
//	$1 the target, as syncResolveArg reads it
//	$2 the binary's sha256
//	$3 its size in bytes
//	$4 1 to restart the daemon, 0 to leave it
//	$5 the old binary, which stops the old daemon, or "-"
//
// The binary is written to a temp file in the target's own folder, checked,
// run once, and only then renamed onto the target. A rename is atomic and
// leaves the running daemon's file alone: the daemon keeps the file it
// opened. The temp file is removed on every way out.
func InstallScript() string {
	return strings.Join([]string{
		syncResolveArg,
		`d=$(dirname "$a")`,
		`mkdir -p "$d" || { echo "fail=mkdir"; exit 3; }`,
		`t=$(mktemp "$d/.tuios.sync.XXXXXX") || { echo "fail=mktemp"; exit 3; }`,
		`trap "rm -f $t" 0 1 2 15`,
		`cat > "$t" || { echo "fail=write"; exit 4; }`,
		`s=$(wc -c < "$t" | tr -d " ")`,
		`if [ "$s" = "$3" ]; then :; else echo "fail=size"; echo "got=$s"; exit 4; fi`,
		syncShaFunc,
		`h=$(sum "$t")`,
		`if [ -n "$h" ]; then if [ "$h" = "$2" ]; then :; else echo "fail=hash"; exit 4; fi; fi`,
		`chmod 755 "$t"`,
		`if v=$("$t" --version </dev/null 2>&1); then echo "staged=$(echo "$v" | head -n 1)"; else echo "fail=run"; echo "$v" | head -n 3 | while IFS= read -r l; do echo "out=$l"; done; exit 5; fi`,
		`stopped=0`,
		syncStopFunc,
		`if [ "$4" = 1 ]; then o=$5; if [ "$o" = - ]; then o=$t; fi; if stopd "$o"; then stopped=1; echo "stopped=1"; else echo "stopfail=1"; fi; fi`,
		`mv -f "$t" "$a" || { echo "fail=move"; exit 6; }`,
		`echo "target=$a"`,
		`if [ "$stopped" = 1 ]; then if "$a" start-server </dev/null >/dev/null 2>&1; then echo "started=1"; else echo "startfail=1"; fi; fi`,
		`"$a" --version </dev/null 2>&1 | head -n 1 | while IFS= read -r l; do echo "after=$l"; done`,
		syncFirstFunc,
		`q=; for s in "$SHELL" sh; do q=$("$s" -l -c "command -v tuios" </dev/null 2>/dev/null | first); if [ -n "$q" ]; then break; fi; done`,
		`echo "login=$q"`,
	}, "; ")
}

// RestartScript stops the daemon and starts it again with the binary in
// $1, as syncResolveArg reads it. kill-server saves every session first, and
// the new daemon restores them.
func RestartScript() string {
	return strings.Join([]string{
		syncResolveArg,
		syncStopFunc,
		`if stopd "$a"; then echo "stopped=1"; else echo "stopfail=1"; exit 7; fi`,
		`if "$a" start-server </dev/null >/dev/null 2>&1; then echo "started=1"; else echo "startfail=1"; exit 7; fi`,
	}, "; ")
}

// StartScript starts the daemon with the binary in $1, as syncResolveArg
// reads it. It is for a machine with no daemon: start-server refuses to start
// a second one.
func StartScript() string {
	return strings.Join([]string{
		syncResolveArg,
		`if "$a" start-server </dev/null >/dev/null 2>&1; then echo "started=1"; else echo "startfail=1"; exit 7; fi`,
	}, "; ")
}

// StartDaemonWith runs start-server on a machine with the binary arg, as
// syncResolveArg reads it. shown is the binary as a person types it, for the
// error.
func StartDaemonWith(ctx context.Context, runner Runner, arg, shown string) error {
	out, stderr, err := runner.Run(ctx, StartScript(), []string{arg}, nil)
	f := ParseFacts(out)
	switch {
	case f.Has("started"):
		return nil
	case f.Has("startfail"):
		return errfmt.Errorf("the daemon did not start. Run '%s start-server' on the host to see why", shown)
	}
	if err == nil {
		return errors.New("the start of the daemon did not finish")
	}
	return RemoteRunError("the start of the daemon failed", stderr, err)
}

// PaneScript reports, for each pane process in $@, the foreground
// process group of its terminal, the process's own name, and the name of
// that group's leader. A pane whose foreground group is not its own process
// runs a program, and so does a pane whose own process is not a shell.
func PaneScript() string {
	return strings.Join([]string{
		`for p in "$@"; do g=$(ps -o tpgid= -p "$p" 2>/dev/null | tr -d " "); c=$(ps -o comm= -p "$p" 2>/dev/null); if [ -z "$c" ]; then continue; fi; f=; if [ -n "$g" ] && [ "$g" -gt 0 ] 2>/dev/null; then f=$(ps -o comm= -p "$g" 2>/dev/null); fi; echo "pane=$p|$g|$c|$f"; done`,
	}, "; ")
}

// FindBinaryArg is what CommandArg returns for "look the way the link does":
// "-".
const FindBinaryArg = "-"

// CommandArg is how a configured command reaches the probe: "-" when
// there is none, so the probe looks the way the link does. A command that is
// not one plain path cannot be read as a file, and is reported as such.
func CommandArg(command string) (arg string, ok bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "-", true
	}
	if rel, found := strings.CutPrefix(command, "~/"); found && federation.SafeRemoteArg(rel) {
		return "H:" + rel, true
	}
	if strings.HasPrefix(command, "/") && federation.SafeRemoteArg(command) {
		return command, true
	}
	return "-", false
}

// ParseVersionLine reads the first line of `tuios --version`:
// "tuios version 0.8.0 [pure-Go backend]".
func ParseVersionLine(line string) (ver, backend string) {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "tuios version ")
	if i := strings.Index(line, "["); i >= 0 {
		if j := strings.Index(line[i:], " backend]"); j > 0 {
			backend = line[i+1 : i+j]
		}
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) > 0 {
		ver = fields[0]
	}
	return ver, backend
}

// GoPlatform maps `uname -s` and `uname -m` to GOOS and GOARCH.
func GoPlatform(unameS, unameM string) (goos, goarch string, err error) {
	switch strings.ToLower(strings.TrimSpace(unameS)) {
	case "linux":
		goos = "linux"
	case "darwin":
		goos = "darwin"
	case "freebsd":
		goos = "freebsd"
	default:
		return "", "", errfmt.Errorf("the host runs %q. tuios hosts sync supports Linux, macOS and FreeBSD hosts", unameS)
	}
	switch strings.TrimSpace(unameM) {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	case "i386", "i686":
		goarch = "386"
	case "riscv64":
		goarch = "riscv64"
	default:
		return "", "", errfmt.Errorf("the host has the architecture %q, which tuios hosts sync does not support", unameM)
	}
	return goos, goarch, nil
}

// RemoteRunError is one plain error for a script that failed, with what ssh
// or the far side said.
//
// A Tailscale SSH gate is its own error, whatever ssh did after it, because
// the last line ssh wrote ("Connection timed out") names the wrong cause.
func RemoteRunError(what, stderr string, err error) error {
	if ge := GateErrorOf(err); ge != nil {
		return ge
	}
	if ge := federation.GateFromStderr(stderr); ge != nil {
		return ge
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 255 {
		if f := federation.ClassifySSHFailure(stderr); f != "" {
			return &SSHFailError{What: what, Failure: f, Said: plainLine(LastLine(stderr))}
		}
		what += ": ssh could not connect"
	} else if errors.Is(err, context.DeadlineExceeded) {
		what += ": it did not answer in time"
	}
	if stderr != "" {
		return errfmt.Errorf("%s. ssh reported: %s", what, plainLine(LastLine(stderr)))
	}
	return errfmt.Errorf("%s: %v", what, err)
}

// SSHFailError is a script that ssh could not run, for a reason ssh named.
// Its kind is the error kind hosts sync reports.
type SSHFailError struct {
	// What is the step that failed.
	What string
	// Failure is the reason ssh named.
	Failure federation.SSHFailure
	// Said is the last line ssh wrote, on one line.
	Said string
}

func (e *SSHFailError) Error() string {
	return fmt.Sprintf("%s: %s ssh reported: %s", e.What, e.Failure.Reason(), e.Said)
}

// LastLine is the last line of s, with the space around s trimmed.
func LastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
