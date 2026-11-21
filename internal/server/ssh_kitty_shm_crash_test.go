package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
// emitted by a shell loop that writes a /dev/shm object and then cats a raw
// kitty APC sequence that names it, once per frame.
func TestSSHKittyShmDoesNotKillSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH integration test in short mode")
	}
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("no /dev/shm on this platform")
	}

	const textMarker = "TUIOS-TEXT-FLOOD"

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
		const buffers = 4
		var script strings.Builder
		fmt.Fprintf(&script, "(for i in $(seq 0 %d); do case $((i %% %d)) in\n", shape.frames-1, buffers)
		for b := range buffers {
			shmName := fmt.Sprintf("tuios-crash-%d-%d-%d", os.Getpid(), si, b)
			shmPath := "/dev/shm/" + shmName
			shmData := make([]byte, shape.pxW*shape.pxH*4)
			for i := range shmData {
				shmData[i] = byte(i + b*37)
			}
			srcPath := filepath.Join(tempDir, fmt.Sprintf("buf-%d-%d.rgba", si, b))
			if err := os.WriteFile(srcPath, shmData, 0o600); err != nil {
				t.Fatalf("write frame source: %v", err)
			}
			if err := os.WriteFile(shmPath, shmData, 0o600); err != nil {
				t.Skipf("cannot write /dev/shm object: %v", err)
			}
			t.Cleanup(func() { _ = os.Remove(shmPath) })

			nameB64 := base64.StdEncoding.EncodeToString([]byte(shmName))
			framePath := filepath.Join(tempDir, fmt.Sprintf("frame-%d-%d.bin", si, b))
			frame := fmt.Sprintf("\x1b_Ga=T,t=s,i=1,f=32,s=%d,v=%d;%s\x1b\\", shape.pxW, shape.pxH, nameB64)
			if err := os.WriteFile(framePath, []byte(frame), 0o600); err != nil {
				t.Fatalf("write frame: %v", err)
			}
			fmt.Fprintf(&script, "%d) cp %s %s; cat %s ;;\n", b, srcPath, shmPath, framePath)
		}
		script.WriteString("esac; done) &\n")
		script.WriteString("exec yes " + textMarker + "\n")

		floodScripts[si] = filepath.Join(tempDir, fmt.Sprintf("flood-%d.sh", si))
		if err := os.WriteFile(floodScripts[si], []byte(script.String()), 0o700); err != nil {
			t.Fatalf("write flood script: %v", err)
		}
	}

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
			NoAuth:    true,
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
	for i := range numSessions {
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

	var totalRead atomic.Int64
	var markerCount atomic.Int64
	var graphicsAPC atomic.Bool
	streamClosed := make(chan struct{})
	go func() {
		marker := []byte(textMarker)
		gmark := []byte("\x1b_G")
		matched := 0
		buf := make([]byte, 64*1024)
		for {
			n, rerr := stdout.Read(buf)
			totalRead.Add(int64(n))
			if !graphicsAPC.Load() && bytes.Contains(buf[:n], gmark) {
				graphicsAPC.Store(true)
			}
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

	command := fmt.Sprintf("\x15sh %s\r", floodScript)
	graphicsSeen, textSeen := false, false
	attempts := 20
	if raceEnabled {
		attempts = 60
	}
	for attempt := 0; attempt < attempts && !(graphicsSeen && textSeen); attempt++ {
		if _, err := fmt.Fprint(stdin, command); err != nil {
			return fmt.Errorf("write command: %w", err)
		}
		waitUntil := time.Now().Add(1 * time.Second)
		for time.Now().Before(waitUntil) {
			if graphicsAPC.Load() {
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
				return fmt.Errorf("SSH stdout closed while starting the kitty shm flood: session torn down (%s, read %d bytes)",
					teardownReason(), totalRead.Load())
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	if !graphicsSeen {
		return fmt.Errorf("could not start the floods in the pane (graphics=%v text=%v)", graphicsSeen, textSeen)
	}

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
		if !graphicsAPC.Load() {
			return fmt.Errorf("no kitty graphics reached the client (read %d bytes); flood did not run", totalRead.Load())
		}
		return nil
	}
}
