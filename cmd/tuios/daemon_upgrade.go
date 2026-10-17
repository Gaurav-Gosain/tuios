package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// killServerCommand is the kill-server command for the daemon being upgraded.
// On a named server it carries --server, since the bare command stops the
// default daemon.
func killServerCommand() string { return serverCommand("kill-server") }

// serverCommand is the tuios command sub, for this command's server.
func serverCommand(sub string) string {
	if name := serverscope.Name(); name != "" {
		return "tuios --server " + name + " " + sub
	}
	return "tuios " + sub
}

// adoptFDFlag is the daemon command's flag naming the descriptor a live
// upgrade's manifest is on.
const adoptFDFlag = "adopt-fd"

// handoffArgs is the command line, after the program, a live upgrade execs
// the next daemon image with: this daemon's own, which carries its server and
// its log level, plus the manifest's descriptor. A flag left by an earlier
// upgrade is dropped, and so is --cpuprofile in every form: the next image
// would truncate the file this one is writing.
func handoffArgs(manifestFD int) []string {
	args := make([]string, 0, len(os.Args))
	skipValue := false
	for _, a := range os.Args[1:] {
		if skipValue {
			skipValue = false
			continue
		}
		if a == "--cpuprofile" {
			skipValue = true
			continue
		}
		if strings.HasPrefix(a, "--cpuprofile=") {
			continue
		}
		if strings.HasPrefix(a, "--"+adoptFDFlag) {
			continue
		}
		args = append(args, a)
	}
	return append(args, "--"+adoptFDFlag+"="+strconv.Itoa(manifestFD))
}

// runAdoptingDaemon runs the daemon image a live upgrade execed: it takes
// over the panes, sockets and lock its predecessor handed down, rather than
// starting fresh. The socket is live, so the running-daemon check a fresh
// start makes would find this very process.
func runAdoptingDaemon(manifestFD int) error {
	return session.NewDaemon(foregroundDaemonConfig()).RunAdopted(manifestFD)
}

// handoffAcceptsCommand is the preflight a live upgrade runs on the binary it
// is about to exec: it prints, as JSON, which handoff manifests this binary
// reads and which protocols its daemon serves, and exits. Running it is also
// the proof that the binary starts at all.
func handoffAcceptsCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "handoff-accepts",
		Short:  "Print the live upgrade versions this binary accepts",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := jsonx.Marshal(session.LocalHandoffAccepts(version))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return err
		},
	}
}

// handoffKeeperCommand is the keeper a live upgrade starts before it stops
// the panes: it holds a copy of the descriptors the exec carries, and if the
// new image dies before it is up, it execs the previous binary in its own
// place to take the panes back. The daemon starts it; nobody else should. See
// internal/session/handoff_keeper.go.
func handoffKeeperCommand() *cobra.Command {
	var hold string
	cmd := &cobra.Command{
		Use:    "handoff-keeper --hold FD,... -- BINARY ARGS...",
		Short:  "Hold a live upgrade's descriptors until the new daemon image is up",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fds, err := session.ParseKeeperHold(hold)
			if err != nil {
				return err
			}
			return session.RunHandoffKeeper(fds, args)
		},
	}
	cmd.Flags().StringVar(&hold, "hold", "", "The descriptor numbers the manifest names, in the order this process holds them from descriptor 4")
	return cmd
}

// daemonUpgradeCommand is `tuios daemon upgrade`: a live upgrade of the
// running daemon to another binary, with every pane's program still running.
func daemonUpgradeCommand() *cobra.Command {
	var binary string
	var asJSON, orRestart bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the running daemon to a new binary without ending any pane's program",
		Long: `Upgrade the running daemon to a new tuios binary in place.

Every pane's program keeps running, with the same process, the same terminal
and the same screen, and the daemon keeps its pid. Attached clients are told
the daemon is being upgraded and exit; attach again to continue.

The new binary is run once first and must say it can take this daemon's
state. A refusal changes nothing. If the new binary starts and cannot take
the panes over, the daemon goes back to the binary it was running, with every
pane, and the command says so.

Run from inside a pane, the upgrade waits for you to confirm it: every
attached client shows a dialog with the binary's full path, its version and
the pane that asked. Only a key you press at a client answers it, and No is
the default. No, Esc, a minute without an answer, or no attached client
refuses it. A plugin is always refused.

--or-restart is for an installer or a script that wants the daemon on the new
binary in any case. When the daemon cannot be upgraded live, because it
predates live upgrades, it refuses (a pane on another machine, a running fan
check), or the binary cannot take its state, the command says why and
restarts the daemon with the binary instead, which ends running programs.
With no daemon running, it starts one. A daemon that went back to its old
binary is not restarted: its programs are still running, and the command
fails so the reason can be read in 'tuios logs'.

Live upgrades are experimental, macOS and Linux only, and need a daemon from
a build that has them. 'tuios restart-server' moves any daemon to the newest
tuios installed, and stops every program in every pane. Use --server NAME to
upgrade a named daemon.`,
		Example: `  tuios daemon upgrade
  tuios daemon upgrade --binary ~/bin/tuios-next
  tuios --server test daemon upgrade --binary ./tuios
  tuios daemon upgrade --binary ~/.local/bin/tuios --or-restart`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonUpgrade(cmd.OutOrStdout(), binary, asJSON, orRestart)
		},
	}
	cmd.Flags().StringVar(&binary, "binary", "", "The tuios binary to run the daemon as. Defaults to this tuios")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the outcome as one JSON object")
	cmd.Flags().BoolVar(&orRestart, "or-restart", false, "When the daemon cannot be upgraded live, restart it with the binary instead, which ends running programs. Start one when none runs")
	return cmd
}

// daemonUpgradeWait bounds how long daemon upgrade waits for the new image to
// answer. A handoff of a busy daemon takes under a second.
const daemonUpgradeWait = 60 * time.Second

// What daemon upgrade did, as its JSON names it.
const (
	upgradeActionUpgraded  = "upgraded"
	upgradeActionRestarted = "restarted"
	upgradeActionStarted   = "started"
	upgradeActionFallback  = "fallback"
	upgradeActionRefused   = "refused"
	upgradeActionFailed    = "failed"
)

// upgradeOutcome is what daemon upgrade reports.
type upgradeOutcome struct {
	// Action is one of the upgradeAction names: upgraded live, restarted or
	// started by --or-restart, fallback to the old binary, refused with
	// nothing changed, or failed.
	Action   string  `json:"action"`
	PID      int     `json:"pid"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Binary   string  `json:"binary"`
	Panes    int     `json:"panes"`
	Clients  int     `json:"clients"`
	Seconds  float64 `json:"seconds"`
	Fallback bool    `json:"fallback"`
	// Recovered is a new image that died before it was up, after which the
	// keeper started the previous binary under a pid of its own.
	Recovered bool                  `json:"recovered"`
	Instance  string                `json:"instance"`
	Handoff   *session.HandoffStats `json:"handoff,omitempty"`
	// Reason is why the daemon was not upgraded live.
	Reason string `json:"reason,omitempty"`
	// Error is the failure, when the command failed.
	Error string `json:"error,omitempty"`
}

// notLiveError is a live upgrade that never started: nothing was changed, and
// a running daemon runs as it did. --or-restart restarts the daemon instead.
type notLiveError struct {
	// reason is one line for the report.
	reason string
	// running says a daemon runs, so a restart has one to stop.
	running bool
	// from is the version of the running daemon, when it is known.
	from string
	err  error
}

func (e *notLiveError) Error() string { return e.err.Error() }
func (e *notLiveError) Unwrap() error { return e.err }

func runDaemonUpgrade(out io.Writer, binary string, asJSON, orRestart bool) error {
	say := func(format string, a ...any) {
		if !asJSON {
			fmt.Fprintf(out, format, a...)
		}
	}
	o, err := upgradeDaemon(say, binary, orRestart)
	if asJSON {
		if err != nil && o.Error == "" {
			o.Error = err.Error()
		}
		data, _ := jsonx.Marshal(o)
		fmt.Fprintln(out, string(data))
	}
	return err
}

// upgradeDaemon upgrades the daemon live, and with orRestart restarts it when
// a live upgrade cannot start. Every path that installs a tuios and moves the
// daemon onto it comes through here: the installers and hosts sync run this
// command with --or-restart.
func upgradeDaemon(say func(string, ...any), binary string, orRestart bool) (upgradeOutcome, error) {
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return upgradeOutcome{Action: upgradeActionFailed}, errfmt.Errorf("find this tuios: %w", err)
		}
		binary = exe
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		return upgradeOutcome{Action: upgradeActionFailed}, err
	}
	o, err := upgradeLive(say, binary)
	notLive, ok := errors.AsType[*notLiveError](err)
	if !ok {
		return o, err
	}
	if !orRestart {
		return upgradeOutcome{Action: upgradeActionRefused, Binary: binary, From: notLive.from, Reason: notLive.reason}, err
	}
	say("Not upgraded live: %s.\n", notLive.reason)
	return restartDaemonWith(say, binary, notLive)
}

// upgradeLive is the live upgrade itself. A *notLiveError means nothing was
// changed.
func upgradeLive(say func(string, ...any), binary string) (upgradeOutcome, error) {
	o := upgradeOutcome{Action: upgradeActionFailed, Binary: binary}
	// The daemon's owner and mode check, made before anything runs the file.
	// Its refusal is no notLiveError: --or-restart would run the same file
	// for the restart, or the start where no daemon runs.
	if err := session.CheckUpgradeBinary(binary); err != nil {
		o.Action, o.Reason = upgradeActionRefused, err.Error()
		return o, &diagnosticError{
			What:  err.Error() + ".",
			Cause: "another user could have put it there, so the daemon would refuse it too.",
			Fix:   "run this command again once the file is fixed. Nothing was changed.",
		}
	}
	if runtime.GOOS == "windows" {
		return o, &notLiveError{reason: "live upgrades are not supported on Windows", running: session.IsDaemonRunning(), err: session.ErrHandoffUnsupported}
	}

	client, err := dialVerb()
	if err != nil {
		if mismatch, ok := errors.AsType[*session.ProtocolMismatchError](err); ok {
			return o, &notLiveError{
				reason:  fmt.Sprintf("the daemon (%s, pid %d) speaks a control protocol this tuios does not", cmp.Or(mismatch.DaemonVersion, "unknown version"), mismatch.DaemonPID),
				running: true, from: mismatch.DaemonVersion, err: err,
			}
		}
		if !session.IsDaemonRunning() {
			return o, &notLiveError{reason: "no daemon is running", err: err}
		}
		return o, err
	}
	raw, err := client.Call("hello", map[string]any{"client": "tuios", "version": version, "protocol": session.VerbProtocolVersion})
	if err != nil {
		_ = client.Close()
		return o, err
	}
	var before struct {
		PID      int    `json:"pid"`
		Instance string `json:"instance"`
		Version  string `json:"daemon_version"`
	}
	_ = jsonx.Unmarshal(raw, &before)
	o.From = before.Version

	// The same run the daemon makes, made here first: it says what the
	// binary is, and it pays a new file's first exec before the daemon
	// pauses anything.
	acc, err := handoffAcceptsOf(binary)
	if err != nil {
		_ = client.Close()
		return o, &notLiveError{
			reason: fmt.Sprintf("%s cannot take the daemon over: %v", binary, err), running: true, from: before.Version,
			err: &diagnosticError{
				What:  fmt.Sprintf("%s cannot take the daemon over: %v.", binary, err),
				Cause: "it is not a tuios, or a tuios from before live upgrades.",
				Fix:   "pass a tuios with --binary. To run another tuios, use '" + serverCommand("restart-server") + "'. It stops every program in every pane, and lists them before it asks.",
			},
		}
	}
	if os.Getenv("TUIOS_PANE_ID") != "" {
		say("Asking you to confirm the upgrade in the tuios dialog...\n")
	}
	say("Checking %s (%s) accepts this daemon's state (handoff v%d)... ", binary, acc.Version, session.HandoffVersion)

	started := time.Now()
	// From a pane the daemon waits for the person's answer before it starts.
	raw, err = client.CallWithTimeout("daemon-upgrade", map[string]any{"binary": binary}, session.UpgradeConfirmWait+daemonUpgradeWait)
	_ = client.Close()
	if err != nil {
		var verr *session.VerbCallError
		// A pane's upgrade that nobody said Yes to is refused as forbidden,
		// and the binary was never what said no.
		if errors.As(err, &verr) && verr.Code == session.ErrVerbForbidden {
			say("not confirmed\n")
		} else {
			say("refused\n")
		}
		if errors.As(err, &verr) && verr.Code == session.ErrVerbUnknownVerb {
			return o, &notLiveError{
				reason: fmt.Sprintf("the daemon (%s, pid %d) predates live upgrades", before.Version, before.PID), running: true, from: before.Version,
				err: &diagnosticError{
					What:  fmt.Sprintf("The daemon (%s, pid %d) predates live upgrades.", before.Version, before.PID),
					Cause: "it is from a build that cannot hand its panes over.",
					Fix:   "run '" + serverCommand("restart-server") + "', which stops every program in every pane, and lists them before it asks. The next upgrade can be live.",
				},
			}
		}
		if errors.As(err, &verr) {
			return o, &notLiveError{
				reason: "the daemon refused: " + verr.Message, running: true, from: before.Version,
				err: &diagnosticError{What: strings.TrimSuffix(verr.Message, ".") + ".", Cause: "the daemon refused the upgrade.", Fix: "nothing was changed. Fix what the message names and run 'tuios daemon upgrade' again."},
			}
		}
		return o, err
	}
	var accepted struct {
		Panes         int    `json:"panes"`
		ManifestBytes int64  `json:"manifest_bytes"`
		Clients       int    `json:"clients"`
		To            string `json:"to"`
		From          string `json:"from"`
	}
	_ = jsonx.Unmarshal(raw, &accepted)
	say("ok\n")
	say("Paused %d %s, saved their screens (%s).\n", accepted.Panes, plural.Word(accepted.Panes, "pane", "panes"), humanBytes(accepted.ManifestBytes))
	say("Starting %s in place (pid %d kept)...\n", accepted.To, before.PID)

	after, err := awaitUpgradedDaemon(before.Instance, daemonUpgradeWait)
	if err != nil {
		return o, err
	}
	o = upgradeOutcome{
		Action: upgradeActionUpgraded,
		PID:    after.PID, From: accepted.From, To: after.Version, Binary: binary,
		Panes: accepted.Panes, Clients: accepted.Clients, Seconds: time.Since(started).Seconds(),
		Instance: after.Instance, Handoff: after.Upgrade,
	}
	if after.Upgrade != nil {
		o.Fallback, o.Recovered = after.Upgrade.Fallback, after.Upgrade.Recovered
	}
	if o.Recovered {
		o.Action = upgradeActionFallback
		return o, &diagnosticError{
			What:  fmt.Sprintf("%s died before it was up, so the daemon was started again from %s, as pid %d (was %d).", binary, o.To, after.PID, before.PID),
			Cause: "the new binary crashed or was killed while it took the panes over. The keeper the upgrade left holding them brought the previous binary back. The daemon log says what it saw.",
			Fix:   "every program is still running, with the same pid. The daemon is no longer their parent: on Linux the exit status of a program that ends from now on is not known. Run 'tuios logs' to see what failed.",
		}
	}
	if o.Fallback {
		o.Action = upgradeActionFallback
		return o, &diagnosticError{
			What:  fmt.Sprintf("%s started and could not take the panes over, so the daemon went back to %s.", binary, o.To),
			Cause: "the new binary failed while it adopted the panes. The daemon log says why.",
			Fix:   "every program is still running. Run 'tuios logs' to see what failed.",
		}
	}
	if after.PID != before.PID {
		o.Action = upgradeActionFailed
		return o, errfmt.Errorf("the daemon's pid changed from %d to %d: it was restarted, not upgraded", before.PID, after.PID)
	}
	say("Upgraded in %.2f s. %d %s and their programs kept.", o.Seconds, o.Panes, plural.Word(o.Panes, "pane", "panes"))
	if o.Clients > 0 {
		say(" %d attached %s told to attach again.", o.Clients, plural.Word(o.Clients, "client was", "clients were"))
	}
	say("\n")
	return o, nil
}

// restartBudget bounds each step of a restart: kill-server waits up to 10 s
// for the daemon to save its sessions, and start-server for the new one.
const restartBudget = 60 * time.Second

// restartDaemonWith puts the daemon on binary the way a restart does: it stops
// the daemon, which saves every session, and starts binary's daemon, which
// restores them with their layout and new shells. The programs in the panes
// end. Both steps run as binary and inherit this process's server, so the
// daemon that comes back is binary's and serves the same socket.
func restartDaemonWith(say func(string, ...any), binary string, why *notLiveError) (upgradeOutcome, error) {
	o := upgradeOutcome{Action: upgradeActionFailed, Binary: binary, From: why.from, Reason: why.reason}
	// A binary that does not run would stop the daemon and start nothing.
	to, err := runBinary(binary, "--version")
	if err != nil {
		return o, &diagnosticError{
			What:  fmt.Sprintf("%s does not run, so the daemon was neither upgraded nor restarted.", binary),
			Cause: plainLine(err.Error()),
			Fix:   "pass a tuios with --binary. Nothing was changed.",
		}
	}
	o.To, _ = hostsync.ParseVersionLine(firstLine(to))
	if why.running {
		say("Restarting the daemon with %s (%s), which ends the programs in its panes...\n", binary, cmp.Or(o.To, "unknown version"))
		if out, err := runBinary(binary, "kill-server"); err != nil {
			return o, &diagnosticError{
				What:  "The daemon did not stop, so it still runs the old build.",
				Cause: plainLine(hostsync.LastLine(out)),
				Fix:   "run '" + killServerCommand() + "' to see why.",
			}
		}
	} else {
		say("Starting the daemon with %s (%s)...\n", binary, cmp.Or(o.To, "unknown version"))
	}
	if out, err := runBinary(binary, "start-server"); err != nil {
		what := "The daemon did not start."
		if why.running {
			what = "The old daemon stopped and the new one did not start."
		}
		return o, &diagnosticError{
			What:  what,
			Cause: plainLine(hostsync.LastLine(out)),
			Fix:   "run '" + binary + " start-server' to see why. The sessions come back when it starts.",
		}
	}
	o.Action = upgradeActionRestarted
	if !why.running {
		o.Action = upgradeActionStarted
	}
	if client, err := dialVerb(); err == nil {
		raw, err := client.Call("hello", map[string]any{"client": "tuios", "version": version, "protocol": session.VerbProtocolVersion})
		_ = client.Close()
		var h upgradedDaemon
		if err == nil && jsonx.Unmarshal(raw, &h) == nil {
			o.PID, o.Instance, o.To = h.PID, h.Instance, cmp.Or(h.Version, o.To)
		}
	}
	if why.running {
		say("Restarted. The daemon runs %s (pid %d). Its sessions come back with their layout and new shells.\n", cmp.Or(o.To, "unknown version"), o.PID)
	} else {
		say("Started. The daemon runs %s (pid %d).\n", cmp.Or(o.To, "unknown version"), o.PID)
	}
	return o, nil
}

// runBinary runs one tuios command as binary and returns what it printed.
func runBinary(binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), restartBudget)
	defer cancel()
	//tuios:allow-exec the tuios a restart moves the daemon onto, run as its own kill-server and start-server
	cmd := exec.CommandContext(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// upgradedDaemon is a hello answer after an upgrade.
type upgradedDaemon struct {
	PID      int                   `json:"pid"`
	Instance string                `json:"instance"`
	Version  string                `json:"daemon_version"`
	Upgrade  *session.HandoffStats `json:"upgrade"`
}

// awaitUpgradedDaemon waits for a hello from an image other than old. Between
// the old image dropping its clients and the new one serving, a call waits in
// the socket's backlog. An answer from the old image means the exec failed and
// the daemon carried on.
func awaitUpgradedDaemon(old string, wait time.Duration) (upgradedDaemon, error) {
	deadline := time.Now().Add(wait)
	var last error
	for time.Now().Before(deadline) {
		client, err := session.DialVerbClientAs(version)
		if err == nil {
			var raw json.RawMessage
			raw, err = client.CallWithTimeout("hello", map[string]any{"client": "tuios", "version": version, "protocol": session.VerbProtocolVersion}, time.Until(deadline))
			_ = client.Close()
			if err == nil {
				var h upgradedDaemon
				if err = jsonx.Unmarshal(raw, &h); err == nil {
					if h.Instance != old {
						return h, nil
					}
					return h, &diagnosticError{
						What:  "The daemon could not exec the new binary and carried on as it was.",
						Cause: "the exec failed after the clients were dropped. The daemon log says why.",
						Fix:   "every program is still running. Attach again, and run 'tuios logs' to see what failed.",
					}
				}
			}
		}
		last = err
		time.Sleep(50 * time.Millisecond)
	}
	return upgradedDaemon{}, &diagnosticError{
		What:  fmt.Sprintf("The daemon did not answer within %s of the upgrade: %v.", wait, last),
		Cause: "the new image is still adopting the panes, or it and the fallback both failed.",
		Fix:   "run 'tuios ls' to check. If the daemon is gone, 'tuios start-server' restores the sessions, and the programs in them have ended.",
	}
}

// handoffAcceptsOf runs binary's handoff-accepts.
func handoffAcceptsOf(binary string) (session.HandoffAccepts, error) {
	var acc session.HandoffAccepts
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	//tuios:allow-exec the binary a live upgrade is about to exec, run once to ask what it takes
	cmd := exec.CommandContext(ctx, binary, "daemon", "handoff-accepts")
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return acc, errfmt.Errorf("it did not answer handoff-accepts (%w)", err)
	}
	if err := jsonx.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &acc); err != nil {
		return acc, errors.New("it answered handoff-accepts with something that is not its answer")
	}
	if session.HandoffVersion < acc.Handoff[0] || session.HandoffVersion > acc.Handoff[1] {
		return acc, errfmt.Errorf("it reads handoff v%d to v%d, and this daemon writes v%d", acc.Handoff[0], acc.Handoff[1], session.HandoffVersion)
	}
	return acc, nil
}

// humanBytes is n bytes for a person.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 1, 64) + " KB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}
