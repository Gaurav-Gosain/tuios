package capture

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"testing"
)

// countingReaderAt counts the reads that reach the file.
type countingReaderAt struct {
	r     io.ReaderAt
	reads int
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.reads++
	return c.r.ReadAt(p, off)
}

// TestBlockReaderAnswersLikeTheFile holds the block reader to io.ReaderAt, for
// the reads a parser makes: inside a block, across two or three, at the end of
// the file, past it, and of no length.
//
// How it could pass wrongly, written down first:
//   - It could be compared with itself. The reference is the bytes.Reader the
//     blocks are cut from, asked the same question.
//   - The block edges could be skipped. The file is two and a half blocks and
//     the reads include every offset around each edge.
//   - A short last block could hide a wrong end. Reads end exactly at the end,
//     one byte before it and one past it.
//
// Negative control: reading a block's bytes from offset 0 instead of its own
// start returned the first block's bytes for a later one and failed.
func TestBlockReaderAnswersLikeTheFile(t *testing.T) {
	data := make([]byte, 2*fontBlock+fontBlock/2)
	rand.New(rand.NewSource(1)).Read(data)
	want := bytes.NewReader(data)
	b := newBlockReaderAt(bytes.NewReader(data))
	check := func(off, n int) {
		t.Helper()
		got, exp := make([]byte, n), make([]byte, n)
		gn, gerr := b.ReadAt(got, int64(off))
		en, eerr := want.ReadAt(exp, int64(off))
		if n == 0 {
			// A read of no bytes returns none. What error it comes with at the
			// end of the file differs between readers (bytes.Reader says EOF,
			// os.File says nothing), and a caller has nothing to read in it.
			if gn != 0 {
				t.Fatalf("ReadAt(0 bytes at %d) = %d", off, gn)
			}
			return
		}
		if gn != en || !bytes.Equal(got[:gn], exp[:en]) || errors.Is(gerr, io.EOF) != errors.Is(eerr, io.EOF) || (gerr == nil) != (eerr == nil) {
			t.Fatalf("ReadAt(%d bytes at %d) = %d, %v; the file gave %d, %v", n, off, gn, gerr, en, eerr)
		}
	}
	for _, edge := range []int{0, fontBlock, 2 * fontBlock, len(data)} {
		for _, d := range []int{-3, -1, 0, 1, 3} {
			for _, n := range []int{0, 1, 4, 100, fontBlock, fontBlock + 7, 2*fontBlock + 5} {
				if off := edge + d; off >= 0 {
					check(off, n)
				}
			}
		}
	}
	rng := rand.New(rand.NewSource(2))
	for range 500 {
		check(rng.Intn(len(data)+50), rng.Intn(3*fontBlock))
	}
	if _, err := b.ReadAt(make([]byte, 1), -1); err == nil {
		t.Error("a negative offset was read")
	}
}

// TestBlockReaderMakesFewReads holds the point of it: a parser's many small
// reads reach the file a handful of times.
//
// Negative control: reading through with no blocks made one file read for
// each of the 2000 small reads and failed.
func TestBlockReaderMakesFewReads(t *testing.T) {
	data := make([]byte, 3*fontBlock)
	c := &countingReaderAt{r: bytes.NewReader(data)}
	b := newBlockReaderAt(c)
	buf := make([]byte, 12)
	for i := range 2000 {
		if _, err := b.ReadAt(buf, int64(i*40)); err != nil {
			t.Fatal(err)
		}
	}
	if c.reads > 4 {
		t.Errorf("2000 small reads inside 80 KB made %d reads of the file, want at most 4", c.reads)
	}
}
