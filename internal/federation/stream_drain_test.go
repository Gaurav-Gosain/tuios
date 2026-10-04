package federation

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

// TestStreamReadsEverythingDeliveredBeforeTheLinkCloses: data a stream was
// handed before its link went down is read in full before the end is
// reported. The link ending closes the mux's done channel and the stream's
// closed channel together, and Read picked among its ready arms at random:
// the done arm reported the end without draining, so a link that closed
// right behind an answer lost all or part of it about half the time.
//
// The race is in select's choice, not in timing, so repeating the same
// sequence is enough to hit both arms.
func TestStreamReadsEverythingDeliveredBeforeTheLinkCloses(t *testing.T) {
	const chunks = 8
	for round := range 200 {
		m := newMuxRW(nil, nil, nil, nil, 1)
		s := newStream(m, 1)
		m.streams[s.id] = s

		var want []byte
		for i := range chunks {
			chunk := []byte(fmt.Sprintf("chunk %d of round %d;", i, round))
			want = append(want, chunk...)
			s.deliver(chunk)
		}
		_ = m.Close()

		// A small buffer, so a chunk is also left partly read across the end.
		got, err := readAllSmall(s, 5)
		if err != nil {
			t.Fatalf("round %d: the stream ended with %v, want io.EOF", round, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("round %d: read %d of %d bytes delivered before the link closed", round, len(got), len(want))
		}
	}
}

// readAllSmall reads r to its end through a buffer of size n, and returns
// nil for an io.EOF end.
func readAllSmall(r io.Reader, n int) ([]byte, error) {
	var out []byte
	buf := make([]byte, n)
	for {
		k, err := r.Read(buf)
		out = append(out, buf[:k]...)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}
