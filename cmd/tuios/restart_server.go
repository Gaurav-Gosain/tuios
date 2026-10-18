package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/restartplan"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// `tuios restart-server`: stop the daemon and start it again with the newest
// tuios installed.
//
// It is the one command for "the daemon runs an old build". Before it, the
// answer was to detach, run kill-server, and start tuios again, and nothing
// said what that stopped. This command says it first: the panes that run a
// program, by session, and the agents that can resume. It asks, and then it
// does the three steps and checks each one.
//
// The new daemon runs the newest tuios installed: the binary at the path
// this tuios was started from, or the tuios on PATH, whichever is newer. See
// release.NewestInstalled.
//
// With --json the command writes one JSON object per line, one for each step,
// so the app can show the progress while it waits. The app runs this command,
// so the plan it shows and the restart it runs cannot differ.

// restartTimeout bounds the stop and the start, each.
const restartTimeout = 30 * time.Second

// restartReport is one line of --json. See internal/restartplan.
type restartReport = restartplan.Report

// restartOptions are the flags.
type restartOptions struct {
	yes    bool
	dryRun bool
	json   bool
	last   bool
	// expect is the digest of the plan the caller agreed to. See
	// restartplan.Digest.
	expect string
	// worker is the plan a detached worker runs. See runRestartWorker.
	worker string
	out    io.Writer
	in     io.Reader
	tty    *bool
}

func newRestartServerCommand() *cobra.Command {
	var opts restartOptions
	cmd := &cobra.Command{
		Use:   "restart-server",
		Short: "Restart the TUIOS daemon with the newest tuios installed",
		Long: `Stop the TUIOS daemon and start it again with the newest tuios installed.

Use it after an update: the daemon runs the build it started with until it
stops. The new daemon runs the newer of two binaries: the tuios at the path of
this command, which an update replaces, and the tuios on PATH, which an
install in the Nix store, mise, asdf or Linuxbrew moves.

A restart stops every program in every pane. Before it asks, the command
lists the panes that run a program, for each session, and the agents that can
resume. Each session comes back with its layout, its pane history and new
shells. The daemon offers to resume each agent conversation, as
daemon.resume_agents says.

The stop and the start run in a process of their own, so they finish even
when this command runs in a pane of the daemon it stops. Each step goes to
the restart log. --last shows the log of the last restart.

The command waits until the new daemon answers, and then says which sessions
came back. The app runs the same command from the command palette ("Restart
the daemon"), from the version notice, and from the Daemon tab of the settings.`,
		Example: `  # See what a restart stops, and change nothing
  tuios restart-server --dry-run

  # Restart without a question
  tuios restart-server --yes

  # Show what the last restart did
  tuios restart-server --last`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			opts.out, opts.in = os.Stdout, os.Stdin
			switch {
			case opts.worker != "":
				return runRestartWorker(opts.worker)
			case opts.last:
				return showLastRestart(opts.out)
			}
			return runRestartServer(opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Restart without a question")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Show what a restart stops, and change nothing")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Write one JSON object per line for each step")
	cmd.Flags().BoolVar(&opts.last, "last", false, "Show the steps of the last restart")
	cmd.Flags().StringVar(&opts.expect, "expect", "", "Refuse unless the plan has this digest, the one the app showed")
	cmd.Flags().StringVar(&opts.worker, "worker", "", "Run the restart of this plan, detached")
	_ = cmd.Flags().MarkHidden("worker")
	return cmd
}

func runRestartServer(opts restartOptions) error {
	if err := session.CheckSocketEnv(); err != nil {
		return err
	}
	rep := planRestart()
	if opts.json {
		writeRestartJSON(opts.out, rep)
	} else {
		writeRestartPlan(opts.out, rep)
	}
	if rep.Error != "" {
		return restartFailure(rep, opts)
	}
	if opts.dryRun {
		return nil
	}
	// The app passes the plan the person agreed to. A program that started
	// since, or a session that went, makes it another plan, and the person
	// has to see that one first.
	if opts.expect != "" && opts.expect != restartplan.Digest(rep) {
		changed := &restartReport{Step: restartplan.StepDone, Error: "What a restart stops changed after you agreed to it.",
			Fix: "read the new list, then agree again. Nothing was changed."}
		if opts.json {
			writeRestartJSON(opts.out, changed)
		}
		return restartFailure(changed, opts)
	}
	// Every state but a stopped daemon loses something, or may: a daemon
	// that did not answer is asked about too.
	if !opts.yes && rep.Daemon.State != daemonStopped {
		if opts.json {
			return restartFailure(&restartReport{Step: restartplan.StepDone,
				Error: "A restart needs --yes with --json.", Fix: "add --yes. Nothing was changed."}, opts)
		}
		if err := confirmRestart(opts); err != nil {
			return err
		}
	}
	done := runDetachedRestart(rep, func(step string) {
		if opts.json {
			writeRestartJSON(opts.out, &restartReport{Step: step})
			return
		}
		switch step {
		case restartplan.StepStopping:
			fmt.Fprintln(opts.out, "Stopping the daemon...")
		case restartplan.StepStarting:
			fmt.Fprintf(opts.out, "Starting the daemon %s...\n", cmp.Or(rep.Version, "with the new tuios"))
		}
	})
	if opts.json {
		writeRestartJSON(opts.out, done)
	}
	if done.Error != "" {
		return restartFailure(done, opts)
	}
	if !opts.json {
		writeRestartDone(opts.out, done)
	}
	return nil
}

// restartFailure is the error the command exits with. With --json the line
// already carries it, so the error is one plain line.
func restartFailure(rep *restartReport, opts restartOptions) error {
	if opts.json {
		return errors.New(rep.Error)
	}
	return &diagnosticError{What: rep.Error, Fix: rep.Fix}
}

// planRestart reads the daemon and the binary the new daemon runs.
func planRestart() *restartReport {
	rep := &restartReport{Step: restartplan.StepPlan}
	bin, ver, err := release.NewestInstalled()
	if err != nil {
		rep.Error = "tuios could not find its own binary: " + err.Error() + "."
		rep.Fix = "install tuios again, or put it on PATH."
		return rep
	}
	rep.Binary = bin
	rep.Version = cmp.Or(ver, version)

	t := &syncTarget{name: "this machine", local: true, runner: localSyncRunner{}}
	ctx, cancel := context.WithTimeout(context.Background(), syncProbeTimeout)
	defer cancel()
	readLocalDaemon(ctx, t)
	d := t.res.Daemon
	rep.Daemon = &restartplan.Daemon{State: d.State, Version: d.Version, PID: d.PID, SessionCount: d.SessionCount, Sessions: d.Sessions, Unread: d.Unread}

	cfg := session.DaemonConfigFromUser(nil)
	if uc, err := config.LoadUserConfig(); err == nil {
		cfg = session.DaemonConfigFromUser(uc)
	}
	if !cfg.AgentsOff {
		rep.ResumeMode = resumeModeName(cfg.ResumeAgents)
	} else {
		for i := range rep.Daemon.Sessions {
			rep.Daemon.Sessions[i].Resume = nil
		}
	}
	return rep
}

// resumeModeName is daemon.resume_agents the way the daemon reads it.
func resumeModeName(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case config.ResumeAgentsOff:
		return config.ResumeAgentsOff
	case config.ResumeAgentsAuto:
		return config.ResumeAgentsAuto
	}
	return config.ResumeAgentsAsk
}

// resumableAgents lists the agent panes of one session whose conversation the
// new daemon can offer to resume. live maps each pane that has an agent now to
// its name. The conversation id and its harness are read from the session's
// saved file, which is where the new daemon reads them from.
func resumableAgents(sessionName string, live map[string]string) []syncAgent {
	if len(live) == 0 {
		return nil
	}
	st, err := session.LoadResurrectionState(sessionName)
	if err != nil || st == nil {
		return nil
	}
	reg, _ := harness.Load(harness.UserDir())
	var out []syncAgent
	for _, w := range st.Windows {
		name, ok := live[w.ID]
		if !ok || w.AgentSessionID == "" {
			continue
		}
		h := cmp.Or(w.AgentSessionHarness, w.AgentHarness)
		if _, err := reg.ResumeArgv(h, w.AgentSessionID); err != nil {
			continue
		}
		out = append(out, syncAgent{Window: name, Harness: h})
	}
	return out
}

// runRestart stops the daemon and starts it with rep.Binary. step is called
// as each step begins.
//
// Both steps run the new binary as a command. kill-server stops a daemon of
// any version, since it signals the process the pid file names, and it
// returns only once the sessions are saved. start-server returns once the
// new daemon takes the socket, and the new daemon restores every saved
// session before it does.
func runRestart(plan *restartReport, step func(string)) *restartReport {
	rep := &restartReport{Step: restartplan.StepDone, Binary: plan.Binary, Version: plan.Version}
	var before []string
	if plan.Daemon != nil {
		for _, s := range plan.Daemon.Sessions {
			before = append(before, s.Name)
		}
	}
	if plan.Daemon != nil && plan.Daemon.State != daemonStopped {
		step(restartplan.StepStopping)
		// --restart tells the attached clients to wait for the new daemon. A
		// binary older than the flag stops the daemon without it.
		out, err := runTuios(plan.Binary, "kill-server", "--restart")
		if err != nil && strings.Contains(out, "unknown flag") {
			out, err = runTuios(plan.Binary, "kill-server")
		}
		if err != nil {
			rep.Error = fmt.Sprintf("The daemon did not stop. It still runs version %s.", cmp.Or(plan.Daemon.Version, "unknown"))
			rep.Fix = "run 'tuios kill-server' to see why." + outputTail(out)
			return rep
		}
	}
	step(restartplan.StepStarting)
	// A daemon that some other start brought up in the meantime is as good
	// as this start's own: what matters is that one answers.
	if out, err := runTuios(plan.Binary, "start-server"); err != nil && !session.IsDaemonRunning() {
		rep.Error = "The daemon did not start."
		rep.Fix = "run 'tuios start-server' to see why. The sessions come back when the daemon starts." + outputTail(out)
		return rep
	}
	after, live, err := readRestartedDaemon()
	if err != nil {
		rep.Error = "The daemon started and did not answer."
		rep.Fix = "run 'tuios ls' to check it, or see the daemon log with 'tuios logs'."
		return rep
	}
	rep.Restarted = true
	rep.After = cmp.Or(after, plan.Version)
	rep.Back = live
	for _, name := range before {
		if !slices.Contains(live, name) {
			rep.Missing = append(rep.Missing, name)
		}
	}
	return rep
}

// runTuios runs one tuios command and keeps its output for the error.
func runTuios(bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), restartTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = nil
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// outputTail is the last line a failed command wrote, as a sentence to add
// to the fix.
func outputTail(out string) string {
	line := lastLine(out)
	if line == "" {
		return ""
	}
	return " It said: " + plainLine(line)
}

// readRestartedDaemon asks the new daemon its version and its sessions.
func readRestartedDaemon() (string, []string, error) {
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = client.Close() }()
	var after string
	if raw, err := client.Call("hello", map[string]any{"client": "tuios-restart-server", "version": version, "protocol": session.VerbProtocolVersion}); err == nil {
		var hs struct {
			DaemonVersion string `json:"daemon_version"`
		}
		if json.Unmarshal(raw, &hs) == nil {
			after = hs.DaemonVersion
		}
	}
	raw, err := client.Call("list-sessions", nil)
	if err != nil {
		return after, nil, err
	}
	var list struct {
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return after, nil, err
	}
	names := make([]string, 0, len(list.Sessions))
	for _, s := range list.Sessions {
		names = append(names, plainLine(s.Name))
	}
	return after, names, nil
}

// confirmRestart asks the question. Without a terminal there is no one to
// ask, and the command refuses rather than guess.
func confirmRestart(opts restartOptions) error {
	if !syncIsTTY(syncOptions{tty: opts.tty}) {
		return &diagnosticError{
			What:  "A restart needs your agreement, and there is no terminal to ask on.",
			Cause: "a restart stops the programs listed above.",
			Fix:   "add --yes to restart without a question, or run the command in a terminal. Nothing was changed.",
		}
	}
	fmt.Fprint(opts.out, "Restart the daemon? [y/N] ")
	line, _ := bufio.NewReader(opts.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errors.New("the daemon was not restarted and nothing was changed")
}

// writeRestartJSON writes one report as one line.
func writeRestartJSON(w io.Writer, rep *restartReport) {
	data, err := json.Marshal(rep)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}

// writeRestartPlan says what a restart does: the versions, what stops and
// what comes back. The app draws the same lines.
func writeRestartPlan(w io.Writer, rep *restartReport) {
	if rep.Error != "" {
		return
	}
	fmt.Fprint(w, restartplan.Text(restartplan.Lines(rep, "")))
}

// writeRestartDone says the restart worked, and which sessions came back.
func writeRestartDone(w io.Writer, rep *restartReport) {
	fmt.Fprintf(w, "The daemon restarted on %s.\n", rep.After)
	if len(rep.Back) > 0 {
		fmt.Fprintf(w, "%s: %s.\n", plural.Word(len(rep.Back), "This session came back", "These sessions came back"), strings.Join(rep.Back, ", "))
	}
	if len(rep.Missing) > 0 {
		fmt.Fprintf(w, "%s did not come back: %s. Run 'tuios resurrect' to see the saved sessions.\n",
			plural.Word(len(rep.Missing), "This session", "These sessions"), strings.Join(rep.Missing, ", "))
	}
}
