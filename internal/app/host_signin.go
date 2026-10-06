package app

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A machine behind Tailscale SSH in check mode waits for the person to sign in
// in a browser before ssh goes on. The daemon's link holds that ssh open and
// reports the sign-in page (federation.StatusApproval, with ApprovalURL). This
// file is the rail's half: the "sign in" label on the machine's header is a
// control, and a click or Enter on it opens the page. The header itself still
// folds the group. The link comes up on its own once the person has signed in.
//
// The link's own dial is the wait. When the person signs in, the same ssh goes
// on and the link comes up with no new dial. A dial that Tailscale ended has
// no page to open, so the label asks the daemon to dial again (retry-host) and
// opens the page the new dial reports.
//
// The page comes from the banner on ssh's stderr, which anything on the far
// machine can print. The daemon keeps only an https address on a Tailscale
// login origin, or on the host's tailscale_login (federation.SignInURLAllowed),
// and reports any other as refused. The rail opens nothing for a refused one
// and says so.

const (
	// hostSignInWatch is how long the rail polls at the active cadence after
	// the person opened a sign-in page, and how long it waits for a page it
	// asked for to arrive. It matches the daemon's quick-redial window.
	hostSignInWatch = 2 * time.Minute
)

// hostRetryMsg is the daemon's answer to retry-host.
type hostRetryMsg struct {
	Host    string
	URL     string
	Refused bool
	Err     error
}

// hostWaitsForSignIn reports whether a machine's link waits for a Tailscale
// sign-in.
func (m *OS) hostWaitsForSignIn(host string) bool {
	return host != "" && m.hostStatusByName(host) == string(federation.StatusApproval)
}

// hostSignIn is the sign-in page the last snapshot holds for a machine, or "",
// and whether the daemon refused the one the banner named.
func (m *OS) hostSignIn(host string) (string, bool) {
	for _, h := range m.FederationHosts {
		if h.Name == host {
			return h.ApprovalURL, h.ApprovalRefused
		}
	}
	return "", false
}

// signInOpeningNote says where a sign-in goes: the domain of the page first,
// so the person can see it is Tailscale's, and the machine. It is short so
// the dock does not cut the domain off.
func signInOpeningNote(host, rawURL string) string {
	domain := "the Tailscale sign-in page"
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		domain = sanitizeLinkText(u.Hostname())
	}
	return "Opening " + domain + " to sign in to " + printableTitle(host) + "."
}

// signInRefusedNote is the warning for a sign-in link the daemon refused.
func signInRefusedNote(host string) string {
	return "The sign-in link from " + printableTitle(host) + " is not a Tailscale address, so tuios did not open it."
}

// openHostSignIn opens the sign-in page of a machine, or asks the daemon for
// one when the snapshot has none. Either way the daemon's link redials
// quickly for a while, and the rail polls at the active cadence, so the
// machine comes up on the rail soon after the person signs in.
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

// openSignInPage opens a sign-in page in the person's browser. A client that
// cannot start a browser on the person's machine (an ssh or web client, or a
// machine with no desktop) shows the address in a notice and puts it on the
// clipboard instead.
//
// The daemon checked the address against the login origins. It is checked for
// https again here, so a daemon of another version cannot hand the rail a
// page on this machine.
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

// takePendingSignIns opens the sign-in page of every machine the person asked
// for while the snapshot had none, now that the snapshot has one. A request
// older than hostSignInWatch is dropped. A refused page is reported and
// dropped.
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

// applyHostRetry handles the daemon's answer to retry-host.
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
			ApprovalURL     string `json:"approval_url"`
			ApprovalRefused bool   `json:"approval_refused"`
		}
		_ = json.Unmarshal(raw, &res)
		return hostRetryMsg{Host: host, URL: res.ApprovalURL, Refused: res.ApprovalRefused}
	}
}
