package session

import (
	"fmt"
	"testing"
)

// The daemon rebuilds a lifecycle snapshot and diffs it against the previous
// one on every state sync, and encodes the whole session state to broadcast it.
// Both scale with the window count rather than with what changed, so these
// measure them at window counts a heavy user reaches.

// benchState builds a session state with n windows spread over workspaces.
func benchState(n int) *SessionState {
	st := &SessionState{
		Name:             "bench",
		CurrentWorkspace: 1,
		MasterRatio:      0.5,
		AutoTiling:       true,
		Width:            207,
		Height:           55,
		WorkspaceFocus:   make(map[int]string, 9),
		Windows:          make([]WindowState, 0, n),
	}
	for i := range n {
		id := fmt.Sprintf("window-%04d-abcdef", i)
		st.Windows = append(st.Windows, WindowState{
			ID:        id,
			Title:     fmt.Sprintf("bash - /home/user/project/dir%02d", i),
			PTYID:     fmt.Sprintf("pty-%04d", i),
			X:         (i % 4) * 50,
			Y:         (i / 4) * 12,
			Width:     50,
			Height:    12,
			Z:         i,
			Workspace: 1 + (i % 9),
		})
	}
	if n > 0 {
		st.FocusedWindowID = st.Windows[0].ID
	}
	return st
}

// BenchmarkSnapshotLifecycle measures the snapshot the daemon takes before and
// after every state mutation. It allocates a slice and a map sized to the
// window count each time it runs.
func BenchmarkSnapshotLifecycle(b *testing.B) {
	for _, n := range []int{4, 16, 64} {
		b.Run(fmt.Sprintf("windows-%d", n), func(b *testing.B) {
			st := benchState(n)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = snapshotLifecycle(st)
			}
		})
	}
}

// BenchmarkDiffLifecycle measures the diff itself in the case that dominates:
// nothing changed. A state sync that produces no events still walks every
// window three times and does a map lookup per window per pass, so the cost of
// a no-op sync is the floor for every sync.
func BenchmarkDiffLifecycle(b *testing.B) {
	for _, n := range []int{4, 16, 64} {
		st := benchState(n)
		before := snapshotLifecycle(st)

		b.Run(fmt.Sprintf("windows-%d/unchanged", n), func(b *testing.B) {
			after := snapshotLifecycle(st)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = diffLifecycle(before, after)
			}
		})

		b.Run(fmt.Sprintf("windows-%d/one-renamed", n), func(b *testing.B) {
			changed := benchState(n)
			changed.Windows[n/2].CustomName = "renamed"
			after := snapshotLifecycle(changed)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = diffLifecycle(before, after)
			}
		})
	}
}

// BenchmarkStateCodec measures encoding a full session state, which is what
// goes over the wire to every attached client on every sync. The whole state is
// re-encoded whatever changed, so this is the per-sync cost.
func BenchmarkStateCodec(b *testing.B) {
	for _, n := range []int{4, 16, 64} {
		st := benchState(n)

		for _, ct := range []CodecType{CodecJSON, CodecGob} {
			codec := GetCodec(ct)
			b.Run(fmt.Sprintf("%s/windows-%d/encode", ct, n), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if _, err := codec.Encode(st); err != nil {
						b.Fatal(err)
					}
				}
			})

			data, err := codec.Encode(st)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(fmt.Sprintf("%s/windows-%d/decode", ct, n), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					var out SessionState
					if err := codec.Decode(data, &out); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(len(data)), "wire-bytes")
			})
		}
	}
}
