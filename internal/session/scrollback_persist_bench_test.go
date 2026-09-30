package session

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// benchPane is a 200x50 pane holding a full 5000-line history of text.
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

// BenchmarkHistoryCapture is the part of a save that holds the pane's emulator
// lock: reading 5000 rows and the screen into the packed form.
func BenchmarkHistoryCapture(b *testing.B) {
	for name, line := range benchLines {
		b.Run(name, func(b *testing.B) {
			p := benchPane(b, line)
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.historyState(DefaultHistoryLines)
			}
		})
	}
}

// BenchmarkHistorySave is a whole save of one pane: capture, encode, compress
// and write. The file size is reported as bytes/file.
func BenchmarkHistorySave(b *testing.B) {
	for name, line := range benchLines {
		b.Run(name, func(b *testing.B) {
			defer useResurrectionDir(b.TempDir())()
			s := &Session{ptys: map[string]*PTY{}}
			s.setName("bench")
			p := benchPane(b, line)
			pol := ResolveHistoryPolicy(nil, 0, 0)
			var size int
			b.ResetTimer()
			for b.Loop() {
				if _, err := s.saveOnePane("bench", "win", p, pol, time.Now()); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if h := loadHistory("bench")["win"]; h != nil {
				data, _ := encodeHistory(h)
				size = len(data)
			}
			b.ReportMetric(float64(size), "bytes/file")
		})
	}
}
