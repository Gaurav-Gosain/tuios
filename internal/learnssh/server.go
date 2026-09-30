package learnssh

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/ssh"
	"charm.land/wish/v2"
	gossh "golang.org/x/crypto/ssh"
)

// Config is the server's limits and places. Defaults() fills the values the
// deployment plan was measured with.
type Config struct {
	Listen   string // SSH address, such as ":22" or "127.0.0.1:2222"
	StateDir string // host key, bests, board and counters
	Health   string // HTTP address for /healthz, "" for none

	MaxSessions   int           // at once, over every address
	MaxPerIP      int           // at once, per address (IPv6: per /64)
	ConnPerMinute float64       // new connections per address per minute
	ConnBurst     float64       // and the burst above that rate
	MaxConns      int           // TCP connections at once, before and after login
	Idle          time.Duration // no input for this long ends a session
	MaxSession    time.Duration // a session ends after this long in any case
	SessionMemMiB int           // a session process over this RSS is stopped
	InputRate     float64       // bytes per second a client may send
	InputBurst    float64
	Exe           string // the binary to run sessions with; "" for this one
	// AllowNoSandbox starts the server even where the session sandbox does
	// not work. For a local test only.
	AllowNoSandbox bool
}

// Defaults are the values for learn.tuios.dev.
func Defaults() Config {
	return Config{
		Listen:        ":22",
		StateDir:      "/var/lib/tuios-learn",
		MaxSessions:   50,
		MaxPerIP:      3,
		ConnPerMinute: 10,
		ConnBurst:     5,
		MaxConns:      120,
		Idle:          10 * time.Minute,
		MaxSession:    30 * time.Minute,
		SessionMemMiB: 160,
		InputRate:     16 << 10,
		InputBurst:    64 << 10,
	}
}

// Server is the SSH front.
type Server struct {
	cfg    Config
	store  *store
	ips    *ipLimits
	log    *slog.Logger
	active atomic.Int64
	conns  atomic.Int64
	ssh    *ssh.Server
	exe    string
	wg     sync.WaitGroup
	start  time.Time
}

type ctxKey string

const (
	ctxOwner ctxKey = "owner" // the key hash, when the client used a key
	ctxChans ctxKey = "chans" // session channels open on the connection
)

// NewServer checks the config and prepares the state directory and host
// key. It does not listen yet.
func NewServer(cfg Config, log *slog.Logger) (*Server, error) {
	st, err := openStore(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("state directory: %w", err)
	}
	exe := cfg.Exe
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	if err := checkSandbox(exe); err != nil {
		if !cfg.AllowNoSandbox {
			return nil, err
		}
		log.Warn("sessions run without their sandbox", "err", err)
	}
	s := &Server{
		cfg: cfg, store: st, log: log, exe: exe, start: time.Now(),
		ips: newIPLimits(cfg.ConnPerMinute, cfg.ConnBurst, cfg.MaxPerIP),
	}
	srv, err := wish.NewServer(
		wish.WithAddress(cfg.Listen),
		wish.WithHostKeyPath(filepath.Join(cfg.StateDir, "host_ed25519")),
		wish.WithVersion("tuios-learn"),
		wish.WithMiddleware(s.handle, s.recover),
		wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			// Every key is welcome. It only files the person's best times.
			ctx.SetValue(ctxOwner, st.owner(gossh.FingerprintSHA256(key)))
			return true
		}),
		wish.WithKeyboardInteractiveAuth(func(ssh.Context, gossh.KeyboardInteractiveChallenge) bool {
			// No key, no password: anyone may come in.
			return true
		}),
		wish.WithIdleTimeout(cfg.Idle+2*time.Minute),
		wish.WithMaxTimeout(cfg.MaxSession+2*time.Minute),
		func(srv *ssh.Server) error {
			srv.HandshakeTimeout = 15 * time.Second
			srv.ConnCallback = s.onConn
			// Only "shell". An exec or a subsystem (sftp, scp) is refused.
			srv.SessionRequestCallback = func(_ ssh.Session, req string) bool { return req == "shell" }
			srv.PtyCallback = func(_ ssh.Context, p ssh.Pty) bool { return p.Term != "" && len(p.Term) <= 64 }
			// Forwarding stays off: nil callbacks refuse local and remote
			// port forwards, and no request handler serves tcpip-forward.
			srv.LocalPortForwardingCallback = nil
			srv.ReversePortForwardingCallback = nil
			srv.RequestHandlers = map[string]ssh.RequestHandler{}
			srv.SubsystemHandlers = map[string]ssh.SubsystemHandler{}
			srv.ChannelHandlers = map[string]ssh.ChannelHandler{"session": oneSession}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	s.ssh = srv
	return s, nil
}

// checkSandbox runs "exe selfcheck", which applies the session sandbox in a
// throwaway process and checks that it holds.
func checkSandbox(exe string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "selfcheck")
	cmd.Env = []string{"GOMAXPROCS=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("the session sandbox does not work here (%s): %s. "+
			"Build with CGO_ENABLED=0 and run on Linux 5.13 or later with Landlock on, "+
			"or pass --unsafe-no-sandbox for a local test", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SelfCheck is "tuios-learn selfcheck": it applies the session sandbox to
// this process and checks that a file can no longer be opened.
func SelfCheck() error {
	if err := sandbox(); err != nil {
		return err
	}
	if f, err := os.Open("/"); err == nil {
		f.Close()
		return errors.New("the sandbox is on, but / still opens")
	}
	return nil
}

// oneSession allows one session channel per connection. A client that opens
// more (ssh -M multiplexing, or a script) gets the rest refused.
func oneSession(srv *ssh.Server, conn *gossh.ServerConn, ch gossh.NewChannel, ctx ssh.Context) {
	n, _ := ctx.Value(ctxChans).(*atomic.Int32)
	if n == nil || n.Add(1) > 1 {
		if n != nil {
			n.Add(-1)
		}
		_ = ch.Reject(gossh.ResourceShortage, "one session per connection")
		return
	}
	defer n.Add(-1)
	ssh.DefaultSessionHandler(srv, conn, ch, ctx)
}

// onConn runs before the SSH handshake: the per-address connection rate,
// the cap on open connections, and the input rate limit.
func (s *Server) onConn(ctx ssh.Context, conn net.Conn) net.Conn {
	key := ipKey(conn.RemoteAddr())
	if !s.ips.allowConn(key, time.Now()) {
		s.store.count(func(st *Stats) { st.Refused["rate"]++ })
		return nil
	}
	if s.conns.Add(1) > int64(s.cfg.MaxConns) {
		s.conns.Add(-1)
		s.store.count(func(st *Stats) { st.Refused["conns"]++ })
		return nil
	}
	ctx.SetValue(ctxChans, new(atomic.Int32))
	return &limitedConn{
		Conn:    conn,
		b:       newBucket(s.cfg.InputRate, s.cfg.InputBurst, time.Now()),
		onClose: func() { s.conns.Add(-1) },
	}
}

// recover keeps one session's panic from ending the server.
func (s *Server) recover(next ssh.Handler) ssh.Handler {
	return func(sess ssh.Session) {
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("session panic", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			}
		}()
		next(sess)
	}
}

// ListenAndServe serves until ctx ends.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.log.Info("listening", "addr", ln.Addr().String(), "maxSessions", s.cfg.MaxSessions)
	var health *http.Server
	if s.cfg.Health != "" {
		health = &http.Server{Addr: s.cfg.Health, Handler: s.healthHandler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.log.Error("health endpoint", "err", err)
			}
		}()
	}
	go s.janitor(ctx)
	errc := make(chan error, 1)
	go func() { errc <- s.ssh.Serve(ln) }()
	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	s.log.Info("shutting down", "active", s.active.Load())
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if health != nil {
		_ = health.Shutdown(sctx)
	}
	_ = s.ssh.Shutdown(sctx)
	s.wg.Wait()
	return s.store.flush()
}

func (s *Server) janitor(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.ips.sweep(now, 10*time.Minute)
			if err := s.store.flush(); err != nil {
				s.log.Error("write state", "err", err)
			}
		}
	}
}

// healthHandler serves /healthz: 200 and the counters while the server can
// take sessions, 503 when it is full.
func (s *Server) healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		active := s.active.Load()
		body := map[string]any{
			"status":      "ok",
			"active":      active,
			"maxSessions": s.cfg.MaxSessions,
			"connections": s.conns.Load(),
			"uptimeSecs":  int64(time.Since(s.start).Seconds()),
			"stats":       s.store.snapshot(),
		}
		w.Header().Set("Content-Type", "application/json")
		if active >= int64(s.cfg.MaxSessions) {
			body["status"] = "full"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	return mux
}

// say writes a line to the client and ends the session.
func say(sess ssh.Session, code int, text string) {
	_, _ = io.WriteString(sess, "\r\n  "+text+"\r\n\r\n")
	_ = sess.Exit(code)
}

// handle is one session: the checks, then a child process running the tour.
func (s *Server) handle(_ ssh.Handler) ssh.Handler {
	return func(sess ssh.Session) {
		sid := newSID()
		log := s.log.With("sid", sid)
		ptyReq, winch, ok := sess.Pty()
		if !ok {
			s.store.count(func(st *Stats) { st.Refused["nopty"]++ })
			say(sess, 1, "This tour needs a terminal. Connect with: ssh -t learn.tuios.dev")
			return
		}
		if ptyReq.Term == "dumb" {
			say(sess, 1, "This tour needs a terminal with colour and cursor control.")
			return
		}
		if n := s.active.Add(1); n > int64(s.cfg.MaxSessions) {
			s.active.Add(-1)
			s.store.count(func(st *Stats) { st.Refused["full"]++ })
			say(sess, 0, "The tour is full right now. Please try again in a few minutes.")
			return
		} else {
			s.store.count(func(st *Stats) {
				st.PeakActive = max(st.PeakActive, n)
			})
		}
		defer s.active.Add(-1)
		key := ipKey(sess.RemoteAddr())
		if !s.ips.acquire(key, time.Now()) {
			s.store.count(func(st *Stats) { st.Refused["perip"]++ })
			say(sess, 0, "You have too many sessions open. Close one and try again.")
			return
		}
		defer s.ips.release(key)
		s.wg.Add(1)
		defer s.wg.Done()

		owner, keyed := sess.Context().Value(ctxOwner).(string)
		if !keyed {
			owner = "s-" + sid
		}
		env := sess.Environ()
		color, in := probeColor(sess, ptyReq.Term, env)
		w, h := clampSize(ptyReq.Window.Width, ptyReq.Window.Height)
		s.store.count(func(st *Stats) {
			st.Sessions++
			if keyed {
				st.Keyed++
			}
		})
		log.Info("session start", "ip", logIP(sess.RemoteAddr()), "term", safeTerm(ptyReq.Term),
			"size", fmt.Sprintf("%dx%d", ptyReq.Window.Width, ptyReq.Window.Height), "color", color, "key", keyed)
		began := time.Now()
		reason := s.run(sess, log, runArgs{
			owner: owner, keyed: keyed, color: color, w: w, h: h, winch: winch, in: in,
		})
		secs := int64(time.Since(began).Seconds())
		s.store.count(func(st *Stats) {
			st.Ended[reason]++
			st.SessionSecs += secs
		})
		log.Info("session end", "reason", reason, "secs", secs)
		_ = sess.Exit(0)
	}
}

type runArgs struct {
	owner string
	keyed bool
	color string
	w, h  int
	winch <-chan ssh.Window
	in    <-chan []byte
}

// run starts the session's child process and serves it until it ends. It
// returns why the session ended.
func (s *Server) run(sess ssh.Session, log *slog.Logger, a runArgs) string {
	cmd := exec.Command(s.exe, "session")
	cmd.Dir = "/"
	cmd.Env = []string{
		"GOMAXPROCS=1",
		"GOMEMLIMIT=" + strconv.Itoa(s.cfg.SessionMemMiB*3/4) + "MiB",
		"GOTRACEBACK=single",
		"TZ=UTC",
		"PATH=/nonexistent",
		"HOME=/nonexistent",
	}
	setChildAttrs(cmd)
	toChildR, toChildW, err := os.Pipe()
	if err != nil {
		return "spawn"
	}
	fromChildR, fromChildW, err := os.Pipe()
	if err != nil {
		return "spawn"
	}
	cmd.ExtraFiles = []*os.File{toChildR, fromChildW}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		log.Error("spawn", "err", err)
		say(sess, 1, "The tour could not start. Please try again later.")
		return "spawn"
	}
	toChildR.Close()
	fromChildW.Close()
	defer toChildW.Close()

	ctl := newCtlWriter(toChildW)
	user := sess.User()
	if len(user) > 32 {
		user = user[:32]
	}
	_ = ctl.Send(Msg{
		T: MsgInit, User: s.store.boardName(user), Name: s.store.boardName(user),
		Term:  safeTerm(func() string { p, _, _ := sess.Pty(); return p.Term }()),
		Color: a.color, W: a.w, H: a.h, Bests: s.store.bestsFor(a.owner), Keyed: a.keyed,
		Board: s.store.publicBoard(), MaxMins: int(s.cfg.MaxSession.Minutes()),
	})

	done := make(chan struct{})
	var reason atomic.Value
	reason.Store("left")
	end := func(r string) {
		if reason.CompareAndSwap("left", r) {
			log.Info("ending", "why", r)
		}
	}

	// Output to the client.
	out := &serialWriter{w: sess}
	go func() {
		_, _ = io.Copy(out, stdout)
	}()
	// The child's log lines, bounded.
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 4096), 64<<10)
		n := 0
		for sc.Scan() {
			if n++; n <= 20 {
				line := sc.Text()
				if len(line) > 300 {
					line = line[:300]
				}
				log.Warn("child", "line", line)
			}
		}
	}()
	// Control messages from the child.
	go func() {
		_ = readCtl(fromChildR, func(m Msg) {
			switch m.T {
			case MsgTrackDone:
				if len(m.ID) <= 32 {
					s.store.count(func(st *Stats) { st.TracksDone[m.ID]++ })
				}
			case MsgChallenge:
				if findChallenge(m.ID) == nil || m.Ms <= 0 {
					return
				}
				best, rank := s.store.record(a.owner, a.keyed, s.store.boardName(sess.User()), m.ID, m.Ms)
				_ = ctl.Send(Msg{T: MsgResult, ID: m.ID, Ms: m.Ms, Best: best, Rank: rank, Board: s.store.publicBoard()})
			case MsgPublish:
				if findChallenge(m.ID) == nil {
					return
				}
				s.store.publish(a.owner, m.ID)
				_ = ctl.Send(Msg{T: MsgBoard, Board: s.store.publicBoard()})
			case MsgQuit:
				end("quit")
			}
		})
	}()

	// Input from the client, with the idle clock.
	var lastInput atomic.Int64
	lastInput.Store(time.Now().UnixNano())
	go func() {
		defer stdin.Close()
		for b := range a.in {
			lastInput.Store(time.Now().UnixNano())
			if _, err := stdin.Write(b); err != nil {
				return
			}
		}
	}()

	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	began := time.Now()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	warnedIdle, warnedEnd := false, false
	stop := func(r, text string) {
		end(r)
		_ = ctl.Send(Msg{T: MsgBye, Text: text})
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			killChild(cmd)
			<-done
		}
	}
	for {
		select {
		case <-done:
			if st := cmd.ProcessState; st != nil && !st.Success() {
				end("crash")
				_, _ = io.WriteString(out, resetTerminal)
				say(sess, 1, "The tour stopped by mistake. Please connect again.")
			}
			return reason.Load().(string)
		case <-sess.Context().Done():
			end("disconnect")
			killChild(cmd)
			<-done
			return reason.Load().(string)
		case win, ok := <-a.winch:
			if !ok {
				a.winch = nil
				continue
			}
			w, h := clampSize(win.Width, win.Height)
			_ = ctl.Send(Msg{T: MsgResize, W: w, H: h})
		case now := <-tick.C:
			idle := now.Sub(time.Unix(0, lastInput.Load()))
			switch {
			case idle >= s.cfg.Idle:
				stop("idle", "You were away for "+human(s.cfg.Idle)+", so the session ends here. Come back any time: ssh learn.tuios.dev")
				return reason.Load().(string)
			case idle >= s.cfg.Idle-time.Minute && !warnedIdle:
				warnedIdle = true
				_ = ctl.Send(Msg{T: MsgNotice, Text: "Still there? The session ends in one minute if you stay away."})
			case idle < s.cfg.Idle-time.Minute:
				warnedIdle = false
			}
			age := now.Sub(began)
			if age >= s.cfg.MaxSession {
				stop("maxtime", "Time is up for this session. Connect again for more: ssh learn.tuios.dev")
				return reason.Load().(string)
			}
			if age >= s.cfg.MaxSession-2*time.Minute && !warnedEnd {
				warnedEnd = true
				_ = ctl.Send(Msg{T: MsgNotice, Text: "Two minutes left in this session. You can connect again after it ends."})
			}
			if rss := rssMiB(cmd); rss > s.cfg.SessionMemMiB {
				log.Warn("session over memory limit", "rssMiB", rss)
				end("memory")
				killChild(cmd)
				<-done
				_, _ = io.WriteString(out, resetTerminal)
				say(sess, 1, "This session used too much memory and was stopped. Please connect again.")
				return "memory"
			}
		}
	}
}

// resetTerminal undoes what the tour turned on, for a session whose process
// was stopped before it could: the alternate screen, mouse reports, the kitty
// keyboard flags, bracketed paste, focus reports and a hidden cursor.
const resetTerminal = "\x1b[?1049l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l" +
	"\x1b[<u\x1b[?2004l\x1b[?1004l\x1b[0m\x1b[?25h"

// serialWriter serialises writes to the session, as internal/server does.
type serialWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *serialWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func newSID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// rssMiB is the process's resident memory, from /proc. 0 if unknown.
func rssMiB(cmd *exec.Cmd) int {
	if cmd.Process == nil {
		return 0
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	pages, _ := strconv.Atoi(f[1])
	return pages * os.Getpagesize() >> 20
}

// human is a duration as the reader sees it: "10 minutes", "30 seconds".
func human(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		if d == time.Minute {
			return "1 minute"
		}
		return strconv.Itoa(int(d.Minutes())) + " minutes"
	}
	return strconv.Itoa(int(d.Seconds())) + " seconds"
}
