package session

import (
	"context"
	"fmt"
	"image/color"
	"io"

	"log"
	"maps"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// debugEnabled returns true if debug logging is enabled via TUIOS_DEBUG_INTERNAL env var
func debugEnabled() bool {
	return os.Getenv("TUIOS_DEBUG_INTERNAL") == "1"
}

// debugLog logs a message only if debug mode is enabled
func debugLog(format string, args ...any) {
	if debugEnabled() {
		log.Printf(format, args...)
	}
}

// WindowState represents the serializable state of a window.
type WindowState struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CustomName   string `json:"custom_name,omitempty"`
	X            int    `json:"x"`
	Y            int    `json:"y"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Z            int    `json:"z"`
	Workspace    int    `json:"workspace"`
	Minimized    bool   `json:"minimized,omitempty"`
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
	// Cwd is the working directory of the window's shell process, captured on the
	// daemon side when saving resurrection state. On cold-start restore a fresh
	// shell is respawned here. Empty for live state syncs (clients do not set it).
	Cwd string `json:"cwd,omitempty"`
	// CwdHost names the machine the pane's shell last reported its folder on,
	// when that is not the machine the pane runs on: the pane is running ssh.
	// Empty otherwise. A live fact, filled into every snapshot and never
	// stored. A client lists no files for such a pane, because Cwd is not a
	// folder on any disk it can reach.
	CwdHost string `json:"cwd_host,omitempty"`
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
	// AgentSubagents is how many subagents the pane's agent is running, as its
	// hooks reported them (see agent_subagents.go). Daemon-owned like
	// AgentQueued and never set by a client. Additive: zero, which is what an
	// older daemon sends, means none.
	AgentSubagents int `json:"agent_subagents,omitzero"`
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
	// ForegroundCmd is the base name of the program running in the pane's
	// foreground, empty while the pane sits at its login shell. It is what lets a
	// row say "nvim" instead of repeating a title every pane in one directory
	// shares. Daemon-owned and refreshed by the agent detector's existing poll,
	// so it costs no extra process reads and may be one poll out of date, which
	// is fine for a label.
	ForegroundCmd string `json:"foreground_cmd,omitempty"`
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
	// DisplayName is an optional user-facing label. Name stays the identity: it
	// keys the session map, names the resurrection file, is exported as
	// TUIOS_SESSION into every pane, and is what every verb's session parameter
	// resolves against. Renaming for display must not disturb any of that, which
	// is why the label is a field of its own rather than a write to Name.
	//
	// Empty means unnamed, and every reader falls back to Name, so a session that
	// was never renamed reads exactly as it did before this field existed.
	DisplayName string `json:"display_name,omitempty"`
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
	// Accent is an optional accent for the session, recorded verbatim the way
	// Options are: the daemon has no palette and does not interpret it. Clients
	// read it as a colour name from the ANSI sixteen or as a hex literal, and an
	// empty or unreadable value means they pick the session's colour themselves.
	// It lives here rather than in a client-side file because it has to survive a
	// reattach and be the same for every client attached to this session.
	Accent string `json:"accent,omitempty"`
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
	// Daemon-owned: it is set when the session is created and never changes,
	// so a client sync that omits it must not clear it. False is what every
	// older client and every state file written before this reads back as.
	Global bool `json:"global,omitempty"`
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

	MasterRatio float64 `json:"master_ratio"`
	AutoTiling  bool    `json:"auto_tiling"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	// Input mode (window-management vs terminal) is deliberately absent: it is
	// per-viewer, not per-session. It used to live here, which meant one client
	// entering terminal mode flipped the input mode of every other client
	// attached to the same session. Clients own their own mode.
	//
	// BSP tiling state

	// WorkspaceNames maps a workspace number to its optional label. The number
	// stays the workspace's identity and its fallback label: everything that
	// addresses a workspace (the window's Workspace field, WorkspaceFocus,
	// WorkspaceTrees, the verbs, TUIOS_WORKSPACE) keeps using the number, and a
	// workspace with no entry here is unnamed and renders as its number, exactly
	// as every workspace did before this existed. Naming one is a daemon-owned
	// change so it survives a reattach and every attached client sees it.
	WorkspaceNames map[int]string `json:"workspace_names,omitempty"`
	// WorkspaceOrder is the order the workspaces are shown in, and only that.
	// The number stays the identity, so the window's Workspace field,
	// WorkspaceFocus, WorkspaceTrees, the verbs and TUIOS_WORKSPACE all keep
	// addressing by number and none of them moves when this does. It sits beside
	// the names because it is the same kind of thing: a presentation choice the
	// daemon owns so it survives a reattach and every attached client sees the
	// one arrangement. Absent means the plain ascending order, which is what
	// every session had before this existed.
	WorkspaceOrder  []int                      `json:"workspace_order,omitempty"`
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
	// LayoutTreeOps says whether the session's clients send their BSP trees as
	// ops (true) or inside their pushes, as before the op existed (false). The
	// daemon turns the ops off while a client too old for them is attached
	// and on again when it leaves, and it changes the flag as a mutation, so
	// every client switches at one Version. A daemon that predates the ops
	// never sets it; a client reads it only from a daemon whose welcome
	// offered them. Wire only.
	LayoutTreeOps bool `json:"-"`
	// ScratchWorkspaces says every attached client shows a scratch group
	// from its own workspace. While a client that predates it is attached it
	// is false, a new scratch terminal is a popup again, and no focus goes
	// to a pane on a scratch workspace. See Session.SetScratchWorkspaces. A
	// daemon that predates it never sets it. Wire only.
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

// paneIO is where a pane's bytes come from and go to.
//
// A pane on this machine gets the pty the daemon spawned, which is what this
// has always been. The type is narrowed to the four operations a PTY actually
// performs on it, out of the nine xpty.Pty carries, because the other five
// cannot be answered honestly by anything that is not a local file: a stream to
// another machine has no file descriptor, no name on this filesystem, and no
// command of its own to start.
//
// Those four are the entire seam between "a pane is a process here" and "a pane
// is a process somewhere else". Nothing else in PTY touches the handle: the
// emulator, the scrollback, the sequenced ring buffer and every subscriber path
// are already indifferent to where the bytes came from, which is why a pane on
// another machine can reuse all of it rather than needing a second
// implementation of it.
type paneIO interface {
	io.ReadWriteCloser
	Resize(width, height int) error
}

// PTY represents a daemon-managed pseudo-terminal.
type PTY struct {
	ID string
	// sessionID is the session the pane belongs to, which a nesting probe
	// seen in its output is recorded against. See nest_probe.go.
	sessionID string
	// probeTail is the end of the last output chunk, so a probe split across
	// two reads is still seen. Only readOutput touches it.
	probeTail []byte
	// host is the machine the process is on, empty for this one. It is what
	// tells the two halves of exit detection apart: a pane here has an
	// exec.Cmd to wait on, and a pane elsewhere has only its stream ending.
	host string
	pty  paneIO
	cmd  *exec.Cmd
	// rawLog appends every byte the process writes, when TUIOS_PTY_LOG asks
	// for it, and is nil otherwise. See pty_log.go.
	rawLog *ptyLogger
	ctx    context.Context
	cancel context.CancelFunc

	// Terminal emulator: maintains scrollback, screen state, cursor position
	// This persists across client disconnect/reconnect
	terminal vt.Terminal
	// terminalMu guards the daemon-side VT emulator (p.terminal) and the
	// p.width/p.height it is sized to. This is the daemon process; it is a
	// different lock from app.OS.terminalMu and the two never coexist.
	//
	//   LOCK ORDER (within the daemon):
	//       PTY.terminalMu  ->  (nothing)
	//
	//   It is a leaf lock. No holder may take p.outputMu, send on a channel,
	//   or touch p.pty while holding it. Resize deliberately drops it before
	//   calling p.pty.Resize, and vtWriter drops it between queue items, for
	//   exactly that reason: p.pty operations are syscalls that can block, and
	//   the subscriber/capture paths take the read side constantly.
	//
	//   NOT REENTRANT. Size, SetCellSize, Resize, GetTerminalState,
	//   CaptureContent and vtWriter must not call one another
	//   (UpdatePixelDimensions calls SetCellSize and Size in sequence, not
	//   nested, which is why it is written that way).
	terminalMu sync.RWMutex
	width      int
	height     int

	// Output ring buffer that drives resubscribe catch-up.
	// outputSeq is the total number of bytes this PTY has ever produced, so the
	// buffer holds the stream's last outputPos bytes ending at outputSeq. It is
	// what lets a resubscribing client be resumed where it left off instead of
	// being handed the buffer from the top.
	outputMu     sync.RWMutex
	outputBuffer []byte
	outputPos    int
	outputSeq    int64
	// resizeMarks are the stream positions resizes took effect at, oldest
	// first, kept while the ring still holds bytes either side of them plus
	// the newest one behind the ring, which is the width the ring's first
	// byte was laid out at.
	resizeMarks []resizeMark

	// Subscribers for raw output streaming.
	subscribers   map[string]*ptySubscriber
	subscribersMu sync.RWMutex
	// subscriberCount is len(subscribers), kept beside the map so the
	// emulator's callbacks can read it. They run under terminalMu, and
	// subscribeLocked takes terminalMu under subscribersMu, so taking
	// subscribersMu there would be the other order.
	subscriberCount atomic.Int32
	// graphicsAt is when the pane last wrote a kitty graphics command that
	// is not a query, in unix nanoseconds, zero if never. See
	// holdForSlowSubscribers.
	graphicsAt atomic.Int64
	// gfx cuts the output into text and frames for the subscribers. Used
	// by broadcast only, under streamMu. See kitty_frames.go.
	gfx gfxScanner
	// unbroadcast is how many bytes the ring holds that broadcast has not
	// cut yet: readOutput appends a read before it broadcasts it.
	unbroadcast atomic.Int64
	// reads counts the reads broadcast has cut. Used by broadcast only.
	reads uint64
	// framesSkipped counts frames dropped for a client that was behind.
	framesSkipped atomic.Int64
	// streamsCut counts the client streams gapped because their queue filled.
	streamsCut atomic.Int64
	// The pacing state. See graphics_backpressure.go.
	paceWake    chan struct{}
	holding     atomic.Bool
	holdSpent   bool // readOutput only
	holds       atomic.Int64
	holdsRanOut atomic.Int64
	heldNanos   atomic.Int64
	// readOutput only: when reportPacing last wrote, and what.
	pacingReportAt time.Time
	pacingReported pacingCounts

	// debug mirrors TUIOS_DEBUG_INTERNAL, read once when the PTY is built.
	// broadcast runs per chunk per subscriber, and a debugLog there costs an
	// os.Getenv (which takes the process-wide environment lock) plus a boxed
	// argument slice on every call, whether or not the flag is set. The env var
	// is set from the --debug flag before any PTY exists, so a value read here
	// is the value the run was started with.
	debug bool

	exited   bool
	exitedMu sync.RWMutex
	exitCode int

	// Single-goroutine VT writer channel. Closed by readOutput on exit so
	// vtWriter's range terminates.
	vtWriteChan chan vtChunk
	// focusReporting is whether the guest had focus reporting on after the
	// last write to the emulator. Only vtWriter reads and writes it. See
	// focus_report.go.
	focusReporting bool
	// hasFocus reports whether this pane has focus. Nil for a PTY made
	// outside a session. See Session.paneHasFocus.
	hasFocus func() bool

	// streamMu puts a resize at one position in the pane's stream. readOutput
	// holds it across appending a chunk, broadcasting it and queueing it for
	// the emulator, so a resize taken under it lands between the same two
	// bytes on the daemon's emulator and on every subscriber's. Without that
	// the two sides change width at different bytes and disagree for good
	// about where the line in between wrapped.
	streamMu sync.Mutex
	// vtClosed records that readOutput has closed vtWriteChan, so a resize
	// arriving during teardown does not send on a closed channel. Guarded by
	// streamMu, which readOutput also holds to close.
	vtClosed bool

	// winsizeMu serializes writes of the real PTY's window size, and guards
	// the cell size in pixels they are computed from. Resize takes it under
	// streamMu; UpdatePixelDimensions takes it alone. It is held across the
	// ioctl, so it must not be taken under terminalMu.
	//
	// The cell size is kept so a resize carries the pixel size in the same
	// ioctl as the cell size. Two ioctls, one without pixels and one with,
	// gave the guest two SIGWINCHes for one resize. Zero means no client has
	// reported a cell size yet, and the pixel size stays zero.
	winsizeMu  sync.Mutex
	cellWidth  int
	cellHeight int

	// The window size writes are coalesced; see pty_winsize.go. spawnedAt is
	// set once at creation. The rest is guarded by winsizeMu, except
	// winsizeHeld, which is also read without it on the input path.
	spawnedAt             time.Time
	winsizeWritten        time.Time
	winsizeHeld           atomic.Bool
	heldWidth, heldHeight int
	winsizeTimer          *time.Timer
	winsizeClosed         bool

	// vtSeq is the stream position the emulator has consumed, guarded by
	// terminalMu. It trails outputSeq by whatever is still queued.
	vtSeq int64
	// vtResizes counts the resizes the emulator has applied, guarded by
	// terminalMu. With vtSeq it makes the pane's revision (see paneMeta).
	vtResizes int64

	// Callback when PTY process exits, used by daemon to notify clients
	onExit func(ptyID string)

	// emit, when set, raises a control-plane event (output activity, bell, mode
	// change, process exit) already tagged with this PTY's window and PTY ID. It
	// is a no-op when the session has no event sink installed.
	emit func(SessionEvent)

	// lastOutput is the unix-nano time this PTY most recently produced output. It
	// is written on the PTY read goroutine and read by the daemon's output-stall
	// heuristic, so it is an atomic rather than guarded by a lock.
	lastOutput atomic.Int64

	// lastAgentProbe is the unix-nano time of the last output-driven agent-exit
	// probe. It throttles the /proc read the probe does so a busy pane does not
	// re-check its foreground on every output chunk.
	lastAgentProbe atomic.Int64

	// lastScreenScan is the unix-nano time of the last output-driven screen scan,
	// throttling it the same way.
	lastScreenScan atomic.Int64

	// lastDetectScan is the unix-nano time the detection poll last read this
	// pane's foreground process, and detectSkips how many ticks have passed
	// over it since. Only the poll's goroutine touches them. See detectScanDue.
	lastDetectScan atomic.Int64
	detectSkips    atomic.Int32

	// screenSettle is the one-shot that scans the screen after a pane goes quiet.
	// It is a timer rather than a ticker so a silent pane costs nothing, which is
	// the rule the whole daemon is built to.
	//
	// The timer is built once and re-armed with Reset. Arming happens on every
	// chunk a pane emits, and a fresh time.AfterFunc there allocated a runtime
	// timer per chunk. screenLook is what it runs, held separately so the caller
	// does not have to build a closure per chunk either.
	screenSettleMu sync.Mutex
	screenSettle   *time.Timer
	screenLook     func()

	// agentProgress parks the most recent OSC 9;4 progress state the emulator
	// saw, as the state plus one so zero means none pending. The VT callback runs
	// on the vtWriter goroutine with the terminal lock held, where mutating
	// session state would re-enter that lock, so it only stores here and the PTY
	// read goroutine applies it on the output event that carried the sequence.
	agentProgress atomic.Int64
	// lastProgress is the most recent OSC 9;4 report, kept after it is
	// applied so a manifest's osc_progress rules can read it on any look. It
	// packs the state plus one into the high half and the percentage into the
	// low half, so zero means the pane has sent none.
	lastProgress atomic.Int64
	// agentNotify parks the most recent desktop notification (OSC 9, 777 or
	// 99) for the read goroutine, for the reason agentProgress does. See
	// agent_notify.go.
	agentNotify atomic.Pointer[paneNotification]

	// title is the last title this PTY's application set. The daemon reads every
	// byte of every window, so this is the freshest title anyone holds: a client
	// only sees the windows it is subscribed to, and its copy of the title stops
	// where its subscription did.
	title atomic.Pointer[string]
	// place is where the shell is: its directory from OSC 7 (seeded from the
	// spawn directory) and the git branch there. See session_place.go.
	place placeRecord
	// cwdCheck paces the read of where the shell is that output drives. See
	// pane_cwd_check.go.
	cwdCheck paneCwdCheck
	// shell follows the shell's commands through its OSC 133 marks. See
	// shell_commands.go.
	shell shellTrack
	// runClaim is held by one run call from its prompt check until the call
	// ends, so two runs cannot both pass the check and type into one line.
	// See verb_run.go.
	runClaim atomic.Bool
}

// Title returns the last title this PTY's application set, or "" if it has set
// none.
func (p *PTY) Title() string {
	if t := p.title.Load(); t != nil {
		return *t
	}
	return ""
}

// agentExitProbeInterval bounds how often output drives an agent-exit probe, so a
// pane streaming heavy output probes /proc at most a few times a second while a
// quit agent still clears well inside one detection poll.
const agentExitProbeInterval = 250 * time.Millisecond

// LastOutput returns the unix-nano time this PTY most recently produced output,
// or 0 if it has produced none. It backs the daemon's agent-state stall
// heuristic, which demotes a pane that reported working but has gone quiet.
func (p *PTY) LastOutput() int64 {
	return p.lastOutput.Load()
}

// probeAgentExitDue reports whether enough time has passed since the last
// output-driven agent-exit probe to run another, and claims the slot if so. It is
// only ever called from the single PTY read goroutine, so a plain load/store is
// race-free.
func (p *PTY) probeAgentExitDue(now int64) bool {
	if now-p.lastAgentProbe.Load() < int64(agentExitProbeInterval) {
		return false
	}
	p.lastAgentProbe.Store(now)
	return true
}

// storeAgentProgress parks an OSC 9;4 progress state for the read goroutine to
// apply. Called from the VT callback under the terminal lock, so it must stay a
// single atomic store and nothing more.
func (p *PTY) storeAgentProgress(state vt.ProgressState, percent int) {
	p.agentProgress.Store(int64(state) + 1)
	p.lastProgress.Store((int64(state)+1)<<32 | int64(uint32(int32(percent))))
}

// takeAgentProgress returns the parked OSC 9;4 progress state and clears it,
// reporting whether one was pending. A burst that parked several states between
// two output events collapses to the newest, which is the only one still true.
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
	// Identity
	ID string
	// name is the session's one name: what the daemon lists, addresses it by,
	// saves its state file under and exports as TUIOS_SESSION to new panes. It
	// changes only through Manager.RenameSession, and it is read from many
	// goroutines, so it is an atomic pointer rather than a plain string.
	name atomic.Pointer[string]
	// persistMu serializes writes of the state file with a rename, which moves
	// the file. See persist.
	persistMu sync.Mutex

	// PTYs managed by this session
	ptys   map[string]*PTY
	ptysMu sync.RWMutex
	// reportBg and reportFg are the colours every emulator in the session
	// answers OSC 11 and OSC 10 with, from the last client push, and the
	// strings they were read from. Guarded by ptysMu, so a pane created while
	// they change cannot miss them. See applyReportColors.
	reportBgHex, reportFgHex string
	reportBg, reportFg       color.Color
	// reportPalHex and reportPal are the host's sixteen, the same way, for
	// OSC 4.
	reportPalHex string
	reportPal    [16]color.Color

	// The last directory read out of each PTY's process, and when. See
	// liveCwds: GetState is on the render path and reading a process
	// directory is a syscall per window, so the read is throttled per
	// session rather than taken on every call.
	cwdCache   map[string]string
	cwdReadAt  time.Time
	cwdCacheMu sync.Mutex
	// placePushQueued coalesces the pushes publishPlaceMove starts.
	placePushQueued atomic.Bool

	// Session state (serializable)
	state            *SessionState
	stopResurrection func() // Stops periodic resurrection saving
	// history records when each pane's history was last saved. See
	// scrollback_persist.go.
	history historySaver
	// discardHistory is set when the session is killed. The history would be
	// deleted with the state straight after, so the last save skips it.
	discardHistory atomic.Bool

	stateMu sync.RWMutex
	// snapSeq is the last SnapshotSeq handed out.
	snapSeq atomic.Uint64
	// pushSeen is what every snapshot's PushSeen is copied from, guarded by
	// stateMu. It is kept beside the state rather than in it so that nothing
	// that replaces the state wholesale (a restore, a verb) can lose it: a
	// client whose entry went missing would take every broadcast for one that
	// predates its own push until it next pushed.
	pushSeen map[string]uint64
	// focusMovedVersion is the Version of the last daemon mutation that moved
	// the focus or the workspace, or that a focus verb made, guarded by
	// stateMu. A stale push built at or after it keeps its own focus. See
	// keepClientFocus.
	focusMovedVersion int
	// clientFocusMoved is, by push origin, the Version at which that client's
	// last push moved the focus, guarded by stateMu. A push never advances
	// Version, so a move by one client is invisible to focusMovedVersion, and a
	// stale push from another client built at that version may predate it.
	clientFocusMoved map[string]int
	// treeOps holds the recent Versions that were tree ops, with the client
	// connection that sent each, at its version modulo the length, guarded by
	// stateMu. A fixed ring, so it never grows. See missedMutationLocked and
	// missedPeerTreeLocked.
	treeOps [1024]treeOpRecord
	// treeOpsOff is set while a client too old for tree ops is attached.
	// Guarded by stateMu. See SetLayoutTreeOps.
	treeOpsOff bool
	// scratchWSOff is set while a client too old for scratch workspaces is
	// attached. Guarded by stateMu. See SetScratchWorkspaces.
	scratchWSOff bool
	// treeOpsMu is held across working out whether the session's tree ops
	// should be on and applying the answer, so two refreshes cannot apply
	// their answers in the opposite order to the one they worked them out
	// in. See Daemon.refreshTreeOps.
	treeOpsMu sync.Mutex
	// focusIntent is set by a focus verb inside mutateState, so the mutation
	// counts as a focus move even when the focus it names is the one already
	// held: the verb is a later intent than any push in flight.
	focusIntent bool

	// stateDirty is set by every change to the session's structure and consumed
	// by the resurrection saver, which is how a new window reaches disk in a
	// couple of seconds instead of at the next blind tick. It is an atomic rather
	// than a field of state because the saver goroutine reads it and holds no
	// lock of this session.
	stateDirty atomic.Bool
	// stateWake is closed by noteStateChangeLocked to wake WaitState. Guarded
	// by stateWakeMu, which is never held with another lock taken after it.
	stateWake   chan struct{}
	stateWakeMu sync.Mutex

	// eventSink, when set, receives control-plane events raised by this session
	// and its PTYs (window lifecycle, output activity, bell, mode changes). The
	// daemon installs it so events reach the event hub; nil for a session with no
	// hub (e.g. bare unit tests).
	eventSink   func(SessionEvent)
	eventSinkMu sync.RWMutex

	// stateSink, when set, receives the canonical state after every daemon-side
	// mutation, so an attached client can be told what the daemon just did. The
	// daemon installs it and broadcasts the snapshot to the session's clients;
	// nil for a session with no clients (bare unit tests, resurrection loads).
	stateSink   func(*SessionState)
	stateSinkMu sync.RWMutex

	// pushMu serializes state-sink deliveries and pushedVersion records the
	// highest version already delivered. Snapshots are taken under stateMu but
	// delivered without it, so two concurrent mutations can reach the sink in
	// either order; this drops the loser rather than letting a client see the
	// daemon go backwards.
	pushMu        sync.Mutex
	pushedVersion int

	// broadcastFP is the fingerprint of the state last forwarded to this
	// session's peers on a client sync, and broadcastFPSet says whether there
	// is one. See NoteBroadcastFingerprint.
	broadcastFP    uint64
	broadcastFPSet bool

	// Terminal size, and the chrome reserve every client lays its panes out
	// around. Both are guarded by sizeMu.
	width   int
	height  int
	reserve LayoutReserve
	// layoutGen counts settlements of the two above. See
	// SessionResizePayload.Generation.
	layoutGen uint64
	// sizePolicy is the window_size policy the size above was settled under.
	sizePolicy string
	sizeMu     sync.RWMutex

	// Lifecycle
	Created time.Time

	// lastActive is when this session was last used, and activeMu guards it. A
	// mutex rather than a bare field because it is written from the connection
	// goroutine on every keystroke and read from whichever goroutine is
	// answering a session listing.
	lastActive time.Time
	activeMu   sync.Mutex

	// Configuration
	config *SessionConfig

	// agentClaims records, by window ID, who owns the window's agent state: the
	// ranked source that last set it (see AgentSource) and whether the
	// foreground-process detector promoted the window and so must clear it when
	// the agent exits.
	//
	// It used to be a bool holding only the second half. That was enough while the
	// detector was the only thing competing with an explicit report; it cannot
	// express which of several sources should win, so the value carries the source
	// now. Read and written under stateMu, so it needs no lock of its own.
	agentClaims map[string]agentClaim

	// agentHarnessPIDs records, by window ID, the pid of the harness process
	// whose hook last set the window's AgentSessionID, as the hook reported it.
	// sessionGuard reads it to tell a new conversation in the same harness
	// process (/clear or /resume after an interrupted turn) from a nested run
	// in another process, and applyAgentSession reads it for the same reason.
	// It is kept apart from agentClaims because other sources replace the
	// claim, and the pid must outlive that. The detector forgets it when it sees
	// the agent leave the pane. Daemon memory only, and read and written under
	// stateMu.
	agentHarnessPIDs map[string]int

	// agentSubagents records, by window ID, the subagents the window's agent
	// is running, as its hooks reported them: subagent id to its type and
	// when it was last reported. The window's AgentSubagents and the reserved
	// metadata key subagents count them, and all three move in one mutation.
	// See agent_subagents.go. Daemon memory only, and read and written under
	// stateMu.
	agentSubagents map[string]map[string]subagent

	// transcripts binds windows to the record files their harnesses write. It is
	// held here rather than in SessionState because none of it is state: a
	// transcript path names a project directory and a session, so it is kept in
	// daemon memory and never serialised, never versioned, and never pushed to a
	// client. Only the AgentState derived from it is.
	transcripts agentTranscriptState

	// agentHolds records, by window ID, a quieter agent state waiting out the
	// anti-flicker window before it is published (see holdQuieterState). It has a
	// lock of its own rather than riding stateMu because it is read and written
	// around ApplyAgentReport, which takes stateMu itself.
	agentHolds map[string]agentHold
	// agentHoldTimer is the one-shot that publishes a hold whose source then
	// went silent. Nil when nothing is waiting. Guarded by agentHoldMu.
	agentHoldTimer *time.Timer
	agentHoldMu    sync.Mutex

	// idle holds idle readings from the title and screen tiers until they are
	// confirmed (see agent_idle.go). It has its own lock.
	idle idleGate

	// agentTurns records, by window ID, when the window's current working
	// phase began, so a return to rest can be counted as a finished turn (see
	// agent_turns.go). completionSeen is the CompletionSeq each window had when
	// an attached client last pushed state with it focused. Both are guarded by
	// stateMu and neither is serialised.
	agentTurns     map[string]agentTurn
	completionSeen map[string]uint64

	// agentMetaPrune drops expired agent metadata. Idle when no token has a
	// TTL. See agent_meta.go.
	agentMetaPrune pruneTimer

	// subagentPrune drops subagents gone quiet. Idle while no pane has
	// subagents. See agent_subagents.go.
	subagentPrune pruneTimer

	// Graphics capabilities of the attached client's host terminal. The daemon
	// records them on attach so shells spawned afterwards can advertise a
	// terminal identity the guest's image tools recognise.
	graphicsMu    sync.RWMutex
	kittyGraphics bool
	sixelGraphics bool
	// linkedViewer says a client attached over a link, from another machine,
	// is drawing this session. Read from the VT callback, so it is atomic
	// rather than behind a lock that callback could wait on. See
	// kittyQueryResponse.
	linkedViewer atomic.Bool
	// kittyAnimation says the attached client's host edits image frames. Read
	// from the VT callback, so it is atomic. See SetKittyAnimation.
	kittyAnimation atomic.Bool
	// sixelAdvertised says the session's panes are told they can draw
	// sixel. Read from the VT's DA1 handler, so it is atomic. See
	// SetSixelAdvertised.
	sixelAdvertised atomic.Bool
	// kittyAdvertised says a kitty image a pane draws will be shown. Read
	// from the VT callback, so it is atomic. See SetKittyAdvertised.
	kittyAdvertised atomic.Bool
	// fed is the link manager a window on another machine is opened over. It is
	// nil unless the daemon installed one, and every reader checks. See
	// remote_pane.go.
	fed paneFederation
	// onRemotePane is told of every window this session opens on another
	// machine, so the daemon can hold the pane's report channel. Guarded by
	// ptysMu, like fed. See hosted_calls.go.
	onRemotePane func(windowID string, p *remotePane)
	// hostShown reports whether the session is on a screen. See
	// SetHostShownProbe.
	hostShown atomic.Pointer[func() bool]
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
}

// SetKittyAnimation records whether the hosts of the session's attached
// clients make kitty frame edits: true only while every one of them does.
// Daemon.refreshTreeOps counts it on every attach, detach and disconnect.
//
// A frame edit the host cannot make is refused by the daemon, from the pane's
// own emulator, so the refusal reaches the guest in the order the guest asked
// its questions. A client used to refuse it, a round trip later: after the
// daemon had answered DA1, which is where a probing guest stops reading. A
// nested tuios then took the refusal for keys and typed it into its pane.
func (s *Session) SetKittyAnimation(ok bool) { s.kittyAnimation.Store(ok) }

// SetSixelAdvertised records whether a sixel image a pane draws will be shown:
// true while any attached client's terminal draws sixel or kitty graphics,
// since a client with kitty and no sixel is sent the picture as a kitty image.
// Daemon.refreshTreeOps counts it with SetKittyAnimation.
//
// It is what the pane's DA1 answer lists. The daemon's emulator is the one that
// answers a guest's queries, so this is where a program such as chafa, lsix or
// yazi learns whether to draw sixel or fall back to text. A client with neither
// protocol is shown a placeholder box where the image is.
func (s *Session) SetSixelAdvertised(ok bool) { s.sixelAdvertised.Store(ok) }

// SetKittyAdvertised records whether a kitty image a pane draws will be
// shown: true while any attached client's terminal draws kitty graphics.
// Daemon.refreshTreeOps counts it with SetSixelAdvertised, by the same rule.
//
// It decides whether the daemon answers a guest's kitty graphics query (see
// kittyQueryResponse). A client whose terminal has no kitty graphics drops
// the image, so a guest told OK draws nothing where it would have drawn a
// text fallback.
func (s *Session) SetKittyAdvertised(ok bool) { s.kittyAdvertised.Store(ok) }

// SixelAdvertised reports what SetSixelAdvertised last recorded.
func (s *Session) SixelAdvertised() bool { return s.sixelAdvertised.Load() }

// GraphicsCapabilities returns the recorded kitty and sixel support.
func (s *Session) GraphicsCapabilities() (kitty, sixel bool) {
	s.graphicsMu.RLock()
	defer s.graphicsMu.RUnlock()
	return s.kittyGraphics, s.sixelGraphics
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
	// HerdrEnv returns the herdr protocol variables a pane with the given
	// window id that runs the given command is started with, nil for none.
	// See Manager.HerdrEnv.
	HerdrEnv func(sessionID, windowID string, workspace int, command []string) []string
	// PaneToken returns the token a pane with the given window id is started
	// with, exported as TUIOS_PANE_TOKEN. The manager stamps it with its own.
	// Nil, or an empty answer, leaves the variable unset. See pane_token.go.
	PaneToken func(windowID string) string
	// grants is the manager's pane grant table. Every local pane is entered
	// in it before its process starts, and TUIOS_PANE_GRANTS is read from
	// it. Nil for a session made outside a manager, whose panes hold the
	// default. See pane_grants.go.
	grants *paneGrantTable
	// history is what the session does with its panes' history across a
	// restart. The manager stamps it from the daemon's config. Nil saves
	// nothing.
	history *HistoryPolicy
	// Global creates the session as a global one. See SessionState.Global.
	Global bool
	// restoreID is the id a restored session had before the daemon
	// restarted, from its state file. A session made with it keeps that id,
	// so a client's ids for it stay valid. Empty, or an id already in use,
	// mints a new one.
	restoreID string
}

// historyPolicy is the session's HistoryPolicy, off when none was stamped.
func (s *Session) historyPolicy() HistoryPolicy {
	if s.config == nil || s.config.history == nil {
		return HistoryPolicy{}
	}
	return *s.config.history
}

// inheritedCwd is the directory a new window should start in when the caller
// named none, or "" when there is nothing to inherit.
//
// The focused pane's live shell is asked rather than the Cwd on the window
// record: that field is filled when resurrection state is saved, so it says
// where the pane was the last time the daemon wrote state, not where the user
// has since cd'd to. Reading the process is what makes a window opened from a
// project land in the project. The record is the fallback for a pane whose
// process cannot be read, which is every pane on a platform with no procfs
// equivalent, and "" is the fallback after that, meaning the daemon's own
// directory exactly as before.
func (s *Session) inheritedCwd() string {
	if s.config == nil || !s.config.InheritCwd {
		return ""
	}
	state := s.GetState()
	win, ok := findWindowState(state, state.FocusedWindowID)
	if !ok {
		return ""
	}
	if pty := s.GetPTY(win.PTYID); pty != nil {
		if cwd, ok := pty.ProcessCwd(); ok && cwd != "" {
			return cwd
		}
	}
	return win.Cwd
}

// scrollbackLines is the history depth a new pane in this session keeps.
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
			// Versions start at 1 so that a BaseVersion of 0 on an incoming sync
			// means one thing only: a client that predates state versioning and
			// cannot say what it saw. A versioned client always echoes back at
			// least 1, even before the daemon has mutated anything.
			Version: 1,
			Global:  cfg != nil && cfg.Global,
		},
		width:      width,
		height:     height,
		Created:    now,
		lastActive: now,
		config:     cfg,
	}

	session.setName(name)

	// Start periodic resurrection saving
	session.stopResurrection = startPeriodicSaveWith(
		func() *SessionState { return session.ResurrectionState() },
		func() bool { return session.stateDirty.Swap(false) },
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

// setName records the session's name. Only the constructor and
// Manager.RenameSession call it.
func (s *Session) setName(name string) {
	s.name.Store(&name)
}

// persist writes a state snapshot to the session's state file. It holds
// persistMu, which a rename also holds while it moves the file, and it drops a
// snapshot taken under a name the session no longer has: written after the
// rename, it would bring the old name back as a second session on the next
// daemon start.
func (s *Session) persist(state *SessionState) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if state == nil || state.Name != s.Name() {
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

// emit forwards a control-plane event to the installed sink, if any.
func (s *Session) emit(ev SessionEvent) {
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

// publishState hands a post-mutation snapshot to the state sink, in version
// order and never twice for the same version. It must be called without stateMu
// held: the sink writes to client sockets, and a slow client must be able to
// delay other pushes without also blocking every mutation of the session.
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

	// The same live facts a verb would read. Without this the clients were the
	// one audience told less than everybody else: the snapshot they were
	// pushed carried only what had been stored, so a pane whose shell never
	// announces its directory appeared to have none, and the rail's file
	// section had nothing to ask about.
	s.fillLiveFacts(snap)

	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if snap.Version <= s.pushedVersion {
		return
	}
	s.pushedVersion = snap.Version
	// A daemon-side push reaches the clients by a different road than a client
	// sync does, so what the peers hold afterwards is not what the sync
	// suppressor last recorded. Forget the record rather than try to keep it in
	// step: the cost of being wrong here is one state sync too many, and the
	// cost of being wrong the other way is a peer that never hears about a
	// change.
	s.forgetBroadcastFingerprint()
	fn(snap)
}

// NoteBroadcastFingerprint records fp as the state about to be forwarded to
// this session's peers, and reports whether that forward is worth making.
//
// It answers false only when fp is exactly what was forwarded last time, which
// means every peer already holds this state and the message would tell them
// nothing. See the call site in handleUpdateState for why a suppressed sync
// costs a peer nothing.
func (s *Session) NoteBroadcastFingerprint(fp uint64) bool {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if s.broadcastFPSet && s.broadcastFP == fp {
		return false
	}
	s.broadcastFP = fp
	s.broadcastFPSet = true
	return true
}

// forgetBroadcastFingerprint drops the record, so the next client sync is
// forwarded whatever it says. pushMu must already be held: publishState holds
// it for the whole delivery. forgetBroadcast is the same for a caller that
// does not hold it.
func (s *Session) forgetBroadcastFingerprint() {
	s.broadcastFPSet = false
}

// forgetBroadcast drops the record for a client joining the session. The
// record says what every peer holds, and a joining client holds its attach
// snapshot instead. See handleAttach.
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

// RestorePTY creates a fresh PTY for a resurrected window. It behaves like
// CreatePTY but starts the shell in cwd (when that directory still exists) and
// marks the shell as restored: the shell's environment carries TUIOS_RESTORED=1
// and a one-line banner is written to the terminal so the user can see the
// process is a freshly respawned shell, not the original long-lived one.
func (s *Session) RestorePTY(windowID string, width, height int, cwd string, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(width, height, ptySpawn{windowID: windowID, cwd: cwd, restored: &restoreSpec{}, onExit: onExit})
}

// restorePTYWithGrants is RestorePTY for a window that was saved with grants
// of its own, which the new process holds from its first instruction.
// history, when not nil, is the pane's saved history, which the new emulator
// shows above the banner.
func (s *Session) restorePTYWithGrants(windowID string, width, height int, cwd string, grants *Grants, history *savedHistory, workspace int, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(width, height, ptySpawn{windowID: windowID, cwd: cwd, restored: &restoreSpec{history: history}, onExit: onExit, grants: grants, workspace: workspace})
}

// ptySpawn is what createPTY starts a pane with, beside its size.
type ptySpawn struct {
	// windowID is the client-side window id, exported as TUIOS_WINDOW_ID.
	windowID string
	cwd      string
	command  []string
	// env is the caller's KEY=VALUE pairs. See createPTY.
	env        []string
	host       string
	restored   *restoreSpec
	onExit     func(ptyID string)
	stdout     *os.File
	extraFiles []*os.File
	grants     *Grants
	// workspace is the workspace the window is being made on, for the
	// shell's HERDR_TAB_ID. 0 takes it from the state. See herdrTabFor.
	workspace int
}

// createPTY starts a pane's process. sp.command, when non-empty, is an argv
// exec'd as the PTY's process in place of
// the shell. It is deliberately not persisted: a restored window respawns as a
// shell, because silently rerunning a program the user ran once is not what
// restoration promises. sp.env, KEY=VALUE pairs, goes on top of the daemon's
// environment and under the TUIOS_ variables; see buildEnvWith. It is not
// persisted either, and a window on another machine ignores it.
//
// stdout, when non-nil, is the process's standard output instead of the PTY:
// a popup's captured output goes to a pipe the daemon reads, while the program
// still draws on the PTY through stderr or /dev/tty. It is only honoured for a
// local process. extraFiles are inherited as fd 3 and up, again only by a
// local process.
//
// grants is what the pane may do through tuios, nil for the default. A local
// pane is entered in the grant table before its process starts and leaves it
// when the process exits, so the process is never placed in a pane the table
// does not know. See pane_grants.go.
func (s *Session) createPTY(width, height int, sp ptySpawn) (*PTY, error) {
	// A window that was given grants and gets a new process with none named
	// keeps what it was given, even when its last process has already gone
	// and taken its entry in the grant table with it. Read before ptysMu is
	// taken, so the two locks are never held together here.
	if sp.grants == nil && sp.host == "" && sp.windowID != "" {
		sp.grants = s.recordedGrants(sp.windowID)
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
		// A spawn that fails leaves no process to exit.
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

	// The PTY and the process are created together, and retried together, by
	// ptyspawn: a spawn can be refused with a transient EPERM from the kernel's
	// controlling-terminal grab, and that refusal belongs to every spawn path
	// rather than to this one. See the ptyspawn package comment.
	//
	// The command is rebuilt per attempt, because an exec.Cmd that failed to
	// start cannot be started again.
	// A window on another machine takes the same path from here down. Only the
	// two lines that produce the handle differ: the process is started over a
	// link instead of here, so there is no exec.Cmd to wait on and no local pid
	// to read. Everything below is the code a local pane runs, which is the
	// point of the paneIO seam.
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
		ptyInstance, cmd, err = ptyspawn.SpawnTTY(width, height, func(tty string) *exec.Cmd {
			var cmd *exec.Cmd
			if len(sp.command) > 0 {
				cmd = exec.Command(sp.command[0], sp.command[1:]...)
			} else {
				cmd = exec.Command(shell)
			}
			cmd.Env = s.buildEnvFor(sp.windowID, sp.workspace, sp.restored != nil, sp.env, sp.command)
			// The pane's terminal, so a tuios client can tell whether it runs
			// on it or only inherited the pane's variables. See
			// nested_attach.go.
			if tty != "" {
				cmd.Env = append(cmd.Env, PaneTTYEnv+"="+tty)
			}
			if sp.stdout != nil {
				cmd.Stdout = sp.stdout
			}
			if len(sp.extraFiles) > 0 && runtime.GOOS != "windows" {
				cmd.ExtraFiles = sp.extraFiles
			}
			// Start the shell in cwd when one was named and still exists; otherwise
			// fall back to the shell's default (inherited) directory.
			//
			// The restored flag used to gate this too, which meant a caller that
			// asked a fresh window for a directory was answered with a shell
			// somewhere else and no indication of it. Restoration and placement are
			// separate questions: the flag still decides the banner, because that
			// is what it is about.
			if sp.cwd != "" {
				if info, statErr := os.Stat(sp.cwd); statErr == nil && info.IsDir() {
					cmd.Dir = sp.cwd
				}
			}
			return cmd
		}, debugLog)
		if err != nil {
			cancel()
			return nil, err
		}
	}

	// Create VT emulator for persistent terminal state
	// This maintains scrollback, screen content, cursor position across reconnects
	//
	// For a restored shell, seed the emulator with a one-line banner so the
	// respawned process is clearly marked, under the pane's saved history when
	// there is one (see scrollback_persist.go). This is written directly
	// (before the reader/writer goroutines start) so it lands ahead of the
	// shell's first prompt; it only touches the daemon-side emulator and never
	// the real PTY, so the shell is unaffected.
	var terminal vt.Terminal
	if sp.restored != nil {
		terminal = newRestoredEmulator(width, height, s.scrollbackLines(), sp.cwd, sp.restored.history)
	} else {
		terminal = vt.NewWithScrollback(width, height, s.scrollbackLines())
	}

	pty := &PTY{
		ID:           id,
		sessionID:    s.ID,
		host:         sp.host,
		pty:          ptyInstance,
		cmd:          cmd,
		ctx:          ctx,
		cancel:       cancel,
		terminal:     terminal,
		width:        width,
		height:       height,
		outputBuffer: make([]byte, 64*1024), // 64KB ring buffer
		subscribers:  make(map[string]*ptySubscriber),
		paceWake:     make(chan struct{}, 1),
		vtWriteChan:  make(chan vtChunk, 256),
		onExit:       sp.onExit,
		debug:        debugEnabled(),
		rawLog:       newPTYLogger(id),
		spawnedAt:    time.Now(),
	}

	// Per-PTY control-plane event emitter, pre-tagged with this window and PTY
	// ID. It routes through the session's event sink so events reach the daemon's
	// event hub; when no sink is installed it is a cheap no-op.
	pty.hasFocus = func() bool { return s.paneHasFocus(sp.windowID) }
	pty.emit = func(ev SessionEvent) {
		ev.Window = sp.windowID
		ev.PTYID = id
		s.emit(ev)
	}

	// Raise control-plane events from the daemon-side VT emulator: bell, an
	// app-driven title change, and alt-screen mode toggles. These fire from the
	// single vtWriter goroutine; the emitter only does a non-blocking hub publish,
	// so it never re-enters the terminal lock held during Write.
	terminal.SetCallbacks(vt.Callbacks{
		Bell: func() { pty.emit(SessionEvent{Type: EventBell}) },
		Title: func(title string) {
			pty.title.Store(&title)
			pty.emit(SessionEvent{Type: EventWindowRetitled, Title: title})
		},
		AltScreen: func(on bool) {
			pty.emit(SessionEvent{Type: EventModeChanged, Mode: "alt-screen", Enabled: on})
		},
		// Recorded, not applied: this fires with the terminal lock held, and
		// the record is two atomics plus one branch read on its own goroutine.
		//
		// A pane on another machine keeps the plain record, for the session's
		// label. Its shell reports a folder on that machine, which this side
		// would read as a shell that went somewhere else; the machine running
		// it says where it is instead (remotePane.Cwd).
		WorkingDirectory: func(raw string) {
			if _, remote := pty.pty.(*remotePane); remote {
				pty.place.setCwd(raw)
				return
			}
			if pty.place.announce(raw) {
				s.publishPlaceMove()
			}
		},
		// Parked rather than applied: this fires with the terminal lock held, and
		// applying it mutates session state. The read goroutine picks it up on the
		// output event carrying these same bytes.
		Progress: func(state vt.ProgressState, percent int) {
			pty.storeAgentProgress(state, percent)
		},
		// A desktop notification: published at once, like the bell, and parked
		// for the read goroutine to match against the harness's rules, like the
		// progress report.
		Notify: func(title, body string) {
			title, body = capNotifyText(title), capNotifyText(body)
			pty.storeAgentNotify(title, body)
			pty.emit(SessionEvent{Type: EventNotification, Title: title, Body: body})
		},
		// A shell's OSC 133 marks: recorded under the track's own leaf lock
		// and published at once, like the bell. See shell_commands.go.
		SemanticMark: pty.noteShellMark,
	})

	// DA1 and XTSMGRAPHICS answer from what the attached clients can show.
	terminal.SetSixelAdvertised(s.SixelAdvertised)

	// Handle kitty graphics queries on the daemon side for low-latency
	// responses. All other commands flow through the raw PTY broadcast.
	// The daemon reads only the control keys, so it does not decode the
	// payload of a frame that only the clients draw.
	remotePane := sp.host != ""
	terminal.SetKittyHeaderOnly(true)
	terminal.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		if cmd.Action == vt.KittyActionQuery {
			if response := s.kittyQueryResponse(cmd, remotePane); response != nil {
				terminal.WriteResponse(response)
			}
			return
		}
		if response := kittyAnimationRefusal(cmd, remotePane, s.kittyAnimation.Load()); response != nil {
			terminal.WriteResponse(response)
		}
		// With no client attached to this pane, nothing will read the object
		// a shared memory frame names. See kitty_medium.go.
		if !remotePane && pty.subscriberCount.Load() == 0 {
			releaseUnreadKittyMedium(cmd)
		}
	})

	// The shell's first directory is known before it prints a prompt, so the
	// pane has a place from the start even under a shell that never reports
	// OSC 7, which bash and zsh mostly do not.
	//
	// A pane on another machine is seeded with nothing, and that is the honest
	// answer rather than a gap. Both seeds here are paths on this machine: the
	// command's directory is one this daemon chose, and the daemon's own
	// working directory is plainly local. Neither describes where a shell on
	// another machine started, and the far side is free to ignore the
	// directory that was asked for when it does not exist there. So the pane
	// has no place until its own shell announces one over OSC 7, which is the
	// only report that actually comes from the machine the process is on.
	if cmd != nil {
		if seed := cmd.Dir; seed != "" {
			pty.place.setCwd(seed)
		} else if wd, err := os.Getwd(); err == nil {
			pty.place.setCwd(wd)
		}
	}

	// Before the goroutines start, so the emulator needs no lock yet.
	if s.reportBg != nil || s.reportFg != nil {
		terminal.SetReportColors(s.reportFg, s.reportBg)
	}
	if s.reportPalHex != "" {
		terminal.SetReportPalette(s.reportPal)
	}
	s.ptys[id] = pty

	// Start VT writer goroutine (single, persistent)
	go pty.vtWriter()

	// Start output reader
	go pty.readOutput()

	// Start terminal response forwarder. The daemon's emulator generates query responses
	// (DA, CPR, etc.) which must be sent to the PTY for applications to receive.
	// Client emulators DRAIN their responses to prevent duplicates.
	go pty.forwardTerminalResponses()

	// Monitor process exit
	go pty.monitorExit()

	s.TouchActive()
	return pty, nil
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
}

// LastActive is when the session was last used.
func (s *Session) LastActive() time.Time {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return s.lastActive
}

// GetPTY returns a PTY by ID.
func (s *Session) GetPTY(id string) *PTY {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	return s.ptys[id]
}

// ClosePTY closes and removes a PTY.
func (s *Session) ClosePTY(id string) error {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	pty, exists := s.ptys[id]
	if !exists {
		return fmt.Errorf("PTY %s not found", id)
	}

	delete(s.ptys, id)
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

// hasLivePTY reports whether a PTY with this ID is still open on the session.
// A window whose PTY is gone has been closed; see reconcileStale.
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

// fillLiveFacts writes what the emulators and the processes know into a
// snapshot, over the top of what was stored.
//
// It is applied to every snapshot that leaves the session, both the ones a
// verb reads and the ones pushed to clients, and that is the point. It used to
// run in GetState alone, so `tuios list-windows` reported a pane's directory
// and the client drawing that same pane was never told it. Local panes hid it:
// a shell that announces over OSC 7 reaches the client through its own
// emulator, so only a pane whose shell says nothing went without, and the
// panes that say nothing are exactly the ones on another machine.
//
// It writes into the copy. The stored state keeps holding only what was
// actually announced, which is what resurrection saves.
func (s *Session) fillLiveFacts(state *SessionState) {
	if state == nil {
		return
	}
	// Retitle from the live emulators. The stored title is only as fresh as the
	// last sync from a client, which knows the title of the windows it is
	// subscribed to and nothing about the rest, so a reattach or a resurrection
	// snapshot built from the stored value alone brings back a title the window
	// stopped having. Taken outside stateMu: the PTY map is a different lock.
	live := s.livePaneFacts()
	for i := range state.Windows {
		if t := live[state.Windows[i].PTYID].title; t != "" {
			state.Windows[i].Title = ClampDisplayText(t)
		}
	}

	// And fill the directory the same way, for the same reason. A shell that
	// announces one wins, because it is the shell's own answer and it stays
	// right when the pane is running something that changed directory without
	// the process doing so. The read of the process is what a pane whose shell
	// never announced gets, and it is the only thing a client on another
	// machine can be given. Both are live and both win over the stored value,
	// which is only where the window was created: a client takes the
	// directory from here when the pane is not on its machine, so a stored
	// value that won would keep that client on the first folder for good. This
	// writes into the copy, so the stored state keeps what it had.
	cwds := s.liveCwds()
	for i := range state.Windows {
		w := &state.Windows[i]
		place := live[w.PTYID].place
		w.CwdHost = place.elsewhere
		switch {
		case place.cwd != "":
			w.Cwd = place.cwd
		case cwds[w.PTYID] != "":
			w.Cwd = cwds[w.PTYID]
		case w.Cwd == "" && place.seed != "":
			// The process read is cached, and a snapshot taken just after
			// the spawn can come from a read made before it. The folder
			// the shell was started in is the answer until then.
			w.Cwd = place.seed
		}
	}

	// A window on another machine whose link is lost says so, and until
	// when the far machine keeps its process. Nothing stored holds this: it
	// is true only while it is true, so whatever a client pushed back is
	// cleared first.
	for i := range state.Windows {
		state.Windows[i].HostLink, state.Windows[i].HostLinkUntil = "", 0
	}
	for id, link := range s.liveHostLinks() {
		for i := range state.Windows {
			if state.Windows[i].PTYID == id {
				state.Windows[i].HostLink = link.state
				state.Windows[i].HostLinkUntil = link.until
			}
		}
	}
}

// hostLinkFact is one remote pane's link state for a snapshot.
type hostLinkFact struct {
	state string
	until int64
}

// liveHostLinks is the link state of every window on another machine whose
// link is not up, by PTY id.
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
		out[id] = hostLinkFact{state: state, until: until.Unix()}
	}
	return out
}

// placeFact is what a pane's shell reported about where it is.
type placeFact struct {
	cwd       string
	elsewhere string
	// seed is the folder the shell was started in, for a shell that has not
	// announced one.
	seed string
}

// paneFact is what a pane's emulator and shell know now, for a snapshot.
type paneFact struct {
	title string
	place placeFact
}

// livePaneFacts is each pane's live facts by PTY id, in one pass over the
// PTYs: the title its application last set, and what its shell announced
// over OSC 7, the folder and the machine when the report named another one,
// or else the folder the shell was started in. A pane with none of them is
// left out. A few atomic loads per pane, so it is not cached the way the
// process read is.
func (s *Session) livePaneFacts() map[string]paneFact {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	out := make(map[string]paneFact, len(s.ptys))
	for id, pty := range s.ptys {
		var fact paneFact
		fact.title = pty.Title()
		if _, remote := pty.pty.(*remotePane); !remote {
			fact.place = placeFact{cwd: pty.place.announcedCwd(), elsewhere: pty.place.Elsewhere()}
			if fact.place.cwd == "" {
				fact.place.seed = pty.place.Cwd()
			}
		}
		if fact != (paneFact{}) {
			out[id] = fact
		}
	}
	return out
}

// forgetCwdCache drops the cached directory read, so the next snapshot asks
// again rather than repeating an answer that is known to be stale.
func (s *Session) forgetCwdCache() {
	s.cwdCacheMu.Lock()
	s.cwdCache, s.cwdReadAt = nil, time.Time{}
	s.cwdCacheMu.Unlock()
}

// cwdReadInterval bounds how often a session reads its shells' directories out
// of the operating system. A second is well under the time it takes a person to
// notice a cd, and well over the rate the render path asks.
const cwdReadInterval = time.Second

// liveCwds is each window's working directory, read from the process the daemon
// owns, keyed by PTY id.
//
// The stored Cwd is only ever set by a shell announcing one over OSC 7, and
// most shells are not configured to announce. That was survivable while the
// only consumer was the file section running on the same machine as the pane,
// because it could read the process itself when nothing was announced. It
// stopped being survivable once a client could attach a session on another
// machine: that client has no process to read, so a pane on another machine had
// no directory at all, and a file section with nothing to be about does not
// draw a row, which means the whole section disappears.
//
// The side that owns the process is the side that can answer. It answers here,
// and the answer crosses the wire with the rest of the window state, so a
// client gets a directory the same way whether the pane is on this machine or
// another one.
func (s *Session) liveCwds() map[string]string {
	s.cwdCacheMu.Lock()
	if s.cwdCache != nil && time.Since(s.cwdReadAt) < cwdReadInterval {
		cached := s.cwdCache
		s.cwdCacheMu.Unlock()
		return cached
	}
	s.cwdCacheMu.Unlock()

	// Read outside the cache lock so the two are never held together. Two
	// callers racing here both do the work and both store the same answer,
	// which is cheaper than serialising the render path behind a syscall.
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

// liveTitles maps PTY ID to the title that PTY's application last set, for every
// PTY that has set one.
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

// snapshotStateLocked returns a copy of the canonical state. The caller must
// hold stateMu (for read or write).
func (s *Session) snapshotStateLocked() *SessionState {
	// Return a copy
	stateCopy := *s.state
	stateCopy.Windows = make([]WindowState, len(s.state.Windows))
	copy(stateCopy.Windows, s.state.Windows)
	// Worktree is a pointer, so the struct copy above aliases the canonical
	// record rather than copying it. That was harmless while the only write was
	// SetWorktree replacing the whole pointer, but the fan prompt writes the
	// status fields through the pointer under stateMu, and the snapshot is
	// encoded for the wire after stateMu is released. Copying the record here
	// is what makes the snapshot a snapshot. WorktreeInfo is value fields
	// apart from Verify and PR, which are only ever replaced whole, so one
	// level is the whole of it.
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
	stateCopy.PushSeen = maps.Clone(s.pushSeen)
	stateCopy.LayoutTreeOps = !s.treeOpsOff
	stateCopy.ScratchWorkspaces = !s.scratchWSOff
	// Taken under the state lock, so a copy with a higher number shows the
	// state at least as late as one with a lower number.
	stateCopy.SnapshotSeq = s.snapSeq.Add(1)
	// WorkspaceTrees, WindowToBSPID, PaneGeometry and ScrollStrip are left
	// aliased on purpose: the daemon only ever replaces those whole, never
	// writes into what they point at, so a snapshot that shares them is reading
	// something nothing mutates. Deep-copying the BSP trees on every mutation
	// would cost more than that buys. The invariant is the whole of the safety
	// here, so a daemon-side write *through* one of those pointers has to clone
	// the field here first. See the Worktree case above, which is exactly that
	// invariant broken.
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
	// The first window's directory is the session's, and this is the one place
	// it is read on a cadence: a save happens only when the state changed, so a
	// shell that moved into or out of a worktree is noticed here and an idle
	// session is never looked at.
	if len(state.Windows) > 0 {
		s.refreshWorktree(state.Windows[0].Cwd)
	}
	state.SessionID = s.ID
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

// notePushLocked is NotePush for a caller that holds stateMu. A push or an op
// that changes the state is counted here, inside the same critical section as
// the change, so no snapshot can say the daemon has seen push k while showing
// the state from before it. A client that trusted such a snapshot took an old
// tree over the op it had just sent.
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
	delete(s.clientFocusMoved, origin)
}

// UpdateStateFrom is UpdateState for a push from a client that may or may not
// be the person. seen false keeps the push from marking the focused window's
// finished turn seen: a client running inside a pane is an agent looking, and
// finished_unread is about whether the person has. See human_origin.go.
func (s *Session) UpdateStateFrom(state *SessionState, seen bool) bool {
	accepted, _ := s.updateStateFrom(state, seen)
	return accepted
}

// updateStateFrom is UpdateStateFrom that also reports whether the push was
// built before a tree op another client sent. Such a push is accepted, because
// it cannot undo a tree op (see missedMutationLocked). But the client that
// sent it has not seen that tree, and a client is never sent its own push
// back, so the caller answers it with the session's state.
func (s *Session) updateStateFrom(state *SessionState, seen bool) (accepted, behind bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	// The push is counted here, with the change it makes. The table it goes
	// into lives beside the state, not in it. The origin is also what tells
	// one client's focus move from another's, below.
	origin := state.PushOrigin
	s.notePushLocked(origin, state.PushSeq)
	state.PushOrigin, state.PushSeq, state.PushSeen, state.SnapshotSeq = "", 0, nil, 0

	accepted = true
	prev := s.state
	if prev != nil {
		behind = state.BaseVersion != 0 && s.missedPeerTreeLocked(origin, state.BaseVersion, prev.Version)
		if state.BaseVersion != 0 && s.missedMutationLocked(state.BaseVersion, prev.Version) {
			mine := focusViewOf(state)
			reconcileStale(state, prev, s.hasLivePTY)
			if s.pushOwnsFocusLocked(origin, state.BaseVersion) {
				// Nothing the client missed moved the focus, so the focus in
				// the push is the person's latest move.
				keepClientFocus(state, mine)
			}
			accepted = false
		}
		retainDaemonExclusive(state, prev)
		// While a client too old for scratch workspaces is attached, a push
		// cannot put the focus on one: that client would draw it nowhere.
		if s.scratchWSOff {
			unfocusScratchLocked(state)
		}
		// Version counts daemon-side mutations only, so converging on a client
		// snapshot carries it forward unchanged: the client is not telling the
		// daemon anything the daemon did not already know.
		state.Version = prev.Version
		// A push from a client too old for tree ops carries its trees and is
		// taken as sent, without a version, as before. It is not answered to
		// the other clients. Counting it as a tree op (a version, or a reply
		// owed to every other client) was measured: with a v0.8.0 client the
		// pair ended on different screens in 60 to 76 rounds of 150, against
		// 0 without it, because the reply handed the current client the older
		// client's tree after the older client had already taken the current
		// one. A pair with a v0.8.0 client stays last-writer-wins.
	}
	state.BaseVersion = 0

	before := snapshotLifecycle(prev)
	if prev != nil && !focusViewOf(prev).sameFocus(focusViewOf(state)) {
		if s.clientFocusMoved == nil {
			s.clientFocusMoved = make(map[string]int)
		}
		s.clientFocusMoved[origin] = state.Version
	}
	// The name is the daemon's. A client that built this push before a rename
	// still carries the old one, and taking it would undo the rename.
	state.Name = s.Name()
	s.state = state
	// A window the client closed takes its subagents with it.
	s.forgetSubagentsLocked(state)
	// A client pushing state with a pane focused has that pane in front of
	// its user, so whatever it finished has been seen.
	if seen {
		s.markCompletionSeenLocked(state.FocusedWindowID)
		// A move of the focus is the person turning to a pane, which ends an
		// approval hold on it (see approvals.go). Only a move: a push that
		// keeps the focus says nothing new.
		if state.FocusedWindowID != "" && (prev == nil || prev.FocusedWindowID != state.FocusedWindowID) {
			s.emit(SessionEvent{Type: eventPaneFocused, Window: state.FocusedWindowID})
		}
	}
	s.TouchActive()
	s.noteStateChangeLocked()
	s.emitLifecycleLocked(before)
	return accepted, behind
}

// pushOwnsFocusLocked reports whether a stale push from origin, built at base,
// may keep its own focus: no daemon move and no other client's move of the
// focus landed at or after base. Such a move may postdate the push, and the
// push would undo it. The origin's own earlier moves do not count: the push
// is newer than they are. The caller holds stateMu.
func (s *Session) pushOwnsFocusLocked(origin string, base int) bool {
	if base < s.focusMovedVersion {
		return false
	}
	for o, v := range s.clientFocusMoved {
		if o != origin && v >= base {
			return false
		}
	}
	return true
}

// markFocusIntentLocked records, from inside a mutateState function, that the
// mutation is an explicit focus request. See focusIntent.
func (s *Session) markFocusIntentLocked() {
	s.focusIntent = true
}

// mutateState runs fn against the canonical state under the state lock, raises
// the window lifecycle events implied by whatever fn changed, and hands the
// resulting state to the state sink. Daemon-side (headless) window operations go
// through it so they emit through the same diff as a TUI state sync, rather than
// each op emitting for itself.
//
// The sink call is what makes an attached client a subscriber to the daemon
// rather than the daemon's only writer: every mutation the daemon makes itself
// reaches the client's renderer through this one place, so a new daemon-side
// operation is live in the TUI without a line of routing code of its own.
func (s *Session) mutateState(fn func(state *SessionState) error) error {
	snap, err := s.mutateStateLocked(fn)
	if err != nil {
		return err
	}
	// Deliberately outside the state lock: the sink writes to client sockets.
	s.publishState(snap)
	return nil
}

// mutateStateLocked is mutateState's critical section. It returns the snapshot
// to publish once the lock is released.
func (s *Session) mutateStateLocked(fn func(state *SessionState) error) (*SessionState, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	before := snapshotLifecycle(s.state)
	focusBefore := focusViewOf(s.state)
	s.focusIntent = false
	if err := fn(s.state); err != nil {
		s.focusIntent = false
		return nil, err
	}
	s.noteAgentTurnsLocked(before, time.Now().UnixNano())
	clearNowAtRestLocked(before, s.state)
	s.forgetSubagentsLocked(s.state)
	pruneDeadLeaves(s.state)
	// A daemon-side mutation is exactly what a client sync must not undo, so it
	// is what advances the version. A client that pushes a snapshot built before
	// this point is reconciled by UpdateState rather than winning by arriving
	// last.
	s.state.Version++
	if s.focusIntent || !focusBefore.sameFocus(focusViewOf(s.state)) {
		s.focusMovedVersion = s.state.Version
	}
	s.focusIntent = false
	s.noteStateChangeLocked()
	s.emitLifecycleLocked(before)
	return s.snapshotStateLocked(), nil
}

// emitLifecycleLocked diffs the current state against before and emits the
// resulting events. It is called with the state lock held, deliberately: holding
// it across the emit is what keeps a session's events in the same order as the
// mutations that caused them when several callers mutate concurrently. It is
// safe because the sink only stamps a sequence number and does non-blocking
// channel sends; it never re-enters the session.
func (s *Session) emitLifecycleLocked(before lifecycleSnapshot) {
	events := diffLifecycle(before, snapshotLifecycle(s.state))
	for _, ev := range events {
		s.emit(ev)
	}
}

// Stop closes all PTYs and cleans up.
func (s *Session) Stop() {
	// Stop resurrection saving
	if s.stopResurrection != nil {
		s.stopResurrection()
	}
	// Final save before stopping. Capture cwds while the shells are still alive.
	// This is the last chance to persist the session, so a failure here is the
	// difference between it coming back and not; it is reported rather than
	// dropped even though Stop cannot act on it.
	final := s.ResurrectionState()
	if err := s.persist(final); err != nil {
		LogError("Final resurrection save for session %q failed, it will not come back: %v", s.Name(), err)
	}
	// Every pane with output since its last save, whatever the interval: this
	// is the save a restart restores from.
	s.persistMu.Lock()
	s.saveHistory(final, true)
	s.persistMu.Unlock()

	// Before the panes go, so a hold cannot publish a state against a session
	// that has already saved and stopped.
	s.stopAgentHoldTimer()
	s.idle.stop()
	s.agentMetaPrune.stop()
	s.subagentPrune.stop()

	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	for id, pty := range s.ptys {
		_ = pty.Close()
		delete(s.ptys, id)
	}
}

// WindowCount returns the number of windows in state.
func (s *Session) WindowCount() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return len(s.state.Windows)
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

// swapWindowSizePolicy records the window_size policy the session's size was
// last settled under and reports whether it changed. Guarded by sizeMu with
// the size it describes.
func (s *Session) swapWindowSizePolicy(policy string) bool {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	if s.sizePolicy == policy {
		return false
	}
	s.sizePolicy = policy
	return true
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
	displayName, accent := s.state.DisplayName, s.state.Accent
	currentWorkspace := s.state.CurrentWorkspace
	restored := s.state.Restored
	focusedPTY := ""
	if w, ok := findWindowState(s.state, s.state.FocusedWindowID); ok {
		focusedPTY = w.PTYID
	}
	s.stateMu.RUnlock()

	// The focused pane's place labels the session. Read after stateMu is
	// released: the PTY table has its own lock and the two are never nested.
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
		Attached:         false, // Will be set by manager
		Width:            width,
		Height:           height,
		Windows:          windows,
		DisplayName:      displayName,
		Accent:           accent,
		CurrentWorkspace: currentWorkspace,
		Restored:         restored,
		Global:           s.isGlobal(),
		Dir:              dir,
		Branch:           branch,
		Worktree:         s.worktreeListing(),
	}
}

// windowSummaries builds the lightweight per-window listing for Info() from the
// cached window states, titled from the live emulators. It does no PTY work, so
// it stays cheap enough to run on every session list.
func (s *Session) windowSummaries() []WindowSummary {
	live := s.liveTitles()

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
		// A named pane has nothing to gain from a command name, and offering one
		// would let it outrank the name the user chose.
		fg := w.ForegroundCmd
		if w.CustomName != "" {
			fg = ""
		}
		out = append(out, WindowSummary{
			ID:            w.ID,
			Title:         title,
			AgentState:    string(w.AgentState),
			AgentStateAt:  w.AgentStateAt,
			AgentHarness:  w.AgentHarness,
			AgentMessage:  w.AgentMessage,
			AgentKind:     agentBlockedBy(*w),
			CompletionSeq: w.CompletionSeq,
			AgentMeta:     w.AgentMeta,
			AgentQueued:   w.AgentQueued,
			Subagents:     w.AgentSubagents,
			ForegroundCmd: fg,
			Workspace:     w.Workspace,
			Scratch:       w.Scratch,
		})
	}
	return out
}

// getShell returns the shell a new pane in this session runs.
func (s *Session) getShell() string {
	shell, _ := s.resolveShell()
	return shell
}

// resolveShell picks the shell for a new pane: the one the client that made
// the session named, then the daemon's appearance.preferred_shell, then $SHELL
// and the platform default, through the same config.ShellFor the standalone
// path uses. missing is the preferred shell when it was set and not found,
// for the spawn path to log.
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

func (s *Session) buildEnv(windowID string, restored bool) []string {
	return s.buildEnvWith(windowID, restored, nil)
}

// buildEnvWith is buildEnvFor a pane that runs the user's shell.
func (s *Session) buildEnvWith(windowID string, restored bool, extra []string) []string {
	return s.buildEnvFor(windowID, 0, restored, extra, nil)
}

// buildEnvFor is buildEnv with a caller's own variables, for a pane that runs
// command (nil for the user's shell). The caller's variables replace the
// daemon's variables of the same name, and every variable set below them,
// TERM and the TUIOS_ contract, is set after them and wins. The caller's
// variables are checked before they get here (callerEnv), which refuses a
// TUIOS_ name outright. command decides what a harness started directly is
// told beyond that: see guestenv.TermProgramFor. workspace is the one the
// window is being made on, 0 to read it from the state (herdrTabFor).
func (s *Session) buildEnvFor(windowID string, workspace int, restored bool, extra, command []string) []string {
	// The daemon's environment, less TMUX and TMUX_PANE. A daemon started from
	// inside tmux would otherwise hand every pane the variables that make a
	// program believe it is in a tmux pane. See guestenv.WithoutHostMultiplexer.
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

	// The TERM the session's first client named, checked against this
	// machine's terminfo: a client's xterm-kitty is no use to a pane on a host
	// with no entry for it. See guestenv.PaneTerm.
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
	env = append(env, "TERM_PROGRAM_VERSION=0.1.0")
	env = append(env, "TUIOS_SESSION="+s.Name())
	// TUIOS_HOST names the machine this pane runs on. A pane is always local
	// to the daemon that made it, so this is the daemon's own hostname, on
	// every machine: a program that wants to know where it is reads it, and a
	// program that sends mail to another machine signs with it.
	if s.config != nil && s.config.HostName != "" {
		env = append(env, "TUIOS_HOST="+s.config.HostName)
	}
	if windowID != "" {
		env = append(env, "TUIOS_WINDOW_ID="+windowID)
		// TUIOS_PANE_ID is an alias a state-reporting shim guards on, mirroring
		// the pane-id contract other multiplexers' agent integrations use.
		env = append(env, "TUIOS_PANE_ID="+windowID)
		// TUIOS_PANE_TOKEN proves the pane id to restrict-connection where
		// the kernel cannot name the caller's pane. See pane_token.go.
		if s.config != nil && s.config.PaneToken != nil {
			if tok := s.config.PaneToken(windowID); tok != "" {
				env = append(env, "TUIOS_PANE_TOKEN="+tok)
			}
		}
		// TUIOS_PANE_GRANTS says what the pane may do through tuios as it
		// starts. pane-grants gives the current answer. See pane_grants.go.
		if s.config != nil && s.config.grants != nil {
			env = append(env, "TUIOS_PANE_GRANTS="+s.config.grants.envValue(windowID))
		}
	}
	// TUIOS_ENV marks a process as running under tuios, and TUIOS_SOCKET tells a
	// shim which daemon socket to report to. Together with the pane id above they
	// are the whole contract the agent-state shim needs; a shim finds nothing to
	// report to when they are unset and no-ops.
	env = append(env, "TUIOS_ENV=1")
	if s.config != nil && s.config.SocketPath != "" {
		env = append(env, "TUIOS_SOCKET="+s.config.SocketPath)
	}
	// A harness that reports to herdr (Crush) finds tuios's herdr protocol
	// socket here, when this pane starts one. See herdr_compat.go.
	if s.config != nil && s.config.HerdrEnv != nil {
		env = append(env, s.config.HerdrEnv(s.ID, windowID, s.herdrTabFor(windowID, workspace), command)...)
	}
	// Mark restored shells so the user's shell rc (and scripts) can react, and
	// so the restore is observable without relying on the visual banner.
	if restored {
		env = append(env, "TUIOS_RESTORED=1")
	}

	return env
}

// restoredBanner returns the dim one-line notice written to a restored shell's
// terminal emulator. cwd, when set, is included so the user sees where the
// fresh shell was spawned.
func restoredBanner(cwd string) string {
	msg := "-- tuios: session restored, fresh shell"
	if cwd != "" {
		msg += " in " + cwd
	}
	msg += " --"
	return "\x1b[2m" + msg + "\x1b[0m\r\n"
}

// PTY methods

// maxSubscriberQueue bounds, in bytes, the output one (client, pane) stream
// holds for a client that has stopped taking it. The queue used to be bounded
// by slot count alone, and with 16 KiB reads that was 64 MiB per stream, so a
// daemon's heap grew by hundreds of megabytes while one stalled client sat
// behind one flooding pane. Past this the stream is marked gapped and stops
// queuing; it resumes from the ring once it has drained (resumeAfterGap). A
// variable so a test can lower it.
var maxSubscriberQueue int64 = 8 << 20

// ptySubscriber is one client's output stream. sent is the stream position of
// the last chunk this subscriber was handed, which is where the client is
// resumed if it comes back.
type ptySubscriber struct {
	ch   chan ptyChunk
	sent atomic.Int64
	// queued is how many bytes sit on ch that the stream goroutine has not
	// taken yet.
	queued atomic.Int64
	// gapped is set when a chunk could not be queued, because the queue held
	// maxSubscriberQueue bytes or every slot was taken. Nothing more is queued
	// for a gapped stream: what it holds drains, and then the stream is
	// rebuilt from sent through the ring. A chunk dropped in the middle of a
	// stream used to be a silent hole the client painted the rest of the
	// stream on top of, until the next workspace switch replaced the screen.
	gapped atomic.Bool

	// framesWaiting counts the frames on ch that the stream goroutine has not
	// taken. skipped counts the frames dropped for newer ones. See
	// kitty_frames.go.
	framesWaiting atomic.Int32
	skipped       atomic.Int64
	// noPace marks a client that never holds the pane: a read-only viewer or
	// a client on another machine. See holdForSlowSubscribers.
	noPace atomic.Bool

	// Read and written by broadcast only, under streamMu. seen is the stream
	// position this client has been handed or skipped up to. open is the
	// frame it is gathering, and passFrame sends the rest of a frame as it
	// arrives. latest is the newest waiting frame of each image on each
	// screen, and lastFrame the last frame queued, while nothing but a
	// cursor move has come after it.
	seen      int64
	skipFrame bool // the catch-up began inside this frame: skip its rest
	// The catch-up began inside a graphics command that is not a frame,
	// which starts at skipFrom: skip its rest.
	skipSpan  bool
	skipFrom  int64
	open      *queuedFrame
	passFrame bool
	latest    map[imageKey]*queuedFrame
	lastFrame *queuedFrame
}

// ptyChunk is one item on a subscriber's stream: output bytes, or the size the
// daemon's emulator took at exactly this point. A resize carries no bytes and
// so does not move the stream position.
type ptyChunk struct {
	data          []byte
	width, height int // both > 0 marks a resize rather than output
	// frame is one whole kitty graphics frame instead of data. See
	// kitty_frames.go.
	frame *queuedFrame
}

// size is how many bytes the chunk holds.
func (c ptyChunk) size() int64 {
	if c.frame != nil {
		return c.frame.size
	}
	return int64(len(c.data))
}

// resizeMark records the stream position a resize took effect at. The ring
// holds bytes only, so without the marks a subscriber resumed across a resize
// was handed the whole span at one width and laid out at that width lines the
// daemon had wrapped at another. Guarded by outputMu and pruned with the ring.
type resizeMark struct {
	seq           int64
	width, height int
}

func (c ptyChunk) isResize() bool { return c.width > 0 && c.height > 0 }

// resyncPrefix homes the cursor and clears the screen and the scrollback. It
// goes in front of a catch-up the client cannot splice onto what it already
// holds.
var resyncPrefix = []byte("\x1b[H\x1b[2J\x1b[3J")

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
func (p *PTY) Subscribe(clientID string, fromSeq int64) <-chan ptyChunk {
	return p.subscribe(clientID, fromSeq, false)
}

// SubscribeFromSnapshot is Subscribe for a client that has just laid down an
// authoritative snapshot of the pane ending at fromSeq. When the catch-up ring
// has rolled past fromSeq, a plain Subscribe clears the client's screen before
// replaying the tail so the tail paints against a known state; that clear
// throws away the snapshot's rows, which are exactly what a full-screen program
// drew and cannot be recovered from the ring (issue #123). A snapshot is the
// whole of the stream up to fromSeq, so the tail replays on top of it.
func (p *PTY) SubscribeFromSnapshot(clientID string, fromSeq int64) <-chan ptyChunk {
	return p.subscribe(clientID, fromSeq, true)
}

func (p *PTY) subscribe(clientID string, fromSeq int64, fromSnapshot bool) <-chan ptyChunk {
	return p.subscribeSub(clientID, fromSeq, fromSnapshot).ch
}

// subscribeSub is Subscribe returning the subscriber itself. A stream
// goroutine holds on to it, so that when the goroutine ends it releases its
// own subscription and not a newer one the same client has made since.
func (p *PTY) subscribeSub(clientID string, fromSeq int64, fromSnapshot bool) *ptySubscriber {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
	p.subscribeLocked(clientID, fromSeq, fromSnapshot)
	return p.subscribers[clientID]
}

// subscriberFor returns the client's stream, or nil when it has none. The
// stream goroutine reads it once per stream to account for what it takes.
func (p *PTY) subscriberFor(clientID string) *ptySubscriber {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()
	return p.subscribers[clientID]
}

// resumeAfterGap rebuilds the gapped stream cur once it has drained. The new
// stream resumes at the position the old one reached, so the client is handed
// what it missed from the ring, behind a clear when the ring has rolled past
// it. It returns nil when cur is not gapped, still holds chunks, or is no
// longer the client's stream: a client that unsubscribed and subscribed again
// while cur's goroutine was still draining has a new stream, and rebuilding
// over it would close the channel its new goroutine reads.
func (p *PTY) resumeAfterGap(clientID string, cur *ptySubscriber) (<-chan ptyChunk, *ptySubscriber) {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
	sub, ok := p.subscribers[clientID]
	if !ok || sub != cur || !sub.gapped.Load() || len(sub.ch) > 0 {
		return nil, nil
	}
	close(sub.ch)
	delete(p.subscribers, clientID)
	debugLog("[DEBUG] PTY %s: client %s fell behind at %d, resuming from the ring", p.ID[:8], clientID, sub.sent.Load())
	ch := p.subscribeLocked(clientID, sub.sent.Load(), false)
	next := p.subscribers[clientID]
	next.noPace.Store(sub.noPace.Load())
	p.wakePacer()
	return ch, next
}

// subscribeLocked is subscribe with subscribersMu held.
func (p *PTY) subscribeLocked(clientID string, fromSeq int64, fromSnapshot bool) <-chan ptyChunk {
	// Return existing channel if already subscribed
	if existing, ok := p.subscribers[clientID]; ok {
		debugLog("[DEBUG] PTY %s: client %s already subscribed", p.ID[:8], clientID)
		return existing.ch
	}

	// Slots bound the count of chunks, maxSubscriberQueue bounds their bytes.
	// Each chunk is one PTY read of up to 16 KiB, and the catch-up below is
	// at most the ring, so the byte bound is the one a stalled client meets.
	// The channel itself is 160 KiB per (client, pane); at 16384 it was 640.
	sub := &ptySubscriber{ch: make(chan ptyChunk, 4096)}
	p.subscribers[clientID] = sub
	p.subscriberCount.Store(int32(len(p.subscribers)))
	debugLog("[DEBUG] PTY %s: added subscriber %s (total: %d)", p.ID[:8], clientID, len(p.subscribers))

	// Send whatever the client has not seen to catch it up.
	p.outputMu.RLock()
	// A client that fell further behind than the buffer reaches cannot be
	// resumed exactly, so it gets everything still held rather than a gap.
	bufStart := p.outputSeq - int64(p.outputPos)
	// The catch-up ends where broadcast has got to. readOutput appends a read
	// to the ring before it broadcasts it, so a subscribe can land between
	// the two: that read goes out through broadcast, which cuts it into text
	// and frames, rather than in the catch-up, which the cut has not seen.
	ringEnd := p.outputPos
	if unscanned := p.unbroadcast.Load(); unscanned > 0 {
		ringEnd = max(0, p.outputPos-int(unscanned))
	}
	endSeq := bufStart + int64(ringEnd)
	start := 0
	rolled := fromSeq > 0 && fromSeq < bufStart
	if fromSeq > bufStart {
		start = min(int(fromSeq-bufStart), ringEnd)
	}
	// A catch-up that starts inside a kitty graphics command would hand the
	// client the rest of it with no start, and the client prints the payload
	// as text. Start after it instead. A frame still arriving is skipped as
	// it arrives (route).
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
			select {
			case sub.ch <- c:
				sub.queued.Add(int64(len(c.data)))
			default:
				debugLog("[DEBUG] PTY %s: failed to send catch-up chunk (channel full)", p.ID[:8])
			}
		}
		// The replay is cut at every resize mark inside it and the resize sent
		// between the two segments, so the client lays each segment out at the
		// width the daemon laid it out at. One flat replay put the whole span
		// at one width, and a catch-up that crossed a resize came back with
		// every line after it wrapped where the daemon had not wrapped it.
		startSeq := bufStart + int64(start)
		// The width the replay begins at. A client resumed here is usually at
		// it already and skips it; a rolled client, whose screen the resync
		// below clears, is not.
		for i := len(p.resizeMarks) - 1; i >= 0; i-- {
			if m := p.resizeMarks[i]; m.seq <= startSeq {
				send(ptyChunk{width: m.width, height: m.height})
				break
			}
		}
		var prefix []byte
		if rolled && !fromSnapshot {
			// The client still holds the screen it drew up to fromSeq, and the
			// bytes between there and the buffer's start are gone. Appending the
			// tail to that screen splices two halves of the stream that never
			// met: the missing bytes carried the cursor moves and the modes the
			// tail is written against, so the guest's output lands wherever the
			// old screen had left off. Clear first, so the tail repaints from a
			// known state instead of over a stale one.
			//
			// A client that just restored a snapshot is not in that state: the
			// snapshot is the whole of the stream up to fromSeq, and clearing
			// it throws away rows a full-screen program drew that the ring no
			// longer holds (issue #123). The tail replays on top of it.
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
			send(ptyChunk{data: seg})
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

	// The size the emulator is at now, behind the catch-up. A resize is only
	// broadcast to the subscribers of the moment, so one that landed between a
	// client's snapshot and its subscribe reaches nobody; this states the
	// answer on every subscribe instead of leaving the pane at whatever width
	// the client last heard about. It is a no-op whenever nothing was missed.
	p.terminalMu.RLock()
	if p.terminal != nil {
		w, h := p.terminal.Width(), p.terminal.Height()
		select {
		case sub.ch <- ptyChunk{width: w, height: h}:
		default:
		}
	}
	p.terminalMu.RUnlock()

	return sub.ch
}

// Unsubscribe removes a subscriber and returns the stream position it reached,
// to hand back to Subscribe when the client returns.
func (p *PTY) Unsubscribe(clientID string) int64 {
	return p.unsubscribe(clientID, nil)
}

// unsubscribeSub is Unsubscribe for one stream: it removes the client's
// subscriber only while that is still sub. A stream goroutine ends some time
// after the client has let its subscription go, and by then the client may
// have subscribed again. Removing by client ID alone took the new
// subscription with it, and the pane stopped updating until it was hidden and
// shown once more.
func (p *PTY) unsubscribeSub(clientID string, sub *ptySubscriber) {
	p.unsubscribe(clientID, sub)
}

// unsubscribe removes the client's subscriber, when only is nil or is that
// subscriber.
func (p *PTY) unsubscribe(clientID string, only *ptySubscriber) int64 {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()

	sub, ok := p.subscribers[clientID]
	if !ok || (only != nil && sub != only) {
		return 0
	}
	// Closing lets the streaming goroutine drain what is still queued, so every
	// chunk broadcast up to here does reach the client.
	close(sub.ch)
	delete(p.subscribers, clientID)
	p.subscriberCount.Store(int32(len(p.subscribers)))
	p.wakePacer()
	return sub.sent.Load()
}

// Write sends input to the PTY.
func (p *PTY) Write(data []byte) (int, error) {
	if p.pty == nil {
		return 0, fmt.Errorf("PTY not available")
	}
	p.flushWinsize()
	return p.pty.Write(data)
}

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
	if p.terminal != nil && cellWidth > 0 && cellHeight > 0 {
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
	if p.cellWidth == cellWidth && p.cellHeight == cellHeight {
		return nil
	}
	p.cellWidth, p.cellHeight = cellWidth, cellHeight
	if _, ok := p.pty.(ptyspawn.WinsizeSetter); !ok {
		return nil
	}
	// A size already held is written with the new pixels when it is due.
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
	// A resize to the size the pane is already at is nothing, and has to cost
	// nothing. It is not a rare case: every client of a session announces every
	// pane's size for itself, so a second client attaching, or a client
	// re-announcing after a retile that moved nothing, arrives here with the
	// size that is already set.
	//
	// A resize is not free. It marks the ring, broadcasts a width to every
	// subscriber, resizes the emulator (which drops the scroll region a
	// full-screen program had set) and SIGWINCHes the guest into repainting.
	// A repaint at an unchanged width is invisible when it works and is a line
	// of lost scrollback when the guest's own idea of where the cursor is does
	// not survive it.
	//
	// Zero is not a size and is left to the layers below to reject; it must not
	// be recorded as the pane's own.
	//
	// The size is recorded, queued for the emulator and applied to the real PTY
	// under one lock, because every client of a session announces sizes on
	// its own connection and two of them can arrive here at once. Recording
	// under one lock and queueing under another let a pair of resizes be
	// recorded in one order and queued in the other: the pane then said 26
	// columns while its emulator sat at 22, and the next 26 from anyone was
	// declined above as unchanged, so nothing ever put the emulator right. A
	// client attached to that pane was handed a snapshot laid out at a width
	// no client was drawing.
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	p.terminalMu.Lock()
	unchanged := width > 0 && height > 0 && p.width == width && p.height == height
	oldW, oldH := p.width, p.height
	p.width, p.height = width, height
	p.terminalMu.Unlock()
	if unchanged {
		return nil
	}
	// A size change is rare and is the one thing a shell repaints its prompt
	// for, so it is worth a line in `tuios logs`: a pane that gains blank lines
	// on a focus move is answered by whether this line appears with it.
	LogBasic("PTY %s resized %dx%d -> %dx%d", shortID(p.ID), oldW, oldH, width, height)

	if !p.vtClosed {
		// Recorded against the ring before it is broadcast, so a catch-up cut
		// from the ring later replays it between the same two bytes every
		// subscriber of this moment saw it between.
		p.outputMu.Lock()
		p.resizeMarks = append(p.resizeMarks, resizeMark{seq: p.outputSeq, width: width, height: height})
		p.outputMu.Unlock()
		p.broadcast(ptyChunk{width: width, height: height}, 0)
		select {
		case p.vtWriteChan <- vtChunk{width: width, height: height}:
		case <-p.ctx.Done():
		}
	}

	// The real PTY is resized without waiting for the emulator to catch up
	// with the backlog. The pixel size goes in the same ioctl: see
	// ptyspawn.SetWinsize. A resize inside a burst is held and written with
	// the burst's last size: see pty_winsize.go.
	p.winsizeMu.Lock()
	defer p.winsizeMu.Unlock()
	return p.setWinsizeLocked(width, height)
}

// DefaultStateScrollback is how many scrollback lines a state request carries
// when it does not ask for a number.
const DefaultStateScrollback = 1000

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

	// The emulator's own size, not the pane's announced one. They differ only
	// while a resize is still behind output in the stream, and a snapshot has
	// to describe the grid it is serializing: reporting the size the pane is
	// about to be handed one row of cells short.
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
	state, finish := beginTerminalState(p.terminal, p.terminal.Width(), p.terminal.Height(), maxScrollback, have, true)
	state.Seq = p.vtSeq
	p.terminalMu.RUnlock()
	finish()
	return state
}

// TerminalStateOf serializes everything a client needs to arrive at the picture
// this emulator holds. It is one half of the wire contract; ApplyTerminalState
// is the other, and the two are kept in this file so a field added to one is
// read by the other. docs/REHYDRATION.md states the contract.
//
// Width and height are the pane's, which the caller knows; the emulator's own
// size lags a resize the shell has not acknowledged yet.
//
// have is how many scrollback rows the receiving emulator already holds; only
// the rows past it are serialized, because only those can be used. See
// GetTerminalState.
func TerminalStateOf(t vt.Terminal, width, height, maxScrollback, have int) *TerminalState {
	return terminalStateOf(t, width, height, maxScrollback, have, false)
}

// terminalStateOf is TerminalStateOf, with the cells packed as they are read
// when packed is set (see GetTerminalStatePacked).
func terminalStateOf(t vt.Terminal, width, height, maxScrollback, have int, packed bool) *TerminalState {
	state, finish := beginTerminalState(t, width, height, maxScrollback, have, packed)
	finish()
	return state
}

// beginTerminalState is terminalStateOf in two halves. It reads the emulator
// and returns the state with finish, which completes it without reading the
// emulator again, so a caller can release the emulator's lock before it calls
// finish. The state is complete only once finish has run.
func beginTerminalState(t vt.Terminal, width, height, maxScrollback, have int, packed bool) (*TerminalState, func()) {
	state := &TerminalState{
		Width:         width,
		Height:        height,
		CursorX:       t.CursorPosition().X,
		CursorY:       t.CursorPosition().Y,
		ScrollbackLen: t.ScrollbackLen(),
		IsAltScreen:   t.IsAltScreen(),        // Capture alt screen state for mouse event forwarding
		Modes:         t.GetModes(),           // Capture terminal modes (mouse tracking, bracketed paste, etc.)
		KittyKbdStack: t.KittyKeyboardStack(), // Capture kitty keyboard protocol flag stack
		// The main screen's stack, under a program on the alternate one.
		KittyKbdMainStack: t.KittyKeyboardMainStack(),
		// Set once by an editor at start, like the kitty flags, so it
		// cannot be recovered from the output buffer either.
		ModifyOtherKeys:      t.ModifyOtherKeys(),
		ModifyOtherKeysKnown: true,
	}

	// None of these is recoverable from the cells. They are what the guest set
	// and has not reset, and they decide how the output that has not arrived
	// yet is painted, where it lands, and which glyphs it draws.
	pen, link := t.CursorPen()
	ps := styleToWire(pen, link)
	state.Pen = &ps

	// Carried only when the guest set one. A region that is simply the whole
	// screen says nothing, and sending it pins a client that has been resized
	// since to whatever size this pane was when the snapshot was taken.
	if m := t.ScrollRegion(); m != t.Bounds() {
		state.Margins = []int{m.Min.X, m.Min.Y, m.Dx(), m.Dy()}
	}

	ids, gl, gr := t.Charsets()
	state.Charsets = []int{int(ids[0]), int(ids[1]), int(ids[2]), int(ids[3]), gl, gr}

	// The cursor shape is set once, by a shell's prompt or by an editor
	// changing mode, and is long out of the output buffer's reach by the time
	// anyone reattaches. Without it here a pane that asked for a bar comes back
	// as a block, because that is what a fresh emulator is.
	state.CursorShape = decscusrParam(t.CursorStyle())

	// Colours are encoded through one cache for the whole snapshot: a
	// truecolor cell's hex string was one allocation per cell, eleven
	// thousand per screen, for what is usually a few dozen distinct colours.
	colors := colorWireCache{}
	first, end := scrollbackWindow(t.ScrollbackLen(), maxScrollback, have)
	finish := func() {}
	if packed {
		finish = packStateCells(t, state, colors, first, end)
	} else {
		stateCells(t, state, colors, first, end)
	}
	screen := make([]rowFlags, state.Height)
	for y := range screen {
		screen[y] = screenRowFlags(t, y)
	}
	state.ScreenWraps, state.ScreenPads = rowFlagBits(screen)
	return state, finish
}

// rowFlags is one row's soft-wrap and padding flags (vt.Terminal's
// RowSoftWrapped and RowPadded). A capture collects them together, and the
// wire carries each as a bitset of its own, split at encode time
// (rowFlagBits).
type rowFlags uint8

const (
	rowWrapped rowFlags = 1 << iota
	rowPadded
)

// newRowFlags is the flags of a row that is wrapped and padded as said.
func newRowFlags(wrapped, padded bool) rowFlags {
	var f rowFlags
	if wrapped {
		f |= rowWrapped
	}
	if padded {
		f |= rowPadded
	}
	return f
}

// screenRowFlags is the flags of screen row y.
func screenRowFlags(t vt.Terminal, y int) rowFlags {
	w, _ := t.RowSoftWrapped(y)
	return newRowFlags(w, t.RowPadded(y))
}

// historyRowFlags is the flags of scrollback row i. The capture loops append
// them as they send each row, so entry n is the n-th row sent.
func historyRowFlags(t vt.Terminal, i int) rowFlags {
	w, _ := t.ScrollbackSoftWrapped(i)
	return newRowFlags(w, t.ScrollbackPadded(i))
}

// rowFlagBits splits flags into the wire's wrap and pad bitsets, each as
// wrapBits builds it: nil when no row has the flag.
func rowFlagBits(flags []rowFlags) (wraps, pads []byte) {
	for i, f := range flags {
		if f&rowWrapped != 0 {
			if wraps == nil {
				wraps = make([]byte, (len(flags)+7)/8)
			}
			wraps[i/8] |= 1 << (i % 8)
		}
		if f&rowPadded != 0 {
			if pads == nil {
				pads = make([]byte, (len(flags)+7)/8)
			}
			pads[i/8] |= 1 << (i % 8)
		}
	}
	return wraps, pads
}

// scrollbackWindow returns the scrollback rows [first, end) a snapshot carries.
// maxScrollback is as GetTerminalState documents it.
//
// Rows the caller already holds (have) are rows it will discard on arrival: it
// keeps its own history and merges only what scrolled off while it was away.
// Sending them anyway is what made a workspace switch move megabytes per pane
// to be thrown away at the far end. state.ScrollbackLen is still the true
// length, which is what the caller subtracts against.
func scrollbackWindow(scrollbackLen, maxScrollback, have int) (first, end int) {
	if maxScrollback == 0 {
		maxScrollback = DefaultStateScrollback
	}
	want := max(scrollbackLen-have, 0)
	if maxScrollback >= 0 && want < maxScrollback {
		maxScrollback = want
	}
	if maxScrollback < 0 {
		first = scrollbackLen
	} else if scrollbackLen > maxScrollback {
		first = scrollbackLen - maxScrollback
	}
	return first, scrollbackLen
}

// stateCells captures the screen, the main screen under an alternate one, and
// scrollback rows [first, end) as cells.
func stateCells(t vt.Terminal, state *TerminalState, colors colorWireCache, first, end int) {
	width, height := state.Width, state.Height
	// Capture visible screen with full styling. The rows share one backing
	// array: gob writes each row by its own length, so the shape on the wire
	// is the same and the allocation count is one instead of one per row.
	state.Screen = make([][]CellState, height)
	cells := make([]CellState, width*height)
	for y := range height {
		state.Screen[y] = cells[y*width : (y+1)*width : (y+1)*width]
		for x := range width {
			cell := t.CellAt(x, y)
			if cell != nil {
				state.Screen[y][x] = colors.cellState(cell)
			}
		}
	}

	if state.IsAltScreen {
		state.MainScreen = make([][]CellState, height)
		cells := make([]CellState, width*height)
		for y := range height {
			state.MainScreen[y] = cells[y*width : (y+1)*width : (y+1)*width]
			for x := range width {
				if cell := t.MainCellAt(x, y); cell != nil {
					state.MainScreen[y][x] = colors.cellState(cell)
				}
			}
		}
	}

	// One backing array for the history too, cut into rows as they are read.
	// A row wider than the screen, from before a narrowing resize, is rare and
	// merely starts a new array.
	state.Scrollback = make([][]CellState, 0)
	var pool []CellState
	end = min(end, t.ScrollbackLen())
	t.ScrollbackRows(first, end, func(i int, line uv.Line) bool {
		if cap(pool) < len(line) {
			pool = make([]CellState, max(len(line), width*(end-i)))
		}
		row := pool[:len(line):len(line)]
		pool = pool[len(line):]
		for x := range line {
			row[x] = colors.cellState(&line[x])
		}
		state.Scrollback = append(state.Scrollback, row)
		return true
	})
	var flags []rowFlags
	for i := first; i < end; i++ {
		flags = append(flags, historyRowFlags(t, i))
	}
	state.ScrollbackWraps, state.ScrollbackPads = rowFlagBits(flags)
}

// packStateCells is stateCells followed by Pack, done a row at a time through
// one scratch row, so the cells never exist as [][]CellState. The grids are
// packed in the order Pack packs them (screen, scrollback, main screen), which
// is what makes the style table, and so every byte, the same as Pack's.
// TestDirectPackMatchesPack holds it to that.
//
// It packs the screen and copies what the rest needs from the emulator, and
// returns finish, which packs the history and the main screen from those
// copies without reading the emulator. The history is copied in its encoded
// form, a few bytes a cell, rather than decoded: 1000 rows of a 207-column
// pane read through ScrollbackLine were 23 MB of cells, and the line cache
// kept the last 256 of them.
func packStateCells(t vt.Terminal, state *TerminalState, colors colorWireCache, first, end int) (finish func()) {
	width, height := state.Width, state.Height
	p := newRowPacker()
	row := make([]CellState, width)
	readRow := func(at func(x, y int) *uv.Cell, y int, row []CellState) {
		for x := range width {
			if cell := at(x, y); cell != nil {
				row[x] = colors.cellState(cell)
			} else {
				row[x] = CellState{}
			}
		}
	}

	b := newPackedRows(height * 32)
	for y := range height {
		readRow(t.CellAt, y, row)
		b.add(p, row[:width])
	}
	state.PackedScreen = b.blob()

	end = min(end, t.ScrollbackLen())
	history := t.CopyScrollback(first, end)
	// The main screen under an alternate one is packed after the history,
	// so its cells are read now.
	var main []CellState
	if state.IsAltScreen {
		main = make([]CellState, width*height)
		for y := range height {
			readRow(t.MainCellAt, y, main[y*width:(y+1)*width])
		}
	}

	return func() {
		n := history.Len()
		b := newPackedRows(n * 32)
		flags := make([]rowFlags, n)
		history.Rows(0, n, func(i int, line uv.Line) bool {
			flags[i] = newRowFlags(history.Wrapped(i), history.Padded(i))
			if cap(row) < len(line) {
				row = make([]CellState, len(line))
			}
			r := row[:len(line)]
			// A history row is mostly blank tail, and the packer drops that
			// tail anyway, so only the cells before it are converted. The
			// rest are written as the blank the packer compares against,
			// which keeps the bytes identical to Pack's.
			used := usedCells(line)
			for x := range used {
				r[x] = colors.cellState(&line[x])
			}
			for x := used; x < len(r); x++ {
				r[x] = blankCellState
			}
			b.add(p, r)
			return true
		})
		state.PackedScrollback = b.blob()
		state.ScrollbackWraps, state.ScrollbackPads = rowFlagBits(flags)

		if main != nil {
			b := newPackedRows(height * 32)
			for y := range height {
				b.add(p, main[y*width:(y+1)*width])
			}
			state.PackedMain = b.blob()
		}
		state.Styles = p.styles
	}
}

// blankCellState is a never-written cell as the wire holds it: the cell the
// packer trims from a row's tail.
var blankCellState = CellState{Content: " ", Width: 1}

// usedCells is the length of a line without its blank tail: the cells that
// convert to blankCellState, a space of width one with no style and no link.
func usedCells(line uv.Line) int {
	n := len(line)
	for n > 0 {
		c := &line[n-1]
		if c.Content != " " || c.Width != 1 || c.Link != (uv.Link{}) ||
			c.Style.Fg != nil || c.Style.Bg != nil || c.Style.UnderlineColor != nil ||
			c.Style.Attrs != 0 || c.Style.Underline != 0 {
			break
		}
		n--
	}
	return n
}

// ApplyTerminalState brings an emulator to the state a snapshot describes. It
// is the reading half of the wire contract TerminalStateOf writes, and it is
// the whole of what rehydration does to an emulator: the caller owns the
// locking, the window-level flags and the stream that resumes afterwards.
//
// The emulator may be fresh or may be one that survived a workspace switch and
// already holds most of this, so every step is written to be idempotent.
func ApplyTerminalState(t vt.Terminal, state *TerminalState) {
	if t == nil || state == nil {
		return
	}
	// The cells are read in their packed form only (snapshot_pack.go), which
	// is how the client asks for them. A snapshot that arrived as cells, from
	// a daemon older than the packed form, is packed here first. Each style in
	// the table is resolved for t once, rather than once per cell.
	state.Pack()
	styles := resolveStyles(t, state.Styles)

	// A snapshot too big for the emulator it is going into used to be taken
	// silently, because writing a cell outside the buffer is a no-op: every row
	// past the client's own height was dropped and the pane came back with its
	// bottom blank. An editor came back without the last line of the file or
	// its status line, which is the shape this was reported in, and it stayed
	// that way: the alternate screen keeps no scrollback to recover those rows
	// from, and the guest does not redraw a size it was never told changed.
	//
	// Grown to fit and never shrunk. How much room a pane has is the client's
	// layout to decide and it resizes this emulator on the next pass either
	// way, so growing is transient; dropping the content is not.
	if state.Width > t.Width() || state.Height > t.Height() {
		t.Resize(max(state.Width, t.Width()), max(state.Height, t.Height()))
	}

	// Sending ESC[?1049h instead would clear the buffer it is switching to.
	//
	// Applied in both directions. Only entering was applied, so an emulator
	// that survived a workspace switch and whose guest had quit its full-screen
	// program while the pane was hidden stayed pointed at the alternate buffer,
	// and the shell's screen was blitted into the wrong one.
	t.RestoreAltScreenMode(state.IsAltScreen)

	// Modes come after the screen switch so the map lands on top of it. They
	// are what apps like vim and htop need to receive mouse events at all, and
	// the guest set them once, long out of the output buffer's reach.
	if len(state.Modes) > 0 {
		t.RestoreModes(state.Modes)
	}

	// Kitty keyboard flags travel outside the DEC mode map and are set once by
	// the guest (CSI > u / CSI = u), so like the modes above they cannot be
	// recovered from the bounded output buffer. Without this a reattached
	// client encodes keys in legacy form for a pane that negotiated the
	// protocol.
	t.RestoreKittyKeyboardState(state.KittyKbdStack)
	// Each screen has its own stack, and the one above is the alternate
	// screen's while a program runs there. Without the main one a client
	// that attached under the program left the shell's flags behind when it
	// quit.
	t.RestoreKittyKeyboardMainStack(state.KittyKbdMainStack)
	// modifyOtherKeys is the other way a guest asks for keys the legacy
	// encoding cannot tell apart (vim sends CSI > 4 ; 2 m once at start). It
	// is applied when it is off too: an emulator that survived a workspace
	// switch still holds the level of an editor that quit while the pane was
	// hidden, and Ctrl+C then reached the shell as CSI 27 ; 5 ; 99 ~. An older
	// daemon reports nothing, and the level the emulator has stays.
	if state.ModifyOtherKeysKnown || state.ModifyOtherKeys > 0 {
		t.RestoreModifyOtherKeys(state.ModifyOtherKeys)
	}

	// The rendition the guest left in force, which paints everything that
	// arrives after this snapshot. Without it the stream resuming on top of a
	// restore was written in whatever colour this emulator happened to be left
	// in: default on a pane rebuilt from nothing, and stale on one that
	// survived. That is corruption on new output rather than on restored
	// content, which is why it looked random.
	if state.Pen != nil {
		t.RestoreCursorPen(styleFromWire(t, *state.Pen))
	}
	if len(state.Margins) == 4 {
		t.RestoreScrollRegion(uv.Rect(state.Margins[0], state.Margins[1], state.Margins[2], state.Margins[3]))
	} else {
		// No margins on the wire means the guest set none, so this emulator
		// scrolls its whole screen. Left alone, a pane that had margins before
		// the route would keep them after it.
		t.ResetScrollRegion()
	}
	if len(state.Charsets) == 6 {
		ids := [4]byte{
			byte(state.Charsets[0]), byte(state.Charsets[1]),
			byte(state.Charsets[2]), byte(state.Charsets[3]),
		}
		t.RestoreCharsets(ids, state.Charsets[4], state.Charsets[5])
	}
	if state.CursorShape > 0 {
		t.RestoreCursorStyle(decscusrStyle(state.CursorShape))
	}

	// The scrollback goes back first, and it is the main screen's either way:
	// the alternate screen keeps none, and both sides read the same buffer.
	// Seeding it is what makes a pane's history survive a route that builds it
	// on a new emulator.
	//
	// A pane whose emulator survived keeps the history it already holds and is
	// only handed the lines that scrolled off while it was away. The daemon
	// sends a bounded window of its scrollback and a client keeps far more than
	// that, so replacing the whole buffer would cut a long history down to the
	// size of the window on every workspace switch.
	//
	// skip is how many of the rows sent are already held: none on an empty
	// emulator, and otherwise all but the last missing ones.
	skip := 0
	if have := t.ScrollbackLen(); have > 0 {
		missing := state.ScrollbackLen - have
		skip = packedRowCount(state.PackedScrollback) - max(missing, 0)
	}
	if err := walkPacked(state.PackedScrollback, len(styles), func(y int, row []packedCell) error {
		if y >= skip {
			// A line of its own for every row: the ghostty terminal keeps what
			// it is pushed until its next flush.
			t.PushScrollbackLine(packedLine(row, styles, make(uv.Line, len(row))))
		}
		return nil
	}); err != nil {
		debugLog("[CLIENT] snapshot scrollback dropped: %v", err)
	}

	// The alternate screen is restored the same way as the normal one. It used
	// to be skipped, on the grounds that a resize would make vim or htop
	// repaint itself, which asks the guest to do the client's job: a program
	// that does not redraw on SIGWINCH, or one that is between frames, leaves
	// the pane blank.
	//
	// grid writes one packed screen through set and reports whether it held
	// any rows. The emulators copy what SetCell is handed, so one line serves
	// every row.
	var line uv.Line
	grid := func(blob []byte, set func(x, y int, c *uv.Cell)) bool {
		rows := false
		err := walkPacked(blob, len(styles), func(y int, row []packedCell) error {
			rows = true
			if y >= state.Height {
				return nil
			}
			if cap(line) < len(row) {
				line = make(uv.Line, len(row))
			}
			line = packedLine(row, styles, line[:len(row)])
			for x := 0; x < len(line) && x < state.Width; x++ {
				// A wide rune's continuation column is empty and is written by
				// SetCell from the lead cell's width, so skipping it is right.
				if line[x].Content == "" {
					continue
				}
				set(x, y, &line[x])
			}
			return nil
		})
		if err != nil {
			debugLog("[CLIENT] snapshot cells dropped: %v", err)
		}
		return rows
	}
	if grid(state.PackedScreen, t.SetCell) {
		// The cursor was serialized and thrown away. Whatever came next was
		// written from wherever this client's emulator happened to be left,
		// which on a pane rebuilt from nothing is the top left corner.
		t.RestoreCursorPosition(state.CursorX, state.CursorY)
	}

	// The shell's screen under a running full-screen program. Quitting the
	// program reveals it, and the client had never been sent it: a pane where
	// vim was open across a switch came back correct and went blank the moment
	// vim exited, because the buffer underneath had nothing in it.
	grid(state.PackedMain, t.SetMainCell)

	// The soft-wrap flags, last, because writing cells does not touch them.
	// A surviving emulator's rows held flags for what they showed before, and
	// left in place they joined a row the snapshot ends with a newline to the
	// row under it, which made one link out of two. The history flags cover
	// every row sent, the ones this emulator already held included, which is
	// what lines them up with the end of its history.
	t.RestoreSoftWraps(
		wrapFlags(state.ScreenWraps, state.Height),
		wrapFlags(state.ScrollbackWraps, packedRowCount(state.PackedScrollback)),
	)
	t.RestorePads(
		wrapFlags(state.ScreenPads, state.Height),
		wrapFlags(state.ScrollbackPads, packedRowCount(state.PackedScrollback)),
	)
}

// wireStyle is one entry of a snapshot's style table, resolved for the
// emulator the snapshot is being applied to.
type wireStyle struct {
	style uv.Style
	link  uv.Link
}

// resolveStyles resolves a snapshot's style table for t.
func resolveStyles(t vt.Terminal, styles []StyleState) []wireStyle {
	out := make([]wireStyle, len(styles))
	for i, ss := range styles {
		out[i].style, out[i].link = styleFromWire(t, ss)
	}
	return out
}

// packedLine fills line, which is len(row) long, with one packed row.
func packedLine(row []packedCell, styles []wireStyle, line uv.Line) uv.Line {
	for x, pc := range row {
		ws := &styles[pc.style]
		line[x] = uv.Cell{Content: pc.content, Width: pc.width, Style: ws.style, Link: ws.link}
	}
	return line
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

// CaptureContentResolved is CaptureContent with styling on, plus the SGR index
// colours rewritten to 24-bit RGB against the given palette. The daemon still
// knows nothing about themes: the palette is an explicit parameter, the
// client's theme palette, so the capture matches what the client paints. A
// bare reset stays a bare reset. Indices 0-15 come from the palette, the
// standard 256-colour cube and grey ramp resolve through their fixed
// formulas, and only true colour passes through untouched.
func (p *PTY) CaptureContentResolved(scrollback bool, palette [16]color.Color) string {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return ResolveSGR(vt.StripKittyPlaceholders(p.captureContent(scrollback, true)), palette)
}

// paneMeta is what a capture reports about a pane beside its content.
type paneMeta struct {
	// HistoryRows is how many scrollback lines the pane holds above its
	// screen: the lines a "recent" capture has before the screen.
	HistoryRows int
	// Revision changes whenever the pane's content can have changed: output
	// reached its emulator, or the emulator was resized. It only grows, and
	// it counts from 0 again when the daemon restarts. Two captures with one
	// revision have the same content.
	Revision int64
}

// metaLocked is the pane's paneMeta. Callers hold terminalMu.
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
func (p *PTY) CaptureContentMeta(scrollback, ansi bool) (string, paneMeta) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.captureContent(scrollback, ansi), p.metaLocked()
}

// CaptureContentResolvedMeta is CaptureContentResolved plus the pane's
// paneMeta, read under the same lock.
func (p *PTY) CaptureContentResolvedMeta(scrollback bool, palette [16]color.Color) (string, paneMeta) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return ResolveSGR(p.captureContent(scrollback, true), palette), p.metaLocked()
}

// captureContent is the shared core of the two capture paths. Callers hold
// terminalMu.
func (p *PTY) captureContent(scrollback, ansi bool) string {
	// A sixel image's cells are markers in the grid; a capture gets blanks.
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

// scrollbackTexter is an emulator that writes its scrollback as plain text
// without decoding every line into cells. The pure Go emulator is one. The
// plain capture uses it when it is there and reads line by line otherwise, and
// the two give the same bytes.
type scrollbackTexter interface {
	AppendScrollbackText(*strings.Builder)
}

// captureState identifies what the emulator has been given: the stream
// position it has applied and the size it has. Output and resizes are the
// only things that change a pane's content, and each moves one of these, so a
// capture taken at one captureState reads the same as any later capture at an
// equal one.
type captureState struct {
	seq           int64
	width, height int
}

// captureStateLocked is the pane's captureState. Callers hold terminalMu.
func (p *PTY) captureStateLocked() captureState {
	st := captureState{seq: p.vtSeq}
	if p.terminal != nil {
		st.width, st.height = p.terminal.Width(), p.terminal.Height()
	}
	return st
}

// currentCaptureState is the pane's captureState now.
func (p *PTY) currentCaptureState() captureState {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.captureStateLocked()
}

// capturePlainAt is CaptureContent without styling, plus the captureState the
// capture was taken at, read under the same lock.
func (p *PTY) capturePlainAt(scrollback bool) (string, captureState) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.captureContent(scrollback, false), p.captureStateLocked()
}

// decscusrParam encodes a cursor shape as the DECSCUSR parameter a guest would
// have sent to ask for it, which is how it travels. Never zero: zero on the
// wire means the snapshot did not carry a shape.
func decscusrParam(style vt.CursorStyle, steady bool) int {
	n := int(style)*2 + 1
	if steady {
		n++
	}
	return n
}

// decscusrStyle is the inverse, for a parameter a snapshot actually carried.
func decscusrStyle(n int) (vt.CursorStyle, bool) {
	return vt.CursorStyle((n - 1) / 2), n%2 == 0
}

// TerminalState represents the serializable state of a terminal.
type TerminalState struct {
	// Seq is the stream position this snapshot was taken at: the emulator here
	// has consumed exactly the first Seq bytes the pane ever produced. A client
	// restoring this state subscribes from Seq, so it receives what came after
	// the snapshot and not what the snapshot already shows.
	Seq           int64       `json:"seq,omitempty"`
	Width         int         `json:"width"`
	Height        int         `json:"height"`
	CursorX       int         `json:"cursor_x"`
	CursorY       int         `json:"cursor_y"`
	ScrollbackLen int         `json:"scrollback_len"`
	IsAltScreen   bool        `json:"is_alt_screen,omitempty"` // Alternate screen buffer active (for mouse event forwarding)
	Pen           *StyleState `json:"pen,omitempty"`           // Graphic rendition in force: what the guest's next output is painted with
	// Margins is the scroll region, as x, y, width, height. A guest sets it
	// once to hold a header or a status line out of the scrolling part of the
	// screen.
	Margins []int `json:"margins,omitempty"`
	// Charsets names the character set selected into each of G0 to G3 by its
	// designator byte, followed by the GL and GR slots. A program that draws
	// boxes selects the DEC line-drawing set once and then sends the box
	// characters as plain letters.
	Charsets []int `json:"charsets,omitempty"`
	// CursorShape is the shape the guest asked for, as the DECSCUSR parameter
	// that asks for it: 1 blinking block, 2 steady block, and so on up to 6 for
	// a steady bar. The guest's own spelling rather than a pair of fields
	// because zero then means "this snapshot does not say", which is what a
	// client restoring from an older daemon gets, and it leaves the pane on the
	// default instead of forcing a blinking block onto it.
	CursorShape   int          `json:"cursor_shape,omitempty"`
	Modes         map[int]bool `json:"modes,omitempty"`           // Terminal modes (mouse tracking, bracketed paste, etc.)
	KittyKbdStack []int        `json:"kitty_kbd_stack,omitempty"` // Kitty keyboard protocol flag stack, base entry first
	// ModifyOtherKeys is the xterm modifyOtherKeys level the guest set with
	// XTMODKEYS, 0 to 2. ModifyOtherKeysKnown says the daemon reported it:
	// a level of 0 is then the guest turning it off, which a client emulator
	// that kept an older level has to take, and not an older daemon that
	// says nothing.
	// KittyKbdMainStack is the main screen's kitty keyboard flag stack,
	// carried only while the alternate screen is active, when KittyKbdStack
	// is the alternate screen's.
	KittyKbdMainStack    []int         `json:"kitty_kbd_main_stack,omitempty"`
	ModifyOtherKeys      int           `json:"modify_other_keys,omitempty"`
	ModifyOtherKeysKnown bool          `json:"modify_other_keys_known,omitempty"`
	Screen               [][]CellState `json:"screen"`
	Scrollback           [][]CellState `json:"scrollback,omitempty"`
	// MainScreen is the normal screen, carried only while the alternate one is
	// active. It is the shell's screen underneath a full-screen program, which
	// quitting that program puts back on display. The alternate screen needs no
	// such treatment: entering it clears it, so what it held before is never
	// seen again.
	MainScreen [][]CellState `json:"main_screen,omitempty"`

	// The three grids above in packed form, sent instead of them when the
	// request asked (GetTerminalStatePayload.Packed). Styles is the table the
	// packed cells index, and is never empty when the cells are packed. See
	// snapshot_pack.go for the layout and why it exists.
	Styles           []StyleState `json:"styles,omitempty"`
	PackedScreen     []byte       `json:"packed_screen,omitempty"`
	PackedScrollback []byte       `json:"packed_scrollback,omitempty"`
	PackedMain       []byte       `json:"packed_main,omitempty"`

	// ScreenWraps and ScrollbackWraps are the soft-wrap flags, one bit per
	// row (bit i of byte i/8, low bit first): the active screen's rows, and
	// the scrollback rows this snapshot carries, in the order it carries
	// them. A set bit says the row carries on to the next one because the
	// emulator wrapped it, where a row that is merely full ended with a
	// newline. A peer from before these fields sends neither, which reads as
	// no row wrapped: the safe answer, since a wrongly joined row glues two
	// lines into one link. See vt.Terminal.RestoreSoftWraps.
	ScreenWraps     []byte `json:"screen_wraps,omitempty"`
	ScrollbackWraps []byte `json:"scrollback_wraps,omitempty"`

	// ScreenPads and ScrollbackPads are, in the same layout, the rows that
	// wrapped a column early because a double-width character did not fit
	// in the last column, so that column is padding, not a typed space. A
	// reflow drops it when it joins the row to the next; without the flag
	// it keeps it, and the client and the daemon then lay the line out
	// apart. A peer from before these fields sends neither, which reads as
	// no padding: the line keeps a blank, and no text is lost.
	ScreenPads     []byte `json:"screen_pads,omitempty"`
	ScrollbackPads []byte `json:"scrollback_pads,omitempty"`
}

// wrapBits packs soft-wrap flags into the wire's bitset, or nil when none is
// set.
func wrapBits(flags []bool) []byte {
	var out []byte
	for i, f := range flags {
		if !f {
			continue
		}
		if out == nil {
			out = make([]byte, (len(flags)+7)/8)
		}
		out[i/8] |= 1 << (i % 8)
	}
	return out
}

// wrapFlags unpacks n flags from the wire's bitset. Bits past the end of the
// set read as false.
func wrapFlags(bits []byte, n int) []bool {
	out := make([]bool, max(n, 0))
	for i := range out {
		if i/8 < len(bits) {
			out[i] = bits[i/8]&(1<<(i%8)) != 0
		}
	}
	return out
}

// CellState represents a single terminal cell with full styling information.
//
// The attributes travel as the emulator's own bitmask rather than as a bool per
// attribute. Spelling them out one at a time is what left blink, conceal and
// strikethrough off the wire entirely, and collapsed the five underline styles
// into one.
type CellState struct {
	Content string `json:"c,omitempty"` // Cell content (character or grapheme)
	Width   int    `json:"w,omitempty"` // Cell width (1 for normal, 2 for wide chars, 0 for continuation)
	StyleState
}

// StyleState is a graphic rendition on the wire: how something is painted,
// separate from what it holds. It describes both a cell and the pen, which are
// the same rendition seen at two moments.
type StyleState struct {
	FgColor    string `json:"fg,omitempty"` // Foreground color, encoded by colorToWire
	BgColor    string `json:"bg,omitempty"` // Background color
	UlColor    string `json:"uc,omitempty"` // Underline color (SGR 58)
	Attrs      uint8  `json:"a,omitempty"`  // uv.Attr* bitmask: bold, faint, italic, blink, reverse, conceal, strikethrough
	Underline  uint8  `json:"u,omitempty"`  // ansi.Underline style: none, single, double, curly, dotted, dashed
	LinkURL    string `json:"l,omitempty"`  // OSC 8 hyperlink target
	LinkParams string `json:"lp,omitempty"` // OSC 8 hyperlink parameters
}

// styleToWire encodes a graphic rendition and the hyperlink that travels with it.
func styleToWire(s uv.Style, link uv.Link) StyleState {
	return StyleState{
		FgColor:    colorToWire(s.Fg),
		BgColor:    colorToWire(s.Bg),
		UlColor:    colorToWire(s.UnderlineColor),
		Attrs:      s.Attrs,
		Underline:  uint8(s.Underline),
		LinkURL:    link.URL,
		LinkParams: link.Params,
	}
}

// styleFromWire is styleToWire read back into the emulator that will hold it.
func styleFromWire(t vt.Terminal, ss StyleState) (uv.Style, uv.Link) {
	// The two values are named rather than returned as inline composite
	// literals, because gofmt 1.26 and 1.27 indent that construct differently
	// and each rejects the other's output. Naming them keeps the file
	// formatted the same way under both toolchains.
	style := uv.Style{
		Fg:             colorFromWire(t, ss.FgColor),
		Bg:             colorFromWire(t, ss.BgColor),
		UnderlineColor: colorFromWire(t, ss.UlColor),
		Underline:      ansi.Underline(ss.Underline),
		Attrs:          ss.Attrs,
	}
	link := uv.Link{
		URL:    ss.LinkURL,
		Params: ss.LinkParams,
	}
	return style, link
}

// colorToWire encodes a cell color so the client gets back the kind of color the
// guest asked for, not merely the shade it resolves to.
//
// A palette entry follows the user's terminal theme and the RGB it happens to
// resolve to does not. Flattening every color to hex meant a pane came back
// repainted in whichever shades the default palette gives: `31m` red became a
// fixed maroon, and a theme's own red was gone. That is the whole of the
// "colours randomly changing after a switch" report, and it was invisible to a
// comparison that read both sides through RGBA().
func colorToWire(c color.Color) string {
	switch v := c.(type) {
	case nil:
		return ""
	case ansi.BasicColor:
		if int(v) < len(basicColorWire) {
			return basicColorWire[v]
		}
		return "a" + strconv.Itoa(int(v))
	case ansi.IndexedColor:
		return indexedColorWire[v]
	}
	// A color.Color interface can hold a typed-nil pointer such as
	// (*color.RGBA)(nil). The case nil above matches only an untyped nil, and
	// RGBA has a value receiver, so reading it through a wrapped nil panics the
	// daemon. Encode it as the absence of color, the same as an untyped nil.
	if rv := reflect.ValueOf(c); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return ""
	}
	r, g, b, _ := c.RGBA()
	const digits = "0123456789abcdef"
	buf := [7]byte{'#'}
	for i, v := range [3]byte{byte(r >> 8), byte(g >> 8), byte(b >> 8)} {
		buf[1+2*i] = digits[v>>4]
		buf[2+2*i] = digits[v&0xf]
	}
	return string(buf[:])
}

// basicColorWire and indexedColorWire are every palette encoding colorToWire
// can produce, built once. A snapshot encodes a colour per cell, so building
// the string each time was one allocation per styled cell, per pane, per
// workspace switch.
var (
	basicColorWire   = paletteWire('a', 16)
	indexedColorWire = paletteWire('i', 256)
)

func paletteWire(prefix byte, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(prefix) + strconv.Itoa(i)
	}
	return out
}

// colorFromWire is colorToWire read back. Palette entries are resolved through
// the emulator that will hold them, so a restored cell is colored by the same
// rule as a cell the guest writes live into that emulator.
func colorFromWire(t vt.Terminal, s string) color.Color {
	if s == "" {
		return nil
	}
	if s[0] == '#' {
		// Read by hand rather than through fmt.Sscanf: this runs once per
		// coloured cell on the client's UI goroutine, and Sscanf was over
		// two microseconds of reflection per call, which put a truecolor
		// pane at twenty milliseconds per workspace switch.
		if len(s) != 7 {
			return nil
		}
		var rgb [3]byte
		for i := range rgb {
			hi, lo := hexNibble(s[1+2*i]), hexNibble(s[2+2*i])
			if hi < 0 || lo < 0 {
				return nil
			}
			rgb[i] = byte(hi<<4 | lo)
		}
		return color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 0xff}
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil {
		return nil
	}
	switch s[0] {
	case 'a':
		return t.PaletteColor(n)
	case 'i':
		return t.IndexedColor(n)
	}
	return nil
}

// hexNibble is the value of one hex digit, or -1 for anything else.
func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// colorWireCache remembers the wire form of each RGB colour it has encoded.
// Palette colours have their strings built once for the process
// (basicColorWire, indexedColorWire); an RGB string is built per distinct
// colour per snapshot instead of per cell.
type colorWireCache map[color.RGBA]string

func (c colorWireCache) encode(col color.Color) string {
	rgba, ok := col.(color.RGBA)
	if !ok {
		return colorToWire(col)
	}
	if s, ok := c[rgba]; ok {
		return s
	}
	s := colorToWire(col)
	c[rgba] = s
	return s
}

// cellState is CellStateOf through the cache.
func (c colorWireCache) cellState(cell *uv.Cell) CellState {
	return CellState{
		Content: cell.Content,
		Width:   cell.Width,
		StyleState: StyleState{
			FgColor:    c.encode(cell.Style.Fg),
			BgColor:    c.encode(cell.Style.Bg),
			UlColor:    c.encode(cell.Style.UnderlineColor),
			Attrs:      cell.Style.Attrs,
			Underline:  uint8(cell.Style.Underline),
			LinkURL:    cell.Link.URL,
			LinkParams: cell.Link.Params,
		},
	}
}

// CellStateOf converts a VT cell to a serializable CellState.
func CellStateOf(cell *uv.Cell) CellState {
	if cell == nil {
		return CellState{}
	}

	return CellState{
		Content:    cell.Content,
		Width:      cell.Width,
		StyleState: styleToWire(cell.Style, cell.Link),
	}
}

// Close terminates the PTY.
func (p *PTY) Close() error {
	p.cancel()

	// Before anything else, so a settle scan already armed cannot fire against a
	// pane whose emulator is about to be closed.
	p.stopScreenSettle()

	// Close all subscriber channels
	p.subscribersMu.Lock()
	for id, sub := range p.subscribers {
		close(sub.ch)
		delete(p.subscribers, id)
	}
	p.subscriberCount.Store(0)
	p.subscribersMu.Unlock()

	// Kill process
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}

	// Mark the VT emulator closed so forwardTerminalResponses returns EOF on
	// its next read. (A read already blocked in the response pipe is only
	// unblocked once Emulator.Close CloseWrites the pipe.)
	if p.terminal != nil {
		_ = p.terminal.Close()
	}

	// No held window size may be written once the descriptor is closed.
	p.closeWinsize()

	// The pane's emulator and scrollback are garbage now. A burst of closes
	// is one trim, once the daemon settles. See memtrim.
	memtrim.Request()

	// Close PTY. This unblocks readOutput's pending Read, which then closes
	// vtWriteChan so vtWriter exits.
	if p.pty != nil {
		return p.pty.Close()
	}
	return nil
}

// ProcessCwd returns the current working directory of the PTY's shell process.
// The second return is false when it cannot be determined (process gone, or an
// unsupported platform). Used to capture cwd for session resurrection.
func (p *PTY) ProcessCwd() (string, bool) {
	// A pane on another machine has no process here to read. The machine
	// running it is the only one that can say where it is, so the answer is
	// the one that machine last gave, and asking for a fresher one is started
	// here rather than waited for: this is called from GetState, which is on
	// the render path.
	if rp, ok := p.pty.(*remotePane); ok {
		return rp.Cwd()
	}
	if p.cmd == nil || p.cmd.Process == nil {
		return "", false
	}
	return ptyspawn.ProcessCwd(p.cmd.Process.Pid)
}

// ShellPID returns the process id of the PTY's shell child, or 0 when the process
// is not running. It is the anchor the agent auto-detector uses to resolve the
// pane's foreground process group.
func (p *PTY) ShellPID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
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
func (p *PTY) ExitStatus() (int, bool) {
	p.exitedMu.RLock()
	defer p.exitedMu.RUnlock()
	return p.exitCode, p.exited
}

func (p *PTY) readOutput() {
	// Closing vtWriteChan lets vtWriter's range terminate when the read loop
	// exits. Resize sends on it too, so the close is taken under the lock it
	// sends under and leaves the flag that stops it trying.
	defer func() {
		p.streamMu.Lock()
		p.vtClosed = true
		close(p.vtWriteChan)
		p.streamMu.Unlock()
		p.rawLog.Close()
	}()

	buf := make([]byte, 16*1024) // 16KB: matches typical PTY pipe buffer
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		// A pane streaming graphics waits here while a client is behind,
		// so the program waits in write() instead of the client's stream
		// being cut. See holdForSlowSubscribers.
		p.holdForSlowSubscribers()

		n, err := p.pty.Read(buf)
		if err != nil {
			// A pane on another machine ends here. Its stream stopping is the
			// only notice that crosses: the far side closes the connection
			// when the process exits, when the owner hangs up and when the
			// link drops, and none of those leaves anything here to wait on.
			// Without this the window stayed open around a pane that had
			// nothing behind it, because monitorExit had returned at once.
			if p.host != "" {
				p.noteExit(0)
			}
			return
		}

		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			// The raw stream, when TUIOS_PTY_LOG asks for it. Taken here,
			// before anything reads or reorders it, so what lands in the file
			// is what the program wrote.
			p.rawLog.Write(data)

			// A tuios client whose output lands in this pane announces
			// itself with a probe. See nest_probe.go.
			p.scanNestProbes(data)

			// Held across all three steps so a resize taken under the same
			// lock cannot land between two of them: the daemon's emulator and
			// every subscriber must change width at the same byte.
			p.streamMu.Lock()

			// Store in ring buffer for reconnection
			p.outputMu.Lock()
			seq := p.appendToBuffer(data)
			p.outputMu.Unlock()

			// Broadcast to subscribers
			p.broadcast(ptyChunk{data: data}, seq)

			// VT emulator: feed via a dedicated single goroutine to
			// avoid unbounded goroutine growth at high FPS.
			//
			// This send blocks rather than dropping. Falling behind is fine;
			// skipping a chunk is not. Every route a client takes into a pane
			// rehydrates it from this emulator, so a chunk dropped here is
			// output that no client will ever see again: the screen it is
			// handed is missing the cursor moves and modes the rest of the
			// stream is written against. Blocking applies backpressure to the
			// shell, which is what a terminal does when a program outputs
			// faster than the terminal can take it. vtWriter only ever holds
			// the leaf terminal lock and never waits on this loop, so there is
			// nothing here to deadlock against.
			select {
			case p.vtWriteChan <- vtChunk{data: data, seq: seq}:
			case <-p.ctx.Done():
				p.streamMu.Unlock()
				return
			}
			p.streamMu.Unlock()
			p.reportPacing()

			// Record the activity time for the agent-state stall heuristic before
			// anything that can block, so a demotion decision is made against when
			// output actually arrived.
			p.lastOutput.Store(time.Now().UnixNano())

			// Raise a control-plane output-activity event. This is a lightweight
			// signal (byte count only, no content) that drives wait-for-output and
			// window-idle waits and lets a subscriber know the pane is active; the
			// raw bytes still flow only through the binary subscriber stream.
			if p.emit != nil {
				p.emit(SessionEvent{Type: EventOutput, Bytes: n})
			}
		}
	}
}

// vtChunk is one chunk of PTY output and the stream position it ends at, or a
// resize to apply between the chunks either side of it.
type vtChunk struct {
	data          []byte
	seq           int64
	width, height int // both > 0 marks a resize rather than output
}

// vtWriter is a single persistent goroutine that feeds the daemon's VT
// emulator. Using a dedicated goroutine (instead of spawning one per PTY
// read) prevents unbounded goroutine growth at high FPS.
func (p *PTY) vtWriter() {
	for chunk := range p.vtWriteChan {
		if chunk.width > 0 && chunk.height > 0 {
			// The pane's size and its emulator move together, so a snapshot
			// can never report a width the grid it serializes is not at.
			//
			// Skipped at the size the emulator already has, exactly as the
			// client skips it (applyStreamResize): a same-size resize resets
			// the scroll region and the tab stops, and one side doing that
			// while the other declines is a divergence the guest never asked
			// for.
			p.terminalMu.Lock()
			if p.terminal != nil && (p.terminal.Width() != chunk.width || p.terminal.Height() != chunk.height) {
				p.terminal.Resize(chunk.width, chunk.height)
				p.vtResizes++
			}
			p.terminalMu.Unlock()
			continue
		}
		p.terminalMu.Lock()
		if p.terminal != nil {
			_, _ = p.terminal.Write(chunk.data)
		}
		// Recorded under the same lock the emulator is written and read under,
		// so a state snapshot and the position it was taken at can never
		// disagree. That pairing is what lets a client be resumed exactly where
		// the snapshot it was handed ends.
		p.vtSeq = chunk.seq
		focusOn := p.terminal != nil && p.terminal.FocusReportingEnabled()
		p.terminalMu.Unlock()
		p.noteFocusReporting(focusOn)
	}
}

// appendToBuffer records a chunk in the catch-up buffer and returns the stream
// position it ends at.
func (p *PTY) appendToBuffer(data []byte) int64 {
	p.unbroadcast.Add(int64(len(data)))
	p.outputSeq += int64(len(data))
	// Marks the ring has rolled past stop being split points, but the newest
	// of them is still the width the ring's first byte was laid out at, so a
	// catch-up can start a rolled client at it.
	bufStart := p.outputSeq - int64(len(p.outputBuffer))
	for len(p.resizeMarks) > 1 && p.resizeMarks[1].seq <= bufStart {
		p.resizeMarks = p.resizeMarks[1:]
	}
	bufLen := len(p.outputBuffer)
	// If data is bigger than the buffer, keep only the tail
	if len(data) >= bufLen {
		copy(p.outputBuffer, data[len(data)-bufLen:])
		p.outputPos = bufLen
		return p.outputSeq
	}
	// Shift in half-buffer steps until there is room. A single half-shift is
	// not always enough when len(data) exceeds bufLen/2, so loop until the
	// remaining space fits or the buffer is empty.
	for bufLen-p.outputPos < len(data) && p.outputPos > 0 {
		half := min(bufLen/2, p.outputPos)
		copy(p.outputBuffer, p.outputBuffer[half:p.outputPos])
		p.outputPos -= half
	}
	// Advance by bytes actually copied so outputPos can never exceed bufLen.
	n := copy(p.outputBuffer[p.outputPos:], data)
	p.outputPos += n
	return p.outputSeq
}

// broadcast hands a chunk ending at stream position seq to every subscriber.
// Output is cut into text and kitty graphics frames on the way, so a client
// that is behind can skip frames (kitty_frames.go).
func (p *PTY) broadcast(chunk ptyChunk, seq int64) {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()

	if p.debug {
		debugLog("[DEBUG] PTY %s: BROADCAST called with %d bytes, %d subscribers", p.ID[:8], len(chunk.data), len(p.subscribers))
	}
	if chunk.isResize() {
		// A resize carries no bytes, so there is no position for it to be
		// behind and nothing to skip it against: it goes to every
		// subscriber. A frame a client is gathering ends before it, so the
		// frame goes first. A resize leaves sent where the last bytes put
		// it; it used to store its zero, and a client that switched away
		// after a resize came back to the whole ring painted over its screen.
		for clientID, sub := range p.subscribers {
			if sub.gapped.Load() {
				continue
			}
			if sub.open != nil {
				p.flushOpen(clientID, sub)
			}
			select {
			case sub.ch <- chunk:
			default:
				sub.gapped.Store(true)
				sub.forgetFrames()
			}
		}
		return
	}

	// Cut even with nobody subscribed, so the scanner never loses its place
	// in the stream.
	p.reads++
	p.unbroadcast.Add(-int64(len(chunk.data)))
	segs, saw := p.gfx.scan(chunk.data, seq)
	p.gfx.pruneSpans(seq - int64(len(p.outputBuffer)))
	if saw {
		p.graphicsAt.Store(time.Now().UnixNano())
	}
	// A chunk appended between a subscriber's catch-up being copied and this
	// broadcast running is in both, because Subscribe blocks the broadcast
	// rather than the append. route skips what a subscriber has seen:
	// delivering it again paints it twice at the seam, which is one
	// duplicated line every time a pane is shown while it is producing. A
	// gapped stream takes nothing more until it is rebuilt from the ring:
	// queuing past the hole would paint the rest of the stream on top of it.
	for clientID, sub := range p.subscribers {
		p.route(clientID, sub, segs)
	}
}

func (p *PTY) monitorExit() {
	if p.cmd == nil {
		// A pane on another machine. There is no process here to wait on, so
		// its exit is heard where its bytes stop instead: see readOutput.
		return
	}

	_ = p.cmd.Wait()

	code := 0
	if p.cmd.ProcessState != nil {
		code = p.cmd.ProcessState.ExitCode()
	}
	p.noteExit(code)
}

// noteExit marks the process gone and tells everything that was waiting on it.
// It is idempotent: a pane on another machine can reach it from the read loop
// and from a close at the same time.
//
// The two callers differ only in what they know. A local pane arrives with the
// exit status the kernel gave it; a pane on another machine arrives with the
// fact that its stream ended, which is all that crosses. That is why the code
// is a parameter rather than read from the command here.
func (p *PTY) noteExit(code int) {
	p.exitedMu.Lock()
	if p.exited {
		p.exitedMu.Unlock()
		return
	}
	p.exited = true
	p.exitCode = code
	p.exitedMu.Unlock()

	debugLog("[DEBUG] PTY %s: process exited with code %d", p.ID[:8], p.exitCode)

	// Notify callback (used by daemon to inform clients)
	if p.onExit != nil {
		p.onExit(p.ID)
	}

	// Raise a control-plane window-exit event so wait-for window-exit resolves.
	if p.emit != nil {
		p.emit(SessionEvent{Type: EventWindowExit})
	}
}

// forwardTerminalResponses reads responses from the daemon's terminal emulator and
// forwards them to the PTY as input for applications to receive.
// The emulator writes responses (like DA1, CPR) to its pipe. If nothing reads from the pipe,
// Write() will block forever (io.Pipe is synchronous).
// Client emulators DRAIN their responses to prevent duplicates.
func (p *PTY) forwardTerminalResponses() {
	if p.terminal == nil {
		return
	}

	buf := make([]byte, 4096)
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
			n, err := p.terminal.Read(buf)
			if err != nil {
				return
			}
			if n > 0 && p.pty != nil {
				// Forward response to PTY as input
				_, _ = p.pty.Write(buf[:n])
			}
		}
	}
}

// isGlobal reports whether this session is a global one. See
// SessionState.Global.
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
