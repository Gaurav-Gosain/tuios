package hostsync

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/restartplan"
)

// The plan and apply steps of an install on one machine: what tuios hosts
// sync does for each host, and what tuios hosts setup does for the machine
// it sets up. One path installs, restarts, starts and installs the service,
// so the two commands cannot drift.

// PlanOptions say what a plan may do besides the install.
type PlanOptions struct {
	Restart, Start, Service, DryRun bool
}

// Daemon states.
const (
	DaemonRunning = "running"
	DaemonStopped = "stopped"
	DaemonUnknown = "unknown"
)

// Budgets for an install and a restart on one machine.
const (
	InstallTimeout = 15 * time.Minute
	// A restart may be a live upgrade, which waits up to a minute for the
	// new image, then a restart if the upgrade could not start.
	RestartTimeout = 150 * time.Second
)

// Daemon is the daemon on a host.
type Daemon struct {
	State        string                `json:"state"`
	Version      string                `json:"version,omitempty"`
	PID          int                   `json:"pid,omitzero"`
	SessionCount int                   `json:"session_count"`
	Sessions     []restartplan.Session `json:"sessions,omitempty"`
	// Unread says the sessions could not be listed, so a restart's cost is
	// not known.
	Unread    bool `json:"unread,omitzero"`
	Restarted bool `json:"restarted"`
	// Upgraded says --restart upgraded the daemon live: it kept its pid and
	// every program in its panes.
	Upgraded bool `json:"upgraded"`
	// Started says --start started a daemon that was not running.
	Started bool `json:"started"`
}

// Result is one host's row.
type Result struct {
	Host          string `json:"host"`
	Addr          string `json:"addr"`
	Local         bool   `json:"local,omitzero"`
	OS            string `json:"os,omitempty"`
	Arch          string `json:"arch,omitempty"`
	Path          string `json:"path,omitempty"`
	Before        string `json:"before,omitempty"`
	BeforeBackend string `json:"before_backend,omitempty"`
	After         string `json:"after,omitempty"`
	InstallPath   string `json:"install_path,omitempty"`
	Installed     bool   `json:"installed"`
	Daemon        Daemon `json:"daemon"`
	// Action is what was done, or with --dry-run what would be done.
	Action         string   `json:"action"`
	RestartNeeded  bool     `json:"restart_needed"`
	RestartCommand string   `json:"restart_command,omitempty"`
	Notes          []string `json:"notes,omitempty"`
	Error          string   `json:"error,omitempty"`
	// ErrorKind names an error a script can act on: "tailscale_check" for a
	// login that waits for an approval, "tailscale_policy" for a login the
	// tailnet policy refuses, or the class of an ssh failure
	// (federation.SSHFailure). Empty for any other error.
	ErrorKind string `json:"error_kind,omitempty"`
	// ApprovalURL is where the person approves the login, with
	// "tailscale_check".
	ApprovalURL string `json:"approval_url,omitempty"`
	// Fix says what to do about an ssh failure of a kind federation
	// classifies (host_key_new, auth, dns and the others).
	Fix string `json:"fix,omitempty"`
	// Service is the daemon service on the host: "installed", "none",
	// "unsupported", or empty when the probe did not say.
	Service string `json:"service,omitempty"`
	// ServiceInstalled says --service installed it in this run.
	ServiceInstalled bool `json:"service_installed,omitzero"`
}

// Target is one machine being worked on.
type Target struct {
	Name   string
	Host   federation.Host
	Local  bool
	Runner Runner
	Res    Result

	// From the probe.
	Home        string
	Configured  string
	Symlink     bool
	DirWritable bool
	SHA         string

	// The plan.
	Platform  string
	Install   bool
	TargetArg string
	Restart   bool
	// LiveOnly says a restart may only be a live upgrade: one that cannot
	// be live leaves the daemon as it is.
	LiveOnly   bool
	Start      bool
	Service    bool
	OldArg     string
	NewVersion string
	Failed     bool
	// daemonNote is why the link could not read the daemon. It is kept
	// apart because it says nothing new when the probe already failed or
	// found no tuios.
	DaemonNote string
	// Progress, when set, hears the bytes of a binary as they are sent.
	Progress func(done, total int64)
	mu       sync.Mutex
}

func (t *Target) Note(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Res.Notes = append(t.Res.Notes, s)
}

func (t *Target) Fail(err error) {
	t.Failed = true
	t.Res.Error = err.Error()
	if ge := GateErrorOf(err); ge != nil {
		t.Res.ErrorKind = ge.Gate.Kind
		t.Res.ApprovalURL = ge.Gate.URL
	}
	if fe, ok := errors.AsType[*SSHFailError](err); ok {
		t.Res.ErrorKind = string(fe.Failure)
		t.Res.Fix = fe.Failure.Fix(t.Host.Addr)
	}
}

// Plan decides what one host needs.
func Plan(t *Target, src *Source, opts PlanOptions) {
	t.Platform = t.Res.OS + "/" + t.Res.Arch
	if src.Kind == KindBinary && t.Platform != src.Platform {
		t.Fail(errfmt.Errorf("the binary is for %s and %s runs %s", src.Platform, t.Name, t.Platform))
		return
	}
	t.NewVersion = src.Version
	// A binary that cannot run here tells its version only where it is
	// installed: the same bytes on the host report it.
	if t.NewVersion == "" && src.Kind == KindBinary && t.SHA != "" && t.SHA == src.BinaryFor(src.Platform).SHA {
		t.NewVersion = t.Res.Before
	}

	current := t.Res.Path != "" && (SameVersion(t.Res.Before, t.NewVersion) ||
		(t.SHA != "" && src.Kind == KindBinary && t.SHA == src.BinaryFor(src.Platform).SHA))
	t.Install = !current

	// Where it goes.
	t.TargetArg, t.Res.InstallPath = installTarget(t)
	t.OldArg = "-"
	if t.Res.Path != "" && federation.SafeRemoteArg(t.Res.Path) {
		t.OldArg = t.Res.Path
	}

	// The daemon.
	d := &t.Res.Daemon
	mismatched := d.State == DaemonRunning && !SameVersion(d.Version, t.NewVersion)
	if d.State == DaemonRunning && t.NewVersion == "" {
		// Nothing to compare against: the version of the new binary is not
		// known until it is installed.
		mismatched = false
		if opts.Restart {
			t.Note("The version of the binary is not known here, so the daemon is not restarted. Install it first, then run the restart.")
		}
	}
	t.Res.RestartNeeded = mismatched
	if mismatched {
		t.Res.RestartCommand = restartCommand(t, src)
		if opts.Restart {
			switch {
			case t.Local && os.Getenv("TUIOS_PANE_ID") != "":
				// A live upgrade keeps this command's pane and this command;
				// a restart would end both halfway.
				t.Restart, t.LiveOnly = true, true
			case d.Unread && d.SessionCount != 0:
				t.Note("The sessions of the daemon could not be read, so it is not restarted. Restart it by hand on the host.")
			default:
				t.Restart = true
			}
		}
	}
	if opts.Service && t.Res.Service == "none" {
		t.Service = true
	}
	if opts.Start {
		switch d.State {
		case DaemonStopped:
			t.Start = true
		case DaemonUnknown:
			t.Note("The state of the daemon is not known, so sync does not start one.")
		}
	}
}

// SameVersion reports whether two version strings name the same build. A
// bare "dev" names no build in particular, so it matches nothing.
func SameVersion(a, b string) bool {
	if a == "" || b == "" || a == "dev" || b == "dev" {
		return false
	}
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// installTarget is where a host's new binary goes, as the install script
// reads it and as a person reads it.
func installTarget(t *Target) (arg, display string) {
	home := strings.TrimSuffix(t.Home, "/")
	fallback := func(why string) (string, string) {
		if why != "" {
			t.Note(why)
		}
		return "-", home + "/.local/bin/tuios"
	}
	// A configured command is what the link runs, so a binary goes there.
	if cmdArg, ok := CommandArg(t.Host.Command); ok && cmdArg != "-" {
		p := t.Configured
		if t.Res.Path != "" && !t.DirWritable {
			return fallback(fmt.Sprintf("You cannot write the folder of %s, so sync installs to ~/.local/bin. The config still runs %s.", p, t.Host.Command))
		}
		return cmdArg, p
	}
	p := t.Res.Path
	switch {
	case p == "":
		return fallback("")
	case !federation.SafeRemoteArg(p):
		return fallback(fmt.Sprintf("The path %s has characters sync does not send to a shell, so sync installs to ~/.local/bin.", p))
	case strings.HasPrefix(p, "/nix/"):
		return fallback(fmt.Sprintf("Nix owns %s, so sync installs to ~/.local/bin.", p))
	case t.Symlink && p != home+"/.local/bin/tuios":
		return fallback(fmt.Sprintf("%s is a link that another installer may own, so sync installs to ~/.local/bin.", p))
	case !t.DirWritable:
		return fallback(fmt.Sprintf("You cannot write the folder of %s, and sync does not use sudo. It installs to ~/.local/bin.", p))
	}
	return p, p
}

// restartCommand is the command that restarts one daemon later, with the
// same source as this run.
func restartCommand(t *Target, src *Source) string {
	parts := []string{"tuios", "hosts", "sync", t.Name, "--restart"}
	switch src.Kind {
	case KindDev:
		if src.Explicit {
			parts = append(parts, "--src", src.SrcDir)
		} else {
			parts = append(parts, "--dev")
		}
	case KindBinary:
		parts = append(parts, "--binary", src.BinPath)
	}
	return strings.Join(parts, " ")
}

// Apply installs and restarts on one host.
func Apply(t *Target, src *Source) {
	if t.Install {
		b := src.BinaryFor(t.Platform)
		if b == nil {
			t.Fail(errfmt.Errorf("no binary was made for %s", t.Platform))
			return
		}
		if t.NewVersion == "" {
			t.NewVersion = b.Version
		}
		restart := t.restartMode()
		ctx, cancel := context.WithTimeout(context.Background(), InstallTimeout)
		defer cancel()
		var body io.Reader = bytes.NewReader(b.Data)
		if t.Progress != nil {
			body = &countingReader{r: body, total: int64(len(b.Data)), tick: t.Progress}
		}
		out, stderr, err := t.Runner.Run(ctx, InstallScript(),
			[]string{t.TargetArg, b.SHA, strconv.Itoa(len(b.Data)), restart, t.OldArg}, body)
		f := ParseFacts(out)
		if err != nil || f.Has("fail") {
			t.Fail(installError(f, stderr, err))
			return
		}
		t.Res.Installed = true
		t.Res.InstallPath = f.Get("target")
		staged, _ := ParseVersionLine(f.Get("staged"))
		after, _ := ParseVersionLine(f.Get("after"))
		t.Res.After = after
		if after == "" || after != staged {
			t.Fail(errfmt.Errorf("the installed binary did not run after the install. It reported %q", f.Get("after")))
			return
		}
		if t.NewVersion == "" {
			t.NewVersion = after
		}
		if login := f.Get("login"); login != t.Res.InstallPath {
			if login == "" {
				t.Note(fmt.Sprintf("A login shell on %s does not find tuios. Add %s to the PATH there. The link finds it anyway.", t.Name, path.Dir(t.Res.InstallPath)))
			} else {
				t.Note(fmt.Sprintf("A login shell on %s runs %s, not %s.", t.Name, login, t.Res.InstallPath))
			}
		}
		if t.Restart {
			restartResult(t, f)
		}
		installService(t)
		startDaemon(t)
		return
	}
	t.Res.After = t.Res.Before
	if t.Restart {
		ctx, cancel := context.WithTimeout(context.Background(), RestartTimeout)
		defer cancel()
		arg := t.OldArg
		if arg == "-" {
			arg = t.TargetArg
		}
		out, stderr, err := t.Runner.Run(ctx, RestartScript(), []string{arg, t.restartMode()}, nil)
		f := ParseFacts(out)
		if err != nil && !f.Has("stopfail") && !f.Has("startfail") {
			t.Fail(RemoteRunError("the restart failed", stderr, err))
			return
		}
		restartResult(t, f)
	}
	installService(t)
	startDaemon(t)
}

// installService installs the daemon service on a host that has none,
// with the binary the host has now. When the service starts the daemon, the
// start that --start asked for is done.
func installService(t *Target) {
	if !t.Service || t.Failed {
		return
	}
	arg := t.OldArg
	if t.Res.Installed {
		arg = t.Res.InstallPath
		if !federation.SafeRemoteArg(arg) {
			arg = t.TargetArg
		}
	}
	if arg == "-" {
		arg = t.TargetArg
	}
	ctx, cancel := context.WithTimeout(context.Background(), RestartTimeout)
	defer cancel()
	out, stderr, err := t.Runner.Run(ctx, ServiceScript(), []string{arg}, nil)
	f := ParseFacts(out)
	said := strings.Join(f["out"], " ")
	switch {
	case f.Has("svcok"):
		t.Res.ServiceInstalled = true
		t.Res.Service = "installed"
		if said != "" {
			t.Note("The service: " + plainLine(said))
		}
		if f.Has("up") && t.Res.Daemon.State != DaemonRunning {
			t.Start = false
			t.Res.Daemon.Started = true
			t.Res.Daemon.State = DaemonRunning
			t.Res.Daemon.Version = t.Res.After
		}
	case f.Has("svcfail"):
		t.Note("The service was not installed. tuios on the host said: " + plainLine(said))
	default:
		t.Note(RemoteRunError("the service was not installed", stderr, err).Error())
	}
}

// startDaemon starts the daemon on a host where it does not run, with the
// binary the host has now: the one just installed, or else the one it had.
func startDaemon(t *Target) {
	if !t.Start || t.Failed {
		return
	}
	arg, shown := t.OldArg, t.Res.Path
	if t.Res.Installed {
		arg, shown = t.Res.InstallPath, t.Res.InstallPath
		if !federation.SafeRemoteArg(arg) {
			arg = t.TargetArg
		}
	}
	if arg == "-" {
		arg = t.TargetArg
	}
	ctx, cancel := context.WithTimeout(context.Background(), RestartTimeout)
	defer cancel()
	if err := StartDaemonWith(ctx, t.Runner, arg, shown); err != nil {
		t.Fail(err)
		return
	}
	d := &t.Res.Daemon
	d.Started = true
	d.State = DaemonRunning
	d.Version = t.Res.After
	t.Res.RestartNeeded = false
	t.Res.RestartCommand = ""
}

// restartMode is the daemon mode of the install and restart scripts.
func (t *Target) restartMode() string {
	switch {
	case !t.Restart:
		return "0"
	case t.LiveOnly:
		return "2"
	}
	return "1"
}

// restartResult reads what the install or restart script said about the
// daemon.
func restartResult(t *Target, f Facts) {
	if lines := f["upgrade"]; len(lines) > 0 {
		upgradeResult(t, lines[len(lines)-1])
		return
	}
	switch {
	case f.Has("nolive"):
		t.Note("The new binary cannot upgrade a daemon live, and this command runs in a pane of that daemon, so the daemon was left running the old version. Run the restart from a terminal outside tuios.")
	case f.Has("stopfail"):
		t.Fail(errors.New("the old daemon did not stop. It still runs the old version"))
	case f.Has("startfail"):
		t.Fail(errors.New("the old daemon stopped and the new one did not start. Run 'tuios start-server' on the host. The sessions come back then"))
	case f.Has("started"):
		t.Res.Daemon.Restarted = true
		t.Res.RestartNeeded = false
		t.Res.RestartCommand = ""
	}
}

// The actions of `tuios daemon upgrade --json`, as the host's tuios writes them.
const (
	upgradeActionUpgraded  = "upgraded"
	upgradeActionRestarted = "restarted"
	upgradeActionStarted   = "started"
	upgradeActionFallback  = "fallback"
	upgradeActionRefused   = "refused"
)

// upgradeResult reads the JSON outcome of `tuios daemon upgrade` on the host.
func upgradeResult(t *Target, line string) {
	var o struct {
		Action string `json:"action"`
		PID    int    `json:"pid"`
		To     string `json:"to"`
		Panes  int    `json:"panes"`
		Reason string `json:"reason"`
		Error  string `json:"error"`
	}
	if err := jsonx.Unmarshal([]byte(line), &o); err != nil {
		t.Fail(errfmt.Errorf("the daemon upgrade answered with something that is not its answer: %s", plainLine(line)))
		return
	}
	d := &t.Res.Daemon
	moved := func() {
		d.State = DaemonRunning
		d.Version = cmp.Or(plainLine(o.To), d.Version)
		if o.PID > 0 {
			d.PID = o.PID
		}
		t.Res.RestartNeeded = false
		t.Res.RestartCommand = ""
	}
	reason := plainLine(o.Reason)
	switch o.Action {
	case upgradeActionUpgraded:
		moved()
		d.Upgraded = true
		t.Note(fmt.Sprintf("The daemon was upgraded live, with its pid. %s and their programs kept.", plural.Count(o.Panes, "pane")))
	case upgradeActionRestarted:
		moved()
		d.Restarted = true
		t.Note("The daemon could not be upgraded live, so it was restarted: " + reason + ".")
	case upgradeActionStarted:
		moved()
		d.Started = true
		t.Note("No daemon was running when the binary was in place, so one was started.")
	case upgradeActionFallback:
		t.Fail(errfmt.Errorf("the new binary could not take the panes over, so the daemon went back to %s with every program still running. Run 'tuios logs' on the host to see why", cmp.Or(plainLine(o.To), "the old version")))
	case upgradeActionRefused:
		t.Note("The daemon could not be upgraded live: " + reason + ". This command runs in a pane of that daemon, so it was not restarted, and it still runs the old version. Run the restart from a terminal outside tuios.")
	default:
		t.Fail(errfmt.Errorf("the daemon was not moved to the new version: %s", plainLine(cmp.Or(o.Error, "the upgrade did not say why"))))
	}
}

// installError is one plain sentence for a failed install.
func installError(f Facts, stderr string, err error) error {
	switch f.Get("fail") {
	case "mkdir", "mktemp":
		return errors.New("the install folder could not be written. Nothing was changed")
	case "write", "size":
		return errors.New("the binary did not arrive whole. Nothing was changed")
	case "hash":
		return errors.New("the binary that arrived is not the one that was sent. Nothing was changed")
	case "run":
		out := strings.Join(f["out"], " ")
		return errfmt.Errorf("the new binary did not run on the host, so it was not installed. It said: %s", plainLine(out))
	case "move":
		return errors.New("the new binary could not be renamed into place. The old one is still there")
	}
	if err == nil {
		return errors.New("the install did not finish")
	}
	return RemoteRunError("the install failed", stderr, err)
}

// Finish writes the action and the notes for the daemon.
func Finish(t *Target, src *Source, opts PlanOptions) {
	r := &t.Res
	if r.Error != "" {
		r.Action = "failed"
		if r.ErrorKind == federation.GateTailscaleCheck {
			r.Action = "needs approval"
		}
		return
	}
	var parts []string
	switch {
	case opts.DryRun && t.Install && r.Path == "":
		parts = append(parts, "would install")
	case opts.DryRun && t.Install:
		parts = append(parts, "would update")
	case t.Install:
		parts = append(parts, "installed")
	default:
		parts = append(parts, "up to date")
	}
	if opts.DryRun {
		if t.Install {
			r.After = src.Version
		} else {
			r.After = r.Before
		}
	}
	d := r.Daemon
	switch {
	case d.Started:
		parts = append(parts, "daemon started")
	case opts.DryRun && t.Start:
		parts = append(parts, "would start daemon")
	case d.State == DaemonStopped && !opts.Start && !t.Local:
		t.Note(fmt.Sprintf("The daemon does not run. To start it, run: tuios hosts sync %s --start", t.Name))
	case d.Upgraded:
		parts = append(parts, "daemon upgraded live")
	case d.Restarted:
		parts = append(parts, "daemon restarted")
		if d.SessionCount == 0 {
			t.Note("The daemon held no sessions, so it restarted without a question.")
		}
	case opts.DryRun && t.LiveOnly:
		parts = append(parts, "would upgrade daemon live")
	case opts.DryRun && t.Restart:
		parts = append(parts, "would upgrade or restart daemon")
	case r.RestartNeeded:
		parts = append(parts, "daemon restart needed")
		if !t.Restart {
			if d.SessionCount == 0 && !d.Unread {
				t.Note(fmt.Sprintf("The daemon still runs %s and holds no sessions. A restart loses nothing. Run: %s", cmp.Or(d.Version, "unknown"), r.RestartCommand))
			} else {
				t.Note(fmt.Sprintf("The daemon still runs %s. Its %s %s running. A client of the new version can refuse to connect to it if the protocol changed. To restart it, run: %s. A restart ends the programs in its panes.",
					cmp.Or(d.Version, "unknown"), plural.Count(d.SessionCount, "session"), plural.Word(d.SessionCount, "keeps", "keep"), r.RestartCommand))
			}
		}
	}
	switch {
	case r.ServiceInstalled:
		parts = append(parts, "service installed")
	case opts.DryRun && t.Service:
		parts = append(parts, "would install service")
	case r.Service == "none" && !opts.Service:
		t.Note(fmt.Sprintf("The daemon does not start at boot. To install the service, run: tuios hosts sync %s --service", t.Name))
	}
	if r.BeforeBackend == "ghostty" && t.Install {
		t.Note("The old binary used the ghostty backend. The new one uses the pure Go backend.")
	}
	r.Action = strings.Join(parts, ", ")
}

// ProbeBinary runs the probe script and fills in what it found.
func ProbeBinary(ctx context.Context, t *Target) error {
	arg, plain := CommandArg(t.Host.Command)
	if !plain {
		t.Note("The host has a command set in the config that is not one path. Sync looks for tuios the way the link does without one.")
	}
	out, stderr, err := t.Runner.Run(ctx, ProbeScript(), []string{arg}, nil)
	if err != nil {
		return RemoteRunError("could not read the host", stderr, err)
	}
	f := ParseFacts(out)
	goos, goarch, err := GoPlatform(f.Get("os"), f.Get("arch"))
	t.Res.OS, t.Res.Arch = goos, goarch
	if t.Res.OS == "" {
		t.Res.OS = strings.ToLower(f.Get("os"))
		t.Res.Arch = f.Get("arch")
	}
	if err != nil {
		return err
	}
	t.Home = f.Get("home")
	t.Configured = f.Get("configured")
	t.Res.Path = f.Get("path")
	t.Symlink = f.Has("symlink")
	t.DirWritable = f.Has("dirw")
	t.SHA = f.Get("sha")
	t.Res.Service = f.Get("svc")
	if vers := f["ver"]; len(vers) > 0 {
		t.Res.Before, t.Res.BeforeBackend = ParseVersionLine(vers[0])
	}
	if t.Res.Path != "" && t.Res.Before == "" {
		t.Note("The tuios on the host did not report a version.")
	}
	return nil
}

// countingReader tells tick the bytes read through it.
type countingReader struct {
	r           io.Reader
	done, total int64
	tick        func(done, total int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if n > 0 {
		c.tick(c.done, c.total)
	}
	return n, err
}
