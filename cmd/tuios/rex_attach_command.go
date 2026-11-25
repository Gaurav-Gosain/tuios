//go:build !windows

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/rexclient"
)

// rexDetachSequence is what the rex_detach action types into a Rex pane. The
// bridge reads it from its input, lets the shell go and exits. It is an APC
// string, which a terminal never sends for a key.
const rexDetachSequence = "\x1b_tuios-rex-detach\x1b\\"

// rexSession is the label of the Rex session tuios opens shells in.
const rexSession = "tuios"

func newRexAttachCommand() *cobra.Command {
	var blockID string
	var bridge bool
	cmd := &cobra.Command{
		Use:    "rex-attach [TARGET]",
		Short:  "Run a Rex shell as the program of this pane",
		Hidden: true,
		Long: `Run a shell of a Rex server as the program of this pane.

tuios new-window --rex starts it. TARGET is local (the default), a host of
rex hosts, host[:port] for a tailnet listener, http://host:port or
unix:///path. With --block, the pane opens a running shell of the tuios or
monstar Rex session, with its screen and scrollback.`,
		Args: cobra.MaximumNArgs(1),
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
	ep, err := rexEndpoint(target)
	if err != nil {
		return err
	}
	ctl, err := rexclient.Dial(ep, "tuios")
	if err != nil {
		return err
	}
	defer func() { _ = ctl.Close() }()

	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	var b rexclient.Block
	if blockID != "" {
		b, err = ctl.FindBlock(blockID, rexSession, "monstar")
	} else {
		cwd, _ := os.Getwd()
		b, err = ctl.NewShell(rexSession, map[string]any{"cwd": cwd, "size": map[string]int{"columns": cols, "rows": rows}})
	}
	if err != nil {
		return err
	}
	// A closed pane leaves this process in a background group of a terminal
	// with no session leader, where restoring the terminal mode would stop it
	// with SIGTTOU. So that is ignored, and the block closes before the
	// terminal is restored.
	signal.Ignore(syscall.SIGTTOU)
	if old, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		defer func() { _ = term.Restore(int(os.Stdin.Fd()), old) }()
	}
	closeBlock := true
	defer func() {
		if closeBlock {
			// A closing pane sends SIGHUP again when its terminal goes.
			signal.Ignore(syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
			_, _ = ctl.Call("block.close", b)
		}
	}()
	if _, err := ctl.Call("session.own_block", map[string]any{"session_id": b.SessionID, "block_ids": []string{b.BlockID}}); err != nil {
		return err
	}
	_ = ctl.Send("com.superlogical.terminal.resize", map[string]any{"session_id": b.SessionID, "block_id": b.BlockID, "args": map[string]int{"columns": cols, "rows": rows}})

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

	detach := make(chan struct{}, 1)
	go rexForwardInput(ctl, b, detach)

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	// No signal.Stop: the process ends when this returns, and SIGHUP must not
	// end it before the block is closed.

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
	for {
		select {
		case chunk := <-out:
			if _, err := os.Stdout.Write(chunk); err != nil {
				return err
			}
		case err := <-ended:
			if errors.Is(err, rexclient.ErrBlockEnded) {
				closeBlock = false
				return nil
			}
			return errfmt.Errorf("the Rex data stream ended: %w", err)
		case <-detach:
			closeBlock = false
			_, err := ctl.Call("session.disown_block", map[string]any{"session_id": b.SessionID, "block_ids": []string{b.BlockID}})
			return err
		case sig := <-sigs:
			if sig != syscall.SIGWINCH {
				return nil
			}
			if c, r, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
				_ = ctl.Send("com.superlogical.terminal.resize", map[string]any{"session_id": b.SessionID, "block_id": b.BlockID, "args": map[string]int{"columns": c, "rows": r}})
			}
		case <-ctl.Done():
			return errors.New("the Rex server closed the control connection")
		}
	}
}

// rexEndpoint reads a target the way monstar and rex do. A label of rex
// hosts is looked up with the rex CLI when it is on PATH.
func rexEndpoint(target string) (rexclient.Endpoint, error) {
	if target != "" && target != "local" && !strings.Contains(target, "/") && !strings.Contains(target, ":") {
		if ep, ok := rexHostEndpoint(target); ok {
			return ep, nil
		}
	}
	return rexclient.ParseEndpoint(target, os.Getenv("REX_SERVER"), os.Getenv("XDG_RUNTIME_DIR"), os.Getuid())
}

// rexHostEndpoint looks up a label of rex hosts and takes its first endpoint
// this client can reach.
func rexHostEndpoint(label string) (rexclient.Endpoint, bool) {
	path, err := exec.LookPath("rex")
	if err != nil {
		return rexclient.Endpoint{}, false
	}
	out, err := childproc.Command(path, "hosts", "--json").Output()
	if err != nil {
		return rexclient.Endpoint{}, false
	}
	var res struct {
		Hosts []struct {
			Host struct {
				Label     string   `json:"label"`
				Endpoints []string `json:"endpoints"`
			} `json:"host"`
		} `json:"hosts"`
	}
	if jsonx.Unmarshal(out, &res) != nil {
		return rexclient.Endpoint{}, false
	}
	for _, h := range res.Hosts {
		if h.Host.Label != label {
			continue
		}
		for _, e := range h.Host.Endpoints {
			ep, err := rexclient.ParseEndpoint(e, "", os.Getenv("XDG_RUNTIME_DIR"), os.Getuid())
			if err != nil {
				continue
			}
			if ep.Unix != "" {
				if _, err := os.Stat(ep.Unix); err != nil {
					continue
				}
			}
			return ep, true
		}
	}
	return rexclient.Endpoint{}, false
}

// rexSeed writes the block's screen and scrollback to this pane, as escape
// sequences, before the live output. The format call and the data stream are
// two cuts of the shell's output: output the shell writes between them is
// not shown. It is taken first, so nothing is drawn twice.
func rexSeed(ctl *rexclient.Control, b rexclient.Block) error {
	const chunk = 960 << 10
	raw, err := ctl.BlockCall(b, "com.superlogical.terminal.format", map[string]any{
		"format": "vt", "max_bytes": chunk,
		"extra_cursor": true, "extra_modes": true, "extra_kitty_keyboard": true, "extra_keyboard": true,
		"extra_palette": true, "extra_scrolling_region": true, "extra_tabstops": true, "extra_charsets": true,
		"extra_style": true, "extra_hyperlink": true, "extra_protection": true, "extra_pwd": true,
	})
	if err != nil {
		return err
	}
	var res struct {
		Content string `json:"content"`
		ID      uint64 `json:"id"`
		EOF     bool   `json:"eof"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("\x1b[H\x1b[2J\x1b[3J")
	buf.WriteString(res.Content)
	for res.ID != 0 && !res.EOF {
		raw, err := ctl.BlockCall(b, "com.superlogical.terminal.blob_read", map[string]any{"id": res.ID, "offset": buf.Len() - len("\x1b[H\x1b[2J\x1b[3J"), "max_bytes": chunk})
		if err != nil {
			return err
		}
		var next struct {
			Content string `json:"content"`
			EOF     bool   `json:"eof"`
		}
		if err := jsonx.Unmarshal(raw, &next); err != nil {
			return err
		}
		if next.Content == "" {
			break
		}
		buf.WriteString(next.Content)
		res.EOF = next.EOF
	}
	_, err = os.Stdout.Write(buf.Bytes())
	return err
}

// rexForwardInput sends what is typed in the pane to the shell, as typed. It
// signals detach on the detach sequence and stops there.
func rexForwardInput(ctl *rexclient.Control, b rexclient.Block, detach chan<- struct{}) {
	buf := make([]byte, 32<<10)
	var held []byte
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		data := append(held, buf[:n]...)
		held = nil
		if i := bytes.Index(data, []byte(rexDetachSequence)); i >= 0 {
			if i > 0 {
				rexWrite(ctl, b, data[:i])
			}
			detach <- struct{}{}
			return
		}
		// Hold a tail that may start the detach sequence.
		for k := min(len(data), len(rexDetachSequence)-1); k > 0; k-- {
			if bytes.HasPrefix([]byte(rexDetachSequence), data[len(data)-k:]) {
				held = append([]byte(nil), data[len(data)-k:]...)
				data = data[:len(data)-k]
				break
			}
		}
		if len(data) > 0 {
			rexWrite(ctl, b, data)
		}
	}
}

// rexWrite types bytes into the block. client.action keeps them in order
// with the other input of this connection.
func rexWrite(ctl *rexclient.Control, b rexclient.Block, data []byte) {
	_ = ctl.Send("client.action", map[string]any{
		"action": "com.superlogical.terminal.write", "session_id": b.SessionID, "block_id": b.BlockID,
		"args": map[string]string{"data": strings.ToValidUTF8(string(data), "\uFFFD")},
	})
}
