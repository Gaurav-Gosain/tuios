package session

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"

	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"tuios.dev/vt/dnd"

	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/google/uuid"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
	"github.com/Gaurav-Gosain/tuios/internal/panescope"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/scrollclear"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/shellinteg"
	"tuios.dev/progstatus"
	"tuios.dev/vt"
	"tuios.dev/vt/snapshot"
)

func debugEnabled() bool {
	return debuglog.Enabled()
}

func debugLog(format string, args ...any) {
	if debugEnabled() {
		log.Printf(format, args...)
	}
}

// KeptFlags are the flags of an agent's command line that its manifest keeps
// across a resume (harness.Resume.KeepFlags and KeepOptions), such as
// --model opus, and the harness they were read from: a resume of any other
// harness ignores them. The detector reads them on the poll that already
// reads the agent's process, and replaces them each time it sees the harness
// again, so an agent started with no kept flags clears them. A value is
// replaced whole and never changed in place, since copies of a window share
// it.
type KeptFlags struct {
	Harness string   `json:"harness"`
	Flags   []string `json:"flags,omitempty"`
}

// WindowState represents the serializable state of a window.
type WindowState struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CustomName string `json:"custom_name,omitempty"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Z          int    `json:"z"`
	Workspace  int    `json:"workspace"`
	Minimized  bool   `json:"minimized,omitempty"`
	// MinimizedAt is when the window was minimized, in Unix nanoseconds, and
	// 0 while it is not. The daemon stamps it on every write that minimizes
	// a window and clears it on restore (stampMinimizedLocked), so every
	// client lists the minimized windows in one order, the order they were
	// minimized in, and a client push never sets it.
	MinimizedAt  int64  `json:"minimized_at,omitempty"`
	PreMinimizeX int    `json:"pre_minimize_x,omitempty"`
	PreMinimizeY int    `json:"pre_minimize_y,omitempty"`
	PreMinimizeW int    `json:"pre_minimize_w,omitempty"`
	PreMinimizeH int    `json:"pre_minimize_h,omitempty"`
	PTYID        string `json:"pty_id"`                  // Reference to daemon-managed PTY
	IsAltScreen  bool   `json:"is_alt_screen,omitempty"` // Alternate screen buffer active (for mouse forwarding)
	// IsFloating marks a window the user lifted out of the tiling. It is synced
	// because it is an input to every peer's layout arithmetic: a peer that does
	// not know a pane floats still counts it among the tiled panes, tiles it
	// back into the box, and pushes the result, which destroys the float on the
	// client that made it and moves every shared PTY twice. The zero value is a
	// tiled window, which is what every older client and older state reads as.
	IsFloating bool `json:"is_floating,omitempty"`
	// Zoomed marks a pane the user blew up to fill the content region. It is
	// synced for the reason IsFloating is, and for a second one that is not a
	// matter of taste: zooming resizes the shared PTY. toggleZoom routes the
	// zoomed rectangle through Window.Resize, and a PTY has one size, so a peer
	// that does not know a pane is zoomed counts it among the tiled panes,
	// tiles it back into the box and pushes that, which drops the zoom on the
	// client that asked for it and leaves every client drawing a guest grid the
	// shell is not running in. tmux shares resize-pane -Z across attached
	// clients for the same arithmetic.
	//
	// The flag travels; the rectangle does not. A zoomed pane covers the
	// content region of the client that zoomed it, and that box is that
	// client's own render size less the agreed reserve. A peer recomputes it
	// against its own bounds, the way it recomputes a tiled layout. See
	// applyZoomState. The zero value is an unzoomed window, which is what every
	// older client and older state reads as.
	Zoomed bool `json:"zoomed,omitempty"`
	// PreZoomX/Y/W/H are the rectangle the pane had before it was zoomed, and
	// they travel with the flag for the same reason the PreMinimize four do: any
	// client may be the one that unzooms, and under a floating or untiled layout
	// nothing else will ever put the pane back. Without them a peer's unzoom
	// restored a pane to 0x0.
	PreZoomX int `json:"pre_zoom_x,omitempty"`
	PreZoomY int `json:"pre_zoom_y,omitempty"`
	PreZoomW int `json:"pre_zoom_w,omitempty"`
	PreZoomH int `json:"pre_zoom_h,omitempty"`
	// Host names the machine this window's process runs on, and it is empty for
	// every window whose process is on the daemon that owns the session, which
	// is every window unless someone asked otherwise.
	//
	// A session holding one of these is what a global session is. There is no
	// second kind of session and no flag that says so: the field is on the
	// window because the machine is a property of the process, not of the
	// session it is grouped under, and that is what lets `tuios ls`, the verbs,
	// the mailbox and hooks keep working on such a session with no special
	// case. The zero value is a window on this machine, which is what every
	// older client and every older state file reads as.
	Host string `json:"host,omitempty"`
	// HostLink is the state of the link a window on another machine runs
	// over, empty while it is up: "reconnecting" while the link is lost and
	// the far machine keeps the process for its grace. HostLinkUntil is when
	// that grace ends, in unix seconds. Both are live facts, filled into every
	// snapshot and never stored. See remote_pane.go.
	HostLink      string `json:"host_link,omitempty"`
	HostLinkUntil int64  `json:"host_link_until,omitempty"`
	// HostEnded is why a pane on another machine cannot come back, set with
	// HostLink "ended". Like HostLink it is true only while it is true and
	// is filled from the live pane on every snapshot.
	HostEnded string `json:"host_ended,omitempty"`
	// ScreenStopped is set while the daemon has stopped feeding the pane's
	// terminal emulator after it failed again and again: the program runs
	// and the clients draw its output, but a new client, capture-pane and
	// find see the screen from before. A live fact, like HostLink. See
	// pane_vt_fault.go.
	ScreenStopped bool `json:"screen_stopped,omitempty"`
	// Recording is set while record-pane writes the pane to an asciicast
	// file. The rail marks the pane, so a person sees that what it prints
	// goes to a file. A live fact, like ScreenStopped. See pane_record.go.
	Recording bool `json:"recording,omitempty"`
	// Cwd is the working directory of the window's shell process, captured on the
	// daemon side when saving resurrection state. On cold-start restore a fresh
	// shell is respawned here. Empty for live state syncs (clients do not set it).
	Cwd string `json:"cwd,omitempty"`
	// StartDir is the folder the window's first process started in. The
	// daemon fixes it when it makes the window, and the shell's cd and OSC 7
	// never move it, so a grant can be held to it. Clients never set it.
	StartDir string `json:"start_dir,omitempty"`
	// Note is the pane's return note: one short line that says what the pane
	// is for or what comes next. Only the set-note verb writes it, so a client
	// sync never moves it, and it is saved with the session. Empty for none.
	Note string `json:"note,omitempty"`
	// CwdHost names the machine the pane's shell last reported its folder on,
	// when that is not the machine the pane runs on: the pane is running ssh.
	// Empty otherwise. A live fact, filled into every snapshot and never
	// stored. A client lists no files for such a pane, because Cwd is not a
	// folder on any disk it can reach.
	CwdHost string `json:"cwd_host,omitempty"`
	// DnDAccepts and DnDOffers say the pane's program registered for drag
	// and drop (OSC 72): it takes drops, or offers drags. DnDID is the
	// multiplexer id it used. A live fact like CwdHost: a client that
	// attached after the program started never saw the registration. See
	// dnd_registration.go.
	DnDAccepts bool `json:"dnd_accepts,omitempty"`
	DnDOffers  bool `json:"dnd_offers,omitempty"`
	DnDID      int  `json:"dnd_id,omitempty"`
	// DnDOwner is the process group the registration is bound to, which a
	// client on this machine checks as the daemon does.
	DnDOwner int `json:"dnd_owner,omitempty"`
	// Unplaced marks a window the daemon created whose X/Y/Width/Height are a
	// nominal box rather than a position anyone chose. The daemon has no viewport
	// and cannot place a window; a client that receives an unplaced window puts it
	// where it would have put one of its own and clears the flag on its next sync.
	//
	// The default is the safe one: a state without the field (an older client, or
	// resurrection state written before this existed) reads as placed, which is
	// exactly the pre-existing behavior of trusting the geometry as sent.
	Unplaced bool `json:"unplaced,omitempty"`
	// AgentState is the semantic state of an agent running in this pane
	// (working, needs_input, idle, done, errored). It is daemon-owned: a pane
	// reports it through the set-agent-state verb and clients never set it, so it
	// is retained across a client sync the same way Cwd and Options are. The zero
	// value (empty) is AgentStateNone and is omitted from serialized state, so
	// older state and older clients read back as "no agent", the pre-existing
	// behavior.
	AgentState AgentState `json:"agent_state,omitempty"`
	// AgentMessage is an optional short note the pane reported alongside its
	// state, e.g. what it is waiting for. Daemon-owned, like AgentState.
	AgentMessage string `json:"agent_message,omitempty"`
	// AgentKind is what sort of block a needs_input state is: "approval" for a
	// yes or no on something the agent proposed, "question" for one that wants
	// an answer in words, empty when the source did not say. It is taken from
	// the rule that matched, or guessed from the reported message, and is
	// meaningless in any other state. Daemon-owned like AgentState, and empty in
	// older state, which reads as "not said".
	AgentKind string `json:"agent_kind,omitempty"`
	// AgentWaitsOn is what the pane's screen says its agent still runs after
	// its turn, such as "1 background shell" (harness waits_on). It is read
	// on each screen look and shown only beside done and idle; see
	// agentWaitsOn.
	AgentWaitsOn string `json:"agent_waits_on,omitempty"`
	// AgentReason is why the state a reader is shown differs from AgentState,
	// empty when it does not. The one value is AgentReasonSilent: a pane whose
	// hooks reported working and then stopped reporting, shown as unknown
	// while AgentState stays working underneath (see agent_silent.go). The
	// next state the daemon sets clears it. Daemon-owned like AgentState.
	// Additive: an older peer drops it and keeps drawing working.
	AgentReason string `json:"agent_reason,omitempty"`
	// AgentStateAt is the unix-nano time AgentState was last set. It is stamped
	// daemon-side and drives the output-stall heuristic (see applyStallHeuristic).
	// A title or screen look that reads back its own claim unchanged does not
	// restamp it (see lookRepeatsClaim).
	AgentStateAt int64 `json:"agent_state_at,omitempty"`
	// AgentHarness is the harness id the reporting source named, empty when
	// nothing named one (the foreground detector never does). It is synced
	// because a client-side alert has to be able to say which harness stopped;
	// the ranked source that won stays daemon-side, where get-agent-state reads
	// it from the claim.
	AgentHarness string `json:"agent_harness,omitempty"`
	// CompletionSeq counts the turns this pane has finished: it goes up by one
	// each time the agent goes from working to idle, done or unknown after
	// working for a few seconds (see agent_turns.go). A client compares it with
	// the value it saw when its user last focused the pane to tell a finished
	// turn nobody has looked at. Daemon-owned like AgentState. Zero, which is
	// what an older daemon sends, means no turn has been counted.
	CompletionSeq uint64 `json:"completion_seq,omitempty"`
	// AgentSessionID is the harness's own id for the conversation running in
	// the pane, as a hook reported it (Claude Code's session_id, for one). It
	// is what a later resume names, and it is how a hook event from a nested
	// or foreign session is told apart from the pane's own. It is kept when the
	// agent exits, so the last conversation a pane ran can still be resumed,
	// and it is replaced when another session reports into the pane. It is
	// daemon-owned and never set by a client.
	AgentSessionID string `json:"agent_session_id,omitempty"`
	// AgentSessionHarness is the harness AgentSessionID belongs to, as the
	// report that set the id named it. It is kept with the id rather than read
	// from AgentHarness, because the harness attribution is cleared when the
	// agent leaves the pane and the id is not: a resume after a restart needs
	// both, and by then the agent is long gone. Daemon-owned like
	// AgentSessionID. Empty in state written before it existed, and a resume
	// then falls back to AgentHarness.
	AgentSessionHarness string `json:"agent_session_harness,omitempty"`
	// AgentFlags are the flags the pane's agent was started with that its
	// manifest keeps across a resume. Kept when the agent exits, like
	// AgentSessionID. Daemon-owned. A pointer, because WindowState is copied
	// by value all over the daemon and this is rarely set.
	AgentFlags *KeptFlags `json:"agent_flags,omitempty"`
	// AgentMeta is what the pane reported about its agent through
	// set-agent-meta (model, context, cost, a summary), in the order the keys
	// first arrived. Daemon-owned like AgentState, display only, and cleared
	// when the agent leaves the pane. Additive: an older peer drops it and a
	// state without it reads as "the pane said nothing". See agent_meta.go.
	AgentMeta []AgentMetaToken `json:"agent_meta,omitempty"`
	// AgentQueued is how many messages wait in the pane's delivery queue to be
	// typed when its agent comes to rest (queue-prompt). Daemon-owned like
	// AgentMeta and never set by a client. Additive: zero, which is what an
	// older daemon sends, means nothing is queued. See verb_queue.go.
	AgentQueued int `json:"agent_queued,omitempty"`
	// AgentDraftHeld is true while the person's draft in the agent's input
	// box holds the next queued message (agent_queue_draft.go). Daemon-owned
	// like AgentQueued. Additive: false, which is what an older daemon sends,
	// means nothing is held.
	AgentDraftHeld bool `json:"agent_draft_held,omitempty"`
	// AgentSettled marks an agent the person put away: the agents view draws
	// it on the Settled shelf until it needs the person again. Only
	// mark-attention settle and unsettle set it, and the daemon clears it when
	// the agent asks, errors, finishes a turn, sends the person mail or leaves
	// the pane (see agent_settle.go). Daemon-owned like AgentQueued and never
	// set by a client. Additive: false, which is what an older daemon sends,
	// means not settled.
	AgentSettled bool `json:"agent_settled,omitempty"`
	// AgentQuiet marks a pane whose finished turns, questions and failures do
	// not reach the person's Inbox or the client's alerts. The agent state and
	// its events do not change, and an approval still reaches the person. Only
	// set-agent-quiet sets it (see agent_quiet.go). Daemon-owned like
	// AgentQueued and never set by a client. Additive: false, which is what an
	// older daemon sends, means not quiet.
	AgentQuiet bool `json:"agent_quiet,omitempty"`
	// AgentSubagents is how many subagents the pane's agent is running, as its
	// hooks reported them (see agent_subagents.go). Daemon-owned like
	// AgentQueued and never set by a client. Additive: zero, which is what an
	// older daemon sends, means none.
	AgentSubagents int `json:"agent_subagents,omitzero"`
	// AgentSubagentList is the subagents themselves, running and finished a
	// moment ago, in start order: what each was asked to do, the tool it
	// runs, and how it ended (see agent_subagents.go). At most
	// subagentListMax, with bounded strings. Daemon-owned like
	// AgentSubagents and never set by a client: a push's copy is replaced
	// with the daemon's. Additive: an older peer drops it on decode, and an
	// empty list beside a count, which is what an older daemon sends, means
	// the daemon did not say, so a client draws the count alone.
	AgentSubagentList []SubagentInfo `json:"agent_subagent_list,omitempty"`
	// ProgramStatus is the pane's OSC 7501 records (the Program Status
	// Protocol), the root first, with title and msg already made safe to
	// draw. Daemon-owned like AgentMeta and never set by a client. Additive:
	// an older peer drops it on decode, and nil, which is what an older
	// daemon sends, means the pane reported nothing. See program_status.go.
	ProgramStatus []ProgramStatusRecord `json:"program_status,omitempty"`
	// Popup marks a transient floating pane that runs one command and closes
	// when the command exits. It is session state, not a client's own, for the
	// two reasons IsFloating and Zoomed are: a peer that does not know the pane
	// is a popup counts it among the tiled panes and tiles it back into the box,
	// which destroys the popup on the client that opened it; and the box sizes
	// the shared PTY, so two clients disagreeing about it hand one shell two
	// sizes. A popup is always floating, so both flags travel together.
	//
	// The flag travels; the rectangle does not. See PopupWidth below. The zero
	// value is an ordinary window, which is what every older client and older
	// state reads as.
	Popup bool `json:"popup,omitempty"`
	// PopupWidth and PopupHeight are the size the caller asked for, as the
	// caller wrote it: "60" for cells, "60%" for a share of the content region.
	// The request travels rather than the resolved box because the box is
	// measured against one client's content region, and a peer of another size
	// measures the same request against its own. This is the split zoom makes
	// between its flag and its rectangle, applied to a size the user chose.
	//
	// Empty means the caller named no size and the client uses
	// PopupDefaultWidth or PopupDefaultHeight.
	PopupWidth  string `json:"popup_width,omitempty"`
	PopupHeight string `json:"popup_height,omitempty"`
	// PopupDim is daemon-owned: nil inherits the client's popup or modal dim;
	// a present zero disables it. The struct pointer preserves zero over gob.
	PopupDim *config.PopupDim `json:"popup_dim,omitempty"`
	// Scratch marks the session's scratch terminal: the one shell the
	// toggle_scratch key shows as a popup and hides again. A hidden scratch
	// terminal is Minimized, and every list a user reads leaves it out, the
	// dock's minimized entries above all. There is at most one per session.
	// It is set at creation by the popup verb and, like Popup, is the
	// daemon's: a push cannot set or clear it.
	Scratch bool `json:"scratch,omitempty"`
	// ScratchName keys a scratch pane: empty or "scratch" for the built-in
	// scratch terminal, the entry's name for a [[keybindings.command]] entry
	// of type scratch. A session has at most one scratch pane per name. It is
	// the daemon's, like Scratch.
	ScratchName string `json:"scratch_name,omitempty"`
	// Rail marks the pane a client draws in its rail's pane section, and
	// holds the command line the client's config gave it, which is how a
	// client tells its own pane from one another config asked for. Empty for
	// every other window. The pane sits on a scratch-range workspace that no
	// client shows, so no layout, tiler or workspace list reaches it, and
	// the lists a user reads leave it out. It is set at creation by the
	// rail-pane verb and is the daemon's, like Scratch: a push cannot set or
	// clear it. See verb_rail_pane.go.
	Rail string `json:"rail,omitempty"`
	// Stack and StackIndex put the window in a stack: panes that share one
	// tile of the split tree, one open and the others as title rows. Every
	// client lays the stack out from them, as from IsFloating, so a client
	// pushes them. The zero values are a window in no stack, which is what
	// every older client and state reads as.
	Stack      string `json:"stack,omitempty"`
	StackIndex int    `json:"stack_index,omitempty"`
	// Kind says what draws the window: "terminal" (empty), or for a window
	// a renderer draws natively, "view", "web" or "app" (plan 5.1). URI names
	// what such a window shows, such as tuios://review/<pane> or a page. Both
	// are set when the window is made and are the daemon's, like Scratch: a
	// push can neither set nor clear them. Such a window still has a PTY, and
	// its program prints a one-line placeholder for a terminal client.
	Kind string `json:"kind,omitempty"`
	URI  string `json:"uri,omitempty"`
	// ForegroundCmd is the base name of the program running in the pane's
	// foreground, empty while the pane sits at its login shell. It is what lets a
	// row say "nvim" instead of repeating a title every pane in one directory
	// shares. Daemon-owned and refreshed by the agent detector's existing poll,
	// so it costs no extra process reads and may be one poll out of date, which
	// is fine for a label.
	ForegroundCmd string `json:"foreground_cmd,omitempty"`
	// SSHOffer is set while the pane's foreground program is an interactive
	// ssh or mosh login to a machine that no [hosts] entry reaches, so the
	// client can offer to add it. Nil otherwise. Daemon-owned and refreshed
	// by the detection poll, like ForegroundCmd. See ssh_offer.go.
	SSHOffer *SSHOffer `json:"ssh_offer,omitempty"`
	// ShellPID is the process id of the pane's shell, as the daemon that spawned
	// it knows it. Zero means the daemon has no live process for the pane, which
	// is every window with no PTY and every window on a build that predates the
	// field.
	//
	// It is here so the client can corroborate the directory the pane reports
	// over OSC 7 against the one the kernel holds for its shell (see
	// app.cwdIsSpoofed). The pane writes the OSC 7 string, so it is the pane's
	// word; the shell's working directory is the kernel's, and it is the only
	// second source there is. Without this a daemon-backed pane, which is every
	// pane in the default deployment, had nothing to check against and a pane
	// could steer the rail's delete at a folder it is not in.
	//
	// The pid rather than a daemon-resolved path, for two reasons. A path stamped
	// on the detector's poll is up to one poll old, so every `cd` would read as a
	// disagreement for two seconds and blink the file actions off; the pid does
	// not go stale, because the client reads /proc at the moment it lists. And
	// the pid costs nothing to collect: it is a field on the PTY, not a read.
	//
	// The client process always runs on the pane machine, in every deployment
	// tuios has, including `tuios ssh` and `tuios-web` (see app/link_open.go), so
	// a pid from the daemon names a process the client can read.
	//
	// json:"-" keeps it off disk. Resurrection state is JSON and outlives the
	// processes it describes, so a pid saved there would name whatever process
	// later reused the number, and the client would corroborate against a
	// stranger. The wire is gob, which ignores the json tag, so the field still
	// travels. It is stamped where the PTY is created, so a new pane is checkable
	// at once, and refreshed on the agent detector's poll, like ForegroundCmd.
	// The poll is the authority: it corrects the stamp and clears it when the
	// shell goes.
	ShellPID int `json:"-"`
	// Grants is what the pane's process may do through tuios, by grant name,
	// when it was given grants of its own: at start (start-agent, fan and
	// new-window take grants) or later with set-pane-grants. Nil means it
	// holds the default of [agents.permissions]; ["none"] means it holds
	// nothing. Daemon-owned: the daemon enforces the grants from its
	// own table (pane_grants.go), a client sync can neither set nor clear
	// this copy, and it is saved so a restored pane holds what it held.
	// Older peers and older state read it as absent, which is the default.
	Grants []string `json:"grants,omitempty"`
	// StartedBy names the pane whose call created this one: a process in
	// that pane ran new-window, split-window, start-agent, fan or
	// new-worktree. Nil when the call came from outside every pane or over a
	// link. Daemon-owned like Grants: a client sync can neither set nor clear
	// it, and it is saved so a restored pane keeps it. Older peers and older
	// state read it as absent.
	StartedBy *PaneRef `json:"started_by,omitempty"`
	// AgentLink is set on an agent pane another agent started: StartedBy is
	// then its parent, and this is the rest of the chain. Nil for every other
	// pane. Daemon-owned like StartedBy, and saved, so a restored or handed
	// over pane keeps it. See agent_parent.go.
	AgentLink *AgentLink `json:"agent_link,omitempty"`
	// Delegation is set on an agent pane the delegate verb started: the
	// task's role, how it runs, and where it is. Nil for every other pane.
	// Daemon-owned like AgentLink, and saved, so a handed over pane keeps
	// it. See verb_delegate.go.
	Delegation *Delegation `json:"delegation,omitempty"`
}

// Delegation is the record of a task one agent handed to a child agent with
// delegate. It is written when the child gets its prompt and changed only by
// the daemon: when the child asks a question, and once when it finishes.
// The record is replaced on every change, never written in place, because
// older copies of the state share it.
type Delegation struct {
	// Role names the job: implementation, research, review, design, test
	// or general.
	Role string `json:"role"`
	// Isolation is shared, the parent's checkout, or worktree, a worktree
	// made from the parent's commit.
	Isolation string `json:"isolation"`
	// Mode is async, the call returned at once, or wait, the call waited
	// for the outcome.
	Mode string `json:"mode"`
	// By is the pane whose call delegated the task, empty for a call from
	// outside every pane. A wake of the parent is typed with its authority.
	By string `json:"by,omitempty"`
	// Parent is the pane the result is mailed to, nil when there is none: a
	// delegate with wait from outside every pane.
	Parent *PaneRef `json:"parent,omitempty"`
	// Worktree is the worktree the child works in, and Base the commit it
	// was made from, for worktree isolation.
	Worktree string `json:"worktree,omitempty"`
	Base     string `json:"base,omitempty"`
	// StartedAt is the unix-milli time the prompt was typed. A state the
	// child reaches after it is the child's answer to the task.
	StartedAt int64 `json:"started_at"`
	// State is working, asking, done, errored, closed or stopped.
	// FinishedAt is the unix-milli time it became done, errored or stopped,
	// zero before.
	State      string `json:"state"`
	FinishedAt int64  `json:"finished_at,omitempty"`
	// AskedAt is the unix-milli time the child came to a question, zero
	// while it asks none. QuestionMail is the message that told the parent,
	// zero when no message was sent (an approval, or a call that waited).
	AskedAt      int64  `json:"asked_at,omitempty"`
	QuestionMail uint64 `json:"question_mail,omitempty"`
	// ResultMail is the message that carried the answer to the parent, zero
	// when none was sent.
	ResultMail uint64 `json:"result_mail,omitempty"`
}

// AgentLink is what a child agent pane knows of the agent that started it,
// beyond the parent pane itself (WindowState.StartedBy).
type AgentLink struct {
	// Harness is the parent's harness id when the child started, empty when
	// nothing named one.
	Harness string `json:"harness,omitempty"`
	// AgentSessionID is the parent harness's own conversation id when the
	// child started, empty until a hook reported one.
	AgentSessionID string `json:"agent_session_id,omitempty"`
	// Root is the first agent of the chain: the parent's root, or the parent
	// when it has none.
	Root PaneRef `json:"root"`
	// Depth is how many links lead from the root to this pane: 1 for a child
	// of the root. It is at most agentLinkMaxDepth.
	Depth int `json:"depth"`
}

// PaneRef names a pane by its session and window id.
type PaneRef struct {
	Session string `json:"session"`
	Window  string `json:"window"`
}

// SerializedBSPNode represents a BSP tree node for serialization
type SerializedBSPNode struct {
	WindowID   int                `json:"window_id"`
	SplitType  int                `json:"split_type"`
	SplitRatio float64            `json:"split_ratio"`
	Left       *SerializedBSPNode `json:"left,omitempty"`
	Right      *SerializedBSPNode `json:"right,omitempty"`
}

// SerializedBSPTree represents a BSP tree for serialization
type SerializedBSPTree struct {
	Root         *SerializedBSPNode `json:"root,omitempty"`
	AutoScheme   int                `json:"auto_scheme"`
	DefaultRatio float64            `json:"default_ratio"`
}

// SessionState represents the complete serializable state of a session.
type SessionState struct {
	Name string `json:"name"`
	// Tasks are the session's task cards, oldest first, and NextTaskID
	// numbers the next one. Daemon-owned: only the task verbs write them,
	// and retainDaemonExclusive keeps them through a client sync. They are
	// saved, so a card and its history survive a restart. See task.go.
	Tasks      []TaskState `json:"tasks,omitempty"`
	NextTaskID uint64      `json:"next_task_id,omitempty"`
	// DisplayName is an optional user-facing label. Name stays the identity: it
	// keys the session map, names the resurrection file, is exported as
	// TUIOS_SESSION into every pane, and is what every verb's session parameter
	// resolves against. Renaming for display must not disturb any of that, which
	// is why the label is a field of its own rather than a write to Name.
	//
	// Empty means unnamed, and every reader falls back to Name, so a session that
	// was never renamed reads exactly as it did before this field existed.
	DisplayName string `json:"display_name,omitempty"`
	// WindowMoves is the newest windows that moved between this session and
	// another (see window_move_session.go). A client that sees a window leave
	// or arrive in a push looks it up here, and says the window moved rather
	// than that it closed or was made. Daemon-owned and never saved.
	WindowMoves []WindowMove `json:"-"`
	// SidebarWidth and SidebarCollapsed are the rail as the user last left it.
	// They are session state rather than a per-client preference because the
	// rail is chrome, and chrome that differs between two clients of one session
	// is chrome they cannot both draw: the panes' box is settled across every
	// client (see LayoutReserve), so a rail one client folded and another did
	// not costs the wider one a blank band. Sharing them means the usual case is
	// that there is nothing to reconcile.
	//
	// A zero width means the client had no stored preference and is not a
	// request to narrow anyone's rail. Collapsed is a plain flag, and its zero
	// value (an open rail) is what state written before this existed reads as,
	// which is the pre-existing behaviour.
	SidebarWidth     int  `json:"sidebar_width,omitempty"`
	SidebarCollapsed bool `json:"sidebar_collapsed,omitempty"`
	// Sidebar is whether the session shows the rail: SidebarShown,
	// SidebarHidden, or empty when no client has said yet. Only
	// MsgSidebarVisibility writes it (see sidebar_visibility.go); a push never
	// carries it, and retainDaemonExclusive keeps the daemon's copy whatever a
	// push holds. A client too old to read it keeps its own rail, which is
	// what every client did before the field existed.
	Sidebar string `json:"sidebar,omitempty"`
	// LayoutGen is the layout generation the pushing client tiled its
	// windows in: the generation of the last session resize it took. The
	// daemon keeps a push's rectangles out when it is older than the
	// session's, and clears the field. Zero is a client too old to say. See
	// layout_gen.go.
	LayoutGen uint64 `json:"layout_gen,omitempty"`
	// DaemonLayout is the Version at which the daemon last changed a split
	// tree or a zoom itself (split-window, zoom-window, swap-window,
	// resize-window, move-window --beside), zero when it never has. A client
	// that applies a state carrying a newer one than it has seen sends its
	// rectangles of the new layout, which the daemon does not compute while
	// a client shows the session. A tree that another client changed leaves
	// it alone, so that client's push is not answered with a stale one. A
	// push does not carry it.
	DaemonLayout int `json:"daemon_layout,omitempty"`
	// Accent is an optional accent for the session, recorded verbatim the way
	// Options are: the daemon has no palette and does not interpret it. Clients
	// read it as a colour name from the ANSI sixteen or as a hex literal, and an
	// empty or unreadable value means they pick the session's colour themselves.
	// It lives here rather than in a client-side file because it has to survive a
	// reattach and be the same for every client attached to this session.
	Accent string `json:"accent,omitempty"`
	// Note is the session's return note: one short line that says what the
	// session is for or what comes next, shown when a person comes back to
	// it. Only the set-note verb writes it, so canonical state wins over any
	// client sync, and it is saved with the session. Empty for none.
	Note string `json:"note,omitempty"`
	// Restored marks a session the daemon rebuilt from saved state and that no
	// client has attached to since. Nothing else at session level says so: the
	// layout is back but every shell under it is new, and the only evidence was
	// a per-pane banner that scrolls away. The daemon sets it on restore and
	// clears it on the first attach, so it answers "why is this session here"
	// for exactly as long as the question is unanswered.
	//
	// Daemon-owned: clients never send it, and false is what every older client
	// and every pre-existing state file reads back as.
	Restored bool `json:"restored,omitempty"`
	// Global marks a session that is meant to hold panes from more than one
	// machine. Nothing in the daemon behaves differently for one: the panes on
	// other machines work the same way in any session, and a global session
	// with only local panes is an ordinary session. What the mark does is tell
	// the clients where to file it, which is its own group in the rail rather
	// than under the machine whose daemon happens to be holding it.
	//
	// Daemon-owned: it is set when the session is created, or later by
	// set-session-space with global, so a client sync that omits it must not
	// clear it. False is what every
	// older client and every state file written before this reads back as.
	Global bool `json:"global,omitempty"`
	// Space is the name of the group of related sessions this one is filed
	// under, or empty for a session in none. It is only a label: a space has no
	// record of its own and ends when its last session leaves it. A global
	// session is never in one.
	//
	// Daemon-owned: only set-session-space and new-session write it, so a
	// client sync never moves it, and an empty value from a client is not a
	// request to leave. Empty is what every older client and every state file
	// written before it reads back as.
	Space string `json:"space,omitempty"`
	// Rank is the session's place in the session order every client lists:
	// 1 is first. Zero is a session nobody placed, which lists after every
	// placed one, in creation order. One order for the daemon, so a session
	// moved up on one client's rail moves on every rail, the phone's over SSH
	// too, and stays there across a daemon restart.
	//
	// Daemon-owned: only set-session-order writes it, so a client sync never
	// moves it. Zero is what every older client and every state file written
	// before it reads back as.
	Rank int `json:"rank,omitempty"`
	// StartDir is the session's start directory (see Session.startDir), as
	// it was when the state was saved. ResurrectionState stamps it, and a
	// restore sets it again, so the chosen start directory survives a daemon
	// restart. Only the save reads this field:
	// a client push may carry a copy of it into canonical state, and
	// ResurrectionState writes the in-memory value over that copy before
	// every save. Empty in state written before it existed, and such a
	// session starts its new windows where the daemon did.
	StartDir string `json:"start_dir,omitempty"`
	// LastUsed is when the person last used the session, in unix
	// nanoseconds (see Session.lastUsed), as it was when the state was saved.
	// ResurrectionState stamps it and a restore sets it again, so a bare
	// attach after a daemon restart still lands on the session the person
	// used last. Only the save and the restore read it. Zero in state written
	// before it existed, and for a session nobody has used.
	LastUsed int64 `json:"last_used,omitempty"`
	// KittyGraphics and SixelGraphics are the graphics the session's last
	// attached client draws (Session.GraphicsCapabilities), as they were when
	// the state was saved. ResurrectionState stamps them and a restore sets
	// them again before it spawns a shell. The restore runs before any client
	// attaches, so without them every restored shell was told
	// TERM_PROGRAM=TUIOS where it had been told ghostty, and Claude Code
	// resumed in it lost Shift+Enter (see guestenv.TermProgram).
	KittyGraphics bool `json:"kitty_graphics,omitempty"`
	SixelGraphics bool `json:"sixel_graphics,omitempty"`
	// Worktree is the daemon's record of the git worktree this session's
	// directory is, or nil for a session that is not in one. Daemon-owned and
	// omitted when nil, which is what every older client and state file reads.
	// See worktree.go.
	Worktree         *WorktreeInfo  `json:"worktree,omitempty"`
	Windows          []WindowState  `json:"windows"`
	FocusedWindowID  string         `json:"focused_window_id,omitempty"`
	CurrentWorkspace int            `json:"current_workspace"`
	WorkspaceFocus   map[int]string `json:"workspace_focus,omitempty"` // workspace -> focused window ID
	// FocusHistory is newest-first per workspace. Unlike WorkspaceFocus, which
	// names the pane currently selected there, it lets a close return to the
	// pane the user visited immediately before it.
	FocusHistory map[int][]string `json:"focus_history,omitempty"`
	// FocusUnmoved marks a push whose focus is the one its client was last
	// given: nobody at that client moved the focus, the workspace or the
	// strip since. Such a push says nothing about the focus, so the daemon
	// keeps the session's. A client pushes for reasons of its own, such as a
	// new layout generation, and its push could reach the daemon after a
	// peer's focus move it had not seen yet. Taken as sent, it moved the focus
	// back for everyone. False from an older client, whose push is taken as
	// sent. Read and cleared by the daemon, never stored.
	FocusUnmoved bool `json:"focus_unmoved,omitempty"`
	// FocusByCommand marks a push whose focus a command moved, not the
	// person at the client: a routed run-command, or keys sent to the client
	// with send-keys or a tape. Every pane may run run-command by default, so
	// a move like that is an agent's, and it must not end the person's
	// approval hold as if they had turned to the pane (approvals.go,
	// noteFocused). False from an older client. Read and cleared by the
	// daemon, never stored.
	FocusByCommand bool `json:"focus_by_command,omitempty"`

	MasterRatio float64 `json:"master_ratio"`
	AutoTiling  bool    `json:"auto_tiling"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`

	// WorkspaceNames maps a workspace number to its optional label. A workspace
	// with no entry here is unnamed and renders as its number. Naming one is a
	// daemon-owned change so it survives a reattach and every attached client
	// sees it. A workspace's number is its position, and a move renumbers the
	// name with the rest of the workspace (see workspace_renumber.go).
	WorkspaceNames map[int]string `json:"workspace_names,omitempty"`
	// IconNames maps a window ID to the name its program gave with OSC 1, the
	// one an unnamed workspace shows while the window is its active one. OSC 0
	// does not set it (vt.Callbacks.ExplicitIconName). It is a live fact,
	// filled into every snapshot from the daemon's emulators and never stored,
	// so a value a client sends back is dropped. It is a map on the session
	// and not a field of WindowState because the daemon copies WindowState by
	// value in hundreds of places, and a field there grew each one.
	IconNames map[string]string `json:"icon_names,omitempty"`
	// WorkspaceOrder is a display order saved by a build that arranged the
	// workspaces without renumbering them. It is read so such a state still
	// loads, applied once as a renumber when the session is restored, and never
	// written: a move renumbers the workspaces instead.
	WorkspaceOrder []int `json:"workspace_order,omitempty"`
	// WorkspaceTabKeys is, by workspace number, the herdr tab number of the
	// workspace: a number of its own that moves with it through a renumber, so
	// a herdr tab id read before a move names the same tab after it. Nil until
	// a session is first renumbered or a tab closes, and then every
	// workspace's key is its number. Daemon-owned: a client push never sets
	// it. See workspace_renumber.go.
	WorkspaceTabKeys map[int]int `json:"workspace_tab_keys,omitempty"`
	// NextWorkspaceTabKey is the next tab key to hand out. Keys are never
	// reused, as herdr's tab numbers are not.
	NextWorkspaceTabKey int `json:"next_workspace_tab_key,omitempty"`

	WorkspaceTrees  map[int]*SerializedBSPTree `json:"workspace_trees,omitempty"`  // BSP tree per workspace
	WindowToBSPID   map[string]int             `json:"window_to_bsp_id,omitempty"` // Window UUID -> BSP int ID
	NextBSPWindowID int                        `json:"next_bsp_window_id,omitempty"`
	TilingScheme    int                        `json:"tiling_scheme,omitempty"` // Default auto-insertion scheme
	// WorkspaceMasterRatio is the master-stack split, keyed by workspace, the way
	// this state already keys the focus, the names, the order and the BSP trees.
	// The ratio is the session's rather than any one client's (a PTY has one
	// size, which is why MasterRatio was settled here to begin with), and a
	// workspace is what a ratio belongs to: BSP split ratios have been
	// per-workspace state for as long as they were carried at all, and
	// master-stack's single flat value was the odd one out. Each client kept a
	// private per-workspace memory of it and never sent it, so a client that had
	// not visited a workspace laid that workspace out at its own configured ratio
	// and pushed the result back, taking another client's tuned value away from
	// the whole session.
	//
	// Absent means what state written before this field existed meant: MasterRatio
	// alone, which is the ratio in force on the workspace CurrentWorkspace names.
	// A peer too old to send this is read exactly that way and behaves as it
	// always did. MasterRatio stays beside this and keeps that same meaning, so an
	// older peer reading this state still gets the one value it understands. An
	// entry-less map says the same nothing as an absent one and is read the same
	// way, which matters because gob drops a nil map and sends an empty one, so
	// both forms reach a reader.
	WorkspaceMasterRatio map[int]float64 `json:"workspace_master_ratio,omitempty"`
	// WorkspaceStackRatio is the other split of the three pane master-stack
	// layout, keyed by workspace: the top stacked pane's share of the height. It
	// is session state for the reason WorkspaceMasterRatio is, and it follows the
	// same rules. Absent, or with no entry for a workspace, means the stacked
	// panes split the height equally, which is what every client did before the
	// field existed, so an older peer that never sends it changes nothing.
	WorkspaceStackRatio map[int]float64 `json:"workspace_stack_ratio,omitempty"`
	// WorkspaceMasterSplits is the size of every other master-stack pane a
	// user resized, keyed by workspace: the masters' shares, the stack's, and
	// the grid's rows and cells. See layout.MasterSplits. It is session state
	// for the reason WorkspaceMasterRatio is and follows the same rules. A
	// workspace with no entry shares that space equally, which is what every
	// client did before the field existed, so an older peer that never sends
	// it changes nothing.
	WorkspaceMasterSplits map[int]layout.MasterSplits `json:"workspace_master_splits,omitempty"`
	// WorkspaceLayoutModes is which tiling layout each workspace chose, keyed by
	// workspace. A workspace gets an entry when a layout row names it directly,
	// so a session can run stacked in one workspace and scrolling in another.
	// Absent, or with no entry for a workspace, means the workspace follows the
	// session's LayoutMode, which is what every client did before the field
	// existed, so an older peer that never sends it changes nothing.
	WorkspaceLayoutModes map[int]string `json:"workspace_layout_modes,omitempty"`
	// WorkspaceMasterLayout is each workspace's master-stack shape: where the
	// masters go and how many there are. Only MsgMasterLayout writes it (see
	// master_layout.go); a push never carries it, and retainDaemonExclusive
	// keeps the daemon's copy whatever a push holds. A workspace with no entry
	// is laid out with the configured default, which is what every client did
	// before the field existed.
	WorkspaceMasterLayout map[int]MasterLayoutState `json:"workspace_master_layout,omitempty"`
	// WorkspaceHasCustom says, per workspace, whether the panes there sit where a
	// user put them rather than where the tiler would. It is what the retile on a
	// workspace switch is skipped on, and it is the session's answer for the same
	// reason the master ratio above is: a PTY has one size, so two clients showing
	// one workspace have to agree about whether the tiler owns it.
	//
	// It was each client's own before this. A client that had never been to a
	// workspace held no entry for it, read that as "not custom", retiled the
	// workspace on its first visit and pushed the tiler's rectangles, which took
	// away, for every client, the layout another client had arranged by hand.
	//
	// The rectangles themselves are not carried with it. They are already here,
	// on WindowState.X/Y/W/H, for every window on every workspace, and a second
	// copy of them keyed by workspace would be the older of two answers to one
	// question. This says which of the two authorities owns the rectangles that
	// are already on the wire, and nothing more.
	//
	// Absent means what state written before this field existed meant: no
	// workspace is custom, which is how a client with no entries behaved and how
	// an older peer still behaves. An entry-less map says the same nothing and is
	// read the same way, which matters because gob drops a nil map and sends an
	// empty one, so both forms reach a reader. An entry that is present and false
	// is a client saying a workspace stopped being custom, which is not the same
	// as saying nothing, and the merges below keep the two apart.
	WorkspaceHasCustom map[int]bool `json:"workspace_has_custom,omitempty"`
	// LayoutMode is which tiling layout the session uses: "bsp", "master-stack"
	// or "scrolling". It sits beside the BSP topology it selects between, which
	// was already carried here; without it a scrolling session came back as a BSP
	// one on reattach, because the topology survived and the mode that reads it
	// did not.
	//
	// Empty means unstated, and a client that receives it leaves its own mode
	// alone. That is what makes the field additive: state written before it
	// existed, and clients that never send it, behave exactly as they did.
	LayoutMode string `json:"layout_mode,omitempty"`
	// NumWorkspaces is how many workspaces this session has. The daemon-side
	// operations bound workspace indices by it; it used to be a constant 9
	// duplicated here to keep this package free of a config import, which meant
	// the daemon disagreed with a client configured for any other number.
	// Zero means unstated, and the bound falls back to defaultWorkspaces.
	NumWorkspaces int `json:"num_workspaces,omitempty"`
	// SessionID is the session's id, stamped on every save so a restored
	// session keeps it. Empty in state written before it existed, and such a
	// session gets a new id on restore.
	SessionID string `json:"session_id,omitempty"`
	// ResurrectionVersion tags the on-disk state schema. It is stamped by
	// SaveSessionForResurrection (not by clients) and checked on load so that
	// state written by a newer, incompatible tuios is archived rather than
	// misinterpreted. Absent (0) means pre-versioning state, which is a
	// structural subset of the current schema and loads fine.
	ResurrectionVersion int `json:"resurrection_version,omitempty"`
	// Version counts the daemon-side mutations of this state: every headless
	// window operation bumps it, a client sync does not. It is the daemon's
	// answer to "how much have I changed since you last looked", and it is
	// stamped on every state the daemon hands out.
	Version int `json:"version,omitempty"`
	// BaseVersion is the Version a client last saw, echoed back on the state it
	// pushes. It says which daemon state the client's snapshot was built from, so
	// the daemon can tell a current sync from one that predates its own
	// mutations. Zero means a client that predates state versioning; its syncs
	// are taken at face value, as they were before.
	BaseVersion int `json:"base_version,omitempty"`
	// PushOrigin and PushSeq name the push that carries this state: which
	// client connection sent it, and how many pushes that connection had sent
	// to this session counting this one. TUIClient.UpdateState stamps them and
	// the daemon consumes them; a state the daemon hands out never carries
	// them. See PushSeen.
	PushOrigin string `json:"-"`
	PushSeq    uint64 `json:"-"`
	// PushSeen is, for each client connection attached to the session, the
	// newest of its pushes the daemon had merged when it handed this state out.
	// It is what lets a client tell a broadcast built before its own last push
	// from one built after it. Without that a client that pushed while a
	// peer's broadcast was on its way to it applied the broadcast afterwards,
	// took the peer's older view over its own newer one, and was never told
	// otherwise: the daemon held the client's push, sent it to everyone else,
	// and a client is never sent its own push back. Two clients were left
	// showing a pane zoomed and not zoomed for good. See
	// TUIClient.PredatesOwnPush. Wire only: it is about connections, which do
	// not survive a restart, so it is never saved.
	PushSeen map[string]uint64 `json:"-"`
	// LayoutTreeOps says the session's clients send their BSP trees as ops.
	// Wire only: this daemon always sets it, because a v0.12 client reads it
	// and sends its trees in its pushes without it.
	LayoutTreeOps bool `json:"-"`
	// ScratchWorkspaces says every attached client shows a scratch group
	// from its own workspace. This daemon always sets it, because a v0.12
	// client reads it. Wire only.
	ScratchWorkspaces bool `json:"-"`
	// SnapshotSeq numbers the copies of the state the session hands out, in
	// the order they were taken. A copy is taken under the state lock and sent
	// after it is released, and the daemon sends from more than one goroutine
	// (a client's push is answered from its connection, a daemon-side change
	// from wherever it was made), so two copies can reach a client in the
	// opposite order to the one they were taken in. The client applying the
	// older one last went back to a state the session had left: a window the
	// daemon had just opened disappeared from it. A client drops any copy
	// numbered below one it has already applied. Zero is a daemon that does
	// not number them. Wire only, like PushSeen.
	SnapshotSeq uint64 `json:"-"`
	changeSeq   uint64
	// Options is a daemon-owned key/value store for session options set through
	// the JSON verb protocol (set-option / get-option). It is additive: older
	// clients and older on-disk state simply omit it. Keys are advisory names;
	// the daemon records them verbatim so a later get-option can read them back
	// and an attached TUI can apply the ones it understands.
	Options map[string]string `json:"options,omitempty"`
	// PaneReportBg and PaneReportFg are the ground the pushing client paints
	// behind pane content (appearance.pane_background) and the ink it gives
	// default-coloured text there, as #rrggbb. The daemon's emulators answer a
	// program's OSC 11 and OSC 10 queries with them, because it is the daemon's
	// emulator that answers and only the client knows what it draws. Empty
	// means nothing is painted, which keeps the emulator's own answer; an older
	// client never sends them and gets exactly that. The last client to push
	// wins, as it does for the rest of what a client owns here.
	PaneReportBg string `json:"pane_report_bg,omitempty"`
	PaneReportFg string `json:"pane_report_fg,omitempty"`
	// PaneReportPalette is the pushing client's host terminal's own sixteen
	// ANSI colours, as sixteen comma-separated #rrggbb entries with an empty
	// entry for a slot the host did not answer for. The daemon's emulators
	// answer a program's OSC 4 query for a slot nothing else has set with it.
	// Empty keeps the emulator's own answers, which is what an older client
	// gets. A string rather than a list, so there is nothing to bound: the
	// daemon reads the first sixteen well-formed entries and ignores the rest.
	PaneReportPalette string `json:"pane_report_palette,omitempty"`
	// PaneGeometry is the session's agreed intra-box layout arithmetic: the
	// inputs that decide how the panes' box is partitioned and how much of each
	// rectangle a guest may draw in. See PaneGeometryState for why it is session
	// state and not a per-client preference.
	//
	// Nil means unstated (an older peer, or state written before this existed),
	// and a client that receives nil keeps its own configured values, which is
	// the pre-existing behaviour.
	PaneGeometry *PaneGeometryState `json:"pane_geometry,omitempty"`
	// ScrollStrip is the scrolling layout's strip as the session has it, on the
	// workspace this state names. It travels with CurrentWorkspace and means
	// nothing without it: each workspace keeps its own strip.
	//
	// Nil means unstated (a client too old to know the field, or state written
	// before it existed), and a client that receives nil keeps the strip it has,
	// which is the behaviour that predates this. A pointer rather than a plain
	// field because the offset's zero, a strip at its left end, is the commonest
	// position there is, and gob omits a zero-valued field from the wire: a plain
	// int (or a *int, which gob flattens to one) would send "scrolled home" as
	// "nothing to say". A non-nil pointer to a struct survives, which is what
	// makes home a value this can carry.
	ScrollStrip *ScrollStripState `json:"scroll_strip,omitempty"`
	// WorkspaceScrollColumns is the scrolling layout's columns on each
	// workspace: which panes each column holds, top to bottom, and how wide it
	// is.
	//
	// It is layout intent on the same terms as WorkspaceTrees, which carries the
	// BSP layout's splits and ratios. Before it existed the strip kept only its
	// offset, so anything that rebuilt a session from its state brought every
	// column back one pane wide at the default width: a column widened with the
	// width key lost the width and two stacked panes came back side by side. A
	// session switch is exactly such a rebuild, which is how it was reported.
	//
	// A column's width moves rectangles, so it is also the kind of thing two
	// clients have to agree on: a pane's PTY has one size, and two clients
	// holding different widths for its column drag it between them.
	//
	// Nil means unstated, and a client that receives nil keeps the columns it
	// has, which is the behaviour that predates the field.
	WorkspaceScrollColumns map[int][]SerializedScrollColumn `json:"workspace_scroll_columns,omitempty"`
}

// SerializedScrollColumn is one column of the scrolling layout.
//
// Panes are named by their window ID rather than by the integer a client
// numbers them with. The integers are that client's own, and naming panes by
// them would make the columns mean something only alongside a mapping that has
// to be restored first and kept in step.
type SerializedScrollColumn struct {
	// Windows are the panes in the column, top to bottom.
	Windows []string `json:"windows"`
	// Proportion is the column's width as a share of the screen, and zero is
	// the default width.
	Proportion float64 `json:"proportion,omitempty"`
	// FixedWidth is the column's width in cells, and zero means it goes by
	// Proportion.
	FixedWidth int `json:"fixed_width,omitempty"`
	// Active is the index in Windows the column is focused on.
	Active int `json:"active,omitempty"`
}

// PaneGeometryState carries the appearance settings that change cell geometry.
//
// A pane's PTY has exactly one size, and every client attached to a session is
// looking at the same PTYs, so every input to pane geometry has to be identical
// across attached clients. The outer box is already settled (the session size
// is the minimum over clients, and the chrome reserve is negotiated; see
// LayoutReserve); these are the inputs to the arithmetic inside that box. Two
// clients that disagree on them partition the same box into different
// rectangles, or the same rectangles into different guest grids, and drag the
// shared PTYs back and forth between the two answers on every state push.
//
// The line is deliberate: anything that moves a rectangle is session state,
// anything purely visual (theme, colours, glyphs, border style, title
// position, dimming) stays per-client and riceable. Border style and title
// position are visual because they draw on cells the pane already reserves;
// they never change how many cells it gets.
type PaneGeometryState struct {
	// SharedBorders merges tiled panes' border boxes into single dividers,
	// which changes both the rectangles (a divider column is kept between
	// panes) and the guest grid inside each rectangle (a borderless pane hands
	// its guest the whole rectangle rather than the rectangle less its border).
	SharedBorders bool `json:"shared_borders,omitempty"`
	// PaneGap is the cells of empty ground the tiler keeps between
	// neighbouring panes.
	PaneGap int `json:"pane_gap,omitempty"`
	// ScrollColumnWidth is how wide a column is in the scrolling layout, as a
	// percent of the screen, before anything resizes it. It decides every
	// column's cell width in that layout and so belongs here rather than in a
	// client's own config: two clients disagreeing about it would hand the same
	// pane two different widths.
	//
	// Zero is a peer that has not said, on the same terms as a nil
	// PaneGeometry: an older client, or state written before the field existed.
	// The receiving client keeps its own configured value.
	ScrollColumnWidth int `json:"scroll_column_width,omitempty"`
}

// ScrollStripState is the scrolling layout's strip, shared across the clients
// attached to one session.
//
// The strip is one long row of columns and the viewport is a window onto it, so
// where it is scrolled to is a place in the session rather than a property of a
// screen: two clients holding one offset are looking at the same place, which is
// what a shared session means. That is safe for the same reason it is wanted:
// the panes' box is identical on every client (the session's size is the
// minimum over them and the chrome reserve the maximum, so GetContentWidth
// agrees everywhere), so one offset puts the same columns on every screen.
//
// It is not a rectangle. A pane's rectangle is what one client's size and the
// shared layout came to between them and must never be adopted from a peer; an
// offset is an input to that arithmetic, like the master ratio and the column
// width beside it.
type ScrollStripState struct {
	// ViewportX is how many cells the strip is scrolled by, from its left end.
	ViewportX int `json:"viewport_x"`
}

type paneIO interface {
	io.ReadWriteCloser
	Resize(width, height int) error
}

// PTY represents a daemon-managed pseudo-terminal.
type PTY struct {
	ID                       string
	largeSlot                chan struct{}
	largeSlotOnce            sync.Once
	largeSince               atomic.Int64
	sessionID                string
	home                     atomic.Pointer[Session]
	windowID                 string
	probeTail                []byte
	probeJoin                [2 * nestProbeMax]byte
	host                     string
	pty                      paneIO
	cmd                      *exec.Cmd
	proc                     *os.Process
	procStart                uint64
	reap                     reapGate
	gate                     *readGate
	readMu                   sync.Mutex
	quiesced                 bool
	resumed                  chan struct{}
	respMu                   sync.Mutex
	respWake                 chan struct{}
	vtDone                   chan struct{}
	frozen                   atomic.Bool
	frozenW, frozenH         int
	frozenCellW, frozenCellH int
	rawLog                   *ptyLogger
	ctx                      context.Context
	cancel                   context.CancelFunc

	terminal   vt.Terminal
	terminalMu sync.RWMutex
	width      int
	height     int

	outputMu     sync.RWMutex
	outputBuffer []byte
	outputPos    int
	outputSeq    int64
	outputTaps   []*outputTap
	resizeMarks  []resizeMark

	paneMarks atomic.Uint32

	subscribers     map[string]*ptySubscriber
	subscribersMu   sync.RWMutex
	subscriberCount atomic.Int32
	graphicsAt      atomic.Int64
	gfx             gfxScanner
	unbroadcast     atomic.Int64
	reads           uint64
	framesSkipped   atomic.Int64
	streamsCut      atomic.Int64
	paceWake        chan struct{}
	holding         atomic.Bool
	holdSpent       bool
	holds           atomic.Int64
	holdsRanOut     atomic.Int64
	heldNanos       atomic.Int64
	pacingReportAt  time.Time
	pacingReported  pacingCounts

	debug bool

	exited     bool
	exitedMu   sync.RWMutex
	exitCode   int
	exitSignal atomic.Int32
	keepOpen   *exitNote

	vtWriteChan    chan vtChunk
	screenStopped  atomic.Bool
	recording      atomic.Bool
	faultNotices   faultNoticeQueue
	focusReporting bool
	hasFocus       func() bool

	streamMu sync.Mutex
	vtClosed bool

	askedW, askedH int
	leases         map[string]paneLease
	noClients      atomic.Bool
	leaseAt        time.Time
	leaseTimer     *time.Timer
	spawnW, spawnH int

	winsizeMu  sync.Mutex
	cellWidth  int
	cellHeight int

	spawnedAt             time.Time
	winsizeWritten        time.Time
	winsizeHeld           atomic.Bool
	heldWidth, heldHeight int
	winsizeTimer          *time.Timer
	winsizeClosed         bool

	vtSeq     int64
	vtResizes int64

	onExit      func(ptyID string)
	exitedAlone func(ptyID string, shell bool, signal int)
	ownShell    bool

	emit func(SessionEvent)

	lastOutput atomic.Int64

	humanKeyAt atomic.Int64
	draftWatch atomic.Pointer[func()]

	lastAgentProbe atomic.Int64

	lastScreenScan atomic.Int64

	lastDetectScan atomic.Int64
	detectSkips    atomic.Int32

	sshOffer sshOfferCheck

	screenSettleMu sync.Mutex
	screenSettle   *time.Timer
	screenLook     func()

	agentProgress atomic.Int64
	lastProgress  atomic.Int64
	agentNotify   atomic.Pointer[paneNotification]

	progStatus        progstatus.Store
	progStatusDirty   atomic.Bool
	chunkGroup        atomic.Int64
	progStatusApplyMu sync.Mutex

	title    atomic.Pointer[string]
	iconName atomic.Pointer[string]
	place    placeRecord
	dnd      dndRegistration
	marks    *vt.SemanticMarkerList
	cwdCheck paneCwdCheck
	shell    shellTrack
	runClaim atomic.Bool
}

func (p *PTY) owner() *Session { return p.home.Load() }

func (p *PTY) ownerID() string {
	if s := p.home.Load(); s != nil {
		return s.ID
	}
	return p.sessionID
}

// Title returns the last title this PTY's application set, or "" if it has set
// none.
func (p *PTY) Title() string {
	if t := p.title.Load(); t != nil {
		return *t
	}
	return ""
}

// IconName returns the name this PTY's application last gave with OSC 1, or
// "" if it has given none.
func (p *PTY) IconName() string {
	if n := p.iconName.Load(); n != nil {
		return *n
	}
	return ""
}

func (p *PTY) setIconName(name string) bool {
	old := p.iconName.Swap(&name)
	if old == nil {
		return name != ""
	}
	return *old != name
}

const agentExitProbeInterval = 250 * time.Millisecond

// LastOutput returns the unix-nano time this PTY most recently produced output,
// or 0 if it has produced none. It backs the daemon's agent-state stall
// heuristic, which demotes a pane that reported working but has gone quiet.
func (p *PTY) LastOutput() int64 {
	return p.lastOutput.Load()
}

func (p *PTY) probeAgentExitDue(now int64) bool {
	if now-p.lastAgentProbe.Load() < int64(agentExitProbeInterval) {
		return false
	}
	p.lastAgentProbe.Store(now)
	return true
}

func (p *PTY) storeAgentProgress(state vt.ProgressState, percent int) {
	p.agentProgress.Store(int64(state) + 1)
	p.lastProgress.Store((int64(state)+1)<<32 | int64(uint32(int32(percent))))
}

func (p *PTY) takeAgentProgress() (vt.ProgressState, bool) {
	v := p.agentProgress.Swap(0)
	if v == 0 {
		return 0, false
	}
	return vt.ProgressState(v - 1), true
}

// Session represents a persistent TUIOS session.
// The daemon manages PTYs and stores state; the client runs the TUI.
type Session struct {
	startDirs      sync.Map
	agentDirsMu    sync.Mutex
	agentDirs      map[string]map[string]string
	places         sync.Map
	attachMu       sync.Mutex
	exclusiveSeq   atomic.Uint64
	optionApplyMu  sync.Mutex
	optionsWaiting map[string]waitingOption

	// Identity
	ID        string
	name      atomic.Pointer[string]
	persistMu sync.Mutex
	startDir  atomic.Pointer[string]

	ptys                     map[string]*PTY
	ptysMu                   sync.RWMutex
	reportBgHex, reportFgHex string
	reportBg, reportFg       color.Color
	reportPalHex             string
	reportPal                [16]color.Color
	reportsHeld              bool
	reportHeld               *heldReportColors
	tuiClients               atomic.Int32
	viewers                  sessionViewers

	cwdCache            map[string]string
	cwdReadAt           time.Time
	cwdCacheMu          sync.Mutex
	liveFactsPushQueued atomic.Bool
	liveFactsPushedAt   atomic.Int64

	state            *SessionState
	stopResurrection func()
	history          historySaver
	discardHistory   atomic.Bool

	stateMu           sync.RWMutex
	snapSeq           atomic.Uint64
	pushSeen          map[string]uint64
	refusedOrigin     string
	focusMovedVersion int
	clientFocusMoved  map[string]int
	askedFocusMoved   map[string]*askedFocusMoves
	workspaceTrail    []int
	renumberedAt      int
	tabListed         map[int]bool
	existed           map[int]bool
	hadPanes          map[int]bool
	panesAt           map[string]paneAt
	selfEmptied       map[int]bool
	gaps              map[int]bool
	closeUpPending    bool
	exitedAloneMu     sync.Mutex
	exitedAlone       map[string]bool
	signalHold        atomic.Int64
	tabKeys           atomic.Pointer[map[int]int]
	returnTo          map[int]int
	treeOps           [1024]treeOpRecord
	zoomOpAt          int
	recountMu         sync.Mutex
	focusIntent       bool
	focusNeutral      bool
	focusAskedBy      string
	movingWindow      string

	stateDirty  wakeLatch
	saveStale   wakeLatch
	saverWakes  atomic.Int64
	wakes       atomic.Pointer[idleWakes]
	offerProbe  atomic.Pointer[sshOfferProbe]
	stateWake   chan struct{}
	stateWakeMu sync.Mutex

	eventSink    func(SessionEvent)
	eventSinkMu  sync.RWMutex
	paneMarkSink atomic.Pointer[paneMarkFunc]

	stateSink   func(*SessionState)
	stateSinkMu sync.RWMutex

	changeSeq uint64

	pushMu       sync.Mutex
	deliveredSeq uint64

	broadcastFP    uint64
	broadcastFPSet bool

	width      int
	height     int
	reserve    LayoutReserve
	layoutGen  uint64
	sizePolicy string
	sizedBy    SizeOwner
	sizeMu     sync.RWMutex

	// Lifecycle
	Created time.Time

	lastActive time.Time
	lastUsed   time.Time
	usedMarked time.Time
	activeMu   sync.Mutex

	config *SessionConfig

	agentClaims map[string]agentClaim

	askShows map[string]askShow

	agentHarnessPIDs map[string]harnessProc

	agentSharedGuards map[string]sharedGuard
	harnessNamed      []string
	harnessNamedMu    sync.Mutex

	agentSubagents  map[string]*paneSubagents
	subagentShownAt int64

	transcripts agentTranscriptState

	agentHolds     map[string]agentHold
	agentHoldTimer *time.Timer
	agentHoldMu    sync.Mutex

	idle idleGate

	agentTurns     map[string]agentTurn
	completionSeen map[string]uint64

	agentMetaPrune pruneTimer

	subagentPrune pruneTimer
	subagentFlush pruneTimer

	graphicsMu      sync.RWMutex
	kittyGraphics   bool
	sixelGraphics   bool
	graphicsKnown   bool
	linkedViewer    atomic.Bool
	kittyAnimation  atomic.Bool
	sixelAdvertised atomic.Bool
	kittyAdvertised atomic.Bool
	fed             paneFederation
	onRemotePane    func(windowID string, p *remotePane)
	hostShown       atomic.Pointer[func() bool]
	taskAuto        atomic.Pointer[func() bool]
	taskOutcome     atomic.Pointer[func(board string, finished bool)]
}

// SetGraphicsCapabilities records the graphics protocols tuios can forward to
// the attached client's host terminal. It is called on every attach, so the
// most recent client wins; PTYs already running keep the environment they were
// started with.
func (s *Session) SetGraphicsCapabilities(kitty, sixel bool) {
	s.graphicsMu.Lock()
	defer s.graphicsMu.Unlock()
	s.kittyGraphics = kitty
	s.sixelGraphics = sixel
	s.graphicsKnown = true
}

// SetKittyAnimation records whether the hosts of the session's attached
// clients make kitty frame edits: true only while every one of them does.
// Daemon.recountTUIClients counts it on every attach, detach and disconnect.
//
// A frame edit the host cannot make is refused by the daemon, from the pane's
// own emulator, so the refusal reaches the guest in the order the guest asked
// its questions. A client used to refuse it, a round trip later: after the
// daemon had answered DA1, which is where a probing guest stops reading. A
// nested tuios then took the refusal for keys and typed it into its pane.
func (s *Session) SetKittyAnimation(ok bool) { s.kittyAnimation.Store(ok) }

func (s *Session) dragAndDrop() bool {
	return s.config != nil && s.config.DragAndDrop != nil && s.config.DragAndDrop()
}

// SetSixelAdvertised records whether a sixel image a pane draws will be shown:
// true while any attached client's terminal draws sixel or kitty graphics,
// since a client with kitty and no sixel is sent the picture as a kitty image.
// Daemon.recountTUIClients counts it with SetKittyAnimation.
//
// It is what the pane's DA1 answer lists. The daemon's emulator is the one that
// answers a guest's queries, so this is where a program such as chafa, lsix or
// yazi learns whether to draw sixel or fall back to text. A client with neither
// protocol is shown a placeholder box where the image is.
func (s *Session) SetSixelAdvertised(ok bool) { s.sixelAdvertised.Store(ok) }

// SetKittyAdvertised records whether a kitty image a pane draws will be
// shown: true while any attached client's terminal draws kitty graphics.
// Daemon.recountTUIClients counts it with SetSixelAdvertised, by the same rule.
//
// It decides whether the daemon answers a guest's kitty graphics query (see
// kittyQueryResponse). A client whose terminal has no kitty graphics drops
// the image, so a guest told OK draws nothing where it would have drawn a
// text fallback.
func (s *Session) SetKittyAdvertised(ok bool) { s.kittyAdvertised.Store(ok) }

// SixelAdvertised reports what SetSixelAdvertised last recorded.
func (s *Session) SixelAdvertised() bool { return s.sixelAdvertised.Load() }

// GraphicsCapabilities returns the recorded kitty and sixel support, or the
// daemon's most recent client's while no client has attached to the session.
func (s *Session) GraphicsCapabilities() (kitty, sixel bool) {
	s.graphicsMu.RLock()
	kitty, sixel, known := s.kittyGraphics, s.sixelGraphics, s.graphicsKnown
	s.graphicsMu.RUnlock()
	if !known && s.config != nil && s.config.HostGraphics != nil {
		return s.config.HostGraphics()
	}
	return kitty, sixel
}

// SessionConfig holds configuration for a session.
type SessionConfig struct {
	Term      string
	ColorTerm string
	Shell     string
	// SocketPath is the daemon socket a shell spawned in this session reports to.
	// The manager stamps it from the daemon's own socket when a session is
	// created, so it is exported into every pane's environment as TUIOS_SOCKET.
	SocketPath string
	// ScrollbackLines is the history depth every pane in this session keeps.
	// The manager stamps it from the daemon's config; zero means the
	// emulator's default.
	ScrollbackLines int
	// HostName is the name of the machine the session runs on, exported into
	// every pane as TUIOS_HOST. The manager stamps it from the daemon's own
	// hostname. Empty leaves the variable unset.
	HostName string
	// InheritCwd starts a new window in the focused pane's working directory
	// rather than the daemon's. The manager stamps it from the daemon's config.
	InheritCwd bool
	// PreferredShell returns appearance.preferred_shell, used when Shell is
	// empty. The manager stamps it with its own getter, so a config reload
	// reaches the next pane. Nil, or an empty answer, means $SHELL and then
	// the platform default.
	PreferredShell func() string
	// ShellIntegration reports whether [shell] integration is on, read at
	// each spawn so a reload reaches the next pane. Nil means on. See
	// internal/shellinteg.
	ShellIntegration func() bool
	// ShellLogin reports whether [shell] login is on, read at each spawn.
	// Nil means off. See shellinteg.Login.
	ShellLogin func() bool
	// ReturnWhenEmpty reports workspaces.return_when_empty. The manager
	// stamps it with its own getter, so a config reload reaches every
	// session. Nil means on.
	ReturnWhenEmpty func() bool
	// DragAndDrop reports whether [panes] drag_and_drop is on, read each
	// time so a reload applies. Off, the daemon neither records what a
	// pane's program says about drag and drop nor answers its query.
	DragAndDrop func() bool
	// SubagentFade is how long a finished subagent that was done or stopped
	// stays on the list. Zero is defaultSubagentFade. The manager stamps it
	// from daemon.subagent_fade_seconds.
	SubagentFade time.Duration
	// SaveEvery is the longest the session waits to be saved after its
	// panes printed. Zero is resurrectionInterval. The manager stamps it
	// from daemon.save_seconds.
	SaveEvery time.Duration
	// HerdrEnv returns the herdr protocol variables a pane with the given
	// window id that runs the given command is started with, nil for none.
	// See Manager.HerdrEnv.
	HerdrEnv func(sessionID, windowID string, workspace int, command []string) []string
	// AgentEnv returns SSH_AUTH_SOCK naming the session's ssh agent link
	// while [daemon] ssh_agent is follow, nil otherwise. The manager stamps
	// it. See ssh_agent_follow.go.
	AgentEnv func(sessionID string) []string
	// DisplayEnv returns WAYLAND_DISPLAY, DISPLAY and XAUTHORITY of the
	// session's newest local client while [daemon] display follows, nil
	// otherwise. The manager stamps it. See display_follow.go.
	DisplayEnv func(sessionID string) []string
	// HostGraphics returns the graphics of the daemon's most recent client,
	// which a session no client has attached to tells its shells. The
	// manager stamps it. See Manager.noteHostGraphics.
	HostGraphics func() (kitty, sixel bool)
	// CellSize returns the cell, in pixels, a new pane of the session takes,
	// ok false when no client has measured one. The manager stamps it. See
	// Daemon.newPaneCellSize.
	CellSize func(sessionID string) (w, h int, ok bool)
	// PaneToken returns the token a pane with the given window id is started
	// with, exported as TUIOS_PANE_TOKEN. The manager stamps it with its own.
	// Nil, or an empty answer, leaves the variable unset. See pane_token.go.
	PaneToken func(windowID string) string
	grants    *paneGrantTable
	paneLife  *sync.RWMutex
	history   *HistoryPolicy
	// Global creates the session as a global one. See SessionState.Global.
	Global bool
	// Space creates the session in a space. See SessionState.Space. A restore
	// sets it from the state file, because the client sync the restore goes
	// through keeps the new session's own value.
	Space string
	// Rank is the session's place in the session order, set by a restore
	// from the state file for the reason Space is. See SessionState.Rank.
	Rank      int
	restoreID string
}

// SetStartDir sets the directory new local windows start in when nothing
// else names one. See Session.startDir.
func (s *Session) SetStartDir(dir string) {
	s.startDir.Store(&dir)
}

func absStartDir(dir string) string {
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func (s *Session) noteStartDir(windowID, dir string) {
	if windowID == "" || dir == "" {
		return
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	s.startDirs.LoadOrStore(windowID, startDirEntry{dir: realPath(filepath.Clean(dir)), at: time.Now()})
}

func (s *Session) windowStartDir(windowID string) string {
	if v, ok := s.startDirs.Load(windowID); ok {
		return v.(startDirEntry).dir
	}
	return ""
}

type startDirEntry struct {
	dir string
	at  time.Time
}

// StartDir is the directory set with SetStartDir, or "".
func (s *Session) StartDir() string {
	if p := s.startDir.Load(); p != nil {
		return *p
	}
	return ""
}

func (s *Session) historyPolicy() HistoryPolicy {
	if s.config == nil || s.config.history == nil {
		return HistoryPolicy{}
	}
	return *s.config.history
}

func (s *Session) inheritedCwd(caller string) string {
	if s.config == nil || !s.config.InheritCwd {
		return ""
	}
	if caller != "" {
		return caller
	}
	return s.windowCwd(s.GetState().FocusedWindowID)
}

func (s *Session) windowCwd(id string) string {
	win, ok := findWindowState(s.GetState(), id)
	if !ok {
		return ""
	}
	var procCwd string
	var procOK bool
	if pty := s.GetPTY(win.PTYID); pty != nil {
		procCwd, procOK = pty.ProcessCwd()
	}
	return pickWindowCwd(procCwd, procOK, win.Cwd)
}

func pickWindowCwd(procCwd string, procOK bool, record string) string {
	if procOK {
		if isLocalDir(procCwd) {
			return procCwd
		}
		return ""
	}
	if isLocalDir(record) {
		return record
	}
	return ""
}

func isLocalDir(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func (s *Session) cwdFrom(id string) string {
	if win, ok := findWindowState(s.GetState(), id); id != "" && ok && win.Host == "" {
		if cwd := s.windowCwd(id); cwd != "" {
			return cwd
		}
	}
	return s.StartDir()
}

func (s *Session) scrollbackLines() int {
	if s.config != nil && s.config.ScrollbackLines > 0 {
		return s.config.ScrollbackLines
	}
	return vt.DefaultScrollbackSize
}

// NewSession creates a new persistent session.
func NewSession(name string, cfg *SessionConfig, width, height int) (*Session, error) {
	id := uuid.New().String()
	if cfg != nil && cfg.restoreID != "" {
		id = cfg.restoreID
	}
	if name == "" {
		name = fmt.Sprintf("session-%s", id[:8])
	}

	now := time.Now()

	session := &Session{
		ID:   id,
		ptys: make(map[string]*PTY),
		state: &SessionState{
			Name:             name,
			Windows:          []WindowState{},
			CurrentWorkspace: 1,
			WorkspaceFocus:   make(map[int]string),
			MasterRatio:      0.5,
			Width:            width,
			Height:           height,
			Version:          1,
			Global:           cfg != nil && cfg.Global,
		},
		width:      width,
		height:     height,
		Created:    now,
		lastActive: now,
		config:     cfg,
	}
	if cfg != nil {
		session.state.Space = cfg.Space
		session.state.Rank = cfg.Rank
	}

	session.setName(name)

	session.stateDirty.init()
	session.saveStale.init()
	saveEvery := resurrectionInterval
	if cfg != nil && cfg.SaveEvery > 0 {
		saveEvery = cfg.SaveEvery
	}
	session.stopResurrection = startPeriodicSaveWith(saveEvery,
		func() *SessionState { return session.ResurrectionState() },
		&session.stateDirty, &session.saveStale, &session.saverWakes,
		session.persist,
	)

	return session, nil
}

// Name is the session's name. See the name field.
func (s *Session) Name() string {
	if p := s.name.Load(); p != nil {
		return *p
	}
	return ""
}

func (s *Session) setName(name string) {
	s.name.Store(&name)
}

func (s *Session) persist(state *SessionState) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if state == nil || state.Name != s.Name() {
		return nil
	}
	if s.holdingCheckpoint() {
		return nil
	}
	state.SessionID = s.ID
	err := SaveSessionForResurrection(state)
	s.saveHistory(state, false)
	return err
}

// SetEventSink installs the control-plane event sink for this session. It is
// safe to call concurrently and may be set after windows already exist; the
// per-PTY emitters read it dynamically.
func (s *Session) SetEventSink(fn func(SessionEvent)) {
	s.eventSinkMu.Lock()
	s.eventSink = fn
	s.eventSinkMu.Unlock()
}

func (s *Session) emit(ev SessionEvent) {
	switch ev.Type {
	case EventOutput:
		s.saveStale.Set()
		s.noteActivity()
	case EventWindowRetitled:
		s.noteListingChange()
	}
	s.eventSinkMu.RLock()
	fn := s.eventSink
	s.eventSinkMu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}

// SetStateSink installs the sink that receives the canonical state after every
// daemon-side mutation. It is safe to call concurrently and may be set after the
// session already holds windows.
func (s *Session) SetStateSink(fn func(*SessionState)) {
	s.stateSinkMu.Lock()
	s.stateSink = fn
	s.stateSinkMu.Unlock()
}

func (s *Session) publishState(snap *SessionState) {
	if snap == nil {
		return
	}
	s.stateSinkMu.RLock()
	fn := s.stateSink
	s.stateSinkMu.RUnlock()
	if fn == nil {
		return
	}

	s.fillLiveFacts(snap)

	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if snap.changeSeq <= s.deliveredSeq {
		return
	}
	s.deliveredSeq = snap.changeSeq
	s.forgetBroadcastFingerprint()
	fn(snap)
}

func (s *Session) resendState(send func(*SessionState)) {
	s.stateSinkMu.RLock()
	fn := s.stateSink
	s.stateSinkMu.RUnlock()
	for {
		snap := s.GetState()
		if hook := stateResendSnapshotTaken.Load(); hook != nil {
			(*hook)()
		}
		s.pushMu.Lock()
		if snap.changeSeq < s.deliveredSeq {
			s.pushMu.Unlock()
			continue
		}
		if snap.changeSeq > s.deliveredSeq && fn != nil {
			s.deliveredSeq = snap.changeSeq
			s.forgetBroadcastFingerprint()
			fn(snap)
		} else {
			send(snap)
		}
		s.pushMu.Unlock()
		return
	}
}

var stateResendSnapshotTaken atomic.Pointer[func()]

type pushSends struct {
	toSender func()
	toPeers  func()
	fp       uint64
}

func (s *Session) deliverPush(snap *SessionState, pushSeq uint64, prepare func(snap *SessionState, withPeers bool) pushSends) {
	if snap == nil {
		return
	}
	s.stateSinkMu.RLock()
	fn := s.stateSink
	s.stateSinkMu.RUnlock()
	sends := prepare(snap, true)
	if sends.toSender == nil && sends.toPeers == nil {
		return
	}

	s.pushMu.Lock()
	for snap.changeSeq < s.deliveredSeq && sends.toSender != nil {
		s.pushMu.Unlock()
		snap = s.GetState()
		sends = prepare(snap, false)
		s.pushMu.Lock()
	}
	defer s.pushMu.Unlock()
	if snap.changeSeq <= s.deliveredSeq {
		if sends.toSender != nil {
			sends.toSender()
		}
		return
	}
	s.deliveredSeq = snap.changeSeq
	if snap.changeSeq > pushSeq && fn != nil {
		s.forgetBroadcastFingerprint()
		fn(snap)
		return
	}
	if sends.toSender != nil {
		sends.toSender()
	}
	if sends.toPeers == nil {
		return
	}
	if s.broadcastFPSet && s.broadcastFP == sends.fp {
		return
	}
	s.broadcastFP = sends.fp
	s.broadcastFPSet = true
	sends.toPeers()
}

var statePushSnapshotTaken atomic.Pointer[func()]

func (s *Session) forgetBroadcastFingerprint() {
	s.broadcastFPSet = false
}

func (s *Session) forgetBroadcast() {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	s.forgetBroadcastFingerprint()
}

// CreatePTY creates a new PTY in this session. windowID, if non-empty, is the
// client-side window UUID exported to the shell as TUIOS_WINDOW_ID. onExit, if
// non-nil, is invoked with the PTY ID when the process exits; it is set before
// the monitor goroutine starts so it is always visible to monitorExit.
func (s *Session) CreatePTY(windowID string, width, height int, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(width, height, ptySpawn{windowID: windowID, onExit: onExit})
}

func (s *Session) restorePTYWithGrants(windowID string, width, height int, cwd string, grants *Grants, history *savedHistory, workspace int, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(width, height, ptySpawn{windowID: windowID, cwd: cwd, restored: &restoreSpec{history: history, savedCwd: cwd}, onExit: onExit, grants: grants, workspace: workspace})
}

func (s *Session) restorePlaceholderPTY(windowID string, width, height int, cwd, kind, uri string, workspace int, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(width, height, ptySpawn{windowID: windowID, cwd: cwd, command: PlaceholderCommand(kind, uri), onExit: onExit, workspace: workspace})
}

type ptySpawn struct {
	windowID              string
	cwd                   string
	command               []string
	env                   []string
	host                  string
	restored              *restoreSpec
	onExit                func(ptyID string)
	stdout                *os.File
	extraFiles            []*os.File
	grants                *Grants
	workspace             int
	noStartDir            bool
	keepOpen              bool
	closeOnExit           bool
	shellIntegration      string
	cellWidth, cellHeight int
}

func (s *Session) createPTY(width, height int, sp ptySpawn) (*PTY, error) {
	defer s.holdPaneLife()()
	if sp.grants == nil && sp.host == "" && sp.windowID != "" {
		sp.grants = s.recordedGrants(sp.windowID)
	}
	var startDir string
	if sp.host == "" && sp.cwd != "" {
		if info, err := os.Stat(sp.cwd); err == nil && info.IsDir() {
			startDir = sp.cwd
		}
	}
	if sp.restored == nil && sp.host == "" && sp.windowID != "" && !sp.noStartDir {
		dir := s.StartDir()
		if startDir != "" {
			dir = startDir
		}
		s.noteStartDir(sp.windowID, dir)
	}

	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	id := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())

	if sp.host == "" && sp.windowID != "" && s.config != nil && s.config.grants != nil {
		table := s.config.grants
		table.add(sp.windowID, id, s.Name(), sp.grants)
		exit := sp.onExit
		sp.onExit = func(ptyID string) {
			table.remove(sp.windowID, ptyID)
			if exit != nil {
				exit(ptyID)
			}
		}
		defer func() {
			if _, ok := s.ptys[id]; !ok {
				table.remove(sp.windowID, id)
			}
		}()
	}

	shell, missingShell := s.resolveShell()
	if missingShell != "" {
		log.Printf("Warning: configured shell %q not found, using %s", missingShell, shell)
	}

	var (
		ptyInstance paneIO
		cmd         *exec.Cmd
		err         error
	)
	if sp.host != "" {
		ptyInstance, err = s.openRemotePaneFor(sp.windowID, sp.host, width, height, sp.cwd, sp.command)
		if err != nil {
			cancel()
			return nil, err
		}
		if rp, ok := ptyInstance.(*remotePane); ok && s.onRemotePane != nil {
			s.onRemotePane(sp.windowID, rp)
		}
	} else {
		if s.config != nil && s.config.CellSize != nil {
			if w, h, ok := s.config.CellSize(s.ID); ok {
				sp.cellWidth, sp.cellHeight = w, h
			}
		}
		ptyInstance, cmd, err = ptyspawn.SpawnTTY(width, height, sp.cellWidth, sp.cellHeight, func(tty string) *exec.Cmd {
			var cmd *exec.Cmd
			if len(sp.command) > 0 {
				//tuios:allow-exec ptyspawn starts the pane program on a pseudo console
				cmd = exec.Command(sp.command[0], sp.command[1:]...)
			} else {
				//tuios:allow-exec ptyspawn starts the pane program on a pseudo console
				cmd = exec.Command(shell)
			}
			cmd.Env = s.buildEnvFor(sp.windowID, sp.workspace, sp.restored != nil, sp.env, sp.command)
			if len(sp.command) == 0 && s.shellIntegrationFor(sp.shellIntegration) {
				if args, env, ok := shellinteg.Setup(shell, cmd.Env); ok {
					cmd.Args = append(cmd.Args, args...)
					cmd.Env = env
				}
			}
			if len(sp.command) == 0 && s.config != nil && s.config.ShellLogin != nil && s.config.ShellLogin() {
				shellinteg.Login(cmd)
			}
			if tty != "" {
				cmd.Env = append(cmd.Env, PaneTTYEnv+"="+tty)
			}
			if sp.stdout != nil {
				cmd.Stdout = sp.stdout
			}
			if len(sp.extraFiles) > 0 && runtime.GOOS != "windows" {
				cmd.ExtraFiles = sp.extraFiles
			}
			if startDir != "" {
				cmd.Dir = startDir
			}
			return cmd
		}, debugLog)
		if err != nil {
			cancel()
			return nil, err
		}
		panescope.Place(cmd.Process.Pid, log.Printf)
	}

	var terminal vt.Terminal
	if sp.restored != nil {
		startedIn := startDir
		if startedIn == "" && sp.restored.savedCwd != "" {
			startedIn, _ = os.Getwd()
		}
		terminal = newRestoredEmulator(width, height, s.scrollbackLines(), startedIn, sp.restored)
	} else {
		terminal = vt.NewWithScrollback(width, height, s.scrollbackLines())
	}
	pty := s.installPTY(ctx, cancel, id, ptyInstance, cmd, width, height, sp, terminal)
	if cmd != nil {
		pty.proc = cmd.Process
		pty.procStart = processStart(cmd.Process)
	}
	pty.startPTY()
	s.TouchActive()
	return pty, nil
}

func (s *Session) installPTY(ctx context.Context, cancel context.CancelFunc, id string, ptyInstance paneIO, cmd *exec.Cmd, width, height int, sp ptySpawn, terminal vt.Terminal) *PTY {
	pty := &PTY{
		ID:          id,
		sessionID:   s.ID,
		windowID:    sp.windowID,
		host:        sp.host,
		pty:         ptyInstance,
		cmd:         cmd,
		ctx:         ctx,
		cancel:      cancel,
		terminal:    terminal,
		width:       width,
		height:      height,
		spawnW:      width,
		spawnH:      height,
		subscribers: make(map[string]*ptySubscriber),
		paceWake:    make(chan struct{}, 1),
		vtWriteChan: make(chan vtChunk, 256),
		vtDone:      make(chan struct{}),
		respWake:    make(chan struct{}, 1),
		onExit:      sp.onExit,
		exitedAlone: s.notePaneExitedAlone,
		ownShell:    len(sp.command) == 0 && !sp.closeOnExit && sp.host == "",
		debug:       debugEnabled(),
		rawLog:      newPTYLogger(id),
		spawnedAt:   time.Now(),
		cellWidth:   sp.cellWidth,
		cellHeight:  sp.cellHeight,
	}
	if sp.keepOpen {
		pty.keepOpen = newExitNote()
	}
	if sp.host == "" {
		pty.gate = newReadGate(ptyInstance)
	}

	pty.home.Store(s)
	pty.hasFocus = func() bool { return pty.owner().paneHasFocus(sp.windowID) }
	pty.emit = func(ev SessionEvent) {
		ev.Window = sp.windowID
		ev.PTYID = id
		owner := pty.owner()
		owner.emit(ev)
		if bit := paneMarkBit(ev.Type); bit != 0 && pty.raisePaneMark(bit) {
			owner.notePaneMark(id, sp.windowID, bit)
		}
	}
	pty.place.onChange = func() {
		if s := pty.owner(); s != nil {
			s.noteListingChange()
		}
	}

	s.setUpPaneTerminal(pty, terminal)

	if cmd != nil {
		if seed := cmd.Dir; seed != "" {
			pty.place.setCwd(seed)
		} else if wd, err := os.Getwd(); err == nil {
			pty.place.setCwd(wd)
		}
	}

	terminal.SetReportColors(s.reportFg, s.reportBg, s.reportPal)
	s.ptys[id] = pty
	if sp.host == "" {
		s.places.Store(id, pty)
	}

	return pty
}

func (pty *PTY) startPTY() {
	go pty.vtWriter()

	go pty.readOutput()

	if pty.terminal != nil {
		pty.terminal.OnResponse(pty.wakeResponses)
	}
	go pty.forwardTerminalResponses()
	pty.wakeResponses()

	go pty.monitorExit()
}

// TouchActive records that the session was just used.
//
// "Used" has to include a keystroke reaching a pane, and that is the only
// reason this is exported. A client's state sync is not a reliable signal,
// because redundant syncs are suppressed and a keystroke need not change any
// state. A session someone is typing in is active, and the listing and the "most recently
// active" session lookup both read this.
func (s *Session) TouchActive() {
	s.activeMu.Lock()
	s.lastActive = time.Now()
	s.activeMu.Unlock()
	s.noteListingChange()
}

// LastActive is when the session was last used.
func (s *Session) LastActive() time.Time {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return s.lastActive
}

// TouchUsed records that the person used the session now. Only a
// MsgSessionUsed report from a client that may act as the person calls it
// (see session_used.go). A window spawn, a routed command, a tape and a
// restore do not. It bumps LastActive too.
//
// A use marks the session dirty at most once per usedSaveGap, so the first use
// after an idle spell reaches disk within about two seconds. A later use marks
// it stale, and the saver writes it within resurrectionInterval. See
// usedSaveGap.
func (s *Session) TouchUsed() {
	now := time.Now()
	s.activeMu.Lock()
	s.lastUsed, s.lastActive = now, now
	save := now.Sub(s.usedMarked) >= usedSaveGap
	if save {
		s.usedMarked = now
	}
	s.activeMu.Unlock()
	s.noteListingChange()
	if save {
		s.stateDirty.Set()
	} else {
		s.saveStale.Set()
	}
}

// LastUsed is when the person last used the session, zero when never.
func (s *Session) LastUsed() time.Time {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return s.lastUsed
}

func (s *Session) setLastUsed(t time.Time) {
	s.activeMu.Lock()
	s.lastUsed = t
	s.activeMu.Unlock()
}

func (s *Session) holdPaneLife() func() {
	if s.config == nil || s.config.paneLife == nil {
		return func() {}
	}
	s.config.paneLife.RLock()
	return s.config.paneLife.RUnlock
}

// GetPTY returns a PTY by ID.
func (s *Session) GetPTY(id string) *PTY {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	return s.ptys[id]
}

// ClosePTY closes and removes a PTY.
func (s *Session) ClosePTY(id string) error {
	defer s.holdPaneLife()()
	return s.closePTYHeld(id)
}

func (s *Session) closePTYHeld(id string) error {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	pty, exists := s.ptys[id]
	if !exists {
		return errfmt.Errorf("PTY %s not found", id)
	}

	delete(s.ptys, id)
	s.places.Delete(id)
	return pty.Close()
}

// ListPTYIDs returns all PTY IDs in this session.
func (s *Session) ListPTYIDs() []string {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()

	ids := make([]string, 0, len(s.ptys))
	for id := range s.ptys {
		ids = append(ids, id)
	}
	return ids
}

func (s *Session) hasLivePTY(id string) bool {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	_, ok := s.ptys[id]
	return ok
}

// PTYCount returns the number of PTYs.
func (s *Session) PTYCount() int {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	return len(s.ptys)
}

// GetState returns the current session state.
func (s *Session) GetState() *SessionState {
	s.stateMu.RLock()
	state := s.snapshotStateLocked()
	s.stateMu.RUnlock()

	s.fillLiveFacts(state)
	return state
}

func (s *Session) fillLiveFacts(state *SessionState) {
	if state == nil {
		return
	}
	live := s.livePaneFacts()
	state.IconNames = nil
	for i := range state.Windows {
		fact := live[state.Windows[i].PTYID]
		if fact.title != "" {
			state.Windows[i].Title = ClampDisplayText(fact.title)
		}
		if fact.iconName != "" {
			if state.IconNames == nil {
				state.IconNames = make(map[string]string)
			}
			state.IconNames[state.Windows[i].ID] = fact.iconName
		}
	}

	cwds := s.liveCwds()
	for i := range state.Windows {
		w := &state.Windows[i]
		place := live[w.PTYID].place
		w.CwdHost = place.elsewhere
		dnd := live[w.PTYID].dnd
		w.DnDAccepts, w.DnDOffers, w.DnDID, w.DnDOwner = dnd.accepts, dnd.offers, dnd.id, dnd.owner
		switch {
		case place.cwd != "":
			w.Cwd = place.cwd
		case cwds[w.PTYID] != "":
			w.Cwd = cwds[w.PTYID]
		case w.Cwd == "" && place.seed != "":
			w.Cwd = place.seed
		}
	}

	for i := range state.Windows {
		state.Windows[i].HostLink, state.Windows[i].HostLinkUntil, state.Windows[i].HostEnded = "", 0, ""
	}
	for id, link := range s.liveHostLinks() {
		for i := range state.Windows {
			if state.Windows[i].PTYID == id {
				state.Windows[i].HostLink = link.state
				state.Windows[i].HostLinkUntil = link.until
				state.Windows[i].HostEnded = link.ended
			}
		}
	}
	stopped := s.stoppedScreens()
	recorded := s.recordedPanes()
	for i := range state.Windows {
		state.Windows[i].ScreenStopped = stopped[state.Windows[i].PTYID]
		state.Windows[i].Recording = recorded[state.Windows[i].PTYID]
	}
}

type hostLinkFact struct {
	state string
	until int64
	ended string
}

func (s *Session) liveHostLinks() map[string]hostLinkFact {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	var out map[string]hostLinkFact
	for id, pty := range s.ptys {
		rp, ok := pty.pty.(*remotePane)
		if !ok {
			continue
		}
		state, until := rp.linkState()
		if state == "" {
			continue
		}
		if out == nil {
			out = make(map[string]hostLinkFact)
		}
		fact := hostLinkFact{state: state, ended: rp.endedWhy()}
		if !until.IsZero() {
			fact.until = until.Unix()
		}
		out[id] = fact
	}
	return out
}

type placeFact struct {
	cwd       string
	elsewhere string
	seed      string
}

type paneFact struct {
	title    string
	iconName string
	place    placeFact
	dnd      dndRegistrationFact
}

func (s *Session) livePaneFacts() map[string]paneFact {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	out := make(map[string]paneFact, len(s.ptys))
	for id, pty := range s.ptys {
		var fact paneFact
		fact.title = pty.Title()
		fact.iconName = pty.IconName()
		fact.dnd = pty.dnd.fact()
		if _, remote := pty.pty.(*remotePane); !remote {
			fact.place = placeFact{cwd: pty.place.announcedCwd(), elsewhere: pty.place.Elsewhere()}
			if fact.place.cwd == "" {
				fact.place.seed = pty.place.Cwd()
			}
		}
		if fact.title != "" || fact.iconName != "" || fact.place != (placeFact{}) || fact.dnd.accepts || fact.dnd.offers {
			out[id] = fact
		}
	}
	return out
}

func (s *Session) forgetCwdCache() {
	s.cwdCacheMu.Lock()
	s.cwdCache, s.cwdReadAt = nil, time.Time{}
	s.cwdCacheMu.Unlock()
}

const cwdReadInterval = time.Second

func (s *Session) liveCwds() map[string]string {
	s.cwdCacheMu.Lock()
	if s.cwdCache != nil && time.Since(s.cwdReadAt) < cwdReadInterval {
		cached := s.cwdCache
		s.cwdCacheMu.Unlock()
		return cached
	}
	s.cwdCacheMu.Unlock()

	s.ptysMu.RLock()
	cwds := make(map[string]string, len(s.ptys))
	for id, pty := range s.ptys {
		if cwd, ok := pty.ProcessCwd(); ok && cwd != "" {
			cwds[id] = cwd
		}
	}
	s.ptysMu.RUnlock()

	s.cwdCacheMu.Lock()
	s.cwdCache, s.cwdReadAt = cwds, time.Now()
	s.cwdCacheMu.Unlock()
	return cwds
}

func (s *Session) liveTitles() map[string]string {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()

	titles := make(map[string]string, len(s.ptys))
	for id, pty := range s.ptys {
		if t := pty.Title(); t != "" {
			titles[id] = t
		}
	}
	return titles
}

func (s *Session) snapshotStateLocked() *SessionState {
	stateCopy := *s.state
	stateCopy.Windows = make([]WindowState, len(s.state.Windows))
	copy(stateCopy.Windows, s.state.Windows)
	if s.state.Worktree != nil {
		wt := *s.state.Worktree
		stateCopy.Worktree = &wt
	}
	if s.state.WorkspaceFocus != nil {
		stateCopy.WorkspaceFocus = make(map[int]string)
		maps.Copy(stateCopy.WorkspaceFocus, s.state.WorkspaceFocus)
	}
	if s.state.FocusHistory != nil {
		stateCopy.FocusHistory = make(map[int][]string, len(s.state.FocusHistory))
		for workspace, history := range s.state.FocusHistory {
			stateCopy.FocusHistory[workspace] = slices.Clone(history)
		}
	}
	if s.state.Options != nil {
		stateCopy.Options = make(map[string]string, len(s.state.Options))
		maps.Copy(stateCopy.Options, s.state.Options)
	}
	if s.state.WorkspaceNames != nil {
		stateCopy.WorkspaceNames = make(map[int]string, len(s.state.WorkspaceNames))
		maps.Copy(stateCopy.WorkspaceNames, s.state.WorkspaceNames)
	}
	if s.state.WorkspaceOrder != nil {
		stateCopy.WorkspaceOrder = slices.Clone(s.state.WorkspaceOrder)
	}
	stateCopy.Tasks = slices.Clone(s.state.Tasks)
	stateCopy.WindowMoves = slices.Clone(s.state.WindowMoves)
	if s.state.WorkspaceMasterRatio != nil {
		stateCopy.WorkspaceMasterRatio = maps.Clone(s.state.WorkspaceMasterRatio)
	}
	if s.state.WorkspaceStackRatio != nil {
		stateCopy.WorkspaceStackRatio = maps.Clone(s.state.WorkspaceStackRatio)
	}
	if s.state.WorkspaceMasterSplits != nil {
		stateCopy.WorkspaceMasterSplits = cloneMasterSplits(s.state.WorkspaceMasterSplits)
	}
	if s.state.WorkspaceMasterLayout != nil {
		stateCopy.WorkspaceMasterLayout = maps.Clone(s.state.WorkspaceMasterLayout)
	}
	if s.state.WorkspaceHasCustom != nil {
		stateCopy.WorkspaceHasCustom = maps.Clone(s.state.WorkspaceHasCustom)
	}
	if s.state.WorkspaceLayoutModes != nil {
		stateCopy.WorkspaceLayoutModes = maps.Clone(s.state.WorkspaceLayoutModes)
	}
	stateCopy.PushSeen = maps.Clone(s.pushSeen)
	stateCopy.LayoutTreeOps = true
	stateCopy.ScratchWorkspaces = true
	stateCopy.SnapshotSeq = s.snapSeq.Add(1)
	stateCopy.changeSeq = s.changeSeq
	return &stateCopy
}

// SetDisplayName records the session's optional display label. It runs through
// mutateState, so the new label reaches every attached client on the same push
// every other daemon-side mutation uses, and the periodic resurrection save
// picks it up with the rest of the state. An empty name clears the label; the
// session's identity (Name) is untouched either way.
func (s *Session) SetDisplayName(name string) error {
	return s.mutateState(func(st *SessionState) error {
		st.DisplayName = name
		return nil
	})
}

// SetAccent records the session's optional accent slot, propagated and persisted
// exactly as SetDisplayName is. The value is opaque to the daemon: it is the
// client's palette that knows what a slot name means.
func (s *Session) SetAccent(accent string) error {
	return s.mutateState(func(st *SessionState) error {
		st.Accent = accent
		return nil
	})
}

// SetNote records the session's return note, or the note of the window
// target names when target is not empty, propagated and persisted exactly as
// SetDisplayName is. The note is stored as given: the verb cleans it.
// It returns the window id the note went on, empty for the session.
func (s *Session) SetNote(target, note string) (string, error) {
	var id string
	err := s.mutateState(func(st *SessionState) error {
		if target == "" {
			st.Note = note
			return nil
		}
		idx, err := findWindowStateIndex(st.Windows, target)
		if err != nil {
			return err
		}
		if idx < 0 {
			return errfmt.Errorf("no window found matching %q", target)
		}
		st.Windows[idx].Note = note
		id = st.Windows[idx].ID
		return nil
	})
	return id, err
}

// RestoreNotes puts saved notes back on a session a restore just rebuilt.
// UpdateState keeps the daemon's own copy of the notes (see
// retainDaemonExclusive), and a new session has none, so the restore cannot
// hand them in through the state it pushes.
func (s *Session) RestoreNotes(sessionNote string, windowNotes map[string]string) {
	if sessionNote == "" && len(windowNotes) == 0 {
		return
	}
	_ = s.mutateState(func(st *SessionState) error {
		st.Note = sessionNote
		for i := range st.Windows {
			if n, ok := windowNotes[st.Windows[i].ID]; ok {
				st.Windows[i].Note = n
			}
		}
		return nil
	})
}

// SetSpace files the session under the named space, propagated and persisted
// exactly as SetDisplayName is. An empty space takes it out of any.
func (s *Session) SetSpace(space string) error {
	return s.mutateState(func(st *SessionState) error {
		st.Space = space
		return nil
	})
}

// SetGlobal sets or clears the session's global mark (SessionState.Global),
// propagated and persisted exactly as SetSpace is. A global session is in no
// space, so setting the mark also takes the session out of its space.
func (s *Session) SetGlobal(global bool) error {
	return s.mutateState(func(st *SessionState) error {
		st.Global = global
		if global {
			st.Space = ""
		}
		return nil
	})
}

// SetRank records the session's place in the session order, propagated and
// persisted exactly as SetDisplayName is. Zero takes it out of the order.
func (s *Session) SetRank(rank int) error {
	return s.mutateState(func(st *SessionState) error {
		st.Rank = rank
		return nil
	})
}

func (s *Session) rank() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.Rank
}

// MarkRestored records that this session was rebuilt from saved state. It runs
// through mutateState rather than being written into the state the restore
// pushes, because a client sync always takes the daemon's value for this field
// and would otherwise wipe it right back off.
func (s *Session) MarkRestored() {
	_ = s.mutateState(func(st *SessionState) error {
		st.Restored = true
		return nil
	})
}

// ClearRestored drops the restored mark now that a client is looking at the
// session. It is a no-op on a session that was not restored.
//
// Deliberately not published to the session's clients. This runs inside the
// attach handler, which has already recorded the connection's session, so a
// state push here reaches the attaching client on the same socket ahead of the
// attach reply it is blocked waiting for, and that client fails the attach with
// "unexpected response". Nothing needs the push: every surface that shows the
// mark for a session it is not attached to reads it from the session listing,
// which is polled.
func (s *Session) ClearRestored() {
	s.stateMu.RLock()
	restored := s.state.Restored
	s.stateMu.RUnlock()
	if !restored {
		return
	}
	_, _ = s.mutateStateLocked(func(st *SessionState) error {
		st.Restored = false
		return nil
	})
}

// SetOption records a daemon-owned session option under stateMu. It is the write
// side of the JSON verb protocol's set-option and is safe for concurrent use.
func (s *Session) SetOption(key, value string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state.Options == nil {
		s.state.Options = make(map[string]string)
	}
	s.state.Options[key] = value
	s.noteStateChangeLocked()
}

// GetOption reads a daemon-owned session option under stateMu, returning the
// value and whether the key was set. It is the read side of get-option.
func (s *Session) GetOption(key string) (string, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.state.Options == nil {
		return "", false
	}
	v, ok := s.state.Options[key]
	return v, ok
}

// OptionKeys returns every option key set on this session, sorted. It backs the
// available-keys hint on a get-option miss, so a caller that guessed a key wrong
// learns which keys exist without a second round trip.
func (s *Session) OptionKeys() []string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	keys := make([]string, 0, len(s.state.Options))
	for k := range s.state.Options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ResurrectionState returns a copy of the session state enriched for on-disk
// resurrection: each window's Cwd is filled from its live PTY process so a
// cold-start restore can respawn the shell in the same directory. Clients never
// send Cwd, so this daemon-side capture is the only source of it.
func (s *Session) ResurrectionState() *SessionState {
	state := s.GetState()
	for i := range state.Windows {
		ptyID := state.Windows[i].PTYID
		if ptyID == "" {
			continue
		}
		if pty := s.GetPTY(ptyID); pty != nil {
			if cwd, ok := pty.ProcessCwd(); ok {
				state.Windows[i].Cwd = cwd
			}
		}
	}
	live := make(map[string]bool, len(state.Windows))
	for i := range state.Windows {
		live[state.Windows[i].ID] = true
		state.Windows[i].StartDir = s.windowStartDir(state.Windows[i].ID)
	}
	s.startDirs.Range(func(k, v any) bool {
		if !live[k.(string)] && time.Since(v.(startDirEntry).at) > time.Minute {
			s.startDirs.Delete(k)
		}
		return true
	})
	if len(state.Windows) > 0 {
		s.refreshWorktree(state.Windows[0].Cwd)
	}
	state.SessionID = s.ID
	state.StartDir = s.StartDir()
	state.KittyGraphics, state.SixelGraphics = s.GraphicsCapabilities()
	state.LastUsed = 0
	if used := s.LastUsed(); !used.IsZero() {
		state.LastUsed = used.UnixNano()
	}
	return state
}

// UpdateState converges a client's view of the session onto the daemon's state.
//
// The daemon owns this state, so a client sync does not simply replace it. The
// incoming snapshot carries the daemon Version the client last saw. When that
// version is current the client has seen everything the daemon did and its
// snapshot is taken as sent. When it is behind, the client built its snapshot
// before a daemon-side mutation it has never seen (a layout op the same client
// sent by any client does not count: see missedMutationLocked), and the fields the daemon
// owns are restored on top of it rather than being silently undone. Fields no
// client ever sets (Options, Cwd, ResurrectionVersion) are carried over either
// way.
//
// It reports whether the state was applied as the client sent it. False means
// the result differs from what was pushed, and the caller is expected to send
// the merged state back so that client converges instead of pushing the same
// stale view again.
//
// This is where an attached TUI's mutations land, so it is also where the window
// lifecycle events for those mutations are raised: the state that ends up
// canonical is diffed against the state it replaces. See state_events.go.
func (s *Session) UpdateState(state *SessionState) bool {
	return s.UpdateStateFrom(state, true)
}

// NotePush records that the push numbered seq from the client connection origin
// has reached the session, so every state handed out from here on says so in
// PushSeen. It is recorded whether or not the push is then accepted: a client
// counts every push it sent, and a refused one that was never counted here
// would leave it taking every later broadcast for an older one.
func (s *Session) NotePush(origin string, seq uint64) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.notePushLocked(origin, seq)
}

func (s *Session) notePushLocked(origin string, seq uint64) {
	if origin == "" || seq == 0 || len(origin) > maxPushOriginLen {
		return
	}
	if s.pushSeen == nil {
		s.pushSeen = make(map[string]uint64)
	}
	if seq > s.pushSeen[origin] {
		s.pushSeen[origin] = seq
	}
}

// ForgetPush drops a client connection's entry from the push table once it has
// left the session, so the table holds the clients that are here.
func (s *Session) ForgetPush(origin string) {
	if origin == "" {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	delete(s.pushSeen, origin)
	if s.refusedOrigin == origin {
		s.refusedOrigin = ""
	}
	delete(s.clientFocusMoved, origin)
	delete(s.askedFocusMoved, origin)
}

// UpdateStateFrom is UpdateState for a push from a client that may or may not
// be the person. seen false keeps the push from marking the focused window's
// finished turn seen: a client running inside a pane is an agent looking, and
// finished_unread is about whether the person has. See human_origin.go.
func (s *Session) UpdateStateFrom(state *SessionState, seen bool) bool {
	accepted, _, _ := s.updateStateFrom(state, seen)
	return accepted
}

func (s *Session) updateStateFrom(state *SessionState, seen bool) (accepted, behind bool, seq uint64) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	origin := state.PushOrigin
	s.notePushLocked(origin, state.PushSeq)
	state.PushOrigin, state.PushSeq, state.PushSeen, state.SnapshotSeq = "", 0, nil, 0

	accepted = true
	unmoved := state.FocusUnmoved
	state.FocusUnmoved = false
	byCommand := state.FocusByCommand
	state.FocusByCommand = false
	prev := s.state
	if prev != nil && unmoved && keepSessionFocus(state, prev) {
		accepted = false
	}
	staleForeign := false
	if prev != nil {
		behind = state.BaseVersion != 0 && s.missedPeerTreeLocked(origin, state.BaseVersion, prev.Version)
		if state.BaseVersion != 0 && s.missedMutationLocked(state.BaseVersion, prev.Version) {
			if origin != "" && s.refusedOrigin == origin {
				staleForeign = s.foreignPushLocked(state, prev)
			}
			mine := focusViewOf(state)
			reconcileStale(state, prev, s.hasLivePTY)
			if state.BaseVersion < s.zoomOpAt {
				keepDaemonZoom(state, prev)
			}
			if s.pushOwnsFocusLocked(origin, state.BaseVersion, mine) {
				keepClientFocus(state, mine)
			}
			accepted = false
		} else if s.foreignPushLocked(state, prev) {
			if origin == "" || s.refusedOrigin != origin {
				s.refusedOrigin = origin
				LogError("Refused a state update to %s: it names none of the session's windows", s.Name())
			}
			return false, behind, s.changeSeq
		}
		if s.refusedOrigin == origin && !staleForeign {
			s.refusedOrigin = ""
		}
		if s.workspaceRenumberMissedLocked(state.BaseVersion) {
			takeWorkspaceMapsFrom(state, prev)
			accepted = false
		}
		retainDaemonExclusive(state, prev)
		state.Version = prev.Version
	}
	if moves := s.askedFocusMoved[origin]; moves != nil && state.BaseVersion != 0 {
		moves.seen(state.BaseVersion)
		if moves.last() == 0 {
			delete(s.askedFocusMoved, origin)
		}
	}
	state.BaseVersion = 0

	before := snapshotLifecycle(prev)
	if prev != nil && !focusViewOf(prev).sameFocus(focusViewOf(state)) {
		if s.clientFocusMoved == nil {
			s.clientFocusMoved = make(map[string]int)
		}
		s.clientFocusMoved[origin] = state.Version
	}
	state.Name = s.Name()
	s.state = state
	if stampMinimizedLocked(state, time.Now().UnixNano()) {
		behind = true
	}
	s.forgetSubagentsLocked(state)
	if seen {
		s.markCompletionSeenLocked(state.FocusedWindowID)
		if !byCommand && state.FocusedWindowID != "" && (prev == nil || prev.FocusedWindowID != state.FocusedWindowID) {
			s.emit(SessionEvent{Type: eventPaneFocused, Window: state.FocusedWindowID})
		}
	}
	s.TouchActive()
	if s.noteWorkspacesLocked() {
		s.state.Version++
		s.noteRenumberLocked()
		accepted = false
	}
	s.noteStateChangeLocked()
	s.emitLifecycleLocked(before)
	return accepted, behind, s.changeSeq
}

func (s *Session) foreignPushLocked(state, prev *SessionState) bool {
	if len(prev.Windows) == 0 || len(state.Windows) == 0 {
		return false
	}
	for i := range state.Windows {
		w := &state.Windows[i]
		for j := range prev.Windows {
			if prev.Windows[j].ID == w.ID {
				return false
			}
		}
		if w.PTYID != "" && s.hasLivePTY(w.PTYID) {
			return false
		}
	}
	return true
}

func (s *Session) pushOwnsFocusLocked(origin string, base int, mine focusView) bool {
	if base < s.focusMovedVersion {
		return false
	}
	for o, v := range s.clientFocusMoved {
		if o != origin && v >= base {
			return false
		}
	}
	for o, moves := range s.askedFocusMoved {
		if o != origin {
			if moves.last() > base {
				return false
			}
			continue
		}
		m, ok := moves.firstAfter(base)
		if !ok {
			continue
		}
		if m.version == 0 || (mine.window == m.before.window && mine.workspace == m.before.workspace) {
			return false
		}
	}
	return true
}

type askedFocusMove struct {
	version int
	before  focusView
}

const maxAskedFocusMoves = 16

type askedFocusMoves struct {
	moves   []askedFocusMove
	dropped int
}

func (a *askedFocusMoves) add(m askedFocusMove) {
	if len(a.moves) == maxAskedFocusMoves {
		a.dropped = a.moves[0].version
		a.moves = a.moves[1:]
	}
	a.moves = append(a.moves, m)
}

func (a *askedFocusMoves) last() int {
	if len(a.moves) == 0 {
		return max(a.dropped, 0)
	}
	return a.moves[len(a.moves)-1].version
}

func (a *askedFocusMoves) firstAfter(base int) (askedFocusMove, bool) {
	if base < a.dropped {
		return askedFocusMove{}, true
	}
	for _, m := range a.moves {
		if m.version > base {
			return m, true
		}
	}
	return askedFocusMove{}, false
}

func (a *askedFocusMoves) seen(base int) {
	if base >= a.dropped {
		a.dropped = 0
	}
	i := 0
	for i < len(a.moves) && a.moves[i].version <= base {
		i++
	}
	a.moves = a.moves[i:]
}

func (s *Session) markFocusIntentLocked() {
	s.focusIntent = true
}

func (s *Session) mutateState(fn func(state *SessionState) error) error {
	snap, err := s.mutateStateLocked(fn)
	if err != nil {
		return err
	}
	s.publishState(snap)
	return nil
}

func (s *Session) mutateStateLocked(fn func(state *SessionState) error) (*SessionState, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	before := snapshotLifecycle(s.state)
	focusBefore := focusViewOf(s.state)
	s.focusIntent, s.focusNeutral, s.focusAskedBy = false, false, ""
	if err := fn(s.state); err != nil {
		s.focusIntent, s.focusNeutral, s.focusAskedBy = false, false, ""
		return nil, err
	}
	s.noteAgentTurnsLocked(before, time.Now().UnixNano())
	stampMinimizedLocked(s.state, time.Now().UnixNano())
	clearStaleSilenceLocked(before, s.state)
	clearNowAtRestLocked(before, s.state)
	wakeSettledLocked(before, s.state)
	clearQuietOfLeftAgentLocked(before, s.state)
	s.forgetSubagentsLocked(s.state)
	pruneDeadLeaves(s.state)
	s.state.Version++
	if !s.focusNeutral && (s.focusIntent || !focusBefore.sameFocus(focusViewOf(s.state))) {
		if s.focusAskedBy != "" {
			if s.askedFocusMoved == nil {
				s.askedFocusMoved = make(map[string]*askedFocusMoves)
			}
			moves := s.askedFocusMoved[s.focusAskedBy]
			if moves == nil {
				moves = &askedFocusMoves{}
				s.askedFocusMoved[s.focusAskedBy] = moves
			}
			moves.add(askedFocusMove{version: s.state.Version, before: focusBefore})
		} else {
			s.focusMovedVersion = s.state.Version
		}
	}
	s.focusIntent, s.focusNeutral, s.focusAskedBy = false, false, ""
	if s.noteWorkspacesLocked() {
		s.noteRenumberLocked()
	}
	s.noteStateChangeLocked()
	s.emitLifecycleLocked(before)
	return s.snapshotStateLocked(), nil
}

func (s *Session) emitLifecycleLocked(before lifecycleSnapshot) {
	events := diffLifecycle(before, snapshotLifecycle(s.state))
	events = append(events, releasedTaskEvents(s, s.state, events, time.Now().UnixNano())...)
	moving := s.movingWindow
	s.movingWindow = ""
	for _, ev := range events {
		if moving != "" && ev.Window == moving {
			switch ev.Type {
			case EventWindowClosed:
				continue
			case EventWindowCreated:
				ev = SessionEvent{Type: EventWindowMoved, Window: ev.Window, PTYID: ev.PTYID, Workspace: ev.hookWorkspace, hookTitle: ev.hookTitle, hookWorkspace: ev.hookWorkspace}
			}
		}
		s.emit(ev)
	}
}

// Stop closes all PTYs and cleans up.
func (s *Session) Stop() {
	if s.stopResurrection != nil {
		s.stopResurrection()
	}
	final := s.ResurrectionState()
	if s.holdingCheckpoint() {
		log.Printf("Session %q keeps its last saved layout: panes were killed by a signal just before the stop", s.Name())
	} else {
		if err := s.persist(final); err != nil {
			LogError("Final resurrection save for session %q failed, it will not come back: %v", s.Name(), err)
		}
		s.persistMu.Lock()
		s.saveHistory(final, true)
		s.persistMu.Unlock()
	}

	s.stopAgentHoldTimer()
	s.idle.stop()
	s.agentMetaPrune.stop()
	s.subagentPrune.stop()
	s.subagentFlush.stop()
	s.dropAgentTranscripts()

	defer s.holdPaneLife()()
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	for id, pty := range s.ptys {
		_ = pty.Close()
		delete(s.ptys, id)
		s.places.Delete(id)
	}
}

// Size returns the current session dimensions.
func (s *Session) Size() (width, height int) {
	s.sizeMu.RLock()
	defer s.sizeMu.RUnlock()
	return s.width, s.height
}

// Resize records the session dimensions when the effective size changes (min
// of all connected clients).
//
// It records and nothing more. A pane's winsize belongs to the client layout,
// which announces each pane's own content size over ResizePTY. Resizing every
// PTY to the session's outer box here handed each guest the whole viewport's
// dimensions; the retile that followed re-announced only panes whose tile size
// changed, so a pane whose geometry survived the resize kept a winsize wider
// than the pane and its shell drew prompts the pane's emulator had to wrap.
func (s *Session) Resize(width, height int) {
	s.sizeMu.Lock()
	s.width = width
	s.height = height
	s.sizeMu.Unlock()
}

// LayoutReserve returns the chrome reserve every client of this session lays
// its panes out around. Recorded here beside the size because it is the other
// half of the same answer.
func (s *Session) LayoutReserve() LayoutReserve {
	s.sizeMu.RLock()
	defer s.sizeMu.RUnlock()
	return s.reserve
}

// SettleLayout records a size and a reserve together and returns the generation
// they were recorded at, which is what tells a client whether an announcement
// carrying them is newer than the one it last applied.
func (s *Session) SettleLayout(width, height int, r LayoutReserve) uint64 {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	s.width, s.height = width, height
	s.reserve = r
	s.layoutGen++
	return s.layoutGen
}

// SizeOwner is the client that set a session's size, as the hint over the
// leftover names it. Every field is "" when it is not known.
type SizeOwner struct {
	// Machine is the client's machine, Kind its ClientKind, and Client its
	// connection ID.
	Machine, Kind, Client string
}

func (s *Session) swapWindowSizePolicy(policy string, by SizeOwner) bool {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	if s.sizePolicy == policy && s.sizedBy == by {
		return false
	}
	s.sizePolicy, s.sizedBy = policy, by
	return true
}

// SizedBy is the client that set the session's size.
func (s *Session) SizedBy() SizeOwner {
	s.sizeMu.RLock()
	defer s.sizeMu.RUnlock()
	return s.sizedBy
}

// WindowSizePolicy is the window_size policy in force for the session's size:
// smallest, largest or latest. Empty before any client has been measured.
func (s *Session) WindowSizePolicy() string {
	s.sizeMu.RLock()
	defer s.sizeMu.RUnlock()
	return s.sizePolicy
}

// LayoutGeneration is the generation the session's layout currently stands at.
func (s *Session) LayoutGeneration() uint64 {
	s.sizeMu.RLock()
	defer s.sizeMu.RUnlock()
	return s.layoutGen
}

// Info returns session information.
func (s *Session) Info() SessionInfo {
	s.sizeMu.RLock()
	width, height := s.width, s.height
	s.sizeMu.RUnlock()

	windows := s.windowSummaries()

	s.stateMu.RLock()
	displayName, accent, space, rank := s.state.DisplayName, s.state.Accent, s.state.Space, s.state.Rank
	note := s.state.Note
	currentWorkspace := s.state.CurrentWorkspace
	restored := s.state.Restored
	focusedPTY := ""
	if w, ok := findWindowState(s.state, s.state.FocusedWindowID); ok {
		focusedPTY = w.PTYID
	}
	s.stateMu.RUnlock()

	var dir, branch string
	if p := s.GetPTY(focusedPTY); p != nil {
		dir, branch = p.place.place()
	}

	return SessionInfo{
		Name:             s.Name(),
		ID:               s.ID,
		Created:          s.Created.Unix(),
		LastActive:       s.LastActive().Unix(),
		WindowCount:      len(windows),
		Attached:         false,
		Width:            width,
		Height:           height,
		Windows:          windows,
		DisplayName:      displayName,
		Accent:           accent,
		Note:             note,
		CurrentWorkspace: currentWorkspace,
		Restored:         restored,
		Global:           s.isGlobal(),
		Space:            space,
		Rank:             rank,
		Dir:              dir,
		Branch:           branch,
		Worktree:         s.worktreeListing(),
	}
}

func (s *Session) windowSummaries() []WindowSummary {
	live := s.liveTitles()
	cwds := s.liveCwds()

	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	if len(s.state.Windows) == 0 {
		return nil
	}
	out := make([]WindowSummary, 0, len(s.state.Windows))
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		title := w.CustomName
		if title == "" {
			title = live[w.PTYID]
		}
		if title == "" {
			title = w.Title
		}
		if title == "" {
			title = "shell"
		}
		fg := w.ForegroundCmd
		if w.CustomName != "" {
			fg = ""
		}
		out = append(out, WindowSummary{
			ID:            w.ID,
			Title:         title,
			AgentState:    string(w.ShownAgentState()),
			AgentReason:   w.AgentReason,
			AgentStateAt:  w.AgentStateAt,
			AgentHarness:  w.AgentHarness,
			AgentMessage:  w.AgentMessage,
			AgentKind:     agentBlockedBy(*w),
			CompletionSeq: w.CompletionSeq,
			AgentMeta:     w.AgentMeta,
			AgentQueued:   w.AgentQueued,
			DraftHeld:     w.AgentDraftHeld,
			AgentQuiet:    w.AgentQuiet,
			AgentSettled:  w.AgentSettled,
			Subagents:     w.AgentSubagents,
			SubagentList:  w.AgentSubagentList,
			ProgramStatus: w.ProgramStatus,
			ForegroundCmd: fg,
			Workspace:     w.Workspace,
			Scratch:       w.Scratch || w.Rail != "",
			Note:          w.Note,
			AgentParent:   agentParentRef(*w),
			Cwd:           firstNonEmpty(cwds[w.PTYID], w.Cwd),
		})
	}
	return out
}

func (s *Session) getShell() string {
	shell, _ := s.resolveShell()
	return shell
}

func (s *Session) resolveShell() (shell, missing string) {
	if s.config != nil && s.config.Shell != "" {
		return s.config.Shell, ""
	}
	preferred := ""
	if s.config != nil && s.config.PreferredShell != nil {
		preferred = s.config.PreferredShell()
	}
	shell, notFound := config.ShellFor(preferred)
	if notFound {
		missing = preferred
	}
	return shell, missing
}

func (s *Session) shellIntegrationFor(pane string) bool {
	if pane != "" {
		return config.ShellIntegrationOn(pane)
	}
	return s.config == nil || s.config.ShellIntegration == nil || s.config.ShellIntegration()
}

func (s *Session) buildEnv(windowID string, restored bool) []string {
	return s.buildEnvWith(windowID, restored, nil)
}

func (s *Session) buildEnvWith(windowID string, restored bool, extra []string) []string {
	return s.buildEnvFor(windowID, 0, restored, extra, nil)
}

func (s *Session) buildEnvFor(windowID string, workspace int, restored bool, extra, command []string) []string {
	env := guestenv.WithoutHostMultiplexer(os.Environ())
	if len(extra) > 0 {
		replaced := make(map[string]bool, len(extra))
		for _, kv := range extra {
			if k, _, ok := strings.Cut(kv, "="); ok {
				replaced[k] = true
			}
		}
		kept := env[:0:0]
		for _, kv := range env {
			if k, _, _ := strings.Cut(kv, "="); !replaced[k] {
				kept = append(kept, kv)
			}
		}
		env = append(kept, extra...)
	}
	if s.config != nil && s.config.AgentEnv != nil && !slices.ContainsFunc(extra, func(kv string) bool {
		return strings.HasPrefix(kv, "SSH_AUTH_SOCK=")
	}) {
		if agent := s.config.AgentEnv(s.ID); len(agent) > 0 {
			env = slices.DeleteFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "SSH_AUTH_SOCK=") })
			env = append(env, agent...)
		}
	}
	if s.config != nil && s.config.DisplayEnv != nil && !slices.ContainsFunc(extra, func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(displayVarNames, k)
	}) {
		if disp := s.config.DisplayEnv(s.ID); len(disp) > 0 {
			env = slices.DeleteFunc(env, func(kv string) bool {
				k, _, _ := strings.Cut(kv, "=")
				return slices.Contains(displayVarNames, k)
			})
			env = append(env, disp...)
		}
	}

	term := ""
	if s.config != nil {
		term = s.config.Term
	}
	env = append(env, "TERM="+guestenv.PaneTerm(term))

	colorTerm := "truecolor"
	if s.config != nil && s.config.ColorTerm != "" {
		colorTerm = s.config.ColorTerm
	}
	env = append(env, "COLORTERM="+colorTerm)
	kitty, sixel := s.GraphicsCapabilities()
	env = append(env, "TERM_PROGRAM="+guestenv.TermProgramFor(command, kitty, sixel))
	env = append(env, "TERM_PROGRAM_VERSION="+guestenv.TermProgramVersion())
	env = append(env, "TUIOS_SESSION="+s.Name())
	if s.config != nil && s.config.HostName != "" {
		env = append(env, "TUIOS_HOST="+s.config.HostName)
	}
	if windowID != "" {
		env = append(env, "TUIOS_WINDOW_ID="+windowID)
		env = append(env, "TUIOS_PANE_ID="+windowID)
		if s.config != nil && s.config.PaneToken != nil {
			if tok := s.config.PaneToken(windowID); tok != "" {
				env = append(env, "TUIOS_PANE_TOKEN="+tok)
			}
		}
		if s.config != nil && s.config.grants != nil {
			env = append(env, "TUIOS_PANE_GRANTS="+s.config.grants.envValue(windowID))
		}
	}
	env = append(env, "TUIOS_ENV=1")
	if s.config != nil && s.config.SocketPath != "" {
		env = append(env, "TUIOS_SOCKET="+s.config.SocketPath)
	}
	env = slices.DeleteFunc(env, func(kv string) bool { return strings.HasPrefix(kv, serverscope.Env+"=") })
	if name := serverscope.Name(); name != "" {
		env = append(env, serverscope.Env+"="+name)
	}
	if s.config != nil && s.config.HerdrEnv != nil {
		env = append(env, s.config.HerdrEnv(s.ID, windowID, s.herdrTabFor(windowID, workspace), command)...)
	}
	if restored {
		env = append(env, "TUIOS_RESTORED=1")
	}

	return env
}

var maxSubscriberQueue int64 = 8 << 20

type ptySubscriber struct {
	out    ptyQueue
	sent   atomic.Int64
	queued atomic.Int64
	gapped atomic.Bool
	missed bool

	framesWaiting atomic.Int32
	skipped       atomic.Int64
	noPace        atomic.Bool

	seen      int64
	skipFrame bool
	skipSpan  bool
	skipFrom  int64
	open      *queuedFrame
	passFrame bool
	latest    map[imageKey]*queuedFrame
	lastFrame *queuedFrame
}

type ptyChunk struct {
	data          []byte
	width, height int
	frame         *queuedFrame
	end           int64
}

func (c ptyChunk) size() int64 {
	if c.frame != nil {
		return c.frame.size
	}
	return int64(len(c.data))
}

type resizeMark struct {
	seq           int64
	width, height int
}

func (c ptyChunk) isResize() bool { return c.width > 0 && c.height > 0 }

var resyncPrefix = []byte("\x1b[H\x1b[2J" + scrollclear.Drop)

var cancelPrefix = []byte{0x18}

// Subscribe adds a subscriber to receive PTY output, resuming it at fromSeq: the
// stream position a previous subscription for this client reached, as returned
// by Unsubscribe. Zero means the client has seen nothing of this PTY and gets
// the whole catch-up buffer.
//
// Resuming is what makes hiding and showing a pane free. A client subscribes
// again every time the pane's workspace becomes current, and it already holds
// the pane's screen; replaying the buffer from the top painted the pane's whole
// history a second time below the paint already there, which is the stacked
// prompts a workspace switch used to leave behind.
func (p *PTY) Subscribe(clientID string, fromSeq int64) *ptyQueue {
	return p.subscribe(clientID, fromSeq, false)
}

// SubscribeFromSnapshot is Subscribe for a client that has just laid down an
// authoritative snapshot of the pane ending at fromSeq. When the catch-up ring
// has rolled past fromSeq, a plain Subscribe clears the client's screen before
// replaying the tail so the tail paints against a known state; that clear
// throws away the snapshot's rows, which are exactly what a full-screen program
// drew and cannot be recovered from the ring (issue #123). A snapshot is the
// whole of the stream up to fromSeq, so the tail replays on top of it.
func (p *PTY) SubscribeFromSnapshot(clientID string, fromSeq int64) *ptyQueue {
	return p.subscribe(clientID, fromSeq, true)
}

func (p *PTY) subscribe(clientID string, fromSeq int64, fromSnapshot bool) *ptyQueue {
	return &p.subscribeSub(clientID, fromSeq, fromSnapshot).out
}

func (p *PTY) subscribeSub(clientID string, fromSeq int64, fromSnapshot bool) *ptySubscriber {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
	p.subscribeLocked(clientID, fromSeq, fromSnapshot)
	return p.subscribers[clientID]
}

func (p *PTY) subscriberFor(clientID string) *ptySubscriber {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()
	return p.subscribers[clientID]
}

func (p *PTY) resumeAfterGap(clientID string, cur *ptySubscriber) (*ptyQueue, *ptySubscriber) {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
	sub, ok := p.subscribers[clientID]
	if !ok || sub != cur || !sub.gapped.Load() || sub.out.len() > 0 {
		return nil, nil
	}
	sub.out.close()
	delete(p.subscribers, clientID)
	debugLog("[DEBUG] PTY %s: client %s fell behind at %d, resuming from the ring", p.ID[:8], clientID, sub.sent.Load())
	q := p.subscribeLocked(clientID, sub.sent.Load(), false)
	next := p.subscribers[clientID]
	next.noPace.Store(sub.noPace.Load())
	p.wakePacer()
	return q, next
}

func (p *PTY) subscribeLocked(clientID string, fromSeq int64, fromSnapshot bool) *ptyQueue {
	if existing, ok := p.subscribers[clientID]; ok {
		debugLog("[DEBUG] PTY %s: client %s already subscribed", p.ID[:8], clientID)
		return &existing.out
	}

	sub := newSubscriber()
	p.subscribers[clientID] = sub
	p.subscriberCount.Store(int32(len(p.subscribers)))
	debugLog("[DEBUG] PTY %s: added subscriber %s (total: %d)", p.ID[:8], clientID, len(p.subscribers))

	p.outputMu.RLock()
	bufStart := p.outputSeq - int64(p.outputPos)
	ringEnd := p.outputPos
	if unscanned := p.unbroadcast.Load(); unscanned > 0 {
		ringEnd = max(0, p.outputPos-int(unscanned))
	}
	endSeq := bufStart + int64(ringEnd)
	start := 0
	rolled := fromSeq > 0 && fromSeq < bufStart
	sub.missed = fromSeq < bufStart
	if fromSeq > bufStart {
		start = min(int(fromSeq-bufStart), ringEnd)
	}
	if inside, end := p.gfx.spanAt(bufStart + int64(start)); inside {
		if end < 0 {
			start = ringEnd
			sub.skipFrame = p.gfx.inFrame
			sub.skipSpan, sub.skipFrom = !p.gfx.inFrame, p.gfx.spanFrom
		} else if end <= endSeq {
			start = int(end - bufStart)
		}
	}
	if n := ringEnd - start; n > 0 {
		debugLog("[DEBUG] PTY %s: sending %d buffered bytes to new subscriber", p.ID[:8], n)
		send := func(c ptyChunk) {
			if sub.out.push(c) {
				sub.queued.Add(int64(len(c.data)))
			} else {
				debugLog("[DEBUG] PTY %s: failed to send catch-up chunk (queue full)", p.ID[:8])
			}
		}
		startSeq := bufStart + int64(start)
		for _, m := range slices.Backward(p.resizeMarks) {
			if m.seq <= startSeq {
				send(ptyChunk{width: m.width, height: m.height})
				break
			}
		}
		var prefix []byte
		switch {
		case rolled && fromSnapshot:
			prefix = cancelPrefix
		case rolled:
			prefix = resyncPrefix
		}
		segStart := start
		cut := func(end int) {
			if end == segStart && prefix == nil {
				return
			}
			seg := make([]byte, 0, len(prefix)+end-segStart)
			seg = append(seg, prefix...)
			prefix = nil
			seg = append(seg, p.outputBuffer[segStart:end]...)
			send(ptyChunk{data: seg, end: bufStart + int64(end)})
			segStart = end
		}
		for _, m := range p.resizeMarks {
			if m.seq <= startSeq || m.seq > endSeq {
				continue
			}
			cut(int(m.seq - bufStart))
			send(ptyChunk{width: m.width, height: m.height})
		}
		cut(ringEnd)
	} else {
		debugLog("[DEBUG] PTY %s: no buffered output to send", p.ID[:8])
	}
	sub.sent.Store(endSeq)
	sub.seen = endSeq
	p.outputMu.RUnlock()

	p.terminalMu.RLock()
	if p.terminal != nil {
		w, h := p.terminal.Width(), p.terminal.Height()
		sub.out.push(ptyChunk{width: w, height: h})
	}
	p.terminalMu.RUnlock()

	return &sub.out
}

// Unsubscribe removes a subscriber and returns the stream position it reached,
// to hand back to Subscribe when the client returns.
func (p *PTY) Unsubscribe(clientID string) int64 {
	return p.unsubscribe(clientID, nil)
}

func (p *PTY) unsubscribeSub(clientID string, sub *ptySubscriber) {
	p.unsubscribe(clientID, sub)
}

func (p *PTY) unsubscribe(clientID string, only *ptySubscriber) int64 {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()

	sub, ok := p.subscribers[clientID]
	if !ok || (only != nil && sub != only) {
		return 0
	}
	sub.out.close()
	delete(p.subscribers, clientID)
	p.subscriberCount.Store(int32(len(p.subscribers)))
	p.wakePacer()
	return sub.sent.Load()
}

// Write sends input to the PTY.
//
// Input of more than largeFrame bytes, such as a paste, takes the pane's one
// slot for a large write. A second large write waits for the slot, so large
// writes into a pane that reads go in one after another, however many queue.
// It is refused with errPaneInputBusy once the write holding the slot has
// held it longer than paneWriteWait: the pane is not reading. The clock is
// the holder's, not the waiter's, so a write queued behind several others
// into a pane that reads slowly is not taken for one into a pane that does
// not read. A pane whose program does not read its input blocks the write,
// and the write holds the input's memory while it waits, so one waiting
// write per pane bounds that memory to the largest input, 16 MiB, for each
// pane, plus the writes waiting for the slot, each until the pane has not
// read for paneWriteWait. Every path that types into a pane comes through here:
// client input, send-text, paste-buffer, send-keys, submit-prompt and
// respond. Only a caller that may already write to the pane can make it hold
// that memory. See frame_budget.go.
func (p *PTY) Write(data []byte) (int, error) {
	if p.pty == nil {
		return 0, errfmt.Errorf("PTY not available")
	}
	if len(data) > largeFrame {
		if err := p.takeLargeSlot(); err != nil {
			return 0, err
		}
		defer p.giveLargeSlot()
	}
	p.flushWinsize()
	if s := p.owner(); s != nil {
		s.noteActivity()
	}
	return p.pty.Write(data)
}

var paneWriteWait = 5 * time.Second

func (p *PTY) largeSlotCh() chan struct{} {
	p.largeSlotOnce.Do(func() { p.largeSlot = make(chan struct{}, 1) })
	return p.largeSlot
}

func (p *PTY) takeLargeSlot() error {
	slot := p.largeSlotCh()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case slot <- struct{}{}:
			p.largeSince.Store(time.Now().UnixNano())
			return nil
		default:
		}
		since := time.Now()
		if ns := p.largeSince.Load(); ns != 0 {
			since = time.Unix(0, ns)
		}
		wait := time.Until(since.Add(paneWriteWait))
		if wait <= 0 {
			return errPaneInputBusy
		}
		if timer == nil {
			timer = time.NewTimer(wait)
		} else {
			timer.Reset(wait)
		}
		select {
		case slot <- struct{}{}:
			p.largeSince.Store(time.Now().UnixNano())
			return nil
		case <-timer.C:
		}
	}
}

func (p *PTY) giveLargeSlot() {
	p.largeSince.Store(0)
	<-p.largeSlotCh()
}

func (p *PTY) largeWriteWaiting() bool { return p.largeSince.Load() != 0 }

var errPaneInputBusy = errors.New("the pane has not read the last large input yet")

// Size returns the current PTY dimensions.
func (p *PTY) Size() (width, height int) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.width, p.height
}

// SetCellSize sets the cell dimensions in pixels for the PTY's VT emulator.
// This enables proper XTWINOPS responses (CSI 14t, CSI 16t) for applications
// that query terminal pixel dimensions.
func (p *PTY) SetCellSize(cellWidth, cellHeight int) {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if p.terminal != nil && cellWidth > 0 && cellHeight > 0 && !p.frozen.Load() {
		p.terminal.SetCellSize(cellWidth, cellHeight)
	}
}

// UpdatePixelDimensions sets the cell size on the VT emulator, records it for
// later resizes, and sets the PTY's pixel size from it and the current size.
//
// The PTY is only written when the cell size changed. Resize already carries
// the pixel size, so the call that follows every resize has nothing to add,
// and writing the same winsize again is a wasted syscall. A pane that cannot
// take pixels (a remote pane, ConPTY) is not written at all.
func (p *PTY) UpdatePixelDimensions(cellWidth, cellHeight int) error {
	if cellWidth <= 0 || cellHeight <= 0 {
		return nil
	}
	p.SetCellSize(cellWidth, cellHeight)

	p.winsizeMu.Lock()
	defer p.winsizeMu.Unlock()
	if p.frozen.Load() {
		p.frozenCellW, p.frozenCellH = cellWidth, cellHeight
		return nil
	}
	if p.cellWidth == cellWidth && p.cellHeight == cellHeight {
		return nil
	}
	p.cellWidth, p.cellHeight = cellWidth, cellHeight
	if _, ok := p.pty.(ptyspawn.WinsizeSetter); !ok {
		return nil
	}
	if p.winsizeHeld.Load() {
		return nil
	}
	width, height := p.Size()
	return p.setWinsizeLocked(width, height)
}

// Resize changes the PTY and terminal emulator size.
//
// The emulator is not resized here. A resize is a point in the pane's output
// stream, not an event outside it: the guest has already produced bytes this
// pane has not laid out yet, and which width they are laid out at decides where
// they wrap. Queueing the resize behind them puts it at one byte, tells every
// subscriber the same byte, and leaves the daemon and its clients agreeing on
// the line at the seam. Resizing the emulator here instead let a client that
// had already resized itself lay out everything produced between asking and
// being heard one width narrower than the daemon did, and a line that wrapped
// differently is in the scrollback for good.
func (p *PTY) Resize(width, height int) error {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if width > 0 && height > 0 {
		p.askedW, p.askedH = width, height
	}
	width, height = p.leasedSizeLocked(width, height)
	if s := p.owner(); s != nil {
		if w := s.wakes.Load(); w != nil {
			w.cleanup.Set()
		}
	}
	return p.resizeStreamLocked(width, height)
}

func (p *PTY) resizeStreamLocked(width, height int) error {
	if p.frozen.Load() {
		if width > 0 && height > 0 {
			p.frozenW, p.frozenH = width, height
		}
		return nil
	}
	p.terminalMu.Lock()
	unchanged := width > 0 && height > 0 && p.width == width && p.height == height
	oldW, oldH := p.width, p.height
	p.width, p.height = width, height
	p.terminalMu.Unlock()
	if unchanged {
		return nil
	}
	LogBasic("PTY %s resized %dx%d -> %dx%d", shortID(p.ID), oldW, oldH, width, height)

	if !p.vtClosed {
		p.outputMu.Lock()
		p.resizeMarks = append(p.resizeMarks, resizeMark{seq: p.outputSeq, width: width, height: height})
		p.outputMu.Unlock()
		p.broadcast(ptyChunk{width: width, height: height}, 0)
		select {
		case p.vtWriteChan <- vtChunk{width: width, height: height}:
		case <-p.ctx.Done():
		}
	}

	p.winsizeMu.Lock()
	defer p.winsizeMu.Unlock()
	return p.setWinsizeLocked(width, height)
}

// GetTerminalState returns the current terminal screen state for restore.
// Returns the visible screen content as a 2D array of cells.
//
// maxScrollback bounds the scrollback rows included: zero means
// DefaultStateScrollback and a negative number means none. The rows returned
// are the newest ones. Taking them from the front instead, which is what this
// did, handed a pane with a long history its most ancient screenfuls and
// dropped everything the user had actually been looking at.
//
// have is how many rows the caller's own emulator already holds, and bounds the
// reply the same way maxScrollback does: only the rows past it can be used, so
// only those are sent. ScrollbackLen in the reply is always the true length, so
// the caller's own arithmetic against it is unaffected by either bound.
func (p *PTY) GetTerminalState(maxScrollback, have int) *TerminalState {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()

	if p.terminal == nil {
		return nil
	}

	state := TerminalStateOf(p.terminal, p.terminal.Width(), p.terminal.Height(), maxScrollback, have)
	state.Seq = p.vtSeq
	return state
}

// GetTerminalStatePacked is GetTerminalState with the cells packed as they are
// read, for a client that asked for the packed form. The result is the same as
// GetTerminalState followed by Pack, without building every cell of the screen
// and the history as a CellState first: for a screen that was 1.4 MB of
// garbage per request, allocated under terminalMu.
//
// The history rows are copied in their encoded form under terminalMu and
// packed after it is released, so the pane's output waits for a copy of a
// few bytes a cell and not for the packing: 1000 rows of a 207-column pane
// held it for about 50 ms.
func (p *PTY) GetTerminalStatePacked(maxScrollback, have int) *TerminalState {
	p.terminalMu.RLock()
	if p.terminal == nil {
		p.terminalMu.RUnlock()
		return nil
	}
	state, finish := snapshot.CapturePacked(p.terminal, p.terminal.Width(), p.terminal.Height(), maxScrollback, have)
	state.Seq = p.vtSeq
	p.terminalMu.RUnlock()
	finish()
	return state
}

// CaptureContent renders the PTY's current screen (and optionally its
// scrollback) to text from the daemon-side VT emulator. When ansi is true the
// output keeps SGR escape sequences; otherwise it is plain text.
//
// This is how capture-pane is answered, attached or not. It used to be answered
// here only when nothing was attached and routed to the client otherwise, which
// made the result of a read depend on whether someone happened to be watching.
// The client's OS.capturePane is the same rendering of a VT emulator fed by the
// same PTY, so there was nothing the round trip could add; it survives only as
// the local scrollback browser's own reader.
func (p *PTY) CaptureContent(scrollback, ansi bool) string {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return vt.StripKittyPlaceholders(p.captureContent(scrollback, ansi))
}

type paneMeta struct {
	HistoryRows int
	Revision    int64
}

func (p *PTY) metaLocked() paneMeta {
	m := paneMeta{Revision: p.vtSeq + p.vtResizes}
	if p.terminal != nil {
		m.HistoryRows = p.terminal.ScrollbackLen()
	}
	return m
}

// Meta is the pane's paneMeta now.
func (p *PTY) Meta() paneMeta {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.metaLocked()
}

// CaptureContentMeta is CaptureContent plus the pane's paneMeta, read under
// the same lock, so the revision is the one the content was taken at.
//
// unwrap joins the rows the terminal soft-wrapped (see capture_unwrap.go). It
// is read under the same lock as the content, so the wrap flags describe the
// rows that were captured.
func (p *PTY) CaptureContentMeta(scrollback, ansi, unwrap bool) (string, paneMeta) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	content := p.captureContent(scrollback, ansi)
	if unwrap {
		content = unwrapCapture(p.terminal, content, scrollback)
	}
	return content, p.metaLocked()
}

// CaptureContentResolvedMeta is CaptureContent with styling on, plus the SGR
// index colours rewritten to 24-bit RGB against the given palette, and the
// pane's paneMeta, read under the same lock. The daemon still knows nothing
// about themes: the palette is an explicit parameter, the client's theme
// palette, so the capture matches what the client paints. A bare reset stays
// a bare reset. Indices 0-15 come from the palette, the standard 256-colour
// cube and grey ramp resolve through their fixed formulas, and only true
// colour passes through untouched.
func (p *PTY) CaptureContentResolvedMeta(scrollback bool, palette [16]color.Color, unwrap bool) (string, paneMeta) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	content := p.captureContent(scrollback, true)
	if unwrap {
		content = unwrapCapture(p.terminal, content, scrollback)
	}
	return ResolveSGR(content, palette), p.metaLocked()
}

func (p *PTY) captureContent(scrollback, ansi bool) string {
	return vt.StripSixelMarkers(p.captureContentRaw(scrollback, ansi))
}

func (p *PTY) captureContentRaw(scrollback, ansi bool) string {
	if p.terminal == nil {
		return ""
	}

	var content string
	if ansi {
		content = p.terminal.Render()
	} else {
		content = p.terminal.String()
	}

	if scrollback {
		scrollbackLen := p.terminal.ScrollbackLen()
		if scrollbackLen > 0 {
			var sb strings.Builder
			if texter, ok := p.terminal.(scrollbackTexter); ok && !ansi {
				texter.AppendScrollbackText(&sb)
				sb.WriteString(content)
				return sb.String()
			}
			p.terminal.ScrollbackRows(0, scrollbackLen, func(_ int, line uv.Line) bool {
				if ansi {
					sb.WriteString(line.Render())
				} else {
					sb.WriteString(line.String())
				}
				sb.WriteByte('\n')
				return true
			})
			sb.WriteString(content)
			content = sb.String()
		}
	}

	return content
}

type scrollbackTexter interface {
	AppendScrollbackText(*strings.Builder)
}

type captureState struct {
	seq           int64
	width, height int
}

func (p *PTY) captureStateLocked() captureState {
	st := captureState{seq: p.vtSeq}
	if p.terminal != nil {
		st.width, st.height = p.terminal.Width(), p.terminal.Height()
	}
	return st
}

func (p *PTY) currentCaptureState() captureState {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.captureStateLocked()
}

func (p *PTY) capturePlainAt(scrollback bool) (string, captureState) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.captureContent(scrollback, false), p.captureStateLocked()
}

// Close terminates the PTY.
func (p *PTY) Close() error {
	p.cancel()

	p.stopScreenSettle()

	p.subscribersMu.Lock()
	for id, sub := range p.subscribers {
		sub.out.close()
		delete(p.subscribers, id)
	}
	p.subscriberCount.Store(0)
	p.subscribersMu.Unlock()

	killPaneTree(p.proc)
	if p.gate != nil {
		p.gate.wake()
	}

	if p.terminal != nil {
		_ = p.terminal.Close()
	}

	p.closeWinsize()

	memtrim.Request()

	if p.pty != nil {
		return p.pty.Close()
	}
	return nil
}

// ProcessCwd returns the current working directory of the PTY's shell process.
// The second return is false when it cannot be determined (process gone, or an
// unsupported platform). Used to capture cwd for session resurrection.
func (p *PTY) ProcessCwd() (string, bool) {
	if rp, ok := p.pty.(*remotePane); ok {
		return rp.Cwd()
	}
	if p.proc == nil {
		return "", false
	}
	return ptyspawn.ProcessCwd(p.proc.Pid)
}

// ShellPID returns the process id of the PTY's shell child, or 0 when the process
// is not running. It is the anchor the agent auto-detector uses to resolve the
// pane's foreground process group.
func (p *PTY) ShellPID() int {
	if p.proc == nil {
		return 0
	}
	return p.proc.Pid
}

// TTYName returns the path of the pane's terminal device, such as /dev/pts/3.
// It is empty for a pane on another machine, and on a platform whose PTY has
// no device name (ConPTY).
func (p *PTY) TTYName() string {
	if named, ok := p.pty.(interface{ SlaveName() string }); ok {
		return named.SlaveName()
	}
	return ""
}

// IsExited returns true if the shell process has exited.
func (p *PTY) IsExited() bool {
	p.exitedMu.RLock()
	defer p.exitedMu.RUnlock()
	return p.exited
}

// ExitStatus returns the process's exit status and whether it has exited. A
// process ended by a signal reports -1.
// ExitSignal is the signal that ended the pane's process, 0 when it exited
// on its own or is still running.
func (p *PTY) ExitSignal() int { return int(p.exitSignal.Load()) }

func (p *PTY) ExitStatus() (int, bool) {
	p.exitedMu.RLock()
	defer p.exitedMu.RUnlock()
	return p.exitCode, p.exited
}

const gateRetryDelay = 50 * time.Millisecond

func (p *PTY) readOutput() {
	defer func() {
		p.noteReadEnd()
		p.streamMu.Lock()
		p.vtClosed = true
		close(p.vtWriteChan)
		p.streamMu.Unlock()
		p.rawLog.Close()
		if p.gate != nil {
			p.gate.close()
		}
	}()

	buf := make([]byte, 16*1024)
	gateFailed := false
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		p.holdForSlowSubscribers()

		if p.gate != nil {
			ready, err := p.gate.wait(p.pty)
			if err != nil {
				if !gateFailed {
					LogError("Pane %s: %v. Retrying", shortID(p.ID), err)
					gateFailed = true
				}
				select {
				case <-time.After(gateRetryDelay):
				case <-p.ctx.Done():
					return
				}
				continue
			}
			if !ready {
				return
			}
		}
		p.readMu.Lock()
		if p.quiesced {
			resumed := p.resumed
			p.readMu.Unlock()
			select {
			case <-resumed:
			case <-p.ctx.Done():
				return
			}
			continue
		}

		n, err := p.pty.Read(buf)
		if err != nil {
			p.readMu.Unlock()
			if p.host != "" {
				p.noteExit(0)
			}
			return
		}

		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			reportGroup := p.readerReportGroup(data)

			p.rawLog.Write(data)

			p.scanNestProbes(data)

			p.streamMu.Lock()
			p.readMu.Unlock()

			p.outputMu.Lock()
			seq := p.appendToBuffer(data)
			for _, tap := range p.outputTaps {
				tap.add(data)
			}
			p.outputMu.Unlock()

			p.broadcast(ptyChunk{data: data}, seq)

			select {
			case p.vtWriteChan <- vtChunk{data: data, seq: seq, group: reportGroup}:
			case <-p.ctx.Done():
				p.streamMu.Unlock()
				return
			}
			p.streamMu.Unlock()
			p.reportPacing()

			p.lastOutput.Store(time.Now().UnixNano())
			if f := p.draftWatch.Load(); f != nil {
				(*f)()
			}

			if p.emit != nil {
				p.emit(SessionEvent{Type: EventOutput, Bytes: n})
			}
		} else {
			p.readMu.Unlock()
		}
	}
}

type vtChunk struct {
	data          []byte
	seq           int64
	width, height int
	group         int64
	ack           chan struct{}
	notice        bool
}

func (s *Session) setUpPaneTerminal(pty *PTY, terminal vt.Terminal) {
	scrollclear.Apply(terminal)
	if pty.host == "" && ptyspawn.HostIsConPTY() {
		terminal.DisableKittyKeyboardProtocol()
	}
	if pty.cellWidth > 0 && pty.cellHeight > 0 {
		terminal.SetCellSize(pty.cellWidth, pty.cellHeight)
	}

	pty.marks = terminal.SemanticMarkers()
	terminal.SetCallbacks(vt.Callbacks{
		Bell: func() { pty.emit(SessionEvent{Type: EventBell}) },
		Title: func(title string) {
			pty.title.Store(&title)
			pty.emit(SessionEvent{Type: EventWindowRetitled, Title: title})
		},
		ExplicitIconName: func(name string) {
			if pty.setIconName(ClampDisplayText(name)) {
				pty.owner().publishLiveFactsSoon()
			}
		},
		AltScreen: func(on bool) {
			pty.emit(SessionEvent{Type: EventModeChanged, Mode: "alt-screen", Enabled: on})
		},
		WorkingDirectory: func(raw string) {
			if _, remote := pty.pty.(*remotePane); remote {
				pty.place.setCwd(raw)
				return
			}
			if pty.place.announce(raw) {
				pty.owner().publishLiveFactsSoon()
			}
		},
		Progress: func(state vt.ProgressState, percent int) {
			pty.storeAgentProgress(state, percent)
		},
		Notify: func(title, body string) {
			title, body = capNotifyText(title), capNotifyText(body)
			pty.storeAgentNotify(title, body)
			pty.emit(SessionEvent{Type: EventNotification, Title: title, Body: body})
		},
		SemanticMark: func(m vt.SemanticMarker) {
			pty.noteProgramStatusMark(m)
			pty.noteShellMark(m)
		},
		ProgramStatus: pty.noteProgramStatus,
		PrivateOSC: func(cmd int, data []byte) {
			if cmd != dnd.Code || pty.host != "" || !s.dragAndDrop() {
				return
			}
			if reply := pty.dnd.note(data, func() int { return ForegroundGroup(pty.ShellPID()) }); reply != nil {
				terminal.WriteResponse(reply)
			}
		},
	})

	terminal.SetSixelAdvertised(func() bool { return pty.owner().SixelAdvertised() })

	remotePane := pty.host != ""
	terminal.SetKittyHeaderOnly(true)
	terminal.SetKittyFilesElsewhere(remotePane)
	terminal.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		if cmd.Action == vt.KittyActionQuery {
			if response := pty.owner().kittyQueryResponse(cmd, remotePane); response != nil {
				terminal.WriteResponse(response)
			}
			return
		}
		if response := kittyAnimationRefusal(cmd, remotePane, pty.owner().kittyAnimation.Load()); response != nil {
			terminal.WriteResponse(response)
		}
		if !remotePane && pty.subscriberCount.Load() == 0 {
			releaseUnreadKittyMedium(cmd)
		}
	})
}

func (p *PTY) vtWriter() {
	if p.vtDone != nil {
		defer close(p.vtDone)
	}
	var size [2]int
	p.terminalMu.Lock()
	if p.terminal != nil {
		size = [2]int{p.terminal.Width(), p.terminal.Height()}
	}
	p.terminalMu.Unlock()
	var faults []time.Time
	frozen := false
	for chunk := range p.vtWriteChan {
		if chunk.ack != nil {
			close(chunk.ack)
			continue
		}
		resize := chunk.width > 0 && chunk.height > 0
		if resize {
			size = [2]int{chunk.width, chunk.height}
		}
		focusOn, fault, stack := p.feedVT(chunk, frozen)
		if fault != nil {
			faults, frozen = p.vtFault(chunk, fault, stack, faults, size)
			continue
		}
		if resize {
			continue
		}
		p.noteFocusReporting(focusOn)
		p.flushProgramStatus()
	}
}

const outputRingMax = 64 * 1024

func outputRingSize(n int) int {
	size := 1024
	for size < n && size < outputRingMax {
		size *= 2
	}
	return min(size, outputRingMax)
}

func (p *PTY) appendToBuffer(data []byte) int64 {
	p.unbroadcast.Add(int64(len(data)))
	p.outputSeq += int64(len(data))
	bufStart := p.outputSeq - int64(max(len(p.outputBuffer), outputRingMax))
	for len(p.resizeMarks) > 1 && p.resizeMarks[1].seq <= bufStart {
		p.resizeMarks = p.resizeMarks[1:]
	}
	if len(p.outputBuffer)-p.outputPos < len(data) && len(p.outputBuffer) < outputRingMax {
		grown := make([]byte, outputRingSize(p.outputPos+len(data)))
		copy(grown, p.outputBuffer[:p.outputPos])
		p.outputBuffer = grown
	}
	bufLen := len(p.outputBuffer)
	if len(data) >= bufLen {
		copy(p.outputBuffer, data[len(data)-bufLen:])
		p.outputPos = bufLen
		return p.outputSeq
	}
	for bufLen-p.outputPos < len(data) && p.outputPos > 0 {
		half := min(bufLen/2, p.outputPos)
		copy(p.outputBuffer, p.outputBuffer[half:p.outputPos])
		p.outputPos -= half
	}
	n := copy(p.outputBuffer[p.outputPos:], data)
	p.outputPos += n
	return p.outputSeq
}

func (p *PTY) broadcast(chunk ptyChunk, seq int64) {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()

	if p.debug {
		debugLog("[DEBUG] PTY %s: BROADCAST called with %d bytes, %d subscribers", p.ID[:8], len(chunk.data), len(p.subscribers))
	}
	if chunk.isResize() {
		for clientID, sub := range p.subscribers {
			if sub.gapped.Load() {
				continue
			}
			if sub.open != nil {
				p.flushOpen(clientID, sub)
			}
			if !sub.out.push(chunk) {
				sub.gapped.Store(true)
				sub.forgetFrames()
			}
		}
		return
	}

	p.reads++
	p.unbroadcast.Add(-int64(len(chunk.data)))
	segs, saw := p.gfx.scan(chunk.data, seq)
	p.gfx.pruneSpans(seq - int64(max(len(p.outputBuffer), outputRingMax)))
	if saw {
		p.graphicsAt.Store(time.Now().UnixNano())
	}
	for clientID, sub := range p.subscribers {
		p.route(clientID, sub, segs)
	}
}

func (p *PTY) noteExit(code int) {
	p.exitedMu.Lock()
	if p.exited {
		p.exitedMu.Unlock()
		return
	}
	p.exited = true
	p.exitCode = code
	p.exitedMu.Unlock()
	p.logExit(code)
	if p.keepOpen != nil {
		p.keepOpen.exited(p)
	}

	debugLog("[DEBUG] PTY %s: process exited with code %d", p.ID[:8], p.exitCode)

	if p.progStatus.DropTransient() {
		p.progStatusDirty.Store(true)
		p.flushProgramStatus()
	}

	if p.exitedAlone != nil && p.ctx.Err() == nil && p.keepOpen == nil {
		p.exitedAlone(p.ID, p.ownShell, p.ExitSignal())
	}

	if p.onExit != nil {
		p.onExit(p.ID)
	}

	if p.emit != nil {
		p.emit(SessionEvent{Type: EventWindowExit})
	}
}

func (p *PTY) forwardTerminalResponses() {
	if p.terminal == nil {
		return
	}
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.respWake:
		}
		p.respMu.Lock()
		p.sendResponsesLocked()
		p.respMu.Unlock()
	}
}

func (p *PTY) sendResponsesLocked() {
	for {
		data := p.terminal.TakeResponses()
		if len(data) == 0 || p.pty == nil {
			return
		}
		_, _ = p.pty.Write(data)
	}
}

func (p *PTY) wakeResponses() {
	select {
	case p.respWake <- struct{}{}:
	default:
	}
}

func (s *Session) isGlobal() bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.Global
}

// ScratchKey is the name a scratch pane is kept under, with the built-in
// scratch terminal's name filled in.
func (w WindowState) ScratchKey() string {
	if w.ScratchName == "" {
		return "scratch"
	}
	return w.ScratchName
}
