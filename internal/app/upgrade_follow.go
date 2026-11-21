package app

import (
	"path/filepath"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// UpgradeExec is how the entry point brings the client onto the build a
// live upgrade gave the daemon. Set only with ExitUpgradeFollowed.
type UpgradeExec struct {
	Binary  string
	Session string
	// Version is the build the upgrade named, for the new client's
	// --after-upgrade.
	Version string
}

func (m *OS) upgradeToFollow() *UpgradeExec {
	if !m.followUpgrades || runtime.GOOS == "js" || !m.DaemonRestartAvailable() || m.SessionName == "" {
		return nil
	}
	if m.UserConfig != nil && !m.UserConfig.Daemon.FollowsUpgrade() {
		return nil
	}
	to, upgraded := m.DaemonClient.UpgradedTo()
	bin := m.DaemonClient.UpgradeBinary()
	if !upgraded || to == "" || bin == "" || !filepath.IsAbs(bin) || to == m.followedUpgrade {
		return nil
	}
	if !upgradeIsNewer(to, m.DaemonClient.ClientVersion()) {
		return nil
	}
	return &UpgradeExec{Binary: bin, Session: m.SessionName, Version: to}
}

func upgradeIsNewer(to, own string) bool {
	if to == own {
		return false
	}
	a, okA := release.ParseVersion(to)
	b, okB := release.ParseVersion(own)
	if okA && okB {
		return release.Compare(a, b) > 0
	}
	return true
}

func (m *OS) followUpgrade(exec *UpgradeExec, client *session.TUIClient) tea.Cmd {
	if client != nil {
		_ = client.Close()
	}
	m.daemonReconnect = nil
	m.daemonWaiting.Store(false)
	m.LogInfo("The daemon was upgraded to %s. The client runs %s on session %s.", exec.Version, exec.Binary, exec.Session)
	m.UpgradeExec = exec
	m.ExitReason = ExitUpgradeFollowed
	return m.QuitCmd()
}

func (m *OS) announceUpgradeFollowed() {
	if m.followedUpgrade == "" || m.upgradeAnnounced || m.DaemonClient == nil {
		return
	}
	m.upgradeAnnounced = true
	text := "The daemon was upgraded to " + m.followedUpgrade + ". This window runs it too."
	if c, d := m.DaemonClient.BuildMismatch(); c != "" {
		text = "The daemon was upgraded to " + d + ". This window runs " + c + ": start tuios again to use " + d + "."
	}
	m.ShowNotification(text, "success", 2*m.Settings.NotificationDuration)
}
