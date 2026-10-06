package app

import (
	"encoding/json"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A machine behind Tailscale SSH in check mode waits for the person to sign in
// in a browser before ssh goes on. The daemon's link holds that ssh open and
// reports the sign-in page (federation.StatusApproval, with ApprovalURL). This
// file is the rail's half: a click or Enter on the machine's header opens the
// page, and the link comes up on its own once the person has signed in.
//
// The link's own dial is the wait. When the person signs in, the same ssh goes
// on and the link comes up with no new dial. A dial that Tailscale ended has
// no page to open, so the header asks the daemon to dial again (retry-host)
// and opens the page the new dial reports.

const (
	// hostSignInWatch is how long the rail polls at the active cadence after
	// the person opened a sign-in page, and how long it waits for a page it
	// asked for to arrive. It matches the daemon's quick-redial window.
	hostSignInWatch = 2 * time.Minute

	signInOpeningNote = "Opening the Tailscale sign-in page."
)

// hostRetryMsg is the daemon's answer to retry-host.
type hostRetryMsg struct {
	Host string
	URL  string
	Err  error
}

// hostWaitsForSignIn reports whether a machine's link waits for a Tailscale
// sign-in.
func (m *OS) hostWaitsForSignIn(host string) bool {
	return host != "" && m.hostStatusByName(host) == string(federation.StatusApproval)
}

// hostSignInURL is the sign-in page the last snapshot holds for a machine, or
// "".
func (m *OS) hostSignInURL(host string) string {
	for _, h := range m.FederationHosts {
		if h.Name == host {
			return h.ApprovalURL
		}
	}
	return ""
}

// activateHostHeader is a click or Enter on a machine's header. A machine
// that waits for a sign-in opens the sign-in page, because its rows are a
// cached listing and the sign-in is the one thing a person can do for it.
// Any other machine folds or opens its group.
func (m *OS) activateHostHeader(host string) {
	if m.hostWaitsForSignIn(host) {
		m.queueSidebarCmd(m.openHostSignIn(host))
		return
	}
	m.SidebarToggleHostCollapsed(host)
}

// openHostSignIn opens the sign-in page of a machine, or asks the daemon for
// one when the snapshot has none. Either way the daemon's link redials
// quickly for a while, and the rail polls at the active cadence, so the
// machine comes up on the rail soon after the person signs in.
func (m *OS) openHostSignIn(host string) tea.Cmd {
	now := time.Now()
	m.hostSignInUntil = now.Add(hostSignInWatch)
	retry := retryHostCmd(host)
	if url := m.hostSignInURL(host); url != "" {
		return tea.Batch(retry, m.openSignInPage(url))
	}
	if m.hostSignInPending == nil {
		m.hostSignInPending = map[string]time.Time{}
	}
	m.hostSignInPending[host] = now.Add(hostSignInWatch)
	m.ShowNotification(signInOpeningNote, "info", m.Settings.NotificationDuration)
	return retry
}

// openSignInPage opens a sign-in page in the person's browser. A client that
// cannot start a browser on the person's machine (an ssh or web client, or a
// machine with no desktop) shows the address in a notice and puts it on the
// clipboard instead.
func (m *OS) openSignInPage(url string) tea.Cmd {
	if !linkTextClean(url) || !linkOpenableScheme(url) {
		return nil
	}
	showURL := func() tea.Cmd {
		m.ShowNotification("Open "+url+" to sign in to Tailscale. The address is on your clipboard.",
			"info", m.Settings.NotificationDuration*3)
		return tea.SetClipboard(url)
	}
	if m.IsRemoteClient() {
		return showURL()
	}
	argv, err := linkOpenerArgv(m.Settings.LinkOpener, url)
	if errors.Is(err, errNoDesktop) {
		return showURL()
	}
	if err != nil {
		m.LogError("Could not read the link opener: %v", err)
		return showURL()
	}
	watch, err := startLinkOpener(argv, url)
	if err != nil {
		m.LogError("Failed to open the sign-in page with %s: %v", argv[0], err)
		return showURL()
	}
	m.ShowNotification(signInOpeningNote, "info", m.Settings.NotificationDuration)
	return watch
}

// takePendingSignIns opens the sign-in page of every machine the person asked
// for while the snapshot had none, now that the snapshot has one. A request
// older than hostSignInWatch is dropped.
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
		if url := m.hostSignInURL(host); url != "" {
			delete(m.hostSignInPending, host)
			cmds = append(cmds, m.openSignInPage(url))
		}
	}
	return tea.Batch(cmds...)
}

// applyHostRetry handles the daemon's answer to retry-host.
func (m *OS) applyHostRetry(msg hostRetryMsg) tea.Cmd {
	if msg.Err != nil {
		m.LogError("retry-host %s: %v", msg.Host, msg.Err)
		return nil
	}
	if _, waiting := m.hostSignInPending[msg.Host]; waiting && msg.URL != "" {
		delete(m.hostSignInPending, msg.Host)
		return m.openSignInPage(msg.URL)
	}
	return nil
}

// hostSignInWatching reports whether a sign-in page was opened recently, which
// keeps the host poll at the active cadence.
func (m *OS) hostSignInWatching() bool {
	return time.Now().Before(m.hostSignInUntil)
}

// retryHostCmd asks the daemon to dial a machine again now, off the Update
// goroutine.
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
			ApprovalURL string `json:"approval_url"`
		}
		_ = json.Unmarshal(raw, &res)
		return hostRetryMsg{Host: host, URL: res.ApprovalURL}
	}
}
