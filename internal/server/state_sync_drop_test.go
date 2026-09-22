package server

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestStateSyncFloodLeavesTheClientOnTheNewestSnapshot pushes more state updates
// than the sync channel holds while the receiving client's Update loop is busy.
//
// Each message is a whole snapshot, so losing an intermediate costs nothing, but
// losing the last one leaves that client rendering a view the daemon abandoned,
// with nothing to correct it until the session next changes. That is the "the
// other client doesn't show the new pane" report.
func TestStateSyncFloodLeavesTheClientOnTheNewestSnapshot(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Setenv("SHELL", "/bin/sh")

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	const name = "syncflood"

	// The client under test. Its Update loop never runs, which is what a client
	// busy with a repaint or a round trip looks like from the read loop.
	viewer := session.NewTUIClient()
	if err := viewer.Connect("test", 80, 24); err != nil {
		t.Fatalf("viewer connect: %v", err)
	}
	t.Cleanup(func() { _ = viewer.Close() })
	if _, err := viewer.AttachSession(name, true, 80, 24); err != nil {
		t.Fatalf("viewer attach: %v", err)
	}
	m := app.NewOS(app.OSOptions{
		UserConfig:      config.DefaultConfig(),
		IsDaemonSession: true,
		DaemonClient:    viewer,
		SessionName:     viewer.SessionName(),
		Width:           80,
		Height:          24,
	})
	m.WireDaemonClient(viewer)
	// The state-sync hook, the same as WireDaemonClient installs, and one more
	// thing: it notes when the newest snapshot has reached this client. That is
	// the barrier the drain below waits on.
	//
	// It used to be a round trip on this client's own connection, on the
	// grounds that the reply comes back only after every sync sent before it.
	// That holds for syncs already on this connection, and the daemon writes
	// each client's broadcasts from a goroutine of its own, so the last
	// broadcast can still be on its way when the reply overtakes it. Under the
	// race detector it did, and the test reported a dropped snapshot that was
	// only late.
	//
	// Noting the arrival here rather than draining early is what keeps the test
	// able to fail: the channel is left full while the snapshots land, so a
	// queue that threw the newest away instead of the oldest would still be
	// caught.
	newest := 0.30 + float64(19)*0.01
	var sawNewest atomic.Bool
	viewer.OnStateSync(func(state *session.SessionState, triggerType, sourceID string) {
		m.QueueStateSync(app.StateSyncMsg{State: state, TriggerType: triggerType, SourceID: sourceID})
		if state != nil && state.MasterRatio == newest {
			sawNewest.Store(true)
		}
	})
	viewer.StartReadLoop()

	// The other client, making the changes this one has to hear about.
	author := session.NewTUIClient()
	if err := author.Connect("test", 80, 24); err != nil {
		t.Fatalf("author connect: %v", err)
	}
	t.Cleanup(func() { _ = author.Close() })
	state, err := author.AttachSession(name, false, 80, 24)
	if err != nil {
		t.Fatalf("author attach: %v", err)
	}
	author.StartReadLoop()
	if state == nil {
		t.Fatalf("author attach returned no state")
	}

	// More updates than the channel holds, each one distinguishable, spaced so
	// the daemon's per-client broadcast goroutines cannot reorder them.
	const pushes = 20
	want := make([]float64, 0, pushes)
	for i := range pushes {
		ratio := 0.30 + float64(i)*0.01
		state.MasterRatio = ratio
		want = append(want, ratio)
		if err := author.UpdateState(state); err != nil {
			t.Fatalf("push state %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pushes <= cap(m.StateSyncChan) {
		t.Fatalf("vacuous: %d pushes fit in a channel of %d", pushes, cap(m.StateSyncChan))
	}

	if want[len(want)-1] != newest {
		t.Fatalf("setup: the barrier waits for %.2f but the last push is %.2f", newest, want[len(want)-1])
	}
	// Wait for the newest snapshot to reach this client. See the hook above.
	deadline := time.Now().Add(15 * time.Second)
	for !sawNewest.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("the newest snapshot never reached the viewer")
		}
		time.Sleep(5 * time.Millisecond)
	}

	got := make([]float64, 0, pushes)
drain:
	for {
		select {
		case s := <-m.StateSyncChan:
			got = append(got, s.State.MasterRatio)
		default:
			break drain
		}
	}
	if len(got) == 0 {
		t.Fatalf("the viewer was told nothing")
	}
	// Precondition: this test means nothing if the daemon delivered the updates
	// out of order, because then "the newest" was never the last to arrive.
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("vacuous: syncs arrived out of order (%v)", got)
		}
	}

	if last := got[len(got)-1]; last != newest {
		t.Fatalf("the viewer is stuck on master ratio %.2f while the daemon holds %.2f: %d of %d syncs were dropped and nothing asks for them again",
			last, newest, pushes-len(got), pushes)
	}
}
