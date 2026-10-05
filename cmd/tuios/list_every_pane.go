package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// list-windows --all: every pane of every session, on this machine and with
// --all-hosts on every host in [hosts], with what the pane navigator
// (choose_tree) shows for it. --text N adds the last N lines of each pane's
// screen.
//
// It is built from the verbs a single list-windows and capture-pane already
// make, one call per session and per pane, so the grants and the link policy
// that hold for those verbs hold here: a pane whose text the caller may not
// read is listed with the reason and no text.

// maxListText caps --text, so one command cannot pull every pane's whole
// scrollback.
const maxListText = 200

// listCallTimeout bounds one verb call of the listing.
const listCallTimeout = 5 * time.Second

// listedPane is one row of list-windows --all.
type listedPane struct {
	Host        string   `json:"host,omitempty"`
	Untrusted   bool     `json:"untrusted,omitempty"`
	Session     string   `json:"session"`
	Workspace   int      `json:"workspace"`
	WorkspaceNm string   `json:"workspace_name,omitempty"`
	WindowID    string   `json:"window_id"`
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Command     string   `json:"command,omitempty"`
	Cwd         string   `json:"cwd,omitempty"`
	Focused     bool     `json:"focused"`
	AgentState  string   `json:"agent_state,omitempty"`
	Text        []string `json:"text,omitempty"`
	TextError   string   `json:"text_error,omitempty"`
}

// listError is a session or a machine the listing could not read.
type listError struct {
	Host    string `json:"host,omitempty"`
	Session string `json:"session,omitempty"`
	Error   string `json:"error"`
}

// runListAllWindows is list-windows with --all, --all-hosts or --text.
// session limits it to one session when --all is not given.
func runListAllWindows(sessionName string, all, allHosts bool, text int, jsonOutput bool) error {
	if text < 0 || text > maxListText {
		return fmt.Errorf("--text takes a number of lines from 0 to %d", maxListText)
	}
	if sessionName != "" && (all || allHosts) {
		return fmt.Errorf("--session names one session, so it cannot be used with --all or --all-hosts")
	}
	var panes []listedPane
	var errs []listError

	if !all && !allHosts {
		t, err := dialSessionTarget(sessionName)
		if err != nil {
			return err
		}
		defer t.Close()
		p, e := listSessionPanes(t.client, t.host, t.session, text)
		panes, errs = append(panes, p...), append(errs, e...)
		return printListedPanes(panes, errs, text, jsonOutput)
	}

	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	names, err := listSessionNames(client)
	if err != nil {
		// A caller limited to its own session may not list the others. It
		// still gets its own.
		errs = append(errs, listError{Error: explainVerbError("list-sessions", err).Error()})
		names = []string{""}
	}
	for _, name := range names {
		p, e := listSessionPanes(client, "", name, text)
		panes, errs = append(panes, p...), append(errs, e...)
	}

	if allHosts {
		raw, err := client.CallWithTimeout("list-host-sessions", nil, listCallTimeout)
		if err != nil {
			errs = append(errs, listError{Error: explainVerbError("list-host-sessions", err).Error()})
		} else {
			var res struct {
				Hosts []hostSessionsEntry `json:"hosts"`
			}
			if json.Unmarshal(raw, &res) == nil {
				for _, h := range res.Hosts {
					if h.Host == federation.LocalHostName {
						continue
					}
					if h.Error != "" || h.Stale {
						errs = append(errs, listError{Host: h.Host, Error: hostTrouble(h.Status, h.Reason)})
						continue
					}
					hc, _, err := session.DialVerbClientThroughHost(h.Host, version)
					if err != nil {
						errs = append(errs, listError{Host: h.Host, Error: err.Error()})
						continue
					}
					for _, s := range h.Sessions {
						p, e := listSessionPanes(hc, h.Host, s.Name, text)
						panes, errs = append(panes, p...), append(errs, e...)
					}
					_ = hc.Close()
				}
			}
		}
	}
	return printListedPanes(panes, errs, text, jsonOutput)
}

// listSessionNames is every session on the client's machine.
func listSessionNames(client *session.VerbClient) ([]string, error) {
	raw, err := client.CallWithTimeout("list-sessions", nil, listCallTimeout)
	if err != nil {
		return nil, err
	}
	var res struct {
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("failed to parse list-sessions: %w", err)
	}
	names := make([]string, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		names = append(names, s.Name)
	}
	return names, nil
}

// listSessionPanes lists one session's panes, and with text > 0 reads each
// one's last lines with capture-pane.
func listSessionPanes(client *session.VerbClient, host, name string, text int) ([]listedPane, []listError) {
	raw, err := client.CallWithTimeout("list-windows", map[string]any{"session": name}, listCallTimeout)
	if err != nil {
		return nil, []listError{{Host: host, Session: name, Error: explainVerbError("list-windows", err).Error()}}
	}
	var list struct {
		Session string `json:"session_name"`
		Focused string `json:"focused_window_id"`
		Windows []struct {
			ID         string `json:"window_id"`
			Display    string `json:"display_name"`
			Title      string `json:"title"`
			Cwd        string `json:"cwd"`
			Command    string `json:"foreground_cmd"`
			Workspace  int    `json:"workspace"`
			Scratch    bool   `json:"scratch"`
			AgentState string `json:"agent_state"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, []listError{{Host: host, Session: name, Error: "could not read the window list"}}
	}
	if list.Session != "" {
		name = list.Session
	}
	wsNames, sessName := listWorkspaceNames(client, name)
	if sessName != "" {
		name = sessName
	}
	out := make([]listedPane, 0, len(list.Windows))
	for _, w := range list.Windows {
		if w.Scratch {
			continue
		}
		state := w.AgentState
		if state == "none" {
			state = ""
		}
		p := listedPane{
			Host: host, Untrusted: host != "", Session: name,
			Workspace: w.Workspace, WorkspaceNm: wsNames[w.Workspace],
			WindowID: w.ID, Name: w.Display, Title: w.Title, Command: w.Command, Cwd: w.Cwd,
			Focused: w.ID == list.Focused, AgentState: state,
		}
		if text > 0 {
			p.Text, p.TextError = listPaneText(client, name, w.ID, text)
		}
		out = append(out, p)
	}
	return out, nil
}

// listWorkspaceNames is a session's named workspaces, and the session's
// name as its daemon reports it.
func listWorkspaceNames(client *session.VerbClient, name string) (map[int]string, string) {
	out := map[int]string{}
	raw, err := client.CallWithTimeout("session-info", map[string]any{"session": name}, listCallTimeout)
	if err != nil {
		return out, ""
	}
	var info struct {
		Name  string            `json:"session_name"`
		Names map[string]string `json:"workspace_names"`
	}
	if json.Unmarshal(raw, &info) != nil {
		return out, ""
	}
	for k, v := range info.Names {
		if n, err := strconv.Atoi(k); err == nil {
			out[n] = v
		}
	}
	return out, info.Name
}

// listPaneText reads a pane's last lines through capture-pane, or says why it
// could not.
func listPaneText(client *session.VerbClient, sessionName, window string, lines int) ([]string, string) {
	raw, err := client.CallWithTimeout("capture-pane", map[string]any{
		"session": sessionName, "window": window, "source": "recent", "lines": lines,
	}, listCallTimeout)
	if err != nil {
		return nil, explainVerbError("capture-pane", err).Error()
	}
	var res struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return nil, "could not read the capture"
	}
	out := strings.Split(ansi.Strip(res.Content), "\n")
	for i, l := range out {
		out[i] = strings.TrimRight(l, " \r")
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) > lines {
		out = out[len(out)-lines:]
	}
	return out, ""
}

// printListedPanes writes the listing as JSON or as a table, with each
// pane's text under its row when --text was given.
func printListedPanes(panes []listedPane, errs []listError, text int, jsonOutput bool) error {
	if jsonOutput {
		if panes == nil {
			panes = []listedPane{}
		}
		out := map[string]any{"success": true, "total": len(panes), "panes": panes}
		if len(errs) > 0 {
			out["errors"] = errs
		}
		outputJSON(out)
		return nil
	}
	for _, e := range errs {
		where := e.Session
		if e.Host != "" {
			where = e.Host + ":" + where
		}
		if where != "" {
			where += ": "
		}
		fmt.Printf("%s%s\n", where, e.Error)
	}
	if len(panes) == 0 {
		fmt.Println("No panes.")
		return nil
	}
	for _, p := range panes {
		where := p.Session
		if p.Host != "" {
			where = p.Host + ":" + where
		}
		ws := strconv.Itoa(p.Workspace)
		if p.WorkspaceNm != "" {
			ws += " " + p.WorkspaceNm
		}
		mark := " "
		if p.Focused {
			mark = "*"
		}
		fmt.Printf("%s %-20s %-10s %-24s %-12s %s\n", mark, where, ws, p.Name, p.Command, p.Cwd)
		if text > 0 {
			if p.TextError != "" {
				fmt.Printf("    (%s)\n", p.TextError)
			}
			for _, l := range p.Text {
				fmt.Printf("    │ %s\n", l)
			}
		}
	}
	return nil
}
