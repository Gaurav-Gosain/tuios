package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// `tuios hosts add`, `remove` and `test`: the three commands that made a host
// something a person can manage instead of a table they hand-edit.
//
// Two rules shape all three.
//
// The file is the source of truth, not the daemon. Every one of these reads and
// writes the config file directly, so they work before a daemon has ever run,
// and the running daemon picks the change up from the file. There is no verb
// that writes a host, and adding one would put the config file behind a
// control protocol for no gain.
//
// Only the [hosts.NAME] table is touched. The rest of the file, comments
// included, is left exactly as the user wrote it. See internal/config's
// hosts_edit.go.

// hostAddFlags are the optional parts of a host, as flags.
type hostAddFlags struct {
	command    string
	timeout    int
	sshOptions []string
	tailnet    bool
	reposRoot  string
}

// newHostsSubcommands builds add, remove and test.
func newHostsSubcommands() []*cobra.Command {
	var add hostAddFlags

	addCmd := &cobra.Command{
		Use:   "add <name> <addr>",
		Short: "Add a machine to the [hosts] config table",
		Long: `Add a machine this daemon may ask for listings.

The name is what you type to name the machine. It accepts letters, digits, dot,
dash and underscore. The address is anything ssh understands, including an
ssh_config alias.

The change takes effect at once. A running daemon reads the config file and
opens the link. You do not have to restart it.

The link is tested at once, for a few seconds, and the result is printed. A
host that does not answer is still added. Run 'tuios hosts test NAME' when it
is awake.

The link finds tuios on the host by itself. It looks on the PATH, then at the
known install paths, then in a login shell. Add --command to run a given binary
instead. Then nothing is looked for.

To open a session on the host in this client, run
'tuios attach --host NAME SESSION', or press enter on its row in the rail.`,
		Example: `  # A machine you reach as user@host
  tuios hosts add build gaurav@buildbox

  # An ssh_config alias
  tuios hosts add work workstation

  # Run a given tuios binary on the host instead of the one the link finds
  tuios hosts add build gaurav@buildbox --command /opt/tools/tuios

  # A machine behind a jump host
  tuios hosts add lab lab-01 --ssh-option -J --ssh-option bastion`,
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeHostAddArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			addr := ""
			if len(args) > 1 {
				addr = args[1]
			}
			return runHostAdd(args[0], addr, add)
		},
	}
	addCmd.Flags().StringVar(&add.command, "command", "", "The tuios binary to run on the host. The link then looks for none")
	addCmd.Flags().IntVar(&add.timeout, "connect-timeout", 0, "Seconds one dial may take before the host is called unreachable (default 10)")
	addCmd.Flags().StringArrayVar(&add.sshOptions, "ssh-option", nil, "One extra argument for ssh. Repeat the flag for each one")
	addCmd.Flags().BoolVar(&add.tailnet, "tailnet", false, "Take the address from the machine of that name on your tailnet")
	addCmd.Flags().StringVar(&add.reposRoot, "repos-root", "", "Where the host keeps its checkouts, as the host reads it (e.g. ~/src). fan --host and worktree new --host look there")

	removeCmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a machine from the [hosts] config table",
		Long: `Remove a machine from the [hosts] config table.

The link closes at once. A running daemon reads the config file and drops it.
You do not have to restart the daemon.

Nothing on the other machine changes. This only stops asking it for listings.`,
		Example:           `  tuios hosts remove build`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeConfiguredHosts,
		RunE: func(_ *cobra.Command, args []string) error {
			return runHostRemove(args[0])
		},
	}

	var testOpts hostTestOptions
	testCmd := &cobra.Command{
		Use:   "test <name>",
		Short: "Open one link to a host and report what happened",
		Long: `Dial one host now and report what happened.

This runs ssh itself, so it does not need a daemon and it does not use the
links a daemon already holds. When the link works, it prints which tuios binary
the link runs on the host. When the link fails, it prints what ssh said. That
is where the real reason appears: "Permission denied", "Host key verification
failed". When the link cannot find tuios on the host, it prints every place it
looked.

tuios runs ssh with BatchMode on. A link never asks for a password and never
asks about a host key. Run ssh to the machine once by hand to accept its key.

--start starts the daemon on the host when it does not run. It uses the
tuios binary the link found there, over the same ssh. When tuios is missing on
the host, --start does nothing: run 'tuios hosts sync NAME --start' to install
it and start the daemon.`,
		Example: `  tuios hosts test build

  # Start the daemon on the host if it does not run
  tuios hosts test build --start

  # See what --start would do, and change nothing
  tuios hosts test build --start --dry-run --json`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeConfiguredHosts,
		RunE: func(_ *cobra.Command, args []string) error {
			return runHostTest(args[0], testOpts)
		},
	}
	testCmd.Flags().BoolVar(&testOpts.start, "start", false, "Start the daemon on the host when it does not run")
	testCmd.Flags().BoolVar(&testOpts.dryRun, "dry-run", false, "Show what --start would do and change nothing")
	testCmd.Flags().BoolVar(&testOpts.json, "json", false, "Print the result as JSON")

	return []*cobra.Command{addCmd, removeCmd, testCmd, newHostsTailnetCommand(), newHostsSyncCommand(), newHostsSigninCommand()}
}

// runHostAdd writes one [hosts.NAME] table.
func runHostAdd(name, addr string, flags hostAddFlags) error {
	path, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("cannot find the config file: %w", err)
	}
	if err := federation.ValidHostName(name); err != nil {
		return err
	}
	addr = strings.TrimSpace(addr)
	if flags.tailnet {
		// The name the user typed is a decision, so the filters that decide
		// what to suggest do not apply: a machine they named by hand is added
		// even if it is one the suggestion list would have left out.
		found, ok := tailnetAddrFor(name)
		if !ok {
			return fmt.Errorf("no machine called %q on your tailnet.\nRun 'tuios hosts tailnet' to see what is there", name)
		}
		if addr != "" && addr != found {
			return fmt.Errorf("host %q was given the address %s and --tailnet, which says %s.\nPass one or the other", name, addr, found)
		}
		addr = found
	}
	if addr == "" {
		return fmt.Errorf("host %q needs an address.\n%s", name, addrHelp())
	}
	if strings.HasPrefix(strings.TrimSpace(addr), "-") || strings.HasPrefix(strings.TrimSpace(flags.command), "-") {
		return fmt.Errorf("host %q was not added: the address and --command may not start with a dash, because ssh would read them as options", name)
	}
	if err := federation.CheckSSHOptions(flags.sshOptions); err != nil {
		return fmt.Errorf("host %q was not added: %w", name, err)
	}

	existing, err := config.HostsInFile(path)
	if err != nil {
		return err
	}
	prev, replaced := existing[name]

	entry := config.HostConfig{
		Addr:           addr,
		Command:        flags.command,
		ConnectTimeout: flags.timeout,
		SSHOptions:     flags.sshOptions,
		ReposRoot:      flags.reposRoot,
		// A Headscale origin is not a flag here, so a new address keeps it.
		TailscaleLogin: prev.TailscaleLogin,
		// What that machine may do here is not what this command sets, so a
		// new address keeps it.
		Allow:       prev.Allow,
		HoldMail:    prev.HoldMail,
		HostedGrace: prev.HostedGrace,
	}
	// repos_root says where the host's checkouts are, not how to reach it,
	// so pointing a host at a new address keeps it unless a new one is given.
	if entry.ReposRoot == "" {
		entry.ReposRoot = prev.ReposRoot
	}
	if err := config.SetHostInFile(path, name, entry); err != nil {
		return err
	}

	if replaced {
		fmt.Printf("Host %s now points at %s.\n", name, addr)
	} else {
		fmt.Printf("Host %s is added. Its address is %s.\n", name, addr)
	}
	if applyHostNow(name) {
		fmt.Println("A running daemon opens the link now. No restart is needed.")
	}
	probeAddedHost(name)
	return nil
}

// hostAddProbeTimeout bounds the dial 'tuios hosts add' makes right after the
// write. It is shorter than a host's own connect timeout on purpose: the add
// must not sit on a machine that is asleep, and a machine that is awake
// answers well inside this.
const hostAddProbeTimeout = 5 * time.Second

// probeAddedHost dials the host that was just added and prints what happened,
// so a tuios that cannot be found, or a key ssh refuses, is seen now rather
// than the first time a listing is wanted. The host is added whatever the
// result: a machine that is merely off is still a machine the person named.
func probeAddedHost(name string) {
	host, err := resolveConfiguredHost(name)
	if err != nil {
		return
	}
	if host.ConnectTimeout <= 0 || host.ConnectTimeout > hostAddProbeTimeout {
		host.ConnectTimeout = hostAddProbeTimeout
	}
	r, err := dialHostOnce(host, hostAddProbeTimeout+hostTestGrace)
	if err != nil {
		fmt.Printf("Run 'tuios hosts test %s' to see whether the link works.\n", name)
		return
	}
	if err := printHostTest(r); err != nil {
		fmt.Printf("The host stays in the config file. Run 'tuios hosts test %s' when it is ready.\n", name)
	}
}

// runHostRemove deletes one [hosts.NAME] table.
func runHostRemove(name string) error {
	path, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("cannot find the config file: %w", err)
	}
	removed, err := config.RemoveHostFromFile(path, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no host is named %q. Run 'tuios hosts' to see the names", name)
	}
	fmt.Printf("Host %s is removed. A running daemon closes the link now.\n", name)
	return nil
}

// hostTestBudget bounds one test. It is the dial timeout plus room for the
// handshake, so a machine that is off is reported rather than waited on.
const hostTestBudget = 20 * time.Second

// hostTestGrace is the room a bounded dial gets past its connect timeout for
// the probe and the handshake.
const hostTestGrace = 3 * time.Second

// hostTestOptions are the flags of `tuios hosts test`.
type hostTestOptions struct {
	start  bool
	dryRun bool
	json   bool
}

// hostTestResult is the JSON of `tuios hosts test`: the report of the last
// dial, and what --start did.
type hostTestResult struct {
	federation.HostReport
	// Before is the status before --start started the daemon. It is empty
	// when nothing was started.
	Before federation.Status `json:"before,omitempty"`
	// Action is what --start did, or with --dry-run what it would do.
	Action string `json:"action,omitempty"`
	// StartCommand is the command that starts the daemon on the host, as a
	// person types it there.
	StartCommand string `json:"start_command,omitempty"`
	Error        string `json:"error,omitempty"`
}

// The actions --start reports.
const (
	testActionStarted   = "daemon started"
	testActionWould     = "would start daemon"
	testActionNone      = "none"
	testActionStartFail = "start failed"
)

// runHostTest dials one host and prints what happened. With --start it starts
// the daemon there when it does not run, and dials again.
//
// The dial is this process's own, not the daemon's. That is what makes the
// command useful before a daemon has ever started, and what makes it a test of
// the host rather than a reading of a link that came up minutes ago.
func runHostTest(name string, opts hostTestOptions) error {
	host, err := resolveConfiguredHost(name)
	if err != nil {
		return err
	}
	r, err := dialHostOnce(host, hostTestBudget)
	if err != nil {
		return err
	}
	res := hostTestResult{HostReport: r}
	if r.Status == federation.StatusNoDaemon {
		res.StartCommand = remoteStartCommand(r.Command)
	}

	var startErr error
	if opts.start {
		startErr = startHostDaemon(host, &res, opts.dryRun)
		if startErr != nil {
			res.Error = startErr.Error()
		}
	}

	if opts.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return err
		}
		switch {
		case startErr != nil:
			return &diagnosticError{What: startErr.Error(), Status: 1}
		case res.Action == testActionWould, res.Status == federation.StatusUp:
			return nil
		}
		return &diagnosticError{What: fmt.Sprintf("Host %s is %s.", res.Host, res.Status), Status: 1}
	}

	if res.Action == testActionStarted {
		fmt.Printf("%s  %s  %s\n", r.Host, r.Addr, r.Status)
		fmt.Printf("No daemon ran on the host. tuios started one with this command: %s\n", res.StartCommand)
	}
	err = writeHostTest(os.Stdout, res.HostReport, opts.start)
	switch res.Action {
	case testActionWould:
		fmt.Printf("--start would run this command on the host: %s\n", res.StartCommand)
		fmt.Println("This is a dry run. Nothing is changed.")
		return startErr
	case testActionNone:
		fmt.Println("The daemon runs. --start did nothing.")
	}
	if startErr != nil {
		return startErr
	}
	return err
}

// startHostDaemon is --start: it starts the daemon on a host that has tuios
// and no running daemon, and fills res with the dial after the start.
func startHostDaemon(host federation.Host, res *hostTestResult, dryRun bool) error {
	switch res.Status {
	case federation.StatusUp:
		res.Action = testActionNone
		return nil
	case federation.StatusNoDaemon:
	case federation.StatusNoBinary:
		return &diagnosticError{
			What: fmt.Sprintf("tuios is missing on %s, so --start did not start a daemon.", res.Host),
			Fix:  fmt.Sprintf("run 'tuios hosts sync %s --start' to install tuios and start the daemon", res.Host),
		}
	default:
		// The host cannot be reached, or its daemon cannot be used. There is
		// nothing to start, and the report says what is wrong.
		return nil
	}
	arg, ok := syncCommandArg(res.Command)
	if !ok || arg == "-" {
		return &diagnosticError{
			What: fmt.Sprintf("The link runs %q on %s, which is not one path, so --start cannot run it.", res.Command, res.Host),
			Fix:  "run the start command on the host: " + res.StartCommand,
		}
	}
	if dryRun {
		res.Action = testActionWould
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncRestartTimeout)
	defer cancel()
	if err := startDaemonWith(ctx, sshSyncRunner{host: host}, arg, res.Command); err != nil {
		res.Action = testActionStartFail
		return err
	}
	res.Action = testActionStarted
	res.Before = res.Status
	// The daemon answers once start-server returns. A second dial is the
	// proof, and a few are allowed for a slow machine.
	var after federation.HostReport
	for range 3 {
		var err error
		after, err = dialHostOnce(host, hostTestBudget)
		if err != nil {
			return err
		}
		if after.Status == federation.StatusUp {
			break
		}
		time.Sleep(time.Second)
	}
	res.HostReport = after
	if after.Status != federation.StatusUp {
		return fmt.Errorf("start-server ran on %s, and the link still reports %s", res.Host, after.Status)
	}
	return nil
}

// remoteStartCommand is the command that starts the daemon on a host, with the
// binary the link runs there.
func remoteStartCommand(binary string) string {
	if binary == "" {
		binary = "tuios"
	}
	return binary + " start-server"
}

// dialHostOnce opens one link to the host in this process, waits for its
// first attempt to settle, and returns the report.
func dialHostOnce(host federation.Host, budget time.Duration) (federation.HostReport, error) {
	table, _ := federation.NewTable([]federation.Host{host})

	m := federation.New(table, federation.Options{
		Dial:            federation.SSHDialer(os.Getenv("TUIOS_SSH")),
		ClientName:      "tuios-hosts-test",
		ClientVersion:   version,
		VerbProtocol:    session.VerbProtocolVersion,
		MinVerbProtocol: session.MinVerbProtocolVersion,
	})
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	m.Start(ctx)
	reports := m.Reports(ctx)
	m.Stop()

	if len(reports) == 0 {
		return federation.HostReport{}, fmt.Errorf("host %s did not report a state. Run 'tuios hosts' to see the links", host.Name)
	}
	return reports[0], nil
}

// printHostTest prints one dial's result and fails the command when the host is
// not usable, so a script can act on it.
func printHostTest(r federation.HostReport) error {
	return writeHostTest(os.Stdout, r, false)
}

// writeHostTest is printHostTest to w. starting says the command was run with
// --start, so the advice does not tell the person to add it.
func writeHostTest(w io.Writer, r federation.HostReport, starting bool) error {
	fmt.Fprintf(w, "%s  %s  %s\n", r.Host, r.Addr, r.Status)
	if r.Status == federation.StatusApproval {
		// The link of this command stops when the command does, so the
		// person approves and runs the command again. The reason the link
		// gives says it waits, which is true only of the daemon's link.
		gate := federation.SSHGate{Kind: federation.GateTailscaleCheck, URL: r.ApprovalURL}
		fmt.Fprintln(w, gate.Sentence())
		return fmt.Errorf("host %s waits for a Tailscale approval", r.Host)
	}
	if r.Reason != "" {
		fmt.Fprintln(w, r.Reason)
	}
	if r.Detail != "" {
		// The detail comes from ssh or from the other machine. It is labelled
		// so a reader cannot mistake it for something tuios said.
		fmt.Fprintf(w, "  the link reported: %s\n", r.Detail)
	}
	switch r.Status {
	case federation.StatusUp:
		version := r.DaemonVersion
		if version == "" {
			version = "unknown"
		}
		fmt.Fprintf(w, "The host answers. It runs tuios %s and holds %s.\n", version, plural.Count(r.Sessions, "session"))
		if r.Command != "" {
			fmt.Fprintf(w, "The link runs %s on the host.\n", r.Command)
		}
		return nil
	case federation.StatusNoDaemon:
		if !starting {
			fmt.Fprintln(w, "To start the daemon, run this command on the host:")
			fmt.Fprintf(w, "  %s\n", remoteStartCommand(r.Command))
			fmt.Fprintln(w, "Or start it from this machine:")
			fmt.Fprintf(w, "  tuios hosts test %s --start\n", r.Host)
		}
	case federation.StatusNoBinary:
		// The whole reason this state exists: the person sees at once that
		// their install is somewhere unusual, and what to type about it.
		fmt.Fprintln(w, "The link looked on the PATH, in a login shell, and at these paths:")
		for _, c := range federation.RemoteBinaryCandidates() {
			fmt.Fprintf(w, "  %s\n", c)
		}
		fmt.Fprintf(w, "Install tuios on the host. 'tuios hosts sync %s --start' installs it and starts the daemon.\n", r.Host)
		fmt.Fprintf(w, "If tuios is somewhere else, run 'tuios hosts add %s %s --command PATH'.\n", r.Host, r.Addr)
	case federation.StatusIncompatible:
		fmt.Fprintln(w, "Upgrade tuios on one of the two machines.")
	default:
		// A policy refusal is already said in full. Anything else is said
		// best by ssh itself.
		if g := federation.ParseSSHGate(r.Detail); g == nil || g.Kind != federation.GateTailscalePolicy {
			fmt.Fprintln(w, "Run ssh to the machine by hand to see the whole error.")
		}
	}
	if r.Status == federation.StatusNoBinary {
		return fmt.Errorf("host %s has no tuios that the link can find", r.Host)
	}
	return fmt.Errorf("host %s is %s", r.Host, r.Status)
}

// addrHelp is what to type for an address, with the ssh_config aliases this
// machine already has as candidates.
//
// Nothing is added from this list. It is read so a person does not have to
// remember what they called a machine, and only the Host names are read: no key
// file and no known_hosts is ever opened. See internal/federation's sshalias.go.
func addrHelp() string {
	var b strings.Builder
	b.WriteString("An address is anything ssh understands, for example user@machine.")
	if aliases := federation.ReadSSHAliases(federation.UserSSHConfigPath()); len(aliases) > 0 {
		b.WriteString("\nYour ssh config names these machines:\n  ")
		b.WriteString(strings.Join(aliases, "\n  "))
	}
	if addrs := tailnetAddrs(); len(addrs) > 0 {
		b.WriteString("\nYour tailnet has these machines:\n  ")
		b.WriteString(strings.Join(addrs, "\n  "))
		b.WriteString("\nAdd one by name with 'tuios hosts add NAME --tailnet'.")
	}
	return b.String()
}

// completeHostAddArgs completes the address argument with the ssh_config
// aliases, which is where the shell can offer them without the user asking.
func completeHostAddArgs(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	candidates := append(federation.ReadSSHAliases(federation.UserSSHConfigPath()), tailnetAddrs()...)
	if len(candidates) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return candidates, cobra.ShellCompDirectiveNoFileComp
}

// completeConfiguredHosts completes a host name with the names in the config
// file.
func completeConfiguredHosts(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	path, err := config.GetConfigPath()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	hosts, err := config.HostsInFile(path)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(hosts))
	for n := range hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// applyHostNow asks a running daemon to apply the entry of host name from
// config.toml, and nothing else in the file: a change another process wrote
// there, such as a wider [agents.permissions], is not applied with it. It
// reports false only when a running daemon kept the change for the person.
// With no daemon running there is nothing to apply, and the next start reads
// the file.
func applyHostNow(name string) bool {
	client, err := dialVerb()
	if err != nil {
		return true
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Call("apply-config", map[string]any{"host": name}); err != nil {
		fmt.Println(configWaitsNote)
		return false
	}
	return true
}

// configWaitsNote is what a command says when the daemon keeps a change for
// the person.
const configWaitsNote = "The running daemon applies this change after tuios config apply from a terminal outside tuios, or a daemon restart."

// describeConfigApplied is what tuios config apply prints: each change, then
// the grant mode in force.
func describeConfigApplied(raw []byte) string {
	var res struct {
		Mode          string   `json:"mode"`
		DefaultGrants []string `json:"default_grants"`
		Changes       []string `json:"changes"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "The daemon applied config.toml.\n"
	}
	var b strings.Builder
	b.WriteString("The daemon applied config.toml.\n")
	if len(res.Changes) == 0 {
		b.WriteString("Nothing changed.\n")
	}
	for _, c := range res.Changes {
		b.WriteString(plainLine(c) + "\n")
	}
	grants := "no grants"
	if len(res.DefaultGrants) > 0 {
		grants = strings.Join(res.DefaultGrants, ", ")
	}
	fmt.Fprintf(&b, "Mode %s: a pane started with no grants of its own holds %s.\n", plainLine(res.Mode), plainLine(grants))
	return b.String()
}
