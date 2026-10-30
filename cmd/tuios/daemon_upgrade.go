package main

import (
	"bytes"
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
			data, err := json.Marshal(session.LocalHandoffAccepts(version))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return err
		},
	}
}

// daemonUpgradeCommand is `tuios daemon upgrade`: a live upgrade of the
// running daemon to another binary, with every pane's program still running.
func daemonUpgradeCommand() *cobra.Command {
	var binary string
	var asJSON bool
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

Live upgrades are experimental, macOS and Linux only, and need a daemon from
a build that has them. 'tuios restart-server' moves any daemon to the newest
tuios installed, and stops every program in every pane. Use --server NAME to
upgrade a named daemon.`,
		Example: `  tuios daemon upgrade
  tuios daemon upgrade --binary ~/bin/tuios-next
  tuios --server test daemon upgrade --binary ./tuios`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemonUpgrade(cmd.OutOrStdout(), binary, asJSON)
		},
	}
	cmd.Flags().StringVar(&binary, "binary", "", "The tuios binary to run the daemon as. Defaults to this tuios")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the outcome as one JSON object")
	return cmd
}

// daemonUpgradeWait bounds how long daemon upgrade waits for the new image to
// answer. A handoff of a busy daemon takes under a second.
const daemonUpgradeWait = 60 * time.Second

// upgradeOutcome is what daemon upgrade reports.
type upgradeOutcome struct {
	PID      int                   `json:"pid"`
	From     string                `json:"from"`
	To       string                `json:"to"`
	Binary   string                `json:"binary"`
	Panes    int                   `json:"panes"`
	Clients  int                   `json:"clients"`
	Seconds  float64               `json:"seconds"`
	Fallback bool                  `json:"fallback"`
	Instance string                `json:"instance"`
	Handoff  *session.HandoffStats `json:"handoff,omitempty"`
}

func runDaemonUpgrade(out io.Writer, binary string, asJSON bool) error {
	if runtime.GOOS == "windows" {
		return session.ErrHandoffUnsupported
	}
	say := func(format string, a ...any) {
		if !asJSON {
			fmt.Fprintf(out, format, a...)
		}
	}
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find this tuios: %w", err)
		}
		binary = exe
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		return err
	}

	client, err := dialVerb()
	if err != nil {
		return err
	}
	raw, err := client.Call("hello", map[string]any{"client": "tuios", "version": version, "protocol": session.VerbProtocolVersion})
	if err != nil {
		_ = client.Close()
		return err
	}
	var before struct {
		PID      int    `json:"pid"`
		Instance string `json:"instance"`
		Version  string `json:"daemon_version"`
	}
	_ = json.Unmarshal(raw, &before)

	// The same run the daemon makes, made here first: it says what the
	// binary is, and it pays a new file's first exec before the daemon
	// pauses anything.
	acc, err := handoffAcceptsOf(binary)
	if err != nil {
		_ = client.Close()
		return &diagnosticError{
			What:  fmt.Sprintf("%s cannot take the daemon over: %v.", binary, err),
			Cause: "it is not a tuios, or a tuios from before live upgrades.",
			Fix:   "pass a tuios with --binary. To run another tuios, use '" + serverCommand("restart-server") + "'. It stops every program in every pane, and lists them before it asks.",
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
		say("refused\n")
		var verr *session.VerbCallError
		if errors.As(err, &verr) && verr.Code == session.ErrVerbUnknownVerb {
			return &diagnosticError{
				What:  fmt.Sprintf("The daemon (%s, pid %d) predates live upgrades.", before.Version, before.PID),
				Cause: "it is from a build that cannot hand its panes over.",
				Fix:   "run '" + killServerCommand() + "' and start it again, which ends running programs. The next upgrade can be live.",
			}
		}
		if errors.As(err, &verr) {
			return &diagnosticError{What: verr.Message + ".", Cause: "the daemon refused the upgrade.", Fix: "nothing was changed. Fix what the message names and run 'tuios daemon upgrade' again."}
		}
		return err
	}
	var accepted struct {
		Panes         int    `json:"panes"`
		ManifestBytes int64  `json:"manifest_bytes"`
		Clients       int    `json:"clients"`
		To            string `json:"to"`
		From          string `json:"from"`
	}
	_ = json.Unmarshal(raw, &accepted)
	say("ok\n")
	say("Paused %d %s, saved their screens (%s).\n", accepted.Panes, pluralize(accepted.Panes, "pane", "panes"), humanBytes(accepted.ManifestBytes))
	say("Starting %s in place (pid %d kept)...\n", accepted.To, before.PID)

	after, err := awaitUpgradedDaemon(before.Instance, daemonUpgradeWait)
	if err != nil {
		return err
	}
	o := upgradeOutcome{
		PID: after.PID, From: accepted.From, To: after.Version, Binary: binary,
		Panes: accepted.Panes, Clients: accepted.Clients, Seconds: time.Since(started).Seconds(),
		Instance: after.Instance, Handoff: after.Upgrade,
	}
	if after.Upgrade != nil {
		o.Fallback = after.Upgrade.Fallback
	}
	if asJSON {
		data, _ := json.Marshal(o)
		fmt.Fprintln(out, string(data))
	}
	if o.Fallback {
		return &diagnosticError{
			What:  fmt.Sprintf("%s started and could not take the panes over, so the daemon went back to %s.", binary, o.To),
			Cause: "the new binary failed while it adopted the panes. The daemon log says why.",
			Fix:   "every program is still running. Run 'tuios logs' to see what failed.",
		}
	}
	if after.PID != before.PID {
		return fmt.Errorf("the daemon's pid changed from %d to %d: it was restarted, not upgraded", before.PID, after.PID)
	}
	say("Upgraded in %.2f s. %d %s and their programs kept.", o.Seconds, o.Panes, pluralize(o.Panes, "pane", "panes"))
	if o.Clients > 0 {
		say(" %d attached %s told to attach again.", o.Clients, pluralize(o.Clients, "client was", "clients were"))
	}
	say("\n")
	return nil
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
				if err = json.Unmarshal(raw, &h); err == nil {
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
		return acc, fmt.Errorf("it did not answer handoff-accepts (%w)", err)
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &acc); err != nil {
		return acc, errors.New("it answered handoff-accepts with something that is not its answer")
	}
	if session.HandoffVersion < acc.Handoff[0] || session.HandoffVersion > acc.Handoff[1] {
		return acc, fmt.Errorf("it reads handoff v%d to v%d, and this daemon writes v%d", acc.Handoff[0], acc.Handoff[1], session.HandoffVersion)
	}
	return acc, nil
}

// pluralize is one or many.
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
