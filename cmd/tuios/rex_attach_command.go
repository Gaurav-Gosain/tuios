//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/rexclient"
)

// rexSession is the label of the Rex session tuios opens shells in.
const rexSession = "tuios"

func newRexAttachCommand() *cobra.Command {
	var blockID string
	var bridge bool
	cmd := &cobra.Command{
		Use:    "rex-attach [TARGET]",
		Short:  "Run a Rex shell as the program of this pane",
		Hidden: true,
		Long:   `Run a shell of a Rex server as the program of this pane. tuios new-window --rex starts it.`,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			if !bridge {
				return runRexKeeper()
			}
			return runRexAttach(target, blockID)
		},
	}
	cmd.Flags().StringVar(&blockID, "block", "", "Open this running shell instead of a new one")
	cmd.Flags().BoolVar(&bridge, "bridge", false, "Run the bridge itself (rex-attach starts it)")
	_ = cmd.Flags().MarkHidden("bridge")
	return cmd
}

// runRexKeeper runs the bridge as a child and waits for it.
//
// A closing pane gets SIGHUP in each of its process groups and then SIGKILL
// for its own program at once (session.killPaneTree). The program of the pane
// has no time to close its Rex block, and a block lives on without a client.
// So the pane's program is this keeper, and the bridge is its child in the
// same process group: the child gets the SIGHUP, outlives the keeper's
// SIGKILL and closes the block.
func runRexKeeper() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	child := childproc.Command(self, append(os.Args[1:], "--bridge")...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	// The child acts on these. Caught here, not ignored: an ignored signal
	// stays ignored across exec.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT, syscall.SIGWINCH)
	defer signal.Stop(sigs)
	go func() {
		for range sigs {
		}
	}()
	if err := child.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

func runRexAttach(target, blockID string) error {
	// Caught from the start, so a pane closed while the shell starts still
	// closes it. No signal.Stop: the process ends when this returns, and
	// SIGHUP must not end it before the block is closed.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	// A closed pane leaves this process in a background group of a terminal
	// with no session leader, where restoring the terminal mode would stop it
	// with SIGTTOU. So that is ignored, and the block closes before the
	// terminal is restored.
	signal.Ignore(syscall.SIGTTOU)

	ep, err := rexEndpoint(target)
	if err != nil {
		return err
	}
	ctl, err := rexclient.Dial(ep)
	if err != nil {
		return err
	}
	defer func() { _ = ctl.Close() }()

	fd := int(os.Stdin.Fd())
	cols, rows, err := term.GetSize(fd)
	if err != nil || cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	var b rexclient.Block
	if blockID != "" {
		b, err = ctl.FindBlock(blockID, rexSession, "monstar")
	} else {
		cwd, _ := os.Getwd()
		b, err = ctl.NewShell(rexSession, cwd, rexclient.Size{Columns: cols, Rows: rows})
	}
	if err != nil {
		return err
	}
	if old, err := term.MakeRaw(fd); err == nil {
		defer func() { _ = term.Restore(fd, old) }()
	}
	// A new shell belongs to this pane from the start. A running shell
	// belongs to it once it shows here, so a failed reattach leaves it be.
	closeBlock := blockID == ""
	defer func() {
		if closeBlock {
			// A closing pane sends SIGHUP again when its terminal goes.
			signal.Ignore(syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
			_, _ = ctl.Call("block.close", b)
		}
	}()
	disown := func() error {
		closeBlock = false
		return ctl.Own(b, false)
	}
	if err := ctl.Own(b, true); err != nil {
		return err
	}
	_ = ctl.Resize(b, rexclient.Size{Columns: cols, Rows: rows})
	if blockID != "" {
		if err := rexSeed(ctl, b); err != nil {
			return err
		}
	}
	data, err := ctl.OpenData(b)
	if err != nil {
		return err
	}
	defer func() { _ = data.Close() }()
	closeBlock = true

	detach := make(chan struct{}, 1)
	go rexForwardInput(ctl, b, detach)
	out := make(chan []byte, 16)
	ended := make(chan error, 1)
	go func() {
		for {
			chunk, err := data.Read()
			if err != nil {
				ended <- err
				return
			}
			out <- chunk
		}
	}()
	// The block's own terminal on the Rex server answers the shell's
	// requests. The filter keeps them from this pane's terminal too, which
	// would answer again, and the bridge would type that answer in.
	var filter queryFilter
	for {
		select {
		case chunk := <-out:
			if _, err := os.Stdout.Write(filter.Write(chunk)); err != nil {
				return err
			}
		case err := <-ended:
			if errors.Is(err, rexclient.ErrBlockEnded) {
				closeBlock = false
				return nil
			}
			// A lost stream is not a closed pane: the shell stays for
			// --rex-block.
			_ = disown()
			return errfmt.Errorf("lost the Rex data stream, and the shell %s keeps running: %w", b.BlockID, err)
		case <-detach:
			return disown()
		case sig := <-sigs:
			if sig != syscall.SIGWINCH {
				return nil
			}
			if c, r, err := term.GetSize(fd); err == nil {
				_ = ctl.Resize(b, rexclient.Size{Columns: c, Rows: r})
			}
		case <-ctl.Done():
			return errors.New("the Rex server closed the control connection")
		}
	}
}

// rexEndpoint reads a target the way monstar does. A bare name is a host
// on the tailnet, at Rex's port.
func rexEndpoint(target string) (rexclient.Endpoint, error) {
	return rexclient.ParseEndpoint(target, os.Getenv("REX_SERVER"), os.Getenv("XDG_RUNTIME_DIR"), os.Getuid())
}

// rexSeed writes the block's screen and scrollback to this pane, as escape
// sequences, before the live output. The format call and the data stream are
// two cuts of the shell's output: output the shell writes between them is
// not shown. It is taken first, so nothing is drawn twice.
func rexSeed(ctl *rexclient.Control, b rexclient.Block) error {
	const chunk = 960 << 10
	const format = `{"format":"vt","max_bytes":983040,"extra_cursor":true,"extra_modes":true,` +
		`"extra_kitty_keyboard":true,"extra_keyboard":true,"extra_palette":true,"extra_scrolling_region":true,` +
		`"extra_tabstops":true,"extra_charsets":true,"extra_style":true,"extra_hyperlink":true,` +
		`"extra_protection":true,"extra_pwd":true}`
	type part struct {
		Content string `json:"content"`
		ID      uint64 `json:"id"`
		EOF     bool   `json:"eof"`
	}
	type read struct {
		ID       uint64 `json:"id"`
		Offset   int    `json:"offset"`
		MaxBytes int    `json:"max_bytes"`
	}
	raw, err := ctl.BlockCall(b, "com.superlogical.terminal.format", json.RawMessage(format))
	var p part
	if err == nil {
		err = jsonx.Unmarshal(raw, &p)
	}
	id, n := p.ID, 0
	buf := []byte("\x1b[H\x1b[2J\x1b[3J")
	for err == nil {
		buf = append(buf, p.Content...)
		n += len(p.Content)
		if id == 0 || p.EOF || p.Content == "" {
			break
		}
		if raw, err = ctl.BlockCall(b, "com.superlogical.terminal.blob_read", read{id, n, chunk}); err == nil {
			p = part{}
			err = jsonx.Unmarshal(raw, &p)
		}
	}
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(buf)
	return err
}

// rexForwardInput sends what is typed in the pane to the shell, as typed. It
// signals detach on the detach sequence and stops there.
//
// A tail of a read waits for the next one when it may start the detach
// sequence (two bytes or more: a lone ESC is a key and goes at once), or
// when it cuts a UTF-8 character. A tail that no read completes goes after
// rexTailWait, on a timer that exists only while a tail waits.
func rexForwardInput(ctl *rexclient.Control, b rexclient.Block, detach chan<- struct{}) {
	var (
		mu    sync.Mutex
		held  []byte
		timer *time.Timer
		gen   int
	)
	buf := make([]byte, 32<<10)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		gen++
		data := append(held, buf[:n]...)
		held = nil
		if i := bytes.Index(data, []byte(rexclient.DetachSequence)); i >= 0 {
			if i > 0 {
				rexSend(ctl, b, data[:i])
			}
			mu.Unlock()
			detach <- struct{}{}
			return
		}
		if k := rexTail(data); k > 0 {
			held = append([]byte(nil), data[len(data)-k:]...)
			data = data[:len(data)-k]
			g := gen
			timer = time.AfterFunc(rexTailWait, func() {
				mu.Lock()
				defer mu.Unlock()
				if g == gen && len(held) > 0 {
					rexSend(ctl, b, held)
					held = nil
				}
			})
		}
		if len(data) > 0 {
			rexSend(ctl, b, data)
		}
		mu.Unlock()
	}
}

// rexTailWait is how long a tail waits for the read that completes it.
const rexTailWait = 50 * time.Millisecond

// rexTail is how many bytes at the end of data wait for the next read.
func rexTail(data []byte) int {
	seq := rexclient.DetachSequence
	for k := min(len(data), len(seq)-1); k >= 2; k-- {
		if strings.HasPrefix(seq, string(data[len(data)-k:])) {
			return k
		}
	}
	// A legacy mouse report is six bytes, ESC [ M and three raw bytes. A
	// whole one at the end is complete. The start of one waits.
	x10 := []byte("\x1b[M")
	if len(data) >= 6 && bytes.Equal(data[len(data)-6:len(data)-3], x10) {
		return 0
	}
	if bytes.HasSuffix(data, x10[:2]) {
		return 2
	}
	off := max(0, len(data)-5)
	if i := bytes.LastIndex(data[off:], x10); i >= 0 {
		return len(data) - off - i
	}
	for k := 1; k <= min(len(data), utf8.UTFMax-1); k++ {
		if c := data[len(data)-k]; utf8.RuneStart(c) {
			if c >= utf8.RuneSelf && !utf8.FullRune(data[len(data)-k:]) {
				return k
			}
			return 0
		}
	}
	return 0
}

// rexSend types data into the block. Rex takes typed input as text, so a
// legacy mouse report whose raw bytes are not text, past column 95, goes as a
// mouse event, which the block encodes again for its program.
func rexSend(ctl *rexclient.Control, b rexclient.Block, data []byte) {
	start := 0
	for i := 0; ; i += 3 {
		j := bytes.Index(data[i:], []byte("\x1b[M"))
		if j < 0 || len(data) < i+j+6 {
			break
		}
		i += j
		if r := data[i+3 : i+6]; r[0]|r[1]|r[2] >= utf8.RuneSelf {
			if i > start {
				_ = ctl.Write(b, string(data[start:i]))
			}
			rexMouse(ctl, b, int(r[0])-32, int(r[1])-33, int(r[2])-33)
			start = i + 6
		}
	}
	if start < len(data) {
		_ = ctl.Write(b, string(data[start:]))
	}
}

// rexMouse sends a legacy mouse report's button code cb at cell x, y as a
// Rex mouse or wheel event.
func rexMouse(ctl *rexclient.Control, b rexclient.Block, cb, x, y int) {
	var mods []string
	for bit, name := range []string{"shift", "alt", "ctrl"} {
		if cb&(4<<bit) != 0 {
			mods = append(mods, name)
		}
	}
	if cb&64 != 0 {
		d := [4][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}}[cb&3]
		_ = ctl.Event(b, "wheel", struct {
			X      int `json:"x"`
			Y      int `json:"y"`
			DeltaX int `json:"delta_x"`
			DeltaY int `json:"delta_y"`
		}{x, y, d[0], d[1]})
		return
	}
	action := "press"
	if cb&32 != 0 {
		action = "motion"
	} else if cb&3 == 3 {
		action = "release"
	}
	_ = ctl.Event(b, "mouse", struct {
		Action string `json:"action"`
		Button string `json:"button,omitempty"`
		X      int    `json:"x"`
		Y      int    `json:"y"`
		Mods   string `json:"mods,omitempty"`
	}{action, [4]string{"left", "middle", "right", ""}[cb&3], x, y, strings.Join(mods, "+")})
}
