package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
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

Run 'tuios hosts test NAME' after this to see whether the link works.

To open a session on the host in this client, run
'tuios attach --host NAME SESSION', or press enter on its row in the rail.`,
		Example: `  # A machine you reach as user@host
  tuios hosts add build gaurav@buildbox

  # An ssh_config alias
  tuios hosts add work workstation

  # A machine where tuios is not on the non-interactive PATH
  tuios hosts add build gaurav@buildbox --command ~/.local/bin/tuios

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
	addCmd.Flags().StringVar(&add.command, "command", "", "The tuios binary on the far side, for a machine where it is not on the PATH")
	addCmd.Flags().IntVar(&add.timeout, "connect-timeout", 0, "Seconds one dial may take before the host is called unreachable (default 10)")
	addCmd.Flags().StringArrayVar(&add.sshOptions, "ssh-option", nil, "One extra argument for ssh. Repeat the flag for each one")

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

	testCmd := &cobra.Command{
		Use:   "test <name>",
		Short: "Open one link to a host and report what happened",
		Long: `Dial one host now and report what happened.

This runs ssh itself, so it does not need a daemon and it does not use the
links a daemon already holds. When the link fails, it prints what ssh said.
That is where the real reason appears: "Permission denied", "Host key
verification failed", "command not found".

tuios runs ssh with BatchMode on. A link never asks for a password and never
asks about a host key. Run ssh to the machine once by hand to accept its key.`,
		Example:           `  tuios hosts test build`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeConfiguredHosts,
		RunE: func(_ *cobra.Command, args []string) error {
			return runHostTest(args[0])
		},
	}

	return []*cobra.Command{addCmd, removeCmd, testCmd}
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
	if addr == "" {
		return fmt.Errorf("host %q needs an address.\n%s", name, addrHelp())
	}

	existing, err := config.HostsInFile(path)
	if err != nil {
		return err
	}
	_, replaced := existing[name]

	entry := config.HostConfig{
		Addr:           addr,
		Command:        flags.command,
		ConnectTimeout: flags.timeout,
		SSHOptions:     flags.sshOptions,
	}
	if err := config.SetHostInFile(path, name, entry); err != nil {
		return err
	}

	if replaced {
		fmt.Printf("Host %s now points at %s.\n", name, addr)
	} else {
		fmt.Printf("Host %s is added. Its address is %s.\n", name, addr)
	}
	fmt.Println("A running daemon opens the link now. No restart is needed.")
	fmt.Printf("Run 'tuios hosts test %s' to see whether it works.\n", name)
	return nil
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

// runHostTest dials one host and prints what happened.
//
// The dial is this process's own, not the daemon's. That is what makes the
// command useful before a daemon has ever started, and what makes it a test of
// the host rather than a reading of a link that came up minutes ago.
func runHostTest(name string) error {
	host, err := resolveConfiguredHost(name)
	if err != nil {
		return err
	}
	table, _ := federation.NewTable([]federation.Host{host})

	m := federation.New(table, federation.Options{
		Dial:            federation.SSHDialer(os.Getenv("TUIOS_SSH")),
		ClientName:      "tuios-hosts-test",
		ClientVersion:   version,
		VerbProtocol:    session.VerbProtocolVersion,
		MinVerbProtocol: session.MinVerbProtocolVersion,
	})
	ctx, cancel := context.WithTimeout(context.Background(), hostTestBudget)
	defer cancel()
	m.Start(ctx)
	reports := m.Reports(ctx)
	m.Stop()

	if len(reports) == 0 {
		return fmt.Errorf("host %s did not report a state. Run 'tuios hosts' to see the links", name)
	}
	return printHostTest(reports[0])
}

// printHostTest prints one dial's result and fails the command when the host is
// not usable, so a script can act on it.
func printHostTest(r federation.HostReport) error {
	fmt.Printf("%s  %s  %s\n", r.Host, r.Addr, r.Status)
	if r.Reason != "" {
		fmt.Println(r.Reason)
	}
	if r.Detail != "" {
		// The detail comes from ssh or from the other machine. It is labelled
		// so a reader cannot mistake it for something tuios said.
		fmt.Printf("  the link reported: %s\n", r.Detail)
	}
	switch r.Status {
	case federation.StatusUp:
		version := r.DaemonVersion
		if version == "" {
			version = "unknown"
		}
		fmt.Printf("The host answers. It runs tuios %s and holds %d session(s).\n", version, r.Sessions)
		return nil
	case federation.StatusNoDaemon:
		fmt.Println("Start tuios on that machine, then test it again.")
	case federation.StatusIncompatible:
		fmt.Println("Upgrade tuios on one of the two machines.")
	default:
		fmt.Println("Run ssh to the machine by hand to see the whole error.")
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
	aliases := federation.ReadSSHAliases(federation.UserSSHConfigPath())
	if len(aliases) == 0 {
		return b.String()
	}
	b.WriteString("\nYour ssh config names these machines:\n  ")
	b.WriteString(strings.Join(aliases, "\n  "))
	return b.String()
}

// completeHostAddArgs completes the address argument with the ssh_config
// aliases, which is where the shell can offer them without the user asking.
func completeHostAddArgs(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	aliases := federation.ReadSSHAliases(federation.UserSSHConfigPath())
	if len(aliases) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return aliases, cobra.ShellCompDirectiveNoFileComp
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
