package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/ssh"
	"github.com/adrg/xdg"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Gaurav-Gosain/tuios/internal/netutil"
)

// Authentication for the SSH server.
//
// The server had none. wish builds on charm.land/ssh, which sets
// NoClientAuth when no PasswordHandler, PublicKeyHandler or
// KeyboardInteractiveHandler is installed (see server.go in that module), so
// every connection was accepted, including one that names a user nobody has.
// The session it got is the full TUIOS, and TUIOS opens shells as the account
// running the server. That is a shell on this machine for anyone who can reach
// the port.
//
// Public keys, not passwords: every machine with an ssh client already has a
// keypair, so there is nothing new to store here and nothing to guess.

// ConfigAuthorizedKeys is where TUIOS keeps its own list, relative to the XDG
// config home.
//
// It is the only file read by default. ~/.ssh/authorized_keys used to be the
// fallback, but that is sshd's file: its keys are often restricted with
// command=, from= or restrict for backup jobs and deploy tools, and TUIOS
// cannot apply those options. A key restricted there got a full session here.
// That file is still usable, but only when --authorized-keys names it.
const ConfigAuthorizedKeys = "tuios/authorized_keys"

// AuthorizedKeys is the set of public keys that may open a session, and the
// file they came from.
//
// A zero value means no file exists, which is not the same as a file holding
// no keys. The first is "the user never configured this" and needs --no-auth
// to run; the second is a mistake and stops startup.
type AuthorizedKeys struct {
	// Path is the file the keys were read from, empty when no candidate file
	// exists. The handler re-reads this exact path rather than resolving the
	// candidates again, so creating a second candidate while the server runs
	// cannot silently move which file is trusted.
	Path string
	Keys []ssh.PublicKey
}

// Enabled reports whether these keys turn authentication on.
func (a *AuthorizedKeys) Enabled() bool { return a != nil && a.Path != "" }

// AuthorizedKeysCandidates lists the files searched for public keys: the
// explicit path when one is given, otherwise TUIOS's own file. There is no
// fallback to ~/.ssh/authorized_keys. See ConfigAuthorizedKeys.
func AuthorizedKeysCandidates(explicit string) []string {
	if explicit != "" {
		return []string{explicit}
	}
	return []string{defaultAuthorizedKeysPath()}
}

// defaultAuthorizedKeysPath is TUIOS's own keys file.
func defaultAuthorizedKeysPath() string {
	return filepath.Join(xdg.ConfigHome, ConfigAuthorizedKeys)
}

// LoadAuthorizedKeys reads the first candidate file that exists.
//
// The three outcomes are deliberately distinct, because collapsing them is how
// a server ends up open:
//
//   - No candidate exists: no keys are configured. Returns an empty set and no
//     error, and the caller decides whether that bind may run without them.
//   - A candidate exists and holds at least one key: authentication is on.
//   - A candidate exists but cannot be read, does not parse, or holds no key:
//     an error. A file the process may not open is not a file with no keys, and
//     treating a permissions failure as "nothing configured" would hand out
//     exactly the unauthenticated server the file was written to prevent.
func LoadAuthorizedKeys(explicit string) (*AuthorizedKeys, error) {
	for _, path := range AuthorizedKeysCandidates(explicit) {
		data, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own configuration
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			if explicit != "" {
				return nil, fmt.Errorf("no authorized keys file at %s. Create the file, or drop --authorized-keys", path)
			}
			continue
		default:
			return nil, fmt.Errorf("cannot read the authorized keys file %s: %w. Fix the file permissions, or move the file away", path, err)
		}
		keys, restricted, err := parseAuthorizedKeys(path, data)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 && len(restricted) > 0 {
			return nil, fmt.Errorf("every key in %s has options (lines %s). TUIOS cannot apply options such as command=, from= or restrict, so it does not accept these keys. Add a key with no options, or name a different file with --authorized-keys",
				path, joinLines(restricted))
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("the authorized keys file %s holds no keys. Add a public key to it, or move the file away", path)
		}
		if len(restricted) > 0 {
			log.Printf("[SSH] auth: TUIOS does not accept the keys with options in %s (lines %s). It cannot apply options such as command=, from= or restrict.",
				path, joinLines(restricted))
		}
		return &AuthorizedKeys{Path: path, Keys: keys}, nil
	}
	return &AuthorizedKeys{}, nil
}

// parseAuthorizedKeys reads one authorized_keys file. Blank lines and comments
// are skipped, the way sshd reads the same format.
//
// A line that is neither and does not parse stops startup. sshd skips such a
// line, which is the wrong trade here: a typo in the one file that decides who
// gets a shell should be reported while the operator is watching, not
// discovered later as a key that never worked.
//
// A key with options is left out of keys and its line number is returned in
// restricted. The options (command=, from=, restrict, cert-authority,
// no-pty and the rest) each narrow what sshd lets that key do, and TUIOS can
// apply none of them: every session here is a full interactive TUIOS. Ignoring
// the options would hand the key more than the file grants it, so the key is
// not accepted at all. A cert-authority line names a CA, not a user key, and
// accepting it as a user key would be wrong in a different way.
func parseAuthorizedKeys(path string, data []byte) (keys []ssh.PublicKey, restricted []int, err error) {
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, nil, fmt.Errorf("line %d of %s is not a public key: %w. Fix the line, or delete it", i+1, path, err)
		}
		if len(options) > 0 {
			restricted = append(restricted, i+1)
			continue
		}
		keys = append(keys, key)
	}
	return keys, restricted, nil
}

// joinLines formats line numbers for a message.
func joinLines(lines []int) string {
	parts := make([]string, len(lines))
	for i, n := range lines {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// readAuthorizedKeysPath reads and parses one known file, for the auth handler.
func readAuthorizedKeysPath(path string) ([]ssh.PublicKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path was resolved at startup
	if err != nil {
		return nil, err
	}
	keys, _, err := parseAuthorizedKeys(path, data)
	return keys, err
}

// publicKeyHandler admits a connection whose key is in the authorized keys
// file, and refuses every other connection.
//
// The file is re-read on each attempt, the way sshd reads it, so a key added
// while the server runs works without a restart. Every failure here denies the
// connection: a file that has become unreadable or unparseable authorizes
// nobody, which is the only safe reading of "we cannot tell who this is".
func publicKeyHandler(path string) ssh.PublicKeyHandler {
	return func(ctx ssh.Context, key ssh.PublicKey) bool {
		fingerprint := gossh.FingerprintSHA256(key)
		keys, err := readAuthorizedKeysPath(path)
		if err != nil {
			log.Printf("[SSH] auth: refused %s from %s: %v", fingerprint, ctx.RemoteAddr(), err)
			return false
		}
		for _, allowed := range keys {
			if ssh.KeysEqual(key, allowed) {
				log.Printf("[SSH] auth: accepted %s from %s", fingerprint, ctx.RemoteAddr())
				return true
			}
		}
		log.Printf("[SSH] auth: refused %s from %s. Add that key to %s to let it in", fingerprint, ctx.RemoteAddr(), path)
		return false
	}
}

// ErrNoSSHAuth is what PlanSSHAuth refuses a network bind with when nothing
// says who may connect. It is a sentinel so the command line can tell this
// refusal, which has a menu of answers, from a keys file that is broken, which
// has one.
var ErrNoSSHAuth = errors.New("refusing to serve SSH")

// SSHAuthPlan is what one bind decided to do about authentication.
type SSHAuthPlan struct {
	// Keys is set when authentication is on. Nil means every connection is
	// accepted, which only PlanSSHAuth may decide.
	Keys *AuthorizedKeys
	// Warning is the line printed at startup when authentication is off. Empty
	// when it is on.
	Warning string
}

// Authenticated reports whether this plan checks who is connecting.
func (p *SSHAuthPlan) Authenticated() bool { return p != nil && p.Keys.Enabled() }

// PlanSSHAuth decides how one bind authenticates, and refuses the bind that
// cannot be served safely.
//
// Two outcomes: keys are configured and the bind is served, or nothing is
// configured and the bind is refused unless the operator passes --no-auth.
//
// Loopback is refused too. It used to run with no authentication, and when
// TUIOS stopped reading ~/.ssh/authorized_keys by itself, a user whose key
// sat only there would have lost authentication in silence. Loopback is not
// a boundary on a machine with other users either. TUIOS does not fall back
// to ~/.ssh/authorized_keys for them: sshd_config can restrict those keys in
// ways TUIOS cannot see.
//
// noAuth wins over a keys file, unlike --insecure in tuios-web, which a
// certificate overrides. The reason is recovery: an operator locked out by a
// keys file that no longer holds their key needs one flag that gets them back
// in, and a flag that sometimes does nothing is not that.
func PlanSSHAuth(host, authorizedKeysPath string, noAuth bool) (*SSHAuthPlan, error) {
	if noAuth {
		return &SSHAuthPlan{Warning: noAuthWarning(host, "You started the server with --no-auth.")}, nil
	}

	keys, err := LoadAuthorizedKeys(authorizedKeysPath)
	if err != nil {
		return nil, err
	}
	if keys.Enabled() {
		return &SSHAuthPlan{Keys: keys}, nil
	}
	return nil, fmt.Errorf("%w on %s: there is no %s. Add your public key to it, pass --authorized-keys, or pass --no-auth",
		ErrNoSSHAuth, host, defaultAuthorizedKeysPath())
}

// noAuthWarning is the one loud line an unauthenticated server prints at
// startup. It says who gets the shell, because "no authentication" understates
// what this server hands out.
func noAuthWarning(host, why string) string {
	who := "Anyone on this network"
	if netutil.IsLoopbackHost(host) {
		who = "Anyone on this machine"
	}
	keyFile := defaultAuthorizedKeysPath()
	return fmt.Sprintf("Warning: this SSH server does not check who connects. %s %s can open a shell as %s. To turn authentication on, add a public key to %s:\n  mkdir -p %s && cat %s >> %s",
		why, who, currentAccount(), keyFile, filepath.Dir(keyFile), PublicKeyHint(), keyFile)
}

// PublicKeyHint names a public key file of this user for the commands TUIOS
// prints: the first of the usual ones that exists, or a placeholder.
func PublicKeyHint() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "<your public key file>"
	}
	for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		if _, err := os.Stat(filepath.Join(home, ".ssh", name)); err == nil {
			return "~/.ssh/" + name
		}
	}
	return "<your public key file>"
}

// currentAccount names the account a session's shells run as.
func currentAccount() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	return "the user running this server"
}
