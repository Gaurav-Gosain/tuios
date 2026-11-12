package capture

import (
	"errors"
	"io"
)

// blockReaderAt reads a file in blocks and answers the many small reads the
// font parser makes from them. sfnt reads a name table a record at a time, a
// few bytes per read, so a scan of 1090 font files made 129,000 read calls
// (639 bytes each) and, on a cold disk, one wait for each that the page cache
// had not already answered. A block of 32 KiB holds a name table in one or two
// reads. It is for one reader at a time: a blockReaderAt is not safe for
// several goroutines, and each file gets its own.
type blockReaderAt struct {
	r      io.ReaderAt
	blocks map[int64][]byte
	// order is the blocks held, oldest first, so the oldest goes when the
	// cache is full.
	order []int64
}

const (
	// fontBlock is the size of one read from the file.
	fontBlock = 32 << 10
	// fontBlocksHeld is how many blocks one file keeps: 256 KiB, more than a
	// font's table directory and its name table need together.
	fontBlocksHeld = 8
)

func newBlockReaderAt(r io.ReaderAt) *blockReaderAt {
	return &blockReaderAt{r: r, blocks: map[int64][]byte{}}
}

// block returns block n of the file. A block at the end of the file is short,
// and a block past the end is empty.
func (b *blockReaderAt) block(n int64) ([]byte, error) {
	if data, ok := b.blocks[n]; ok {
		return data, nil
	}
	buf := make([]byte, fontBlock)
	got, err := b.r.ReadAt(buf, n*fontBlock)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:got]
	if len(b.order) >= fontBlocksHeld {
		delete(b.blocks, b.order[0])
		b.order = b.order[1:]
	}
	b.blocks[n] = buf
	b.order = append(b.order, n)
	return buf, nil
}

// ReadAt reads len(p) bytes at off, as io.ReaderAt says: fewer only with an
// error, io.EOF at the end of the file.
func (b *blockReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	done := 0
	for done < len(p) {
		pos := off + int64(done)
		data, err := b.block(pos / fontBlock)
		if err != nil {
			return done, err
		}
		in := int(pos % fontBlock)
		if in >= len(data) {
			return done, io.EOF
		}
		done += copy(p[done:], data[in:])
		if len(data) < fontBlock && done < len(p) {
			return done, io.EOF
		}
	}
	return done, nil
}
