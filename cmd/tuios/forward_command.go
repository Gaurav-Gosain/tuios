package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// forwardMaxConns bounds the open connections of one forward. A browser
// opens about six for each origin, and a viewer two or three.
const forwardMaxConns = 64

func newForwardCmd() *cobra.Command {
	var local int
	var addrFile string
	cmd := &cobra.Command{
		Use:   "forward HOST:PORT [--local PORT]",
		Short: "Reach a port on a linked machine from this machine",
		Long: `Reach a TCP port on a linked machine from this machine, through the link.

tuios forward build:8080 listens on 127.0.0.1 here. Each connection to it
reaches port 8080 on the loopback of build (127.0.0.1, then ::1). The bytes go
through the link to build, so you do not need ssh -L.

The first line on stdout is the local address, for example 127.0.0.1:8080.
The forward keeps running after it, so a script reads the address from
--addr-file instead of a pipe. Without --local, the forward uses the same
port when it is free here, and any free port when it is not. --local 0
always takes any free port. The forward runs until you stop it with Ctrl+C.

At its start, the forward makes one test connection to the far port and
closes it at once. A program on that port sees one short connection.

The forward listens only on this machine's loopback. On Linux, it closes a
connection from another user at once. The policy of build for this machine
must allow open, as for run --host.`,
		Example: `  tuios forward build:8080
  tuios forward build:5173 --local 3000
  tuios forward build:8081 --local 0 --addr-file /tmp/fwd.addr &`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host, port, err := parseForwardTarget(args[0])
			if err != nil {
				return err
			}
			return runForward(cmd.OutOrStdout(), host, port, local, addrFile)
		},
	}
	cmd.Flags().IntVar(&local, "local", -1, "the port to listen on here: 0 for any free port (default: the same port when it is free)")
	cmd.Flags().StringVar(&addrFile, "addr-file", "", "also write the local address to this file once the forward listens")
	return cmd
}

// parseForwardTarget reads HOST:PORT.
func parseForwardTarget(arg string) (string, int, error) {
	host, p, ok := strings.Cut(arg, ":")
	if !ok || host == "" || p == "" {
		return "", 0, errfmt.Errorf("give the host and the port as HOST:PORT, for example build:8080, not %q", arg)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errfmt.Errorf("the port in %q must be a number from 1 to 65535", arg)
	}
	return host, port, nil
}

func runForward(out io.Writer, host string, port, local int, addrFile string) error {
	if err := ensureDaemon(); err != nil {
		return err
	}
	// One connection first, so a host that is down, a policy without open
	// or a port that nothing listens on fails here and not at the first
	// connection a program makes.
	probe, err := session.DialHostPort(host, version, port)
	if err != nil {
		return forwardDialError(host, err)
	}
	_ = probe.Close()

	ln, err := listenForward(port, local)
	if err != nil {
		return err
	}
	addr := ln.Addr().(*net.TCPAddr)
	lns := []net.Listener{ln}
	// Some programs resolve localhost to ::1 first. Listen there too when
	// the same port is free.
	if ln6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(addr.Port))); err == nil {
		lns = append(lns, ln6)
	}
	if addrFile != "" {
		// Written whole and renamed, so a reader never sees half an address.
		tmp := addrFile + ".tmp"
		if err := os.WriteFile(tmp, []byte(addr.String()+"\n"), 0o600); err != nil {
			return errfmt.Errorf("cannot write the address to %s: %w", addrFile, err)
		}
		if err := os.Rename(tmp, addrFile); err != nil {
			return errfmt.Errorf("cannot write the address to %s: %w", addrFile, err)
		}
	}
	fmt.Fprintln(out, addr.String())
	fmt.Fprintf(os.Stderr, "Forwarding %s to port %d on %s. Press Ctrl+C to stop.\n", addr, port, host)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	slots := make(chan struct{}, forwardMaxConns)
	var wg sync.WaitGroup
	for _, l := range lns {
		wg.Go(func() { acceptForward(ctx, l, host, port, slots) })
	}
	<-ctx.Done()
	for _, l := range lns {
		_ = l.Close()
	}
	wg.Wait()
	return nil
}

// listenForward listens on 127.0.0.1: on local when it is 0 or more, else on
// port when it is free, else on any free port.
func listenForward(port, local int) (net.Listener, error) {
	if local >= 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)))
		if err != nil {
			return nil, errfmt.Errorf("cannot listen on 127.0.0.1:%d here: %w", local, err)
		}
		return ln, nil
	}
	if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
		return ln, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errfmt.Errorf("cannot listen on 127.0.0.1 here: %w", err)
	}
	return ln, nil
}

func acceptForward(ctx context.Context, ln net.Listener, host string, port int, slots chan struct{}) {
	var delay time.Duration
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			// A lasting error, such as too many open files, waits a little
			// longer each time, as net/http does, so it does not spin.
			if delay == 0 {
				fmt.Fprintf(os.Stderr, "tuios forward: cannot accept a connection: %v\n", err)
				delay = 5 * time.Millisecond
			} else {
				delay = min(2*delay, time.Second)
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			continue
		}
		delay = 0
		if !forwardPeerIsUs(c) {
			_ = c.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = c.Close() // too many open connections
			continue
		}
		go func() {
			defer func() { <-slots }()
			spliceForward(ctx, c, host, port)
		}()
	}
}

// spliceForward joins one local connection to the port on host.
func spliceForward(ctx context.Context, c net.Conn, host string, port int) {
	defer c.Close()
	remote, err := session.DialHostPort(host, version, port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "A connection to port %d on %s failed: %v\n", port, host, forwardDialError(host, err))
		return
	}
	defer remote.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		session.SpliceHalfClose(c, remote)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// forwardDialError says why a connection to the port did not open.
func forwardDialError(host string, err error) error {
	if _, ok := errors.AsType[*session.HostConnectError](err); ok {
		return explainHostConnectError(host, err)
	}
	return explainHostVerbError(host, "forward", err)
}
