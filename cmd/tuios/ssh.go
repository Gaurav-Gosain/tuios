package main

import "github.com/spf13/cobra"

// newSSHCommand creates a new "ssh" Cobra command for running TUIOS as an SSH server with configurable options.
func newSSHCommand() *cobra.Command {

	var sshPort, sshHost, sshKeyPath, sshDefaultSession, sshAuthorizedKeys string
	var sshEphemeral, sshNoAuth bool

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
  cat ~/.ssh/id_ed25519.pub >> ~/.config/tuios/authorized_keys`,
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
  tuios ssh --host 0.0.0.0 --no-auth`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSSHServer(sshServerFlags{
				host:           sshHost,
				port:           sshPort,
				keyPath:        sshKeyPath,
				defaultSession: sshDefaultSession,
				authorizedKeys: sshAuthorizedKeys,
				ephemeral:      sshEphemeral,
				noAuth:         sshNoAuth,
			})
		},
	}
	sshCmd.Flags().StringVar(&sshPort, "port", "2222", "SSH server port")
	sshCmd.Flags().StringVar(&sshHost, "host", "localhost", "SSH server host")
	sshCmd.Flags().StringVar(&sshKeyPath, "key-path", "", "Path to SSH host key (auto-generated if not specified)")
	sshCmd.Flags().StringVar(&sshDefaultSession, "default-session", "", "Default session name for all connections")
	sshCmd.Flags().BoolVar(&sshEphemeral, "ephemeral", false, "Run in ephemeral mode (standalone, no daemon)")
	sshCmd.Flags().StringVar(&sshAuthorizedKeys, "authorized-keys", "", "Path to the public keys allowed to connect (default ~/.config/tuios/authorized_keys). Keys with options are not accepted")
	sshCmd.Flags().BoolVar(&sshNoAuth, "no-auth", false, "Give every connection a shell without checking who it is (trusted networks only)")
	return sshCmd
}
