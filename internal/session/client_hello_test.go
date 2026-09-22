//go:build !windows

package session

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// Client is the connection behind every control command and shell completion,
// so Connect must not load the user config. Loading it wrote a default
// config.toml when none existed and printed config errors and a missing
// preferred_shell warning to stderr on every call. The hello names no shell;
// the daemon resolves it from its own preferred_shell.
func TestClientConnectLeavesTheConfigAlone(t *testing.T) {
	tests := []struct {
		name   string
		config string // "" writes no config file
	}{
		{name: "no config file"},
		{name: "missing preferred shell", config: "[appearance]\npreferred_shell = \"/no/such/shell\"\n"},
		{name: "config with errors", config: "[keybindings]\nleader_key = \"ctrl+\"\n"},
		{name: "unparsable config", config: "this is not toml ===\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configHome := t.TempDir()
			// Registered before t.Setenv so it runs after it, as in the config
			// package tests: the xdg paths are resolved once, at init.
			t.Cleanup(xdg.Reload)
			t.Setenv("XDG_CONFIG_HOME", configHome)
			xdg.Reload()
			configPath := filepath.Join(configHome, "tuios", "config.toml")
			if tc.config != "" {
				if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(configPath, []byte(tc.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// A short runtime dir: a unix socket path has a small length limit
			// and t.TempDir on macOS is long.
			runtimeDir, err := os.MkdirTemp("/tmp", "tuios-hello-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
			t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
			socketPath, err := GetSocketPath()
			if err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("unix", socketPath)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			hellos := make(chan HelloPayload, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				msg, _, err := ReadMessageWithCodec(conn)
				if err != nil {
					return
				}
				var hello HelloPayload
				_ = msg.ParsePayloadWithCodec(&hello, DefaultCodec())
				hellos <- hello
				resp, _ := NewMessageWithCodec(MsgWelcome, &WelcomePayload{
					Codec:    "gob",
					Protocol: ProtocolVersion,
				}, DefaultCodec())
				_ = WriteMessageWithCodec(conn, resp, DefaultCodec())
				// Hold the connection until the client closes it.
				_, _ = io.Copy(io.Discard, conn)
			}()

			stderr := captureStderr(t, func() {
				c := NewClient(&ClientConfig{Version: "test"})
				if err := c.Connect(); err != nil {
					t.Errorf("Connect: %v", err)
				}
				_ = c.Close()
			})

			if stderr != "" {
				t.Errorf("ASSERTION: Connect printed to stderr: %q", stderr)
			}
			if _, err := os.Stat(configPath); tc.config == "" && err == nil {
				t.Errorf("ASSERTION: Connect wrote a default config at %s", configPath)
			}
			select {
			case hello := <-hellos:
				if hello.Shell != "" {
					t.Errorf("ASSERTION: hello named shell %q, want none so the daemon resolves it", hello.Shell)
				}
			default:
				t.Error("the fake daemon read no hello")
			}
		})
	}
}

// captureStderr runs fn with os.Stderr pointed at a pipe and returns what fn
// wrote there.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	func() {
		defer func() { os.Stderr = orig }()
		fn()
	}()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
