package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that parts of tuios are set up and working",
		Long: `Check that parts of tuios are set up and working.

tuios keybinds doctor checks the keybindings; the checks here cover the rest.`,
	}
	cmd.AddCommand(newDoctorAgentsCommand())
	return cmd
}

// agentPaneGap is an agent pane whose harness has an integration this machine
// does not have installed, so its state rests on screen rules and silence.
type agentPaneGap struct {
	Session string `json:"session"`
	Window  string `json:"window_id"`
	Name    string `json:"name"`
	Harness string `json:"harness"`
}

// doctorAgentsReport is what tuios doctor agents prints.
type doctorAgentsReport struct {
	TuiosOnPath bool                 `json:"tuios_on_path"`
	Harnesses   []integration.Status `json:"harnesses"`
	// Panes lists running agents without their integration. It is empty when
	// no daemon runs, and DaemonRunning says which.
	DaemonRunning bool           `json:"daemon_running"`
	Panes         []agentPaneGap `json:"panes_without_integration"`
	// ManifestDir is the user manifest directory, UserManifests the files in
	// it that loaded, and ManifestErrors the ones that did not. A user file
	// with a bundled id replaces the bundled manifest whole, which is worth
	// seeing in the one place that says what is set up.
	ManifestDir    string         `json:"manifest_dir"`
	UserManifests  []userManifest `json:"user_manifests"`
	ManifestErrors []string       `json:"manifest_errors"`
}

// userManifest is one harness manifest loaded from the user directory.
type userManifest struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	ReplacesBundled bool   `json:"replaces_bundled"`
}

// userManifests loads the registry the daemon would load from dir and
// reports what came from dir: each manifest in force and each file that
// failed to load.
func userManifests(dir string) ([]userManifest, []string) {
	reg, errs := harness.Load(dir)
	var out []userManifest
	for _, id := range reg.IDs() {
		src, replaced := reg.Lookup(id).Source()
		if src == "bundled" {
			continue
		}
		out = append(out, userManifest{ID: id, Path: src, ReplacesBundled: replaced})
	}
	var failed []string
	for _, e := range errs {
		failed = append(failed, e.Error())
	}
	return out, failed
}

func newDoctorAgentsCommand() *cobra.Command {
	var asJSON bool
	var command string
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Report, per harness, whether its integration is installed and current",
		Long: `Report, for every harness tuios can integrate with, whether the harness is on
PATH, whether tuios is on PATH for its hooks to run, and whether the
integration is installed and current. With a daemon running it also lists
the agent panes whose harness has an integration that is not installed,
since their state then rests on screen rules and the silence timer. Last, it
lists the harness manifests loaded from the user manifest directory, saying
which replace a bundled manifest, and the files there that failed to load.`,
		Example: `  tuios doctor agents
  tuios doctor agents --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			env := integration.SystemEnv()
			report := doctorAgents(env, command, livePanes)
			report.ManifestDir = harness.UserDir()
			report.UserManifests, report.ManifestErrors = userManifests(report.ManifestDir)
			return printDoctorAgents(os.Stdout, report, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the report as JSON")
	cmd.Flags().StringVar(&command, "command", "tuios", "Program a current install runs")
	return cmd
}

// agentPane is one row of list-agents, as doctor reads it.
type agentPane struct {
	Session string
	Window  string
	Name    string
	Harness string
}

// livePanes lists every agent pane on the running daemon, and false when no
// daemon runs.
func livePanes() ([]agentPane, bool) {
	if !session.DiagnoseDaemon().Running() {
		return nil, false
	}
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		return nil, false
	}
	defer func() { _ = client.Close() }()
	raw, err := client.CallWithTimeout("list-sessions", nil, 2*time.Second)
	if err != nil {
		return nil, true
	}
	var sessions struct {
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	if json.Unmarshal(raw, &sessions) != nil {
		return nil, true
	}
	var out []agentPane
	for _, s := range sessions.Sessions {
		raw, err := client.CallWithTimeout("list-agents", map[string]any{"session": s.Name}, 2*time.Second)
		if err != nil {
			continue
		}
		var res struct {
			Agents []struct {
				WindowID string `json:"window_id"`
				Name     string `json:"name"`
				Harness  string `json:"harness_id"`
			} `json:"agents"`
		}
		if json.Unmarshal(raw, &res) != nil {
			continue
		}
		for _, a := range res.Agents {
			out = append(out, agentPane{Session: s.Name, Window: a.WindowID, Name: a.Name, Harness: a.Harness})
		}
	}
	return out, true
}

// doctorAgents builds the report. panes is injected so the report can be
// tested without a daemon.
func doctorAgents(env integration.Env, command string, panes func() ([]agentPane, bool)) doctorAgentsReport {
	var r doctorAgentsReport
	installed := map[string]bool{}
	for _, t := range integration.Targets() {
		st := t.Status(env, command)
		r.Harnesses = append(r.Harnesses, st)
		r.TuiosOnPath = st.TuiosOnPath
		installed[t.ID] = st.Installed
	}
	if panes == nil {
		return r
	}
	live, running := panes()
	r.DaemonRunning = running
	for _, p := range live {
		id, ok := integration.Canonical(p.Harness)
		if !ok || installed[id] {
			continue
		}
		r.Panes = append(r.Panes, agentPaneGap(p))
	}
	return r
}

func printDoctorAgents(w io.Writer, r doctorAgentsReport, asJSON bool) error {
	if asJSON {
		out, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(out))
		return err
	}
	if !r.TuiosOnPath {
		fmt.Fprintln(w, "tuios is not on PATH, so hooks that run \"tuios agent-hook\" cannot start. Install with --command set to its full path.")
	}
	for _, s := range r.Harnesses {
		where := "not on PATH"
		if s.BinaryPath != "" {
			where = s.BinaryPath
		}
		fmt.Fprintf(w, "%-12s %s\n", s.Harness, where)
		fmt.Fprintf(w, "%-12s integration %s\n", "", integrationVerdict(s))
		for _, n := range s.Notes {
			fmt.Fprintf(w, "%-12s note: %s\n", "", n)
		}
	}
	switch {
	case !r.DaemonRunning:
		fmt.Fprintln(w, "No daemon is running, so no panes were checked.")
	case len(r.Panes) == 0:
		fmt.Fprintln(w, "Every agent pane with an integration available has it installed.")
	default:
		fmt.Fprintln(w, "Agent panes whose integration is not installed:")
		for _, p := range r.Panes {
			fmt.Fprintf(w, "  %s:%s (%s) runs %s\n", p.Session, p.Window, p.Name, p.Harness)
		}
	}
	for _, m := range r.UserManifests {
		if m.ReplacesBundled {
			fmt.Fprintf(w, "Manifest %s replaces the bundled one: %s\n", m.ID, m.Path)
		} else {
			fmt.Fprintf(w, "Manifest %s is loaded from %s\n", m.ID, m.Path)
		}
	}
	for _, e := range r.ManifestErrors {
		fmt.Fprintf(w, "Manifest not loaded: %s\n", e)
	}
	return nil
}
