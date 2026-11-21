package federation

import (
	"bufio"
	"errors"
	"io"
	"net"
)

// ServeProxyFor is the remote end of a link: what `tuios stdio-proxy` runs.
//
// It writes the preamble, then reads frames from in and writes frames to out.
// Every stream the hub opens gets its own connection to the local daemon
// socket, and bytes are copied both ways until either side ends.
//
// It never starts a daemon, because starting a daemon restores that machine's
// saved sessions: a side effect on remote state that nobody on that machine
// asked for. A host with no daemon running reports
// itself that way instead, which is a true and useful answer.
//
// It returns when in ends, which is what happens when the hub closes the link
// or ssh drops.
//
// dial is told what the hub said about each stream when it opened it. tuios
// stdio-proxy uses it to dial the link-human socket for a stream the hub
// vouched for. See StreamOpen.
func ServeProxyFor(in io.Reader, out io.Writer, dial func(StreamOpen) (net.Conn, error)) error {
	if _, err := io.WriteString(out, LinkPreamble+"\n"); err != nil {
		return err
	}
	if f, ok := out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}

	pipe := &rwc{r: in, w: out}
	m := newMux(pipe, nil, answererFirstID)
	m.accept = func(s *Stream) { serveStream(s, dial) }

	err := m.run()
	m.stop(err)
	m.wg.Wait()
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}

// PurposeCloser is a daemon connection that can tell the daemon its stream was
// closed on purpose by the far side, before the connection ends.
type PurposeCloser interface {
	NoteClosedOnPurpose()
}

func serveStream(s *Stream, dial func(StreamOpen) (net.Conn, error)) {
	defer func() { _ = s.Close() }()

	conn, err := dial(s.open)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(conn, s)
		if s.ClosedByPeer() {
			if n, ok := conn.(PurposeCloser); ok {
				n.NoteClosedOnPurpose()
			}
		}
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	_, _ = io.Copy(s, conn)

	_ = s.Close()
	<-done
}

type rwc struct {
	r io.Reader
	w io.Writer
}

func (p *rwc) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *rwc) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *rwc) Close() error                { return nil }

func readPreamble(br *bufio.Reader) (preambleNote, error) {
	var note preambleNote
	read := 0
	for {
		line, err := br.ReadString('\n')
		read += len(line)
		line = trimCR(line)
		if line == LinkPreamble {
			return note, nil
		}
		note.noteLine(line)
		if err != nil {
			return note, ErrNoPreamble
		}
		if read > preambleScanLimit {
			return note, ErrNoPreamble
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
