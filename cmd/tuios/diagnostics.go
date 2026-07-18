package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/fang"
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
	// Err is the underlying error, preserved for errors.Is/As.
	Err error
}

func (e *diagnosticError) Error() string {
	var b strings.Builder
	b.WriteString(e.What)
	if e.Cause != "" {
		b.WriteString("\nMost likely cause: " + e.Cause)
	}
	for _, line := range e.Extra {
		b.WriteString("\n" + line)
	}
	if e.Fix != "" {
		b.WriteString("\nFix: " + e.Fix)
	}
	return b.String()
}

func (e *diagnosticError) Unwrap() error { return e.Err }

// diagnosticErrorHandler renders errors for the CLI. A diagnostic error is
// printed with its line structure intact, because the whole value of the
// what/why/fix layout is lost when it is reflowed into one paragraph; every
// other error falls through to fang's styled default.
func diagnosticErrorHandler(w io.Writer, styles fang.Styles, err error) {
	var diag *diagnosticError
	var mismatch *session.ProtocolMismatchError
	if !errors.As(err, &diag) && !errors.As(err, &mismatch) {
		fang.DefaultErrorHandler(w, styles, err)
		return
	}

	// Match fang's default behavior for a non-tty: no styling, no decoration,
	// so piped output and CI logs stay parseable.
	if f, ok := w.(interface{ Fd() uintptr }); ok && !term.IsTerminal(int(f.Fd())) {
		_, _ = fmt.Fprintln(w, err.Error())
		return
	}

	_, _ = fmt.Fprintln(w, styles.ErrorHeader.String())
	for _, line := range strings.Split(err.Error(), "\n") {
		_, _ = fmt.Fprintln(w, styles.ErrorText.UnsetTransform().Render(line))
	}
	_, _ = fmt.Fprintln(w)
}

// requireDaemon returns a diagnostic error when no daemon is reachable, naming
// which of the several "not running" states this actually is.
func requireDaemon() error {
	d := session.DiagnoseDaemon()
	if d.Running() {
		return nil
	}
	return &diagnosticError{What: d.Explain(), Err: d.Err}
}

// dialVerb connects to the daemon for a JSON verb-protocol call. Every failure
// it can produce is explained: the daemon being absent, a stale or unreachable
// socket, and an old daemon left running across an upgrade.
func dialVerb() (*session.VerbClient, error) {
	if err := requireDaemon(); err != nil {
		return nil, err
	}

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
	var mismatch *session.ProtocolMismatchError
	if errors.As(err, &mismatch) {
		return mismatch
	}

	d := session.DiagnoseDaemon()
	if !d.Running() {
		// The daemon disappeared between the check and the dial, which is
		// itself worth reporting accurately.
		return &diagnosticError{What: d.Explain(), Err: err}
	}
	return &diagnosticError{
		What:  fmt.Sprintf("Could not open a control connection to the TUIOS daemon: %v.", err),
		Cause: "the daemon is running but rejected or dropped the connection, which usually means it is shutting down or is a different build.",
		Fix:   "run 'tuios kill-server', then run this command again.",
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
			nonEmpty(hint.Param, "this parameter"), strings.Join(hint.Accepted, ", ")))
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

// nonEmpty returns s, or fallback when s is empty.
func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// truncateList caps a list for display so a hundred window ids do not bury the
// fix line.
func truncateList(items []string, limit int) []string {
	if len(items) <= limit {
		return items
	}
	out := make([]string, 0, limit+1)
	out = append(out, items[:limit]...)
	return append(out, fmt.Sprintf("and %d more", len(items)-limit))
}

// explainMissingSession builds the error for a session name that does not
// resolve, listing the names that do exist and suggesting the closest one. It is
// used by commands that resolve a session before opening a verb connection, so
// the user never sees a bare "not found".
func explainMissingSession(name string, available []string) error {
	e := &diagnosticError{
		What: fmt.Sprintf("Session %q was not found.", name),
	}

	sorted := append([]string(nil), available...)
	sort.Strings(sorted)

	switch {
	case len(sorted) == 0:
		e.Cause = "the daemon is running but holds no sessions."
		e.Fix = fmt.Sprintf("run 'tuios new %s' to create it, or 'tuios resurrect' to see saved sessions.", name)
	default:
		e.Cause = "the name does not match any live session."
		if closest := closestName(name, sorted); closest != "" {
			e.Extra = append(e.Extra, fmt.Sprintf("Did you mean %q?", closest))
		}
		e.Extra = append(e.Extra, "Sessions: "+strings.Join(truncateList(sorted, 12), ", ")+".")
		e.Fix = fmt.Sprintf("run 'tuios ls' to list sessions, or 'tuios new %s' to create this one.", name)
	}
	return e
}

// closestName is the CLI-side spelling suggestion, matching the policy the
// daemon uses for its hints so the two never disagree.
func closestName(target string, candidates []string) string {
	if target == "" {
		return ""
	}
	limit := min(len(target)/4+1, 3)

	best, bestDist := "", limit+1
	for _, c := range candidates {
		if c == target {
			continue
		}
		d := editDistance(strings.ToLower(target), strings.ToLower(c))
		if d < bestDist {
			bestDist, best = d, c
		}
	}
	if bestDist > limit {
		return ""
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
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
		What: fmt.Sprintf("The terminal is %dx%d, which is too small to render a TUIOS session (minimum %dx%d).",
			width, height, minTerminalWidth, minTerminalHeight),
		Cause: "the window, font size, or split pane leaves too few rows and columns for the dockbar and a usable window.",
		Fix:   "resize the terminal or reduce the font size, then run this command again.",
	}
}

// checkTerminalCapabilities rejects a TERM value that cannot express the cursor
// movement and styling the renderer needs. Only "dumb" and an unset TERM are
// genuinely unusable; everything else is allowed through, because guessing at
// terminfo coverage would reject working terminals.
func checkTerminalCapabilities(termEnv string) error {
	switch termEnv {
	case "":
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
