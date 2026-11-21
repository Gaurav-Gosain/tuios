package app

import (
	"errors"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	hostSignInWatch = 2 * time.Minute
)

type hostRetryMsg struct {
	Host    string
	URL     string
	Refused bool
	Err     error
}

func (m *OS) hostWaitsForSignIn(host string) bool {
	return host != "" && m.hostStatusByName(host) == string(federation.StatusApproval)
}

func (m *OS) hostSignIn(host string) (string, bool) {
	for _, h := range m.FederationHosts {
		if h.Name == host {
			return h.ApprovalURL, h.ApprovalRefused
		}
	}
	return "", false
}

func signInOpeningNote(host, rawURL string) string {
	domain := "the Tailscale sign-in page"
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		domain = sanitizeLinkText(u.Hostname())
	}
	return "Opening " + domain + " to sign in to " + printableTitle(host) + "."
}

func signInRefusedNote(host string) string {
	return "The sign-in link from " + printableTitle(host) + " is not a Tailscale address, so tuios did not open it."
}

func (m *OS) openHostSignIn(host string) tea.Cmd {
	if !m.hostWaitsForSignIn(host) {
		return nil
	}
	rawURL, refused := m.hostSignIn(host)
	if refused {
		m.ShowNotification(signInRefusedNote(host), "warning", m.Settings.NotificationDuration*2)
		return nil
	}
	now := time.Now()
	m.hostSignInUntil = now.Add(hostSignInWatch)
	retry := retryHostCmd(host)
	if rawURL != "" {
		return tea.Batch(retry, m.openSignInPage(host, rawURL))
	}
	if m.hostSignInPending == nil {
		m.hostSignInPending = map[string]time.Time{}
	}
	m.hostSignInPending[host] = now.Add(hostSignInWatch)
	m.ShowNotification("Opening the Tailscale sign-in page for "+printableTitle(host)+".", "info", m.Settings.NotificationDuration)
	return retry
}

func (m *OS) askHostSignIn(host string) {
	if m.hostSignInPending == nil {
		m.hostSignInPending = map[string]time.Time{}
	}
	m.hostSignInPending[host] = time.Now().Add(hostSignInWatch)
}

func (m *OS) openSignInPage(host, rawURL string) tea.Cmd {
	if !linkTextClean(rawURL) || !strings.HasPrefix(rawURL, "https://") {
		m.ShowNotification(signInRefusedNote(host), "warning", m.Settings.NotificationDuration*2)
		return nil
	}
	showURL := func() tea.Cmd {
		m.ShowNotification("Open "+rawURL+" to sign in to Tailscale for "+printableTitle(host)+". The address is on your clipboard.",
			"info", m.Settings.NotificationDuration*3)
		return tea.SetClipboard(rawURL)
	}
	if m.IsRemoteClient() {
		return showURL()
	}
	argv, err := linkOpenerArgv(m.Settings.LinkOpener, rawURL)
	if errors.Is(err, errNoDesktop) {
		return showURL()
	}
	if err != nil {
		m.LogError("Could not read the link opener: %v", err)
		return showURL()
	}
	watch, err := startLinkOpener(argv, rawURL)
	if err != nil {
		m.LogError("Failed to open the sign-in page with %s: %v", argv[0], err)
		return showURL()
	}
	m.ShowNotification(signInOpeningNote(host, rawURL), "info", m.Settings.NotificationDuration)
	return watch
}

func (m *OS) takePendingSignIns() tea.Cmd {
	if len(m.hostSignInPending) == 0 {
		return nil
	}
	now := time.Now()
	var cmds []tea.Cmd
	for host, until := range m.hostSignInPending {
		if now.After(until) || !m.hostWaitsForSignIn(host) {
			delete(m.hostSignInPending, host)
			continue
		}
		rawURL, refused := m.hostSignIn(host)
		switch {
		case refused:
			delete(m.hostSignInPending, host)
			m.ShowNotification(signInRefusedNote(host), "warning", m.Settings.NotificationDuration*2)
		case rawURL != "":
			delete(m.hostSignInPending, host)
			cmds = append(cmds, m.openSignInPage(host, rawURL))
		}
	}
	return tea.Batch(cmds...)
}

func (m *OS) applyHostRetry(msg hostRetryMsg) tea.Cmd {
	if msg.Err != nil {
		m.LogError("retry-host %s: %v", msg.Host, msg.Err)
		return nil
	}
	if _, waiting := m.hostSignInPending[msg.Host]; !waiting {
		return nil
	}
	switch {
	case msg.Refused:
		delete(m.hostSignInPending, msg.Host)
		m.ShowNotification(signInRefusedNote(msg.Host), "warning", m.Settings.NotificationDuration*2)
	case msg.URL != "":
		delete(m.hostSignInPending, msg.Host)
		return m.openSignInPage(msg.Host, msg.URL)
	}
	return nil
}

func (m *OS) hostSignInWatching() bool {
	return time.Now().Before(m.hostSignInUntil)
}

func retryHostCmd(host string) tea.Cmd {
	return func() tea.Msg {
		client, err := session.DialVerbClient()
		if err != nil {
			return hostRetryMsg{Host: host, Err: err}
		}
		defer func() { _ = client.Close() }()
		raw, err := client.Call("retry-host", map[string]any{"host": host})
		if err != nil {
			return hostRetryMsg{Host: host, Err: err}
		}
		var res struct {
			ApprovalURL     string `json:"approval_url"`
			ApprovalRefused bool   `json:"approval_refused"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return hostRetryMsg{Host: host, Err: errfmt.Errorf("read the retry-host answer: %w", err)}
		}
		return hostRetryMsg{Host: host, URL: res.ApprovalURL, Refused: res.ApprovalRefused}
	}
}
