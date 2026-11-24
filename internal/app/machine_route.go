package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Which machine a thing is on, and which daemon to call about it.
//
// Every listing a client holds (its windows, the Inbox, the board's cards,
// the rail's rows) comes from the daemon of the session it is attached to. So
// an empty host on a row or an item means that machine: this one, or the host
// the client is attached through. A non-empty host names another machine, as
// attachedMachine names machines, and federation.LocalHostName names this
// one. itemMachine reads a host that way, and machineDialer reaches the
// daemon of the machine it returns. A value that already names a machine,
// such as NotifTarget.Host, is passed on as it is: it must not be mapped back
// to "", which would then read as the attached machine.

// itemMachine is the machine a row or an item with host is on, as
// attachedMachine names it.
func (m *OS) itemMachine(host string) string {
	if host == "" {
		return m.attachedMachine()
	}
	return host
}

// machineDialer opens a verb connection to machine's daemon. It is read on the
// UI goroutine and called off it.
func (m *OS) machineDialer(machine string) inboxDial {
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() (*session.VerbClient, error) { return dialMachine(machine, build) }
}

// attachedDialer opens a verb connection to the daemon that holds the attached
// session.
func (m *OS) attachedDialer() inboxDial { return m.machineDialer(m.attachedMachine()) }

// dialMachine opens a verb connection to machine's daemon: this machine's for
// "" or federation.LocalHostName, else machine's through the link, where that
// machine's link policy for this one applies. build is the version the
// connection announces.
func dialMachine(machine, build string) (*session.VerbClient, error) {
	if machine == "" || machine == federation.LocalHostName {
		return dialVerbLocal(build)
	}
	c, _, err := dialVerbThroughHost(machine, build)
	return c, err
}

// dialVerbThroughHost and dialVerbLocal are the two ways dialMachine reaches a
// daemon, as variables so a test can see which one a command used.
var (
	dialVerbThroughHost = session.DialVerbClientThroughHost
	dialVerbLocal       = session.DialVerbClientAs
)
