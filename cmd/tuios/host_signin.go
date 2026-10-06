package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// `tuios hosts signin` opens the Tailscale sign-in page of each host whose
// link waits for one. It is the CLI twin of a click on the host's header in
// the rail. See internal/app/host_signin.go.

// signInURLWait is how long the command waits for the daemon's link to report
// a sign-in page it does not have yet. A redial reaches Tailscale's banner in
// a second or two.
const signInURLWait = 15 * time.Second

// hostStatusWord is the STATUS column of `tuios hosts`. A host that waits for
// a Tailscale sign-in says "sign in", which says what to do. The JSON keeps
// tailscale_check, so a script that reads it does not break.
func hostStatusWord(status string) string {
	if status == string(federation.StatusApproval) {
		return "sign in"
	}
	return status
}

// signInRow is one host in the result of `tuios hosts signin --json`.
type signInRow struct {
	Host string `json:"host"`
	URL  string `json:"url,omitempty"`
	// Opened says a browser was started for the page.
	Opened bool `json:"opened"`
	// Note says why the page was not opened. Omitted when it was.
	Note string `json:"note,omitempty"`
}

func newHostsSigninCommand() *cobra.Command {
	var asJSON, printOnly bool
	cmd := &cobra.Command{
		Use:   "signin [name]",
		Short: "Open the Tailscale sign-in page of each host that waits for one",
		Long: `Open the Tailscale sign-in page of each host that waits for one.

A host behind Tailscale SSH in check mode waits until you sign in in a
browser. 'tuios hosts' shows it as "sign in". This command opens the page.
The link continues when you sign in. You do not have to run anything again.

With a name, only that host is opened. With no name, every host that waits
is opened.

When this machine has no desktop, as over ssh, the command prints the
address. Open it in a browser on your own machine.

The daemon dials the host again at once, and every few seconds for two
minutes after that. A sign-in page that expired is replaced by a new one.`,
		Example: `  tuios hosts signin
  tuios hosts signin build
  tuios hosts signin --print --json`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeConfiguredHosts,
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runHostSignin(os.Stdout, name, printOnly, asJSON)
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "Print the address and do not open a browser")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the result as JSON")
	return cmd
}

func runHostSignin(w io.Writer, name string, printOnly, asJSON bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	reports, err := signInReports(client)
	if err != nil {
		return reportVerbError(explainVerbError("list-hosts", err), asJSON)
	}
	var waiting []string
	found := false
	for _, r := range reports {
		if name != "" && r.Host != name {
			continue
		}
		found = true
		if r.Status == federation.StatusApproval {
			waiting = append(waiting, r.Host)
		}
	}
	if name != "" && !found {
		return fmt.Errorf("no host named %q is configured. Run 'tuios hosts' to see the hosts", name)
	}

	rows := make([]signInRow, 0, len(waiting))
	if len(waiting) > 0 {
		for _, host := range waiting {
			// The redial stays quick while the person signs in, so the
			// link comes up soon after they do.
			if _, err := client.Call("retry-host", map[string]any{"host": host}); err != nil {
				return reportVerbError(explainVerbError("retry-host", err), asJSON)
			}
		}
		urls := waitForSignInURLs(client, waiting, signInURLWait)
		opener := ""
		if cfg, err := config.LoadUserConfig(); err == nil {
			opener = strings.TrimSpace(cfg.Appearance.LinkOpener)
		}
		for _, host := range waiting {
			row := signInRow{Host: host, URL: urls[host]}
			switch {
			case row.URL == "":
				row.Note = "The link has no sign-in page yet. Run ssh to the host in a terminal to see it."
			case printOnly:
				row.Note = "Not opened, because of --print."
			default:
				if err := app.OpenWebLink(opener, row.URL); err != nil {
					row.Note = "Not opened: " + err.Error() + "."
					if errors.Is(err, app.ErrNoDesktop) {
						row.Note = "This machine has no desktop to open it. Open the address in a browser on your own machine."
					}
				} else {
					row.Opened = true
				}
			}
			rows = append(rows, row)
		}
	}

	if asJSON {
		data, err := json.MarshalIndent(map[string]any{"hosts": rows}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
		return nil
	}
	if len(rows) == 0 {
		if name != "" {
			fmt.Fprintf(w, "%s does not wait for a Tailscale sign-in.\n", name)
		} else {
			fmt.Fprintln(w, "No host waits for a Tailscale sign-in.")
		}
		return nil
	}
	for _, r := range rows {
		switch {
		case r.Opened:
			fmt.Fprintf(w, "%s: opened the sign-in page %s\n", r.Host, r.URL)
		case r.URL != "":
			fmt.Fprintf(w, "%s: open %s to sign in.\n", r.Host, r.URL)
			fmt.Fprintf(w, "  %s\n", r.Note)
		default:
			fmt.Fprintf(w, "%s: %s\n", r.Host, r.Note)
		}
	}
	fmt.Fprintln(w, "The link continues when you sign in. Run 'tuios hosts' to see the state.")
	return nil
}

// signInReports reads list-hosts.
func signInReports(client *session.VerbClient) ([]federation.HostReport, error) {
	raw, err := client.Call("list-hosts", nil)
	if err != nil {
		return nil, err
	}
	var res struct {
		Hosts []federation.HostReport `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return res.Hosts, nil
}

// waitForSignInURLs polls list-hosts until every host has a sign-in page, or
// the wait ends. A host that has none by then is left out.
func waitForSignInURLs(client *session.VerbClient, hosts []string, wait time.Duration) map[string]string {
	urls := map[string]string{}
	deadline := time.Now().Add(wait)
	for {
		if reports, err := signInReports(client); err == nil {
			for _, r := range reports {
				if r.Status == federation.StatusApproval && r.ApprovalURL != "" {
					urls[r.Host] = r.ApprovalURL
				}
			}
		}
		done := true
		for _, h := range hosts {
			if urls[h] == "" {
				done = false
			}
		}
		if done || time.Now().After(deadline) {
			return urls
		}
		time.Sleep(500 * time.Millisecond)
	}
}
