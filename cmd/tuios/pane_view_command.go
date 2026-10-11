package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
// attaches: the session's size and its clients do not change.

// paneViewLeaseRenew is how often the size lease is sent again. The daemon
// drops a lease it has not heard of for 30 seconds.
const paneViewLeaseRenew = 10 * time.Second

// paneViewSizePoll is how often the terminal size is read. Polling works on
// every platform, and costs one ioctl.
const paneViewSizePoll = 200 * time.Millisecond

// paneViewReset leaves the terminal as pane-view found it: main screen, no
// mouse or focus reports, no bracketed paste, kitty keyboard flags off,
// normal cursor keys and keypad, a visible cursor and the default pen.
const paneViewReset = "\x1b[?1049l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1004l" +
	"\x1b[?2004l\x1b[=0;1u\x1b[?1l\x1b>\x1b[?25h\x1b[0m"

// paneViewClear readies the terminal for a snapshot: modes reset (DECSTR),
// the main screen, and the screen and scrollback cleared.
const paneViewClear = "\x1b[!p" + paneViewReset + "\x1b[H\x1b[2J\x1b[3J"

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

pane-view does not attach. The session's size and its clients do not change.
With --size follow (the default), the pane takes this terminal's size while
no tuios client shows the session, and never grows past the size a client
asks for while one does. With --size pane, the pane keeps its own size and is
only made smaller to fit this terminal.

A terminal that draws its own panes runs one pane-view for each tuios pane.
Each pane then gets the terminal's own scrollback, selection and search.`,
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
	session string
	window  string
	follow  bool
	in      *os.File
	out     *os.File

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

	reply, err := v.connect()
	if err != nil {
		return err
	}
	// The resolved names, so a reconnect finds the same pane after a rename.
	v.session, v.window = reply.Session, reply.Window
	v.fit()

	old, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return fmt.Errorf("failed to put the terminal in raw mode: %w", err)
	}
	defer func() {
		_, _ = io.WriteString(out, paneViewReset)
		_ = term.Restore(int(in.Fd()), old)
	}()
	if reply.Title != "" {
		_, _ = fmt.Fprintf(out, "\x1b]2;%s\x07", sanitizeTitle(reply.Title))
	}

	go v.pumpInput()
	go v.watchSize()
	go v.renewLease()

	for {
		end, err := v.pumpOutput()
		if end != "" {
			if code, ok := strings.CutPrefix(end, "exited "); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(code)); err == nil && n != 0 {
					// The pane's status, with nothing more to print.
					return &statusError{code: n}
				}
				return nil
			}
			_, _ = io.WriteString(out, paneViewReset)
			return fmt.Errorf("the pane stream ended: %s", end)
		}
		// The connection dropped. Resume from the last seq the terminal got.
		if rerr := v.reconnect(); rerr != nil {
			_, _ = io.WriteString(out, paneViewReset)
			return fmt.Errorf("lost the pane stream (%v) and could not resume it: %w", err, rerr)
		}
	}
}

func (v *paneView) termSize() (int, int) {
	cols, rows, err := term.GetSize(int(v.out.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		return 80, 24
	}
	return min(cols, 4096), min(rows, 4096)
}

// connect opens the stream, resuming from v.seq when there is one.
func (v *paneView) connect() (session.PaneStreamReply, error) {
	client, err := dialVerb()
	if err != nil {
		return session.PaneStreamReply{}, err
	}
	params := map[string]any{"window": v.window, "lease_cols": v.cols, "lease_rows": v.rows}
	if v.session != "" {
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
	buf := make([]byte, session.PaneStreamMaxInput)
	for {
		n, err := v.in.Read(buf)
		if n > 0 {
			if werr := v.current().Input(buf[:n]); werr != nil {
				// The output side sees the drop and reconnects; this
				// keystroke is lost with the connection.
				time.Sleep(50 * time.Millisecond)
			}
		}
		if err != nil {
			return
		}
	}
}

// watchSize follows the terminal's size: a new lease, and the pane resized
// to fit when nothing else decides its size.
func (v *paneView) watchSize() {
	for range time.Tick(paneViewSizePoll) {
		cols, rows := v.termSize()
		v.mu.Lock()
		changed := cols != v.cols || rows != v.rows
		v.cols, v.rows = cols, rows
		v.mu.Unlock()
		if changed {
			_ = v.current().Lease(cols, rows)
			v.fit()
		}
	}
}

func (v *paneView) renewLease() {
	for range time.Tick(paneViewLeaseRenew) {
		cols, rows := v.size()
		_ = v.current().Lease(cols, rows)
	}
}

// fit gives the pane this terminal's size while no tuios client shows its
// session. A lease only makes a pane smaller, so without this a pane-view in
// a terminal larger than the pane would show it in a corner. With a client
// attached the client decides, and the lease keeps the pane within this
// terminal.
func (v *paneView) fit() {
	if !v.follow {
		return
	}
	client, err := dialVerb()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call("list-sessions", nil)
	if err != nil {
		return
	}
	var list struct {
		Sessions []struct {
			Name     string `json:"name"`
			Attached bool   `json:"attached"`
		} `json:"sessions"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return
	}
	for _, s := range list.Sessions {
		if s.Name == v.session && s.Attached {
			return
		}
	}
	cols, rows := v.size()
	_, err = client.Call("resize", map[string]any{"session": v.session, "window": v.window, "width": cols, "height": rows})
	var verr *session.VerbCallError
	if err != nil && !errors.As(err, &verr) {
		return
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
