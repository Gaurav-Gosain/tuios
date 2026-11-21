package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/fang"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type diagnosticError struct {
	What   string
	Cause  string
	Fix    string
	Extra  []string
	Status int
	Err    error
}

func (e *diagnosticError) Error() string {
	var b strings.Builder
	b.WriteString(e.What)
	if e.Cause != "" {
		b.WriteString("\nMost likely cause: ")
		b.WriteString(e.Cause)
	}
	for _, line := range e.Extra {
		b.WriteString("\n")
		b.WriteString(line)
	}
	if e.Fix != "" {
		b.WriteString("\nFix: ")
		b.WriteString(e.Fix)
	}
	return b.String()
}

func (e *diagnosticError) Unwrap() error { return e.Err }

func (e *diagnosticError) ExitStatus() int {
	if e.Status == 0 {
		return 1
	}
	return e.Status
}

const noDaemonStatus = 3

type statusError struct{ code int }

func (e *statusError) Error() string   { return "" }
func (e *statusError) ExitStatus() int { return e.code }
func (e *statusError) Unwrap() error   { return nil }

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	err = agentsOffAnswer(err)
	code := 1
	var coded interface{ ExitStatus() int }
	if errors.As(err, &coded) {
		code = coded.ExitStatus()
	}
	if err.Error() != "" {
		reportCommandError(err)
	}
	return code
}

func diagnosticErrorHandler(w io.Writer, styles fang.Styles, err error) {
	err = agentsOffAnswer(err)
	if !isTerminalWriter(w) {
		_, _ = fmt.Fprintln(w, err.Error())
		return
	}

	var diag *diagnosticError
	var mismatch *session.ProtocolMismatchError
	var problem *hostsync.Problem
	if !errors.As(err, &diag) && !errors.As(err, &mismatch) && !errors.As(err, &problem) {
		fang.DefaultErrorHandler(w, styles, err)
		return
	}

	_, _ = fmt.Fprintln(w, styles.ErrorHeader.String())
	for line := range strings.SplitSeq(err.Error(), "\n") {
		_, _ = fmt.Fprintln(w, fang.RenderErrorText(styles.ErrorText.UnsetTransform(), line))
	}
	_, _ = fmt.Fprintln(w)
}

func isTerminalWriter(w io.Writer) bool {
	if cw, ok := w.(*colorprofile.Writer); ok {
		w = cw.Forward
	}
	f, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(f.Fd()))
}

func errorStyles() fang.Styles {
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}
	return fang.Styles{
		ErrorText: lipgloss.NewStyle().
			MarginLeft(2).
			Width(width - 4),
		ErrorHeader: lipgloss.NewStyle().
			Foreground(lipgloss.Black).
			Background(lipgloss.Red).
			Bold(true).
			Padding(0, 1).
			Margin(1).
			MarginLeft(2).
			SetString("ERROR"),
	}
}

func reportCommandError(err error) bool {
	if err == nil {
		return false
	}
	diagnosticErrorHandler(colorprofile.NewWriter(os.Stderr, os.Environ()), errorStyles(), err)
	return true
}

func interceptErrors(cmd *cobra.Command, stash *error) {
	for _, sub := range cmd.Commands() {
		interceptErrors(sub, stash)
	}
	run := cmd.RunE
	if run == nil {
		return
	}
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if err := run(c, args); err != nil {
			*stash = err
			c.SilenceUsage = true
		}
		return nil
	}
}

func requireDaemon() error {
	d := session.DiagnoseDaemon()
	if d.Running() {
		return nil
	}
	return &diagnosticError{What: d.Explain(), Err: d.Err, Status: noDaemonStatus}
}

func dialVerb() (*session.VerbClient, error) {
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		return nil, explainDialError(err)
	}
	return client, nil
}

func localVerb(verb string, params any, timeout time.Duration, jsonOutput bool) (json.RawMessage, error) {
	client, err := dialVerb()
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	var raw json.RawMessage
	if timeout > 0 {
		raw, err = client.CallWithTimeout(verb, params, timeout)
	} else {
		raw, err = client.Call(verb, params)
	}
	if err != nil {
		return nil, reportVerbError(explainVerbError(verb, err), jsonOutput)
	}
	return raw, nil
}

func localVerbInto(verb string, params any, timeout time.Duration, jsonOutput bool, out any) error {
	raw, err := localVerb(verb, params, timeout, jsonOutput)
	if err != nil {
		return err
	}
	if err := jsonx.Unmarshal(raw, out); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	return nil
}

func explainDialError(err error) error {
	if mismatch, ok := errors.AsType[*session.ProtocolMismatchError](err); ok {
		return mismatch
	}

	if errors.Is(err, session.ErrTooManyConnections) {
		return &diagnosticError{
			What:  "The tuios daemon has too many connections.",
			Cause: "other clients, commands or linked machines hold all of its connections.",
			Fix:   "close some clients or commands, then run this command again.",
			Err:   err,
		}
	}

	d := session.DiagnoseDaemon()
	if !d.Running() {
		return &diagnosticError{What: d.Explain(), Err: err, Status: noDaemonStatus}
	}
	return &diagnosticError{
		What:  fmt.Sprintf("Could not open a control connection to the TUIOS daemon: %v.", err),
		Cause: "the daemon is running but rejected or dropped the connection, which usually means it is shutting down or is a different build.",
		Fix:   "run 'tuios kill-server', then run this command again.",
		Err:   err,
	}
}

func explainVerbError(verb string, err error) error {
	var callErr *session.VerbCallError
	if !errors.As(err, &callErr) {
		return err
	}
	if callErr.Code == session.ErrVerbAgentsDisabled {
		return &agentsOffError{call: callErr}
	}
	if current, ok := session.RenamedSessionTarget(err); ok {
		return &diagnosticError{
			What:  strings.ToUpper(callErr.Message[:1]) + callErr.Message[1:] + ".",
			Cause: "the session has a new name, and " + verb + " takes the new name only.",
			Fix:   fmt.Sprintf("run the command again with %q.", current),
			Err:   err,
		}
	}
	if stale := session.StaleDaemonError(verb, err); stale != nil {
		return &diagnosticError{
			What:  fmt.Sprintf("The running daemon does not know %s.", verb),
			Cause: "the daemon is older than this tuios.",
			Fix:   "run 'tuios restart-server'. Saved sessions come back with new shells.",
			Err:   err,
		}
	}

	d := &diagnosticError{
		What: fmt.Sprintf("%s failed: %s.", verb, strings.TrimSuffix(callErr.Message, ".")),
		Err:  err,
	}

	hint := callErr.Hint
	if hint == nil {
		d.Cause = fmt.Sprintf("the daemon reported %s.", callErr.Code)
		return d
	}

	if hint.DidYouMean != "" {
		d.Extra = append(d.Extra, fmt.Sprintf("Did you mean %q?", hint.DidYouMean))
	}
	if len(hint.Accepted) > 0 {
		d.Extra = append(d.Extra, fmt.Sprintf("Accepted values for %s: %s.",
			cmp.Or(hint.Param, "this parameter"), strings.Join(hint.Accepted, ", ")))
	}
	if len(hint.Available) > 0 {
		d.Extra = append(d.Extra, "Available: "+strings.Join(truncateList(hint.Available, 12), ", ")+".")
	}
	if hint.Detail != "" {
		d.Cause = hint.Detail
	} else {
		d.Cause = fmt.Sprintf("the daemon reported %s.", callErr.Code)
	}

	switch {
	case hint.Command != "":
		d.Fix = "run '" + hint.Command + "'."
	case hint.Verb != "":
		d.Fix = "call the '" + hint.Verb + "' verb to see valid targets."
	}
	return d
}

type agentsOffError struct {
	call *session.VerbCallError
}

func (e *agentsOffError) Error() string { return config.AgentsOffMessage }
func (e *agentsOffError) Unwrap() error { return e.call }

func agentsOffAnswer(err error) error {
	if here, ok := errors.AsType[*session.AgentsOffHereError](err); ok {
		return here
	}
	if call, ok := errors.AsType[*session.VerbCallError](err); ok && call.Code == session.ErrVerbAgentsDisabled {
		return &agentsOffError{call: call}
	}
	return err
}

func refuseAgentCallHere(verb string, params any) error {
	if !session.AgentOnlyCall(verb, params) {
		return nil
	}
	cfg, err := config.LoadUserConfig()
	if err != nil || cfg == nil || cfg.Agents.On() {
		return nil
	}
	path, _ := config.GetConfigPath()
	return &session.AgentsOffHereError{Path: path}
}

func truncateList(items []string, limit int) []string {
	if len(items) <= limit {
		return items
	}
	out := make([]string, 0, limit+1)
	out = append(out, items[:limit]...)
	return append(out, fmt.Sprintf("and %d more", len(items)-limit))
}

func explainMissingSession(name string, available []string) error {
	e := &diagnosticError{
		What: fmt.Sprintf("Session %q was not found.", name),
	}

	if info, ok := savedSession(name); ok {
		e.Cause = "the daemon is running but has not restored it."
		e.Extra = append(e.Extra, fmt.Sprintf("It has saved state (%s) and can be brought back.", plural.Count(info.WindowCount, "window")))
		e.Fix = fmt.Sprintf("run 'tuios resurrect %s' to restore it and attach.", name)
		return e
	}

	sorted := append([]string(nil), available...)
	sort.Strings(sorted)

	switch {
	case len(sorted) == 0:
		e.Cause = "the daemon is running but holds no sessions."
		e.Fix = fmt.Sprintf("run 'tuios new %s' to create it, or 'tuios resurrect' to see saved sessions.", name)
	default:
		e.Cause = "the name does not match any live session."
		if closest := session.ClosestMatch(name, sorted); closest != "" {
			e.Extra = append(e.Extra, fmt.Sprintf("Did you mean %q?", closest))
		}
		e.Extra = append(e.Extra, "Sessions: "+strings.Join(truncateList(sorted, 12), ", ")+".")
		e.Fix = fmt.Sprintf("run 'tuios ls' to list sessions, or 'tuios new %s' to create this one.", name)
	}
	return e
}

func savedSession(name string) (session.ResurrectableInfo, bool) {
	infos, err := session.ListResurrectableInfos()
	if err != nil {
		return session.ResurrectableInfo{}, false
	}
	for _, info := range infos {
		if info.Name == name {
			return info, true
		}
	}
	return session.ResurrectableInfo{}, false
}

const (
	minTerminalWidth  = 40
	minTerminalHeight = 12
)

func checkTerminal() error {
	fd := int(os.Stdout.Fd())

	if !term.IsTerminal(fd) {
		return &diagnosticError{
			What:  "Standard output is not a terminal, so the TUIOS interface cannot be displayed.",
			Cause: "the command was run in a pipe, a redirect, or a non-interactive environment such as CI.",
			Fix:   "run 'tuios attach' from an interactive terminal. For scripted use, drive the session with 'tuios send-keys' and 'tuios capture-pane' instead.",
		}
	}

	if err := checkTerminalSize(fd); err != nil {
		return err
	}
	return checkTerminalCapabilities(os.Getenv("TERM"))
}

func checkTerminalSize(fd int) error {
	width, height, err := term.GetSize(fd)
	if err != nil {
		return nil
	}
	if width == 0 || height == 0 {
		return nil
	}
	if width >= minTerminalWidth && height >= minTerminalHeight {
		return nil
	}
	return &diagnosticError{
		What: fmt.Sprintf("The terminal is %dx%d, which is too small to render a TUIOS session (minimum %dx%d).",
			width, height, minTerminalWidth, minTerminalHeight),
		Cause: "the window, font size, or split pane leaves too few rows and columns for the dockbar and a usable window.",
		Fix:   "resize the terminal or reduce the font size, then run this command again.",
	}
}

func checkTerminalCapabilities(termEnv string) error {
	switch termEnv {
	case "":
		if runtime.GOOS == "windows" {
			return nil
		}
		return &diagnosticError{
			What:  "TERM is not set, so TUIOS cannot tell what the terminal can render.",
			Cause: "the shell was started without a terminal type, which happens over bare ssh commands, in some CI runners, and inside minimal containers.",
			Fix:   "set a terminal type, for example 'TERM=xterm-256color tuios attach'.",
		}
	case "dumb":
		return &diagnosticError{
			What:  `TERM is "dumb", which has no cursor movement or styling, so TUIOS cannot draw its interface.`,
			Cause: "the terminal reports no capabilities. Emacs shell buffers, some CI runners, and minimal containers do this.",
			Fix:   "set a capable terminal type, for example 'TERM=xterm-256color tuios attach'.",
		}
	}
	return nil
}

func hostTerminalSize() (width, height int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}
