package session

import (
	"fmt"
	"testing"
)

func benchPTY(subs int) *PTY {
	p := &PTY{
		ID:           "bench-pty-0000000000000000000000000",
		outputBuffer: make([]byte, 64*1024),
		subscribers:  make(map[string]*ptySubscriber, subs),
	}
	for i := range subs {
		p.subscribers[fmt.Sprintf("client-%d", i)] = newSubscriber()
	}
	return p
}

// BenchmarkPTYOutputChunk is the daemon's steady-state cost per chunk of PTY
// output: ring append plus broadcast to n subscribers. Chunk size is the 16 KiB
// the read loop uses, which is what a flooding pane actually delivers.
//
// The subscriber queues are drained by the benchmark itself rather than by a
// reader goroutine, so the number measures the producer side alone and does not
// vary with how a consumer happens to be scheduled.
func BenchmarkPTYOutputChunk(b *testing.B) {
	for _, subs := range []int{0, 1, 4} {
		b.Run(fmt.Sprintf("subscribers-%d", subs), func(b *testing.B) {
			p := benchPTY(subs)
			data := make([]byte, 16*1024)
			for i := range data {
				data[i] = byte('a' + i%26)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				p.outputMu.Lock()
				seq := p.appendToBuffer(data)
				p.outputMu.Unlock()
				p.broadcast(ptyChunk{data: data}, seq)

				if subs > 0 && i%1024 == 1023 {
					for _, sub := range p.subscribers {
						for _, ok, _ := sub.out.pop(); ok; _, ok, _ = sub.out.pop() {
						}
					}
				}
			}
		})
	}
}

// BenchmarkPTYBroadcast isolates the fan-out from the ring, because the two
// scale with different things: the ring with chunk size, the fan-out with the
// number of attached clients.
func BenchmarkPTYBroadcast(b *testing.B) {
	for _, subs := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("subscribers-%d", subs), func(b *testing.B) {
			p := benchPTY(subs)
			data := make([]byte, 16*1024)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				p.broadcast(ptyChunk{data: data}, int64(i+1))
				if i%1024 == 1023 {
					for _, sub := range p.subscribers {
						for _, ok, _ := sub.out.pop(); ok; _, ok, _ = sub.out.pop() {
						}
					}
				}
			}
		})
	}
}

// BenchmarkScreenSettleArm is the settle timer re-armed the way a flooding pane
// arms it: once per chunk, for a scan that by design runs once, after the flood
// ends. The arm is on the output path, so its cost is paid per chunk whether or
// not the scan it schedules ever runs.
func BenchmarkScreenSettleArm(b *testing.B) {
	p := benchPTY(0)
	p.setScreenLook(func() {})
	b.Cleanup(p.stopScreenSettle)

	b.ReportAllocs()
	for b.Loop() {
		p.armScreenSettle()
	}
}

// BenchmarkEventPublishNoSubs is the control-plane publish every chunk of PTY
// output raises, with nobody listening, which is the normal case: the event
// stream exists for the events verb and the wait verbs, and most of the time no
// client has one open. The hub is shared by every pane in the daemon, so the
// parallel case is the question that matters: whether several flooding panes
// serialize against each other on one lock.
func BenchmarkEventPublishNoSubs(b *testing.B) {
	h := newEventHub()
	ev := streamEvent{Type: EventOutput, Session: "bench", PTYID: "pty", Bytes: 16384}

	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			h.publish(ev)
		}
	})
	b.Run("parallel-4-panes", func(b *testing.B) {
		b.SetParallelism(4)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				h.publish(ev)
			}
		})
	})
}

// BenchmarkPaneMarkRaised is what each chunk of output adds for pane marks
// once the pane's mark is set, which is every chunk but the first after a
// subscription change: the event type compare and one atomic load. See
// pane_marks.go.
//
// The event type is read from a variable. With the constant, the compiler
// folds the type compare away and the benchmark measures half the cost.
func BenchmarkPaneMarkRaised(b *testing.B) {
	p := benchPTY(0)
	p.raisePaneMark(PaneMarkOutput)
	b.ReportAllocs()
	for b.Loop() {
		if bit := paneMarkBit(benchPaneMarkEvent); bit != 0 && p.raisePaneMark(bit) {
			b.Fatal("a set mark was raised again")
		}
	}
}

var benchPaneMarkEvent = EventOutput
