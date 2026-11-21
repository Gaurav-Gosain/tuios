package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

const backendEnv = "TUIOS_E2E_BACKEND"

var simOn bool

func setupBackend() error {
	switch v := os.Getenv(backendEnv); v {
	case "", "real":
		return nil
	case "sim":
		simOn = true
	default:
		return fmt.Errorf("%s=%q: use real or sim", backendEnv, v)
	}
	procsetup.Setup(procsetup.Options{})
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

const simBinEnv = "TUIOS_E2E_SIM_BIN"

func runAsTuiosIfAsked() {
	bin := os.Getenv(simBinEnv)
	if bin == "" || len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-test.") {
		return
	}
	err := syscall.Exec(bin, os.Args, os.Environ())
	fmt.Fprintf(os.Stderr, "e2e: run %s: %v\n", bin, err)
	os.Exit(2)
}

func goEnv(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	return strings.TrimSpace(string(out)), err
}

func requireReal(t *testing.T, why string) {
	t.Helper()
	if simOn {
		t.Skipf("needs the real backend: %s", why)
	}
}

func simCaps() *app.HostCapabilities {
	return &app.HostCapabilities{Term: "xterm-256color", CellWidth: 8, CellHeight: 16}
}

func simProbe(in *probeInput, out io.Writer, cols, rows int) *app.HostCapabilities {
	return app.DetectHostCapabilitiesWith(app.HostCapabilities{Cols: cols, Rows: rows}, func(c *app.HostCapabilities) {
		app.ProbeTerminal(c, out, in.probe)
	})
}

type probeInput struct {
	in       io.Reader
	results  chan probeRead
	pending  bool
	left     []byte
	stopped  chan struct{}
	stopOnce sync.Once
}

func (p *probeInput) stop() { p.stopOnce.Do(func() { close(p.stopped) }) }

type probeRead struct {
	b   []byte
	err error
}

func newProbeInput(in io.Reader) *probeInput {
	return &probeInput{in: in, results: make(chan probeRead, 1), stopped: make(chan struct{})}
}

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
	if len(p.left) == 0 {
		if !p.pending {
			p.pending = true
			go func() {
				buf := make([]byte, 4096)
				n, err := p.in.Read(buf)
				p.results <- probeRead{buf[:n], err}
			}()
		}
		select {
		case r := <-p.results:
			p.pending = false
			if r.err != nil && len(r.b) == 0 {
				return 0, r.err
			}
			p.left = r.b
		case <-p.stopped:
			return 0, io.EOF
		}
	}
	n := copy(b, p.left)
	p.left = p.left[n:]
	return n, nil
}

func simEnv(t *testing.T, env []string) {
	t.Helper()
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

type simDaemon struct {
	t    *testing.T
	base string
	d    *session.Daemon
	once sync.Once
}

func (s *simDaemon) stop() {
	s.once.Do(func() {
		s.d.Stop()
		simDaemons.CompareAndDelete(s.base, s)
	})
}

var (
	simDaemons  sync.Map
	simDaemonMu sync.Mutex
)

func simEnsureDaemon(t *testing.T, base string) {
	t.Helper()
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
	simKilledRoots.Delete(base)
	t.Cleanup(s.stop)
}

var simStartsDaemon = map[string]bool{
	"new": true, "attach": true, "start-server": true, "start-agent": true,
	"delegate": true, "gui-bridge": true, "cp": true, "worktree": true,
}

func simKillServer(base string) string {
	v, ok := simDaemons.Load(base)
	if !ok {
		return "TUIOS daemon is not running.\n"
	}
	simKilledRoots.Store(base, true)
	v.(*simDaemon).stop()
	return fmt.Sprintf("Sent SIGTERM to daemon (PID %d)\n", os.Getpid())
}

func simBeforeCLI(t *testing.T, base string, args []string) (out string, done bool) {
	t.Helper()
	if !simOn {
		return "", false
	}
	if len(args) == 1 && args[0] == "kill-server" {
		return simKillServer(base), true
	}
	if len(args) > 0 && (simStartsDaemon[args[0]] || !simKilled(base)) {
		simEnsureDaemon(t, base)
	}
	return "", false
}

var simKilledRoots sync.Map

func simKilled(base string) bool {
	_, ok := simKilledRoots.Load(base)
	return ok
}

func simRealDaemonCheck(t *testing.T, base string) {
	out, _ := tuiosCLIRun(t, base, "kill-server")
	if strings.Contains(out, "not running") {
		return
	}
	t.Errorf("sim backend: a daemon process served %s, so this test did not run in process; kill-server: %s", base, strings.TrimSpace(out))
}

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

type simArgs struct {
	verb  string
	name  string
	flags cliflags.Interface
}

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

type simClient struct {
	args simArgs
	env  []string
	dir  string
	base string

	resize chan localclient.Size
	ended  chan struct{}

	mu   sync.Mutex
	prog *tea.Program
	in   *probeInput
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
	p, in := c.prog, c.in
	c.mu.Unlock()
	if p != nil {
		p.Quit()
	}
	if in != nil {
		in.stop()
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

func (c *simClient) Run(in io.Reader, out io.Writer, cols, rows int) int {
	defer close(c.ended)
	ov := c.args.flags.Overrides()
	userConfig, settings := localclient.LoadConfig(c.getenv, ov)
	pin := newProbeInput(in)
	c.mu.Lock()
	c.in = pin
	stopNow := c.quit
	c.mu.Unlock()
	if stopNow {
		pin.stop()
	}
	caps := simProbe(pin, out, cols, rows)
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
		switch fo.ExitReason {
		case app.ExitSessionKilled, app.ExitDaemonLost, app.ExitHostLost, app.ExitNestedRefused, app.ExitRestartFailed:
			code = 1
		}
		if client != nil && fo.ExitReason == app.ExitNormal && !fo.QuitRequested {
			fo.SyncStateToDaemon()
		}
		lc.Finish(fo)
		if client == nil {
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
	c := &simClient{args: args, env: env, dir: dir, base: base, resize: make(chan localclient.Size, 16), ended: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-c.ended:
		case <-time.After(10 * time.Second):
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Errorf("sim backend: the client did not return from Run within 10s of Stop, and it writes into the test's folders while it runs:\n%s", buf[:n])
		}
	})
	return tuitest.StartProgramT(t, c, opts...)
}

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

var traceFile = os.Getenv("TUITEST_TRACE")

var traceMu sync.Mutex

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

func traceCLI(t *testing.T, args []string) {
	if traceFile != "" {
		traceLine(t, "cli", strings.Join(args, " "))
	}
}

var clientCount sync.Map

func traceOptions(t *testing.T) []tuitest.Option {
	if traceFile == "" {
		return nil
	}
	return []tuitest.Option{tuitest.WithCastDir(filepath.Join(t.TempDir(), "casts"))}
}

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
