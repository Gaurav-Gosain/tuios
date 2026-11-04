package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/fang"
	"github.com/Gaurav-Gosain/tuios/internal/i18n"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// This file is the CLI's shared vocabulary for failure. Every user-reachable
// error the tuios command prints should answer three questions in order:
//
//	what failed, the most likely cause, and the exact command that fixes it.
//
// diagnosticError is the shape that enforces it, and the helpers below build one
// for each degraded state the CLI can reach.

// diagnosticError is an error whose message is structured as what/why/fix. It
// renders as three lines so a user reading a terminal sees the fix without
// parsing prose.
type diagnosticError struct {
	// What failed, in the imperative past: "Session 'work' was not found."
	What string
	// Why it most likely happened.
	Cause string
	// Fix is the exact command to run, copy-pasteable.
	Fix string
	// Extra holds optional detail lines shown between the cause and the fix,
	// such as the list of session names that do exist.
	Extra []string
	// Status is the exit status this failure should produce. Zero means the
	// generic 1.
	Status int
	// Err is the underlying error, preserved for errors.Is/As.
	Err error
}

func (e *diagnosticError) Error() string {
	var b strings.Builder
	b.WriteString(e.What)
	if e.Cause != "" {
		b.WriteString("\n")
		b.WriteString(i18n.T("cli.label.cause"))
		b.WriteString(e.Cause)
	}
	for _, line := range e.Extra {
		b.WriteString("\n")
		b.WriteString(line)
	}
	if e.Fix != "" {
		b.WriteString("\n")
		b.WriteString(i18n.T("cli.label.fix"))
		b.WriteString(e.Fix)
	}
	return b.String()
}

func (e *diagnosticError) Unwrap() error { return e.Err }

// ExitStatus reports the process status this failure should produce, defaulting
// to the generic 1.
func (e *diagnosticError) ExitStatus() int {
	if e.Status == 0 {
		return 1
	}
	return e.Status
}

// noDaemonStatus is the exit status of a command that needed a daemon and found
// none. It is distinct from 1 so a script can tell "no daemon" from "the daemon
// answered and had nothing" (0) and from a command that genuinely failed (1).
const noDaemonStatus = 3

// statusError carries an exit status and nothing to print. It is for a command
// that has already said everything it has to say on stdout and only needs the
// status to differ.
type statusError struct{ code int }

func (e *statusError) Error() string   { return "" }
func (e *statusError) ExitStatus() int { return e.code }
func (e *statusError) Unwrap() error   { return nil }

// exitStatus renders a failed command, when it has anything to render, and
// returns the status the process should exit with.
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

// diagnosticErrorHandler renders errors for the CLI. A diagnostic error is
// printed with its line structure intact, because the whole value of the
// what/why/fix layout is lost when it is reflowed into one paragraph; every
// other error falls through to fang's styled default.
//
// Off a terminal every error is printed as its bare text, with no header, box
// or wrapping, so piped output and CI logs stay parseable and a path in the
// message stays on one line.
func diagnosticErrorHandler(w io.Writer, styles fang.Styles, err error) {
	// An agent command while the agent features are off prints the one line
	// that says so, however the command wrapped the daemon's answer.
	err = agentsOffAnswer(err)
	if !isTerminalWriter(w) {
		_, _ = fmt.Fprintln(w, err.Error())
		return
	}

	var diag *diagnosticError
	var mismatch *session.ProtocolMismatchError
	if !errors.As(err, &diag) && !errors.As(err, &mismatch) {
		fang.DefaultErrorHandler(w, styles, err)
		return
	}

	_, _ = fmt.Fprintln(w, styles.ErrorHeader.String())
	for line := range strings.SplitSeq(err.Error(), "\n") {
		_, _ = fmt.Fprintln(w, fang.RenderErrorText(styles.ErrorText.UnsetTransform(), line))
	}
	_, _ = fmt.Fprintln(w)
}

// isTerminalWriter reports whether w ends at a terminal. The CLI hands the
// renderer a colorprofile.Writer, which strips colour for a pipe but does not
// expose the file under it, so it is unwrapped first. Checking the wrapper
// itself found no file descriptor and styled every piped error as though it
// were going to a terminal.
func isTerminalWriter(w io.Writer) bool {
	if cw, ok := w.(*colorprofile.Writer); ok {
		w = cw.Forward
	}
	f, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(f.Fd()))
}

// errorStyles builds the styles the error renderer needs without asking the
// terminal anything.
//
// This exists because of how long fang takes to answer that question itself.
// fang builds its styles only on the error path, and to pick colors it calls
// lipgloss.HasDarkBackground, which writes an OSC 11 query and waits up to two
// seconds for a reply, against stdin and then stdout: four seconds when nothing
// answers. Terminals that do not implement the query, multiplexers that swallow
// it, and a stdin that has just been handed back by a torn-down TUI all fail to
// answer, so `tuios attach` would print its diagnostic and then sit there for
// four seconds after its interface was already gone, which reads as a hung
// client.
//
// Fixed ANSI red-on-black needs no query and is legible on a light or a dark
// background, so nothing is lost by not asking.
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
			SetString(i18n.T("cli.error.header")),
	}
}

// reportCommandError prints a failed command's error and reports whether there
// was one, so the caller can pick an exit status.
func reportCommandError(err error) bool {
	if err == nil {
		return false
	}
	diagnosticErrorHandler(colorprofile.NewWriter(os.Stderr, os.Environ()), errorStyles(), err)
	return true
}

// interceptErrors routes every command's failure through reportCommandError
// instead of returning it to fang, whose renderer is the slow path errorStyles
// describes. The error is stashed and the command reports success, so fang sees
// nothing to render; main prints it and picks the exit status afterwards.
//
// This covers failures from running a command, which is every failure the
// session and attach paths can produce. A malformed flag is rejected by cobra
// before any RunE runs and still goes through fang.
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
			// The error is already explained; usage would bury it.
			c.SilenceUsage = true
		}
		return nil
	}
}

// requireDaemon returns a diagnostic error when no daemon is reachable, naming
// which of the several "not running" states this actually is.
func requireDaemon() error {
	d := session.DiagnoseDaemon()
	if d.Running() {
		return nil
	}
	return &diagnosticError{What: d.Explain(), Err: d.Err, Status: noDaemonStatus}
}

// dialVerb connects to the daemon for a JSON verb-protocol call. Every failure
// it can produce is explained: the daemon being absent, a stale or unreachable
// socket, and an old daemon left running across an upgrade.
//
// It dials first and diagnoses only on failure. explainDialError runs the same
// DiagnoseDaemon a probe beforehand would, so the message and exit status are
// the same, and a command against a running daemon does not pay for a probe
// connection the daemon has to accept and drop.
func dialVerb() (*session.VerbClient, error) {
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		return nil, explainDialError(err)
	}
	return client, nil
}

// explainDialError turns a verb-client dial failure into an actionable message.
// A protocol mismatch already carries its own full explanation, so it is passed
// through untouched; anything else is a socket problem, re-diagnosed so the user
// is told which one.
func explainDialError(err error) error {
	if mismatch, ok := errors.AsType[*session.ProtocolMismatchError](err); ok {
		return mismatch
	}

	if errors.Is(err, session.ErrTooManyConnections) {
		return &diagnosticError{
			What:  i18n.T("cli.daemon.too_many.what"),
			Cause: i18n.T("cli.daemon.too_many.cause"),
			Fix:   i18n.T("cli.daemon.too_many.fix"),
			Err:   err,
		}
	}

	d := session.DiagnoseDaemon()
	if !d.Running() {
		// The daemon disappeared between the check and the dial, which is
		// itself worth reporting accurately.
		return &diagnosticError{What: d.Explain(), Err: err, Status: noDaemonStatus}
	}
	return &diagnosticError{
		What:  i18n.T("cli.daemon.dial.what", err),
		Cause: i18n.T("cli.daemon.dial.cause"),
		Fix:   i18n.T("cli.daemon.dial.fix"),
		Err:   err,
	}
}

// explainVerbError renders a failed verb call for a human, folding in the
// structured hint the daemon attached. The daemon already computed the remedy;
// the CLI's job is to show it rather than invent a second, possibly different
// one.
func explainVerbError(verb string, err error) error {
	var callErr *session.VerbCallError
	if !errors.As(err, &callErr) {
		return err
	}
	// The agent features are off. The one line says what to change, and
	// nothing about the daemon helps more.
	if callErr.Code == session.ErrVerbAgentsDisabled {
		return &agentsOffError{call: callErr}
	}
	if current, ok := session.RenamedSessionTarget(err); ok {
		return &diagnosticError{
			What:  strings.ToUpper(callErr.Message[:1]) + callErr.Message[1:] + ".",
			Cause: i18n.T("cli.verb.renamed.cause", verb),
			Fix:   i18n.T("cli.verb.renamed.fix", current),
			Err:   err,
		}
	}
	if stale := session.StaleDaemonError(verb, err); stale != nil {
		return &diagnosticError{
			What:  i18n.T("cli.verb.unknown", verb),
			Cause: i18n.T("cli.verb.unknown.cause"),
			Fix:   i18n.T("cli.verb.unknown.fix"),
			Err:   err,
		}
	}

	d := &diagnosticError{
		What: i18n.T("cli.verb.failed", verb, strings.TrimSuffix(callErr.Message, ".")),
		Err:  err,
	}

	hint := callErr.Hint
	if hint == nil {
		d.Cause = i18n.T("cli.verb.no_hint.cause", callErr.Code)
		return d
	}

	if hint.DidYouMean != "" {
		d.Extra = append(d.Extra, i18n.T("cli.common.did_you_mean", hint.DidYouMean))
	}
	if len(hint.Accepted) > 0 {
		d.Extra = append(d.Extra, i18n.T("cli.common.accepted",
			cmp.Or(hint.Param, i18n.T("cli.common.this_param")), strings.Join(hint.Accepted, ", ")))
	}
	if len(hint.Available) > 0 {
		d.Extra = append(d.Extra, i18n.T("cli.common.available", strings.Join(truncateList(hint.Available, 12), ", ")))
	}
	if hint.Detail != "" {
		d.Cause = hint.Detail
	} else {
		d.Cause = i18n.T("cli.verb.no_hint.cause", callErr.Code)
	}

	switch {
	case hint.Command != "":
		d.Fix = i18n.T("cli.verb.run_fix", hint.Command)
	case hint.Verb != "":
		d.Fix = i18n.T("cli.verb.call_fix", hint.Verb)
	}
	return d
}

// agentsOffError is the answer to an agent command while the agent features
// are off: the one line that says what to change, with the daemon's answer
// under it, so --json can still name the code.
type agentsOffError struct {
	call *session.VerbCallError
}

func (e *agentsOffError) Error() string { return config.AgentsOffMessage }
func (e *agentsOffError) Unwrap() error { return e.call }

// agentsOffAnswer is err as the CLI prints it: an agent command while the
// agent features are off prints the one line that says so, however the
// command wrapped the answer. Any other error is returned as it is.
func agentsOffAnswer(err error) error {
	if here, ok := errors.AsType[*session.AgentsOffHereError](err); ok {
		return here
	}
	if call, ok := errors.AsType[*session.VerbCallError](err); ok && call.Code == session.ErrVerbAgentsDisabled {
		return &agentsOffError{call: call}
	}
	return err
}

// refuseAgentCallHere is session.HostCallGuard for the CLI: an agent call to
// another machine is refused while this machine's config has the agent
// features off. The config is read only for an agent call.
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

// truncateList caps a list for display so a hundred window ids do not bury the
// fix line.
func truncateList(items []string, limit int) []string {
	if len(items) <= limit {
		return items
	}
	out := make([]string, 0, limit+1)
	out = append(out, items[:limit]...)
	return append(out, i18n.T("cli.common.and_more", len(items)-limit))
}

// explainMissingSession builds the error for a session name that does not
// resolve, listing the names that do exist and suggesting the closest one. It is
// used by commands that resolve a session before opening a verb connection, so
// the user never sees a bare "not found".
func explainMissingSession(name string, available []string) error {
	e := &diagnosticError{
		What: i18n.T("cli.session.missing.what", name),
	}

	// A daemon started with --no-restore holds nothing while the sessions are
	// still on disk, so the name can be real and absent at the same time. That
	// is the one case where the answer is neither "create it" nor "you typed it
	// wrong".
	if info, ok := savedSession(name); ok {
		e.Cause = i18n.T("cli.session.missing.cause.not_restored")
		e.Extra = append(e.Extra, i18n.T("cli.session.missing.saved", i18n.Count(info.WindowCount, i18n.T("noun.window"))))
		e.Fix = i18n.T("cli.session.missing.fix.resurrect", name)
		return e
	}

	sorted := append([]string(nil), available...)
	sort.Strings(sorted)

	switch {
	case len(sorted) == 0:
		e.Cause = i18n.T("cli.session.missing.cause.no_sessions")
		e.Fix = i18n.T("cli.session.missing.fix.create", name)
	default:
		e.Cause = i18n.T("cli.session.missing.cause.no_match")
		if closest := session.ClosestMatch(name, sorted); closest != "" {
			e.Extra = append(e.Extra, i18n.T("cli.common.did_you_mean", closest))
		}
		e.Extra = append(e.Extra, i18n.T("cli.common.sessions", strings.Join(truncateList(sorted, 12), ", ")))
		e.Fix = i18n.T("cli.session.missing.fix.list_or_create", name)
	}
	return e
}

// savedSession looks up one name in the saved state on disk.
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

// Terminal capability checks. These run before the TUI takes over the screen,
// because a TUI that cannot render is far harder to diagnose from inside itself.

// minTerminalWidth and minTerminalHeight are the smallest terminal the window
// manager can lay out: below this the dockbar, borders, and a usable pane no
// longer fit, and the UI is unreadable rather than merely cramped.
const (
	minTerminalWidth  = 40
	minTerminalHeight = 12
)

// checkTerminal verifies the terminal can host the TUI, returning a diagnostic
// error when it cannot. It checks the three things that produce an unusable or
// blank screen: not being a terminal at all, being too small, and a TERM value
// with no capabilities to render with.
func checkTerminal() error {
	fd := int(os.Stdout.Fd())

	if !term.IsTerminal(fd) {
		return &diagnosticError{
			What:  i18n.T("cli.term.not_terminal.what"),
			Cause: i18n.T("cli.term.not_terminal.cause"),
			Fix:   i18n.T("cli.term.not_terminal.fix"),
		}
	}

	if err := checkTerminalSize(fd); err != nil {
		return err
	}
	return checkTerminalCapabilities(os.Getenv("TERM"))
}

// checkTerminalSize rejects a terminal too small to lay out a session.
func checkTerminalSize(fd int) error {
	width, height, err := term.GetSize(fd)
	if err != nil {
		// The size is unknowable; that is not itself fatal, and the TUI copes
		// with a default. Do not block the user on it.
		return nil
	}
	// A zero dimension means the terminal has no window size set (a pty opened
	// by script(1), some CI runners, a detached pty). That is unknown, not
	// small: the renderer sizes itself from the first resize event instead, so
	// blocking here would reject setups that go on to work.
	if width == 0 || height == 0 {
		return nil
	}
	if width >= minTerminalWidth && height >= minTerminalHeight {
		return nil
	}
	return &diagnosticError{
		What: i18n.T("cli.term.too_small.what",
			width, height, minTerminalWidth, minTerminalHeight),
		Cause: i18n.T("cli.term.too_small.cause"),
		Fix:   i18n.T("cli.term.too_small.fix"),
	}
}

// checkTerminalCapabilities rejects a TERM value that cannot express the cursor
// movement and styling the renderer needs. Only "dumb" and an unset TERM are
// genuinely unusable; everything else is allowed through, because guessing at
// terminfo coverage would reject working terminals.
//
// An unset TERM says nothing on Windows: Windows Terminal and conhost never set
// it. Whether the console takes escape sequences is settled when Bubble Tea
// asks for ENABLE_VIRTUAL_TERMINAL_PROCESSING, and it returns an error if the
// console refuses, so asking here too would add a second check of the same
// thing. A TERM set to "dumb" is still somebody's explicit answer.
func checkTerminalCapabilities(termEnv string) error {
	switch termEnv {
	case "":
		if runtime.GOOS == "windows" {
			return nil
		}
		return &diagnosticError{
			What:  i18n.T("cli.term.unset.what"),
			Cause: i18n.T("cli.term.unset.cause"),
			Fix:   i18n.T("cli.term.unset.fix"),
		}
	case "dumb":
		return &diagnosticError{
			What:  i18n.T("cli.term.dumb.what"),
			Cause: i18n.T("cli.term.dumb.cause"),
			Fix:   i18n.T("cli.term.dumb.fix"),
		}
	}
	return nil
}

// hostTerminalSize returns the size of the terminal this process is attached
// to, or the conventional 80x24 when there is no answer.
//
// It exists so an attaching client can tell the daemon its real size straight
// away. The session's size is the minimum over its attached clients, so a
// client that names a placeholder does not merely mis-describe itself: it
// shrinks the session for everyone else until it corrects itself.
//
// A zero dimension is treated the same as an error. It means the pty has no
// window size set (script(1), some CI runners), which is unknown rather than
// tiny, and passing a zero through would make the daemon read this client as
// having no size at all.
func hostTerminalSize() (width, height int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}
