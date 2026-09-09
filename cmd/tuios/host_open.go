package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/spf13/cobra"
)

// `tuios new --host` and `tuios attach --host`: a session on another machine,
// opened the way a person opens one by hand.
//
// Both run one interactive ssh to the host from the [hosts] table and hand the
// terminal to the tuios on the far side. Nothing crosses the daemon's link:
// that link carries listings only, and the design keeps it that way until the
// verbs that write are built. What a person gets today is the capability, at
// the cost of a nested client; see the hosts help for what that costs.
//
// The ssh is a child, not an exec. When it fails the reason has to be said in
// words a person can act on, and with --hold the terminal has to wait so those
// words can be read before a pane that was opened for this closes.

// resolveConfiguredHost reads one host out of the config file by name. The
// file, not the daemon, is the source: this works before a daemon has ever
// started, and the error names every configured host.
func resolveConfiguredHost(name string) (federation.Host, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return federation.Host{}, fmt.Errorf("cannot find the config file: %w", err)
	}
	hosts, err := config.HostsInFile(path)
	if err != nil {
		return federation.Host{}, err
	}
	entry, ok := hosts[name]
	if !ok {
		names := make([]string, 0, len(hosts))
		for n := range hosts {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return federation.Host{}, fmt.Errorf("no host is named %q. No hosts are configured. Add one with 'tuios hosts add %s user@machine'", name, name)
		}
		return federation.Host{}, fmt.Errorf("no host is named %q. Configured hosts: %s", name, strings.Join(names, ", "))
	}
	table, problems := federation.NewTable([]federation.Host{{
		Name:           name,
		Addr:           entry.Addr,
		ConnectTimeout: time.Duration(entry.ConnectTimeout) * time.Second,
		Command:        entry.Command,
		SSHOptions:     entry.SSHOptions,
	}})
	if len(problems) > 0 {
		return federation.Host{}, problems[0]
	}
	return table.Lookup(name)
}

// runNewOnHost is `tuios new --host HOST [NAME]`: the far side's own `tuios
// new`, which creates the session and attaches to it in one connection. With
// detach it is the far side's `tuios new -d`, which creates and returns.
func runNewOnHost(host, name string, detach, hold bool) error {
	remote := []string{"new"}
	if name != "" {
		remote = append(remote, name)
	}
	if detach {
		remote = append(remote, "--detach")
	}
	return runOnHost(host, hold, !detach, remote...)
}

// runAttachOnHost is `tuios attach --host HOST NAME`: the far side's own
// `tuios attach`, in a terminal here.
func runAttachOnHost(host, name string, create, hold bool) error {
	remote := []string{"attach"}
	if name != "" {
		remote = append(remote, name)
	}
	if create {
		remote = append(remote, "--create")
	}
	return runOnHost(host, hold, true, remote...)
}

// runOnHost runs the host's tuios with remote as its arguments over one ssh,
// with this process's terminal. tty asks ssh for a pseudo-terminal, which an
// attach needs and a detached create does not.
func runOnHost(hostName string, hold, tty bool, remote ...string) error {
	h, err := resolveConfiguredHost(hostName)
	if err != nil {
		return err
	}
	args, err := h.OpenArgs(remote...)
	if err != nil {
		return err
	}
	if !tty {
		args = append([]string{"-T"}, args[1:]...)
	}
	cmd := exec.Command(federation.SSHBinary(), args...) //nolint:gosec // the argv is the user's own [hosts] table
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	if err == nil {
		return nil
	}
	return holdAfter(explainRemoteFailure(hostName, h.Addr, remote, err), hold)
}

// explainRemoteFailure turns a failed ssh into one message that says what
// happened and what to do next. ssh has already printed its own reason above
// it, and the remote tuios its own when it got that far.
func explainRemoteFailure(host, addr string, remote []string, err error) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return fmt.Errorf("could not run %s: %w. Set TUIOS_SSH to the ssh program to use", federation.SSHBinary(), err)
	}
	verb := "open"
	if len(remote) > 0 && remote[0] == "new" {
		verb = "create"
	}
	switch exit.ExitCode() {
	case 255:
		// ssh's own code: it never reached a shell on the far side.
		return fmt.Errorf("ssh could not reach %s (%s). Run 'tuios hosts test %s' to see why", host, addr, host)
	case 127:
		return fmt.Errorf("%s has no tuios on its PATH. Set 'command' for the host with 'tuios hosts add %s %s --command /path/to/tuios'", host, host, addr)
	default:
		return fmt.Errorf("tuios on %s could not %s the session. Its message is above", host, verb)
	}
}

// holdAfter keeps the terminal open after a failure until enter is pressed,
// when asked to. A pane the rail opened for this command closes the moment it
// exits, and a message nobody had time to read is the same as no message.
func holdAfter(err error, hold bool) error {
	if !hold || err == nil {
		return err
	}
	fmt.Fprintln(os.Stderr, err.Error())
	fmt.Fprint(os.Stderr, "Press enter to close.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	return heldError{}
}

// heldError is what runOnHost returns after it has already printed and held.
// Its text is empty so main prints nothing more, and its status is the plain
// failure code.
type heldError struct{}

func (heldError) Error() string   { return "" }
func (heldError) ExitStatus() int { return 1 }

// registerHostNameCompletion offers the configured host names for a --host
// flag, the same set 'tuios hosts remove' completes.
func registerHostNameCompletion(cmd *cobra.Command, flag string) {
	_ = cmd.RegisterFlagCompletionFunc(flag, func(c *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// The positional arguments are the session name, so they must not
		// silence the host names the way they do for 'tuios hosts remove'.
		return completeConfiguredHosts(c, nil, toComplete)
	})
}
