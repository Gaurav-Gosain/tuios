package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// TestSSHKittyShmDoesNotKillSession exercises the remote-client graphics path
// that used to kill SSH sessions: a guest pane emits kitty a=T frames over
// shared-memory transport (t=s, i=1), and the session re-encodes them to
// multi-MB inline frames written to the SSH channel (RemoteClient=true).
//
// The teardown mechanism this guards against: the bubbletea renderer and the
// graphics passthrough used to write the same ssh.Session from different
// goroutines. x/crypto's channel.WriteExtended shares one packet buffer
// between concurrent writers, so overlapping writes corrupt the channel-data
// length header inside a validly encrypted packet; the client then fails the
// stream with "ssh: wrong packet length" and drops the whole connection. The
// fix routes every session write through one serialized writer (serialWriter
// in ssh.go).
//
// The corrupting overlap is a scheduler race, so one session flooding once is
// only likely to hit it, not guaranteed. To pin the outcome, every session
// runs a text flood (yes) and continuous typed input alongside the graphics
// flood, keeping text writes interleaving with the multi-MB graphics writes,
// and several sessions flood concurrently; the test fails if ANY of them is
// torn down. On the unserialized code at least one session dies within
// seconds on every run; on the fixed code all of them survive, every run.
//
// Ephemeral mode is used so the test is fully isolated: no daemon, no saved
// session state. No browser is launched; the frames are synthesized and
// emitted by cat-ing a file of raw kitty APC sequences that reference a real
// /dev/shm object created by the test.
func TestSSHKittyShmDoesNotKillSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH integration test in short mode")
	}
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("no /dev/shm on this platform")
	}

	// Marker line the text flood prints; see the drain goroutine below.
	const textMarker = "TUIOS-TEXT-FLOOD"

	// Two real shm objects: one sized like the captured browser pane
	// (1810x800 px RGBA, ~7.7MB of inline data per re-encoded frame) and one
	// a quarter of that (905x400, ~1.9MB inline). The sessions are split
	// between them because the two sizes stress different regimes: big frames
	// hold the passthrough mutex so long that the renderer starves and its
	// writes cluster in the gaps, while small frames cycle fast enough to
	// keep both the graphics goroutine and the renderer writing back to back.
	// Whichever regime a given run's scheduling lands in, half the sessions
	// are exercising it, which is what makes the unserialized-writes
	// corruption strike on every run.
	type floodShape struct {
		pxW, pxH int
		frames   int
	}
	shapes := []floodShape{
		{pxW: 1810, pxH: 800, frames: 200},
		{pxW: 905, pxH: 400, frames: 1600},
	}
	tempDir := t.TempDir()
	floodScripts := make([]string, len(shapes))
	for si, shape := range shapes {
		shmName := fmt.Sprintf("tuios-crash-%d-%d", os.Getpid(), si)
		shmPath := "/dev/shm/" + shmName
		shmData := make([]byte, shape.pxW*shape.pxH*4)
		for i := range shmData {
			shmData[i] = byte(i)
		}
		if err := os.WriteFile(shmPath, shmData, 0o600); err != nil {
			t.Skipf("cannot write /dev/shm object: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(shmPath) })

		// A file of raw kitty a=T shm frames, each the captured control
		// command: transmit+place, shared-memory medium, image id 1, no
		// inline data, carrying only the base64 shm name. Enough frames that
		// the flood outlasts the watch window.
		nameB64 := base64.StdEncoding.EncodeToString([]byte(shmName))
		frame := fmt.Sprintf("\x1b_Ga=T,t=s,i=1,f=32,s=%d,v=%d;%s\x1b\\", shape.pxW, shape.pxH, nameB64)
		framesPath := filepath.Join(tempDir, fmt.Sprintf("frames-%d.bin", si))
		var framesBuf []byte
		for i := 0; i < shape.frames; i++ {
			framesBuf = append(framesBuf, frame...)
		}
		if err := os.WriteFile(framesPath, framesBuf, 0o600); err != nil {
			t.Fatalf("write frames file: %v", err)
		}

		// The command typed into the pane lives in a script so the typed line
		// needs no quoting: a partially delivered line then cannot leave the
		// shell's line editor stuck inside an open quote, which would wedge
		// every following attempt.
		floodScripts[si] = filepath.Join(tempDir, fmt.Sprintf("flood-%d.sh", si))
		script := fmt.Sprintf("cat %s &\nexec yes %s\n", framesPath, textMarker)
		if err := os.WriteFile(floodScripts[si], []byte(script), 0o700); err != nil {
			t.Fatalf("write flood script: %v", err)
		}
	}

	// Ephemeral SSH server: self-contained, touches no daemon state.
	port := freePort(t)
	hostKey := filepath.Join(t.TempDir(), "test_host_key")
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- StartSSHServer(ctx, &SSHServerConfig{
			Host:      "127.0.0.1",
			Port:      port,
			KeyPath:   hostKey,
			Ephemeral: true,
			Version:   "test",
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(5 * time.Second):
		}
	})

	addr := net.JoinHostPort("127.0.0.1", port)
	const numSessions = 8
	var wg sync.WaitGroup
	errs := make([]error, numSessions)
	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			errs[id] = runKittyFloodSession(addr, floodScripts[id%len(floodScripts)], textMarker)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("session %d: %v", i, err)
		}
	}
}

// runKittyFloodSession connects one SSH session, drives its startup pane to
// run the graphics and text floods, and watches it for 6 seconds. It returns
// nil if the session survived a proven flood, or an error describing either
// the teardown or a drive failure.
func runKittyFloodSession(addr, floodScript, textMarker string) error {
	clientCfg := &gossh.ClientConfig{
		User:            "tester",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
	var client *gossh.Client
	deadline := time.Now().Add(8 * time.Second)
	for {
		c, err := gossh.Dial("tcp", addr, clientCfg)
		if err == nil {
			client = c
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("could not connect to SSH server: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = client.Close() }()

	// Surfaces the transport-level teardown reason ("ssh: wrong packet
	// length" on the unserialized code) in the failure message.
	connClosed := make(chan error, 1)
	go func() { connClosed <- client.Wait() }()

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	_ = sess.Setenv("TERM_PROGRAM", "ghostty")
	modes := gossh.TerminalModes{gossh.ECHO: 0}
	if err := sess.RequestPty("xterm-kitty", 42, 183, modes); err != nil {
		return fmt.Errorf("request pty: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	if err := sess.Shell(); err != nil {
		return fmt.Errorf("start shell: %w", err)
	}

	// Drain stdout so the SSH channel never blocks on our side; note if it
	// EOFs, count bytes so the drive can wait for the first rendered frame
	// instead of sleeping a fixed interval, and count occurrences of the text
	// flood's marker so the drive can tell when `yes` is really running (the
	// echo of the typed command alone contains it only once or twice; the
	// running flood repaints it endlessly).
	var totalRead atomic.Int64
	var markerCount atomic.Int64
	streamClosed := make(chan struct{})
	go func() {
		marker := []byte(textMarker)
		matched := 0
		buf := make([]byte, 64*1024)
		for {
			n, rerr := stdout.Read(buf)
			totalRead.Add(int64(n))
			for _, b := range buf[:n] {
				if b == marker[matched] {
					matched++
					if matched == len(marker) {
						markerCount.Add(1)
						matched = 0
					}
				} else if b == marker[0] {
					matched = 1
				} else {
					matched = 0
				}
			}
			if rerr != nil {
				close(streamClosed)
				return
			}
		}
	}()

	teardownReason := func() string {
		select {
		case cerr := <-connClosed:
			return fmt.Sprintf("transport err=%v", cerr)
		case <-time.After(2 * time.Second):
			return "transport still open"
		}
	}

	// Readiness: wait until render output has been flushed (the terminal-setup
	// preamble alone is ~120 bytes; the first frame is KBs). The pinned test
	// config (see TestMain) opens one pane and starts in terminal mode on the
	// program's initial WindowSizeMsg, which is enqueued at program start and
	// therefore processed before any input the drive sends afterwards.
	readyBy := time.Now().Add(15 * time.Second)
	for totalRead.Load() < 1024 {
		if time.Now().After(readyBy) {
			return fmt.Errorf("no render output from session; program did not start")
		}
		select {
		case <-streamClosed:
			return fmt.Errorf("SSH stdout closed before the session was driven (%s)", teardownReason())
		case <-time.After(20 * time.Millisecond):
		}
	}

	// Start both floods by running the flood script in the pane: cat replays
	// the kitty shm frames (each re-encoded server-side to ~7.7MB of inline
	// data written by the graphics goroutine), while yes floods the pane with
	// text so the bubbletea renderer keeps flushing text frames to the
	// session for the whole watch window. Running both is what makes the old
	// bug bite: the renderer's text writes must interleave safely with the
	// concurrent multi-MB graphics writes on the SSH channel. Invoking the
	// script with `sh` keeps the line portable across whatever interactive
	// shell the pane spawned.
	//
	// A shell discards queued PTY input while it initializes its line editor
	// (tcsetattr with TCSAFLUSH), so a single type-ahead write can be
	// silently dropped, in whole or in part, depending on shell startup
	// timing. Each attempt starts with ctrl+u to clear any partially
	// delivered previous line, then retypes the command, until both floods
	// are provably running: a 2MB jump within an attempt window can only be
	// the graphics flood, and a marker count past 20 can only be the text
	// flood. Retyping while they already run is harmless echoed text.
	// The two signals are latched independently: the renderer can starve
	// while the graphics goroutine floods, so the marker count may cross its
	// threshold in a later attempt window than the byte jump.
	command := fmt.Sprintf("\x15sh %s\r", floodScript)
	graphicsSeen, textSeen := false, false
	for attempt := 0; attempt < 20 && !(graphicsSeen && textSeen); attempt++ {
		base := totalRead.Load()
		if _, err := fmt.Fprint(stdin, command); err != nil {
			return fmt.Errorf("write command: %w", err)
		}
		waitUntil := time.Now().Add(1 * time.Second)
		for time.Now().Before(waitUntil) {
			if totalRead.Load() > base+2<<20 {
				graphicsSeen = true
			}
			if markerCount.Load() > 20 {
				textSeen = true
			}
			if graphicsSeen && textSeen {
				break
			}
			select {
			case <-streamClosed:
				return fmt.Errorf("SSH stdout closed while starting the kitty shm flood - session torn down (%s, read %d bytes)",
					teardownReason(), totalRead.Load())
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	// The hard gate is the graphics flood: seeing it proves the script ran,
	// and the script's exec makes the text flood run with it. The marker is
	// only confirmation that text frames are rendering; under the race
	// detector with several concurrent sessions the renderer can lag too far
	// behind the graphics goroutine to repaint within the attempt budget, so
	// its absence is not failure.
	if !graphicsSeen {
		return fmt.Errorf("could not start the floods in the pane (graphics=%v text=%v)", graphicsSeen, textSeen)
	}

	// Keep typing into the pane for the whole watch window. Each keystroke is
	// processed by the event loop and echoed by the pane's PTY, which keeps
	// renderer text frames flowing to the session even while the graphics
	// pipeline is saturating the event loop; the echo bursts are what
	// reliably interleave text writes with the multi-MB graphics writes.
	pepperDone := make(chan struct{})
	defer close(pepperDone)
	go func() {
		for {
			select {
			case <-pepperDone:
				return
			case <-time.After(15 * time.Millisecond):
				_, _ = fmt.Fprint(stdin, "xxxxxxxxxxxxxxxx")
			}
		}
	}()

	// The session must survive the flood. On a build where session writes are
	// not serialized, the corrupted stream kills the connection within a
	// couple of seconds; watch for 6s.
	sessionEnded := make(chan error, 1)
	go func() { sessionEnded <- sess.Wait() }()

	select {
	case err := <-sessionEnded:
		return fmt.Errorf("SSH session torn down while processing kitty shm frames (session err=%v, %s, read %d bytes)",
			err, teardownReason(), totalRead.Load())
	case <-streamClosed:
		return fmt.Errorf("SSH stdout closed while processing kitty shm frames (%s, read %d bytes)",
			teardownReason(), totalRead.Load())
	case <-time.After(6 * time.Second):
		// Survived the watch window. Require that the graphics flood really
		// traversed the session, so the test cannot pass vacuously if the
		// pane never ran cat: each re-encoded frame is ~7.7MB of inline
		// data, far above this floor even with several sessions sharing the
		// machine and the race detector slowing the encode down.
		if got := totalRead.Load(); got < 10<<20 {
			return fmt.Errorf("only %d bytes reached the client; graphics flood did not run", got)
		}
		return nil
	}
}
