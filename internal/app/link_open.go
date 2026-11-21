package app

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/anmitsu/go-shlex"

	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func linkFilePath(rawURL string) (string, bool) {
	if !strings.HasPrefix(rawURL, "file://") {
		return "", false
	}
	return localCwdPath(rawURL)
}

// OpenLink performs the default action for a link.
//
// It never fails silently. Every branch that cannot do the obvious thing says
// what it did instead, because a click that appears to do nothing is worse than
// one that says it copied an address.
func (m *OS) OpenLink(rawURL string) tea.Cmd {
	if rawURL == "" {
		return nil
	}
	if !linkTextClean(rawURL) {
		m.ShowNotification("tuios did not open the link. Its address has control characters or spaces.",
			"warning", m.Settings.NotificationDuration)
		return nil
	}

	if bare, line := splitLineFragment(rawURL); line > 0 {
		if path, ok := linkFilePath(bare); ok {
			return m.openLocalPath(path, line, rawURL)
		}
	}
	if path, ok := linkFilePath(rawURL); ok {
		return m.openLocalPath(path, 0, rawURL)
	}

	if target, ok := parseTuiosLink(rawURL); ok {
		return m.openWindowLink(target, rawURL)
	}
	if strings.HasPrefix(strings.ToLower(rawURL), "tuios:") {
		m.ShowNotification("tuios did not understand the link. A window link is tuios://window/<id>, or tuios://window/<id>/close.",
			"warning", m.Settings.NotificationDuration)
		return nil
	}

	if strings.HasPrefix(strings.ToLower(rawURL), "file:") {
		m.ShowNotification("That file link names another machine. tuios did not open it. The address is on your clipboard.",
			"warning", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}

	if !linkOpenableScheme(rawURL) {
		m.ShowNotification("tuios can not open that kind of link. The address is on your clipboard.",
			"warning", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}

	if m.IsRemoteClient() {
		m.ShowNotification("Copied the link. A remote client can not open it for you.",
			"info", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	argv, err := linkOpenerArgv(m.Settings.LinkOpener, rawURL)
	if errors.Is(err, errNoDesktop) {
		m.ShowNotification("Copied the link. This machine has no desktop to open it. Use the link click of your terminal.",
			"info", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	if err != nil {
		m.LogError("Could not read the link opener: %v", err)
		m.ShowNotification("tuios can not read the link opener command. Change appearance.link_opener.",
			"error", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	watch, err := startLinkOpener(argv, rawURL)
	if err != nil {
		m.LogError("Failed to open link %s with %s: %v", rawURL, argv[0], err)
		m.ShowNotification("Could not start "+sanitizeLinkText(argv[0])+". The link is on your clipboard.",
			"error", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	m.ShowNotification("Opening "+linkHostLabel(rawURL)+".", "success", m.Settings.NotificationDuration)
	return watch
}

func frameLink(u string) bool {
	const scheme = "tuios:"
	if len(u) < len(scheme) {
		return true
	}
	return !strings.EqualFold(u[:len(scheme)], scheme)
}

type tuiosLink struct {
	id    string
	close bool
}

const tuiosLinkMinID = 8

func parseTuiosLink(rawURL string) (tuiosLink, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "tuios" || u.Host != "window" {
		return tuiosLink{}, false
	}
	rest := strings.TrimPrefix(u.Path, "/")
	if rest == "" {
		return tuiosLink{}, false
	}
	if id, found := strings.CutSuffix(rest, "/close"); found {
		if len(id) < tuiosLinkMinID {
			return tuiosLink{}, false
		}
		return tuiosLink{id: id, close: true}, true
	}
	if len(rest) < tuiosLinkMinID {
		return tuiosLink{}, false
	}
	return tuiosLink{id: rest}, true
}

func (m *OS) openWindowLink(target tuiosLink, rawURL string) tea.Cmd {
	if m.DaemonClient == nil {
		return m.followWindowLink(target, rawURL, nil)
	}
	client := m.DaemonClient
	return func() tea.Msg {
		sessions, _ := client.RefreshSessionList()
		return windowLinkListedMsg{target: target, rawURL: rawURL, sessions: sessions}
	}
}

func (m *OS) followWindowLink(target tuiosLink, rawURL string, sessions []session.SessionInfo) tea.Cmd {
	matches := m.resolveWindowLink(target.id, sessions)
	if len(matches) != 1 {
		return m.windowLinkMiss(rawURL)
	}
	if matches[0].session != "" && !m.openSession("", matches[0].session) {
		return nil
	}
	i := m.windowIndexByID(matches[0].id)
	if i < 0 {
		return m.windowLinkMiss(rawURL)
	}
	if target.close {
		m.OpenSessionCloseForWindow(i)
		return nil
	}
	m.FocusWindow(i)
	m.RevealFocusedColumn()
	return nil
}

type windowLinkListedMsg struct {
	target   tuiosLink
	rawURL   string
	sessions []session.SessionInfo
}

func (m *OS) windowLinkMiss(rawURL string) tea.Cmd {
	m.ShowNotification("The window link names no window on this machine, or names more than one. The address is on your clipboard.",
		"warning", m.Settings.NotificationDuration)
	return tea.SetClipboard(rawURL)
}

type windowLinkMatch struct{ session, id string }

func (m *OS) resolveWindowLink(id string, sessions []session.SessionInfo) []windowLinkMatch {
	var matches []windowLinkMatch
	add := func(session, wid string) {
		if strings.HasPrefix(strings.ToLower(wid), strings.ToLower(id)) {
			matches = append(matches, windowLinkMatch{session, wid})
		}
	}
	for _, w := range m.Windows {
		add("", w.ID)
	}
	for _, s := range sessions {
		if s.Name == m.SessionName {
			continue
		}
		for _, w := range s.Windows {
			add(s.Name, w.ID)
		}
	}
	return matches
}

func linkTextClean(s string) bool {
	for _, r := range s {
		if r <= 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

func linkHostLabel(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return sanitizeLinkText(u.Host)
	}
	return "the link"
}

func linkOpenableScheme(rawURL string) bool {
	return hints.OpenableScheme(rawURL)
}

func (m *OS) openLocalPath(path string, line int, rawURL string) tea.Cmd {
	info, err := os.Stat(path)
	if err != nil {
		m.ShowNotification("That file is gone. The link is on your clipboard.",
			"warning", m.Settings.NotificationDuration)
		return tea.SetClipboard(rawURL)
	}
	return m.OpenQuickLookPath(path, line, info.IsDir())
}

func (m *OS) openInEditor(path string, line int) tea.Cmd {
	argv, err := editorArgv(m.Settings.SidebarEditor)
	if err != nil {
		m.LogError("Could not read the editor command: %v", err)
		m.ShowNotification(editorCommandNote, "warning", m.Settings.NotificationDuration)
		return nil
	}
	m.AddWindow(filepath.Base(path), append(argv, editorLineArgs(argv[0], path, line)...)...)
	m.ShowNotification("Opened the file in a new pane.", "success", m.Settings.NotificationDuration)
	return nil
}

const editorCommandNote = "tuios can not read the File editor command. Change it in the sidebar settings."

func editorArgv(setting string) ([]string, error) {
	editor := strings.TrimSpace(setting)
	if editor == "" {
		editor = linkEditor()
	}
	argv, err := shlex.Split(editor, true)
	if err == nil && len(argv) == 0 {
		err = errors.New("the editor command is empty")
	}
	return argv, err
}

func linkEditor() string {
	for _, key := range []string{"EDITOR", "VISUAL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return "vi"
}
