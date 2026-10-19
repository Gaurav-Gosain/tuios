package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The rail's worktree dialog: a branch name asked over a repository, and a
// session made in a new worktree of it.
//
// Everything the dialog offers, the daemon already does: the new-worktree verb
// is the same one `tuios worktree new` runs, and the CLI is where the caller
// was before this dialog existed. What the dialog adds is the reach — a
// right-click on a session row or a repository's group header — and the
// session name the verb derives, shown in the empty name field so the user
// can see what enter will do. The name is a pure function of the repository
// and the branch, so the client can derive it exactly.
//
// The folder is neither asked for nor previewed. The verb takes no path; it
// puts the worktree under its own worktree.DefaultDir, read in the daemon's
// environment, which need not be this client's (TUIOS_WORKTREE_DIR set in one
// and not the other, on the same machine, is enough). A preview derived here
// would be a guess, so the folder is said once, from the verb's reply, in the
// notification that reports the create.

// worktreePromptState is the open dialog. The zero value is no dialog, the
// way filePromptState's is: an open dialog always has a repository, so an
// empty RepoRoot is a closed one, which WorktreePromptOpen reports.
type worktreePromptState struct {
	// RepoRoot is the main checkout the worktree branches from. It is captured
	// when the dialog opens, for the same reason filePromptState keeps its dir:
	// the cached listing can be replaced by a reply already in flight, and the
	// create must act on the repository the user was looking at.
	RepoRoot string
	// Branch is the typed branch.
	Branch string
	// Name is the typed session name, empty for the derived one. Tab moves
	// between this and the branch field.
	Name string
	// Field is the field the keyboard is in.
	Field worktreeField
	// Err is the last refusal, drawn under the field so a second try can see
	// what was wrong with the first.
	Err string
}

// worktreeField names one of the dialog's two fields.
type worktreeField int

// The two fields, as the tab key cycles them. worktreeFieldCount is the
// count the tab key cycles over.
const (
	worktreeFieldBranch worktreeField = iota
	worktreeFieldName
	worktreeFieldCount
)

// WorktreePromptOpen reports whether the worktree dialog owns the keyboard.
func (m *OS) WorktreePromptOpen() bool { return m.worktreePrompt.RepoRoot != "" }

// WorktreePromptRepoRoot is the repository the open dialog branches from, or
// "" when it is closed.
func (m *OS) WorktreePromptRepoRoot() string { return m.worktreePrompt.RepoRoot }

// WorktreePromptBranch is the branch as typed, trimmed of the spaces the field
// would hand the daemon, or "" when the dialog is closed.
func (m *OS) WorktreePromptBranch() string { return strings.TrimSpace(m.worktreePrompt.Branch) }

// WorktreePromptSessionName is the typed session name, trimmed, or "" when the
// dialog is closed or the field is, which is how the verb reads "derive it".
func (m *OS) WorktreePromptSessionName() string { return strings.TrimSpace(m.worktreePrompt.Name) }

// BeginWorktreePromptForSession opens the dialog on the repository a session's
// worktree record names. A session with no record — an ordinary session, whose
// full directory the listing does not carry — is refused with a word, which is
// what the dimmed menu row says too: this release offers the dialog only where
// the repository is known. The refusal says what is missing and not "not a
// worktree", because a plain session can be in a repository's main checkout;
// what the rail lacks is the path to it.
func (m *OS) BeginWorktreePromptForSession(name string) {
	info := m.sessionWorktreeInfo(name)
	if info == nil {
		m.ShowNotification("The rail does not know which repository this session is in.", "info", m.Settings.NotificationDuration)
		return
	}
	m.openWorktreePrompt(info.RepoRoot)
}

// BeginWorktreePromptForRepo opens the dialog on a repository's group header.
// The header names the repository; the main checkout comes from any member
// session's worktree record, and there is one while the group is on screen at
// all.
func (m *OS) BeginWorktreePromptForRepo(repo string) {
	root := m.worktreeRepoRoot(repo)
	if root == "" {
		m.ShowNotification("The rail has no session in this repository.", "info", m.Settings.NotificationDuration)
		return
	}
	m.openWorktreePrompt(root)
}

// openWorktreePrompt captures the repository and opens on an empty branch.
func (m *OS) openWorktreePrompt(repoRoot string) {
	m.worktreePrompt = worktreePromptState{RepoRoot: repoRoot}
}

// sessionWorktreeInfo is the worktree record the cached listing carries for a
// session, or nil. Reading the cache and not the socket is the rule every
// other rail surface keeps: this runs where a round trip would freeze the
// client.
func (m *OS) sessionWorktreeInfo(name string) *session.WorktreeInfo {
	if m.DaemonClient == nil || name == "" {
		return nil
	}
	return m.DaemonClient.SessionWorktree(name)
}

// worktreeRepoRoot finds the main checkout a repository's group header stands
// for. Empty without a daemon, or when no cached session names the repository,
// which for a row on screen means the listing changed since it was drawn.
func (m *OS) worktreeRepoRoot(repo string) string {
	if m.DaemonClient == nil {
		return ""
	}
	return m.DaemonClient.RepoRoot(repo)
}

// WorktreePromptNextField moves the keyboard between the branch and the
// session name, cycling, the way the confirmation dialogs cycle their rows.
func (m *OS) WorktreePromptNextField() {
	if !m.WorktreePromptOpen() {
		return
	}
	m.worktreePrompt.Field = (m.worktreePrompt.Field + 1) % worktreeFieldCount
}

// worktreeFieldBuffer is a field's buffer, or nil when the dialog is closed.
// It is the one read on "which buffer is this field", so drawing, typing,
// backspace and clear all agree.
func (m *OS) worktreeFieldBuffer(field worktreeField) *string {
	if !m.WorktreePromptOpen() {
		return nil
	}
	if field == worktreeFieldName {
		return &m.worktreePrompt.Name
	}
	return &m.worktreePrompt.Branch
}

// worktreeActiveField is the buffer the keyboard is in, or nil when the dialog
// is closed.
func (m *OS) worktreeActiveField() *string {
	return m.worktreeFieldBuffer(m.worktreePrompt.Field)
}

// worktreeFieldText is a field's text as typed, or "" when the dialog is
// closed.
func (m *OS) worktreeFieldText(field worktreeField) string {
	if buf := m.worktreeFieldBuffer(field); buf != nil {
		return *buf
	}
	return ""
}

// WorktreePromptType adds typed text to the field the keyboard is in, laundered
// the way every name in this codebase is: only what the dialog will actually
// draw survives. A branch may hold a slash, which is why there is no
// file-prompt-style "trailing / means folder" rule here to fight.
func (m *OS) WorktreePromptType(text string) {
	if text == "" {
		return
	}
	if buf := m.worktreeActiveField(); buf != nil {
		*buf += printableRunes(text)
		m.worktreePrompt.Err = ""
	}
}

// WorktreePromptBackspace drops the last rune of the active field, in runes,
// for the same reason RenameBackspace counts in runes.
func (m *OS) WorktreePromptBackspace() {
	if buf := m.worktreeActiveField(); buf != nil && *buf != "" {
		*buf = dropLastRune(*buf)
		m.worktreePrompt.Err = ""
	}
}

// WorktreePromptClearInput empties the active field.
func (m *OS) WorktreePromptClearInput() {
	if buf := m.worktreeActiveField(); buf != nil {
		*buf = ""
	}
}

// WorktreePromptCancel closes the dialog without running anything.
func (m *OS) WorktreePromptCancel() { m.worktreePrompt = worktreePromptState{} }

// worktreePromptRepo is the repository's name as the dialog shows it: the base
// of the main checkout, which is what the rail's group header says too.
func (m *OS) worktreePromptRepo() string {
	if !m.WorktreePromptOpen() {
		return ""
	}
	return filepath.Base(m.worktreePrompt.RepoRoot)
}

// WorktreePromptSubmit checks the branch and hands the create to the daemon,
// in a command, because a create is a round trip that can take a clone's worth
// of minutes and this runs on the Update goroutine. The dialog closes when the
// work is handed off; the worktreeCreatedMsg handler says what became of it.
//
// The two checks here are the ones the user can fix without a round trip,
// and both are pure, so the Update goroutine never runs git: an empty branch,
// and a session name the daemon would refuse anyway — said here so the dialog
// is still open to show it. The daemon owns the real refusals — a taken
// branch, a taken session name — and those come back as a notification.
func (m *OS) WorktreePromptSubmit() tea.Cmd {
	if !m.WorktreePromptOpen() {
		return nil
	}
	branch := m.WorktreePromptBranch()
	if branch == "" {
		m.worktreePrompt.Err = "Type a branch name."
		return nil
	}
	sessionName := m.WorktreePromptSessionName()
	if sessionName != "" {
		if err := session.ValidateSessionName(sessionName); err != nil {
			m.worktreePrompt.Err = err.Error()
			return nil
		}
	}
	repoRoot := m.worktreePrompt.RepoRoot
	m.WorktreePromptCancel()
	// Acknowledge the hand-off at once: the dialog is closed, and checking out
	// a large repository can take a while, so without this the enter reads as
	// lost until the worktreeCreatedMsg comes back.
	m.ShowNotification("Creating worktree '"+branch+"'...", "info", m.Settings.NotificationDuration)
	return m.createWorktreeCmd(repoRoot, branch, sessionName)
}

// createWorktreeCmd runs the daemon's new-worktree verb, the same one
// `tuios worktree new` runs. This dialog never clones (it sends no repo_url
// and no host), but a checkout of a large tree is not instant, so the bound is
// the CLI's generous one rather than an ordinary verb's.
//
// The params are the repository, the branch, and the session name when the
// user typed one: an empty name is how the verb says "derive it".
func (m *OS) createWorktreeCmd(repoRoot, branch, sessionName string) tea.Cmd {
	dial := m.verbDialer()
	params := map[string]any{"repo": repoRoot, "branch": branch}
	if sessionName != "" {
		params["name"] = sessionName
	}
	return func() tea.Msg {
		c, err := dial()
		if err != nil {
			return worktreeCreatedMsg{Err: fmt.Errorf("failed to reach the daemon: %w", err)}
		}
		defer func() { _ = c.Close() }()

		raw, err := c.CallWithTimeout("new-worktree", params, 5*time.Minute)
		if err != nil {
			return worktreeCreatedMsg{Err: fmt.Errorf("failed to create the worktree: %w", err)}
		}
		var res struct {
			Session string `json:"session"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return worktreeCreatedMsg{Err: fmt.Errorf("failed to read the daemon's reply: %w", err)}
		}
		return worktreeCreatedMsg{Session: res.Session, Path: res.Path}
	}
}

// worktreeCreatedMsg is a finished create on its way back to the loop.
type worktreeCreatedMsg struct {
	// Session is the session made in the new worktree, empty on failure.
	Session string
	// Path is the worktree's directory as the daemon made it: the only
	// honest answer to where it went, since the daemon's environment chose it.
	Path string
	// Err is what went wrong, nil on success.
	Err error
}

// handleWorktreeCreated says what became of a create: attach to the session it
// made, the way the CLI attaches at once, and say so; or say what failed. The
// switch touches the daemon client and the model, which is why it lives here
// and not in the command.
func (m *OS) handleWorktreeCreated(msg worktreeCreatedMsg) tea.Cmd {
	if msg.Err != nil {
		m.ShowNotification(msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	if msg.Session == "" {
		return nil
	}
	note := "Created worktree session '" + msg.Session + "'"
	if msg.Path != "" {
		note += " in " + shortenHome(msg.Path)
	}
	m.ShowNotification(note+", attaching.", "info", m.Settings.NotificationDuration)
	if err := m.SwitchToSession(msg.Session); err != nil {
		m.ShowNotification("Could not attach to "+msg.Session+": "+err.Error(), "warning", m.Settings.NotificationDuration*2)
	} else {
		// The daemon already opened the worktree's first pane, but nobody
		// has arranged it. Apply [startup] just as other session creates do.
		m.applyStartupToUnarranged()
	}
	// The listing the rail reads is refreshed off this goroutine; the new
	// session joins its repository's group when it lands.
	return m.refreshListingCmd()
}
