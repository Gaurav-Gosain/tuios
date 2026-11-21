package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agentproto"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const agentProtoHoldMax = 305 * time.Second

type agentProtoOptions struct {
	protocol   string
	harness    string
	cwd        string
	acpVersion int
	resume     string
}

func newAgentProtoCommand() *cobra.Command {
	var o agentProtoOptions
	cmd := &cobra.Command{
		Use:   "agent-proto --protocol acp|codex -- <agent> [args...]",
		Short: "Run an agent headless over a structured protocol, as a pane",
		Long: `Run a coding agent headless, over ACP (the Agent Client Protocol) or the
Codex app-server protocol, and show it as a transcript with a prompt line.

This is what start-agent --protocol runs in the pane it opens. The agent is
started with pipes, not a terminal: its turns, tool calls, plans and diffs
arrive as messages and are written to the pane as plain text, with every
escape sequence the agent sends removed. Type a prompt and press Enter to send
it. ask-agent and send-text type into the same line. Ctrl+C cancels a turn,
and Ctrl+D on an empty line quits.

Inside a tuios pane it reports the agent's state for its own pane, the way a
hook does: idle once the conversation is open, working during a turn, done or
errored when it ends, and needs_input when the agent asks permission. A
permission is answered in the pane with a number key, or from the Inbox when
the Inbox can show the whole request on one line (a command, not a diff).
The first answer wins. Outside tuios it reports nothing and works the
same.

The agent is offered no file system and no terminal: it reads, writes and runs
things itself, under its own sandbox and approval settings, as it would in its
own TUI.

--protocol acp speaks ACP (the Agent Client Protocol) to any agent that
implements it, for example "opencode acp". It asks for ACP version 1 unless
--acp-version 2 or the agents.acp_version option says 2. Version 2 is a draft
of the spec: an agent that speaks only version 1 answers with 1, or refuses,
and the pane then speaks version 1, starting the agent again when it has to.
The pane's first line names the version it speaks. --resume reattaches to a
conversation the agent kept, by its session id, instead of opening a new one.

--protocol codex speaks the Codex app-server protocol. start-agent adds the
app-server subcommand for you.`,
		Example: `  # Through start-agent, which is how it is meant to be run
  tuios start-agent --protocol acp 'opencode acp' --name helper
  tuios start-agent --protocol codex codex --prompt 'Fix the failing test.'

  # By hand, in any terminal
  tuios agent-proto --protocol acp -- opencode acp`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			startPprofServer()
			if err := startCPUProfile(); err != nil {
				return err
			}
			defer stopCPUProfile()
			code := runAgentProto(o, args)
			if code != 0 {
				return &statusError{code: code}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&o.protocol, "protocol", "", "The protocol the agent speaks: acp or codex")
	cmd.Flags().StringVar(&o.harness, "harness", "", "The harness id to report the pane's state under (default: acp or codex)")
	cmd.Flags().StringVar(&o.cwd, "cwd", "", "The directory the conversation opens in (default: the current directory)")
	cmd.Flags().IntVar(&o.acpVersion, "acp-version", 0, "The ACP version to ask for: 1, or 2, a draft of the spec (default 1)")
	cmd.Flags().StringVar(&o.resume, "resume", "", "Reattach to the ACP session with this id instead of opening a new one")
	_ = cmd.MarkFlagRequired("protocol")
	return cmd
}

func runAgentProto(o agentProtoOptions, argv []string) int {
	if err := agentproto.CheckProtocol(o.protocol); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cwd := o.cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	harness := o.harness
	if harness == "" {
		harness = o.protocol
	}

	stdinFd := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFd) {
		if old, err := term.MakeRaw(stdinFd); err == nil {
			defer func() { _ = term.Restore(stdinFd, old) }()
		}
	}
	_, _ = io.WriteString(os.Stdout, "\x1b[?2004h")
	defer func() { _, _ = io.WriteString(os.Stdout, "\x1b[?2004l\r\n") }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	if o.acpVersion != 0 && o.acpVersion != 1 && o.acpVersion != 2 {
		fmt.Fprintf(os.Stderr, "--acp-version %d: use 1 or 2\n", o.acpVersion)
		return 2
	}
	if o.resume != "" && o.protocol != agentproto.ProtocolACP {
		fmt.Fprintln(os.Stderr, "--resume is for --protocol acp")
		return 2
	}
	agentCmd := &agentproto.Command{Argv: argv, Dir: cwd}
	defer agentCmd.Stop()

	s := &agentproto.Session{
		Events: agentproto.NewEvents(),
		In:     os.Stdin,
		Out:    os.Stdout,
		Width: func() int {
			w, _, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				return 0
			}
			return w
		},
		Header: o.protocol + ": " + strings.Join(argv, " "),
		Cwd:    cwd,
		Stderr: agentCmd.Stderr,
	}
	agent, err := agentproto.NewAgent(o.protocol, agentCmd, agentproto.ACPOptions{Version: version, MaxVersion: o.acpVersion, Resume: o.resume}, s.Emit)
	if err != nil {
		fmt.Fprintf(os.Stdout, "could not start %s: %v\r\n", strings.Join(argv, " "), err)
		return 1
	}
	s.Agent = agent
	if window := os.Getenv("TUIOS_PANE_ID"); window != "" {
		r := &paneReporter{
			session: os.Getenv("TUIOS_SESSION"),
			window:  window,
			harness: harness,
			dial:    func() (*session.VerbClient, error) { return session.DialVerbClientAs(version) },
		}
		defer r.close()
		s.Reporter = r
	}
	return s.Run(ctx)
}

type paneReporter struct {
	session, window, harness string
	dial                     func() (*session.VerbClient, error)

	mu     sync.Mutex
	client *session.VerbClient

	feedMu     sync.Mutex
	feedClient *session.VerbClient
	activity   activitySupport
}

type activitySupport int

const (
	activityUnknown activitySupport = iota
	activityYes
	activityNo
)

const protoMetaSource = "protocol"

const activityIfState = "none"

func (r *paneReporter) close() {
	r.mu.Lock()
	if r.client != nil {
		_ = r.client.Close()
		r.client = nil
	}
	r.mu.Unlock()
	r.feedMu.Lock()
	if r.feedClient != nil {
		_ = r.feedClient.Close()
		r.feedClient = nil
	}
	r.feedMu.Unlock()
}

func (r *paneReporter) Report(ctx context.Context, state, kind, message, ifState string) error {
	params := map[string]any{"session": r.session, "window": r.window, "state": state, "harness": r.harness}
	for k, v := range map[string]string{"kind": kind, "message": message, "if_state": ifState} {
		if v != "" {
			params[k] = v
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		c, err := r.dial()
		if err != nil {
			return err
		}
		r.client = c
	}
	timeout := time.Until(deadlineOr(ctx, 2*time.Second))
	if _, err := r.client.CallWithTimeout("set-agent-state", params, timeout); err != nil {
		_ = r.client.Close()
		r.client = nil
		return err
	}
	return nil
}

func (r *paneReporter) feedCall(ctx context.Context, verb string, params map[string]any) (json.RawMessage, error) {
	if r.feedClient == nil {
		c, err := r.dial()
		if err != nil {
			return nil, err
		}
		r.feedClient = c
	}
	timeout := time.Until(deadlineOr(ctx, 2*time.Second))
	raw, err := r.feedClient.CallWithTimeout(verb, params, timeout)
	if err != nil {
		if _, ok := errors.AsType[*session.VerbCallError](err); !ok {
			_ = r.feedClient.Close()
			r.feedClient = nil
		}
		return nil, err
	}
	return raw, nil
}

func (r *paneReporter) SetMeta(ctx context.Context, tokens map[string]string) error {
	r.feedMu.Lock()
	defer r.feedMu.Unlock()
	values := make(map[string]any, len(tokens))
	for k, v := range tokens {
		if v == "" {
			values[k] = nil
			continue
		}
		values[k] = v
	}
	_, err := r.feedCall(ctx, "set-agent-meta", map[string]any{
		"session": r.session,
		"window":  r.window,
		"tokens":  values,
		"source":  protoMetaSource,
	})
	return err
}

func (r *paneReporter) ReportActivity(ctx context.Context, a agentproto.Activity) error {
	r.feedMu.Lock()
	defer r.feedMu.Unlock()
	if r.activity == activityUnknown {
		raw, err := r.feedCall(ctx, "list-verbs", map[string]any{"verb": "set-agent-state"})
		if _, ok := errors.AsType[*session.VerbCallError](err); ok {
			r.activity = activityNo
			return nil
		}
		if err != nil {
			return err
		}
		r.activity = activityNo
		if listsParam(raw, "set-agent-state", "activity") {
			r.activity = activityYes
		}
	}
	if r.activity != activityYes {
		return nil
	}
	act := map[string]any{"event": a.Event}
	for k, v := range map[string]string{"tool": a.Tool, "target": a.Target, "text": a.Text} {
		if v != "" {
			act[k] = v
		}
	}
	if a.OK != nil {
		act["ok"] = *a.OK
	}
	state := a.State
	if state == "" {
		state = "working"
	}
	_, err := r.feedCall(ctx, "set-agent-state", map[string]any{
		"session":  r.session,
		"window":   r.window,
		"state":    state,
		"if_state": activityIfState,
		"harness":  r.harness,
		"activity": act,
	})
	return err
}

func listsParam(raw json.RawMessage, verb, param string) bool {
	var res struct {
		Verbs []struct {
			Verb   string `json:"verb"`
			Params []struct {
				Name string `json:"name"`
			} `json:"params"`
		} `json:"verbs"`
	}
	if jsonx.Unmarshal(raw, &res) != nil {
		return false
	}
	for _, v := range res.Verbs {
		if v.Verb != verb {
			continue
		}
		for _, p := range v.Params {
			if p.Name == param {
				return true
			}
		}
	}
	return false
}

func (r *paneReporter) Hold(ctx context.Context, line string, options []string) (string, string, error) {
	client, err := r.dial()
	if err != nil {
		return "", "", err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		_ = client.Close()
	}()
	raw, err := client.CallWithTimeout("request-approval", map[string]any{
		"session": r.session,
		"window":  r.window,
		"harness": r.harness,
		"options": options,
		"summary": line,
	}, agentProtoHoldMax)
	if err != nil {
		return "", "", err
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	var res struct {
		Decision   string `json:"decision"`
		AnsweredBy string `json:"answered_by"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return "", "", err
	}
	if res.Decision == "" {
		return "", "", errors.New("no decision")
	}
	return res.Decision, res.AnsweredBy, nil
}

func deadlineOr(ctx context.Context, d time.Duration) time.Time {
	if dl, ok := ctx.Deadline(); ok {
		return dl
	}
	return time.Now().Add(d)
}
