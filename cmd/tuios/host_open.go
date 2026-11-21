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
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

func resolveConfiguredHost(name string) (federation.Host, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return federation.Host{}, errfmt.Errorf("cannot find the config file: %w", err)
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
			return federation.Host{}, errfmt.Errorf("no host is named %q. No hosts are configured. Add one with 'tuios hosts add %s user@machine'", name, name)
		}
		return federation.Host{}, errfmt.Errorf("no host is named %q. Configured hosts: %s", name, strings.Join(names, ", "))
	}
	table, problems := federation.NewTable([]federation.Host{{
		Name:           name,
		Addr:           entry.Addr,
		ConnectTimeout: time.Duration(entry.ConnectTimeout) * time.Second,
		Command:        entry.Command,
		SSHOptions:     entry.SSHOptions,
		TailscaleLogin: entry.TailscaleLogin,
	}})
	if len(problems) > 0 {
		return federation.Host{}, problems[0]
	}
	return table.Lookup(name)
}

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
	if err := jsonx.Unmarshal(raw, &res); err != nil || res.Session == "" {
		return errfmt.Errorf("tuios on %s created the session and sent a reply this build cannot read", host)
	}
	fmt.Printf("Created detached session '%s' on %s. Attach with 'tuios attach --host %s %s'.\n", res.Session, host, host, res.Session)
	return nil
}

func runAttachOnHost(host, name string, create, hold, ssh bool) error {
	if ssh {
		remote := []string{"attach"}
		if name != "" {
			remote = append(remote, name)
		}
		if create {
			remote = append(remote, "--create")
		}
		if attachDetachOthers {
			remote = append(remote, "-d")
		}
		return runOnHost(host, hold, true, remote...)
	}
	if err := ensureDaemon(); err != nil {
		return err
	}
	return holdAfter(runDaemonSessionOn(host, name, create), hold)
}

func explainHostConnectError(host string, err error) error {
	if connect, ok := errors.AsType[*session.HostConnectError](err); ok {
		fix := "run 'tuios hosts' to see the link, and 'tuios attach --host " + host + " NAME --ssh' to use ssh directly"
		switch connect.Code {
		case session.ErrVerbUnknownHost:
			fix = "run 'tuios hosts' for the configured names, or 'tuios hosts add " + host + " user@machine'"
		case session.ErrVerbProtocolMismatch, session.ErrVerbUnknownVerb:
			fix = "run 'tuios restart-server' and attach again, or 'tuios attach --host " + host + " NAME --ssh' to use ssh directly"
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
	if shake, ok := errors.AsType[*session.HostHandshakeError](err); ok {
		return &diagnosticError{
			What:  shake.Error(),
			Cause: "the link to " + host + " is up and its tuios cannot serve this client.",
			Fix:   "upgrade tuios on " + host + " or here, or run 'tuios attach --host " + host + " NAME --ssh' to use ssh directly",
			Err:   err,
		}
	}
	return explainDialError(err)
}

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

func explainHostVerbError(host, verb string, err error) error {
	var call *session.VerbCallError
	if errors.As(err, &call) && call.Code == session.ErrVerbUnknownVerb {
		return errfmt.Errorf("tuios on %s is too old for %s. Upgrade tuios on %s", host, verb, host)
	}
	return errfmt.Errorf("tuios on %s: %w", host, err)
}

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

func explainRemoteFailure(host, addr string, remote []string, err error) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return errfmt.Errorf("could not run %s: %w. Set TUIOS_SSH to the ssh program to use", federation.SSHBinary(), err)
	}
	verb := "open"
	if len(remote) > 0 && remote[0] == "new" {
		verb = "create"
	}
	switch exit.ExitCode() {
	case 255:
		return errfmt.Errorf("ssh could not reach %s (%s). Run 'tuios hosts test %s' to see why", host, addr, host)
	case 127:
		return errfmt.Errorf("tuios was not found on %s. Run 'tuios hosts test %s' to see where the link looked", host, host)
	default:
		return errfmt.Errorf("tuios on %s could not %s the session. Its message is above", host, verb)
	}
}

func holdAfter(err error, hold bool) error {
	if !hold || err == nil {
		return err
	}
	fmt.Fprintln(os.Stderr, err.Error())
	fmt.Fprint(os.Stderr, "Press enter to close.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	return heldError{}
}

type heldError struct{}

func (heldError) Error() string   { return "" }
func (heldError) ExitStatus() int { return 1 }

func registerHostNameCompletion(cmd *cobra.Command, flag string) {
	_ = cmd.RegisterFlagCompletionFunc(flag, func(c *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeConfiguredHosts(c, nil, toComplete)
	})
}
