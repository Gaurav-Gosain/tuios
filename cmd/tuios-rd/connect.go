//go:build linux || darwin

package main

// tuios-rd connect HOST: from nothing to the remote desktop of a linked
// machine, through tuios. Each step is one line in the pane:
//
//  1. probe the host over the link (tuios run --host): system, engine,
//     token, port, whether the engine answers, and what it last logged;
//  2. install the engine when it is missing: a binary (--engine-bin) or a
//     build from source (--engine-src), copied with tuios cp;
//  3. start it as a user service on 127.0.0.1 with a token: a LaunchAgent on
//     macOS, a systemd user unit on Linux, or a plain process;
//  4. on macOS, show what to turn on in System Settings and wait until the
//     engine runs;
//  5. reach its port with tuios forward (ssh -L when the host's tuios is too
//     old), and show the desktop in this pane.
//
// The host keeps its files under XDG_STATE_HOME and XDG_DATA_HOME
// (~/.local/state/tuios-rd and ~/.local/share/tuios-rd). The token stays on
// the host; this side reads it over the link each time.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

const (
	launchdLabel = "dev.tuios.remote-desktop"
	systemdUnit  = "tuios-remote-desktop.service"
)

type connectCfg struct {
	host      string
	display   string
	all       bool
	check     bool
	engineBin string
	engineSrc string
	service   string // auto, launchd, systemd or process
	tuios     string
	viewArgs  []string
	out       io.Writer
}

// hostState is what the probe found.
type hostState struct {
	os, arch    string
	engine      bool
	token, port string
	up          bool
	needScreen  bool
	needInput   bool
	systemd     bool
	setsid      bool
	logTail     string
}

func connectUsage() {
	fmt.Fprint(os.Stderr, `Usage: tuios-rd connect HOST [flags] [viewer flags]

Shows the desktop of HOST, a machine in the [hosts] table of tuios, in this
pane. The first time, it installs the screen engine on HOST and starts it.

Flags:
  --display NAME     show this display of HOST (default: the main one)
  --all              open one pane for each display of HOST
  --check            do every step, read one frame, print the result and quit
  --engine-bin FILE  install this engine binary on HOST (built for HOST)
  --engine-src DIR   build the engine on HOST from this source folder
                     (default: $TUIOS_RD_ENGINE_SRC)
  --service KIND     auto, launchd, systemd or process (default: auto)

The other flags go to the viewer. Run tuios-rd --help to see them.
`)
}

// connectMain is tuios-rd connect. It returns the exit status.
func connectMain(args []string) int {
	c := connectCfg{service: "auto", out: os.Stdout, engineSrc: os.Getenv("TUIOS_RD_ENGINE_SRC")}
	c.tuios = os.Getenv("TUIOS_BIN")
	if c.tuios == "" {
		c.tuios = "tuios"
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		connectUsage()
		return 2
	}
	c.host = args[0]
	for i := 1; i < len(args); i++ {
		a := args[i]
		val := func() string {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "tuios-rd: %s needs a value\n", a)
				os.Exit(2)
			}
			i++
			return args[i]
		}
		switch a {
		case "--display":
			c.display = val()
		case "--all":
			c.all = true
		case "--check":
			c.check = true
		case "--engine-bin":
			c.engineBin = val()
		case "--engine-src":
			c.engineSrc = val()
		case "--service":
			c.service = val()
		case "-h", "--help":
			connectUsage()
			return 0
		default:
			c.viewArgs = append(c.viewArgs, a)
		}
	}
	switch c.service {
	case "auto", "launchd", "systemd", "process":
	default:
		fmt.Fprintln(os.Stderr, "tuios-rd: --service must be auto, launchd, systemd or process.")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	code, err := c.run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\ntuios-rd: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

func (c *connectCfg) step(format string, a ...any) {
	fmt.Fprintf(c.out, "  "+format, a...)
}

func (c *connectCfg) run(ctx context.Context) (int, error) {
	fmt.Fprintf(c.out, "Remote desktop on %s\n", c.host)

	c.step("Checking the host ... ")
	st, err := c.probe(ctx)
	if err != nil {
		fmt.Fprintln(c.out, "failed")
		return 1, err
	}
	fmt.Fprintf(c.out, "%s %s\n", systemName(st.os), st.arch)

	if !st.engine {
		c.step("Installing the engine ... ")
		if err := c.install(ctx, st); err != nil {
			fmt.Fprintln(c.out, "failed")
			return 1, err
		}
		fmt.Fprintln(c.out, "done")
	}

	if !st.up {
		c.step("Starting the engine ... ")
		if err := c.start(ctx, st); err != nil {
			fmt.Fprintln(c.out, "failed")
			return 1, err
		}
		st, err = c.waitUp(ctx, 10*time.Second)
		switch {
		case err != nil:
			fmt.Fprintln(c.out, "failed")
			return 1, err
		case st.needScreen || st.needInput:
			fmt.Fprintln(c.out, "waiting for permissions")
			if st, err = c.permissions(ctx, st); err != nil {
				return 1, err
			}
		case !st.up:
			fmt.Fprintln(c.out, "failed")
			return 1, c.notUp(st)
		default:
			fmt.Fprintln(c.out, "done")
		}
	}

	c.step("Opening the connection ... ")
	addr, closeTunnel, err := c.tunnel(ctx, st.port)
	if err != nil {
		fmt.Fprintln(c.out, "failed")
		return 1, err
	}
	defer closeTunnel()
	atExit = append(atExit, closeTunnel)
	fmt.Fprintln(c.out, addr)
	base := "http://" + addr

	if c.all {
		if err := c.openOtherDisplays(base, st.token); err != nil {
			fmt.Fprintf(c.out, "  %v\n", err)
		}
	}
	if c.check {
		return c.checkFrame(ctx, base, st.token)
	}
	args := append([]string{}, c.viewArgs...)
	args = append(args, "--token", st.token, "--why", c.whyCommand())
	if c.display != "" {
		args = append(args, "--output", c.display)
	}
	args = append(args, base)
	return viewMain(args), nil
}

func systemName(goos string) string {
	switch goos {
	case "Darwin":
		return "macOS"
	case "Linux":
		return "Linux"
	}
	return goos
}

// hostRun runs a shell script on the host through tuios run --host and
// returns its stdout.
func (c *connectCfg) hostRun(ctx context.Context, timeout time.Duration, script string) (string, error) {
	cmd := exec.CommandContext(ctx, c.tuios, "run", "--host", c.host, "--timeout", strconv.FormatInt(timeout.Milliseconds(), 10), "--", script)
	var out, errs strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errs.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(strings.TrimPrefix(msg, "tuios: "))
	}
	return out.String(), nil
}

// hostDirs is the shell prelude every host script starts with.
const hostDirs = `D="${XDG_STATE_HOME:-$HOME/.local/state}/tuios-rd"
E="${XDG_DATA_HOME:-$HOME/.local/share}/tuios-rd/engine"
mkdir -p "$D" "$E" && chmod 700 "$D"
`

const probeScript = hostDirs + `echo "os=$(uname -s)"
echo "arch=$(uname -m)"
[ -x "$E/relay-engine" ] && echo engine=yes
if [ ! -s "$D/token" ]; then (umask 077; od -An -N24 -tx1 /dev/urandom | tr -d ' \n' > "$D/token"); fi
echo "token=$(cat "$D/token")"
if [ ! -s "$D/port" ]; then echo $(( $(od -An -N2 -tu2 /dev/urandom) % 30000 + 20000 )) > "$D/port"; fi
echo "port=$(cat "$D/port")"
if command -v curl >/dev/null 2>&1; then
  curl -fsS -m 2 -o /dev/null -H "Authorization: Bearer $(cat "$D/token")" "http://127.0.0.1:$(cat "$D/port")/capabilities" 2>/dev/null && echo up=yes
else
  echo nocurl=yes
fi
if [ -f "$D/engine.log" ]; then
  n=$(awk '/^--- tuios-rd start/{n=NR} END{print n+0}' "$D/engine.log")
  t=$(tail -n +"$n" "$D/engine.log")
  case $t in *"Screen capture permission is missing"*) echo need_screen=yes;; esac
  case $t in *"Accessibility permission is missing"*) echo need_input=yes;; esac
  printf 'tail=%s\n' "$(printf '%s' "$t" | tail -n 3 | tr '\n' ' ')"
fi
command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1 && echo systemd=yes
command -v setsid >/dev/null 2>&1 && echo setsid=yes
true
`

func (c *connectCfg) probe(ctx context.Context) (hostState, error) {
	out, err := c.hostRun(ctx, 20*time.Second, probeScript)
	if err != nil {
		return hostState{}, err
	}
	return parseProbe(out), nil
}

func parseProbe(out string) hostState {
	var st hostState
	for line := range strings.SplitSeq(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "os":
			st.os = v
		case "arch":
			st.arch = v
		case "engine":
			st.engine = v == "yes"
		case "token":
			st.token = v
		case "port":
			st.port = v
		case "up":
			st.up = v == "yes"
		case "need_screen":
			st.needScreen = true
		case "need_input":
			st.needInput = true
		case "systemd":
			st.systemd = true
		case "setsid":
			st.setsid = true
		case "tail":
			st.logTail = strings.TrimSpace(v)
		}
	}
	return st
}

// copyToHost copies a local file to the host's receive folder with tuios cp
// and returns the path it got there.
func (c *connectCfg) copyToHost(ctx context.Context, local string) (string, error) {
	cmd := exec.CommandContext(ctx, c.tuios, "cp", "--json", "--conflict", "replace", local, c.host+":")
	var errs strings.Builder
	cmd.Stderr = &errs
	out, err := cmd.Output()
	var dst string
	for line := range strings.SplitSeq(string(out), "\n") {
		var ev struct {
			Event   string `json:"event"`
			Dst     string `json:"dst"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "done":
			dst = strings.TrimPrefix(ev.Dst, c.host+":")
		case "failed", "error":
			return "", fmt.Errorf("tuios cp to %s failed: %s", c.host, ev.Message)
		}
	}
	if err != nil || dst == "" {
		msg := strings.TrimSpace(errs.String())
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return "", fmt.Errorf("tuios cp to %s failed: %s", c.host, msg)
	}
	return dst, nil
}

// viewerFiles is the placeholder the engine wants in --viewer-dir. The
// browser viewer is not used here.
const viewerFiles = `mkdir -p "$E/viewer"
[ -f "$E/viewer/index.html" ] || echo '<!doctype html><title>tuios remote desktop</title><p>Open this desktop with tuios remote-desktop.</p>' > "$E/viewer/index.html"
[ -f "$E/viewer/viewer.js" ] || : > "$E/viewer/viewer.js"
`

// install puts the engine on the host, at a path that stays the same from
// one version to the next: macOS keeps a permission for a signed binary at
// the same path.
func (c *connectCfg) install(ctx context.Context, st hostState) error {
	switch {
	case c.engineBin != "":
		dst, err := c.copyToHost(ctx, c.engineBin)
		if err != nil {
			return err
		}
		script := hostDirs + viewerFiles + "f=" + shq(dst) + `
chmod 755 "$f" && mv -f "$f" "$E/relay-engine"
`
		_, err = c.hostRun(ctx, time.Minute, script)
		return err
	case c.engineSrc != "":
		return c.build(ctx, st)
	}
	return errors.New("there is no engine on " + c.host + " yet, and no source to build it from.\n" +
		"Give the screen-relay source with --engine-src DIR, or set TUIOS_RD_ENGINE_SRC.")
}

// build copies the engine source to the host and builds a tiles-only engine
// there (make X264=0).
func (c *connectCfg) build(ctx context.Context, st hostState) error {
	tgz, err := os.CreateTemp("", "tuios-rd-engine-*.tar.gz")
	if err != nil {
		return err
	}
	tgz.Close()
	defer os.Remove(tgz.Name())
	src := c.engineSrc
	var pack *exec.Cmd
	if _, err := os.Stat(filepath.Join(src, ".git")); err == nil {
		pack = childproc.Git(ctx, src, "archive", "--format=tar.gz", "-o", tgz.Name(), "HEAD", "--", "src", "Makefile", "packages.tsv")
	} else {
		pack = exec.CommandContext(ctx, "tar", "-czf", tgz.Name(), "-C", src, "src", "Makefile", "packages.tsv")
	}
	if out, err := pack.CombinedOutput(); err != nil {
		return fmt.Errorf("cannot pack the engine source in %s: %v %s", src, err, strings.TrimSpace(string(out)))
	}
	dst, err := c.copyToHost(ctx, tgz.Name())
	if err != nil {
		return err
	}
	script := hostDirs + viewerFiles + "f=" + shq(dst) + `
missing=""
for t in make c++ pkg-config; do command -v $t >/dev/null 2>&1 || missing="$missing $t"; done
for p in libwebsockets libzstd; do pkg-config --exists $p 2>/dev/null || missing="$missing $p"; done
if [ -n "$missing" ]; then echo "missing=$missing"; rm -f "$f"; exit 3; fi
rm -rf "$E/src" && mkdir -p "$E/src" && tar -xzf "$f" -C "$E/src" && rm -f "$f"
cd "$E/src" && make X264=0 > "$E/build.log" 2>&1 || { tail -n 15 "$E/build.log"; exit 4; }
cp -f relay-engine "$E/relay-engine.new" && mv -f "$E/relay-engine.new" "$E/relay-engine"
`
	out, err := c.hostRun(ctx, 20*time.Minute, script)
	if err != nil {
		if strings.Contains(out, "missing=") {
			return fmt.Errorf("%s needs build tools for the engine:%s\nInstall them on %s, then run this again:\n  %s",
				c.host, strings.TrimPrefix(strings.TrimSpace(out), "missing="), c.host, installHint(st.os))
		}
		return fmt.Errorf("the engine build on %s failed:\n%s", c.host, strings.TrimSpace(out))
	}
	return nil
}

func installHint(goos string) string {
	if goos == "Darwin" {
		return "brew install libwebsockets zstd pkg-config"
	}
	return "on Debian or Ubuntu: sudo apt install build-essential pkg-config libwebsockets-dev libzstd-dev libx11-dev libxext-dev libxdamage-dev libxfixes-dev libxtst-dev\n  on Arch: sudo pacman -S base-devel libwebsockets zstd libx11 libxext libxdamage libxfixes libxtst"
}

// shq quotes s for sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// serviceKind is the service manager to use on the host.
func (c *connectCfg) serviceKind(st hostState) string {
	if c.service != "auto" {
		return c.service
	}
	switch {
	case st.os == "Darwin":
		return "launchd"
	case st.systemd:
		return "systemd"
	}
	return "process"
}

// engineArgs are the engine's flags, as words for a shell line.
func engineArgs(st hostState) string {
	a := `--bind 127.0.0.1 --port "$(cat "$D/port")" --tiles --viewer-dir "$E/viewer" --token-file "$D/token"`
	if st.os == "Darwin" {
		a = "--backend macos " + a + " --scale gpu"
	}
	return a
}

// start starts the engine as a user service and marks the log, so the probe
// reads only what this start wrote.
func (c *connectCfg) start(ctx context.Context, st hostState) error {
	mark := `echo "--- tuios-rd start $(date)" >> "$D/engine.log"
`
	var script string
	switch c.serviceKind(st) {
	case "launchd":
		script = hostDirs + mark + `p="$HOME/Library/LaunchAgents/` + launchdLabel + `.plist"
mkdir -p "$HOME/Library/LaunchAgents"
cat > "$p" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + launchdLabel + `</string>
<key>ProgramArguments</key><array>
<string>$E/relay-engine</string><string>--backend</string><string>macos</string>
<string>--bind</string><string>127.0.0.1</string><string>--port</string><string>$(cat "$D/port")</string>
<string>--tiles</string><string>--scale</string><string>gpu</string>
<string>--viewer-dir</string><string>$E/viewer</string><string>--token-file</string><string>$D/token</string>
</array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><false/>
<key>ProcessType</key><string>Interactive</string>
<key>StandardOutPath</key><string>$D/engine.log</string>
<key>StandardErrorPath</key><string>$D/engine.log</string>
</dict></plist>
EOF
u=$(id -u)
if launchctl print "gui/$u/` + launchdLabel + `" >/dev/null 2>&1; then
  launchctl kickstart -k "gui/$u/` + launchdLabel + `"
else
  launchctl bootstrap "gui/$u" "$p"
fi
`
	case "systemd":
		script = hostDirs + mark + `u="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
mkdir -p "$u"
cat > "$u/` + systemdUnit + `" <<EOF
[Unit]
Description=tuios remote desktop engine

[Service]
ExecStart=$E/relay-engine ` + engineArgs(st) + `
StandardOutput=append:$D/engine.log
StandardError=append:$D/engine.log
Restart=no

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload && systemctl --user restart ` + systemdUnit + `
`
	default:
		if !st.setsid {
			return errors.New(c.host + " has no service manager tuios-rd can use and no setsid. Use --service launchd or systemd")
		}
		script = hostDirs + mark + `if [ -s "$D/engine.pid" ] && kill -0 "$(cat "$D/engine.pid")" 2>/dev/null; then kill "$(cat "$D/engine.pid")"; fi
setsid sh -c 'echo $$ > "$1/engine.pid"; shift; exec "$@"' sh "$D" "$E/relay-engine" ` + engineArgs(st) + ` >> "$D/engine.log" 2>&1 < /dev/null &
`
	}
	_, err := c.hostRun(ctx, time.Minute, script)
	return err
}

// waitUp probes until the engine answers, says what it needs, or d passes.
func (c *connectCfg) waitUp(ctx context.Context, d time.Duration) (hostState, error) {
	end := time.Now().Add(d)
	for {
		st, err := c.probe(ctx)
		if err != nil {
			return st, err
		}
		if st.up || st.needScreen || st.needInput || time.Now().After(end) {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (c *connectCfg) notUp(st hostState) error {
	msg := "the engine on " + c.host + " does not answer."
	if st.logTail != "" {
		msg += " It logged: " + st.logTail
	}
	return errors.New(msg)
}

// permissions shows the person what to turn on in System Settings on the
// Mac and waits until the engine runs. tuios-rd opens the right pane on the
// Mac. It never turns a switch on itself.
func (c *connectCfg) permissions(ctx context.Context, st hostState) (hostState, error) {
	engine := "~/.local/share/tuios-rd/engine/relay-engine"
	fmt.Fprintf(c.out, `
  The Mac needs two permissions for the remote desktop engine.
  System Settings is open on %[1]s at Privacy & Security. On %[1]s:

    1. In "Screen & System Audio Recording", turn on "relay-engine".
    2. In "Accessibility" (on newer macOS, "Device Control and Data Access"),
       turn on "relay-engine".

  If relay-engine is not in a list, click "+", press Cmd+Shift+G, type
  %[2]s and click "Open".
  macOS can ask for your password. Type it on the Mac.

  Waiting for the permissions. This goes on by itself. Press Ctrl+C to stop.
`, c.host, engine)
	pane := "Privacy_ScreenCapture"
	if !st.needScreen {
		pane = "Privacy_Accessibility"
	}
	_, _ = c.hostRun(ctx, 20*time.Second, "open 'x-apple.systempreferences:com.apple.preference.security?"+pane+"'")
	opened := pane
	for {
		select {
		case <-ctx.Done():
			return st, errors.New("stopped while waiting for the permissions")
		case <-time.After(3 * time.Second):
		}
		// A start asks again with the current switches. macOS shows its own
		// dialog only the first time.
		if err := c.start(ctx, st); err != nil {
			return st, err
		}
		next, err := c.waitUp(ctx, 3*time.Second)
		if err != nil {
			return st, err
		}
		st = next
		if st.up {
			fmt.Fprintln(c.out, "  The permissions are on. The engine runs.")
			return st, nil
		}
		if st.needInput && !st.needScreen && opened != "Privacy_Accessibility" {
			opened = "Privacy_Accessibility"
			_, _ = c.hostRun(ctx, 20*time.Second, "open 'x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility'")
		}
	}
}

// whyCommand is the --why line for the viewer: one line on why the engine
// does not answer, read from its log on the host.
func (c *connectCfg) whyCommand() string {
	script := hostDirs + `t=$(tail -n 20 "$D/engine.log" 2>/dev/null)
case $t in
*"permission is missing"*) echo "The engine on this host needs permissions. Run tuios remote-desktop again to see the steps.";;
*"display changed"*|*"no display"*) echo "The display of this host went away. Open the lid or connect a screen.";;
*) printf '%s\n' "$t" | grep -v '^---' | tail -n 1;;
esac`
	return shq(c.tuios) + " run --host " + shq(c.host) + " --timeout 10000 -- " + shq(script)
}

// tunnel reaches the engine's port with tuios forward, or with ssh -L when
// the host's tuios is older than tuios forward.
func (c *connectCfg) tunnel(ctx context.Context, port string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "tuios-rd-*")
	if err != nil {
		return "", nil, err
	}
	addrFile := filepath.Join(dir, "addr")
	cmd := exec.Command(c.tuios, "forward", c.host+":"+port, "--local", "0", "--addr-file", addrFile)
	var errs strings.Builder
	cmd.Stderr = &errs
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stopped := false
	stopFn := func() {
		if !stopped {
			stopped = true
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
				_ = cmd.Process.Kill()
			}
			os.RemoveAll(dir)
		}
	}
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); {
		if b, err := os.ReadFile(addrFile); err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b)), stopFn, nil
		}
		select {
		case <-exited:
			stopped = true
			os.RemoveAll(dir)
			msg := strings.TrimSpace(strings.TrimPrefix(errs.String(), "tuios: "))
			if strings.Contains(msg, "too old") || strings.Contains(msg, "unknown") {
				return c.sshTunnel(ctx, port)
			}
			return "", nil, errors.New(msg)
		case <-ctx.Done():
			stopFn()
			return "", nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	stopFn()
	return "", nil, errors.New("tuios forward did not start in 30 s")
}

// sshTunnel is ssh -L to the host's address from tuios hosts.
func (c *connectCfg) sshTunnel(ctx context.Context, port string) (string, func(), error) {
	out, err := exec.CommandContext(ctx, c.tuios, "hosts", "--json").Output()
	if err != nil {
		return "", nil, err
	}
	var hosts struct {
		Hosts []struct {
			Host string `json:"host"`
			Addr string `json:"addr"`
		} `json:"hosts"`
	}
	_ = json.Unmarshal(out, &hosts)
	addr := ""
	for _, h := range hosts.Hosts {
		if h.Host == c.host {
			addr = h.Addr
		}
	}
	if addr == "" {
		return "", nil, errors.New("tuios hosts has no address for " + c.host)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	local := ln.Addr().String()
	ln.Close()
	_, lport, _ := net.SplitHostPort(local)
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes", "-N", "-L", "127.0.0.1:"+lport+":127.0.0.1:"+port, addr)
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	stopFn := func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if conn, err := net.DialTimeout("tcp", local, time.Second); err == nil {
			conn.Close()
			return local, stopFn, nil
		}
	}
	stopFn()
	return "", nil, errors.New("the ssh tunnel to " + c.host + " did not start")
}

// capabilities reads /capabilities through the tunnel.
func capabilities(base, token string) (map[string]any, error) {
	req, _ := http.NewRequest("GET", base+"/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("the engine answered %s", resp.Status)
	}
	var m map[string]any
	return m, json.NewDecoder(resp.Body).Decode(&m)
}

// openOtherDisplays opens one more pane for each display but the first,
// with tuios new-window. This pane shows the first.
func (c *connectCfg) openOtherDisplays(base, token string) error {
	caps, err := capabilities(base, token)
	if err != nil {
		return err
	}
	outs, _ := caps["outputs"].([]any)
	if len(outs) < 2 {
		return nil
	}
	self, _ := os.Executable()
	for i, o := range outs {
		m, _ := o.(map[string]any)
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		if i == 0 {
			c.display = name
			continue
		}
		args := []string{"new-window", "Remote desktop on " + c.host + ": " + name, "--", self, "connect", c.host, "--display", name}
		if out, err := exec.Command(c.tuios, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("cannot open a pane for display %s: %v %s", name, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// checkFrame reads one frame from the engine and prints its size: the
// --check result.
func (c *connectCfg) checkFrame(ctx context.Context, base, token string) (int, error) {
	caps, err := capabilities(base, token)
	if err != nil {
		return 1, err
	}
	w, h, err := firstTilesFrame(ctx, base, token, c.display)
	if err != nil {
		return 1, err
	}
	fmt.Fprintf(c.out, "  Reading one frame ... %dx%d\n", w, h)
	fmt.Fprintf(c.out, "ok %s backend=%v frame=%dx%d\n", c.host, caps["backend"], w, h)
	return 0, nil
}
