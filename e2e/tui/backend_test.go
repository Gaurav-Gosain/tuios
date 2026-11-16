package tuie2e

// The backends. TUIOS_E2E_BACKEND picks how startIn runs a tuios client:
//
//   - real, the default: the built binary on a PTY, as tuitest.StartT runs it.
//   - sim: the client runs in the test process as a tuitest.Program, through
//     internal/localclient, and the daemon of the test's isolation root runs
//     in the test process too (session.DaemonConfig.SharedProcess). The test
//     drives the client through the same *tuitest.Terminal, so its keys,
//     waits and screen reads do not change.
//
// Under sim the CLI calls (tuiosCLI) and hooks still run the built binary,
// against the in-process daemon's socket. Panes run real shells. The process
// environment is shared, so the harness sets a client's environment with
// t.Setenv, and the test knobs go through testknob.Set. A test that can never
// run in process calls requireReal. See cleanup-2026-11/testing.md, section 3.
//
// With TUITEST_TRACE set to a file, both backends append the observation
// trace of each test to it: every CLI call through tuiosCLI, and the marks
// tuitest records for every wait and every input of each client.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/adrg/xdg"
	"github.com/spf13/pflag"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/cliflags"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/localclient"
	"github.com/Gaurav-Gosain/tuios/internal/procsetup"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
	"github.com/Gaurav-Gosain/tuitest"
)

// backendEnv names the variable that picks the backend.
const backendEnv = "TUIOS_E2E_BACKEND"

// simOn says the tests run on the sim backend. setupBackend sets it.
var simOn bool

// setupBackend reads TUIOS_E2E_BACKEND. Under sim it sets the process
// globals the client reads, as cmd/tuios does, and the terminal the client
// draws to, so nothing probes the terminal the suite runs in.
func setupBackend() error {
	switch v := os.Getenv(backendEnv); v {
	case "", "real":
		return nil
	case "sim":
		simOn = true
	default:
		return fmt.Errorf("%s=%q: use real or sim", backendEnv, v)
	}
	// No reaper and no release installer: those name the tuios executable,
	// and this process is the test binary.
	procsetup.Setup(procsetup.Options{})
	// The tests set HOME and the XDG folders in this process's environment.
	// The go builds a test runs for its helper programs keep the caches
	// they had.
	for key, dir := range map[string]func() (string, error){
		"GOCACHE":    func() (string, error) { return goEnv("GOCACHE") },
		"GOMODCACHE": func() (string, error) { return goEnv("GOMODCACHE") },
		"GOPATH":     func() (string, error) { return goEnv("GOPATH") },
	} {
		if os.Getenv(key) != "" {
			continue
		}
		if v, err := dir(); err == nil && v != "" {
			_ = os.Setenv(key, v)
		}
	}
	app.SetClientCapabilities(simCaps())
	return nil
}

// simBinEnv names the built tuios for the processes the sim backend starts.
const simBinEnv = "TUIOS_E2E_SIM_BIN"

// runAsTuiosIfAsked runs the built tuios in place of this process when it was
// started with a tuios command line. The in-process daemon and client start
// their own executable for some work (agent-proto, look pick, a plugin's
// TUIOS_BIN), and under sim that executable is the test binary.
func runAsTuiosIfAsked() {
	bin := os.Getenv(simBinEnv)
	if bin == "" || len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-test.") {
		return
	}
	// argv[0] stays: a program that tuios runs under another name, such as
	// the herdr shim, reads it.
	err := syscall.Exec(bin, os.Args, os.Environ())
	fmt.Fprintf(os.Stderr, "e2e: run %s: %v\n", bin, err)
	os.Exit(2)
}

// goEnv is the value go env prints for key.
func goEnv(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	return strings.TrimSpace(string(out)), err
}

// requireReal skips the test under the sim backend. why says what the test
// needs that the test process cannot give it, such as real ssh or another
// tuios binary.
func requireReal(t *testing.T, why string) {
	t.Helper()
	if simOn {
		t.Skipf("needs the real backend: %s", why)
	}
}

// simCaps is the terminal a client that probes nothing finds. It is the
// process's seed, so no code asks the terminal the suite runs in. Each sim
// client probes its own terminal (simProbe).
func simCaps() *app.HostCapabilities {
	return &app.HostCapabilities{Term: "xterm-256color", CellWidth: 8, CellHeight: 16}
}

// simProbe asks the client's terminal what it can do, with the probe the
// binary sends to its own terminal: the queries go to out, and the replies
// come from in, before the program reads it.
func simProbe(in *probeInput, out io.Writer, cols, rows int) *app.HostCapabilities {
	return app.DetectHostCapabilitiesWith(app.HostCapabilities{Cols: cols, Rows: rows}, func(c *app.HostCapabilities) {
		app.ProbeTerminal(c, out, in.probe)
	})
}

// probeInput is the client's input. The probe reads the replies from it
// first, with a timeout, and the program then reads the rest. A read the
// probe gave up on is finished by the program's first read, so no byte is
// lost.
type probeInput struct {
	in      io.Reader
	results chan probeRead
	pending bool
	left    []byte
}

type probeRead struct {
	b   []byte
	err error
}

func newProbeInput(in io.Reader) *probeInput {
	return &probeInput{in: in, results: make(chan probeRead, 1)}
}

// next waits up to timeout for the next chunk.
func (p *probeInput) next(timeout time.Duration) (probeRead, bool) {
	if !p.pending {
		p.pending = true
		go func() {
			b := make([]byte, 512)
			n, err := p.in.Read(b)
			p.results <- probeRead{b[:n], err}
		}()
	}
	select {
	case r := <-p.results:
		p.pending = false
		return r, true
	case <-time.After(timeout):
		return probeRead{}, false
	}
}

// probe reads replies until done holds or the timeout runs out.
func (p *probeInput) probe(timeout time.Duration, done func(string) bool) string {
	var got strings.Builder
	deadline := time.Now().Add(timeout)
	for !done(got.String()) {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		r, ok := p.next(left)
		if !ok || r.err != nil {
			break
		}
		got.Write(r.b)
	}
	return got.String()
}

func (p *probeInput) Read(b []byte) (int, error) {
	if len(p.left) == 0 && p.pending {
		r := <-p.results
		p.pending = false
		if r.err != nil && len(r.b) == 0 {
			return 0, r.err
		}
		p.left = r.b
	}
	if len(p.left) > 0 {
		n := copy(b, p.left)
		p.left = p.left[n:]
		return n, nil
	}
	return p.in.Read(b)
}

// simEnv makes env the process environment for the rest of the test, as the
// environment of a spawned tuios would hold it, and reloads the XDG paths.
// The test knobs in env go to testknob.Set instead, merged over the ones the
// suite runs with.
func simEnv(t *testing.T, env []string) {
	t.Helper()
	// Registered first, so it runs after t.Setenv has put the old values
	// back.
	t.Cleanup(xdg.Reload)
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if k == testknob.Env {
			testknob.Set(testknob.Parse(testknob.Merge(os.Getenv(testknob.Env), v)))
			t.Cleanup(func() { testknob.Set(testknob.Parse(os.Getenv(testknob.Env))) })
			continue
		}
		if got, set := os.LookupEnv(k); !set || got != v {
			t.Setenv(k, v)
		}
	}
	xdg.Reload()
}

// simDaemon is the in-process daemon of one isolation root.
type simDaemon struct {
	t    *testing.T
	base string
	d    *session.Daemon
	once sync.Once
}

// stop stops the daemon, once, and forgets it, so the root may start a new
// one, as a tuios started after a kill-server would.
func (s *simDaemon) stop() {
	s.once.Do(func() {
		s.d.Stop()
		simDaemons.CompareAndDelete(s.base, s)
	})
}

// simDaemons maps an isolation root to its *simDaemon.
var (
	simDaemons  sync.Map
	simDaemonMu sync.Mutex
)

// simEnsureDaemon starts the in-process daemon of base, once. A test has one
// isolation root under sim, because the daemon reads the process
// environment: a second root fails the test.
func simEnsureDaemon(t *testing.T, base string) {
	t.Helper()
	// CLI calls a test makes at once all ask for the daemon.
	simDaemonMu.Lock()
	defer simDaemonMu.Unlock()
	if _, ok := simDaemons.Load(base); ok {
		return
	}
	simDaemons.Range(func(k, v any) bool {
		if v.(*simDaemon).t == t {
			t.Fatalf("sim backend: this test uses a second isolation root (%s after %s), and the in-process daemon reads the process environment", base, k)
		}
		return true
	})
	env := []string{"SHELL=/bin/sh", "ENV=", "PS1=$ "}
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	simEnv(t, env)
	pinPreV080Looks(t, base)
	// The daemon and the client log with the standard logger, which the
	// binary keeps off the screen. Here it goes to a file, shown when the
	// test fails.
	logPath := filepath.Join(t.TempDir(), "process.log")
	if f, err := os.Create(logPath); err == nil {
		log.SetOutput(f)
		t.Cleanup(func() {
			log.SetOutput(os.Stderr)
			_ = f.Close()
			if b, _ := os.ReadFile(logPath); t.Failed() && len(b) > 0 {
				t.Logf("sim: the log of the in-process daemon and clients:\n%s", tail(b, 8<<10))
			}
		})
	}
	uc, err := config.LoadUserConfig()
	if err != nil {
		uc = nil
	}
	cfg := session.DaemonConfigFromUser(uc)
	cfg.Version = "dev"
	cfg.SharedProcess = true
	d := session.NewDaemon(cfg)
	if err := d.Start(); err != nil {
		t.Fatalf("sim backend: start the in-process daemon: %v", err)
	}
	s := &simDaemon{t: t, base: base, d: d}
	simDaemons.Store(base, s)
	// Registered after the environment, so the daemon stops while the
	// test's directories are still the ones it writes to.
	t.Cleanup(s.stop)
}

// simStartsDaemon names the commands that start a daemon when none runs
// (ensureDaemon in cmd/tuios). Under sim tuiosCLI starts the in-process
// daemon for them, and leaves every other command to find what runs.
var simStartsDaemon = map[string]bool{
	"new": true, "attach": true, "start-server": true, "start-agent": true,
	"delegate": true, "gui-bridge": true, "cp": true, "worktree": true,
}

// simKillServer is kill-server under sim. The binary would signal the pid
// of the daemon's process, which is this one, so the in-process daemon stops
// here, with the binary's words. A command that starts a daemon starts a new
// one, as it does with the binary.
func simKillServer(base string) string {
	v, ok := simDaemons.Load(base)
	if !ok {
		return "TUIOS daemon is not running.\n"
	}
	v.(*simDaemon).stop()
	return fmt.Sprintf("Sent SIGTERM to daemon (PID %d)\n", os.Getpid())
}

// simStopDaemon stops the in-process daemon of base, if one runs.
func simStopDaemon(base string) {
	if v, ok := simDaemons.Load(base); ok {
		v.(*simDaemon).stop()
	}
}

func tail(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}

// simArgs is a tuios command line the sim client runs: a bare tuios, attach
// [NAME] or new NAME, with the interface flags.
type simArgs struct {
	verb  string // "", "attach" or "new"
	name  string
	flags cliflags.Interface
}

// parseSimArgs reads args as cmd/tuios would for the three command lines the
// sim client runs. ok is false for any other command line.
func parseSimArgs(args []string) (a simArgs, ok bool) {
	fs := pflag.NewFlagSet("tuios", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a.flags.Register(fs)
	if err := fs.Parse(args); err != nil {
		return a, false
	}
	pos := fs.Args()
	switch {
	case len(pos) == 0:
	case pos[0] == "attach" && len(pos) <= 2:
		a.verb = "attach"
		if len(pos) == 2 {
			a.name = pos[1]
		}
	case pos[0] == "new" && len(pos) == 2:
		a.verb, a.name = "new", pos[1]
	default:
		return a, false
	}
	return a, true
}

// simClient is a tuios client that runs in the test process: what
// cmd/tuios runs for a bare tuios, attach and new, on a localclient.Pipe.
type simClient struct {
	args simArgs
	env  []string
	dir  string
	base string

	resize chan localclient.Size

	mu   sync.Mutex
	prog *tea.Program
	quit bool
}

func (c *simClient) Resize(cols, rows int) {
	select {
	case c.resize <- localclient.Size{Width: cols, Height: rows}:
	default:
	}
}

func (c *simClient) Stop() {
	c.mu.Lock()
	c.quit = true
	p := c.prog
	c.mu.Unlock()
	if p != nil {
		p.Quit()
	}
}

func (c *simClient) getenv(key string) string {
	for _, kv := range slices.Backward(c.env) {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// connect makes the daemon client of an attach or a new, as
// runDaemonSessionOn does for a local daemon: a new attaches with createNew.
func (c *simClient) connect(out io.Writer, settings *config.Settings, caps *app.HostCapabilities, cols, rows int) (*session.TUIClient, *session.SessionState, bool) {
	createNew := c.args.verb == "new"
	client := session.NewTUIClient()
	client.UseCallerTerm()
	client.UpgradeDialog = true
	if c.args.name == "" {
		client.StartDir = c.dir
	}
	if err := client.ConnectWithCapabilities("dev", cols, rows, app.ClientCapabilitiesOf(caps, settings.ImageSymbols)); err != nil {
		_, _ = fmt.Fprintf(out, "sim: connect to the daemon: %v\r\n", err)
		return nil, nil, false
	}
	state, err := client.AttachSession(c.args.name, createNew, cols, rows)
	if err != nil {
		_, _ = fmt.Fprintf(out, "sim: attach: %v\r\n", err)
		_ = client.Close()
		return nil, nil, false
	}
	client.StartReadLoop()
	return client, state, true
}

// Run is runLocal or runDaemonSessionOn of cmd/tuios on a Pipe. It does not
// print the notices the binary prints after the TUI has ended.
func (c *simClient) Run(in io.Reader, out io.Writer, cols, rows int) int {
	ov := c.args.flags.Overrides()
	userConfig, settings := localclient.LoadConfig(c.getenv, ov)
	pin := newProbeInput(in)
	caps := simProbe(pin, out, cols, rows)
	// Code that reads the process's terminal and not the model's finds this
	// client's.
	app.SetClientCapabilities(caps)
	defer app.SetClientCapabilities(simCaps())
	opts := app.OSOptions{
		Client:          app.ClientLocal,
		KeybindRegistry: config.NewKeybindRegistry(userConfig),
		UserConfig:      userConfig,
		ShowKeys:        c.args.flags.ShowKeys,
		Overrides:       ov,
		Settings:        settings,
		Caps:            caps,
		Width:           cols,
		Height:          rows,
	}
	var client *session.TUIClient
	var state *session.SessionState
	if c.args.verb != "" {
		var ok bool
		if client, state, ok = c.connect(out, settings, caps, cols, rows); !ok {
			return 1
		}
		opts.IsDaemonSession = true
		opts.DaemonClient = client
		opts.SessionName = client.SessionName()
		opts.FollowUpgrades = true
	}
	lc := localclient.New(localclient.Pipe{
		In: pin, Out: out, Env: c.env, Width: cols, Height: rows, Resize: c.resize,
		Output: termio.Output{Raw: true},
	}, opts, state)
	c.mu.Lock()
	c.prog = lc.Program
	stop := c.quit
	c.mu.Unlock()
	if stop {
		lc.Program.Quit()
	}
	final, err := lc.Run()
	lc.Close()
	code := 0
	if fo, ok := final.(*app.OS); ok {
		// The binary exits 1 for these, with a diagnostic it prints. The
		// sim client prints no notice.
		switch fo.ExitReason {
		case app.ExitSessionKilled, app.ExitDaemonLost, app.ExitHostLost, app.ExitNestedRefused, app.ExitRestartFailed:
			code = 1
		}
		if client != nil && fo.ExitReason == app.ExitNormal && !fo.QuitRequested {
			fo.SyncStateToDaemon()
		}
		lc.Finish(fo)
		if client == nil {
			// The binary's exit takes its pane shells with it.
			for _, w := range fo.Windows {
				w.Close()
			}
		}
	}
	if client != nil {
		_ = client.Close()
	}
	if err != nil {
		return 1
	}
	return code
}

// simStart is the last step of startIn under the sim backend. argv is the
// tuios command line after the program, env the client's environment, and
// dir its directory. A command line the sim client does not run starts the
// built binary, against the in-process daemon.
func simStart(t *testing.T, base string, o startOpts, argv, env []string, dir string, opts []tuitest.Option) *tuitest.Terminal {
	t.Helper()
	args, ok := parseSimArgs(argv)
	if !ok || len(o.wrap) > 0 || (args.verb == "" && o.daemonDefault) {
		if args.verb != "" || o.daemonDefault || !ok {
			simEnsureDaemon(t, base)
		}
		t.Logf("sim backend: %v runs the built binary", argv)
		return nil
	}
	if args.verb != "" {
		simEnsureDaemon(t, base)
	}
	env = lastWins(append([]string{"LANG=C.UTF-8", "TERM=xterm-256color"}, env...))
	simEnv(t, env)
	t.Chdir(dir)
	return tuitest.StartProgramT(t, &simClient{args: args, env: env, dir: dir, base: base, resize: make(chan localclient.Size, 16)}, opts...)
}

// lastWins keeps the last entry of each name in env, in the order the names
// first appear, as tuitest builds the environment of the binary.
func lastWins(env []string) []string {
	val := map[string]string{}
	var order []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, seen := val[k]; !seen {
			order = append(order, k)
		}
		val[k] = v
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+val[k])
	}
	return out
}

// The observation trace.

// traceFile is TUITEST_TRACE: where the trace lines go.
var traceFile = os.Getenv("TUITEST_TRACE")

var traceMu sync.Mutex

// traceLine appends one line of the trace: the test, the stream (cli, or
// the client's number), and what happened.
func traceLine(t *testing.T, stream, what string) {
	traceMu.Lock()
	defer traceMu.Unlock()
	f, err := os.OpenFile(traceFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	what = strings.NewReplacer("\t", " ", "\n", " ").Replace(what)
	_, _ = fmt.Fprintf(f, "%s\t%s\t%s\n", t.Name(), stream, what)
}

// traceCLI records a CLI call.
func traceCLI(t *testing.T, args []string) {
	if traceFile != "" {
		traceLine(t, "cli", strings.Join(args, " "))
	}
}

// clientCount numbers the clients of each test, for the trace.
var clientCount sync.Map // *testing.T -> *int

// traceOptions records the client's waits and inputs when TUITEST_TRACE is
// set.
func traceOptions(t *testing.T) []tuitest.Option {
	if traceFile == "" {
		return nil
	}
	return []tuitest.Option{tuitest.WithCastDir(filepath.Join(t.TempDir(), "casts"))}
}

// traceClient writes the marks of term to the trace once the test is done,
// before the client is closed.
func traceClient(t *testing.T, term *tuitest.Terminal) {
	if traceFile == "" {
		return
	}
	v, _ := clientCount.LoadOrStore(t, new(int))
	n := v.(*int)
	*n++
	stream := fmt.Sprintf("client%d", *n)
	t.Cleanup(func() {
		var b bytes.Buffer
		if err := term.WriteCast(&b); err != nil {
			return
		}
		for i, line := range bytes.Split(b.Bytes(), []byte("\n")) {
			if i == 0 || len(line) == 0 {
				continue
			}
			var ev []any
			if json.Unmarshal(line, &ev) != nil || len(ev) != 3 || ev[1] != "m" {
				continue
			}
			label, _ := ev[2].(string)
			traceLine(t, stream, label)
		}
	})
}
