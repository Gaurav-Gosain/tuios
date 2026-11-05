package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/service"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// The daemon as a service of the user: tuios daemon install-service,
// uninstall-service and service-status. internal/service writes the unit,
// the launchd agent or the task, and says why each line is there.

// serviceOffEnv set to 1 makes a client start the daemon directly even when
// a service is installed.
const serviceOffEnv = "TUIOS_NO_SERVICE"

// serviceName is the service of this command's server.
func serviceName() string { return service.Name(serverscope.Name()) }

// serviceSpec is the service install-service writes for this machine.
func serviceSpec(binary string) (service.Spec, error) {
	if binary == "" {
		b, err := stableTuiosPath()
		if err != nil {
			return service.Spec{}, errfmt.Errorf("find the tuios binary: %w", err)
		}
		binary = b
	}
	if !filepath.IsAbs(binary) {
		return service.Spec{}, errfmt.Errorf("--binary must be an absolute path, not %q", binary)
	}
	if _, err := os.Stat(binary); err != nil {
		return service.Spec{}, errfmt.Errorf("--binary: %w", err)
	}
	socket, err := session.GetSocketPath()
	if err != nil {
		return service.Spec{}, err
	}
	manager, err := service.ManagerEnv()
	if err != nil {
		return service.Spec{}, err
	}
	home, _ := os.UserHomeDir()
	spec := service.Spec{
		Name:   serviceName(),
		Binary: binary,
		Shell:  os.Getenv("SHELL"),
		Socket: socket,
		Dir:    home,
		Env:    service.CarriedEnv(os.Getenv, manager),
	}
	if runtime.GOOS == "darwin" && isDaemonApp(defaultDaemonAppPath()) {
		spec.App = defaultDaemonAppPath()
	}
	return spec, nil
}

// unsupportedServiceError explains a machine with no service manager.
func unsupportedServiceError(err error) error {
	return &diagnosticError{
		What:  "tuios cannot install the daemon as a service on this machine.",
		Cause: err.Error() + ".",
		Fix:   "start the daemon from your own init system with the command 'tuios daemon --service'.",
		Err:   err,
	}
}

func newDaemonInstallServiceCommand() *cobra.Command {
	var dryRun bool
	var binary string
	cmd := &cobra.Command{
		Use:   "install-service",
		Short: "Start the daemon when you log in, or at boot on Linux",
		Long: `Install the daemon as a service of your user: a systemd user unit on
Linux, a launchd agent on macOS, a Task Scheduler task on Windows. A crash
starts the daemon again. A daemon that runs now continues, and moves under
the service when it starts again. See docs/SESSIONS.md.`,
		Example: `  tuios daemon install-service
  tuios daemon install-service --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInstallService(cmd.OutOrStdout(), binary, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show the service file and change nothing")
	cmd.Flags().StringVar(&binary, "binary", "", "The tuios the service runs (default the tuios on PATH)")
	return cmd
}

func runInstallService(out io.Writer, binary string, dryRun bool) error {
	if err := session.CheckSocketEnv(); err != nil {
		return err
	}
	spec, err := serviceSpec(binary)
	if errors.Is(err, service.ErrUnsupported) {
		return unsupportedServiceError(err)
	}
	if err != nil {
		return err
	}
	path, text, err := service.Render(spec)
	if errors.Is(err, service.ErrUnsupported) {
		return unsupportedServiceError(err)
	}
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(out, "# %s\n%s", path, text)
		return nil
	}
	running := session.IsDaemonRunning()
	if err := service.Install(spec, !running); err != nil {
		return &diagnosticError{
			What: "The service could not be installed: " + err.Error() + ".",
			Fix:  "run 'tuios daemon install-service --dry-run' to see the file, and check the service manager.",
			Err:  err,
		}
	}
	fmt.Fprintf(out, "Installed the service %s (%s). It runs %s.\n", spec.Name, path, spec.Binary)
	if running {
		fmt.Fprintf(out, "A daemon runs now outside the service. It moves under the service when it starts again. To move it now, run: %s. This stops the programs in its panes.\n", serverCommand("restart-server"))
	} else {
		fmt.Fprintln(out, "The service started the daemon.")
	}
	writeLingerAdvice(out, service.Get(spec.Name))
	return nil
}

// writeLingerAdvice says what to do when linger is off.
func writeLingerAdvice(out io.Writer, st service.Status) {
	if !service.UsesLinger() || st.Linger {
		return
	}
	fmt.Fprintf(out, "Linger is off. The daemon stops when you log out, and does not start at boot. To turn linger on, run: loginctl enable-linger %[1]s. If that fails, run: sudo loginctl enable-linger %[1]s\n", st.User)
}

func newDaemonUninstallServiceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall-service",
		Short: "Remove the daemon service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			name := serviceName()
			if !service.Get(name).Installed {
				fmt.Fprintf(out, "The service %s is not installed.\n", name)
				return nil
			}
			if err := service.Uninstall(name); err != nil {
				return errfmt.Errorf("remove the service %s: %w", name, err)
			}
			fmt.Fprintf(out, "Removed the service %s.\n", name)
			fmt.Fprintln(out, "A daemon that runs now continues. New daemons start without the service.")
			return nil
		},
	}
}

// serviceReport is service-status --json.
type serviceReport struct {
	service.Status
	// DaemonPID is the daemon that serves the socket now, 0 when none runs.
	DaemonPID int `json:"daemon_pid,omitzero"`
	// UnderService says the daemon that runs is the service's.
	UnderService bool `json:"under_service"`
}

func newDaemonServiceStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "service-status",
		Short: "Show the daemon service and whether the daemon runs under it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep := serviceReport{Status: service.Get(serviceName())}
			if session.IsDaemonRunning() {
				rep.DaemonPID = session.GetDaemonPID()
			}
			rep.UnderService = rep.DaemonPID != 0 && rep.DaemonPID == rep.PID
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			writeServiceStatus(out, rep)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the status as JSON")
	return cmd
}

func writeServiceStatus(out io.Writer, rep serviceReport) {
	st := rep.Status
	switch {
	case !st.Supported:
		fmt.Fprintln(out, "This machine has no service manager that tuios knows.")
	case !st.Installed:
		fmt.Fprintf(out, "The service %s is not installed. To install it, run: %s\n", st.Name, serverCommand("daemon install-service"))
	default:
		fmt.Fprintf(out, "Service: %s, enabled %t, state %s\nFile:    %s\nRuns:    %s\n", st.Name, st.Enabled, st.Detail, st.File, st.Binary)
	}
	where := "not running"
	if rep.DaemonPID != 0 {
		where = fmt.Sprintf("PID %d, under the service %t", rep.DaemonPID, rep.UnderService)
	}
	fmt.Fprintf(out, "Daemon:  %s\n", where)
	if st.Installed {
		writeLingerAdvice(out, st)
	}
}

// startDaemonThroughService starts the daemon through the installed service
// and reports whether it did. It does nothing when no enabled service serves
// this socket, when the service runs another tuios than this one, or when
// TUIOS_NO_SERVICE=1. A service that does not bring a daemon up in time
// leaves the start to the caller.
func startDaemonThroughService() bool {
	if os.Getenv(serviceOffEnv) == "1" {
		return false
	}
	socket, err := session.GetSocketPath()
	if err != nil {
		return false
	}
	name := serviceName()
	if !service.Serves(name, socket) {
		return false
	}
	if st := service.Get(name); st.Binary != "" && !sameFile(st.Binary, mustExecutable()) {
		fmt.Fprintf(os.Stderr, "The service %s runs %s, not this tuios. tuios starts the daemon without the service. To change the service, run: tuios daemon install-service\n", name, st.Binary)
		return false
	}
	if err := service.Start(name); err != nil {
		fmt.Fprintf(os.Stderr, "The service %s did not start: %v. tuios starts the daemon without it.\n", name, err)
		return false
	}
	return waitForDaemon(daemonStartTimeout)
}

// mustExecutable is this binary's path, or "" when it is not known.
func mustExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// sameFile reports whether two paths, links followed, name one file.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// runServiceDaemon is the daemon a service manager started. A daemon that
// already serves the socket is not an error here: the service exits 0, so
// the manager does not start it again and again. A client or a live upgrade
// keeper may have started that daemon.
func runServiceDaemon(noRestore bool) error {
	if session.IsDaemonRunning() {
		fmt.Fprintf(os.Stderr, "A daemon already runs (PID %d). The service does not start another one.\n", session.GetDaemonPID())
		return nil
	}
	err := runDaemon(true, noRestore)
	if err != nil && session.IsDaemonRunning() {
		fmt.Fprintf(os.Stderr, "Another daemon started first (PID %d). The service does not start another one.\n", session.GetDaemonPID())
		return nil
	}
	return err
}
