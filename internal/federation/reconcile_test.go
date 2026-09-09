package federation

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Hot reload of the host table, proved at the layer that owns the links.
//
// The three things the daemon promises when the [hosts] table is edited are
// tested here: a new host gets a link, a removed host loses one, and a changed
// address is dialed again. The fourth, that repeated edits leak nothing, is the
// one a green test could hide, so it is measured rather than asserted about.

// waitForStatus waits until a host reports the wanted status.
func waitForStatus(t *testing.T, m *Manager, host string, want Status) HostReport {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last HostReport
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		for _, r := range m.Reports(ctx) {
			if r.Host == host {
				last = r
			}
		}
		cancel()
		if last.Status == want {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("host %s never reached %s, last was %s (%s)", host, want, last.Status, last.Reason)
	return last
}

// hostNames is the names a manager currently holds.
func hostNames(m *Manager) []string { return m.Table().Names() }

func TestSetTableAddsAHostToARunningManager(t *testing.T) {
	stub := startStubDaemon(t, helloOK("1.2.3", 2))
	m := managerFor(t, testOptions(proxyDialer(t, stub)), Host{Name: "build", Addr: "unused"})
	waitForStatus(t, m, "build", StatusUp)

	table, problems := NewTable([]Host{
		{Name: "build", Addr: "unused"},
		{Name: "lab", Addr: "unused"},
	})
	if len(problems) > 0 {
		t.Fatalf("the table rejected an entry: %v", problems)
	}
	change := m.SetTable(table)

	if len(change.Added) != 1 || change.Added[0] != "lab" {
		t.Errorf("ASSERTION: adding a host to the table did not report it as added, got %+v", change)
	}
	if len(change.Redialed) != 0 {
		t.Errorf("ASSERTION: adding a host redialed an unrelated one: %+v", change.Redialed)
	}
	// The point of the whole feature: the new host has a link, without a
	// restart.
	r := waitForStatus(t, m, "lab", StatusUp)
	if r.Status != StatusUp {
		t.Errorf("ASSERTION: the host added at run time never came up, got %s", r.Status)
	}
	if got := strings.Join(hostNames(m), ","); got != "build,lab" {
		t.Errorf("ASSERTION: the table does not hold both hosts, got %q", got)
	}
}

func TestSetTableRemovesAHostAndClosesItsLink(t *testing.T) {
	stub := startStubDaemon(t, helloOK("1.2.3", 0))
	m := managerFor(t, testOptions(proxyDialer(t, stub)),
		Host{Name: "build", Addr: "unused"},
		Host{Name: "lab", Addr: "unused"},
	)
	waitForStatus(t, m, "build", StatusUp)
	waitForStatus(t, m, "lab", StatusUp)

	table, _ := NewTable([]Host{{Name: "build", Addr: "unused"}})
	change := m.SetTable(table)

	if len(change.Removed) != 1 || change.Removed[0] != "lab" {
		t.Errorf("ASSERTION: removing a host from the table did not report it as removed, got %+v", change)
	}
	if got := strings.Join(hostNames(m), ","); got != "build" {
		t.Errorf("ASSERTION: the removed host is still in the table, got %q", got)
	}
	// A call against the removed name is now unknown, not merely down: the link
	// is gone, and so is the name.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := m.Call(ctx, "lab", "list-sessions", nil); err == nil {
		t.Error("ASSERTION: a call to the removed host still works")
	}
	// The host that was not touched keeps the link it had.
	if r := waitForStatus(t, m, "build", StatusUp); r.Status != StatusUp {
		t.Errorf("ASSERTION: removing one host dropped another one's link, got %s", r.Status)
	}
}

func TestSetTableRedialsAChangedAddress(t *testing.T) {
	stub := startStubDaemon(t, helloOK("1.2.3", 0))
	m := managerFor(t, testOptions(proxyDialer(t, stub)), Host{Name: "build", Addr: "old"})
	first := waitForStatus(t, m, "build", StatusUp)

	table, _ := NewTable([]Host{{Name: "build", Addr: "new"}})
	change := m.SetTable(table)

	if len(change.Redialed) != 1 || change.Redialed[0] != "build" {
		t.Errorf("ASSERTION: changing an address did not redial the host, got %+v", change)
	}
	r := waitForStatus(t, m, "build", StatusUp)
	if r.Addr != "new" {
		t.Errorf("ASSERTION: the link still reports the old address %q, wanted %q", r.Addr, "new")
	}
	if first.Addr != "old" {
		t.Fatalf("the first link did not report the old address, got %q", first.Addr)
	}
}

func TestSetTableLeavesAnUnchangedHostAlone(t *testing.T) {
	stub := startStubDaemon(t, helloOK("1.2.3", 0))
	h := Host{Name: "build", Addr: "unused", Command: "tuios", ConnectTimeout: 3 * time.Second, SSHOptions: []string{"-J", "jump"}}
	m := managerFor(t, testOptions(proxyDialer(t, stub)), h)
	waitForStatus(t, m, "build", StatusUp)

	table, _ := NewTable([]Host{h})
	change := m.SetTable(table)
	if change.Changed() {
		t.Errorf("ASSERTION: a table that says the same thing moved a link, got %+v", change)
	}
}

// TestRepeatedTableEditsLeakNothing is the leak proof.
//
// It adds and removes a host a hundred times against a live stub, then counts
// goroutines. A supervisor that was cancelled but never waited for, or a link
// whose transport was left open, shows up here as a count that climbs with the
// number of edits; nothing else in the suite would notice.
func TestRepeatedTableEditsLeakNothing(t *testing.T) {
	stub := startStubDaemon(t, helloOK("1.2.3", 0))
	m := managerFor(t, testOptions(proxyDialer(t, stub)), Host{Name: "build", Addr: "unused"})
	waitForStatus(t, m, "build", StatusUp)

	const rounds = 100
	// A warm-up round first, so the baseline is taken with every one-off
	// goroutine the machinery starts already running.
	for i := range 3 {
		grow, _ := NewTable([]Host{{Name: "build", Addr: "unused"}, {Name: "lab" + strconv.Itoa(i), Addr: "unused"}})
		m.SetTable(grow)
		shrink, _ := NewTable([]Host{{Name: "build", Addr: "unused"}})
		m.SetTable(shrink)
	}
	settle()
	before := runtime.NumGoroutine()

	for i := range rounds {
		grow, _ := NewTable([]Host{{Name: "build", Addr: "unused"}, {Name: "lab" + strconv.Itoa(i), Addr: "unused"}})
		m.SetTable(grow)
		shrink, _ := NewTable([]Host{{Name: "build", Addr: "unused"}})
		m.SetTable(shrink)
	}
	settle()
	after := runtime.NumGoroutine()

	// One goroutine of slack per ten rounds would still catch a leak of one per
	// edit by a factor of ten, and it does not fail on a runtime goroutine that
	// happened to be between states when the count was taken.
	if after > before+rounds/10 {
		t.Errorf("ASSERTION: %d add-and-remove rounds left goroutines behind: %d before, %d after", rounds, before, after)
	}

	// The manager is still usable after all that, which is the other half of
	// not leaking: nothing was torn down that should have stayed.
	if r := waitForStatus(t, m, "build", StatusUp); r.Status != StatusUp {
		t.Errorf("ASSERTION: the untouched host lost its link after %d edits, got %s", rounds, r.Status)
	}
}

// settle gives cancelled supervisors a moment to be reaped before goroutines
// are counted. SetTable waits for each one it ends, so this covers the
// transport's own reaper rather than the supervisor.
func settle() {
	for range 20 {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSetTableBeforeStartOnlyChangesTheSet(t *testing.T) {
	table, _ := NewTable([]Host{{Name: "build", Addr: "unused"}})
	m := New(table, testOptions(nil))
	next, _ := NewTable([]Host{{Name: "lab", Addr: "unused"}})
	change := m.SetTable(next)
	if len(change.Added) != 1 || len(change.Removed) != 1 {
		t.Errorf("ASSERTION: a swap before Start did not report the change, got %+v", change)
	}
	if got := strings.Join(hostNames(m), ","); got != "lab" {
		t.Errorf("ASSERTION: the table was not replaced, got %q", got)
	}
}

func TestSetTableAfterStopChangesNothing(t *testing.T) {
	stub := startStubDaemon(t, helloOK("1.2.3", 0))
	table, _ := NewTable([]Host{{Name: "build", Addr: "unused"}})
	m := New(table, testOptions(proxyDialer(t, stub)))
	m.Start(context.Background())
	waitForStatus(t, m, "build", StatusUp)
	m.Stop()

	next, _ := NewTable([]Host{{Name: "build", Addr: "unused"}, {Name: "lab", Addr: "unused"}})
	if change := m.SetTable(next); change.Changed() {
		t.Errorf("ASSERTION: a stopped manager started a link, got %+v", change)
	}
}
