// Package proto is the wire protocol between kerod, the daemon that owns
// every session's pseudo-terminal, and its clients: the app and the kero
// CLI.
//
// A client connects to the daemon's Unix socket and sends one JSON line,
// a Hello. A control connection then exchanges JSON lines: requests,
// their responses, and, once subscribed, events. An attach connection
// streams one session's terminal as binary frames (see frame.go).
package proto

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// Version changes whenever the protocol changes in a way the other side
// can't ignore: a client refuses a daemon of another version, which it
// offers to restart. A new optional field, which the other side leaves
// out or skips, doesn't change it.
const Version = 3

// Hello opens a connection.
type Hello struct {
	Kind    string `json:"kind"` // "control" or "attach"
	Version int    `json:"version"`
	// Attach only.
	Session string `json:"session,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	// Who connects, which `kero __bridge` adds to the Hello of a remote
	// client's stream, for `kero clients`: the SSH client's address, how it
	// came (ssh, wsl), and what the client said it is (MuxHello.Client).
	Peer   string `json:"peer,omitempty"`
	Via    string `json:"via,omitempty"`
	Client string `json:"client,omitempty"`
}

// Welcome answers a Hello.
type Welcome struct {
	Version int   `json:"version"`
	PID     int   `json:"pid"`
	Started int64 `json:"started"` // unix ms
	// App is the version of Kero that kerod runs (App.Version): after an
	// update it is the version before, until kerod restarts.
	App string `json:"app,omitempty"`
	// Build fingerprints the code kerod runs (build.Daemon): an update
	// that leaves it as it was needs no restart. An earlier kerod, or the
	// server build's, tells none.
	Build string `json:"build,omitempty"`
	Error string `json:"error,omitempty"`
	// Attach only: the size of the snapshot that follows.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

// Request is a call on a control connection.
type Request struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// Message is what the daemon writes on a control connection: a response
// (ID set) or an event (Event set).
type Message struct {
	ID     int64  `json:"id,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`

	Event string `json:"event,omitempty"` // "state", "select", "action", "notice", "browser"
	State *State `json:"state,omitempty"`
	// Select asks the app to show a session ("kero focus").
	Select string `json:"select,omitempty"`
	// Action asks the app to run one of its actions ("kero do"), with
	// Args, when the user allows Remote Control.
	Action string            `json:"action,omitempty"`
	Args   map[string]string `json:"args,omitempty"`
	// Notice is a program's desktop notification, to show as it is.
	Notice *Notice `json:"notice,omitempty"`
	// Browser asks the app to do something on a browser pane's page
	// ("kero browser"), which it answers with browser.answer.
	Browser *BrowserRequest `json:"browser,omitempty"`
}

// Notice is a desktop notification a program in a session sent.
type Notice struct {
	Session string `json:"session"`
	// Pane is the pane whose program sent it.
	Pane  string `json:"pane,omitempty"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body"`
}

// Status is what a session is doing, as the sidebar shows it.
type Status string

const (
	// Idle: at a prompt, or a program waiting quietly.
	Idle Status = "idle"
	// Working: a program is busy, as an agent thinking or a build.
	Working Status = "working"
	// Waiting: a program needs the user: it asked a question, rang the
	// bell or posted a notification.
	Waiting Status = "waiting"
	// Failed: a program reported it failed and stopped (OSC 7501 error),
	// until the user looks or types.
	Failed Status = "error"
	// Exited: the session's shell ended.
	Exited Status = "exited"
)

// Record is a program's report of its status, as OSC 7501 (program
// status) sends it: one per id, the root's id "".
type Record struct {
	ID    string `json:"id,omitempty"`
	State string `json:"state"`          // idle, working, done, blocked, error
	Kind  string `json:"kind,omitempty"` // blocked: permission, question, auth
	// Progress is 0–100, or -1 when unknown.
	Progress int    `json:"progress"`
	App      string `json:"app,omitempty"`
	Title    string `json:"title,omitempty"`
	Msg      string `json:"msg,omitempty"`
	Updated  int64  `json:"updated"`
}

// State is everything the daemon knows, which it sends whole on every
// change: there are tens of sessions, not thousands.
type State struct {
	Projects []Project `json:"projects"`
	Sessions []Session `json:"sessions"`
	// Viewed is the session a client shows in a focused window.
	Viewed  string `json:"viewed,omitempty"`
	Started int64  `json:"started"` // unix ms, the daemon's start
	// Conversations counts the changes to the conversations kerod
	// remembers that show in the sidebar, for the app to ask for them
	// anew (the conversations method) rather than have them in every
	// state.
	Conversations uint64 `json:"conversations,omitempty"`
}

// Project groups sessions, usually those of one repository.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Dir  string `json:"dir,omitempty"`
}

// Pane is a terminal the daemon keeps running: a shell in a
// pseudo-terminal, one of a session's; or, with a URL, a browser, which
// the app shows and the daemon only keeps the place and page of.
type Pane struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"` // set by the program (OSC 0/2), or the page
	// URL is the page a browser pane shows, as the app last told: set,
	// the pane runs no shell.
	URL     string   `json:"url,omitempty"`
	Dir     string   `json:"dir,omitempty"` // the shell's working directory
	Command []string `json:"command,omitempty"`
	// Process is the foreground program, "" while the shell is.
	Process string `json:"process,omitempty"`
	// ProcessDir is the foreground program's working directory, as a
	// worktree a coding agent went into, while it is not the shell's.
	ProcessDir string `json:"processDir,omitempty"`
	// Agent names the coding agent running in the foreground, when known.
	Agent string `json:"agent,omitempty"`
	// Runtime names the interpreter of a foreground script, as "node",
	// when Process is the script's name.
	Runtime string `json:"runtime,omitempty"`
	// Shell is the name of the pane's shell, as "fish".
	Shell string `json:"shell,omitempty"`

	Status Status `json:"status"`
	// Unread: something happened that the user has not seen: the work
	// ended, the program asked something, or the shell exited.
	Unread bool `json:"unread,omitempty"`
	// Message is the last notification's text, while Waiting, or what a
	// program reported with its status.
	Message string `json:"message,omitempty"`
	// Kind is what a program waits for (OSC 7501): permission, question
	// or auth.
	Kind string `json:"kind,omitempty"`
	// Progress is a program's progress, 0–100, or -1 when unknown.
	Progress int `json:"progress"`
	// Records are the status reports of the programs (OSC 7501), when
	// they sent some, by id.
	Records []Record `json:"records,omitempty"`
	// Since is when the status last changed (unix ms).
	Since int64 `json:"since"`
	// Activity is when the program last wrote (unix ms).
	Activity int64 `json:"activity"`
	Created  int64 `json:"created"`
	ExitCode int   `json:"exitCode,omitempty"`
	// CommandExit is the exit status of the command the pane started
	// with, once it ended and the pane went on with its shell.
	CommandExit *int `json:"commandExit,omitempty"`
	// AgentSession is the coding agent's own session, as its hook told
	// (kero hook), while the agent runs: what a restore resumes.
	AgentSession *AgentSession `json:"agentSession,omitempty"`
	// Restored: the daemon restarted (as after a reboot) and this pane's
	// shell was started again in its directory, under its old scrollback.
	Restored bool `json:"restored,omitempty"`
	Cols     int  `json:"cols"`
	Rows     int  `json:"rows"`
	PID      int  `json:"pid,omitempty"`
	// Git is the git worktree the pane works in (WorkDir), nil for none,
	// or while the configuration's git-status is off.
	Git *GitInfo `json:"git,omitempty"`
	// Turn is where the pane's agent last started working: a snapshot of
	// its worktree, which its changes since are of.
	Turn *Turn `json:"turn,omitempty"`
}

// GitInfo is a git worktree's branch and changes, as kerod last found
// them. kerod replaces it whole, never changing one it sent.
type GitInfo struct {
	Root     string `json:"root"`             // the worktree's top level
	Branch   string `json:"branch,omitempty"` // "" when detached
	Head     string `json:"head,omitempty"`   // the commit, "" before the first
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
	// Default is the repository's default branch: origin's HEAD, else
	// main or master.
	Default string `json:"default,omitempty"`
	// Files changed since HEAD, untracked ones too, and the lines they
	// add and remove.
	Files   int `json:"files,omitempty"`
	Added   int `json:"added,omitempty"`
	Removed int `json:"removed,omitempty"`
	// State is an operation in progress: merge, rebase, cherry-pick or
	// revert.
	State string `json:"state,omitempty"`
}

// Turn is a snapshot of a worktree, as an agent's turn started from it.
type Turn struct {
	Tree string `json:"tree"` // the tree's id
	Root string `json:"root"` // the worktree's
	At   int64  `json:"at"`   // unix ms
}

// AgentSession is a coding agent's session: Claude Code's or Codex's,
// by its id.
type AgentSession struct {
	Agent string `json:"agent"` // "claude", "codex", "pi" or "opencode"
	ID    string `json:"id"`
}

// Conversation is a coding agent's conversation that ran in one of
// kerod's panes, as kerod remembers it after the pane is gone, to resume
// (kero history, kero resume).
type Conversation struct {
	Agent string `json:"agent"` // as AgentSession's
	ID    string `json:"id"`
	Dir   string `json:"dir,omitempty"` // where the agent worked
	// Project is the project of the session it ran in, Name that
	// session's name, the user's or an agent's, and Title what the
	// agent titled the conversation.
	Project string `json:"project,omitempty"`
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Started int64  `json:"started"` // unix ms
	Updated int64  `json:"updated"` // when it last ran
	// Session and Pane run it now; "" when none does.
	Session string `json:"session,omitempty"`
	Pane    string `json:"pane,omitempty"`
	// Archived: the user moved it to the sidebar's Archived, until it
	// runs again.
	Archived bool `json:"archived,omitempty"`
}

// Label is what to call a conversation: its session's name, else its
// title, else its directory's name.
func (c *Conversation) Label() string {
	switch {
	case c.Name != "":
		return c.Name
	case c.Title != "":
		return c.Title
	case c.Dir != "":
		return DirName(c.Dir)
	}
	return c.Agent
}

// AgentNames are the coding agents' names as they show them.
var AgentNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "pi": "Pi", "opencode": "OpenCode"}

// Session is a row of the sidebar: one task, with its terminals, its
// panes, side by side. Its Pane fields are those of its lead pane, the
// one that most needs the user (see Lead), so that a session reads as one.
type Session struct {
	Pane
	ID      string `json:"id"` // the session's, not the lead pane's
	Project string `json:"project"`
	Name    string `json:"name,omitempty"` // set by the user, or an agent
	// AgentNamed tells a Name an agent gave, for its conversation: the
	// next conversation in the session's first pane names it anew.
	AgentNamed bool `json:"agentNamed,omitempty"`
	// Panes are the session's terminals; Layout arranges them, nil for
	// the first alone.
	Panes  []Pane  `json:"panes"`
	Layout *Layout `json:"layout,omitempty"`
	// Lead is the pane the session's status comes from; Focus the pane
	// the user was in last; Zoomed the pane that fills the session, or "".
	Lead   string `json:"lead,omitempty"`
	Focus  string `json:"focus,omitempty"`
	Zoomed string `json:"zoomed,omitempty"`
	// Worktree is the linked git worktree kero made for the session to
	// work in, nil for none.
	Worktree *Worktree `json:"worktree,omitempty"`
}

// Worktree is a linked git worktree kero made for a session: Path checks
// out Branch, which started from Base, in the repository whose main
// checkout is Main.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Base   string `json:"base,omitempty"`
	Main   string `json:"main"`
}

// Losses writes what removing the worktree would lose, in English, as
// "3 files changed"; "" for nothing. Its branch stays unless merged, and
// keeps its commits.
func (wc *WorktreeCheck) Losses() string {
	switch {
	case wc.Files == 1:
		return "1 file changed"
	case wc.Files > 1:
		return fmt.Sprintf("%d files changed", wc.Files)
	}
	return ""
}

// PaneByID returns a pane of the session, or nil.
func (s *Session) PaneByID(id string) *Pane {
	for i := range s.Panes {
		if s.Panes[i].ID == id {
			return &s.Panes[i]
		}
	}
	return nil
}

// FocusPane returns the pane the user was in last, else the first.
func (s *Session) FocusPane() *Pane {
	if p := s.PaneByID(s.Focus); p != nil {
		return p
	}
	if len(s.Panes) > 0 {
		return &s.Panes[0]
	}
	return nil
}

// urgency orders statuses by how much they need the user.
func urgency(p *Pane) int {
	switch {
	case p.Status == Waiting:
		return 5
	case p.Status == Failed:
		return 4
	case p.Status == Exited && p.Unread:
		return 3
	case p.Unread:
		return 2
	case p.Status == Working:
		return 1
	}
	return 0
}

// Lead returns the pane a session's status comes from: the one that most
// needs the user, the focused one among equals.
func Lead(panes []Pane, focus string) int {
	best := -1
	for i := range panes {
		if best < 0 || urgency(&panes[i]) > urgency(&panes[best]) ||
			urgency(&panes[i]) == urgency(&panes[best]) && panes[i].ID == focus {
			best = i
		}
	}
	return best
}

// PaneName is the name to show for a pane: the title a program in the
// foreground set, else the program, else the shell, else its directory's
// name.
func (p *Pane) PaneName() string {
	if p.Browser() {
		if t := strings.TrimSpace(p.Title); t != "" {
			return t
		}
		if p.URL == BlankURL {
			return "browser"
		}
		return URLHost(p.URL)
	}
	if p.Process != "" {
		if t := CleanTitle(p.Title); t != "" && !looksLikePath(t) {
			return t
		}
		return p.Process
	}
	if p.Shell != "" {
		return p.Shell
	}
	if p.Dir != "" {
		if p.Dir == Home() {
			return "~"
		}
		return DirName(p.Dir)
	}
	return "shell"
}

// Browser reports whether the pane is a browser's, not a terminal's.
func (p *Pane) Browser() bool { return p.URL != "" }

// WorkDir is where the pane works: the foreground program's directory,
// else the shell's.
func (p *Pane) WorkDir() string {
	if p.ProcessDir != "" {
		return p.ProcessDir
	}
	return p.Dir
}

// What names a session the user or an agent didn't (settings'
// session-name).
const (
	// NameFocused: the program in the pane the user is in, else the
	// lead's: a shell alone names nothing its directory doesn't.
	NameFocused = "focused"
	NameLead    = "lead"   // the program in the pane that most needs the user
	NameFolder  = "folder" // the first pane's directory, whatever runs
)

// DisplayName is the name to show for a session: the user's or an agent's,
// else the title a program in the foreground set, else its directory's
// name; from, one of the Name constants, picks the pane it comes from.
func (s *Session) DisplayName(from string) string {
	if s.Name != "" {
		return s.Name
	}
	p := &s.Pane
	switch from {
	case NameFolder:
		if len(s.Panes) > 0 {
			p = &s.Panes[0]
		}
		p = &Pane{Dir: p.Dir}
	case NameLead:
	default:
		if f := s.FocusPane(); f != nil && f.Process != "" {
			p = f
		}
	}
	if p.Process != "" {
		if t := CleanTitle(p.Title); t != "" && !looksLikePath(t) {
			return t
		}
		if p.Agent != "" {
			return p.Agent
		}
	}
	// A session of browsers alone goes by its page: beside a terminal,
	// a browser names it no more than a shell does.
	if p.Browser() && !s.hasTerminal() {
		return p.PaneName()
	}
	if p.Dir != "" {
		if p.Dir == Home() {
			return "~"
		}
		return DirName(p.Dir)
	}
	return "shell"
}

func (s *Session) hasTerminal() bool {
	for i := range s.Panes {
		if !s.Panes[i].Browser() {
			return true
		}
	}
	return false
}

// CleanTitle strips the status glyphs agents put before their titles:
// spinners (braille, stars) and bullets.
func CleanTitle(t string) string {
	t = strings.TrimSpace(t)
	for {
		r, size := firstRune(t)
		if size == 0 || !isTitleGlyph(r) {
			break
		}
		t = strings.TrimSpace(t[size:])
	}
	return t
}

// TitleBusy reports whether a title shows a spinner, as agents' titles do
// while they work: a braille pattern, or a spinner's star.
func TitleBusy(t string) bool {
	r, _ := firstRune(strings.TrimSpace(t))
	return r > 0x2800 && r <= 0x28FF || strings.ContainsRune("◐◓◑◒", r)
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func isTitleGlyph(r rune) bool {
	switch {
	case r >= 0x2800 && r <= 0x28FF: // braille spinners
		return true
	case r >= 0x2700 && r <= 0x27BF: // dingbats: ✳ ✶ ✻ ✽ ✓ ✗
		return true
	case r >= 0x25A0 && r <= 0x25FF: // geometric shapes: ◇ ◆ ● ◐
		return true
	case r >= 0x2600 && r <= 0x26FF: // misc symbols: ⚡ ☐
		return true
	case strings.ContainsRune("·•*✦✧⏺⏵>»", r):
		return true
	}
	return false
}

// DirName is a directory's last element, whichever system's it is: the
// app shows a Windows host's C:\src\kero as kero on a Mac, where
// filepath.Base doesn't split at a backslash.
func DirName(dir string) string {
	t := strings.TrimRight(dir, `/\`)
	if i := strings.LastIndexAny(t, `/\`); i >= 0 && i < len(t)-1 && !strings.HasSuffix(t, ":") {
		return t[i+1:]
	}
	if t != "" && !strings.HasSuffix(t, ":") {
		return t
	}
	return dir
}

func looksLikePath(t string) bool {
	return strings.HasPrefix(t, "/") || strings.HasPrefix(t, "~") || strings.Contains(t, "@") && strings.Contains(t, ":") ||
		windowsPath(t) || consoleTitle(t)
}

// windowsPath tells a path of a drive: C:\Users.
func windowsPath(t string) bool {
	return len(t) > 2 && t[1] == ':' && (t[2] == '\\' || t[2] == '/')
}

// consoleTitle tells the title Windows gives a console, its program's
// path, after "Administrator: " in the user's language when it is
// elevated, and before " - ping" while cmd.exe runs a command:
// "管理员: C:\Program Files\PowerShell\7\pwsh.exe".
func consoleTitle(t string) bool {
	i := strings.Index(t, `:\`)
	if i < 1 || !windowsPath(t[i-1:]) {
		return false
	}
	if prefix := strings.TrimSpace(t[:i-1]); prefix != "" && !strings.HasSuffix(prefix, ":") {
		return false
	}
	exe, _, _ := strings.Cut(t[i-1:], " - ")
	return strings.HasSuffix(strings.ToLower(exe), ".exe")
}

// ShortDir writes a directory with ~ for the home directory.
func ShortDir(dir string) string {
	home := Home()
	if home != "" && Within(dir, home) {
		return "~" + dir[len(home):]
	}
	return dir
}

// Within reports whether path is dir or in it. Windows' paths are of any
// case.
func Within(path, dir string) bool {
	if len(path) < len(dir) || dir == "" {
		return false
	}
	head := path[:len(dir)]
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(head, dir) {
			return false
		}
	} else if head != dir {
		return false
	}
	rest := path[len(dir):]
	return rest == "" || os.IsPathSeparator(rest[0]) || os.IsPathSeparator(dir[len(dir)-1])
}

// Home returns the user's home directory.
func Home() string {
	h, _ := os.UserHomeDir()
	return h
}

// Name names kero's directories: "kero", or "kero-dev" for a development
// build, so that it runs a daemon, sessions and settings of its own beside
// the installed Kero.
func Name() string {
	if mygo.IsDev() {
		return "kero-dev"
	}
	return "kero"
}

// Dir returns kero's directory: $KERO_HOME, else
// ~/Library/Application Support/kero (~/.config/kero on Linux,
// %AppData%\kero on Windows), of Name. The app, the daemon and the CLI of
// a build share it.
func Dir() string {
	if d := os.Getenv("KERO_HOME"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = filepath.Join(Home(), ".config")
	}
	return filepath.Join(base, Name())
}

// SocketPath returns the daemon's socket: $KERO_SOCKET, else kerod.sock in
// Dir.
func SocketPath() string {
	if s := os.Getenv("KERO_SOCKET"); s != "" {
		return s
	}
	p := filepath.Join(Dir(), "kerod.sock")
	if len(p) > 100 {
		// Past what a Unix socket's address holds (104 bytes on macOS):
		// a short path of the user's, named after the directory.
		h := fnv.New32a()
		h.Write([]byte(Dir()))
		if runtime.GOOS == "windows" {
			// Its temporary directory is the user's.
			return filepath.Join(os.TempDir(), fmt.Sprintf("kero-%08x.sock", h.Sum32()))
		}
		p = fmt.Sprintf("/tmp/kero-%d-%08x.sock", os.Getuid(), h.Sum32())
	}
	return p
}

// StopEvent names the event that asks the daemon of a pid to stop on
// Windows, where it has no signals.
func StopEvent(pid int) string { return fmt.Sprintf(`Local\kerod-stop-%d`, pid) }

// Ago writes how long ago a time was, as the sidebar shows it: "now",
// "4m", "2h", "3d".
func Ago(ms int64, now time.Time) string {
	if ms == 0 {
		return ""
	}
	d := now.Sub(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return itoa(int(d/time.Hour)) + "h"
	case d < 7*24*time.Hour:
		return itoa(int(d/(24*time.Hour))) + "d"
	}
	return itoa(int(d/(7*24*time.Hour))) + "w"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Params of the control methods.
type (
	CreateParams struct {
		// Split, a session or a pane, makes a pane beside that pane (the
		// session's focused one) instead of a session; Direction is
		// where: right (the default), down, left or up, and Ratio the
		// share of the new pane, 0.5 when zero.
		Split     string  `json:"split,omitempty"`
		Direction string  `json:"direction,omitempty"`
		Ratio     float64 `json:"ratio,omitempty"`

		Project string   `json:"project,omitempty"` // id or name; "" the first
		Name    string   `json:"name,omitempty"`
		Dir     string   `json:"dir,omitempty"` // "" the project's, else home
		Command []string `json:"command,omitempty"`
		Env     []string `json:"env,omitempty"`
		// URL makes a browser pane showing it, instead of a shell.
		URL   string `json:"url,omitempty"`
		After string `json:"after,omitempty"` // put it after this session
		Cols  int    `json:"cols,omitempty"`
		Rows  int    `json:"rows,omitempty"`
		// Worktree starts the session in a linked git worktree of its own
		// of Dir's repository, on a new branch of this name from Base: the
		// repository's default branch when "", "HEAD" for Dir's. An
		// existing branch no worktree has is checked out as it is.
		Worktree string `json:"worktree,omitempty"`
		Base     string `json:"base,omitempty"`
	}
	IDParams struct {
		ID string `json:"id"`
	}
	// CloseParams closes a session; RemoveWorktree removes the worktree
	// kero made for it too, unless another session works in it, and
	// unless changes it would lose (WorktreeCheck.Files) need Force.
	CloseParams struct {
		ID             string `json:"id"`
		RemoveWorktree bool   `json:"removeWorktree,omitempty"`
		Force          bool   `json:"force,omitempty"`
	}
	// WorktreeCheck is what removing a session's worktree would lose:
	// Files changed, untracked ones too, and Unpushed commits on neither
	// its base nor a remote. Merged: its base has its branch, which goes
	// with it. Shared: another session works in it, which keeps it.
	WorktreeCheck struct {
		Worktree
		Files    int  `json:"files"`
		Unpushed int  `json:"unpushed"`
		Merged   bool `json:"merged"`
		Shared   bool `json:"shared"`
	}
	RenameParams struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		// By is the pane the rename comes from, when one does: a name
		// from a pane running an agent is the agent's.
		By string `json:"by,omitempty"`
	}
	MoveParams struct {
		ID      string `json:"id"`
		Project string `json:"project,omitempty"`
		Index   int    `json:"index"` // within the project; -1 last
	}
	SendParams struct {
		ID   string `json:"id"`
		Data string `json:"data"`
	}
	ReadParams struct {
		ID         string `json:"id"`
		Scrollback bool   `json:"scrollback,omitempty"`
		Lines      int    `json:"lines,omitempty"`
	}
	ViewParams struct {
		ID string `json:"id"` // "" when no window shows a session
	}
	DoParams struct {
		Action string            `json:"action"`
		Args   map[string]string `json:"args,omitempty"`
	}
	FocusParams struct {
		Session string `json:"session"`
		Pane    string `json:"pane"`
	}
	LayoutParams struct {
		Session string  `json:"session"`
		Layout  *Layout `json:"layout"`
	}
	// PageParams tell the page a browser pane shows, as the app's user
	// went to it (pane.page).
	PageParams struct {
		ID    string `json:"id"`
		URL   string `json:"url"`
		Title string `json:"title,omitempty"`
	}
	// BrowserParams ask a browser pane's page to do something (browser),
	// which the app showing it does.
	BrowserParams struct {
		ID string `json:"id"`
		// Op is what to do: open, back, forward, reload, text, html,
		// snapshot, click, type, wait, eval, screenshot or cookies.
		Op       string `json:"op"`
		Selector string `json:"selector,omitempty"`
		// Ref names the element a snapshot gave a ref, in place of a
		// selector.
		Ref string `json:"ref,omitempty"`
		// Text is the URL to open, the text to type or wait for, the
		// script to evaluate, or what to do with cookies: save or load.
		Text  string `json:"text,omitempty"`
		Enter bool   `json:"enter,omitempty"` // press Enter after typing
		// Domain keeps the cookies saved to those of a domain and its
		// subdomains.
		Domain string `json:"domain,omitempty"`
		// Cookies are the cookies to load.
		Cookies []Cookie `json:"cookies,omitempty"`
		// Timeout is how long to wait for the element, or the page to
		// load, in ms: 0 for the app's default.
		Timeout int64 `json:"timeout,omitempty"`
	}
	// BrowserRequest is a browser call as kerod hands it to the app: Req
	// is what the app answers it by, and Pane the browser pane's id.
	BrowserRequest struct {
		Req  int64  `json:"req"`
		Pane string `json:"pane"`
		BrowserParams
	}
	// BrowserResult is what the page answered: text for text, html,
	// snapshot and eval, an image for screenshot, the cookies saved.
	BrowserResult struct {
		Text    string   `json:"text,omitempty"`
		PNG     []byte   `json:"png,omitempty"`
		Cookies []Cookie `json:"cookies,omitempty"`
	}
	// Cookie is a cookie of the browser panes' pages, in the shape of
	// Playwright's storage state.
	Cookie struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		// Domain is the host the cookie goes to, or with a leading dot,
		// the domain whose subdomains get it too.
		Domain string `json:"domain"`
		Path   string `json:"path"`
		// Expires is when the cookie expires, in Unix seconds: -1 for one
		// that lasts until the app quits.
		Expires  float64 `json:"expires"`
		HTTPOnly bool    `json:"httpOnly"`
		Secure   bool    `json:"secure"`
		SameSite string  `json:"sameSite"` // Strict, Lax or None
	}
	// BrowserAnswer answers a BrowserRequest (browser.answer): a result,
	// or why there is none.
	BrowserAnswer struct {
		Req    int64          `json:"req"`
		Result *BrowserResult `json:"result,omitempty"`
		Error  string         `json:"error,omitempty"`
	}
	NotifyParams struct {
		ID    string `json:"id"`
		Title string `json:"title,omitempty"`
		Body  string `json:"body,omitempty"`
	}
	WaitParams struct {
		ID string `json:"id"`
		// Status waits for one of these; empty waits for the session to
		// stop working: idle, waiting or exited.
		Status []Status `json:"status,omitempty"`
		// Exit waits instead for the command the pane started with to
		// end (CommandExit), or for its shell to.
		Exit    bool  `json:"exit,omitempty"`
		Timeout int64 `json:"timeout,omitempty"` // ms; 0 waits as long as it takes
	}
	// ReportParams carry a program's status report to pane ID, as the
	// body of an OSC 7501 after "7501;": kero report sends it so when it
	// has no terminal to write it to, as under an agent's shell tool.
	ReportParams struct {
		ID     string `json:"id"`
		Report string `json:"report"`
	}
	// AgentSessionParams tell a pane's agent's session (kero hook).
	// The pane is the one whose shell runs PID, the hook's process, else
	// the one that can only be the agent's: an agent's service that panes
	// share, as Codex's app server, runs hooks under none.
	AgentSessionParams struct {
		PID     int    `json:"pid"`
		Agent   string `json:"agent"`
		Session string `json:"session"`
		Dir     string `json:"dir,omitempty"` // the agent's, to tell its pane by
	}
	// ConversationParams names a conversation (Conversation), to resume
	// in a session after After, of Cols and Rows, to forget, or to
	// archive or not, as Archived.
	ConversationParams struct {
		Agent    string `json:"agent"`
		ID       string `json:"id"`
		After    string `json:"after,omitempty"`
		Cols     int    `json:"cols,omitempty"`
		Rows     int    `json:"rows,omitempty"`
		Archived bool   `json:"archived,omitempty"`
	}
	// AgentSessionResult is the pane told of, and its session; Away: the
	// agent runs under none of kero's shells, and so do its commands.
	AgentSessionResult struct {
		Pane    string `json:"pane"`
		Session string `json:"session"`
		Away    bool   `json:"away,omitempty"`
	}
	ProjectParams struct {
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
		Dir  string `json:"dir,omitempty"`
	}
	MoveProjectParams struct {
		ID    string `json:"id"`
		Index int    `json:"index"`
	}
	ReadResult struct {
		Text string `json:"text"`
	}

	// GitParams ask about a pane's worktree, since a base: "turn" (its
	// Turn), "head" or "branch" (where HEAD left the default branch).
	// Path and OldPath name a file, slash-separated from the worktree's
	// root, the file of a rename was OldPath in the base.
	GitParams struct {
		Pane    string `json:"pane"`
		Base    string `json:"base"`
		Path    string `json:"path,omitempty"`
		OldPath string `json:"oldPath,omitempty"`
		// Known is the hash of the file's text the client has (GitText),
		// which git.file then sends again only when it changed.
		Known string `json:"known,omitempty"`
	}
	// GitChanges are the files changed since a base.
	GitChanges struct {
		Root string `json:"root"`
		// BaseName is what the base is to show: "HEAD", or the default
		// branch's name for "branch"; "" for a turn.
		BaseName string    `json:"baseName,omitempty"`
		Files    []GitFile `json:"files"`
	}
	GitFile struct {
		Path    string `json:"path"`
		OldPath string `json:"oldPath,omitempty"`
		// Status is added, modified, deleted, renamed, or type (a file
		// that became a link, or one that stopped being one).
		Status  string `json:"status"`
		Added   int    `json:"added,omitempty"`
		Removed int    `json:"removed,omitempty"`
		Binary  bool   `json:"binary,omitempty"`
	}
	// GitText is a file's text in the base and now. Same: as Known, so
	// nothing else is sent.
	GitText struct {
		Old    string `json:"old,omitempty"`
		New    string `json:"new,omitempty"`
		Hash   string `json:"hash"`
		Same   bool   `json:"same,omitempty"`
		Binary bool   `json:"binary,omitempty"`
		Large  bool   `json:"large,omitempty"`
		Patch  string `json:"patch,omitempty"`
	}
	// GitWriteParams write a file of a pane's worktree back as it was in
	// part (git.revert): Text, when its content still hashes to Hash.
	GitWriteParams struct {
		Pane string `json:"pane"`
		Base string `json:"base"`
		Path string `json:"path"`
		Hash string `json:"hash"`
		Text string `json:"text"`
	}
)
