package server

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestStateSyncFloodLeavesTheClientOnTheNewestSnapshot pushes more state updates
// than the sync channel holds while the receiving client's Update loop is busy.
//
// Each message is a whole snapshot, so losing an intermediate costs nothing, but
// losing the last one leaves that client rendering a view the daemon abandoned,
// with nothing to correct it until the session next changes. That is the "the
// other client doesn't show the new pane" report.
func TestStateSyncFloodLeavesTheClientOnTheNewestSnapshot(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
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

	// A round trip on the viewer's own connection is the barrier: it is answered
	// by the read loop, so it comes back only after every sync sent before it was
	// dispatched.
	if _, err := viewer.RefreshSessionList(); err != nil {
		t.Fatalf("viewer round trip: %v", err)
	}

	got := make([]float64, 0, pushes)
drain:
	for {
		select {
		case s := <-m.StateSyncChan:
			got = append(got, s.MasterRatio)
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

	newest := want[len(want)-1]
	if last := got[len(got)-1]; last != newest {
		t.Fatalf("the viewer is stuck on master ratio %.2f while the daemon holds %.2f: %d of %d syncs were dropped and nothing asks for them again",
			last, newest, pushes-len(got), pushes)
	}
}
