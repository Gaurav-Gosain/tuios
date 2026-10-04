package session

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// benchPane is a 200x50 pane holding a full history of text.
func benchPane(b *testing.B, line func(i int) string) *PTY {
	b.Helper()
	p := &PTY{ID: "bench", terminal: vt.NewWithScrollback(200, 50, 10000)}
	var in strings.Builder
	for i := range 5100 {
		in.WriteString(line(i))
		in.WriteString("\r\n")
	}
	writePane(p, in.String())
	return p
}

// yes(1) output, and a build log with colours and varied paths, which
// compresses far less.
var benchLines = map[string]func(int) string{
	"yes": func(int) string { return "y" },
	"buildlog": func(i int) string {
		return fmt.Sprintf("\x1b[32m[%5d/9999]\x1b[0m \x1b[1mCC\x1b[0m src/module_%03d/file_%05d.c -o build/obj/%x.o -O2 -Wall", i, i%97, i*7919%100003, i*2654435761)
	},
}

// BenchmarkHistoryCaptureLocked is the part of a save that holds the pane's
// emulator lock: reading the screen and the history rows.
func BenchmarkHistoryCaptureLocked(b *testing.B) {
	for name, line := range benchLines {
		for _, lines := range []int{DefaultHistoryLines, 5000} {
			b.Run(fmt.Sprintf("%s/%d", name, lines), func(b *testing.B) {
				p := benchPane(b, line)
				b.ResetTimer()
				for b.Loop() {
					_, _ = p.captureHistory(lines)
				}
			})
		}
	}
}

// BenchmarkHistorySave is a whole save of one pane: capture, pack, encode,
// compress and write. The file size is reported as bytes/file.
func BenchmarkHistorySave(b *testing.B) {
	for name, line := range benchLines {
		b.Run(name, func(b *testing.B) {
			defer useResurrectionDir(b.TempDir())()
			s := &Session{ptys: map[string]*PTY{}}
			s.setName("bench")
			p := benchPane(b, line)
			pol := ResolveHistoryPolicy(nil, 0, 0)
			b.ResetTimer()
			for b.Loop() {
				if _, err := s.saveOnePane("bench", "win", p, pol, time.Now()); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if h := loadHistory("bench")["win"]; h != nil {
				data, _ := encodeHistory(h)
				b.ReportMetric(float64(len(data)), "bytes/file")
			}
		})
	}
}

// BenchmarkHistorySave20BusyPanes is a forced save of a session of 20 panes
// while each pane's output keeps arriving: the save's length, and the longest
// any one write waited for its pane's emulator lock, which is how long the
// save held up that pane's output.
func BenchmarkHistorySave20BusyPanes(b *testing.B) {
	defer useResurrectionDir(b.TempDir())()
	s := &Session{ptys: map[string]*PTY{}, config: &SessionConfig{}}
	s.setName("busy")
	pol := ResolveHistoryPolicy(nil, 0, 0)
	s.config.history = &pol
	state := &SessionState{Name: "busy"}
	for i := range 20 {
		p := benchPane(b, benchLines["buildlog"])
		p.ID = fmt.Sprintf("pty-%d", i)
		s.ptys[p.ID] = p
		state.Windows = append(state.Windows, WindowState{ID: fmt.Sprintf("win-%d", i), PTYID: p.ID})
	}

	var stop atomic.Bool
	var maxWait atomic.Int64
	var wg sync.WaitGroup
	for _, p := range s.ptys {
		wg.Go(func() {
			line := []byte(benchLines["buildlog"](7) + "\r\n")
			for !stop.Load() {
				t0 := time.Now()
				p.terminalMu.Lock()
				wait := time.Since(t0)
				_, _ = p.terminal.Write(line)
				p.vtSeq += int64(len(line))
				p.terminalMu.Unlock()
				for {
					cur := maxWait.Load()
					if int64(wait) <= cur || maxWait.CompareAndSwap(cur, int64(wait)) {
						break
					}
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
	time.Sleep(50 * time.Millisecond)
	maxWait.Store(0)
	b.ResetTimer()
	for b.Loop() {
		s.saveHistory(state, true)
	}
	b.StopTimer()
	stop.Store(true)
	wg.Wait()
	b.ReportMetric(float64(maxWait.Load())/1e6, "max-wait-ms")
	// What the save leaves behind: the live heap once it is over, with the
	// panes and their histories still held. A save that leaves decoded
	// copies of the rows it read in a cache shows here.
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	b.ReportMetric(float64(ms.HeapAlloc)/(1<<20), "heap-after-MB")
	runtime.KeepAlive(s)
}
