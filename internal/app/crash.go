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

	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/adrg/xdg"
	"tuios.dev/vt"
)

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

var buildStamp atomic.Pointer[buildFacts]

type buildFacts struct {
	version string
	commit  string
}

// SetBuildIdentity hands the build identity to every package that reports it,
// and returns the stamps it settled on. Each main package calls it once at
// startup with its ldflag values, before anything can crash or open a pane.
//
// It is one call so that a second binary cannot set some of the setters and
// miss others: tuios-web once set only the crash stamp, and its in-process
// panes said "dev" in TERM_PROGRAM_VERSION and nothing in XTVERSION.
//
// A `go install ...@vX` build has no ldflags; its module version stands in for
// them first (release.Stamps.WithModuleVersion).
func SetBuildIdentity(s release.Stamps) release.Stamps {
	s = s.WithModuleVersion()
	buildStamp.Store(&buildFacts{version: s.Version, commit: s.Commit})
	herdrplugin.RunningVersion = s.Version
	vt.SetBuildVersion(s.Version)
	guestenv.SetBuildVersion(s.Version)
	return s
}

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

func crashHeadFacts(where string) []CrashFact {
	version, commit := buildIdentity()
	return []CrashFact{
		{Label: "Caught while", Value: where},
		{Label: "tuios", Value: version},
		{Label: "Commit", Value: commit},
	}
}

func crashMachineFacts() []CrashFact {
	return []CrashFact{
		{Label: "Emulator", Value: vt.Backend},
		{Label: "Go", Value: runtime.Version()},
		{Label: "System", Value: runtime.GOOS + "/" + runtime.GOARCH},
	}
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
	fmt.Fprintf(&b, "| Time | %s |\n", r.When.Format(time.RFC3339))
	for _, f := range r.Facts {
		b.WriteString("| ")
		b.WriteString(f.Label)
		b.WriteString(" | ")
		b.WriteString(f.Value)
		b.WriteString(" |\n")
	}
	b.WriteString("\n### Stack trace\n\n```\n")
	stack, trimmed := clipStack(r.Stack, stackLines)
	b.WriteString(stack)
	if !strings.HasSuffix(stack, "\n") {
		b.WriteString("\n")
	}
	if trimmed > 0 {
		fmt.Fprintf(&b, "... %d more lines\n", trimmed)
	}
	b.WriteString("```\n")
	if trimmed > 0 && r.LogPath != "" {
		b.WriteString("\nThe whole trace is in `")
		b.WriteString(r.LogPath)
		b.WriteString("` on the machine that ran tuios.\n")
	}
	return b.String()
}

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
	q.Set("body", r.shortBody())
	return base + "?" + q.Encode()
}

func (r *CrashReport) shortBody() string {
	var b strings.Builder
	b.WriteString("### What happened\n\ntuios reached a state it does not expect.\n\n")
	b.WriteString("The report is too large for a prefilled issue. ")
	b.WriteString("Press c in the crash overlay to copy it, then paste it here.\n\n")
	if r.LogPath != "" {
		b.WriteString("Crash log: `")
		b.WriteString(r.LogPath)
		b.WriteString("`\n\n")
	}
	b.WriteString("### Details\n\n| | |\n|---|---|\n")
	for _, f := range r.Facts {
		b.WriteString("| ")
		b.WriteString(f.Label)
		b.WriteString(" | ")
		b.WriteString(f.Value)
		b.WriteString(" |\n")
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
	fmt.Fprintf(&b, "Time:    %s\n", report.When.Format(time.RFC3339))
	for _, f := range report.Facts {
		fmt.Fprintf(&b, "%-9s%s\n", f.Label+":", f.Value)
	}
	fmt.Fprintf(&b, "\nPanic:   %v\n\n", report.Panic)
	fmt.Fprintf(&b, "Stack trace:\n%s\n", report.Stack)
	b.WriteString("\n---\nReport this at:\n")
	b.WriteString("https://github.com/" + release.Repo + "/issues/new\n")

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		return ""
	}
	report.LogPath = path
	return path
}

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

func (m *OS) crashPaneCount() string {
	total := len(m.Windows)
	visible := total
	if v := m.GetVisibleWindows(); v != nil {
		visible = len(v)
	}
	return fmt.Sprintf("%d visible, %d in session", visible, total)
}

func (m *OS) crashRecentActions() string {
	acts := m.RecentActions()
	if len(acts) == 0 {
		return "none recorded"
	}
	return strings.Join(acts, " → ")
}

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
