//go:build !slim

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/server"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// sshServerFlags is the `tuios ssh` command line, gathered so the runner reads
// as one thing rather than as seven positional arguments.
type sshServerFlags struct {
	host           string
	port           string
	keyPath        string
	defaultSession string
	authorizedKeys string
	ephemeral      bool
	noAuth         bool
}

func runSSHServer(f sshServerFlags) error {
	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	// Decided before anything starts, and before the daemon is touched.
	// StartSSHServer makes the same call and refuses the same binds; this one
	// exists so the refusal can be answered with the flags this command has,
	// which is the same reason cmd/tuios-web decides TLS in the command rather
	// than leaving it to sip.
	if err := checkSSHAuth(os.Stderr, f); err != nil {
		return err
	}

	app.SetInputHandler(input.HandleInput)

	log.Printf("Starting TUIOS SSH server on %s:%s", f.host, f.port)
	if f.defaultSession != "" {
		log.Printf("Default session: %s", f.defaultSession)
	}
	if f.ephemeral {
		log.Printf("Running in ephemeral mode (no daemon)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down SSH server...")
		cancel()
		// Stop in-process daemon if we started one
		session.StopInProcessDaemon()
	}()

	cfg := &server.SSHServerConfig{
		Host:               f.host,
		Port:               f.port,
		KeyPath:            f.keyPath,
		DefaultSession:     f.defaultSession,
		AuthorizedKeysPath: f.authorizedKeys,
		NoAuth:             f.noAuth,
		Version:            version,
		Ephemeral:          f.ephemeral,
		ShowKeys:           interfaceFlags.ShowKeys,
		// The full flag set, not a subset: `tuios ssh` registers the same
		// interface flags as every other run command, and the server applies
		// them over the appearance baseline it loads. Applying them here
		// instead would run before that baseline, which would clobber them.
		Overrides: flagOverrides(),
	}
	if err := server.StartSSHServer(ctx, cfg); err != nil {
		return fmt.Errorf("SSH server error: %w", err)
	}
	return nil
}
