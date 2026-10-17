package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/restartplan"
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
// alone, and says what that means. --restart moves only the daemons whose
// version differs, and only after it has listed what a restart would end and
// the person has agreed. It upgrades each live where it can, which ends
// nothing, and restarts it where it cannot, saying why.
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
	syncInstallTimeout = hostsync.InstallTimeout
	syncRestartTimeout = hostsync.RestartTimeout
)

// syncOptions are the flags.
type syncOptions struct {
	hosts   []string
	dev     bool
	src     string
	binary  string
	restart bool
	start   bool
	service bool
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

// The sync types are hostsync's, which tuios hosts setup shares.
type (
	syncDaemon = hostsync.Daemon
	syncResult = hostsync.Result
	syncTarget = hostsync.Target
)

// Daemon states.
const (
	daemonRunning = hostsync.DaemonRunning
	daemonStopped = hostsync.DaemonStopped
	daemonUnknown = hostsync.DaemonUnknown
)

// planOptions are the sync flags the plan reads.
func (o syncOptions) planOptions() hostsync.PlanOptions {
	return hostsync.PlanOptions{Restart: o.restart, Start: o.start, Service: o.service, DryRun: o.dryRun}
}

// syncSession is one session on a daemon, with the panes that run a program
// and the agents that can resume. restart-server reports the same shape.
type syncSession = restartplan.Session

// syncPane is a pane that runs a program a restart ends.
type syncPane = restartplan.Pane

// syncAgent is an agent pane whose conversation can resume after a restart.
type syncAgent = restartplan.Agent

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

With no names, every host in the table that has an addr is synced. A paired
phone has no addr, so sync skips it. Name hosts to sync only those. Name local, or add --local, to sync this machine too.

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
touch it without --restart, because a restart ends every program in its
panes. The layout comes back with new shells. sync tells you which daemons
are old and gives the command to move them.

--start starts the daemon on each host where it does not run. On a host
with no tuios, sync installs tuios first and then starts the daemon. sync
never starts a daemon where tuios is missing.

--service installs the daemon service on each host that has none (see
'tuios daemon install-service'). Then the daemon starts at boot, or at login
on macOS, and runs after the ssh session ends. A host without the service
gets a note with the command.

--restart moves each daemon whose version is different onto the new binary.
It runs 'tuios daemon upgrade --or-restart' on the host, with the binary just
installed there: the daemon is upgraded live, keeping its pid and every
program in its panes, where it can be. Where it cannot (a daemon from before
live upgrades, a refusal such as a pane on another machine, a new binary
without live upgrades) it is restarted, and the row says why. First sync
lists the sessions and the panes with a running program that a restart would
end on each host. Then it asks you to agree. Without a terminal, add --yes. A
daemon with no sessions is moved without a question. A daemon with the new
version is never touched. Run in a pane of this machine's daemon, --local
upgrades that daemon live or leaves it, and never restarts it.

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

  # Start the daemon at boot on every host
  tuios hosts sync --service

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
	f.BoolVar(&opts.restart, "restart", false, "Move each daemon whose version is different onto the new binary: live where it can, else by a restart")
	f.BoolVar(&opts.start, "start", false, "Start the daemon on each host where it does not run")
	f.BoolVar(&opts.service, "service", false, "Install the daemon service on each host that has none")
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
	// Tailscale SSH approval covers the run. See internal/hostsync/gate.go.
	share := hostsync.NewShare(os.Getenv("TUIOS_SSH"))
	defer share.Close()
	desk := hostsync.NewApprovalDesk(opts.errOut, opts.in, syncIsTTY(opts) && !opts.json)
	for _, t := range targets {
		if t.Local {
			continue
		}
		share.Share(&t.Host)
		t.Runner = hostsync.SSHRunner{Host: t.Host, Desk: desk}
	}
	src, err := chooseSyncSource(opts)
	if err != nil {
		return err
	}
	defer src.Cleanup()

	// 1. Probe. The probe goes first and the daemon is read after it, so the
	// probe's connection is the one the link shares, and a host that
	// Tailscale holds asks for one approval, not two.
	forEachTarget(targets, func(t *syncTarget) {
		ctx, cancel := context.WithTimeout(context.Background(), syncProbeTimeout)
		defer cancel()
		probeErr := hostsync.ProbeBinary(ctx, t)
		if hostsync.GateErrorOf(probeErr) != nil {
			t.Res.Daemon.State = daemonUnknown
		} else {
			// The approval wait does not count against the probe's budget,
			// so the daemon gets a budget of its own.
			dctx, dcancel := context.WithTimeout(context.Background(), syncProbeTimeout)
			if t.Local {
				readLocalDaemon(dctx, t)
			} else {
				readHostDaemon(dctx, t)
			}
			dcancel()
		}
		switch {
		case probeErr != nil:
			t.Fail(probeErr)
		case t.Res.Path == "":
			// No tuios, so no daemon the link could reach.
			t.Res.Daemon = syncDaemon{State: daemonStopped}
		case t.DaemonNote != "":
			t.Note(t.DaemonNote)
		}
	})

	// 2. Plan.
	needed := map[string]bool{}
	for _, t := range targets {
		if t.Failed {
			continue
		}
		hostsync.Plan(t, src, opts.planOptions())
		if t.Install && !t.Failed {
			needed[t.Platform] = true
		}
	}
	if !opts.dryRun {
		if err := src.Prepare(slices.Sorted(maps.Keys(needed))); err != nil {
			return err
		}
		for _, t := range targets {
			if t.Install && !t.Failed {
				if err := src.ErrFor(t.Platform); err != nil {
					t.Fail(err)
				}
			}
		}
	} else if src.Kind == hostsync.KindRelease {
		// The plan should not promise a release that does not exist.
		if err := src.LookupRelease(); err != nil {
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
			if t.Failed {
				return
			}
			hostsync.Apply(t, src)
		})
	}
	for _, t := range targets {
		hostsync.Finish(t, src, opts.planOptions())
	}

	// 4. Report.
	report := syncReport{Version: src.DisplayVersion(), Source: src.Kind, DryRun: opts.dryRun, OK: true}
	failed := 0
	for _, t := range targets {
		if t.Res.Error != "" {
			failed++
			report.OK = false
		}
		report.Hosts = append(report.Hosts, t.Res)
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
		return errfmt.Errorf("%d of %s failed. The rows above say why", failed, plural.Count(len(targets), "host"))
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
		return nil, errfmt.Errorf("cannot find the config file: %w", err)
	}
	hosts, err := config.HostsInFile(cfgPath)
	if err != nil {
		return nil, err
	}
	local := opts.local
	var names []string
	if len(opts.hosts) == 0 {
		// A table with no addr holds only the link policy of a device that
		// dials in, such as a phone added by tuios pair. Nothing can ssh there.
		for _, n := range slices.Sorted(maps.Keys(hosts)) {
			if strings.TrimSpace(hosts[n].Addr) != "" {
				names = append(names, n)
			}
		}
	} else {
		for _, n := range opts.hosts {
			if n == federation.LocalHostName {
				local = true
				continue
			}
			if _, ok := hosts[n]; !ok {
				return nil, errfmt.Errorf("no host is named %q in the config. Run 'tuios hosts' to see the names", n)
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
			Name:   n,
			Host:   h,
			Runner: hostsync.SSHRunner{Host: h},
			Res:    syncResult{Host: n, Addr: h.Addr},
		})
	}
	if local {
		targets = append(targets, &syncTarget{
			Name:   federation.LocalHostName,
			Local:  true,
			Runner: hostsync.LocalRunner{},
			Res:    syncResult{Host: federation.LocalHostName, Addr: "this machine", Local: true},
		})
	}
	return targets, nil
}

// chooseSyncSource decides where the binaries come from.
func chooseSyncSource(opts syncOptions) (*hostsync.Source, error) {
	src := hostsync.NewSource(opts.errOut)
	src.Releases = updateReleases{}
	if opts.binary != "" {
		if opts.dev || opts.src != "" {
			return nil, errors.New("--binary sends a given file, and --dev and --src build one. Pass one or the other")
		}
		return src, src.LoadBinary(opts.binary)
	}
	if opts.dev || opts.src != "" {
		dir := opts.src
		if dir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, err
			}
			found, ok := hostsync.FindTuiosCheckout(cwd)
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
			found, ok := hostsync.FindTuiosCheckout(abs)
			if !ok || found != abs {
				return nil, errfmt.Errorf("%s is not a tuios checkout. --src names the folder that holds its go.mod", dir)
			}
			dir = found
		}
		src.Explicit = opts.src != ""
		return src, src.InitDev(dir)
	}
	if _, ok := release.ParseVersion(version); ok {
		src.Kind = hostsync.KindRelease
		src.Version = version
		return src, nil
	}
	// A dev build has no release to fetch. In a checkout it builds that.
	if cwd, err := os.Getwd(); err == nil {
		if dir, ok := hostsync.FindTuiosCheckout(cwd); ok {
			return src, src.InitDev(dir)
		}
	}
	return nil, &diagnosticError{
		What:  fmt.Sprintf("This tuios is the build %q, which has no release to fetch.", version),
		Cause: "Only a release build can fetch its own release archive.",
		Fix:   "run the command in a tuios checkout with --dev, or pass --src DIR or --binary PATH",
	}
}

// updateReleases gives hosts sync the release helpers of tuios update, so a
// failed lookup or download reads the same in both commands.
type updateReleases struct{}

func (updateReleases) ExplainLookupError(err error) error { return explainLookupError(err, false) }

func (updateReleases) FetchChecksums(ctx context.Context, src release.Source, rel release.Release) (release.Checksums, error) {
	return fetchChecksums(ctx, src, rel)
}

func (updateReleases) ReadAsset(ctx context.Context, src release.Source, asset release.Asset) ([]byte, error) {
	return readAsset(ctx, src, asset)
}

func (updateReleases) ExplainChecksumError(err error, rel release.Release) error {
	return explainChecksumError(err, rel)
}

// confirmSyncRestarts lists what the planned restarts end and asks before any
// of them. A daemon with no sessions loses nothing and is not asked about. A
// dry run lists and does not ask.
func confirmSyncRestarts(targets []*syncTarget, opts syncOptions) error {
	var costly []*syncTarget
	for _, t := range targets {
		if t.Restart && !t.LiveOnly && !t.Failed && t.Res.Daemon.SessionCount > 0 {
			costly = append(costly, t)
		}
	}
	if len(costly) == 0 {
		return nil
	}
	w := opts.errOut
	fmt.Fprintln(w, "Each daemon is upgraded live where it can be, which keeps every program running.")
	fmt.Fprintln(w, "Where it cannot, it is restarted, which ends every program that runs in its panes.")
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
	d := t.Res.Daemon
	fmt.Fprintf(w, "%s (daemon %s, %s):\n", t.Name, cmp.Or(d.Version, "unknown"), plural.Count(d.SessionCount, "session"))
	if d.Unread {
		fmt.Fprintln(w, "  The sessions could not be read.")
		return
	}
	writeSessionCost(w, d.Sessions)
}

// writeSessionCost writes one line per session: its panes, and the programs
// a restart ends.
func writeSessionCost(w io.Writer, sessions []syncSession) {
	for _, s := range sessions {
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
		if h.Fix != "" {
			fmt.Fprintf(w, "  %s\n", h.Fix)
		}
		for _, n := range h.Notes {
			fmt.Fprintf(w, "%s: %s\n", h.Host, n)
		}
	}
}

func sourceWords(kind string) string {
	switch kind {
	case hostsync.KindRelease:
		return "the release on GitHub"
	case hostsync.KindDev:
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
		v := cmp.Or(d.Version, "unknown")
		if d.Started {
			return "started"
		}
		if d.Upgraded {
			return "upgraded"
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
