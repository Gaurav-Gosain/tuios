package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// pane-view shows one tuios pane in the terminal it runs in, with no tuios
// around it: the pane's bytes go straight to the terminal, and the terminal's
// input goes straight to the pane. A terminal that draws its own panes (Tern,
// iTerm2, a GUI) runs one pane-view per tuios pane and so gives each its own
// scrollback, selection and search. It rides stream-pane, so it never
// attaches: the session's clients do not change.
//
// The daemon makes the decisions that need its lock. pane-view's lease is a
// viewer's (fit): while no client shows the session the pane takes the size
// of the largest viewer, and goes back to its own size when the last viewer
// leaves. And pane-view reports the terminal it runs in (its colours and
// graphics), which the daemon answers the pane's queries from while no client
// shows the session.

// paneViewLeaseRenew is how often the size lease is sent again. The daemon
// drops a lease it has not heard of for 30 seconds.
const paneViewLeaseRenew = 10 * time.Second

// paneViewSizePoll is how often the terminal size is read. Polling works on
// every platform, and costs one ioctl.
const paneViewSizePoll = 200 * time.Millisecond

// paneViewProbeWait bounds how long pane-view waits for the terminal to
// answer what it is. A terminal that answers nothing is reported as unknown.
const paneViewProbeWait = 1500 * time.Millisecond

// paneViewReset leaves the terminal as pane-view found it: DECSTR first, then
// the main screen, no mouse or focus reports, no bracketed paste, kitty
// keyboard flags and modifyOtherKeys off, normal cursor keys and keypad, the
// ASCII character set, the whole screen as the scroll region, the default
// cursor shape, a visible cursor and the default pen.
const paneViewReset = "\x1b[!p\x1b[?1049l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1004l" +
	"\x1b[?2004l\x1b[=0;1u\x1b[>4m\x1b[?1l\x1b>\x1b(B\x1b[r\x1b[0 q\x1b[?25h\x1b[0m"

// paneViewClear readies the terminal for a snapshot: the reset, then the
// screen and scrollback cleared.
const paneViewClear = paneViewReset + "\x1b[H\x1b[2J\x1b[3J"

// Exit statuses. A pane's program that exited gives its own status, and one a
// signal ended gives 128 plus the signal, as a shell reports it.
const (
	// paneViewClosed is the status when the pane's window closed while its
	// program still ran: the pane went, and nothing failed.
	paneViewClosed = 3
)

func newPaneViewCommand() *cobra.Command {
	var sessionName, window, size string
	cmd := &cobra.Command{
		Use:   "pane-view",
		Short: "Show one pane in this terminal, with no tuios around it",
		Long: `Show one tuios pane in this terminal, with no tuios around it.

The pane's output goes to this terminal as it is, and what you type goes to
the pane. Ctrl-C and every other key reach the program in the pane. Close
this terminal, or end pane-view with a signal, to stop viewing. The pane and
its session keep running.

pane-view does not attach. With --size follow (the default), the pane takes
this terminal's size while no tuios client shows the session; with several
viewers, the largest. When the last viewer leaves, the pane goes back to its
own size. While a client shows the session, the pane is only made smaller to
fit this terminal. With --size pane, the pane keeps its own size and is only
made smaller.

pane-view tells the daemon this terminal's colours and graphics, so a
program in the pane that asks sees this terminal while no client shows the
session.

Exit status: the status of the pane's program, 128 plus the signal that
ended it, 3 when the pane's window closed with its program still running,
and 1 for an error.`,
		Example: `  # Show the pane named build of the session work
  tuios pane-view -s work -w build

  # Keep the pane at the size the session gives it
  tuios pane-view -s work -w build --size pane`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if window == "" {
				return fmt.Errorf("pane-view needs a pane: pass -w with its id or name")
			}
			if size != "follow" && size != "pane" {
				return fmt.Errorf("--size takes follow or pane, not %q", size)
			}
			return runPaneView(sessionName, window, size == "follow")
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The pane to show, by id or name")
	cmd.Flags().StringVar(&size, "size", "follow", "follow: the pane takes this terminal's size when it can. pane: the pane keeps its size")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// paneView is one run of pane-view.
type paneView struct {
	session   string
	sessionID string
	window    string
	follow    bool
	viewer    *session.ViewerReport
	in        *os.File
	out       *os.File
	keys      <-chan []byte
	// pending is what was typed while the probe waited.
	pending []byte

	mu     sync.Mutex
	stream *session.PaneStream
	seq    uint64
	bootID string
	cols   int
	rows   int
	filter queryFilter
}

func runPaneView(sessionName, window string, follow bool) error {
	in, out := os.Stdin, os.Stdout
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return fmt.Errorf("pane-view needs a terminal on its input and output")
	}
	v := &paneView{session: sessionName, window: window, follow: follow, in: in, out: out}
	v.cols, v.rows = v.termSize()

	old, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return fmt.Errorf("failed to put the terminal in raw mode: %w", err)
	}
	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			_, _ = io.WriteString(out, paneViewReset)
			_ = term.Restore(int(in.Fd()), old)
		})
	}
	defer restore()
	// A signal that ends pane-view leaves the terminal as it found it. In
	// raw mode Ctrl-C reaches the pane as a byte, so these come from outside:
	// the terminal closing (SIGHUP), kill, or a script.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, paneViewSignals...)
	go func() {
		sig := <-sigs
		restore()
		os.Exit(128 + signalNumber(sig))
	}()

	keys := make(chan []byte, 16)
	v.keys = keys
	go readKeys(in, keys)
	v.viewer = v.probe()

	reply, err := v.connect()
	if err != nil {
		return err
	}
	v.session, v.sessionID, v.window = reply.Session, reply.SessionID, reply.Window
	if reply.Title != "" {
		_, _ = fmt.Fprintf(out, "\x1b]2;%s\x07", sanitizeTitle(reply.Title))
	}

	go v.pumpInput()
	go v.watchSize()
	go v.renewLease()

	for {
		end, err := v.pumpOutput()
		if end == "the daemon stopped" {
			// A restart or an upgrade: resume on the next daemon, which
			// restores the session.
			end, err = "", errors.New(end)
		}
		if end != "" {
			restore()
			return paneViewExit(end)
		}
		// The connection dropped. Resume from the last seq the terminal got.
		if rerr := v.reconnect(); rerr != nil {
			restore()
			return fmt.Errorf("lost the pane stream (%v) and could not resume it: %w", err, rerr)
		}
	}
}

// paneViewExit turns the stream's end reason into pane-view's exit.
func paneViewExit(end string) error {
	if code, ok := strings.CutPrefix(end, "exited "); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(code)); err == nil {
			if n == 0 {
				return nil
			}
			if n < 0 {
				n = 1
			}
			return &statusError{code: n}
		}
	}
	if sig, ok := strings.CutPrefix(end, "killed "); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(sig)); err == nil && n > 0 {
			return &statusError{code: 128 + n}
		}
	}
	if end == "closed" {
		return &statusError{code: paneViewClosed}
	}
	return fmt.Errorf("the pane stream ended: %s", end)
}

func (v *paneView) termSize() (int, int) {
	cols, rows, err := term.GetSize(int(v.out.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		return 80, 24
	}
	return min(cols, 4096), min(rows, 4096)
}

// readKeys reads the terminal's input for the whole run: first the answers
// to the probe, then keys.
func readKeys(in *os.File, keys chan<- []byte) {
	buf := make([]byte, session.PaneStreamMaxInput)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			keys <- append([]byte(nil), buf[:n]...)
		}
		if err != nil {
			close(keys)
			return
		}
	}
}

// paneViewProbeQuery asks the terminal for its default colours, its sixteen,
// its name and kitty graphics, and ends with DA1, which every terminal
// answers and which says whether it draws sixel. The answers come back in
// order, so DA1's marks the end.
var paneViewProbeQuery = func() string {
	var b strings.Builder
	b.WriteString("\x1b]10;?\x1b\\\x1b]11;?\x1b\\\x1b]12;?\x1b\\")
	for i := range 16 {
		fmt.Fprintf(&b, "\x1b]4;%d;?\x1b\\", i)
	}
	b.WriteString("\x1b[>q\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c")
	return b.String()
}()

var (
	probeColor = regexp.MustCompile(`\x1b\](10|11|12|4;(\d+));rgba?:([0-9a-fA-F]+)/([0-9a-fA-F]+)/([0-9a-fA-F]+)(?:/[0-9a-fA-F]+)?(?:\x07|\x1b\\)`)
	probeDA1   = regexp.MustCompile(`\x1b\[\?([0-9;]*)c`)
	probeKitty = regexp.MustCompile(`\x1b_Gi=31;OK\x1b\\`)
)

// probe asks the terminal what it is and reads the answers. Bytes typed
// while it waits are kept and typed into the pane later.
func (v *paneView) probe() *session.ViewerReport {
	_, _ = io.WriteString(v.out, paneViewProbeQuery)
	var got []byte
	deadline := time.After(paneViewProbeWait)
	for {
		select {
		case b, ok := <-v.keys:
			if !ok {
				return parseProbe(got)
			}
			got = append(got, b...)
			if loc := probeDA1.FindIndex(got); loc != nil {
				r := parseProbe(got[:loc[1]])
				v.pending = stripProbe(got)
				return r
			}
		case <-deadline:
			v.pending = stripProbe(got)
			return parseProbe(got)
		}
	}
}

// probeAnswer matches any answer the probe can draw: an OSC, DCS or APC
// string, and DA1.
var probeAnswer = regexp.MustCompile(`\x1b[\]P_][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[\?[0-9;]*c`)

// stripProbe removes the probe's answers from what the terminal sent while
// the probe waited, leaving what was typed.
func stripProbe(b []byte) []byte {
	return probeAnswer.ReplaceAll(b, nil)
}

// parseProbe reads the answers to paneViewProbeQuery.
func parseProbe(b []byte) *session.ViewerReport {
	r := &session.ViewerReport{}
	var pal [16]string
	for _, m := range probeColor.FindAllSubmatch(b, -1) {
		hex := "#" + probeHex(m[3]) + probeHex(m[4]) + probeHex(m[5])
		switch string(m[1]) {
		case "10":
			r.Fg = hex
		case "11":
			r.Bg = hex
		default:
			if i, err := strconv.Atoi(string(m[2])); err == nil && i >= 0 && i < 16 {
				pal[i] = hex
			}
		}
	}
	if pal != [16]string{} {
		r.Palette = strings.Join(pal[:], ",")
	}
	if m := probeDA1.FindSubmatch(b); m != nil {
		for p := range strings.SplitSeq(string(m[1]), ";") {
			if p == "4" {
				r.Sixel = true
			}
		}
	}
	r.Kitty = probeKitty.Match(b)
	return r
}

// probeHex reads one X11 colour component of 1 to 4 hex digits as two.
func probeHex(c []byte) string {
	n, err := strconv.ParseUint(string(c), 16, 32)
	if err != nil || len(c) == 0 {
		return "00"
	}
	maxv := uint64(1)<<(4*len(c)) - 1
	return fmt.Sprintf("%02x", n*255/maxv)
}

// errPaneViewPermanent wraps an error a reconnect cannot fix.
type errPaneViewPermanent struct{ err error }

func (e *errPaneViewPermanent) Error() string { return e.err.Error() }
func (e *errPaneViewPermanent) Unwrap() error { return e.err }

// permanentCodes are the verb errors a retry cannot fix: the pane or the
// session is gone, or the caller may not read it.
var permanentCodes = map[string]bool{
	session.ErrVerbWindowNotFound:  true,
	session.ErrVerbSessionNotFound: true,
	session.ErrVerbInvalidParams:   true,
	session.ErrVerbForbidden:       true,
}

// connect opens the stream, resuming from v.seq when there is one.
func (v *paneView) connect() (session.PaneStreamReply, error) {
	client, err := dialVerb()
	if err != nil {
		return session.PaneStreamReply{}, err
	}
	params := map[string]any{"window": v.window, "lease_cols": v.cols, "lease_rows": v.rows, "fit": v.follow}
	if v.viewer != nil {
		params["viewer"] = v.viewer
	}
	if v.sessionID != "" {
		params["session_id"] = v.sessionID
	} else if v.session != "" {
		params["session"] = v.session
	}
	v.mu.Lock()
	if v.bootID != "" {
		params["from_seq"] = v.seq
		params["boot_id"] = v.bootID
	}
	v.mu.Unlock()
	stream, reply, err := client.OpenPaneStream(params)
	if err != nil {
		_ = client.Close()
		var verr *session.VerbCallError
		if errors.As(err, &verr) && permanentCodes[verr.Code] {
			return reply, &errPaneViewPermanent{err: err}
		}
		return reply, err
	}
	v.mu.Lock()
	v.stream = stream
	v.bootID = reply.BootID
	v.seq = reply.Seq
	v.mu.Unlock()
	return reply, nil
}

func (v *paneView) reconnect() error {
	var err error
	for attempt := range 20 {
		time.Sleep(time.Duration(min(200*(attempt+1), 2000)) * time.Millisecond)
		if _, err = v.connect(); err == nil {
			return nil
		}
		var perm *errPaneViewPermanent
		if errors.As(err, &perm) {
			return err
		}
	}
	return err
}

func (v *paneView) current() *session.PaneStream {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stream
}

// pumpOutput copies frames to the terminal until the stream ends or drops.
// It returns the end message of an X frame, or the error that dropped it.
func (v *paneView) pumpOutput() (string, error) {
	stream := v.current()
	for {
		kind, payload, err := stream.ReadFrame()
		if err != nil {
			return "", err
		}
		switch kind {
		case session.PaneFrameSnapshot:
			seq, _ := session.PaneFrameSeq(payload)
			v.setSeq(seq)
			if len(payload) >= 12 {
				_, _ = io.WriteString(v.out, paneViewClear)
				v.filter = queryFilter{}
				_, _ = v.out.Write(v.filter.Write(payload[12:]))
			}
		case session.PaneFrameOutput:
			seq, _ := session.PaneFrameSeq(payload)
			if len(payload) >= 8 {
				_, _ = v.out.Write(v.filter.Write(payload[8:]))
			}
			v.setSeq(seq)
		case session.PaneFrameResize:
			seq, _ := session.PaneFrameSeq(payload)
			v.setSeq(seq)
		case session.PaneFrameError:
			var e struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(payload, &e) == nil && e.Code == "lease_expired" {
				cols, rows := v.size()
				_ = stream.Lease(cols, rows)
			}
		case session.PaneFrameEnd:
			_ = stream.Close()
			return string(payload), nil
		}
	}
}

func (v *paneView) setSeq(seq uint64) {
	v.mu.Lock()
	v.seq = seq
	v.mu.Unlock()
}

func (v *paneView) size() (int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cols, v.rows
}

// pumpInput types what the terminal sends into the pane.
func (v *paneView) pumpInput() {
	if len(v.pending) > 0 {
		_ = v.current().Input(v.pending)
	}
	for b := range v.keys {
		if err := v.current().Input(b); err != nil {
			// The output side sees the drop and reconnects; this keystroke
			// is lost with the connection.
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// watchSize follows the terminal's size with a new lease. The daemon decides
// what the lease does to the pane.
func (v *paneView) watchSize() {
	for range time.Tick(paneViewSizePoll) {
		cols, rows := v.termSize()
		v.mu.Lock()
		changed := cols != v.cols || rows != v.rows
		v.cols, v.rows = cols, rows
		v.mu.Unlock()
		if changed {
			_ = v.current().Lease(cols, rows)
		}
	}
}

func (v *paneView) renewLease() {
	for range time.Tick(paneViewLeaseRenew) {
		cols, rows := v.size()
		_ = v.current().Lease(cols, rows)
	}
}

// sanitizeTitle keeps a title from ending the OSC it is sent in.
func sanitizeTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
