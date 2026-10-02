// Package extension runs tuios extension binaries: separate executables named
// tuios-<name> that add a command tuios-slim leaves out, the way git runs
// git-<name> and kubectl runs kubectl-<name>.
//
// The protocol is pinned here so both sides build against one definition:
//
//   - Name. The executable is tuios-<name>, with .exe on Windows. <name> is
//     the command the user typed: tuios-slim ssh runs tuios-ssh.
//   - Lookup. The extension directory, $XDG_DATA_HOME/tuios/ext, comes first,
//     then PATH. See Lookup.
//   - Handshake. Before it runs one, the host runs the binary with the single
//     argument InfoFlag. The binary prints one JSON object (Info) on stdout
//     and exits 0. The host refuses a binary whose protocol, name or tuios
//     version differs from its own. An extension is built from the same
//     source tree as the host, so anything but the same version is a pair
//     that was never tested together. See Check.
//   - Run. The host then runs the binary with the user's arguments after the
//     command name, the same stdin, stdout and stderr, and its own
//     environment plus the variables in Env. The host exits with the
//     extension's exit status.
//
// An extension talks to the daemon the way any tuios client does: over the
// socket in TUIOS_SOCKET, with the JSON verb protocol or the binary client
// protocol.
package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/adrg/xdg"
)

// Protocol is the version of this protocol. Bump it on any change a binary
// built against the old one would not follow.
const Protocol = 1

// InfoFlag is the one argument the handshake runs an extension with.
const InfoFlag = "--tuios-extension-info"

// Prefix is what every extension executable's name starts with.
const Prefix = "tuios-"

// Environment variables the host sets for an extension it runs.
const (
	// EnvProtocol carries Protocol.
	EnvProtocol = "TUIOS_EXT_PROTOCOL"
	// EnvHostVersion carries the version of the tuios that ran the extension.
	EnvHostVersion = "TUIOS_EXT_HOST_VERSION"
	// EnvHost carries the path of the tuios that ran the extension.
	EnvHost = "TUIOS_EXT_HOST"
	// EnvSocket is the daemon socket. It is the variable every pane already
	// has, set to the socket this host reaches.
	EnvSocket = "TUIOS_SOCKET"
)

// handshakeTimeout bounds the handshake. A binary that has not answered by
// then is not an extension that follows this protocol.
const handshakeTimeout = 5 * time.Second

// Info is what an extension prints for InfoFlag.
type Info struct {
	// Protocol is the protocol version the extension follows.
	Protocol int `json:"protocol"`
	// Name is the command the extension provides, without the prefix.
	Name string `json:"name"`
	// TuiosVersion is the tuios version the extension was built from.
	TuiosVersion string `json:"tuios_version"`
}

// Dir is the extension directory: $XDG_DATA_HOME/tuios/ext.
func Dir() string {
	return filepath.Join(xdg.DataHome, "tuios", "ext")
}

// executableName is the file name of the extension for command name.
func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return Prefix + name + ".exe"
	}
	return Prefix + name
}

// ErrNotFound is what Lookup returns when no extension provides the command.
var ErrNotFound = errors.New("extension not found")

// Lookup finds the extension for command name: first in Dir, then on PATH.
// It returns ErrNotFound when neither has one.
func Lookup(name string) (string, error) {
	if !validName(name) {
		return "", ErrNotFound
	}
	candidate := filepath.Join(Dir(), executableName(name))
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() && executable(info) {
		return candidate, nil
	}
	if path, err := exec.LookPath(executableName(name)); err == nil {
		return path, nil
	}
	return "", ErrNotFound
}

// validName keeps a command name to the characters a command can have, so
// it cannot name a path.
func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// executable reports whether a file has an execute bit. Windows has none to
// check, and the .exe name is what makes the file one there.
func executable(info os.FileInfo) bool {
	return runtime.GOOS == "windows" || info.Mode()&0o111 != 0
}

// MismatchError is the refusal Check returns.
type MismatchError struct {
	Path        string
	Name        string
	HostVersion string
	Info        Info
}

func (e *MismatchError) Error() string {
	if e.Info.Protocol != Protocol {
		return fmt.Sprintf("%s speaks extension protocol %d, and this tuios speaks %d. Update both to the same tuios version.",
			e.Path, e.Info.Protocol, Protocol)
	}
	if e.Info.Name != e.Name {
		return fmt.Sprintf("%s says it provides %q, not %q. Reinstall it.", e.Path, e.Info.Name, e.Name)
	}
	return fmt.Sprintf("%s is built for tuios %s, and this tuios is %s. Update both to the same version.",
		e.Path, e.Info.TuiosVersion, e.HostVersion)
}

// Check runs the handshake against the extension at path and refuses one
// that does not match this host.
func Check(path, name, hostVersion string) error {
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, InfoFlag)
	// A grandchild that inherits stdout holds the pipe open after the
	// extension is killed, and Wait would wait for it with no end. WaitDelay
	// bounds that wait.
	cmd.WaitDelay = time.Second
	out := &cappedBuffer{limit: handshakeMaxOutput, over: cancel}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s did not answer the extension handshake: %w. Reinstall it", path, err)
	}
	var info Info
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &info); err != nil {
		return fmt.Errorf("%s did not answer the extension handshake with its version. Reinstall it", path)
	}
	if info.Protocol != Protocol || info.Name != name || info.TuiosVersion != hostVersion {
		return &MismatchError{Path: path, Name: name, HostVersion: hostVersion, Info: info}
	}
	return nil
}

// handshakeMaxOutput is the most the handshake reads from an extension. The
// answer is one short JSON object. An extension that prints more is not
// answering it, and reading on would let it fill the host's memory.
const handshakeMaxOutput = 64 << 10

// errHandshakeTooLong stops the copy from an extension that prints more than
// handshakeMaxOutput.
var errHandshakeTooLong = errors.New("the answer is longer than 64 KiB")

// cappedBuffer is a bytes.Buffer that refuses a write past limit and calls
// over, which kills the extension: one that is printing without end would
// otherwise run until the timeout. The buffer is a field, not embedded: an
// embedded bytes.Buffer brings ReadFrom, and io.Copy would read through it
// past the limit without calling Write.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
	over  func()
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.limit {
		b.over()
		return 0, errHandshakeTooLong
	}
	return b.buf.Write(p)
}

// Bytes returns what the extension printed.
func (b *cappedBuffer) Bytes() []byte { return b.buf.Bytes() }

// Env is the environment the host adds for an extension.
func Env(hostVersion, socket string) []string {
	env := []string{
		fmt.Sprintf("%s=%d", EnvProtocol, Protocol),
		EnvHostVersion + "=" + hostVersion,
	}
	if self, err := os.Executable(); err == nil {
		env = append(env, EnvHost+"="+self)
	}
	if socket != "" {
		env = append(env, EnvSocket+"="+socket)
	}
	return env
}

// Run runs the extension at path with args, the host's standard streams and
// environment plus Env, and returns its exit status. err is set only when
// the extension could not be started.
func Run(path string, args []string, hostVersion, socket string) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), Env(hostVersion, socket)...)
	err := cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, fmt.Errorf("could not run %s: %w", path, err)
	}
	return 0, nil
}

// ServeInfo answers the handshake for an extension's main. Call it first:
// when args is the single InfoFlag it prints Info and returns true, and main
// then exits 0.
func ServeInfo(args []string, name, tuiosVersion string) bool {
	if len(args) != 1 || args[0] != InfoFlag {
		return false
	}
	_ = json.NewEncoder(os.Stdout).Encode(Info{Protocol: Protocol, Name: name, TuiosVersion: tuiosVersion})
	return true
}
