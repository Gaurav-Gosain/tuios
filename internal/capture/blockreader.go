package capture

import (
	"errors"
	"io"
)

type blockReaderAt struct {
	r      io.ReaderAt
	blocks map[int64][]byte
	order  []int64
}

const (
	fontBlock      = 32 << 10
	fontBlocksHeld = 8
)

func newBlockReaderAt(r io.ReaderAt) *blockReaderAt {
	return &blockReaderAt{r: r, blocks: map[int64][]byte{}}
}

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
