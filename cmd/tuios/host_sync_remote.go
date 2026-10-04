package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The machine side of `tuios hosts sync`: the scripts it runs on a host, the
// runner that carries them there, and the reading of the host's daemon.
//
// Every script here is sent the way the link sends its own probe: one line of
// sh inside single quotes, with no single quote, backslash, newline or
// exclamation mark in it, so any login shell on the far side passes it to sh
// unchanged. See federation.ShellCommand. The arguments are checked words
// (federation.SafeRemoteArg) and never text from the far side.

// syncRunner runs one sh script on a machine.
type syncRunner interface {
	run(ctx context.Context, script string, args []string, stdin io.Reader) (stdout, stderr string, err error)
}

// sshSyncRunner runs a script on a host over ssh, with the options the link
// uses, through TUIOS_SSH when that names a stand-in.
type sshSyncRunner struct {
	host federation.Host
}

func (r sshSyncRunner) run(ctx context.Context, script string, args []string, stdin io.Reader) (string, string, error) {
	for _, a := range args {
		if !federation.SafeRemoteArg(a) {
			return "", "", fmt.Errorf("the argument %q is not safe to send to the remote shell", a)
		}
	}
	bin := os.Getenv("TUIOS_SSH")
	if bin == "" {
		bin = "ssh"
	}
	cmd := exec.CommandContext(ctx, bin, federation.SSHArgs(r.host, federation.ShellCommand(script, args...))...)
	return runSyncCmd(cmd, stdin)
}

// localSyncRunner runs a script on this machine.
type localSyncRunner struct{}

func (localSyncRunner) run(ctx context.Context, script string, args []string, stdin io.Reader) (string, string, error) {
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

// syncFacts reads key=value lines. A key that repeats keeps every value in
// order.
type syncFacts map[string][]string

func parseSyncFacts(out string) syncFacts {
	f := syncFacts{}
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

func (f syncFacts) get(k string) string {
	if v := f[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (f syncFacts) has(k string) bool { return f.get(k) != "" }

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

// syncProbeScript reports what a machine has. $1 is the configured command
// as syncCommandArg spells it, or "-" to look for tuios the way the link does.
func syncProbeScript() string {
	return strings.Join([]string{
		`echo "os=$(uname -s)"`,
		`echo "arch=$(uname -m)"`,
		`echo "home=$HOME"`,
		syncShaFunc,
		`if [ "$1" = - ]; then ` + federation.FindBinaryScript() + `; else ` + syncResolveArg + `; p=$a; echo "configured=$p"; if [ -x "$p" ]; then :; else p=; fi; fi`,
		`echo "path=$p"`,
		`if [ -n "$p" ]; then if [ -L "$p" ]; then echo "symlink=1"; fi; if [ -w "$(dirname "$p")" ]; then echo "dirw=1"; fi; "$p" --version </dev/null 2>/dev/null | head -n 2 | while IFS= read -r l; do echo "ver=$l"; done; echo "sha=$(sum "$p")"; fi`,
	}, "; ")
}

// syncInstallScript receives a binary on stdin and puts it in place.
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
func syncInstallScript() string {
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
		`if [ "$4" = 1 ]; then o=$5; if [ "$o" = - ]; then o=$t; fi; if "$o" kill-server </dev/null >/dev/null 2>&1; then stopped=1; echo "stopped=1"; else echo "stopfail=1"; fi; fi`,
		`mv -f "$t" "$a" || { echo "fail=move"; exit 6; }`,
		`echo "target=$a"`,
		`if [ "$stopped" = 1 ]; then if "$a" start-server </dev/null >/dev/null 2>&1; then echo "started=1"; else echo "startfail=1"; fi; fi`,
		`"$a" --version </dev/null 2>&1 | head -n 1 | while IFS= read -r l; do echo "after=$l"; done`,
		syncFirstFunc,
		`q=; for s in "$SHELL" sh; do q=$("$s" -l -c "command -v tuios" </dev/null 2>/dev/null | first); if [ -n "$q" ]; then break; fi; done`,
		`echo "login=$q"`,
	}, "; ")
}

// syncRestartScript stops the daemon and starts it again with the binary in
// $1, as syncResolveArg reads it. kill-server saves every session first, and
// the new daemon restores them.
func syncRestartScript() string {
	return strings.Join([]string{
		syncResolveArg,
		`if "$a" kill-server </dev/null >/dev/null 2>&1; then echo "stopped=1"; else echo "stopfail=1"; exit 7; fi`,
		`if "$a" start-server </dev/null >/dev/null 2>&1; then echo "started=1"; else echo "startfail=1"; exit 7; fi`,
	}, "; ")
}

// syncPaneScript reports, for each pane process in $@, the foreground
// process group of its terminal, the process's own name, and the name of
// that group's leader. A pane whose foreground group is not its own process
// runs a program, and so does a pane whose own process is not a shell.
func syncPaneScript() string {
	return strings.Join([]string{
		`for p in "$@"; do g=$(ps -o tpgid= -p "$p" 2>/dev/null | tr -d " "); c=$(ps -o comm= -p "$p" 2>/dev/null); if [ -z "$c" ]; then continue; fi; f=; if [ -n "$g" ] && [ "$g" -gt 0 ] 2>/dev/null; then f=$(ps -o comm= -p "$g" 2>/dev/null); fi; echo "pane=$p|$g|$c|$f"; done`,
	}, "; ")
}

// syncCommandArg is how a configured command reaches the probe: "-" when
// there is none, so the probe looks the way the link does. A command that is
// not one plain path cannot be read as a file, and is reported as such.
func syncCommandArg(command string) (arg string, ok bool) {
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

// parseVersionLine reads the first line of `tuios --version`:
// "tuios version 0.8.0 [pure-Go backend]".
func parseVersionLine(line string) (ver, backend string) {
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

// goPlatform maps `uname -s` and `uname -m` to GOOS and GOARCH.
func goPlatform(unameS, unameM string) (goos, goarch string, err error) {
	switch strings.ToLower(strings.TrimSpace(unameS)) {
	case "linux":
		goos = "linux"
	case "darwin":
		goos = "darwin"
	case "freebsd":
		goos = "freebsd"
	default:
		return "", "", fmt.Errorf("the host runs %q. tuios hosts sync supports Linux, macOS and FreeBSD hosts", unameS)
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
		return "", "", fmt.Errorf("the host has the architecture %q, which tuios hosts sync does not support", unameM)
	}
	return goos, goarch, nil
}

// probeHostBinary runs the probe script and fills in what it found.
func probeHostBinary(ctx context.Context, t *syncTarget) error {
	arg, plain := syncCommandArg(t.host.Command)
	if !plain {
		t.note("The host has a command set in the config that is not one path. Sync looks for tuios the way the link does without one.")
	}
	out, stderr, err := t.runner.run(ctx, syncProbeScript(), []string{arg}, nil)
	if err != nil {
		return remoteRunError("could not read the host", stderr, err)
	}
	f := parseSyncFacts(out)
	goos, goarch, err := goPlatform(f.get("os"), f.get("arch"))
	t.res.OS, t.res.Arch = goos, goarch
	if t.res.OS == "" {
		t.res.OS = strings.ToLower(f.get("os"))
		t.res.Arch = f.get("arch")
	}
	if err != nil {
		return err
	}
	t.home = f.get("home")
	t.configured = f.get("configured")
	t.res.Path = f.get("path")
	t.symlink = f.has("symlink")
	t.dirWritable = f.has("dirw")
	t.sha = f.get("sha")
	if vers := f["ver"]; len(vers) > 0 {
		t.res.Before, t.res.BeforeBackend = parseVersionLine(vers[0])
	}
	if t.res.Path != "" && t.res.Before == "" {
		t.note("The tuios on the host did not report a version.")
	}
	return nil
}

// remoteRunError is one plain error for a script that failed, with what ssh
// or the far side said.
func remoteRunError(what, stderr string, err error) error {
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 255 {
		what += ": ssh could not connect"
	} else if errors.Is(err, context.DeadlineExceeded) {
		what += ": it did not answer in time"
	}
	if stderr != "" {
		return fmt.Errorf("%s. ssh reported: %s", what, plainLine(lastLine(stderr)))
	}
	return fmt.Errorf("%s: %v", what, err)
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// daemonCaller runs one verb on the machine's daemon.
type daemonCaller func(ctx context.Context, verb string, params any) (json.RawMessage, error)

// readHostDaemon reads the daemon on a host over a link this process opens,
// the way 'tuios hosts test' does. The link runs the host's own tuios, so
// nothing here starts a daemon there.
func readHostDaemon(ctx context.Context, t *syncTarget) {
	table, _ := federation.NewTable([]federation.Host{t.host})
	m := federation.New(table, federation.Options{
		Dial:            federation.SSHDialer(os.Getenv("TUIOS_SSH")),
		ClientName:      "tuios-hosts-sync",
		ClientVersion:   version,
		VerbProtocol:    session.VerbProtocolVersion,
		MinVerbProtocol: session.MinVerbProtocolVersion,
	})
	dialCtx, cancel := context.WithTimeout(ctx, linkSettleBudget(t.host))
	defer cancel()
	m.Start(dialCtx)
	defer m.Stop()
	reports := m.Reports(dialCtx)
	if len(reports) == 0 {
		t.res.Daemon.State = daemonUnknown
		t.daemonNote = "The link to the host did not report a state."
		return
	}
	r := reports[0]
	switch r.Status {
	case federation.StatusUp:
		t.res.Daemon.State = daemonRunning
		t.res.Daemon.Version = r.DaemonVersion
		t.res.Daemon.PID = r.PID
		t.res.Daemon.SessionCount = r.Sessions
		call := func(ctx context.Context, verb string, params any) (json.RawMessage, error) {
			return m.Call(ctx, t.name, verb, params)
		}
		readDaemonSessions(ctx, t, call)
	case federation.StatusNoDaemon, federation.StatusNoBinary:
		t.res.Daemon.State = daemonStopped
	case federation.StatusIncompatible:
		t.res.Daemon.State = daemonRunning
		t.res.Daemon.Version = r.DaemonVersion
		t.res.Daemon.PID = r.PID
		t.res.Daemon.SessionCount = r.Sessions
		t.res.Daemon.Unread = true
		t.note("The daemon speaks a control protocol this build does not serve, so its sessions could not be read.")
	default:
		t.res.Daemon.State = daemonUnknown
		reason := r.Reason
		if reason == "" {
			reason = "The link did not come up."
		}
		t.daemonNote = "The daemon could not be read. " + reason
	}
}

// linkSettleBudget is how long the link to one host may take to come up.
func linkSettleBudget(h federation.Host) time.Duration {
	timeout := h.ConnectTimeout
	if timeout <= 0 {
		timeout = federation.DefaultConnectTimeout
	}
	return timeout + 10*time.Second
}

// readLocalDaemon reads this machine's daemon over its socket.
func readLocalDaemon(ctx context.Context, t *syncTarget) {
	if !session.IsDaemonRunning() {
		t.res.Daemon.State = daemonStopped
		return
	}
	t.res.Daemon.State = daemonRunning
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		if mismatch, ok := errors.AsType[*session.ProtocolMismatchError](err); ok {
			t.res.Daemon.Version = mismatch.DaemonVersion
			t.res.Daemon.PID = mismatch.DaemonPID
			t.res.Daemon.SessionCount = mismatch.Sessions
			t.res.Daemon.Unread = true
			t.note("The daemon speaks a control protocol this build does not serve, so its sessions could not be read.")
			return
		}
		t.res.Daemon.State = daemonUnknown
		t.note("The daemon could not be read: " + err.Error())
		return
	}
	defer func() { _ = client.Close() }()
	call := func(_ context.Context, verb string, params any) (json.RawMessage, error) {
		return client.Call(verb, params)
	}
	raw, err := call(ctx, "hello", map[string]any{"client": "tuios-hosts-sync", "version": version, "protocol": session.VerbProtocolVersion})
	if err == nil {
		var hs struct {
			DaemonVersion string `json:"daemon_version"`
			PID           int    `json:"pid"`
			Sessions      int    `json:"sessions"`
		}
		if json.Unmarshal(raw, &hs) == nil {
			t.res.Daemon.Version = hs.DaemonVersion
			t.res.Daemon.PID = hs.PID
			t.res.Daemon.SessionCount = hs.Sessions
		}
	}
	readDaemonSessions(ctx, t, call)
}

// readDaemonSessions lists the daemon's sessions and the panes in each that
// run a program. A pane counts as running one when its terminal's foreground
// process is not the pane's own shell, or when the pane's own process is not
// a shell at all.
func readDaemonSessions(ctx context.Context, t *syncTarget, call daemonCaller) {
	raw, err := call(ctx, "list-sessions", nil)
	if err != nil {
		t.res.Daemon.Unread = true
		t.note("The daemon did not list its sessions: " + plainLine(err.Error()))
		return
	}
	var list struct {
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.res.Daemon.Unread = true
		return
	}
	type paneRef struct {
		session int
		window  string
		facts   paneFacts
	}
	var refs []paneRef
	var pids []string
	sessions := make([]syncSession, 0, len(list.Sessions))
	for _, s := range list.Sessions {
		ss := syncSession{Name: plainLine(s.Name)}
		raw, err := call(ctx, "list-windows", map[string]any{"session": s.Name})
		if err == nil {
			var wl struct {
				Windows []struct {
					DisplayName string `json:"display_name"`
					WindowID    string `json:"window_id"`
					PID         int    `json:"pid"`
					AtPrompt    *bool  `json:"at_prompt"`
					Running     string `json:"running_cmdline"`
				} `json:"windows"`
			}
			if json.Unmarshal(raw, &wl) == nil {
				ss.Panes = len(wl.Windows)
				for _, w := range wl.Windows {
					name := plainLine(w.DisplayName)
					if name == "" {
						name = shortWindowID(w.WindowID)
					}
					f := paneFacts{pid: w.PID, running: plainLine(w.Running)}
					if w.AtPrompt != nil {
						f.atPrompt, f.promptKnown = *w.AtPrompt, true
					}
					refs = append(refs, paneRef{session: len(sessions), window: name, facts: f})
					if w.PID > 0 {
						pids = append(pids, strconv.Itoa(w.PID))
					}
				}
			}
		}
		sessions = append(sessions, ss)
	}
	procs := map[int]paneProc{}
	if len(pids) > 0 {
		out, _, err := t.runner.run(ctx, syncPaneScript(), pids, nil)
		if err == nil {
			procs = parsePaneProcs(out)
		}
	}
	for _, r := range refs {
		if program, busy := paneBusy(r.facts, procs); busy {
			sessions[r.session].Busy = append(sessions[r.session].Busy, syncPane{Window: r.window, Program: program})
		}
	}
	t.res.Daemon.Sessions = sessions
	t.res.Daemon.SessionCount = len(sessions)
}

// paneFacts is what the daemon said about one pane.
type paneFacts struct {
	pid         int
	running     string
	atPrompt    bool
	promptKnown bool
}

// paneProc is what ps said about one pane's process.
type paneProc struct {
	tpgid      int
	comm       string
	foreground string
}

func parsePaneProcs(out string) map[int]paneProc {
	procs := map[int]paneProc{}
	for _, line := range parseSyncFacts(out)["pane"] {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		tpgid, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		procs[pid] = paneProc{tpgid: tpgid, comm: strings.TrimSpace(parts[2]), foreground: strings.TrimSpace(parts[3])}
	}
	return procs
}

// shellNames are the programs a pane runs at its prompt.
var shellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"mksh": true, "tcsh": true, "csh": true, "nu": true, "elvish": true, "xonsh": true,
	"pwsh": true, "ash": true, "yash": true, "oksh": true,
}

func programName(comm string) string {
	return strings.TrimPrefix(path.Base(strings.TrimSpace(comm)), "-")
}

// paneBusy decides whether a pane runs a program, and which.
func paneBusy(f paneFacts, procs map[int]paneProc) (string, bool) {
	if p, ok := procs[f.pid]; ok && f.pid > 0 {
		own := programName(p.comm)
		if p.tpgid > 0 && p.tpgid != f.pid {
			if fg := programName(p.foreground); fg != "" {
				return fg, true
			}
			return "a program", true
		}
		if !shellNames[own] {
			return own, true
		}
		return "", false
	}
	// No process to read: what the shell integration said, when it said it.
	if f.running != "" {
		return f.running, true
	}
	if f.promptKnown && !f.atPrompt {
		return "a program", true
	}
	return "", false
}
