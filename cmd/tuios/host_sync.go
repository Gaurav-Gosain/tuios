package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// `tuios hosts sync`: bring the tuios on every host in the [hosts] table to
// the version this machine runs.
//
// The dangerous part is the daemon. A new binary on disk changes nothing that
// runs: the daemon keeps the file it started from. Restarting it ends every
// program in every pane it holds, and resurrection brings back the layout with
// new shells, not the programs. So the default installs and leaves the daemon
// alone, and says what that means. --restart restarts only the daemons whose
// version differs, and only after it has listed what ends and the person has
// agreed.
//
// The work goes in four steps, and nothing is changed before the third:
//
//  1. Probe every host at once: its system, the tuios the link finds, that
//     binary's version, and the daemon, its sessions and the panes that run
//     a program.
//  2. Plan: what each host needs. The binaries are made only for the
//     platforms that need one, each once.
//  3. Apply: upload, check, rename into place, and restart where asked.
//  4. Report: one table, or JSON.

// syncParallel bounds how many hosts are worked on at once.
const syncParallel = 4

// Budgets for each step on one host.
const (
	syncProbeTimeout   = 90 * time.Second
	syncInstallTimeout = 15 * time.Minute
	syncRestartTimeout = 90 * time.Second
)

// syncOptions are the flags.
type syncOptions struct {
	hosts   []string
	dev     bool
	src     string
	binary  string
	restart bool
	start   bool
	yes     bool
	dryRun  bool
	json    bool
	local   bool
	ghostty bool
	out     io.Writer
	errOut  io.Writer
	in      io.Reader
	// tty says whether the person can answer a question. Nil means read it
	// from stdin.
	tty *bool
}

// Daemon states.
const (
	daemonRunning = "running"
	daemonStopped = "stopped"
	daemonUnknown = "unknown"
)

// syncSession is one session on a daemon, with the panes that run a program.
type syncSession struct {
	Name  string     `json:"name"`
	Panes int        `json:"panes"`
	Busy  []syncPane `json:"busy,omitempty"`
}

// syncPane is a pane that runs a program a restart ends.
type syncPane struct {
	Window  string `json:"window"`
	Program string `json:"program"`
}

// syncDaemon is the daemon on a host.
type syncDaemon struct {
	State        string        `json:"state"`
	Version      string        `json:"version,omitempty"`
	PID          int           `json:"pid,omitzero"`
	SessionCount int           `json:"session_count"`
	Sessions     []syncSession `json:"sessions,omitempty"`
	// Unread says the sessions could not be listed, so a restart's cost is
	// not known.
	Unread    bool `json:"unread,omitzero"`
	Restarted bool `json:"restarted"`
	// Started says --start started a daemon that was not running.
	Started bool `json:"started"`
}

// syncResult is one host's row.
type syncResult struct {
	Host          string     `json:"host"`
	Addr          string     `json:"addr"`
	Local         bool       `json:"local,omitzero"`
	OS            string     `json:"os,omitempty"`
	Arch          string     `json:"arch,omitempty"`
	Path          string     `json:"path,omitempty"`
	Before        string     `json:"before,omitempty"`
	BeforeBackend string     `json:"before_backend,omitempty"`
	After         string     `json:"after,omitempty"`
	InstallPath   string     `json:"install_path,omitempty"`
	Installed     bool       `json:"installed"`
	Daemon        syncDaemon `json:"daemon"`
	// Action is what was done, or with --dry-run what would be done.
	Action         string   `json:"action"`
	RestartNeeded  bool     `json:"restart_needed"`
	RestartCommand string   `json:"restart_command,omitempty"`
	Notes          []string `json:"notes,omitempty"`
	Error          string   `json:"error,omitempty"`
	// ErrorKind names an error a script can act on: "tailscale_check" for a
	// login that waits for an approval, "tailscale_policy" for a login the
	// tailnet policy refuses. Empty for any other error.
	ErrorKind string `json:"error_kind,omitempty"`
	// ApprovalURL is where the person approves the login, with
	// "tailscale_check".
	ApprovalURL string `json:"approval_url,omitempty"`
}

// syncTarget is one machine being worked on.
type syncTarget struct {
	name   string
	host   federation.Host
	local  bool
	runner syncRunner
	res    syncResult

	// From the probe.
	home        string
	configured  string
	symlink     bool
	dirWritable bool
	sha         string

	// The plan.
	platform   string
	install    bool
	targetArg  string
	restart    bool
	start      bool
	oldArg     string
	newVersion string
	failed     bool
	// daemonNote is why the link could not read the daemon. It is kept
	// apart because it says nothing new when the probe already failed or
	// found no tuios.
	daemonNote string
	mu         sync.Mutex
}

func (t *syncTarget) note(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.res.Notes = append(t.res.Notes, s)
}

func (t *syncTarget) fail(err error) {
	t.failed = true
	t.res.Error = err.Error()
	if ge := gateErrorOf(err); ge != nil {
		t.res.ErrorKind = ge.Gate.Kind
		t.res.ApprovalURL = ge.Gate.URL
	}
}

// Where the binary comes from.
const (
	sourceRelease = "release"
	sourceDev     = "dev"
	sourceBinary  = "binary"
)

// syncBinary is one binary for one platform.
type syncBinary struct {
	data    []byte
	sha     string
	version string
}

// syncSource makes binaries.
type syncSource struct {
	kind    string
	srcDir  string
	binPath string
	// version is what the binary reports, or "" when it cannot be known
	// here (a --binary for another platform).
	version string
	// platform is the one platform a --binary is for.
	platform string

	commit   string
	date     string
	tmpDir   string
	rel      *release.Release
	sums     release.Checksums
	mu       sync.Mutex
	built    map[string]*syncBinary
	errs     map[string]error
	errOut   io.Writer
	explicit bool
}

// syncReport is the JSON result.
type syncReport struct {
	Version string       `json:"version"`
	Source  string       `json:"source"`
	DryRun  bool         `json:"dry_run"`
	OK      bool         `json:"ok"`
	Hosts   []syncResult `json:"hosts"`
}

// newHostsSyncCommand builds `tuios hosts sync`.
func newHostsSyncCommand() *cobra.Command {
	var opts syncOptions
	cmd := &cobra.Command{
		Use:   "sync [host...]",
		Short: "Install the tuios version of this machine on the hosts in the [hosts] table",
		Long: `Install the tuios version of this machine on each host in the [hosts] table.

With no names, every host in the table is synced. Name hosts to sync only
those. Name local, or add --local, to sync this machine too.

First, sync reads each host over ssh: its system, the tuios on it, the
version of that tuios, and its daemon. Then it installs where the version is
different, and prints one row for each host.

Where the binary comes from:
  A release build fetches the archive of its own release for each host from
  GitHub. It checks the archive against the published checksums. Then it
  sends the binary to the host, so the host needs no internet.
  --dev builds the binary from a tuios checkout for each host system. The
  source is --src DIR, or else the current folder. A dev build also does this
  when the current folder is a checkout. The version is dev+COMMIT.
  --binary PATH sends that file. A host with a different system is refused.

sync installs over the tuios that the host has, if you can write its folder.
Otherwise it installs to ~/.local/bin/tuios, and warns if a login shell does
not find it there. sync never uses sudo. It writes the binary to a temporary
file in the same folder, checks it, runs it once, and renames it into place.

The daemon on a host keeps the old version after an install. sync does not
restart it, because a restart ends every program in its panes. The layout
comes back with new shells. sync tells you which daemons are old and gives
the command to restart them.

--start starts the daemon on each host where it does not run. On a host
with no tuios, sync installs tuios first and then starts the daemon. sync
never starts a daemon where tuios is missing.

--restart restarts each daemon whose version is different. First it lists the
sessions and the panes with a running program on each host. Then it asks you
to agree. Without a terminal, add --yes. A daemon with no sessions restarts
without a question. A daemon with the new version is never restarted.

If Tailscale SSH asks you to approve a login, sync shows the link to open.
On a terminal, sync asks to wait for the approvals. Each host continues when
you approve it. All ssh calls to a host share one connection, so one approval
is enough for the run.

The ghostty backend cannot be cross-compiled. Build it on the host with
scripts/install.sh ghostty.`,
		Example: `  # See what sync would do, and change nothing
  tuios hosts sync --dry-run

  # Install this checkout on every host
  tuios hosts sync --dev

  # Install where tuios is missing, and start every daemon that is not running
  tuios hosts sync --start

  # Install on two hosts, and restart their daemons
  tuios hosts sync build lab --restart

  # The same, for a script or an agent
  tuios hosts sync --restart --yes --json`,
		ValidArgsFunction: completeConfiguredHosts,
		RunE: func(_ *cobra.Command, args []string) error {
			opts.hosts = args
			return runHostsSync(opts)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.dev, "dev", false, "Build the binary from a tuios checkout, not from a release")
	f.StringVar(&opts.src, "src", "", "The tuios checkout to build from. Sets --dev")
	f.StringVar(&opts.binary, "binary", "", "Send this tuios binary")
	f.BoolVar(&opts.restart, "restart", false, "Restart each daemon whose version is different")
	f.BoolVar(&opts.start, "start", false, "Start the daemon on each host where it does not run")
	f.BoolVarP(&opts.yes, "yes", "y", false, "Restart without a question. Needed for --restart without a terminal")
	f.BoolVar(&opts.dryRun, "dry-run", false, "Show the plan and change nothing")
	f.BoolVar(&opts.json, "json", false, "Print the result as JSON")
	f.BoolVar(&opts.local, "local", false, "Sync this machine too")
	f.BoolVar(&opts.ghostty, "ghostty", false, "Ask for the ghostty backend. sync refuses it, because it cannot be cross-compiled")
	return cmd
}

// runHostsSync is the whole command.
func runHostsSync(opts syncOptions) error {
	if opts.out == nil {
		opts.out = os.Stdout
	}
	if opts.errOut == nil {
		opts.errOut = os.Stderr
	}
	if opts.in == nil {
		opts.in = os.Stdin
	}
	if opts.ghostty {
		return &diagnosticError{
			What:  "sync cannot install the ghostty backend.",
			Cause: "The ghostty backend links a native library that is built for the machine it runs on, so it cannot be cross-compiled.",
			Fix:   "build it on the host: scripts/install.sh ghostty",
		}
	}

	targets, err := resolveSyncTargets(opts)
	if err != nil {
		return err
	}
	// The ssh calls of this run share one connection per host, so one
	// Tailscale SSH approval covers the run. See host_gate.go.
	share := newSSHShare(os.Getenv("TUIOS_SSH"))
	defer share.close()
	desk := newApprovalDesk(opts.errOut, opts.in, syncIsTTY(opts) && !opts.json)
	for _, t := range targets {
		if t.local {
			continue
		}
		share.share(&t.host)
		t.runner = sshSyncRunner{host: t.host, desk: desk}
	}
	src, err := chooseSyncSource(opts)
	if err != nil {
		return err
	}
	defer src.cleanup()

	// 1. Probe. The probe goes first and the daemon is read after it, so the
	// probe's connection is the one the link shares, and a host that
	// Tailscale holds asks for one approval, not two.
	forEachTarget(targets, func(t *syncTarget) {
		ctx, cancel := context.WithTimeout(context.Background(), syncProbeTimeout)
		defer cancel()
		probeErr := probeHostBinary(ctx, t)
		if gateErrorOf(probeErr) != nil {
			t.res.Daemon.State = daemonUnknown
		} else {
			// The approval wait does not count against the probe's budget,
			// so the daemon gets a budget of its own.
			dctx, dcancel := context.WithTimeout(context.Background(), syncProbeTimeout)
			if t.local {
				readLocalDaemon(dctx, t)
			} else {
				readHostDaemon(dctx, t)
			}
			dcancel()
		}
		switch {
		case probeErr != nil:
			t.fail(probeErr)
		case t.res.Path == "":
			// No tuios, so no daemon the link could reach.
			t.res.Daemon = syncDaemon{State: daemonStopped}
		case t.daemonNote != "":
			t.note(t.daemonNote)
		}
	})

	// 2. Plan.
	needed := map[string]bool{}
	for _, t := range targets {
		if t.failed {
			continue
		}
		planSyncTarget(t, src, opts)
		if t.install && !t.failed {
			needed[t.platform] = true
		}
	}
	if !opts.dryRun {
		if err := src.prepare(slices.Sorted(maps.Keys(needed))); err != nil {
			return err
		}
		for _, t := range targets {
			if t.install && !t.failed {
				if err := src.errFor(t.platform); err != nil {
					t.fail(err)
				}
			}
		}
	} else if src.kind == sourceRelease {
		// The plan should not promise a release that does not exist.
		if err := src.lookupRelease(); err != nil {
			return err
		}
	}

	// The restarts that end something are agreed to before anything moves.
	if err := confirmSyncRestarts(targets, opts); err != nil {
		return err
	}

	// 3. Apply.
	if !opts.dryRun {
		forEachTarget(targets, func(t *syncTarget) {
			if t.failed {
				return
			}
			applySyncTarget(t, src)
		})
	}
	for _, t := range targets {
		finishSyncTarget(t, src, opts)
	}

	// 4. Report.
	report := syncReport{Version: src.displayVersion(), Source: src.kind, DryRun: opts.dryRun, OK: true}
	failed := 0
	for _, t := range targets {
		if t.res.Error != "" {
			failed++
			report.OK = false
		}
		report.Hosts = append(report.Hosts, t.res)
	}
	if opts.json {
		enc := json.NewEncoder(opts.out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printSyncReport(opts.out, report, opts)
	}
	if failed > 0 {
		if opts.json {
			return &diagnosticError{What: plural.Count(failed, "host") + " failed.", Status: 1}
		}
		return fmt.Errorf("%d of %s failed. The rows above say why", failed, plural.Count(len(targets), "host"))
	}
	return nil
}

// forEachTarget runs fn for every target, syncParallel at a time.
func forEachTarget(targets []*syncTarget, fn func(*syncTarget)) {
	sem := make(chan struct{}, syncParallel)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(t)
		})
	}
	wg.Wait()
}

// resolveSyncTargets reads the hosts to sync from the config file. Only hosts
// in the file are synced, at the address the file gives.
func resolveSyncTargets(opts syncOptions) ([]*syncTarget, error) {
	cfgPath, err := config.GetConfigPath()
	if err != nil {
		return nil, fmt.Errorf("cannot find the config file: %w", err)
	}
	hosts, err := config.HostsInFile(cfgPath)
	if err != nil {
		return nil, err
	}
	local := opts.local
	var names []string
	if len(opts.hosts) == 0 {
		names = slices.Sorted(maps.Keys(hosts))
	} else {
		for _, n := range opts.hosts {
			if n == federation.LocalHostName {
				local = true
				continue
			}
			if _, ok := hosts[n]; !ok {
				return nil, fmt.Errorf("no host is named %q in the config. Run 'tuios hosts' to see the names", n)
			}
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 && !local {
		return nil, errors.New("no hosts are configured. Add one with 'tuios hosts add NAME ADDRESS', or add --local to sync this machine")
	}
	var targets []*syncTarget
	for _, n := range names {
		h, err := resolveConfiguredHost(n)
		if err != nil {
			return nil, err
		}
		targets = append(targets, &syncTarget{
			name:   n,
			host:   h,
			runner: sshSyncRunner{host: h},
			res:    syncResult{Host: n, Addr: h.Addr},
		})
	}
	if local {
		targets = append(targets, &syncTarget{
			name:   federation.LocalHostName,
			local:  true,
			runner: localSyncRunner{},
			res:    syncResult{Host: federation.LocalHostName, Addr: "this machine", Local: true},
		})
	}
	return targets, nil
}

// chooseSyncSource decides where the binaries come from.
func chooseSyncSource(opts syncOptions) (*syncSource, error) {
	src := &syncSource{built: map[string]*syncBinary{}, errs: map[string]error{}, errOut: opts.errOut}
	if opts.binary != "" {
		if opts.dev || opts.src != "" {
			return nil, errors.New("--binary sends a given file, and --dev and --src build one. Pass one or the other")
		}
		return src, src.loadBinary(opts.binary)
	}
	if opts.dev || opts.src != "" {
		dir := opts.src
		if dir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, err
			}
			found, ok := findTuiosCheckout(cwd)
			if !ok {
				return nil, &diagnosticError{
					What: "--dev builds from a tuios checkout, and the current folder is not in one.",
					Fix:  "run the command in a tuios checkout, or pass --src DIR",
				}
			}
			dir = found
		} else {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return nil, err
			}
			found, ok := findTuiosCheckout(abs)
			if !ok || found != abs {
				return nil, fmt.Errorf("%s is not a tuios checkout. --src names the folder that holds its go.mod", dir)
			}
			dir = found
		}
		src.explicit = opts.src != ""
		return src, src.initDev(dir)
	}
	if _, ok := release.ParseVersion(version); ok {
		src.kind = sourceRelease
		src.version = version
		return src, nil
	}
	// A dev build has no release to fetch. In a checkout it builds that.
	if cwd, err := os.Getwd(); err == nil {
		if dir, ok := findTuiosCheckout(cwd); ok {
			return src, src.initDev(dir)
		}
	}
	return nil, &diagnosticError{
		What:  fmt.Sprintf("This tuios is the build %q, which has no release to fetch.", version),
		Cause: "Only a release build can fetch its own release archive.",
		Fix:   "run the command in a tuios checkout with --dev, or pass --src DIR or --binary PATH",
	}
}

// findTuiosCheckout walks up from dir to the folder whose go.mod is the
// tuios module.
func findTuiosCheckout(dir string) (string, bool) {
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			line, _, _ := strings.Cut(string(data), "\n")
			if strings.TrimSpace(line) == "module github.com/"+release.Repo {
				if _, err := os.Stat(filepath.Join(dir, "cmd", "tuios")); err == nil {
					return dir, true
				}
			}
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func (s *syncSource) cleanup() {
	if s.tmpDir != "" {
		_ = os.RemoveAll(s.tmpDir)
	}
}

// displayVersion is the version the hosts are synced to.
func (s *syncSource) displayVersion() string {
	if s.version == "" {
		return "unknown"
	}
	return s.version
}

// errFor is the error making the binary for one platform hit.
func (s *syncSource) errFor(platform string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.errs[platform]; err != nil {
		return err
	}
	if s.built[platform] == nil {
		return fmt.Errorf("no binary was made for %s", platform)
	}
	return nil
}

func (s *syncSource) binaryFor(platform string) *syncBinary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.built[platform]
}

// prepare makes the binary for each platform, once each.
func (s *syncSource) prepare(platforms []string) error {
	if len(platforms) == 0 {
		return nil
	}
	switch s.kind {
	case sourceRelease:
		if err := s.lookupRelease(); err != nil {
			return err
		}
	case sourceDev:
		dir, err := os.MkdirTemp("", "tuios-hosts-sync-")
		if err != nil {
			return err
		}
		s.tmpDir = dir
	}
	for _, p := range platforms {
		var b *syncBinary
		var err error
		switch s.kind {
		case sourceRelease:
			b, err = s.fetchRelease(p)
		case sourceDev:
			b, err = s.buildDev(p)
		case sourceBinary:
			if p == s.platform {
				b = s.built[p]
			}
		}
		s.mu.Lock()
		if err != nil {
			s.errs[p] = err
		} else if b != nil {
			s.built[p] = b
		}
		s.mu.Unlock()
	}
	return nil
}

// planSyncTarget decides what one host needs.
func planSyncTarget(t *syncTarget, src *syncSource, opts syncOptions) {
	t.platform = t.res.OS + "/" + t.res.Arch
	if src.kind == sourceBinary && t.platform != src.platform {
		t.fail(fmt.Errorf("the binary is for %s and %s runs %s", src.platform, t.name, t.platform))
		return
	}
	t.newVersion = src.version
	// A binary that cannot run here tells its version only where it is
	// installed: the same bytes on the host report it.
	if t.newVersion == "" && src.kind == sourceBinary && t.sha != "" && t.sha == src.built[src.platform].sha {
		t.newVersion = t.res.Before
	}

	current := t.res.Path != "" && (sameVersion(t.res.Before, t.newVersion) ||
		(t.sha != "" && src.kind == sourceBinary && t.sha == src.built[src.platform].sha))
	t.install = !current

	// Where it goes.
	t.targetArg, t.res.InstallPath = syncInstallTarget(t)
	t.oldArg = "-"
	if t.res.Path != "" && federation.SafeRemoteArg(t.res.Path) {
		t.oldArg = t.res.Path
	}

	// The daemon.
	d := &t.res.Daemon
	mismatched := d.State == daemonRunning && !sameVersion(d.Version, t.newVersion)
	if d.State == daemonRunning && t.newVersion == "" {
		// Nothing to compare against: the version of the new binary is not
		// known until it is installed.
		mismatched = false
		if opts.restart {
			t.note("The version of the binary is not known here, so the daemon is not restarted. Install it first, then run the restart.")
		}
	}
	t.res.RestartNeeded = mismatched
	if mismatched {
		t.res.RestartCommand = syncRestartCommand(t, src)
		if opts.restart {
			switch {
			case t.local && os.Getenv("TUIOS_PANE_ID") != "":
				t.note("This command runs in a pane of the daemon on this machine, so it does not restart that daemon. Run the restart from a terminal outside tuios.")
			case d.Unread && d.SessionCount != 0:
				t.note("The sessions of the daemon could not be read, so it is not restarted. Restart it by hand on the host.")
			default:
				t.restart = true
			}
		}
	}
	if opts.start {
		switch d.State {
		case daemonStopped:
			t.start = true
		case daemonUnknown:
			t.note("The state of the daemon is not known, so sync does not start one.")
		}
	}
}

// sameVersion reports whether two version strings name the same build. A
// bare "dev" names no build in particular, so it matches nothing.
func sameVersion(a, b string) bool {
	if a == "" || b == "" || a == "dev" || b == "dev" {
		return false
	}
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// syncInstallTarget is where a host's new binary goes, as the install script
// reads it and as a person reads it.
func syncInstallTarget(t *syncTarget) (arg, display string) {
	home := strings.TrimSuffix(t.home, "/")
	fallback := func(why string) (string, string) {
		if why != "" {
			t.note(why)
		}
		return "-", home + "/.local/bin/tuios"
	}
	// A configured command is what the link runs, so a binary goes there.
	if cmdArg, ok := syncCommandArg(t.host.Command); ok && cmdArg != "-" {
		p := t.configured
		if t.res.Path != "" && !t.dirWritable {
			return fallback(fmt.Sprintf("You cannot write the folder of %s, so sync installs to ~/.local/bin. The config still runs %s.", p, t.host.Command))
		}
		return cmdArg, p
	}
	p := t.res.Path
	switch {
	case p == "":
		return fallback("")
	case !federation.SafeRemoteArg(p):
		return fallback(fmt.Sprintf("The path %s has characters sync does not send to a shell, so sync installs to ~/.local/bin.", p))
	case strings.HasPrefix(p, "/nix/"):
		return fallback(fmt.Sprintf("Nix owns %s, so sync installs to ~/.local/bin.", p))
	case t.symlink && p != home+"/.local/bin/tuios":
		return fallback(fmt.Sprintf("%s is a link that another installer may own, so sync installs to ~/.local/bin.", p))
	case !t.dirWritable:
		return fallback(fmt.Sprintf("You cannot write the folder of %s, and sync does not use sudo. It installs to ~/.local/bin.", p))
	}
	return p, p
}

// syncRestartCommand is the command that restarts one daemon later, with the
// same source as this run.
func syncRestartCommand(t *syncTarget, src *syncSource) string {
	parts := []string{"tuios", "hosts", "sync", t.name, "--restart"}
	switch src.kind {
	case sourceDev:
		if src.explicit {
			parts = append(parts, "--src", src.srcDir)
		} else {
			parts = append(parts, "--dev")
		}
	case sourceBinary:
		parts = append(parts, "--binary", src.binPath)
	}
	return strings.Join(parts, " ")
}

// confirmSyncRestarts lists what the planned restarts end and asks before any
// of them. A daemon with no sessions loses nothing and is not asked about. A
// dry run lists and does not ask.
func confirmSyncRestarts(targets []*syncTarget, opts syncOptions) error {
	var costly []*syncTarget
	for _, t := range targets {
		if t.restart && !t.failed && t.res.Daemon.SessionCount > 0 {
			costly = append(costly, t)
		}
	}
	if len(costly) == 0 {
		return nil
	}
	w := opts.errOut
	fmt.Fprintln(w, "A restart ends every program that runs in the panes of these daemons.")
	fmt.Fprintln(w, "The sessions come back with their layout and new shells.")
	for _, t := range costly {
		writeRestartCost(w, t)
	}
	if opts.yes || opts.dryRun {
		return nil
	}
	if !syncIsTTY(opts) {
		return &diagnosticError{
			What:  "--restart needs your agreement, and there is no terminal to ask on.",
			Cause: "a restart ends the programs listed above.",
			Fix:   "add --yes to restart without a question, or run the command in a terminal. Nothing was changed.",
		}
	}
	fmt.Fprint(w, "Restart these daemons? [y/N] ")
	line, _ := bufio.NewReader(opts.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errors.New("no daemon was restarted and nothing was changed")
}

// syncIsTTY reports whether the person can answer a question.
func syncIsTTY(opts syncOptions) bool {
	if opts.tty != nil {
		return *opts.tty
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// writeRestartCost writes what a restart of one daemon ends.
func writeRestartCost(w io.Writer, t *syncTarget) {
	d := t.res.Daemon
	fmt.Fprintf(w, "%s (daemon %s, %s):\n", t.name, orUnknown(d.Version), plural.Count(d.SessionCount, "session"))
	if d.Unread {
		fmt.Fprintln(w, "  The sessions could not be read.")
		return
	}
	for _, s := range d.Sessions {
		if len(s.Busy) == 0 {
			fmt.Fprintf(w, "  session %s: %s, %s at a shell prompt\n", s.Name, plural.Count(s.Panes, "pane"), plural.Word(s.Panes, "it", "each"))
			continue
		}
		var progs []string
		for _, b := range s.Busy {
			progs = append(progs, fmt.Sprintf("%s in %q", b.Program, b.Window))
		}
		fmt.Fprintf(w, "  session %s: %s. Running: %s\n", s.Name, plural.Count(s.Panes, "pane"), strings.Join(progs, ", "))
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// applySyncTarget installs and restarts on one host.
func applySyncTarget(t *syncTarget, src *syncSource) {
	if t.install {
		b := src.binaryFor(t.platform)
		if b == nil {
			t.fail(fmt.Errorf("no binary was made for %s", t.platform))
			return
		}
		if t.newVersion == "" {
			t.newVersion = b.version
		}
		restart := "0"
		if t.restart {
			restart = "1"
		}
		ctx, cancel := context.WithTimeout(context.Background(), syncInstallTimeout)
		defer cancel()
		out, stderr, err := t.runner.run(ctx, syncInstallScript(),
			[]string{t.targetArg, b.sha, strconv.Itoa(len(b.data)), restart, t.oldArg},
			bytes.NewReader(b.data))
		f := parseSyncFacts(out)
		if err != nil || f.has("fail") {
			t.fail(installError(f, stderr, err))
			return
		}
		t.res.Installed = true
		t.res.InstallPath = f.get("target")
		staged, _ := parseVersionLine(f.get("staged"))
		after, _ := parseVersionLine(f.get("after"))
		t.res.After = after
		if after == "" || after != staged {
			t.fail(fmt.Errorf("the installed binary did not run after the install. It reported %q", f.get("after")))
			return
		}
		if t.newVersion == "" {
			t.newVersion = after
		}
		if login := f.get("login"); login != t.res.InstallPath {
			if login == "" {
				t.note(fmt.Sprintf("A login shell on %s does not find tuios. Add %s to the PATH there. The link finds it anyway.", t.name, path.Dir(t.res.InstallPath)))
			} else {
				t.note(fmt.Sprintf("A login shell on %s runs %s, not %s.", t.name, login, t.res.InstallPath))
			}
		}
		if t.restart {
			restartResult(t, f)
		}
		startSyncDaemon(t)
		return
	}
	t.res.After = t.res.Before
	if t.restart {
		ctx, cancel := context.WithTimeout(context.Background(), syncRestartTimeout)
		defer cancel()
		arg := t.oldArg
		if arg == "-" {
			arg = t.targetArg
		}
		out, stderr, err := t.runner.run(ctx, syncRestartScript(), []string{arg}, nil)
		f := parseSyncFacts(out)
		if err != nil && !f.has("stopfail") && !f.has("startfail") {
			t.fail(remoteRunError("the restart failed", stderr, err))
			return
		}
		restartResult(t, f)
	}
	startSyncDaemon(t)
}

// startSyncDaemon starts the daemon on a host where it does not run, with the
// binary the host has now: the one just installed, or else the one it had.
func startSyncDaemon(t *syncTarget) {
	if !t.start || t.failed {
		return
	}
	arg, shown := t.oldArg, t.res.Path
	if t.res.Installed {
		arg, shown = t.res.InstallPath, t.res.InstallPath
		if !federation.SafeRemoteArg(arg) {
			arg = t.targetArg
		}
	}
	if arg == "-" {
		arg = t.targetArg
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncRestartTimeout)
	defer cancel()
	if err := startDaemonWith(ctx, t.runner, arg, shown); err != nil {
		t.fail(err)
		return
	}
	d := &t.res.Daemon
	d.Started = true
	d.State = daemonRunning
	d.Version = t.res.After
	t.res.RestartNeeded = false
	t.res.RestartCommand = ""
}

// restartResult reads what the install or restart script said about the
// daemon.
func restartResult(t *syncTarget, f syncFacts) {
	switch {
	case f.has("stopfail"):
		t.fail(errors.New("the old daemon did not stop. It still runs the old version"))
	case f.has("startfail"):
		t.fail(errors.New("the old daemon stopped and the new one did not start. Run 'tuios start-server' on the host. The sessions come back then"))
	case f.has("started"):
		t.res.Daemon.Restarted = true
		t.res.RestartNeeded = false
		t.res.RestartCommand = ""
	}
}

// installError is one plain sentence for a failed install.
func installError(f syncFacts, stderr string, err error) error {
	switch f.get("fail") {
	case "mkdir", "mktemp":
		return errors.New("the install folder could not be written. Nothing was changed")
	case "write", "size":
		return errors.New("the binary did not arrive whole. Nothing was changed")
	case "hash":
		return errors.New("the binary that arrived is not the one that was sent. Nothing was changed")
	case "run":
		out := strings.Join(f["out"], " ")
		return fmt.Errorf("the new binary did not run on the host, so it was not installed. It said: %s", plainLine(out))
	case "move":
		return errors.New("the new binary could not be renamed into place. The old one is still there")
	}
	if err == nil {
		return errors.New("the install did not finish")
	}
	return remoteRunError("the install failed", stderr, err)
}

// finishSyncTarget writes the action and the notes for the daemon.
func finishSyncTarget(t *syncTarget, src *syncSource, opts syncOptions) {
	r := &t.res
	if r.Error != "" {
		r.Action = "failed"
		if r.ErrorKind == federation.GateTailscaleCheck {
			r.Action = "needs approval"
		}
		return
	}
	var parts []string
	switch {
	case opts.dryRun && t.install && r.Path == "":
		parts = append(parts, "would install")
	case opts.dryRun && t.install:
		parts = append(parts, "would update")
	case t.install:
		parts = append(parts, "installed")
	default:
		parts = append(parts, "up to date")
	}
	if opts.dryRun {
		if t.install {
			r.After = src.version
		} else {
			r.After = r.Before
		}
	}
	d := r.Daemon
	switch {
	case d.Started:
		parts = append(parts, "daemon started")
	case opts.dryRun && t.start:
		parts = append(parts, "would start daemon")
	case d.State == daemonStopped && !opts.start && !t.local:
		t.note(fmt.Sprintf("The daemon does not run. To start it, run: tuios hosts sync %s --start", t.name))
	case d.Restarted:
		parts = append(parts, "daemon restarted")
		if d.SessionCount == 0 {
			t.note("The daemon held no sessions, so it restarted without a question.")
		}
	case opts.dryRun && t.restart:
		parts = append(parts, "would restart daemon")
	case r.RestartNeeded:
		parts = append(parts, "daemon restart needed")
		if !t.restart {
			if d.SessionCount == 0 && !d.Unread {
				t.note(fmt.Sprintf("The daemon still runs %s and holds no sessions. A restart loses nothing. Run: %s", orUnknown(d.Version), r.RestartCommand))
			} else {
				t.note(fmt.Sprintf("The daemon still runs %s. Its %s %s running. A client of the new version can refuse to connect to it if the protocol changed. To restart it, run: %s. A restart ends the programs in its panes.",
					orUnknown(d.Version), plural.Count(d.SessionCount, "session"), plural.Word(d.SessionCount, "keeps", "keep"), r.RestartCommand))
			}
		}
	}
	if r.BeforeBackend == "ghostty" && t.install {
		t.note("The old binary used the ghostty backend. The new one uses the pure Go backend.")
	}
	r.Action = strings.Join(parts, ", ")
}

// printSyncReport writes the table and the notes.
func printSyncReport(w io.Writer, rep syncReport, opts syncOptions) {
	rows := make([][]string, 0, len(rep.Hosts))
	for _, h := range rep.Hosts {
		system := "-"
		if h.OS != "" {
			system = h.OS + "/" + h.Arch
		}
		before := h.Before
		if before == "" {
			before = "-"
			if h.Error == "" && h.OS != "" && h.Path == "" {
				before = "none"
			}
		}
		after := h.After
		if after == "" {
			after = "-"
		}
		rows = append(rows, []string{h.Host, system, before, after, daemonCell(h.Daemon), h.Action})
	}
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("HOST", "SYSTEM", "BEFORE", "AFTER", "DAEMON", "ACTION").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("12"))
			}
			if col == 0 {
				return base.Foreground(lipgloss.Color("3")).Bold(true)
			}
			if col == 5 && rep.Hosts[row].Error != "" {
				return base.Foreground(lipgloss.Color("1"))
			}
			return base
		})
	if opts.dryRun {
		fmt.Fprintf(w, "Plan for version %s, from %s. Nothing is changed.\n", rep.Version, sourceWords(rep.Source))
	} else {
		fmt.Fprintf(w, "Version %s, from %s.\n", rep.Version, sourceWords(rep.Source))
	}
	lipgloss.Fprintln(w, t.Render())
	for _, h := range rep.Hosts {
		if h.Error != "" {
			fmt.Fprintf(w, "%s (%s): %s\n", h.Host, h.Addr, h.Error)
		}
		for _, n := range h.Notes {
			fmt.Fprintf(w, "%s: %s\n", h.Host, n)
		}
	}
}

func sourceWords(kind string) string {
	switch kind {
	case sourceRelease:
		return "the release on GitHub"
	case sourceDev:
		return "a build of the checkout"
	}
	return "the given binary"
}

// daemonCell is the DAEMON column.
func daemonCell(d syncDaemon) string {
	switch d.State {
	case daemonStopped:
		return "not running"
	case daemonRunning:
		v := orUnknown(d.Version)
		if d.Started {
			return "started"
		}
		if d.Restarted {
			return "restarted"
		}
		if d.Unread && d.SessionCount == 0 {
			return v
		}
		s := v + ", " + plural.Count(d.SessionCount, "session")
		busy := 0
		for _, ss := range d.Sessions {
			busy += len(ss.Busy)
		}
		if busy > 0 {
			s += fmt.Sprintf(", %d running", busy)
		}
		return s
	}
	return "unknown"
}
