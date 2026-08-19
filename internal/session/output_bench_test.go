package session

import (
	"fmt"
	"testing"
)

// The daemon's per-chunk output path had no benchmark, so the cost of the work
// that runs once per PTY read was never visible. A flooding pane drives this
// loop thousands of times a second per subscriber, which makes anything
// unconditional here far more expensive than its source line suggests.
//
// The two steps measured are the ones every chunk pays regardless of what the
// bytes are: recording the chunk in the catch-up ring, and handing it to every
// subscriber. The PTY read and the VT parse are deliberately outside: the read
// is a syscall the benchmark cannot honestly reproduce, and the parse is
// already covered by the emulator benchmarks in internal/vt.
func benchPTY(subs int, chanCap int) *PTY {
	p := &PTY{
		ID:           "bench-pty-0000000000000000000000000",
		outputBuffer: make([]byte, 64*1024),
		subscribers:  make(map[string]*ptySubscriber, subs),
	}
	for i := range subs {
		p.subscribers[fmt.Sprintf("client-%d", i)] = &ptySubscriber{
			ch: make(chan ptyChunk, chanCap),
		}
	}
	return p
}

// BenchmarkPTYOutputChunk is the daemon's steady-state cost per chunk of PTY
// output: ring append plus broadcast to n subscribers. Chunk size is the 16 KiB
// the read loop uses, which is what a flooding pane actually delivers.
//
// The subscriber channels are drained by the benchmark itself rather than by a
// reader goroutine, so the number measures the producer side alone and does not
// vary with how a consumer happens to be scheduled.
func BenchmarkPTYOutputChunk(b *testing.B) {
	for _, subs := range []int{0, 1, 4} {
		b.Run(fmt.Sprintf("subscribers-%d", subs), func(b *testing.B) {
			p := benchPTY(subs, 16384)
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

				// Keep the channels from filling, which would turn every later
				// broadcast into the cheap dropped-chunk branch and stop the
				// benchmark measuring the delivery path at all.
				if subs > 0 && i%1024 == 1023 {
					for _, sub := range p.subscribers {
						for len(sub.ch) > 0 {
							<-sub.ch
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
			p := benchPTY(subs, 16384)
			data := make([]byte, 16*1024)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				p.broadcast(ptyChunk{data: data}, int64(i+1))
				if i%1024 == 1023 {
					for _, sub := range p.subscribers {
						for len(sub.ch) > 0 {
							<-sub.ch
						}
					}
				}
			}
		})
	}
}
