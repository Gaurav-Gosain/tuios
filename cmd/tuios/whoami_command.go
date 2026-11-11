package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// newWhoamiCommand builds `tuios whoami`: where the caller runs, as the daemon
// sees it now, and what the person sees.
func newWhoamiCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the pane, session and daemon this command runs in, as they are now",
		Long: `Show where this command runs, as the daemon sees it now: the pane and its
name and workspace, the session, the grants the pane holds, the daemon and its
socket, and the host. It also shows what the person sees in that session: the
focused pane, the workspace on screen, and the clients attached to it.

The daemon finds the pane from the kernel's record of the caller, not from the
environment, so every id it prints exists now. The TUIOS_* variables a pane
starts with are not updated: a session renamed later still shows its old name
in TUIOS_SESSION. whoami lists each variable that no longer matches under
stale_env, so an agent knows to use the ids it printed instead.

Run outside every pane, it says so, and shows the daemon and the clients.`,
		Example: `  # Where am I?
  tuios whoami

  # The current session name, for a script
  tuios whoami --json | jq -r '.session'

  # The pane the person has in focus in this session
  tuios whoami --json | jq -r '.focused_window_id'`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runWhoami(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

// whoamiClient is one client attached to the caller's session.
type whoamiClient struct {
	ClientID string `json:"client_id"`
	PID      int    `json:"pid"`
	Session  string `json:"session"`
}

// whoamiDaemon is the daemon the command reached.
type whoamiDaemon struct {
	Version string `json:"version,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Socket  string `json:"socket,omitempty"`
	// Server is the name of a named daemon (--server), "" for the default one.
	Server string `json:"server,omitempty"`
}

// whoamiResult is what whoami reports. Every id in it comes from the daemon.
type whoamiResult struct {
	Success          bool           `json:"success"`
	Message          string         `json:"message"`
	InPane           bool           `json:"in_pane"`
	WindowID         string         `json:"window_id,omitempty"`
	ShortID          string         `json:"short_id,omitempty"`
	Name             string         `json:"name,omitempty"`
	Workspace        int            `json:"workspace,omitempty"`
	Session          string         `json:"session,omitempty"`
	Grants           []string       `json:"grants"`
	FocusedWindowID  string         `json:"focused_window_id,omitempty"`
	FocusedName      string         `json:"focused_name,omitempty"`
	CurrentWorkspace int            `json:"current_workspace,omitempty"`
	Clients          []whoamiClient `json:"clients"`
	ClientsNote      string         `json:"clients_note,omitempty"`
	Daemon           whoamiDaemon   `json:"daemon"`
	Host             string         `json:"host,omitempty"`
	// StaleEnv names the TUIOS_* variables of this process that no longer
	// match what the daemon says.
	StaleEnv []string `json:"stale_env"`
}

func runWhoami(jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	defer func() { _ = client.Close() }()
	res, err := whoami(client)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		outputJSON(res)
		return nil
	}
	fmt.Print(describeWhoami(res))
	return nil
}

// whoami asks the daemon where the caller runs and what its session shows.
func whoami(client *session.VerbClient) (*whoamiResult, error) {
	res := &whoamiResult{Success: true, Message: "whoami", Grants: []string{}, Clients: []whoamiClient{}, StaleEnv: []string{}}
	if d := client.Daemon(); d != nil {
		res.Daemon.Version = d.DaemonVersion
		res.Daemon.PID = d.PID
	}
	if path, err := session.GetSocketPath(); err == nil {
		res.Daemon.Socket = path
	}
	res.Daemon.Server = serverscope.Name()
	if h, err := os.Hostname(); err == nil {
		res.Host = h
	}

	raw, err := client.Call("pane-grants", nil)
	if err != nil {
		return nil, explainVerbError("pane-grants", err)
	}
	var me struct {
		Pane    bool     `json:"pane"`
		Window  string   `json:"window"`
		Session string   `json:"session"`
		Grants  []string `json:"grants"`
	}
	if err := jsonx.Unmarshal(raw, &me); err != nil {
		return nil, errfmt.Errorf("failed to parse pane-grants: %w", err)
	}
	res.InPane = me.Pane
	if me.Pane {
		res.WindowID = me.Window
		res.ShortID = shortWindowID(me.Window)
		res.Session = me.Session
		if me.Grants != nil {
			res.Grants = me.Grants
		}
		whoamiWindows(client, res)
	}

	// Clients need no pane: outside every pane they are all the attached ones.
	if raw, err := client.Call("list-clients", nil); err != nil {
		res.ClientsNote = plainLine(err.Error())
	} else {
		var list struct {
			Clients []whoamiClient `json:"clients"`
		}
		if err := jsonx.Unmarshal(raw, &list); err != nil {
			return nil, errfmt.Errorf("failed to parse list-clients: %w", err)
		}
		for _, c := range list.Clients {
			// A connection with no session is a CLI call, this one among them.
			if c.Session == "" || res.InPane && c.Session != res.Session {
				continue
			}
			res.Clients = append(res.Clients, c)
		}
	}

	res.StaleEnv = staleEnv(res, os.Getenv)
	return res, nil
}

// whoamiWindows fills in the caller's pane name and workspace, and what the
// session shows, from list-windows. A failure leaves them out: the ids from
// pane-grants still stand.
func whoamiWindows(client *session.VerbClient, res *whoamiResult) {
	raw, err := client.Call("list-windows", map[string]any{"session": res.Session})
	if err != nil {
		return
	}
	var list struct {
		FocusedWindowID  string `json:"focused_window_id"`
		CurrentWorkspace int    `json:"current_workspace"`
		Windows          []struct {
			WindowID    string `json:"window_id"`
			DisplayName string `json:"display_name"`
			Workspace   int    `json:"workspace"`
		} `json:"windows"`
	}
	if jsonx.Unmarshal(raw, &list) != nil {
		return
	}
	res.FocusedWindowID = list.FocusedWindowID
	res.CurrentWorkspace = list.CurrentWorkspace
	for _, w := range list.Windows {
		if w.WindowID == res.WindowID {
			res.Name = w.DisplayName
			res.Workspace = w.Workspace
		}
		if w.WindowID == list.FocusedWindowID {
			res.FocusedName = w.DisplayName
		}
	}
}

// staleEnv names the TUIOS_* variables that disagree with what the daemon
// said. A variable that is unset is not stale: it says nothing.
func staleEnv(res *whoamiResult, getenv func(string) string) []string {
	stale := []string{}
	pane := res.WindowID
	for _, name := range []string{"TUIOS_PANE_ID", "TUIOS_WINDOW_ID"} {
		if v := getenv(name); v != "" && v != pane {
			stale = append(stale, name)
		}
	}
	if v := getenv("TUIOS_SESSION"); v != "" && v != res.Session {
		stale = append(stale, "TUIOS_SESSION")
	}
	if v := getenv("TUIOS_SOCKET"); v != "" && res.Daemon.Socket != "" && v != res.Daemon.Socket {
		stale = append(stale, "TUIOS_SOCKET")
	}
	return stale
}

// describeWhoami is the text whoami prints.
func describeWhoami(res *whoamiResult) string {
	var b strings.Builder
	if res.InPane {
		name := ""
		if res.Name != "" {
			name = " (" + plainLine(res.Name) + ")"
		}
		fmt.Fprintf(&b, "Pane %s%s in session %s", res.ShortID, name, plainLine(res.Session))
		if res.Workspace > 0 {
			fmt.Fprintf(&b, ", workspace %d", res.Workspace)
		}
		fmt.Fprintf(&b, ", holds %s.\n", grantList(res.Grants))
		if res.FocusedWindowID != "" {
			focus := shortWindowID(res.FocusedWindowID)
			if res.FocusedName != "" {
				focus += " (" + plainLine(res.FocusedName) + ")"
			}
			if res.FocusedWindowID == res.WindowID {
				focus += ", this pane"
			}
			fmt.Fprintf(&b, "The session shows workspace %d; the focused pane is %s.\n", res.CurrentWorkspace, focus)
		}
	} else {
		b.WriteString("This command runs in no pane of tuios.\n")
	}
	switch {
	case res.ClientsNote != "":
		fmt.Fprintf(&b, "Clients: not listed (%s).\n", res.ClientsNote)
	case len(res.Clients) == 0 && res.InPane:
		b.WriteString("No client is attached to this session: nobody sees it now.\n")
	case len(res.Clients) == 0:
		b.WriteString("No client is attached to a session.\n")
	default:
		for _, c := range res.Clients {
			fmt.Fprintf(&b, "Client %s (pid %d) shows session %s.\n", plainLine(c.ClientID), c.PID, plainLine(c.Session))
		}
	}
	daemon := "Daemon"
	if res.Daemon.Version != "" {
		daemon += " " + plainLine(res.Daemon.Version)
	}
	if res.Daemon.PID > 0 {
		daemon += fmt.Sprintf(" (pid %d)", res.Daemon.PID)
	}
	if res.Daemon.Server != "" {
		daemon += ", server " + plainLine(res.Daemon.Server)
	}
	if res.Daemon.Socket != "" {
		daemon += " on " + plainLine(res.Daemon.Socket)
	}
	if res.Host != "" {
		daemon += ", host " + plainLine(res.Host)
	}
	b.WriteString(daemon)
	b.WriteString(".\n")
	if len(res.StaleEnv) > 0 {
		fmt.Fprintf(&b, "Out of date in this environment: %s. Use the ids above.\n", strings.Join(res.StaleEnv, ", "))
	}
	return b.String()
}
