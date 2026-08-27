package federation

import (
	"bufio"
	"errors"
	"io"
	"net"
)

// ServeProxy is the remote end of a link: what `tuios stdio-proxy` runs.
//
// It writes the preamble, then reads frames from in and writes frames to out.
// Every stream the hub opens gets its own connection to the local daemon
// socket, and bytes are copied both ways until either side ends.
//
// It never starts a daemon. The federation document has stdio-proxy start one
// if needed, and stage 1 does not, because starting a daemon restores that
// machine's saved sessions: a side effect on remote state, from a command whose
// whole contract is that it only reads. A host with no daemon running reports
// itself that way instead, which is a true and useful answer.
//
// It returns when in ends, which is what happens when the hub closes the link
// or ssh drops.
func ServeProxy(in io.Reader, out io.Writer, dial func() (net.Conn, error)) error {
	if _, err := io.WriteString(out, LinkPreamble+"\n"); err != nil {
		return err
	}
	if f, ok := out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}

	pipe := &rwc{r: in, w: out}
	m := newMux(pipe, nil)
	m.accept = func(s *Stream) { serveStream(s, dial) }

	err := m.run()
	m.stop(err)
	m.wg.Wait()
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}

// serveStream connects one link stream to one daemon socket connection.
func serveStream(s *Stream, dial func() (net.Conn, error)) {
	defer func() { _ = s.Close() }()

	conn, err := dial()
	if err != nil {
		// Closing the stream is the whole report. The hub reads the closed
		// stream as "this host has no daemon to answer with", which is what it
		// then shows, and no text from this side is needed or trusted.
		return
	}
	defer func() { _ = conn.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(conn, s)
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	_, _ = io.Copy(s, conn)
	<-done
}

// rwc glues a separate reader and writer into the io.ReadWriteCloser the mux
// wants. Closing it is a no-op: stdin and stdout belong to the process, and the
// mux closing its pipe must not take the process's own handles down.
type rwc struct {
	r io.Reader
	w io.Writer
}

func (p *rwc) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *rwc) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *rwc) Close() error                { return nil }

// readPreamble consumes lines from br until it sees the link preamble, so
// banner text ahead of the proxy is discarded. It gives up after
// preambleScanLimit bytes rather than reading a remote that will never send it.
func readPreamble(br *bufio.Reader) error {
	read := 0
	for {
		line, err := br.ReadString('\n')
		read += len(line)
		if trimCR(line) == LinkPreamble {
			return nil
		}
		if err != nil {
			return ErrNoPreamble
		}
		if read > preambleScanLimit {
			return ErrNoPreamble
		}
	}
}

// ErrNoPreamble reports a link whose remote end never identified itself. The
// usual causes are tuios missing on the remote machine and an ssh command that
// reached a shell instead of the proxy.
var ErrNoPreamble = errors.New("federation: the remote end did not answer as a tuios link")

func trimCR(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
