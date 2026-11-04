package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Gaurav-Gosain/tuios/internal/guios/shell"
)

// Errors a dial tells apart, for the app to say what to do.
var (
	// ErrNotInstalled: the host answered, but has no kero to run.
	ErrNotInstalled = errors.New("Kero is not installed on the host")
	// ErrAuth: the host refused us, or its key is not one ssh trusts yet.
	ErrAuth = errors.New("the host refused the connection")
)

// MuxPath is where kerod's network listeners take remote connections, as
// a WebSocket.
const MuxPath = "/kero/mux"

// ClientHeader is the header a client sends with its WebSocket handshake:
// browsers cannot send one, so that no web page reaches kerod through a
// browser on the tailnet.
const ClientHeader = "Kero-Client"

// BridgeLocations are where the bridge command looks for kero on a host,
// in order: a server install, the command line tool a desktop install
// links, the PATH, the apps. The last are of Windows, whose sh, as Git's,
// has its home in the user's profile.
var BridgeLocations = []string{
	`$HOME/.local/bin/kero`,
	`$HOME/.config/kero/bin/kero`,
	`$HOME/Library/Application Support/kero/bin/kero`,
	`kero`,
	`/usr/local/bin/kero`,
	`/usr/bin/kero`,
	`/Applications/Kero.app/Contents/MacOS/Kero`,
	`$HOME/AppData/Local/Programs/kero-server/kero`,
	`$HOME/AppData/Roaming/kero/bin/kero`,
}

// bridgeNotFound is how the bridge command says there is no kero, as its
// exit status: a shell's for a command not found.
const bridgeNotFound = 127

// The shells of Windows, which its SSH server runs commands with: cmd, its
// default, or PowerShell. Neither takes sh -c.
const (
	shellCmd        = "cmd"
	shellPowerShell = "powershell"
)

// shells remembers the hosts whose SSH server runs commands with a shell
// of Windows (address → shell), for their next connections to run the
// bridge at once.
var shells sync.Map

func hostShell(a Addr) string {
	s, _ := shells.Load(a.String())
	sh, _ := s.(string)
	return sh
}

// Windows reports whether a host is known to run Windows, as its SSH
// server runs commands with cmd or PowerShell: what a connection found.
func Windows(a Addr) bool { return hostShell(a) != "" }

// bridgeCommand is the command that runs kero's bridge on a host over SSH,
// for the shell that runs it there: sh, whatever the user's shell, so that
// fish and csh take it too, else Windows' cmd or PowerShell.
func bridgeCommand(a Addr, shell string) string {
	switch shell {
	case shellCmd:
		return cmdBridge(a)
	case shellPowerShell:
		return powershellBridge(a)
	}
	return "sh -c " + shellQuote(bridgeScript(a, "ssh"))
}

// cmdBridge is the bridge's command for cmd: kero where install-server.ps1
// puts it, then the desktop app's command line tool, then the PATH's.
// Paths are quoted, which no %VAR% of them, as a user's name, can end;
// the command starts with no quote, which cmd /c would take off.
func cmdBridge(a Addr) string {
	const run = " __bridge --via ssh"
	const missing = `echo kero is not installed on %COMPUTERNAME% 1>&2 & exit /b 127`
	locs := []string{`%LOCALAPPDATA%\Programs\kero-server\kero.exe`, `%APPDATA%\kero\bin\kero.exe`}
	// The PATH's kero.exe, the PATH's only, not the directory's, exits as
	// the bridge does, which may be with an error of its own, never taken
	// for a kero missing.
	s := `((for %k in (kero.exe) do @if not "%~$PATH:k"=="" ("%~$PATH:k"` + run + ` & exit /b)) & ` + missing + `)`
	if a.Kero != "" {
		locs, s = []string{a.Kero}, `(`+missing+`)`
	}
	for i := len(locs) - 1; i >= 0; i-- {
		s = `if exist "` + locs[i] + `" ("` + locs[i] + `"` + run + `) else ` + s
	}
	return s
}

// powershellBridge is the bridge's command for PowerShell, which looks
// where cmdBridge does. It has no double quotes, which the SSH server
// would not keep as it passes the command to PowerShell.
func powershellBridge(a Addr) string {
	locs := `($env:LOCALAPPDATA + '\Programs\kero-server\kero.exe'), ($env:APPDATA + '\kero\bin\kero.exe')`
	path := `if (-not $k) { $k = (Get-Command kero -CommandType Application -ErrorAction Ignore | Select-Object -First 1).Source }; `
	if a.Kero != "" {
		locs, path = shell.PSQuote(a.Kero), ""
	}
	return `$k = @(` + locs + `) | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1; ` + path +
		`if (-not $k) { [Console]::Error.WriteLine('kero is not installed on ' + $env:COMPUTERNAME); exit 127 }; ` +
		`& $k __bridge --via ssh; exit $LASTEXITCODE`
}

// ansiEscape matches the colors PowerShell 7 writes its errors in.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// quotedSh matches sh, quoted, as cmd names a command it does not know,
// in whatever language: 'sh' is not recognized, "sh" no se reconoce.
var quotedSh = regexp.MustCompile(`['"“”«»「」]sh['"“”«»「」]`)

// psCommandError matches PowerShell's error for a command it does not
// know: 5's, which names the exception, and 7's, "sh: " and what it says
// in the language of the host, as in its code page, which no Unix sh's
// error is: dash's "sh: 1: …", bash's "sh: line 1: …" or "sh: -c: …".
var psCommandError = regexp.MustCompile(`CommandNotFoundException|(?m)^sh: (?:[^0-9l\-]|l[^i])`)

// windowsShell tells, from how the bridge's sh -c ended, the shell of
// Windows that ran it, which has no sh: "" for any other end. cmd and
// PowerShell exit with 1 for a command they do not know; the bridge's sh
// never does, nor the SSH server, whose failures exit with 255.
func windowsShell(code int, stderr string) string {
	if code != 1 {
		return ""
	}
	s := ansiEscape.ReplaceAllString(stderr, "")
	switch {
	case psCommandError.MatchString(s):
		return shellPowerShell
	case quotedSh.MatchString(s):
		return shellCmd
	}
	return ""
}

// shellError is the end of a bridge that the host's shell, one of Windows,
// could not run: Dial runs the bridge again, as that shell takes it.
type shellError struct {
	shell string
	err   error
}

func (e *shellError) Error() string { return e.err.Error() }
func (e *shellError) Unwrap() error { return e.err }

// bridgeScript is the script of sh that finds kero and runs its bridge.
func bridgeScript(a Addr, via string) string {
	run := " __bridge --via " + via
	var script string
	if a.Kero != "" {
		script = "exec " + shellQuote(a.Kero) + run
	} else {
		// No character cmd reads as its own, as > & | " %, which it would
		// act on before it says it has no sh: words split at colons only,
		// the spaces of paths escaped.
		var locs []string
		for _, l := range BridgeLocations {
			locs = append(locs, strings.ReplaceAll(l, " ", `\ `))
		}
		script = `IFS=:; for k in ` + strings.Join(locs, " ") + `; do if k=$(command -v $k); then exec $k` + run + `; fi; done; exit ` + strconv.Itoa(bridgeNotFound)
	}
	return script
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// sshArgs are the arguments of ssh(1) that run the bridge on a host. It
// never asks anything, as there is no one to answer in the app: a key
// with a passphrase needs the agent, and a host's key needs trusting in a
// terminal first. It notices a dead link itself, as the mux does.
func sshArgs(a Addr) []string {
	args := []string{"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ClearAllForwardings=yes",
	}
	if a.Port != "" {
		args = append(args, "-p", a.Port)
	}
	if a.User != "" {
		args = append(args, "-l", a.User)
	}
	return append(args, "--", a.Host, bridgeCommand(a, hostShell(a)))
}

// SSHCommand is the program that runs ssh: $KERO_SSH, else ssh.
func SSHCommand() string {
	if s := os.Getenv("KERO_SSH"); s != "" {
		return s
	}
	return "ssh"
}

// transport opens the byte stream to a host that a remote connection runs
// over.
func transport(ctx context.Context, a Addr, client string) (io.ReadWriteCloser, error) {
	switch a.Scheme {
	case "ssh":
		return startBridge(exec.Command(SSHCommand(), sshArgs(a)...), true)
	case "wsl":
		if runtime.GOOS != "windows" {
			return nil, errors.New("wsl:// addresses are of Windows")
		}
		args := []string{}
		if a.Host != "" {
			args = append(args, "-d", a.Host)
		}
		// --exec runs sh itself, not through the distribution's shell.
		args = append(args, "--exec", "sh", "-c", bridgeScript(a, "wsl"))
		return startBridge(exec.Command("wsl.exe", args...), false)
	case "http", "https":
		scheme := "ws"
		if a.Scheme == "https" {
			scheme = "wss"
		}
		if client == "" {
			client = "Kero"
		}
		h := http.Header{}
		h.Set(ClientHeader, client)
		opts := &websocket.DialOptions{
			HTTPHeader:      h,
			CompressionMode: websocket.CompressionDisabled, // ours is across the mux
		}
		if a.Scheme == "http" {
			opts.HTTPClient = &http.Client{Transport: &http.Transport{DialContext: dialTailnet}}
		}
		ws, resp, err := websocket.Dial(ctx, scheme+"://"+a.hostPort()+MuxPath, opts)
		if err != nil {
			if resp != nil && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) {
				return nil, fmt.Errorf("%w: %s", ErrAuth, httpError(resp, err))
			}
			return nil, err
		}
		ws.SetReadLimit(64 << 20)
		return websocket.NetConn(context.Background(), ws, websocket.MessageBinary), nil
	}
	return nil, fmt.Errorf("no way to reach %s", a)
}

func httpError(resp *http.Response, err error) string {
	if resp.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if t := strings.TrimSpace(string(b)); t != "" {
			return t
		}
	}
	return err.Error()
}

// cmdConn is a program's standard input and output: ssh's or wsl.exe's,
// running the bridge. The program is waited for only once all it wrote
// is read, or the connection closed: Wait closes the pipe it reads from,
// and would drop what is still in it, as the bridge's last words.
type cmdConn struct {
	cmd    *exec.Cmd
	ssh    bool // ssh's, whose host may run Windows
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr tail
	eof    chan struct{} // closed once stdout ended, or the connection closed
	eofs   sync.Once
	done   chan struct{} // closed once the program ended
	once   sync.Once
	err    error // the program's end, once done
}

func startBridge(cmd *exec.Cmd, ssh bool) (*cmdConn, error) {
	c := &cmdConn{cmd: cmd, ssh: ssh, eof: make(chan struct{}), done: make(chan struct{})}
	hideWindow(cmd)
	// Never a prompt, as there is no one to answer; wsl.exe's errors in
	// UTF-8, which it writes in UTF-16 otherwise.
	env := os.Environ()
	if ssh {
		env = sshEnv()
	}
	cmd.Env = append(env, "SSH_ASKPASS_REQUIRE=never", "WSL_UTF8=1")
	// A program that keeps the pipes open after its own end, as a
	// ProxyCommand's child, holds up nothing.
	cmd.WaitDelay = 2 * time.Second
	var err error
	if c.stdin, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	if c.stdout, err = cmd.StdoutPipe(); err != nil {
		return nil, err
	}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%s is not installed: Kero reaches the host with it", cmd.Path)
		}
		return nil, err
	}
	go func() {
		<-c.eof
		c.err = cmd.Wait()
		close(c.done)
	}()
	return c, nil
}

func (c *cmdConn) ended() { c.eofs.Do(func() { close(c.eof) }) }

func (c *cmdConn) Read(p []byte) (int, error) {
	n, err := c.stdout.Read(p)
	if err != nil {
		c.ended()
	}
	return n, err
}

func (c *cmdConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

// Close ends the program: its standard input first, which ends the
// bridge, then, should it linger, a kill.
func (c *cmdConn) Close() error {
	c.once.Do(func() {
		c.stdin.Close()
		select {
		case <-c.eof:
		case <-time.After(500 * time.Millisecond):
			c.cmd.Process.Kill()
		}
		c.ended()
	})
	return nil
}

// failure explains why the program ended before the connection was up:
// what it said last, as ssh's "Permission denied", and how it exited.
func (c *cmdConn) failure() error {
	c.ended()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		c.cmd.Process.Kill()
		<-c.done
	}
	msg := c.stderr.last()
	code := -1
	var ee *exec.ExitError
	if errors.As(c.err, &ee) {
		code = ee.ExitCode()
	}
	if c.ssh {
		if sh := windowsShell(code, c.stderr.all()); sh != "" {
			return &shellError{sh, errors.New(msg)}
		}
	}
	switch {
	case code == bridgeNotFound || strings.Contains(msg, "kero is not installed"):
		if msg == "" {
			msg = "no kero there"
		}
		return fmt.Errorf("%w: %s", ErrNotInstalled, msg)
	case code == 255 && authFailure(msg):
		return fmt.Errorf("%w: %s", ErrAuth, msg)
	case msg != "":
		return errors.New(msg)
	case c.err != nil:
		return fmt.Errorf("%s: %v", c.cmd.Path, c.err)
	}
	return fmt.Errorf("%s ended", c.cmd.Path)
}

func authFailure(msg string) bool {
	m := strings.ToLower(msg)
	for _, s := range []string{"permission denied", "host key verification failed", "authentication", "too many authentication failures", "no matching host key"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// tail keeps the end of what a program writes to its standard error.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

// all is what the program wrote, its end at least.
func (t *tail) all() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// last is the last line that says something.
func (t *tail) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := bytes.Split(bytes.TrimSpace(t.buf), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(string(lines[i])); l != "" {
			return l
		}
	}
	return ""
}

// Tailscale's addresses: what WireGuard encrypts, and Tailscale tells the
// peer of.
var tailnets = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// tailnetAddr reports whether an address is one plain HTTP may carry
// Kero over: a tailnet peer's, or this machine's, as through a tunnel of
// ssh -L.
func tailnetAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return true
	}
	for _, p := range tailnets {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// dialTailnet connects for an http:// address, only to a tailnet peer:
// plain HTTP anywhere else, as to a name a LAN's DNS answers, would carry
// what is typed in the clear to whoever answers.
func dialTailnet(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	ips = slices.DeleteFunc(ips, func(ip netip.Addr) bool { return !tailnetAddr(ip) })
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s is not on your tailnet: Kero connects with plain http:// only to Tailscale addresses; use https:// or ssh", host)
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	if err != nil {
		return nil, err
	}
	if ap, err := netip.ParseAddrPort(c.RemoteAddr().String()); err != nil || !tailnetAddr(ap.Addr()) {
		c.Close()
		return nil, fmt.Errorf("%s is not on your tailnet: Kero connects with plain http:// only to Tailscale addresses; use https:// or ssh", addr)
	}
	return c, nil
}
