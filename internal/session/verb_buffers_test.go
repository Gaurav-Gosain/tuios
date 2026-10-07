package session

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
)

// TestBufferUploadsHoldBoundedMemory is a security boundary: any process
// that can reach the socket can start an upload, so what unfinished uploads
// hold must stay bounded however many connections start one.
//
// How the bound could fail, written down first:
//   - Each connection could hold up to the cap, so N connections hold N
//     times it. Sixteen connections each send most of the cap.
//   - A connection could hold several uploads under different ids. One
//     connection starts a second id.
//   - A closed connection's upload could stay until it times out. The
//     connection is dropped and the bytes must go at once.
//   - A refusal could drop or block another connection's upload, which
//     turns the bound into a lockout. The first upload must still finish.
func TestBufferUploadsHoldBoundedMemory(t *testing.T) {
	const capBytes = 1000
	d := &Daemon{}
	d.buffers = pastebuf.New(pastebuf.DefaultLimit, capBytes)
	part := bytes.Repeat([]byte("x"), 900)

	conns := make([]*connState, 16)
	for i := range conns {
		conns[i] = &connState{}
		_, _ = d.uploadPart(conns[i], "u", part, false, capBytes, false)
		if got := d.uploadBytes(); got > capBytes {
			t.Fatalf("after %d connections the unfinished uploads hold %d bytes, more than the cap of %d", i+1, got, capBytes)
		}
	}
	if got := d.uploadBytes(); got != len(part) {
		t.Fatalf("the unfinished uploads hold %d bytes, want the first connection's %d alone", got, len(part))
	}

	// A second id on one connection replaces its first upload.
	if _, verr := d.uploadPart(conns[0], "v", []byte("yy"), false, capBytes, false); verr != nil {
		t.Fatalf("a new upload on the same connection was refused: %v", verr)
	}
	if got := d.uploadBytes(); got != 2 {
		t.Fatalf("after a second id the connection holds %d bytes, want 2", got)
	}

	// Another connection can now upload, and finishing returns the whole.
	if _, verr := d.uploadPart(conns[1], "w", part[:500], false, capBytes, false); verr != nil {
		t.Fatalf("an upload under the cap was refused: %v", verr)
	}
	whole, verr := d.uploadPart(conns[1], "w", part[:100], true, capBytes, false)
	if verr != nil || len(whole) != 600 {
		t.Fatalf("the last part returned %d bytes (%v), want 600", len(whole), verr)
	}

	// A refused part drops only its own upload.
	if _, verr := d.uploadPart(conns[2], "big", part, false, capBytes, false); verr != nil {
		t.Fatalf("an upload under the cap was refused: %v", verr)
	}
	if _, verr := d.uploadPart(conns[3], "late", part, false, capBytes, false); verr == nil {
		t.Fatalf("an upload past the cap was taken")
	}
	if got := d.uploadBytes(); got != 2+len(part) {
		t.Fatalf("after a refusal the uploads hold %d bytes, want %d: the refusal dropped another upload", got, 2+len(part))
	}

	// A closed connection's upload goes at once.
	d.dropUploads(conns[2])
	d.dropUploads(conns[0])
	if got := d.uploadBytes(); got != 0 {
		t.Fatalf("after both connections closed the uploads hold %d bytes, want 0", got)
	}
}

// TestAClosedConnectionDropsItsUpload sends the first part of an upload over
// a real socket and closes the connection. The daemon must drop the part when
// the connection ends, not hold it until the upload times out.
func TestAClosedConnectionDropsItsUpload(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	line := `{"id":1,"verb":"set-buffer","params":{"data":"` + strings.Repeat("x", 4096) + `","upload":"u","more":true}}` + "\n"
	if _, err := conn.Write([]byte(line)); err != nil {
		t.Fatalf("write the part: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
		t.Fatalf("read the answer to the part: %v", err)
	}
	if got := d.uploadBytes(); got != 4096 {
		t.Fatalf("the daemon holds %d bytes of uploads, want the 4096 just sent", got)
	}
	_ = conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for d.uploadBytes() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the daemon still holds %d bytes of a closed connection's upload", d.uploadBytes())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestThePersonsShareSurvivesAFullPanePool is a security boundary: a pane
// without admin must not be able to use up what the person's own large
// requests need. Panes hold every byte their uploads and their large request
// lines may, and the person's 1 MB set-buffer, sent in parts over a real
// socket as the CLI sends it, still succeeds.
//
// How it could pass wrongly: the person's call could be small enough to skip
// both bounds, so it is a 1 MB upload in two parts, each over the size at
// which a line is charged to a budget. The pane pools could be only part
// full, so they are filled until the next pane upload is refused, and every
// chunk of the panes' line budget is taken.
func TestThePersonsShareSurvivesAFullPanePool(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	_, maxBytes := d.bufferStore().Limits()

	// Pane connections fill the panes' upload pool.
	part := bytes.Repeat([]byte("p"), maxBytes/4)
	for i := 0; ; i++ {
		if _, verr := d.uploadPart(&connState{}, "pane", part, false, maxBytes, false); verr != nil {
			if strings.Contains(verr.Message, "bytes") {
				t.Fatalf("the refusal tells how much other uploads hold: %s", verr.Message)
			}
			break
		}
		if i > 8 {
			t.Fatalf("the panes' uploads are not bounded")
		}
	}
	// And the panes' line budget is taken whole.
	panes := d.lineBudgetFor(false)
	for range cap(panes) {
		panes <- struct{}{}
	}
	defer panes.release(cap(panes))

	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	half := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("q"), 512<<10))
	for i, line := range []string{
		`{"id":1,"verb":"set-buffer","params":{"upload":"mine","more":true,"data_b64":"` + half + `"}}`,
		`{"id":2,"verb":"set-buffer","params":{"upload":"mine","name":"persons","data_b64":"` + half + `"}}`,
	} {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write([]byte(line + "\n")); err != nil {
			t.Fatalf("write part %d: %v", i+1, err)
		}
		reply, err := r.ReadString('\n')
		if err != nil || strings.Contains(reply, `"error"`) {
			t.Fatalf("the person's part %d was refused with the pane pools full: %v %s", i+1, err, reply)
		}
	}
	b, err := d.bufferStore().Get("persons", 0, nil)
	if err != nil || len(b.Data) != 1<<20 {
		t.Fatalf("the person's buffer holds %d bytes (%v), want 1 MiB", len(b.Data), err)
	}
}
