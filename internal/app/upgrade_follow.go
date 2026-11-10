package app

import (
	"path/filepath"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A client follows a live upgrade of its daemon.
//
// A live upgrade (tuios daemon upgrade, see session/handoff.go) replaces the
// daemon's image and keeps every pane. The client waits for the new image
// and takes the session back (daemon_reconnect.go), and it went on running
// its own build: the person had to quit and start tuios again to get the
// client's half of the upgrade.
//
// Now the daemon names the binary it was upgraded to as it drops the client
// (MsgDaemonUpgrading). Once the new image answers, a local client whose
// build is older stops instead of taking the session back, and the entry
// point runs that binary as `tuios attach --after-upgrade=VERSION SESSION`,
// with the interface flags this client was started with. On Unix it is an
// exec: the same process and the same terminal. The new client attaches to
// the same session, and the panes never stopped.
//
// It is for a client on the daemon's machine only, as a restart from the app
// is (DaemonRestartAvailable): a client over ssh or in a browser runs for
// someone who is not at this machine, and a session on another host has its
// daemon there. Only an entry point that can run the new build turns it on
// (OSOptions.FollowUpgrades), so the GUI bridge keeps its build.
// daemon.client_follows_upgrade turns it off.
//
// Two rules keep it from going round. A build that is not newer than this
// one is not followed, when both are release versions: an upgrade to an
// older release keeps the newer client. And a client started to follow an
// upgrade to a version does not follow to that version again, so a binary
// that says one version and reports another cannot exec itself for ever.

// UpgradeExec is how the entry point brings the client onto the build a
// live upgrade gave the daemon. Set only with ExitUpgradeFollowed.
type UpgradeExec struct {
	Binary  string
	Session string
	// Version is the build the upgrade named, for the new client's
	// --after-upgrade.
	Version string
}

// upgradeToFollow is the exec that puts this client on the daemon's new
// build, or nil when the client keeps its own.
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

// upgradeIsNewer reports whether build to is one this client, of build own,
// should move to. The same build is not. Two release versions are ordered.
// A source build ("dev+abc123") cannot be ordered, and the daemon was put on
// it on purpose, so a build that differs is followed.
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

// followUpgrade ends this client for the entry point to run the new build.
// client is the connection to the new image, which is not used.
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

// announceUpgradeFollowed says, on the first frame of a client started to
// follow an upgrade, that the window runs the daemon's build now.
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
