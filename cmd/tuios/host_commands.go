package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"golang.org/x/term"
)

// The CLI half of federation stage 1: `tuios hosts`, the --all-hosts listings,
// and the stdio-proxy subcommand the far side of every link runs.
//
// Everything here reads. Nothing in this file can create, kill or change
// anything on another machine, and the daemon has no verb that would let it.

// hostReport is one row of the list-hosts result.
type hostReport struct {
	Host          string `json:"host"`
	Addr          string `json:"addr"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	Detail        string `json:"detail"`
	DaemonVersion string `json:"daemon_version"`
	Protocol      int    `json:"protocol"`
	MinProtocol   int    `json:"min_protocol"`
	Sessions      int    `json:"sessions"`
	LastOK        int64  `json:"last_ok"`
	LastTry       int64  `json:"last_try"`
}

// runListHosts prints the configured hosts and the state of each link.
func runListHosts(jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("list-hosts", nil)
	if err != nil {
		return reportVerbError(explainVerbError("list-hosts", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	return printHostList(os.Stdout, raw)
}

func printHostList(w io.Writer, raw json.RawMessage) error {
	var res struct {
		Hosts          []hostReport `json:"hosts"`
		Total          int          `json:"total"`
		ConfigProblems []string     `json:"config_problems"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if len(res.Hosts) == 0 {
		fmt.Fprintln(w, "No hosts are configured.")
		fmt.Fprintln(w, "To add a host, put a [hosts.NAME] table with an addr in the config file.")
		fmt.Fprintln(w, "Then restart the daemon with 'tuios kill-server'.")
		printConfigProblems(w, res.ConfigProblems)
		return nil
	}

	rows := make([][]string, 0, len(res.Hosts))
	for _, h := range res.Hosts {
		version := h.DaemonVersion
		if version == "" {
			version = "-"
		}
		protocol := "-"
		if h.Protocol > 0 {
			protocol = strconv.Itoa(h.Protocol)
		}
		sessions := "-"
		if h.Status == string(federation.StatusUp) {
			sessions = strconv.Itoa(h.Sessions)
		}
		rows = append(rows, []string{
			h.Host,
			h.Addr,
			h.Status,
			version,
			protocol,
			sessions,
			formatTimeAgo(h.LastOK),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("HOST", "ADDRESS", "STATUS", "VERSION", "PROTO", "SESSIONS", "LAST OK").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("12"))
			}
			switch col {
			case 0:
				return base.Foreground(lipgloss.Color("3")).Bold(true)
			case 2:
				return base.Foreground(hostStatusColor(res.Hosts[row].Status))
			case 1, 3, 4:
				return base.Foreground(lipgloss.Color("8"))
			default:
				return base
			}
		})
	fmt.Fprintln(w, t.Render())
	fmt.Fprintf(w, "\n%d host(s). No writes cross a link. These listings are read only.\n", res.Total)

	// The reason a host is not usable is the only thing the table cannot say in
	// a column, so it goes below, one line per host that has one.
	for _, h := range res.Hosts {
		if h.Status == string(federation.StatusUp) || h.Reason == "" {
			continue
		}
		fmt.Fprintf(w, "%s: %s\n", h.Host, h.Reason)
		if h.Detail != "" {
			// The detail comes from ssh or from the other machine. It is
			// labelled so a reader cannot mistake it for something tuios said.
			fmt.Fprintf(w, "  the link reported: %s\n", h.Detail)
		}
	}
	printConfigProblems(w, res.ConfigProblems)
	return nil
}

func printConfigProblems(w io.Writer, problems []string) {
	for _, p := range problems {
		fmt.Fprintf(w, "config: %s\n", p)
	}
}

func hostStatusColor(status string) lipgloss.Color {
	switch federation.Status(status) {
	case federation.StatusUp:
		return lipgloss.Color("2")
	case federation.StatusUnreachable:
		return lipgloss.Color("1")
	case federation.StatusIncompatible:
		return lipgloss.Color("3")
	default:
		return lipgloss.Color("8")
	}
}

// hostSessionsEntry is one host's slice of an aggregated session listing.
type hostSessionsEntry struct {
	Host     string `json:"host"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Error    string `json:"error"`
	Sessions []struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		WindowCount int    `json:"window_count"`
		Attached    bool   `json:"attached"`
		Restored    bool   `json:"restored"`
		LastActive  int64  `json:"last_active"`
		Created     int64  `json:"created"`
	} `json:"sessions"`
}

// runListSessionsAllHosts is `tuios ls --all-hosts`. One dead host costs its own
// row and nothing else.
func runListSessionsAllHosts(host string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{}
	if host != "" {
		params["host"] = host
	}
	raw, err := client.Call("list-host-sessions", params)
	if err != nil {
		return reportVerbError(explainVerbError("list-host-sessions", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}

	var res struct {
		Hosts []hostSessionsEntry `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	total, reachable := 0, 0
	for _, h := range res.Hosts {
		if h.Error != "" {
			fmt.Printf("%s: %s\n", h.Host, hostTrouble(h.Status, h.Reason))
			fmt.Println()
			continue
		}
		reachable++
		fmt.Printf("%s\n", h.Host)
		if len(h.Sessions) == 0 {
			fmt.Println("  No sessions.")
			fmt.Println()
			continue
		}
		rows := make([][]string, 0, len(h.Sessions))
		for _, s := range h.Sessions {
			total++
			status := "detached"
			if s.Attached {
				status = "attached"
			}
			if s.Restored {
				status = session.RestoredTag
			}
			name := s.Name
			if s.DisplayName != "" {
				name = s.DisplayName
			}
			rows = append(rows, []string{
				name,
				strconv.Itoa(s.WindowCount),
				status,
				formatTimeAgo(s.Created),
				formatTimeAgo(s.LastActive),
			})
		}
		fmt.Println(renderSessionTable(rows))
		fmt.Println()
	}
	fmt.Printf("%d session(s) on %d host(s).\n", total, reachable)
	return nil
}

// hostAgentsEntry is one host's slice of an aggregated agent listing.
type hostAgentsEntry struct {
	Host    string `json:"host"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Error   string `json:"error"`
	Session string `json:"session"`
	Agents  []struct {
		WindowID  string `json:"window_id"`
		Name      string `json:"name"`
		State     string `json:"state"`
		Message   string `json:"message"`
		HarnessID string `json:"harness_id"`
		Unread    int    `json:"unread"`
	} `json:"agents"`
}

// runListAgentsAllHosts is `tuios list-agents --all-hosts`.
func runListAgentsAllHosts(host string, all, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{"all": all}
	if host != "" {
		params["host"] = host
	}
	raw, err := client.Call("list-host-agents", params)
	if err != nil {
		return reportVerbError(explainVerbError("list-host-agents", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}

	var res struct {
		Hosts []hostAgentsEntry `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	total := 0
	for _, h := range res.Hosts {
		if h.Error != "" {
			fmt.Printf("%s: %s\n\n", h.Host, hostTrouble(h.Status, h.Reason))
			continue
		}
		header := h.Host
		if h.Session != "" {
			header += "  session " + h.Session
		}
		fmt.Println(header)
		if len(h.Agents) == 0 {
			fmt.Println("  No agent panes.")
			fmt.Println()
			continue
		}
		rows := make([][]string, 0, len(h.Agents))
		for _, a := range h.Agents {
			total++
			unread := ""
			if a.Unread > 0 {
				unread = strconv.Itoa(a.Unread)
			}
			rows = append(rows, []string{
				shortWindowID(a.WindowID),
				a.Name,
				a.State,
				orNone(a.HarnessID),
				unread,
				a.Message,
			})
		}
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
			Headers("ID", "NAME", "STATE", "HARNESS", "MAIL", "NOTE").
			Rows(rows...).
			StyleFunc(func(row, _ int) lipgloss.Style {
				base := lipgloss.NewStyle().Padding(0, 1)
				if row == table.HeaderRow {
					return base.Bold(true).Foreground(lipgloss.Color("12"))
				}
				return base
			})
		fmt.Println(t.Render())
		fmt.Println()
	}
	fmt.Printf("%d agent pane(s). A pane on another host is read only in this release.\n", total)
	return nil
}

// hostTrouble is the one line a host that did not answer gets.
func hostTrouble(status, reason string) string {
	if reason == "" {
		return status
	}
	return status + ". " + reason
}

// runStdioProxy is the far side of a link.
//
// It connects the link framing on stdin and stdout to this machine's daemon
// socket. It is meant to be run by the hub over ssh, never by hand, so it is
// hidden. It refuses to run on a terminal: frames written to a tty would be
// interpreted as escape sequences by whatever is watching.
//
// It does not start a daemon. Starting one restores that machine's saved
// sessions, which is a change to remote state, and stage 1 of federation reads
// only. A machine with no daemon running is reported as such by 'tuios hosts'.
func runStdioProxy() error {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("stdio-proxy is not meant to be run by hand. " +
			"The tuios daemon runs it over ssh to read another machine's listings")
	}
	socketPath, err := session.GetSocketPath()
	if err != nil {
		return err
	}
	return federation.ServeProxy(os.Stdin, os.Stdout, func() (net.Conn, error) {
		return net.DialTimeout("unix", socketPath, 5*time.Second)
	})
}
