package app

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/adrg/xdg"
)

// What tuios does when it reaches a state it should not have reached.
//
// The panic barriers came first and the report came second, which is the wrong
// way round and is why this file exists. Update and the graphics flush have
// recovered per-event panics for a while: the pane survives, a line goes into
// the in-app log, and a file lands in the state directory. None of that reaches
// the person at the keyboard. LogError draws nothing (see os_notify.go: the log
// ring is only visible behind leader D l), so the whole of what a user saw when
// tuios hit an impossible state was one frame that did not update. A bug nobody
// can see is a bug nobody reports.
//
// A CrashReport is the snapshot the overlay draws and the clipboard carries. It
// is built once, at the moment of the panic, and it is a plain value from then
// on: no pointers back into the model, no methods that read live state. That is
// deliberate and it is the whole design. The overlay renders after something
// has already gone wrong, so anything it reads could be the thing that broke.
// Copying the facts out once, behind its own recover, is what lets the drawing
// code be ordinary.
//
// What the report may carry is the second rule, and it is a privacy rule. See
// crashFacts.

// CrashReport is one panic and the facts that place it.
//
// Every field is a string or an int filled in at capture time. Rendering,
// copying and building an issue URL are all pure functions of this struct, so
// they can be tested against a report that no model ever produced, which is the
// only way to test the case that matters.
type CrashReport struct {
	// When the panic happened.
	When time.Time
	// Where names the barrier that caught it, in words a user can repeat:
	// "handling an event", "drawing the screen", "drawing images".
	Where string
	// Panic is the panic value, formatted.
	Panic string
	// Stack is the goroutine stack at the point of the recover.
	Stack string
	// LogPath is the crash log this report was also written to, or "" when the
	// write failed. The overlay says so either way, because a user told to find
	// a file that is not there loses more time than one told there is no file.
	LogPath string
	// Facts are the structural details, in the order they are shown. See
	// crashFacts for what is allowed in here.
	Facts []CrashFact
}

// CrashFact is one label and one value in the report's detail block.
type CrashFact struct {
	Label string
	Value string
}

// buildStamp is the version this binary was linked with. internal/app cannot
// read main.version, and a report that cannot say which build produced it
// cannot be placed against a commit, so the main package hands it over at
// startup through SetBuildStamp.
//
// Atomic because the SSH server builds a model per connection while other
// sessions run. It is written once before any of them start; the atomic is for
// the race detector's benefit, not for a real contention.
var buildStamp atomic.Pointer[buildFacts]

type buildFacts struct {
	version string
	commit  string
}

// SetBuildStamp records the build identity for crash reports. The main package
// calls it once at startup with the ldflag values.
func SetBuildStamp(version, commit string) {
	buildStamp.Store(&buildFacts{version: version, commit: commit})
}

// buildIdentity is the version and commit a report should name. It prefers the
// stamp the main package supplied and falls back to what the go command
// embedded, so a build made without ldflags still says something true rather
// than nothing.
func buildIdentity() (version, commit string) {
	if b := buildStamp.Load(); b != nil {
		version, commit = b.version, b.commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return orUnknown(version), orUnknown(commit)
	}
	if version == "" || version == "dev" {
		if mv := info.Main.Version; mv != "" && mv != "(devel)" {
			version = mv
		}
	}
	if commit == "" || commit == "none" {
		var rev string
		var dirty bool
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if rev != "" {
			commit = rev
			if dirty {
				commit += "-dirty"
			}
		}
	}
	return orUnknown(version), orUnknown(commit)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// CrashLogDir returns the directory for crash logs.
func CrashLogDir() string {
	return filepath.Join(xdg.StateHome, "tuios")
}

// NewCrashReport builds a report from a recovered panic and a set of facts.
// The caller supplies the facts because only the caller knows whether it has a
// model to read them from.
func NewCrashReport(where string, panicValue any, stack []byte, facts []CrashFact) *CrashReport {
	return &CrashReport{
		When:  time.Now(),
		Where: where,
		Panic: fmt.Sprintf("%v", panicValue),
		Stack: string(stack),
		Facts: facts,
	}
}

// crashHeadFacts are the facts that open the block: what caught it and which
// build it was. They lead because they are what someone reads out loud when
// they show the screen to somebody else, and because a report that loses
// everything below the fold must not lose the version.
func crashHeadFacts(where string) []CrashFact {
	version, commit := buildIdentity()
	return []CrashFact{
		{Label: "Caught while", Value: where},
		{Label: "tuios", Value: version},
		{Label: "Commit", Value: commit},
	}
}

// crashMachineFacts are the toolchain and the platform. They close the block
// because they are the least likely of the facts to be the answer and the most
// likely to be reconstructable from the rest of the report.
func crashMachineFacts() []CrashFact {
	return []CrashFact{
		{Label: "Emulator", Value: vt.Backend},
		{Label: "Go", Value: runtime.Version()},
		{Label: "System", Value: runtime.GOOS + "/" + runtime.GOARCH},
	}
}

// baseFacts is the whole set for a report with no model behind it.
func baseFacts(where string) []CrashFact {
	return append(crashHeadFacts(where), crashMachineFacts()...)
}

// Title is the one-line summary, used as the issue title and as the overlay's
// second line. It is the panic value on its own line, clipped, because that is
// the part that tells two reports apart.
func (r *CrashReport) Title() string {
	if r == nil {
		return "Crash"
	}
	p := strings.TrimSpace(strings.SplitN(r.Panic, "\n", 2)[0])
	if p == "" {
		p = "unknown panic"
	}
	const maxTitle = 90
	if len(p) > maxTitle {
		p = p[:maxTitle-1] + "…"
	}
	return "Crash: " + p
}

// Markdown is the report as it goes on the clipboard and into an issue body.
//
// stackLines caps the trace; zero or less means the whole of it. The cap exists
// for the issue URL, which has a length a browser will refuse, and not for the
// clipboard, which does not.
func (r *CrashReport) Markdown(stackLines int) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("### What happened\n\n")
	b.WriteString("tuios reached a state it does not expect and recovered from it.\n\n")
	b.WriteString("```\n")
	b.WriteString(r.Panic)
	b.WriteString("\n```\n\n### Details\n\n")
	b.WriteString("| | |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| Time | %s |\n", r.When.Format(time.RFC3339)))
	for _, f := range r.Facts {
		b.WriteString("| " + f.Label + " | " + f.Value + " |\n")
	}
	b.WriteString("\n### Stack trace\n\n```\n")
	stack, trimmed := clipStack(r.Stack, stackLines)
	b.WriteString(stack)
	if !strings.HasSuffix(stack, "\n") {
		b.WriteString("\n")
	}
	if trimmed > 0 {
		b.WriteString(fmt.Sprintf("... %d more lines\n", trimmed))
	}
	b.WriteString("```\n")
	if trimmed > 0 && r.LogPath != "" {
		b.WriteString("\nThe whole trace is in `" + r.LogPath + "` on the machine that ran tuios.\n")
	}
	return b.String()
}

// clipStack returns the first n lines of a stack and how many it dropped. A
// non-positive n keeps all of it.
func clipStack(stack string, n int) (string, int) {
	if n <= 0 {
		return stack, 0
	}
	lines := strings.Split(strings.TrimRight(stack, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n"), 0
	}
	return strings.Join(lines[:n], "\n"), len(lines) - n
}

// issueURLLimit is the longest issue URL tuios will build.
//
// A prefilled issue is a GET, so the whole report travels in the query string,
// and every hop has an opinion about how long that may be. Browsers stop
// somewhere past 32k, but the server in front of GitHub answers 414 well before
// that, and xdg-open hands the address to a desktop that has its own argv
// limits. 6000 is comfortably under every one of them and holds the facts plus
// a useful head of the trace, which is the part that identifies the bug. The
// rest is in the crash log and on the clipboard.
const issueURLLimit = 6000

// IssueURL is a new-issue address with the report already filled in.
//
// It fits the body to issueURLLimit by dropping stack lines from the end, which
// is the right end to drop: the top frames are where the panic happened and the
// bottom ones are the event loop, which is the same in every report. It never
// returns an over-long address; in the worst case the body is the facts alone
// and the trace is left to the clipboard.
func (r *CrashReport) IssueURL() string {
	if r == nil {
		return ""
	}
	base := "https://github.com/" + release.Repo + "/issues/new"
	q := url.Values{}
	q.Set("title", r.Title())
	q.Set("labels", "bug")

	for _, lines := range []int{40, 24, 12, 6, 1} {
		q.Set("body", r.Markdown(lines))
		if candidate := base + "?" + q.Encode(); len(candidate) <= issueURLLimit {
			return candidate
		}
	}
	// Even one line of trace does not fit, so the panic value is a very long
	// one. Say where the rest is rather than build an address nothing accepts.
	q.Set("body", r.shortBody())
	return base + "?" + q.Encode()
}

// shortBody is the issue body for a report too large to carry its own trace.
func (r *CrashReport) shortBody() string {
	var b strings.Builder
	b.WriteString("### What happened\n\ntuios reached a state it does not expect.\n\n")
	b.WriteString("The report is too large for a prefilled issue. ")
	b.WriteString("Press c in the crash overlay to copy it, then paste it here.\n\n")
	if r.LogPath != "" {
		b.WriteString("Crash log: `" + r.LogPath + "`\n\n")
	}
	b.WriteString("### Details\n\n| | |\n|---|---|\n")
	for _, f := range r.Facts {
		b.WriteString("| " + f.Label + " | " + f.Value + " |\n")
	}
	return b.String()
}

// WriteCrashLog writes a report to a timestamped file in the crash log
// directory and records the path on the report. It returns the path, or "" when
// the write failed.
//
// The file is still written even though the overlay now shows the same thing,
// because the two answer different questions. The overlay is for the session
// that crashed; the file is for the one after it, and for a user who pressed a
// key before reading. It is also the only artifact an SSH or web client leaves
// behind on the machine that actually ran the code.
func WriteCrashLog(report *CrashReport) string {
	if report == nil {
		return ""
	}
	dir := CrashLogDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		return ""
	}

	filename := fmt.Sprintf("crash-%s.log", report.When.Format("2006-01-02_15-04-05"))
	path := filepath.Join(dir, filename)

	var b strings.Builder
	b.WriteString("tuios crash report\n==================\n\n")
	b.WriteString(fmt.Sprintf("Time:    %s\n", report.When.Format(time.RFC3339)))
	for _, f := range report.Facts {
		b.WriteString(fmt.Sprintf("%-9s%s\n", f.Label+":", f.Value))
	}
	b.WriteString(fmt.Sprintf("\nPanic:   %v\n\n", report.Panic))
	b.WriteString(fmt.Sprintf("Stack trace:\n%s\n", report.Stack))
	b.WriteString("\n---\nReport this at:\n")
	b.WriteString("https://github.com/" + release.Repo + "/issues/new\n")

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		return ""
	}
	report.LogPath = path
	return path
}

// crashFacts is everything the report says about the running client beyond the
// build and the machine.
//
// The rule this function is written to: a crash report carries the shape of the
// session and never its contents. What tuios is doing is a bug report; what the
// user is doing is theirs. So the counts, the modes, the sizes and tuios' own
// action names go in, and pane contents, scrollback, the working directory,
// window and pane titles, the session name and the environment stay out. Every
// one of those five can hold a hostname, a token, a client's name or a path
// that says who someone works for, and none of them has ever helped place a
// panic in a stack trace.
//
// The panic message itself is the one judgement call. It is written by tuios,
// not by the user, so it carries tuios' own vocabulary; and a report without it
// says nothing at all. It goes in.
//
// This runs from inside a recover, on a model that has just failed, so it
// cannot assume any of it is sound. Every read is nil-guarded and the whole
// thing sits behind its own barrier: a report with a missing row is worth far
// more than a second panic on the way to drawing one.
func (m *OS) crashFacts(where string) (facts []CrashFact) {
	facts = crashHeadFacts(where)
	if m == nil {
		return append(facts, crashMachineFacts()...)
	}
	defer func() {
		if r := recover(); r != nil {
			facts = append(facts, CrashFact{
				Label: "Session",
				Value: "not readable, the model was too broken to describe",
			})
			facts = append(facts, crashMachineFacts()...)
		}
	}()

	facts = append(facts,
		CrashFact{Label: "Client", Value: m.clientKind()},
		CrashFact{Label: "Terminal", Value: m.crashTerminalName()},
		CrashFact{Label: "Screen", Value: strconv.Itoa(m.Width) + "x" + strconv.Itoa(m.Height)},
		CrashFact{Label: "Mode", Value: m.crashModeName()},
		CrashFact{Label: "Layout", Value: m.LayoutName()},
		CrashFact{Label: "Panes", Value: m.crashPaneCount()},
		CrashFact{Label: "Daemon", Value: yesNo(m.IsDaemonSession)},
		CrashFact{Label: "Last actions", Value: m.crashRecentActions()},
		CrashFact{Label: "Tape", Value: m.crashTapeState()},
	)
	return append(facts, crashMachineFacts()...)
}

// clientKind names where the person looking at this screen is sitting. It is
// the first thing a bug report needs, because a third of tuios behaves
// differently across the three.
func (m *OS) clientKind() string {
	switch {
	case m.BrowserClient:
		return "web"
	case m.IsSSHMode:
		return "ssh"
	default:
		return "local"
	}
}

func (m *OS) crashTerminalName() string {
	caps := m.hostCaps()
	if caps == nil || caps.TerminalName == "" {
		return "unknown"
	}
	return caps.TerminalName
}

func (m *OS) crashModeName() string {
	if m.Mode == TerminalMode {
		return "terminal"
	}
	return "window"
}

// crashPaneCount reports the panes on screen and the panes in the session, in
// that order. The two differ across workspaces, and a bug that only happens
// with panes parked elsewhere is one the visible count would hide.
func (m *OS) crashPaneCount() string {
	total := len(m.Windows)
	visible := total
	if v := m.GetVisibleWindows(); v != nil {
		visible = len(v)
	}
	return fmt.Sprintf("%d visible, %d in session", visible, total)
}

// crashRecentActions lists the keybind actions this client last ran.
//
// This is as close to "how to reproduce" as tuios can honestly get from a
// session that was not being recorded. They are tuios' own action names, the
// same words the keybind table uses, so a maintainer can follow them; they are
// not keystrokes and they are not text, so nothing the user typed is in here.
func (m *OS) crashRecentActions() string {
	acts := m.RecentActions()
	if len(acts) == 0 {
		return "none recorded"
	}
	return strings.Join(acts, " → ")
}

// crashTapeState says whether a replayable script of this session exists.
//
// A tape is the only artifact tuios has that can actually reproduce a bug, and
// it exists only when the user chose to record. When one is running the report
// says so, because that recording is worth more than everything else in the
// report put together. When one is not, the report says nothing rather than
// implying tuios could have replayed this and did not.
func (m *OS) crashTapeState() string {
	if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
		return "recording, save it after you dismiss this"
	}
	return "not recording"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
