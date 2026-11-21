package app

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

type worktreePromptState struct {
	RepoRoot string
	Branch   textedit.Line
	Name     textedit.Line
	Field    worktreeField
	Err      string
}

type worktreeField int

const (
	worktreeFieldBranch worktreeField = iota
	worktreeFieldName
	worktreeFieldCount
)

// WorktreePromptOpen reports whether the worktree dialog owns the keyboard.
func (m *OS) WorktreePromptOpen() bool { return m.worktreePrompt.RepoRoot != "" }

// WorktreePromptBranch is the branch as typed, trimmed of the spaces the field
// would hand the daemon, or "" when the dialog is closed.
func (m *OS) WorktreePromptBranch() string {
	return strings.TrimSpace(m.worktreePrompt.Branch.String())
}

// WorktreePromptSessionName is the typed session name, trimmed, or "" when the
// dialog is closed or the field is, which is how the verb reads "derive it".
func (m *OS) WorktreePromptSessionName() string {
	return strings.TrimSpace(m.worktreePrompt.Name.String())
}

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

// BeginWorktreePromptForFocusedPane opens the dialog on the repository the
// focused pane's directory is in: the new_worktree key, herdr's leader+G. The
// repository is found by gitstate.Locate, which reads .git entries walking up
// and never runs git, so it is cheap enough for the Update goroutine. From a
// linked worktree the answer is its main checkout, the root the verb would
// resolve too, so the dialog's repository name and derived session name match
// what the daemon makes.
//
// A pane on another machine is refused: its directory is on that machine, and
// reading it here would look at some other folder or none.
func (m *OS) BeginWorktreePromptForFocusedPane() {
	w := m.GetFocusedWindow()
	if w == nil {
		m.ShowNotification("No pane is focused.", "info", m.Settings.NotificationDuration)
		return
	}
	if !m.paneIsLocal(w) {
		m.ShowNotification("This pane is on another machine. Run tuios worktree new on that machine.", "info", m.Settings.NotificationDuration)
		return
	}
	var root string
	if m.learnFiles() {
		root = m.learn.RepoRoot(m.paneDir(w))
	} else {
		root = repoRootOf(m.paneDir(w))
	}
	if root == "" {
		m.ShowNotification("This pane is not in a git repository.", "info", m.Settings.NotificationDuration)
		return
	}
	m.openWorktreePrompt(root)
}

func repoRootOf(dir string) string {
	if dir == "" {
		return ""
	}
	_, common, top, ok := gitstate.Locate(dir)
	if !ok {
		return ""
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return top
}

func (m *OS) openWorktreePrompt(repoRoot string) {
	m.worktreePrompt = worktreePromptState{RepoRoot: repoRoot}
}

func (m *OS) sessionWorktreeInfo(name string) *session.WorktreeInfo {
	if m.DaemonClient == nil || name == "" {
		return nil
	}
	return m.DaemonClient.SessionWorktree(name)
}

func (m *OS) sessionDeletableWorktree(name string) *session.WorktreeInfo {
	info := m.sessionWorktreeInfo(name)
	if info == nil || info.Main {
		return nil
	}
	return info
}

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

func (m *OS) worktreeFieldBuffer(field worktreeField) *textedit.Line {
	if !m.WorktreePromptOpen() {
		return nil
	}
	if field == worktreeFieldName {
		return &m.worktreePrompt.Name
	}
	return &m.worktreePrompt.Branch
}

func (m *OS) worktreeActiveField() *textedit.Line {
	return m.worktreeFieldBuffer(m.worktreePrompt.Field)
}

func (m *OS) worktreeFieldText(field worktreeField) string {
	if buf := m.worktreeFieldBuffer(field); buf != nil {
		return buf.String()
	}
	return ""
}

// WorktreePromptType adds typed text to the field the keyboard is in, laundered
// the way every name in this codebase is: only what the dialog will actually
// draw survives. A branch may hold a slash, which is why there is no
// WorktreePromptType types text at the active field's caret, as a paste does.
func (m *OS) WorktreePromptType(text string) {
	if buf := m.worktreeActiveField(); buf != nil && lineInsert(buf, text, printableRunes) {
		m.worktreePrompt.Err = ""
	}
}

// WorktreePromptKey applies one key press to the active field (see lineKey).
func (m *OS) WorktreePromptKey(key, text string) {
	if buf := m.worktreeActiveField(); buf != nil {
		if changed, _ := lineKey(buf, key, text, printableRunes); changed {
			m.worktreePrompt.Err = ""
		}
	}
}

// WorktreePromptCancel closes the dialog without running anything.
func (m *OS) WorktreePromptCancel() { m.worktreePrompt = worktreePromptState{} }

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
	if m.learnFiles() {
		m.learnNewWorktree(repoRoot, branch)
		return nil
	}
	m.ShowNotification("Creating worktree '"+branch+"'...", "info", m.Settings.NotificationDuration)
	return m.createWorktreeCmd(repoRoot, branch, sessionName)
}

func (m *OS) createWorktreeCmd(repoRoot, branch, sessionName string) tea.Cmd {
	dial := m.verbDialer()
	params := map[string]any{"repo": repoRoot, "branch": branch}
	if sessionName != "" {
		params["name"] = sessionName
	}
	return func() tea.Msg {
		var res struct {
			Session   string `json:"session"`
			Path      string `json:"path"`
			Warning   string `json:"warning"`
			HookError string `json:"hook_error"`
		}
		if err := callWorktreeVerb(dial, "new-worktree", "create the worktree", params, 5*time.Minute, &res); err != nil {
			return worktreeCreatedMsg{Err: err}
		}
		return worktreeCreatedMsg{Session: res.Session, Path: res.Path, Warning: res.Warning, HookError: res.HookError}
	}
}

func callWorktreeVerb(dial agentMailDial, verb, fail string, params map[string]any, timeout time.Duration, res any) error {
	c, err := dial()
	if err != nil {
		return errfmt.Errorf("failed to reach the daemon: %w", err)
	}
	defer func() { _ = c.Close() }()
	raw, err := c.CallWithTimeout(verb, params, timeout)
	if err != nil {
		return errfmt.Errorf("failed to %s: %w", fail, err)
	}
	if err := jsonx.Unmarshal(raw, res); err != nil {
		return errfmt.Errorf("failed to read the daemon's reply: %w", err)
	}
	return nil
}

type worktreeCreatedMsg struct {
	Session   string
	Path      string
	Warning   string
	Err       error
	HookError string
}

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
	if msg.HookError != "" {
		m.ShowNotification(capitalize(msg.HookError)+". The session '"+msg.Session+"' is kept.", "warning", m.Settings.NotificationDuration*2)
	} else {
		m.ShowNotification(note+", attaching.", "info", m.Settings.NotificationDuration)
	}
	if err := m.SwitchToSession(msg.Session); err != nil {
		m.ShowNotification("Could not attach to "+msg.Session+": "+err.Error(), "warning", m.Settings.NotificationDuration*2)
	} else {
		m.applyStartupToUnarranged()
	}
	if msg.Warning != "" {
		m.ShowNotification(msg.Warning, "warning", m.Settings.NotificationDuration*2)
	}
	return m.refreshListingCmd()
}
