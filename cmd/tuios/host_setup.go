package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/hostsetup"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"github.com/spf13/cobra"
)

// `tuios hosts setup`: set up another machine in one command. The steps are
// internal/hostsetup's. This file is the flag mode, for scripts and agents:
// every choice comes from a flag, nothing waits on a prompt, and a choice
// that is missing stops the command before it changes anything, with the
// flag to pass.

// Exit statuses of a setup that stopped.
const (
	setupNeedChoice = 2
	setupNeedPerson = 3
)

// hostSetupOptions are the flags.
type hostSetupOptions struct {
	name       string
	install    string
	manager    string
	service    bool
	noService  bool
	bothWays   bool
	oneWay     bool
	address    string
	yes        bool
	dryRun     bool
	json       bool
	keep       bool
	sshOptions []string
	tailnet    bool
	all        bool
	only       []string
}

func newHostsSetupCommand() *cobra.Command {
	var o hostSetupOptions
	cmd := &cobra.Command{
		Use:   "setup [DEST] [NAME]",
		Short: "Set up a machine: test ssh, install tuios, start it and add it",
		Long: `Set up another machine for tuios in one command.

DEST is the machine as ssh takes it: user@machine, or a name in your ssh
config. tuios reads the machine over ssh with BatchMode on. It changes
nothing there until you agree.

The steps:
  1. Test ssh. A failure gets its one fix, such as ssh-copy-id.
  2. Install tuios when it is missing: a copy of this tuios, the release of
     this version, the package manager there, or the install script.
  3. Start the daemon there, and at boot when you ask.
  4. Add the machine here as NAME, and test the link. A link that does not
     come up is removed again.
  5. With --both-ways, add this machine on that one too. That machine tests
     its way back first. Nothing is added there when it cannot connect.

Without a terminal every choice comes from a flag. A choice that is missing
stops the command before it changes anything (exit 2). A fix that needs you,
such as a password or a host key to check, stops it with the command to run
(exit 3).

--tailnet lists the machines on your tailnet with their state. Add --all, or
--only with names, to set them up with one set of answers. At most 4 run at
once, and one failure does not stop the others.

tuios never takes a password. It never accepts a changed host key.`,
		Example: `  # See what the setup would do, and change nothing
  tuios hosts setup gaurav@buildbox --dry-run

  # Copy this tuios there, start it at boot, add it both ways
  tuios hosts setup gaurav@buildbox --name build --install copy --service --both-ways --yes

  # The same, for a script or an agent
  tuios hosts setup gaurav@buildbox --install copy --yes --json

  # Every machine on the tailnet that can be set up
  tuios hosts setup --tailnet --all --install copy --service --yes`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if o.tailnet {
				if len(args) > 0 {
					return errors.New("--tailnet takes no address. Pass --only with names instead")
				}
				return runTailnetSetup(o)
			}
			if len(args) == 0 {
				return &diagnosticError{What: "Give the machine to set up.", Fix: "tuios hosts setup user@machine, or tuios hosts setup --tailnet", Status: setupNeedChoice}
			}
			if len(args) == 2 {
				if o.name != "" && o.name != args[1] {
					return errfmt.Errorf("the name is %s and --name says %s. Pass one", args[1], o.name)
				}
				o.name = args[1]
			}
			return runHostSetup(args[0], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.name, "name", "", "The name of the machine here (default: the ssh config name, or the host part)")
	f.StringVar(&o.install, "install", "", "How to install tuios when it is missing: copy, release, pkg, script or none")
	f.StringVar(&o.manager, "pkg", "", "The package manager for --install pkg: yay, paru, brew, nix or go")
	f.BoolVar(&o.service, "service", false, "Start tuios at boot on the machine")
	f.BoolVar(&o.noService, "no-service", false, "Do not start tuios at boot")
	f.BoolVar(&o.bothWays, "both-ways", false, "Add this machine on the other machine too")
	f.BoolVar(&o.oneWay, "one-way", false, "Do not add this machine on the other machine")
	f.StringVar(&o.address, "address", "", "This machine's address for the other machine, with --both-ways")
	f.BoolVarP(&o.yes, "yes", "y", false, "Agree to every step the flags name")
	f.BoolVar(&o.dryRun, "dry-run", false, "Test ssh and print the plan. Change nothing")
	f.BoolVar(&o.json, "json", false, "Print the steps and the result as JSON")
	f.BoolVar(&o.keep, "keep", false, "Keep the machine in the list when its link does not come up")
	f.StringArrayVar(&o.sshOptions, "ssh-option", nil, "One extra argument for ssh. Repeat the flag for each one")
	f.BoolVar(&o.tailnet, "tailnet", false, "Set up machines on your tailnet")
	f.BoolVar(&o.all, "all", false, "With --tailnet, every machine that can be set up")
	f.StringSliceVar(&o.only, "only", nil, "With --tailnet, only these machines, by name")
	cmd.MarkFlagsMutuallyExclusive("service", "no-service")
	cmd.MarkFlagsMutuallyExclusive("both-ways", "one-way")
	cmd.MarkFlagsMutuallyExclusive("all", "only")
	return cmd
}

// choices turns the flags into the engine's choices.
func (o hostSetupOptions) choices() hostsetup.Choices {
	c := hostsetup.Choices{Install: o.install, Manager: o.manager, Address: o.address, Yes: o.yes, DryRun: o.dryRun, Keep: o.keep}
	switch {
	case o.service:
		c.Service = new(true)
	case o.noService:
		c.Service = new(false)
	}
	switch {
	case o.bothWays:
		c.BothWays = new(true)
	case o.oneWay:
		c.BothWays = new(false)
	}
	return c
}

// setupBase is the engine's options for this process.
func setupBase(o hostSetupOptions) (hostsetup.Options, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return hostsetup.Options{}, errfmt.Errorf("cannot find the config file: %w", err)
	}
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	src := hostsync.NewSource(os.Stderr)
	src.Kind = hostsync.KindRelease
	src.Version = version
	src.Releases = updateReleases{}
	return hostsetup.Options{
		Source:     src,
		Name:       o.name,
		SSHOptions: o.sshOptions,
		Version:    version,
		Executable: exe,
		ConfigPath: path,
		Apply: func(name string) error {
			applyHostNow(name)
			return nil
		},
	}, nil
}

// setupContext ends on Ctrl+C or SIGTERM, so each machine stops at a step
// boundary.
func setupContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// runHostSetup is the flag mode for one machine.
func runHostSetup(dest string, o hostSetupOptions) error {
	base, err := setupBase(o)
	if err != nil {
		return err
	}
	base.Dest = dest
	s, err := hostsetup.New(base)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := setupContext()
	defer cancel()
	res, runErr := s.Run(ctx, o.choices(), &flagAsker{out: os.Stdout, quiet: o.json || o.dryRun, yes: o.yes, name: s.Name()})
	switch {
	case o.json:
		outputJSON(res)
	case o.dryRun:
		writeSetupPlan(os.Stdout, res.Steps)
	case runErr == nil:
		fmt.Printf("\n%s is ready. Its agents reach your Inbox.\nOpen a session: tuios attach --host %s\n", res.Name, res.Name)
	}
	return setupExit(runErr)
}

// setupExit is the command's error for a setup that stopped: status 2 for a
// missing choice, 3 for a step that needs the person, 1 for the rest.
func setupExit(err error) error {
	if err == nil {
		return nil
	}
	d := &diagnosticError{What: err.Error(), Status: 1}
	if _, ok := errors.AsType[*hostsetup.NeedChoice](err); ok {
		d.Status, d.Fix = setupNeedChoice, "run with --dry-run to see each command"
	}
	if f, ok := errors.AsType[*hostsetup.Failure](err); ok && f.NeedsPerson {
		d.Status = setupNeedPerson
	}
	if _, ok := errors.AsType[*hostsetup.NeedPerson](err); ok {
		d.Status = setupNeedPerson
	}
	if errors.Is(err, context.Canceled) {
		d.What = "Stopped. Nothing more was changed."
	}
	return d
}

// writeSetupPlan prints a dry run: what the probe found, then every command
// in order with where it runs.
func writeSetupPlan(w io.Writer, steps []hostsetup.Event) {
	n := 0
	for _, st := range steps {
		switch {
		case st.Step == hostsetup.StepProbe:
			fmt.Fprintf(w, "%s\nThis changed nothing.\n\nThe plan. Nothing runs.\n", plainLine(st.Text))
		case st.State == hostsetup.StatePlan:
			cmds := st.Commands
			if len(cmds) == 0 {
				cmds = []hostsetup.Command{{Where: "here", Line: st.Text}}
			}
			for _, c := range cmds {
				n++
				fmt.Fprintf(w, "  %2d  %-10s %s\n", n, c.Where, c.Line)
			}
		}
	}
	fmt.Fprintln(w, "\nRun it again without --dry-run, with --yes, to do it.")
}

// flagAsker is the front end of the flag mode. It asks nothing: a choice the
// flags did not make is either off (start at boot, both ways, keep) or a
// NeedChoice that names the flag.
type flagAsker struct {
	out   io.Writer
	quiet bool
	yes   bool
	name  string
	// mu keeps whole the lines of machines that run at once, and prefix
	// names the machine on each line, in a bulk run.
	mu     *sync.Mutex
	prefix string
}

func (a *flagAsker) Event(e hostsetup.Event) {
	if a.quiet || e.State == hostsetup.StateRun || e.State == hostsetup.StatePlan {
		return
	}
	if a.mu != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
	}
	fmt.Fprintf(a.out, "%s%s %s\n", a.prefix, setupMark(e.State), plainLine(e.Text))
	if e.Detail != "" {
		fmt.Fprintf(a.out, "%s    %s\n", a.prefix, plainLine(e.Detail))
	}
}

// setupMark is the mark of a step state on a plain line.
func setupMark(state string) string {
	switch state {
	case hostsetup.StateOK:
		return "✓"
	case hostsetup.StateFailed:
		return "✗"
	case hostsetup.StatePerson:
		return "!"
	}
	return "-"
}

func (a *flagAsker) PickInstall(context.Context, []hostsetup.InstallOption) (hostsetup.InstallOption, error) {
	return hostsetup.InstallOption{}, &hostsetup.NeedChoice{
		Flag: "--install copy, release, pkg or script",
		What: "tuios is not installed on " + a.name + ".",
	}
}

func (a *flagAsker) Confirm(_ context.Context, q hostsetup.Question) (bool, error) {
	switch q.ID {
	case "service", "both_ways", "keep":
		// Extras are off unless a flag turns them on.
		return false, nil
	}
	if a.yes {
		return true, nil
	}
	return false, &hostsetup.NeedChoice{Flag: "--yes", What: strings.TrimSuffix(q.Text, "?") + " needs your yes."}
}

func (a *flagAsker) Fix(_ context.Context, f *hostsetup.Failure) (bool, error) {
	if f.NeedsPerson {
		step := &hostsetup.PersonStep{Why: f.Reason + " " + f.Fix, Line: f.FixCommand, URL: f.URL}
		return false, &hostsetup.NeedPerson{Step: step}
	}
	return false, nil
}

func (a *flagAsker) Person(_ context.Context, p *hostsetup.PersonStep) error {
	return &hostsetup.NeedPerson{Step: p}
}

// runTailnetSetup is the flag mode for a tailnet.
func runTailnetSetup(o hostSetupOptions) error {
	base, err := setupBase(o)
	if err != nil {
		return err
	}
	opt, _ := tailnetOptions()
	added, err := hostsetup.ConfiguredHosts(base.ConfigPath)
	if err != nil {
		return err
	}
	ctx, cancel := setupContext()
	defer cancel()
	peers, err := hostsetup.ListTailnet(ctx, opt, added, version)
	if err != nil {
		return &diagnosticError{What: "tuios cannot read your tailnet: " + err.Error(), Fix: "check that tailscale runs: tailscale status"}
	}
	if !o.all && len(o.only) == 0 {
		if o.json {
			outputJSON(map[string]any{"machines": peers})
		} else {
			fmt.Printf("%-18s %-8s %-36s %s\n", "NAME", "SYSTEM", "STATE", "CAN SET UP")
			for _, p := range peers {
				fmt.Printf("%-18s %-8s %-36s %v\n", p.Name, p.OS, plainLine(p.Why), p.CanSetUp)
			}
			fmt.Println("\nPass --all, or --only with the names, to pick machines.")
		}
		if o.dryRun {
			return nil
		}
		return &diagnosticError{What: "Pick the machines to set up.", Fix: "pass --all, or --only with the names", Status: setupNeedChoice}
	}
	var picked []hostsetup.Peer
	var skipped []hostsetup.Skip
	for _, n := range o.only {
		if !slices.ContainsFunc(peers, func(p hostsetup.Peer) bool { return p.Name == n }) {
			return errfmt.Errorf("no machine called %s is on your tailnet. Run tuios hosts setup --tailnet --dry-run to see the names", n)
		}
	}
	for _, p := range peers {
		switch {
		case !o.all && !slices.Contains(o.only, p.Name):
		case p.CanSetUp:
			picked = append(picked, p)
		default:
			skipped = append(skipped, hostsetup.Skip{Name: p.Name, Why: p.Why})
		}
	}
	res := hostsetup.RunBulk(ctx, base, picked, o.choices(), nil, &flagBulkAsker{quiet: o.json, yes: o.yes})
	res.Skipped = skipped
	res.Summary.Skipped = len(skipped)
	if o.json {
		outputJSON(res)
	} else {
		sm := res.Summary
		fmt.Printf("\n%d ready. %d need you. %d failed. %d skipped.\n", sm.Ready, sm.NeedYou, sm.Failed, sm.Skipped)
		for _, m := range res.Machines {
			if !m.OK {
				fmt.Printf("%s: %s\n", m.Name, plainLine(m.Error))
			}
		}
	}
	switch {
	case res.Summary.Failed > 0:
		return &diagnosticError{What: "Some machines failed.", Status: 1}
	case res.Summary.NeedYou > 0:
		return &diagnosticError{What: "Some machines wait for you.", Status: setupNeedPerson}
	}
	return nil
}

// flagBulkAsker gives each machine a flagAsker whose lines name it.
type flagBulkAsker struct {
	quiet, yes bool
	mu         sync.Mutex
}

func (b *flagBulkAsker) For(name string) hostsetup.Asker {
	return &flagAsker{out: os.Stdout, mu: &b.mu, quiet: b.quiet, yes: b.yes, name: name, prefix: name + ": "}
}
