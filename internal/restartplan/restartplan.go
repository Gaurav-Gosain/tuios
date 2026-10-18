// Package restartplan is what a daemon restart stops and brings back, in the
// one shape `tuios restart-server --json` writes and the app reads, and in the
// words both of them show.
//
// The CLI reads the daemon and fills a Report. The app runs that command of
// the newest tuios installed and decodes its lines into the same Report. Both
// turn a plan into text with Lines, so the list the terminal prints and the
// list the dialog draws cannot say different things.
package restartplan

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
)

// Steps, in the order `tuios restart-server --json` writes them.
const (
	StepPlan     = "plan"
	StepStopping = "stopping"
	StepStarting = "starting"
	StepDone     = "done"
)

// Daemon states.
const (
	StateRunning = "running"
	StateStopped = "stopped"
	StateUnknown = "unknown"
)

// Resume modes, as daemon.resume_agents spells them.
const (
	ResumeAsk  = "ask"
	ResumeAuto = "auto"
	ResumeOff  = "off"
)

// Report is one line of `tuios restart-server --json`: the plan, a step, or
// the result.
type Report struct {
	Step string `json:"step"`
	// Binary is the tuios the new daemon runs, and Version what it reports.
	Binary  string `json:"binary,omitempty"`
	Version string `json:"version,omitempty"`
	// Daemon is the daemon as it ran before the restart.
	Daemon *Daemon `json:"daemon,omitempty"`
	// ResumeMode is daemon.resume_agents as the new daemon reads it. Empty
	// when the agent features are off.
	ResumeMode string `json:"resume_mode,omitempty"`
	// Restarted says the old daemon stopped and the new one answers.
	Restarted bool `json:"restarted,omitzero"`
	// After is the version the new daemon reports.
	After string `json:"after,omitempty"`
	// Back lists the sessions the new daemon holds, and Missing the ones it
	// held before and does not hold now.
	Back    []string `json:"back,omitempty"`
	Missing []string `json:"missing,omitempty"`
	// Error says what failed, and Fix what to do next.
	Error string `json:"error,omitempty"`
	Fix   string `json:"fix,omitempty"`
}

// Daemon is the daemon a restart stops.
type Daemon struct {
	State        string    `json:"state"`
	Version      string    `json:"version,omitempty"`
	PID          int       `json:"pid,omitzero"`
	SessionCount int       `json:"session_count"`
	Sessions     []Session `json:"sessions,omitempty"`
	// Unread says the sessions could not be listed, so the cost is not known.
	Unread bool `json:"unread,omitzero"`
}

// Session is one session on a daemon, with the panes that run a program.
type Session struct {
	Name  string `json:"name"`
	Panes int    `json:"panes"`
	Busy  []Pane `json:"busy,omitempty"`
	// Resume lists the agents the new daemon can offer to resume.
	Resume []Agent `json:"resume,omitempty"`
}

// Pane is a pane that runs a program a restart ends.
type Pane struct {
	Window  string `json:"window"`
	Program string `json:"program"`
}

// Agent is an agent pane whose conversation can resume after a restart.
type Agent struct {
	Window  string `json:"window"`
	Harness string `json:"harness"`
}

// Kinds of Line.
const (
	// KindRow is a label and a value, aligned with the other rows.
	KindRow = iota
	// KindHeading names the list under it.
	KindHeading
	// KindStop is a program that stops.
	KindStop
	// KindBack is something that comes back.
	KindBack
	// KindNote is a quiet sentence.
	KindNote
	// KindWarn is a sentence about something that went wrong.
	KindWarn
	// KindBlank separates two parts.
	KindBlank
)

// Line is one line of a plan.
type Line struct {
	Kind  int
	Label string
	Text  string
}

// RowLabelWidth is the column the values of the rows start at.
const RowLabelWidth = 16

// Lines is the plan in words: the versions, what stops and what comes back.
// window is the version of the client that shows it, or "" for none.
func Lines(r *Report, window string) []Line {
	d := r.Daemon
	if d == nil {
		d = &Daemon{State: StateUnknown}
	}
	now := d.Version
	switch {
	case d.State == StateStopped:
		now = "not running"
	case now == "":
		now = "unknown"
	}
	after := r.Version
	if r.Binary != "" {
		after += "   (from " + homeShort(r.Binary) + ")"
	}
	out := []Line{{Kind: KindRow, Label: "Daemon now", Text: now}, {Kind: KindRow, Label: "After restart", Text: after}}
	if window != "" && window != r.Version {
		out = append(out, Line{Kind: KindRow, Label: "This window", Text: window + "   (moves to " + r.Version + " too)"})
	}
	if d.State != StateStopped && d.Version != "" && d.Version == r.Version {
		out = append(out, Line{Kind: KindNote, Text: "This restart starts the same version."})
	}
	out = append(out, Line{Kind: KindBlank})
	if d.State == StateStopped {
		return append(out, Line{Kind: KindNote, Text: "The daemon is not running, so nothing stops."})
	}
	if d.Unread {
		return append(out, Line{Kind: KindWarn, Text: "The sessions could not be read, so tuios cannot say what a restart stops."})
	}
	if len(d.Sessions) == 0 {
		return append(out, Line{Kind: KindNote, Text: "The daemon has no sessions, so nothing stops."})
	}
	out = append(out, Line{Kind: KindHeading, Text: "These stop"})
	var quiet []string
	busy := 0
	for _, s := range d.Sessions {
		if len(s.Busy) == 0 {
			quiet = append(quiet, s.Name)
		}
		for _, b := range s.Busy {
			out = append(out, Line{Kind: KindStop, Text: b.Program + " in " + strconv.Quote(b.Window) + " (session " + s.Name + ")"})
			busy++
		}
	}
	switch {
	case busy == 0:
		out = append(out, Line{Kind: KindNote, Text: "No program stops. Each pane is at a shell prompt."})
	case len(quiet) > 0:
		out = append(out, Line{Kind: KindNote, Text: "Only shells in " + strings.Join(quiet, ", ") + "."})
	}
	out = append(out, Line{Kind: KindBlank}, Line{Kind: KindHeading, Text: "These come back"},
		Line{Kind: KindBack, Text: "Each session, with its layout, its pane history and new shells."})
	agents := 0
	for _, s := range d.Sessions {
		for _, a := range s.Resume {
			out = append(out, Line{Kind: KindBack, Text: a.Harness + " in " + strconv.Quote(a.Window) + " (session " + s.Name + ") can resume."})
			agents++
		}
	}
	if agents > 0 {
		out = append(out, Line{Kind: KindNote, Text: resumeNote(r.ResumeMode)})
	}
	return out
}

// resumeNote says what the new daemon does with the agents.
func resumeNote(mode string) string {
	switch mode {
	case ResumeAuto:
		return "The new daemon resumes them."
	case ResumeOff:
		return "Run 'tuios resume-agent' in a pane to resume it."
	}
	return "The Inbox offers to resume each one."
}

// homeShort writes a path under the home folder with ~.
func homeShort(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(p, home+string(os.PathSeparator)); ok {
			return "~" + string(os.PathSeparator) + rest
		}
	}
	return p
}

// Text is the plan as a terminal prints it: rows aligned, lists indented.
func Text(lines []Line) string {
	var b strings.Builder
	for _, l := range lines {
		switch l.Kind {
		case KindRow:
			b.WriteString(l.Label + strings.Repeat(" ", max(RowLabelWidth-len(l.Label), 1)) + l.Text)
		case KindHeading:
			b.WriteString(l.Text + ":")
		case KindStop, KindBack:
			b.WriteString("  " + l.Text)
		case KindBlank:
		default:
			b.WriteString(l.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Digest names a plan by what a restart would do: the binary and version it
// starts, the daemon it stops, and every program and agent in its sessions.
// The app passes the digest of the plan the person agreed to, and the
// command refuses when the plan it reads now has another one.
func Digest(r *Report) string {
	var b strings.Builder
	b.WriteString(r.Binary + "\x00" + r.Version + "\x00")
	if d := r.Daemon; d != nil {
		b.WriteString(d.State + "\x00" + d.Version + "\x00" + strconv.FormatBool(d.Unread) + "\x00")
		for _, s := range d.Sessions {
			b.WriteString("s\x00" + s.Name + "\x00")
			for _, p := range s.Busy {
				b.WriteString("b\x00" + p.Window + "\x00" + p.Program + "\x00")
			}
			for _, a := range s.Resume {
				b.WriteString("r\x00" + a.Window + "\x00" + a.Harness + "\x00")
			}
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}
