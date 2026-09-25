package app

import (
	"testing"
)

// TestFederationPollStopsWithNoHosts keeps the default install from paying for
// a feature it is not using. The first answer says zero hosts and no further
// poll is scheduled.
func TestFederationPollStopsWithNoHosts(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.federationPolling = true

	m.applyFederationSnapshot(FederationHostsMsg{Configured: 0})
	if _, refresh := m.federationRefreshPlan(); refresh {
		t.Error("the client keeps polling a daemon that reported no hosts")
	}
	if len(m.FederationHosts) != 0 {
		t.Errorf("hosts are stored for a daemon that has none: %+v", m.FederationHosts)
	}

	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot:   FederationSnapshot{Hosts: []FederationHost{{Name: "build", Status: "up"}}},
	})
	if _, refresh := m.federationRefreshPlan(); !refresh {
		t.Error("the client stopped polling a daemon that has a host")
	}
}

// TestAStaleFederationTickIsDropped keeps the poll to one loop. The snapshot's
// re-arm retires the tick's own re-arm, so a tick from the older generation
// must fire nothing; without the guard every period doubled the timers.
func TestAStaleFederationTickIsDropped(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.federationPolling = true
	m.SidebarCollapsed = false

	_ = m.federationRefreshTick(hostRefreshActive) // gen 1, the tick's own re-arm
	old := m.federationTickGen
	_ = m.federationRefreshTick(hostRefreshActive) // gen 2, the snapshot's re-arm

	if _, cmd := m.Update(FederationRefreshTickMsg{Gen: old}); cmd != nil {
		t.Error("ASSERTION: a tick from a retired generation still polled")
	}
	if _, cmd := m.Update(FederationRefreshTickMsg{Gen: m.federationTickGen}); cmd == nil {
		t.Error("ASSERTION: the live generation's tick did not poll")
	}
}
