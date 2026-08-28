package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
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
// config home. It is read before ~/.ssh/authorized_keys so a user can grant
// TUIOS a narrower set than their sshd trusts.
const ConfigAuthorizedKeys = "tuios/authorized_keys"

// AuthorizedKeys is the set of public keys that may open a session, and the
// file they came from.
//
// A zero value means no file exists, which is not the same as a file holding
// no keys. The first is "the user never configured this" and is allowed to run
// unauthenticated on loopback; the second is a mistake and stops startup.
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

// AuthorizedKeysCandidates lists the files searched for public keys, in search
// order. An explicit path is the only candidate: naming a file and being given
// a different one is worse than being told the file is missing.
func AuthorizedKeysCandidates(explicit string) []string {
	if explicit != "" {
		return []string{explicit}
	}
	candidates := []string{filepath.Join(xdg.ConfigHome, ConfigAuthorizedKeys)}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".ssh", "authorized_keys"))
	}
	return candidates
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
		keys, err := parseAuthorizedKeys(path, data)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("the authorized keys file %s holds no keys. Add a public key to it, or move the file away", path)
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
func parseAuthorizedKeys(path string, data []byte) ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("line %d of %s is not a public key: %w. Fix the line, or delete it", i+1, path, err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// readAuthorizedKeysPath reads and parses one known file, for the auth handler.
func readAuthorizedKeysPath(path string) ([]ssh.PublicKey, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path was resolved at startup
	if err != nil {
		return nil, err
	}
	return parseAuthorizedKeys(path, data)
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
// It mirrors checkTransportSecurity in cmd/tuios-web, which refuses a
// non-loopback bind that would carry keystrokes in clear text. Same shape, same
// three outcomes: configured and allowed, loopback and allowed, non-loopback
// and refused unless the operator opts out by hand. The two gates share
// netutil.IsLoopbackHost so they cannot disagree about which address is on the
// network.
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
	if !netutil.IsLoopbackHost(host) {
		return nil, fmt.Errorf("%w on %s with no authentication: add a public key to %s, or pass --no-auth to accept it",
			ErrNoSSHAuth, host, filepath.Join(xdg.ConfigHome, ConfigAuthorizedKeys))
	}
	return &SSHAuthPlan{Warning: noAuthWarning(host, "TUIOS found no authorized keys file.")}, nil
}

// noAuthWarning is the one loud line an unauthenticated server prints at
// startup. It says who gets the shell, because "no authentication" understates
// what this server hands out.
func noAuthWarning(host, why string) string {
	who := "Anyone on this network"
	if netutil.IsLoopbackHost(host) {
		who = "Anyone on this machine"
	}
	return fmt.Sprintf("Warning: this SSH server does not check who connects. %s %s can open a shell as %s. Add a public key to %s to turn authentication on.",
		why, who, currentAccount(), filepath.Join(xdg.ConfigHome, ConfigAuthorizedKeys))
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
