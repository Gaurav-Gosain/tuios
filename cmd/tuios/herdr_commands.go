package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// herdr's report commands, answered by tuios.
//
// A pane told about tuios's herdr protocol socket also gets HERDR_BIN_PATH
// naming this binary. herdr's guide for agent authors ("Add Herdr support to
// your agent") has an agent report through "$HERDR_BIN_PATH" pane
// report-agent and its siblings rather than the socket, so these commands take
// herdr's arguments and send the same requests to HERDR_SOCKET_PATH that the
// socket reporters send. They are hidden: they are herdr's interface, kept for
// herdr's reporters, and a person uses set-agent-state and set-agent-meta.
//
// Anything else herdr's CLI does is not here, and `pane` with any other
// subcommand fails as an unknown command.

// herdrCLITimeout bounds one request. A reporter must never be held up.
const herdrCLITimeout = 2 * time.Second

func newHerdrPaneCommand() *cobra.Command {
	pane := &cobra.Command{
		Use:    "pane",
		Short:  "herdr's pane report commands, for agents that report to herdr",
		Hidden: true,
	}
	pane.AddCommand(newHerdrReportAgentCommand("report-agent"), newHerdrReportAgentCommand("report-agent-session"),
		newHerdrReleaseAgentCommand(), newHerdrReportMetadataCommand())
	return pane
}

func newHerdrNotificationCommand() *cobra.Command {
	n := &cobra.Command{
		Use:    "notification",
		Short:  "herdr's notification command, for agents that report to herdr",
		Hidden: true,
	}
	var body, position, sound string
	show := &cobra.Command{
		Use:   "show <title>",
		Short: "Send a notification from this pane",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"title": args[0]}
			if body != "" {
				params["body"] = body
			}
			return herdrSend(cmd, "notification.show", params)
		},
	}
	show.Flags().StringVar(&body, "body", "", "The notification text")
	show.Flags().StringVar(&position, "position", "", "Accepted for herdr and not used")
	show.Flags().StringVar(&sound, "sound", "", "Accepted for herdr and not used")
	n.AddCommand(show)
	return n
}

// newHerdrReportAgentCommand is pane report-agent or pane
// report-agent-session: the same arguments, less the state for the second.
func newHerdrReportAgentCommand(name string) *cobra.Command {
	var source, agent, state, message, sessionID, sessionPath, startSource string
	var seq uint64
	withState := name == "report-agent"
	cmd := &cobra.Command{
		Use:   name + " <pane_id> --source ID --agent LABEL [flags] [-- resume command...]",
		Short: "Report the agent in this pane, as herdr's " + name,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"pane_id": args[0], "source": source, "agent": agent}
			if withState {
				params["state"] = state
				if message != "" {
					params["message"] = message
				}
			}
			if cmd.Flags().Changed("seq") {
				params["seq"] = seq
			}
			if sessionID != "" {
				params["agent_session_id"] = sessionID
			}
			if at := cmd.ArgsLenAtDash(); at >= 0 && at < len(args) {
				params["resume_argv"] = args[at:]
			}
			return herdrSend(cmd, "pane."+strings.ReplaceAll(name, "-", "_"), params)
		},
	}
	f := cmd.Flags()
	f.StringVar(&source, "source", "", "The reporter's stable id")
	f.StringVar(&agent, "agent", "", "The agent's name")
	if withState {
		f.StringVar(&state, "state", "", "idle, working, blocked or unknown")
		f.StringVar(&message, "message", "", "What a blocked agent waits for")
	}
	f.Uint64Var(&seq, "seq", 0, "A number that grows with every report from this source")
	f.StringVar(&sessionID, "agent-session-id", "", "The agent's own id for its conversation")
	f.StringVar(&sessionPath, "agent-session-path", "", "Accepted for herdr and not used")
	if !withState {
		f.StringVar(&startSource, "session-start-source", "", "Accepted for herdr and not used")
	}
	return cmd
}

func newHerdrReleaseAgentCommand() *cobra.Command {
	var source, agent string
	var seq uint64
	cmd := &cobra.Command{
		Use:   "release-agent <pane_id> --source ID --agent LABEL [--seq N]",
		Short: "Clear the agent in this pane, as herdr's release-agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"pane_id": args[0], "source": source, "agent": agent}
			if cmd.Flags().Changed("seq") {
				params["seq"] = seq
			}
			return herdrSend(cmd, "pane.release_agent", params)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "The reporter's stable id")
	cmd.Flags().StringVar(&agent, "agent", "", "The agent's name")
	cmd.Flags().Uint64Var(&seq, "seq", 0, "A number that grows with every report from this source")
	return cmd
}

func newHerdrReportMetadataCommand() *cobra.Command {
	var source, agent, appliesTo, title, displayAgent string
	var clearTitle, clearDisplay, clearLabels bool
	var tokens, clearTokens, labels []string
	var seq uint64
	var ttl int64
	cmd := &cobra.Command{
		Use:   "report-metadata <pane_id> --source ID [flags]",
		Short: "Report display text for the agent in this pane, as herdr's report-metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"pane_id": args[0], "source": source}
			if agent != "" {
				params["agent"] = agent
			}
			switch {
			case clearTitle:
				params["title"] = ""
			case cmd.Flags().Changed("title"):
				params["title"] = title
			}
			set := map[string]any{}
			for _, t := range tokens {
				k, v, ok := strings.Cut(t, "=")
				if !ok || k == "" {
					return fmt.Errorf("--token %q is not NAME=VALUE", t)
				}
				set[k] = v
			}
			for _, k := range clearTokens {
				set[k] = nil
			}
			if len(set) > 0 {
				params["tokens"] = set
			}
			if cmd.Flags().Changed("seq") {
				params["seq"] = seq
			}
			if ttl > 0 {
				params["ttl_ms"] = ttl
			}
			return herdrSend(cmd, "pane.report_metadata", params)
		},
	}
	f := cmd.Flags()
	f.StringVar(&source, "source", "", "The reporter's stable id")
	f.StringVar(&agent, "agent", "", "Accepted for herdr and not used")
	f.StringVar(&appliesTo, "applies-to-source", "", "Accepted for herdr and not used")
	f.StringVar(&title, "title", "", "The title token")
	f.BoolVar(&clearTitle, "clear-title", false, "Clear the title token")
	f.StringVar(&displayAgent, "display-agent", "", "Accepted for herdr and not used")
	f.BoolVar(&clearDisplay, "clear-display-agent", false, "Accepted for herdr and not used")
	f.StringArrayVar(&labels, "state-label", nil, "Accepted for herdr and not used")
	f.BoolVar(&clearLabels, "clear-state-labels", false, "Accepted for herdr and not used")
	f.StringArrayVar(&tokens, "token", nil, "Set one token, NAME=VALUE")
	f.StringArrayVar(&clearTokens, "clear-token", nil, "Clear one token")
	f.Uint64Var(&seq, "seq", 0, "A number that grows with every report from this source")
	f.Int64Var(&ttl, "ttl-ms", 0, "How long the tokens live, in milliseconds")
	return cmd
}

// herdrSocketPath is HERDR_SOCKET_PATH, or the herdr protocol socket beside
// this user's daemon socket when it is not set.
func herdrSocketPath() (string, error) {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p, nil
	}
	sock, err := session.GetSocketPath()
	if err != nil {
		return "", err
	}
	return session.HerdrSocketPath(sock), nil
}

// herdrSend sends one request the way a herdr socket reporter does, prints
// the answer, and fails when the answer is an error.
func herdrSend(cmd *cobra.Command, method string, params map[string]any) error {
	path, err := herdrSocketPath()
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", path, herdrCLITimeout)
	if err != nil {
		return fmt.Errorf("the herdr protocol socket %s does not answer: %w", path, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(herdrCLITimeout))
	req, err := json.Marshal(map[string]any{
		"id":     fmt.Sprintf("tuios-cli:%d", time.Now().UnixNano()),
		"method": method,
		"params": params,
	})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("the herdr protocol socket sent no answer: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(line)))
	var resp struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(line, &resp) == nil && resp.Error != nil {
		return errors.New(resp.Error.Code + ": " + resp.Error.Message)
	}
	return nil
}
