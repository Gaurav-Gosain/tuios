package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The daemon-side witness.
//
// # Why a witness is needed at all
//
// Everything an attached client shows is a copy. The original lives in the
// daemon, which owns each pane's emulator and hands a client a byte stream to
// replay into its own. Every assertion that reads only the client's screen is
// therefore asking the copy whether it agrees with itself, and a copy always
// does.
//
// That split is where the worst bugs of the month lived, and docs/REHYDRATION.md
// is the audit of them. What reaches the screen when a route gets it wrong is
// two stretches of one pane's output, far apart in time, adjacent on the glass:
// a stream replayed across a hole, or the same lines painted twice at a seam.
// Nothing a client-only check looks at is wrong in that state. The process is
// alive, the grid is the right size, every pane holds plausible text, and the
// pane is a lie.
//
// # What the witness is
//
// The daemon's own answer to the same question, read through the verbs a user
// already has: capture-pane for a pane's grid and its scrollback, list-windows
// and session-info for the structure. The daemon renders those from its own
// emulator and never from an attached client, so it is an independent observer
// of the same state rather than a second look at the same buffer.
//
// docs/REHYDRATION.md names that emulator the authority for grid, cursor, modes
// and scrollback, on the grounds that it is the only thing that has seen every
// byte, and its feed now blocks rather than dropping a chunk when it falls
// behind. That is what makes it usable as a witness for content and not only for
// structure, and it is why the rules below read it as ground truth.
//
// # Which of that document's invariants these rules cover
//
// The contract states five, and this file is a partial oracle for three of them,
// asserted continuously under an arbitrary action stream rather than at the end
// of one named route:
//
//	2. Scrollback. The client may never hold history the daemon does not have.
//	   witness-provenance and client-ahead are that, bounded from both sides.
//	4. Modes. The alternate-screen flag matches. altscreen-retained is that.
//	5. No duplication. Content produced once appears once. The adjacency rule
//	   fires on a repeated number as readily as on a missing run, so a line
//	   painted twice at a seam breaks it.
//
// Invariant 1, cell-for-cell grid equality, and invariant 3, the cursor, are
// asserted far better by TestRehydrationMatrix in internal/app, which runs a
// daemon in process and compares emulators directly. Restating them here against
// a screen scrape would be slower and less certain about the same property. What
// this file adds is that its three run after every action of a fuzz stream,
// through a real PTY and the real binding table, in states nobody chose.
//
// Scroll position is deliberately absent. The contract says it is where a viewer
// is looking rather than a property of the pane, and that it is not preserved on
// the routes that rebuild windows. A rule about it here would report the design.
//
// # Witness lines
//
// A pane is asked to print numbered lines, "MK<tag>-<n>", one per line, with n
// counting up forever per pane. Two properties fall out of that and they are
// what the oracle is built on.
//
// Adjacency: two witness lines that sit on adjacent rows must carry adjacent
// numbers. A spliced stream breaks it and almost nothing else does, because
// clipping a pane removes whole rows from the top or the bottom, an overlay
// covers a run of rows, and a partly hidden pane loses a run of rows. All of
// those separate the surviving lines by at least one non-witness row, so the
// rule simply declines to compare them. It needs no knowledge of where a pane
// is, how big it is, or what is on top of it, which is what makes it safe to run
// after every single action.
//
// Provenance: a number on the client must be one the daemon actually has. The
// client showing MKab01-12 while the daemon's grid holds 400..423 is the client
// rendering something the daemon has forgotten, which is the stale half of a
// splice even when the visible run happens to be self-consistent.
//
// The command that prints them is written so its own echo cannot be mistaken for
// output: the shell shows `printf 'MKab01-%d\n' $(seq ...)`, and "%d" is not a
// run of digits, so the format string never matches the pattern its output does.

// witnessRe matches one witness line. The tag is the first four characters of
// the window's uuid, which makes a line self-identifying without the reader
// having to know which pane it is looking at.
var witnessRe = regexp.MustCompile(`MK([0-9a-f]{6})-([0-9]+)`)

// altWitnessRe matches the single line a pane paints on the alternate screen.
// It is deliberately a different shape: the alternate screen has no scrollback
// and shares none of its content with the main one, so a rule about it must not
// be satisfiable by a main-screen line.
var altWitnessRe = regexp.MustCompile(`ALT([0-9a-f]{6})`)

// witness is one witness line and the row it was found on.
type witness struct {
	row int
	tag string
	seq int
}

// witnessesIn reads the witness lines out of an ordered list of rows, which is
// either a client screen or a daemon capture. Rows carrying more than one match
// keep only the first: a wrapped or overwritten row is not evidence of order.
func witnessesIn(lines []string) []witness {
	var out []witness
	for row, line := range lines {
		m := witnessRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		out = append(out, witness{row: row, tag: m[1], seq: n})
	}
	return out
}

// spliceIn returns the first pair of witness lines that sit on adjacent rows
// carrying non-adjacent numbers, which is a stream replayed across a hole.
//
// Only adjacent rows are compared, and only within one pane. A gap between rows
// means something else is drawn between them, and something else drawn between
// them is a clip, an overlay, or another window on top, none of which says
// anything about the stream.
func spliceIn(lines []string) (a, b witness, found bool) {
	ws := witnessesIn(lines)
	for i := 1; i < len(ws); i++ {
		prev, cur := ws[i-1], ws[i]
		if prev.tag != cur.tag || cur.row != prev.row+1 {
			continue
		}
		if cur.seq != prev.seq+1 {
			return prev, cur, true
		}
	}
	return witness{}, witness{}, false
}

// seqRange returns the lowest and highest witness number carried by one pane.
func seqRange(lines []string, tag string) (lo, hi int, any bool) {
	for _, w := range witnessesIn(lines) {
		if w.tag != tag {
			continue
		}
		if !any || w.seq < lo {
			lo = w.seq
		}
		if !any || w.seq > hi {
			hi = w.seq
		}
		any = true
	}
	return lo, hi, any
}

// screenLines is the client's screen as an ordered list of rows.
func screenLines(s tuitest.Screen) []string {
	_, rows := s.Size()
	out := make([]string, rows)
	for r := range rows {
		out[r] = s.Line(r)
	}
	return out
}

// ---------------------------------------------------------------------------
// Reading the daemon

// daemonWindow is one entry of list-windows --json
// (internal/session/daemon_native.go:345).
type daemonWindow struct {
	ID        string `json:"window_id"`
	Workspace int    `json:"workspace"`
	Focused   bool   `json:"focused"`
	Minimized bool   `json:"minimized"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// tag is the witness tag for this window: six hex characters of its uuid, which
// is short enough to keep a witness line narrow and long enough that two panes
// of one run sharing a tag is not a thing that happens.
func (w daemonWindow) tag() string {
	id := strings.ToLower(strings.ReplaceAll(w.ID, "-", ""))
	if len(id) < 6 {
		return "000000"
	}
	return id[:6]
}

type daemonWindowList struct {
	Windows          []daemonWindow `json:"windows"`
	Total            int            `json:"total"`
	FocusedWindowID  string         `json:"focused_window_id"`
	CurrentWorkspace int            `json:"current_workspace"`
	Success          bool           `json:"success"`
}

type daemonSessionInfo struct {
	CurrentWorkspace int    `json:"current_workspace"`
	NumWorkspaces    int    `json:"num_workspaces"`
	WindowCount      int    `json:"window_count"`
	TilingMode       string `json:"tiling_mode"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	TUIAttached      bool   `json:"tui_attached"`
	Success          bool   `json:"success"`
}

// tuiosOut runs a tuios subcommand and returns its stdout alone.
//
// tuiosCLI merges stderr in, which is right for a command whose failure message
// is the interesting part and wrong for capture-pane, where the result is a
// pane's literal content and a stray warning would be parsed as if the pane had
// printed it.
func tuiosOut(base string, args ...string) (string, error) {
	cmd := exec.Command(tuiosBin, args...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+filepath.Join(base, key))
	}
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err,
			strings.TrimSpace(errBuf.String()))
	}
	return string(out), nil
}

// daemonJSON runs a read verb and decodes it.
func daemonJSON[T any](base string, args ...string) (T, error) {
	var v T
	out, err := tuiosOut(base, append(args, "--json")...)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return v, fmt.Errorf("%s: decode %q: %w", strings.Join(args, " "), out, err)
	}
	return v, nil
}

func daemonWindows(base, session string) (daemonWindowList, error) {
	return daemonJSON[daemonWindowList](base, "list-windows", "-s", session)
}

func daemonInfo(base, session string) (daemonSessionInfo, error) {
	return daemonJSON[daemonSessionInfo](base, "session-info", "-s", session)
}

// daemonPane is the daemon's own render of one pane's visible grid, as rows.
func daemonPane(base, session, window string) ([]string, error) {
	out, err := tuiosOut(base, "capture-pane", "-s", session, "-w", window)
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// daemonScrollback is the tail of everything a pane has printed, history
// included. lines counts from the last row with content, so a pane sitting idle
// under a screenful of blanks still gives back its real output.
func daemonScrollback(base, session, window string, lines int) ([]string, error) {
	out, err := tuiosOut(base, "capture-pane", "-s", session, "-w", window,
		"--scrollback", "--lines", strconv.Itoa(lines))
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// ---------------------------------------------------------------------------
// Writing into a pane from the daemon side

// paneWitnessCmd is the command that makes a pane print witness lines lo..hi.
//
// One printf over one seq, rather than a shell loop, because a loop calling
// printf per line is a fork per line on any shell where printf is not a builtin,
// and a burst is thousands of lines. The format string carries the tag so the
// echoed command line is not itself a witness: "%d" is not a run of digits.
func paneWitnessCmd(tag string, lo, hi int) string {
	return fmt.Sprintf("printf 'MK%s-%%d\\n' $(seq %d %d)\n", tag, lo, hi)
}

// paneAltCmd puts a pane on the alternate screen with a single identifying line,
// or takes it off again. The alternate screen has no scrollback of its own, so a
// pane that is really on it can show no main-screen witness line at all, which
// is what makes the leaving case observable rather than merely plausible.
func paneAltCmd(tag string, enter bool) string {
	if !enter {
		return paneEmitCmd("\x1b[?1049l")
	}
	return paneEmitCmd("\x1b[?1049h\x1b[2J\x1b[H\nALT" + tag + "\n")
}

// paneEmitCmd builds a command that makes the pane's own program write exactly
// these bytes.
//
// Every byte goes out as a three-digit octal escape. That is not decoration: it
// is the only spelling that survives both the shell and printf whatever the
// payload is. A quote, a backslash, a percent, a newline and a NUL all become
// digits, so nothing in the payload can end the quoting or be read as a
// conversion, and three digits every time removes the ambiguity that bites when
// an escape is followed by a literal digit (printf takes up to three, so \33
// followed by "7" is one character, not two).
func paneEmitCmd(s string) string {
	var b strings.Builder
	b.WriteString("printf '")
	for i := range len(s) {
		fmt.Fprintf(&b, "\\%03o", s[i])
	}
	b.WriteString("'\n")
	return b.String()
}

// paneSend writes a command into a pane's PTY through the daemon, which works
// whether or not a client is attached and regardless of which mode the client is
// in. Typing it at the client instead would be typing at whatever the client
// currently routes keys to, which during a fuzz run is not knowable.
func paneSend(base, session, window, text string) error {
	_, err := tuiosOut(base, "send-text", "-s", session, "-w", window, text)
	return err
}

// ---------------------------------------------------------------------------
// Dock reads
//
// The dock is the client's own summary of the same two numbers session-info
// reports, which is what makes comparing them a real cross-check rather than a
// restatement.

// dockWorkspace reads the workspace number out of the dock status field. It
// returns -1 when the dock is not on screen, matching countWindows.
func dockWorkspace(s tuitest.Screen) int {
	_, rows := s.Size()
	for r := rows - 1; r >= max(0, rows-3); r-- {
		if m := dockStatus.FindStringSubmatch(s.Line(r)); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			return n
		}
	}
	return -1
}

// waitDock blocks until the client's dock is readable, which is how a freshly
// attached client says it has finished rehydrating the session.
func waitDock(t *testing.T, term *tuitest.Terminal) error {
	t.Helper()
	return term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) >= 0
	}, bootTimeout)
}
