package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// `tuios new --host` and `tuios attach --host`: a session on another machine.
//
// The default path attaches the session in this client. The connection goes
// through this machine's daemon over its link to the host, and the session is
// drawn here, with this machine's theme, config and prefix key. See
// runDaemonSessionOn.
//
// The --ssh path is the older way and the fallback: one interactive ssh to the
// host from the [hosts] table, with the terminal handed to the tuios on the
// far side. It is what works when the host's tuios is too old to serve this
// client's attach protocol, at the cost of a nested client. That ssh is a
// child, not an exec. When it fails the reason has to be said in words a
// person can act on, and with --hold the terminal has to wait so those words
// can be read before a pane that was opened for this closes.

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

// runNewOnHost is `tuios new --host HOST [NAME]`: the session is created on
// the host and attached in this client. With detach it is created and left
// running there. With ssh it is the far side's own `tuios new` over an
// interactive ssh.
func runNewOnHost(host, name string, detach, hold, ssh bool) error {
	if ssh {
		remote := []string{"new"}
		if name != "" {
			remote = append(remote, name)
		}
		if detach {
			remote = append(remote, "--detach")
		}
		return runOnHost(host, hold, !detach, remote...)
	}
	if err := ensureDaemon(); err != nil {
		return err
	}
	if detach {
		return holdAfter(runNewOnHostDetached(host, name), hold)
	}
	return holdAfter(runDaemonSessionOn(host, name, true), hold)
}

// runNewOnHostDetached creates a session on the host over the link and
// returns. It is the new-session verb, run on the host's daemon.
func runNewOnHostDetached(host, name string) error {
	client, _, err := session.DialVerbClientThroughHost(host, version)
	if err != nil {
		return explainHostConnectError(host, err)
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{}
	if name != "" {
		params["name"] = name
	}
	raw, err := client.Call("new-session", params)
	if err != nil {
		return explainHostVerbError(host, "new-session", err)
	}
	var res struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Session == "" {
		return fmt.Errorf("tuios on %s created the session and sent a reply this build cannot read", host)
	}
	fmt.Printf("Created detached session '%s' on %s. Attach with 'tuios attach --host %s %s'.\n", res.Session, host, host, res.Session)
	return nil
}

// runAttachOnHost is `tuios attach --host HOST NAME`: the session on the host,
// attached in this client. With ssh it is the far side's own `tuios attach`
// over an interactive ssh.
func runAttachOnHost(host, name string, create, hold, ssh bool) error {
	if ssh {
		remote := []string{"attach"}
		if name != "" {
			remote = append(remote, name)
		}
		if create {
			remote = append(remote, "--create")
		}
		return runOnHost(host, hold, true, remote...)
	}
	if err := ensureDaemon(); err != nil {
		return err
	}
	return holdAfter(runDaemonSessionOn(host, name, create), hold)
}

// explainHostConnectError turns a failed connection through a host into a
// message that names the machine at fault and what to do next. The daemon's
// own codes already say which of the three machines it is: this one, whose
// daemon is too old or not running; the host, which is down; or neither,
// because the name is not configured.
func explainHostConnectError(host string, err error) error {
	var connect *session.HostConnectError
	if errors.As(err, &connect) {
		fix := "run 'tuios hosts' to see the link, and 'tuios attach --host " + host + " NAME --ssh' to use ssh directly"
		switch connect.Code {
		case session.ErrVerbUnknownHost:
			fix = "run 'tuios hosts' for the configured names, or 'tuios hosts add " + host + " user@machine'"
		case session.ErrVerbProtocolMismatch, session.ErrVerbUnknownVerb:
			fix = "run 'tuios kill-server' and attach again, or 'tuios attach --host " + host + " NAME --ssh' to use ssh directly"
		case session.ErrVerbHostRefused:
			fix = "close a session on " + host + " and try again"
		}
		return &diagnosticError{
			What:  connect.Message,
			Cause: "the connection to " + host + " goes through the daemon on this machine, and it could not open one.",
			Fix:   fix,
			Err:   err,
		}
	}
	var shake *session.HostHandshakeError
	if errors.As(err, &shake) {
		return &diagnosticError{
			What:  shake.Error(),
			Cause: "the link to " + host + " is up and its tuios cannot serve this client.",
			Fix:   "upgrade tuios on " + host + " or here, or run 'tuios attach --host " + host + " NAME --ssh' to use ssh directly",
			Err:   err,
		}
	}
	return explainDialError(err)
}

// explainMissingHostSession is explainMissingSession for a session on a host.
// The names come from the host's own handshake, so they are the host's word.
func explainMissingHostSession(host, name string, names []string, err error) error {
	sort.Strings(names)
	have := "The host has no sessions."
	if len(names) > 0 {
		have = "Sessions on " + host + ": " + strings.Join(names, ", ") + "."
	}
	return &diagnosticError{
		What:  fmt.Sprintf("tuios on %s could not attach session %q: %v.", host, name, err),
		Cause: have,
		Fix:   "run 'tuios ls --host " + host + "' to list them, or 'tuios new --host " + host + "' to create one.",
		Err:   err,
	}
}

// explainHostVerbError names the machine a verb failed on. A verb the host's
// daemon does not know is that daemon being older than this tuios.
func explainHostVerbError(host, verb string, err error) error {
	var call *session.VerbCallError
	if errors.As(err, &call) && call.Code == session.ErrVerbUnknownVerb {
		return fmt.Errorf("tuios on %s is too old for %s. Upgrade tuios on %s", host, verb, host)
	}
	return fmt.Errorf("tuios on %s: %w", host, err)
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
		// The probe's own code when it found nothing, and the shell's when a
		// configured command is not there.
		return fmt.Errorf("tuios was not found on %s. Run 'tuios hosts test %s' to see where the link looked", host, host)
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
