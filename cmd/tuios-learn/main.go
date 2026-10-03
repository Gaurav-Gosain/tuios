// Command tuios-learn is the server behind `ssh learn.tuios.dev`: a public
// SSH server with no accounts that teaches tuios with the lessons of
// tuios.dev/learn. Every session runs the real tuios with a pretend shell in
// its own locked-down process. No real shell, file or network is ever
// reachable from a session.
//
//	tuios-learn [flags]            serve (the default)
//	tuios-learn health [addr]      exit 0 if the SSH port answers
//	tuios-learn session            one session; the server runs this itself
//
// It is a separate binary so the tuios binary and its size budget do not
// carry it. See README.md here for running it, and the deployment kit in
// deploy/.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/learnssh"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "session":
			err := learnssh.RunSession(os.Stdin, os.Stdout, os.NewFile(3, "ctl-in"), os.NewFile(4, "ctl-out"))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "selfcheck":
			if err := learnssh.SelfCheck(); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			return
		case "health":
			addr := "127.0.0.1:22"
			if len(os.Args) > 2 {
				addr = os.Args[2]
			}
			os.Exit(health(addr))
		case "version", "--version":
			fmt.Println("tuios-learn", version)
			return
		}
	}

	cfg := learnssh.Defaults()
	flag.StringVar(&cfg.Listen, "listen", cfg.Listen, "SSH address to listen on")
	flag.StringVar(&cfg.StateDir, "state-dir", envOr("STATE_DIRECTORY", cfg.StateDir), "directory for the host key, best times, board and counters")
	flag.StringVar(&cfg.Health, "health", "", "HTTP address for /healthz, such as 127.0.0.1:9022 (off when empty)")
	flag.IntVar(&cfg.MaxSessions, "max-sessions", cfg.MaxSessions, "sessions at once")
	flag.IntVar(&cfg.MaxPerIP, "max-per-ip", cfg.MaxPerIP, "sessions at once from one address (IPv6: one /64)")
	flag.Float64Var(&cfg.ConnPerMinute, "conn-per-minute", cfg.ConnPerMinute, "new connections per address per minute")
	flag.Float64Var(&cfg.ConnBurst, "conn-burst", cfg.ConnBurst, "connections an address may open at once above the rate")
	flag.IntVar(&cfg.MaxConns, "max-conns", cfg.MaxConns, "TCP connections at once")
	flag.DurationVar(&cfg.Idle, "idle", cfg.Idle, "end a session after this long with no input")
	flag.DurationVar(&cfg.MaxSession, "max-session", cfg.MaxSession, "end a session after this long")
	flag.IntVar(&cfg.SessionMemMiB, "session-mem", cfg.SessionMemMiB, "stop a session over this many MiB of memory")
	flag.Float64Var(&cfg.InputRate, "input-rate", cfg.InputRate, "bytes per second a client may send")
	flag.BoolVar(&cfg.AllowNoSandbox, "unsafe-no-sandbox", false, "start even if the session sandbox does not work (local tests only)")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	srv, err := learnssh.NewServer(cfg, log)
	if err != nil {
		log.Error("start", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("tuios-learn", "version", version)
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		// systemd may list several directories, separated by colons.
		return strings.Split(v, ":")[0]
	}
	return def
}

// health checks that the SSH port answers with an SSH banner.
func health(addr string) int {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		return 1
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "SSH-2.0-") {
		fmt.Fprintln(os.Stderr, "unhealthy: no SSH banner")
		return 1
	}
	fmt.Println("healthy:", strings.TrimSpace(string(buf[:n])))
	return 0
}
