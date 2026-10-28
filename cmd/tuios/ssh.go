package main

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// newSSHCommand creates a new "ssh" Cobra command for running TUIOS as an SSH server with configurable options.
func newSSHCommand() *cobra.Command {
	var sshPort, sshHost, sshKeyPath, sshDefaultSession, sshAuthorizedKeys string
	var sshEphemeral, sshNoAuth bool
	var sshHandshakeTimeout, sshIdleTimeout time.Duration
	var sshMaxHandshakes, sshMaxSessions int

	sshCmd := &cobra.Command{
		Use:   "ssh",
		Short: "Run TUIOS as SSH server",
		Long: `Run TUIOS as an SSH server

Allows remote connections to TUIOS via SSH. The server will generate
a host key automatically if not specified.

By default, SSH sessions connect to the TUIOS daemon for persistent sessions.
Session selection priority:
  1. --default-session flag (if specified)
  2. SSH username (if not generic like "tuios", "root", "anonymous")
  3. SSH command argument (e.g., "ssh host attach mysession")
  4. First available session or create new

Use --ephemeral for standalone sessions (legacy behavior).

Every connection gets a shell on this machine, so the server checks who is
connecting. It reads public keys from ~/.config/tuios/authorized_keys. It does
not read ~/.ssh/authorized_keys unless you name it with --authorized-keys.
TUIOS does not accept a key with options such as command=, from= or restrict.
With no keys file, the server does not start, on localhost too, until you add
keys, pass --authorized-keys, or pass --no-auth.

To let your own key in, use your public key file:
  mkdir -p ~/.config/tuios
  cat ~/.ssh/id_ed25519.pub >> ~/.config/tuios/authorized_keys

The server limits how long a login may take, how many connections may log in
at the same time, and how many sessions may be open. Set the limits in the
[ssh] section of the config file. A limit flag replaces the value in the file.`,
		Example: `  # Start SSH server on default port (needs ~/.config/tuios/authorized_keys)
  tuios ssh

  # Start on custom port
  tuios ssh --port 2222

  # Specify custom host key
  tuios ssh --key-path /path/to/host_key

  # Use a default session for all connections
  tuios ssh --default-session mysession

  # Run in ephemeral mode (standalone, no daemon)
  tuios ssh --ephemeral

  # Read the allowed public keys from somewhere else
  tuios ssh --authorized-keys /etc/tuios/authorized_keys

  # Use the keys that sshd accepts (keys with options are not accepted)
  tuios ssh --authorized-keys ~/.ssh/authorized_keys

  # Serve the network with no authentication (trusted networks only)
  tuios ssh --host 0.0.0.0 --no-auth

  # Close a connection after 30 minutes with no traffic
  tuios ssh --idle-timeout 30m`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// This process holds every connection's emulator, compositor and
			// frame buffers and, in daemon mode, the daemon itself, so it is the
			// process a memory question is about. --pprof is a persistent flag
			// that ssh used to accept and ignore.
			startPprofServer()
			f := sshServerFlags{
				host:           sshHost,
				port:           sshPort,
				keyPath:        sshKeyPath,
				defaultSession: sshDefaultSession,
				authorizedKeys: sshAuthorizedKeys,
				ephemeral:      sshEphemeral,
				noAuth:         sshNoAuth,
			}
			// A limit flag replaces the [ssh] value only when it is given, so
			// the flag defaults never hide the file.
			flags := cmd.Flags()
			if flags.Changed("handshake-timeout") {
				f.handshakeTimeout = &sshHandshakeTimeout
			}
			if flags.Changed("idle-timeout") {
				f.idleTimeout = &sshIdleTimeout
			}
			if flags.Changed("max-handshakes") {
				f.maxHandshakes = &sshMaxHandshakes
			}
			if flags.Changed("max-sessions") {
				f.maxSessions = &sshMaxSessions
			}
			if err := f.checkLimits(); err != nil {
				return err
			}
			if err := startCPUProfile(); err != nil {
				return err
			}
			defer stopCPUProfile()
			return runSSHServer(f)
		},
	}
	sshCmd.Flags().StringVar(&sshPort, "port", "2222", "SSH server port")
	sshCmd.Flags().StringVar(&sshHost, "host", "localhost", "SSH server host")
	sshCmd.Flags().StringVar(&sshKeyPath, "key-path", "", "Path to SSH host key (auto-generated if not specified)")
	sshCmd.Flags().StringVar(&sshDefaultSession, "default-session", "", "Default session name for all connections")
	sshCmd.Flags().BoolVar(&sshEphemeral, "ephemeral", false, "Run in ephemeral mode (standalone, no daemon)")
	sshCmd.Flags().StringVar(&sshAuthorizedKeys, "authorized-keys", "", "Path to the public keys allowed to connect (default ~/.config/tuios/authorized_keys). Keys with options are not accepted")
	sshCmd.Flags().BoolVar(&sshNoAuth, "no-auth", false, "Give every connection a shell without checking who it is (trusted networks only)")
	sshCmd.Flags().DurationVar(&sshHandshakeTimeout, "handshake-timeout", config.SSHDefaultHandshakeTimeout, "Close a connection that does not log in within this time. 0 turns the limit off. Replaces handshake_timeout in [ssh]")
	sshCmd.Flags().DurationVar(&sshIdleTimeout, "idle-timeout", config.SSHDefaultIdleTimeout, "Close a connection after this time with no traffic. 0 turns the limit off. Replaces idle_timeout in [ssh]")
	sshCmd.Flags().IntVar(&sshMaxHandshakes, "max-handshakes", config.SSHDefaultMaxHandshakes, "Close a new connection at once when this many connections are logging in. 0 turns the limit off. Replaces max_handshakes in [ssh]")
	sshCmd.Flags().IntVar(&sshMaxSessions, "max-sessions", config.SSHDefaultMaxSessions, "Refuse a new session when this many sessions are open. 0 turns the limit off. Replaces max_sessions in [ssh]")
	return sshCmd
}
