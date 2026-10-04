package learnssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestMain lets the test binary be the session process too: the server
// under test runs os.Args[0] with "session", as the real one runs itself.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "session" {
		if err := RunSession(os.Stdin, os.Stdout, os.NewFile(3, "ctl-in"), os.NewFile(4, "ctl-out")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "selfcheck" {
		if err := SelfCheck(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "sandboxcheck" {
		os.Exit(sandboxCheck())
	}
	os.Exit(m.Run())
}

// startServer runs a server on a free loopback port with cfg's limits.
func startServer(t *testing.T, edit func(*Config)) string {
	t.Helper()
	cfg := Defaults()
	cfg.StateDir = t.TempDir()
	cfg.Exe = os.Args[0]
	cfg.ConnPerMinute, cfg.ConnBurst = 600, 50
	// A race build uses cgo, where the sandbox cannot cover every thread.
	// TestSelfCheck checks the sandbox on its own.
	cfg.AllowNoSandbox = true
	if edit != nil {
		edit(&cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("TUIOS_LEARN_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	srv, err := NewServer(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return ln.Addr().String()
}

// client is a person at a terminal: an SSH client with a pty, and a VT
// emulator standing in for their screen.
type client struct {
	t     *testing.T
	conn  *gossh.Client
	sess  *gossh.Session
	in    io.WriteCloser
	mu    sync.Mutex
	emu   *vt.Emulator
	ended chan struct{}
}

func sshConfig(user string, withKey bool) *gossh.ClientConfig {
	cfg := &gossh.ClientConfig{
		User:            user,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	if withKey {
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		signer, _ := gossh.NewSignerFromKey(priv)
		cfg.Auth = append(cfg.Auth, gossh.PublicKeys(signer))
	}
	cfg.Auth = append(cfg.Auth, gossh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) {
		return nil, nil
	}))
	return cfg
}

func dial(t *testing.T, addr, user string, w, h int) *client {
	t.Helper()
	conn, err := gossh.Dial("tcp", addr, sshConfig(user, true))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sess, err := conn.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RequestPty("xterm-256color", h, w, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	out, _ := sess.StdoutPipe()
	in, _ := sess.StdinPipe()
	c := &client{t: t, conn: conn, sess: sess, in: in, emu: vt.NewEmulator(w, h), ended: make(chan struct{})}
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := out.Read(buf)
			if n > 0 {
				c.mu.Lock()
				_, _ = c.emu.Write(buf[:n])
				c.mu.Unlock()
			}
			if err != nil {
				close(c.ended)
				return
			}
		}
	}()
	// The emulator's answers to the program's queries go back as input.
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := c.emu.Read(buf)
			if n > 0 {
				_, _ = in.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close(); conn.Close() })
	return c
}

func (c *client) screen() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.emu.String()
}

func (c *client) send(s string) {
	if _, err := io.WriteString(c.in, s); err != nil {
		c.t.Logf("send: %v", err)
	}
}

// waitFor waits until the screen shows every one of want.
func (c *client) waitFor(timeout time.Duration, want ...string) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s := c.screen()
		all := true
		for _, w := range want {
			if !strings.Contains(s, w) {
				all = false
				break
			}
		}
		if all {
			return true
		}
		time.Sleep(40 * time.Millisecond)
	}
	return false
}

func (c *client) mustSee(timeout time.Duration, want ...string) {
	c.t.Helper()
	if !c.waitFor(timeout, want...) {
		c.t.Fatalf("screen never showed %q. It shows:\n%s", want, c.screen())
	}
}

// frame saves the screen as evidence when TUIOS_LEARN_FRAMES names a
// directory. A frame is one screen of text, so its size is bounded.
func (c *client) frame(name string) {
	dir := os.Getenv("TUIOS_LEARN_FRAMES")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, name+".txt"), []byte(c.screen()+"\n"), 0o644)
}

// driveTrack plays every step of a track the way "show me" would, and
// reports the steps that did not complete.
func driveTrack(t *testing.T, c *client, tr *lessons.Track) []string {
	var failed []string
	for i, st := range tr.Steps {
		st := st.ForSSH()
		c.mustSee(8*time.Second, "› "+st.Title)
		// Keys wait until the step's scene is set.
		time.Sleep(100 * time.Millisecond)
		deadline := time.Now().Add(5 * time.Second)
		for strings.Contains(c.screen(), "Getting things ready") && time.Now().Before(deadline) {
			time.Sleep(40 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		if st.ID == "jump" {
			// The fake agent thinks for three seconds before it asks.
			time.Sleep(4 * time.Second)
		}
		if st.Explainer != nil {
			c.send("\r")
		} else if st.ShowMe != "" {
			c.send(st.ShowMe)
		} else {
			for _, k := range st.Keys {
				c.send(lessons.KeyBytes(k))
				time.Sleep(250 * time.Millisecond)
			}
		}
		next := "Chapter done"
		if i+1 < len(tr.Steps) {
			next = "› " + tr.Steps[i+1].ForSSH().Title
		}
		// A tape takes a while to play.
		if !c.waitFor(20*time.Second, next) {
			failed = append(failed, st.ID)
			c.frame("stuck-" + tr.ID + "-" + st.ID)
			c.send("\x1bOS") // F4: skip it
		}
	}
	return failed
}

func TestTourTwoChaptersAndAChallenge(t *testing.T) {
	addr := startServer(t, nil)
	c := dial(t, addr, "alice", 120, 40)
	c.mustSee(10*time.Second, "Hi alice!", "Start the tour")
	c.frame("01-welcome")

	file, err := lessons.Load()
	if err != nil {
		t.Fatal(err)
	}

	// Chapter 1: the basics, from the welcome screen.
	c.send("\r")
	c.mustSee(8*time.Second, "Chapter 1 of", "› Open a window")
	c.frame("02-lesson-first-step")
	if failed := driveTrack(t, c, &file.Tracks[0]); len(failed) > 0 {
		t.Errorf("basics: steps that did not complete: %v", failed)
	}
	c.mustSee(8*time.Second, "Chapter done: The basics")
	c.frame("03-chapter-done")

	// Chapter 2, straight from the done screen.
	c.send("\r")
	c.mustSee(8*time.Second, "Chapter 2 of")
	c.frame("04-chapter-two")
	if failed := driveTrack(t, c, &file.Tracks[1]); len(failed) > 0 {
		t.Errorf("%s: steps that did not complete: %v", file.Tracks[1].ID, failed)
	}
	c.mustSee(8*time.Second, "Chapter done: "+file.Tracks[1].Title)

	// A timed challenge from the menu.
	c.send("m")
	c.mustSee(5*time.Second, "Timed challenges")
	c.send("3")
	c.mustSee(5*time.Second, "Four tiles", "target 20.0 s")
	c.send("1")
	c.mustSee(8*time.Second, "Go!")
	c.frame("05-challenge-running")
	// A person's pace: a time under a second is a script, and stays off
	// the board.
	for range 4 {
		c.send("n")
		time.Sleep(300 * time.Millisecond)
	}
	c.send("t")
	c.mustSee(8*time.Second, "Done in")
	c.frame("06-challenge-finished")
	c.mustSee(8*time.Second, "You beat the target", "A new personal best!", "number 1 on the board")
	c.frame("07-challenge-result")

	// Show the name on the board.
	c.send("p")
	c.mustSee(5*time.Second, "Your name is on the board.")
	c.send("m")
	c.mustSee(5*time.Second, "Hi alice!")
	c.send("4")
	c.mustSee(5*time.Second, "The ten best times", " 1. alice")
	c.frame("08-leaderboard")

	c.send("m")
	c.mustSee(5*time.Second, "Hi alice!")
	c.send("5")
	c.mustSee(5*time.Second, "brew install tuios")
	c.frame("09-get-tuios")
}

// TestEveryTrack walks every chapter with its own keys, which checks every
// matcher in lessons.json against the real tuios.
func TestEveryTrack(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	file, err := lessons.Load()
	if err != nil {
		t.Fatal(err)
	}
	addr := startServer(t, func(c *Config) { c.MaxPerIP = 20 })
	for i := range file.Tracks {
		tr := &file.Tracks[i]
		t.Run(tr.ID, func(t *testing.T) {
			c := dial(t, addr, "walker", 120, 40)
			c.mustSee(10*time.Second, "Pick a chapter")
			c.send("2")
			c.mustSee(5*time.Second, tr.Title)
			c.send(fmt.Sprint(i + 1))
			if failed := driveTrack(t, c, tr); len(failed) > 0 {
				t.Errorf("steps that did not complete: %v", failed)
			}
		})
	}
}

// TestMouseClicksMenu clicks a menu row and a card button.
func TestMouseClicksMenu(t *testing.T) {
	addr := startServer(t, nil)
	c := dial(t, addr, "clicker", 120, 40)
	c.mustSee(10*time.Second, "Pick a chapter")
	click := func(label string) {
		t.Helper()
		lines := strings.Split(c.screen(), "\n")
		for y, l := range lines {
			if x := strings.Index(l, label); x >= 0 {
				col := len([]rune(l[:x])) + 1
				c.send(fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", col+1, y+1, col+1, y+1))
				return
			}
		}
		t.Fatalf("no %q on screen:\n%s", label, c.screen())
	}
	click("Pick a chapter")
	c.mustSee(5*time.Second, "Each chapter takes a few minutes")
	click("The basics")
	c.mustSee(8*time.Second, "› Open a window")
	time.Sleep(time.Second)
	click("F2 menu")
	c.mustSee(5*time.Second, "Hi clicker!")
}
