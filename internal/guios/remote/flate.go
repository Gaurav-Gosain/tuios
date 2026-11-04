package remote

import (
	"bufio"
	"compress/flate"
	"io"
	"sync"
)

// deflate is the compression both sides of a remote connection take:
// DEFLATE across everything it carries, flushed with every write so that
// nothing waits. A terminal's frames and kerod's state, sent whole on
// every change, are text that repeats itself, which it makes several
// times smaller.
const deflate = "deflate"

// flateConn compresses what goes through rwc both ways. What it writes
// goes out in one piece per write, through a buffer: the compressor hands
// on a few hundred bytes at a time, each of which would be a write of
// the transport of its own, a WebSocket message or a pipe's.
type flateConn struct {
	rwc io.ReadWriteCloser
	r   io.ReadCloser
	wmu sync.Mutex
	bw  *bufio.Writer
	w   *flate.Writer
}

func newFlateConn(rwc io.ReadWriteCloser) *flateConn {
	bw := bufio.NewWriterSize(rwc, 64<<10)
	// BestSpeed: the link is what is slow, not the machines.
	w, _ := flate.NewWriter(bw, flate.BestSpeed)
	return &flateConn{rwc: rwc, r: flate.NewReader(rwc), bw: bw, w: w}
}

func (c *flateConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *flateConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	n, err := c.w.Write(p)
	if err == nil {
		err = c.w.Flush()
	}
	if err == nil {
		err = c.bw.Flush()
	}
	return n, err
}

func (c *flateConn) Close() error {
	c.r.Close()
	return c.rwc.Close()
}
