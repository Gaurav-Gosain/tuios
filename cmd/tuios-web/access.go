package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/sip"
)

// Who may open a session.
//
// Every session is a full tuios, with shells as the account running the
// server and the session switcher reaching every other session. sip v0.8.2
// can guard the page and the WebSocket and WebTransport handshakes with HTTP
// Basic Auth (BasicUsername and BasicPassword), and until now nothing set
// them, so anyone who could reach the port got a shell.
//
// The rules:
//
//   - A bind outside this machine needs a password, or --no-auth. TLS alone
//     is not enough: it hides the traffic but does not say who is connecting.
//   - A loopback bind may run with no password, for a browser on this
//     machine. It checks the Host header, because a web page whose name the
//     attacker points at 127.0.0.1 (DNS rebinding) passes the same-origin
//     check: its Origin and its Host both carry the attacker's name.
//     --allow-host adds names for a reverse proxy, and needs a password or
//     --no-auth, because the proxy lets the network in.
//   - The password never comes from a flag value, which every user on the
//     machine can read in ps. It comes from a file, from TUIOS_WEB_PASSWORD,
//     or is generated at start and printed once. The variable is removed from
//     the environment once read, so no pane inherits it. That does not change
//     /proc/<pid>/environ, which the same user can still read, so the docs
//     point at --password-file.

// webPasswordEnv names the environment variable that can carry the password.
const webPasswordEnv = "TUIOS_WEB_PASSWORD"

// defaultWebUser is the Basic Auth user name when --user is not given.
const defaultWebUser = "tuios"

// webAccessFlags is the part of the command line that decides who may open a
// session.
type webAccessFlags struct {
	host           string
	port           string
	tls            bool
	user           string
	passwordFile   string
	randomPassword bool
	noAuth         bool
	allowHosts     []string
	// tlsArgs repeats the TLS flags the user gave, so a command copied from
	// the refusal keeps them.
	tlsArgs string
}

// webAccess is what one run decided about who may open a session.
type webAccess struct {
	user     string
	password string
	// hosts is the Host allowlist for the session handshake. Nil turns the
	// check off, which only a network bind with no --allow-host does: such a
	// server answers on names this process cannot know.
	hosts []string
	// loopback is whether the bind stays on this machine.
	loopback bool
	// announce holds the text that shows a generated password. It is printed
	// by announcePassword, after every other startup check has passed.
	announce string
}

// errNoWebAuth is the refusal for a network bind with no password. The menu
// of answers is printed beside it.
var errNoWebAuth = errors.New("refusing to serve with no password")

// planWebAccess settles the password and the Host allowlist before anything
// starts, and refuses a network bind that has neither a password nor
// --no-auth. It prints what the user needs to know to w.
func planWebAccess(w io.Writer, f webAccessFlags) (*webAccess, error) {
	a := &webAccess{user: f.user}
	if a.user == "" {
		a.user = defaultWebUser
	}

	// Read and clear the variable first, whatever else happens, so a refused
	// start does not leave it for a daemon started later in this process.
	envPassword, envSet := os.LookupEnv(webPasswordEnv)
	_ = os.Unsetenv(webPasswordEnv)
	envSet = envSet && envPassword != ""

	sources := 0
	for _, set := range []bool{f.passwordFile != "", f.randomPassword, envSet} {
		if set {
			sources++
		}
	}
	if sources > 1 {
		return nil, fmt.Errorf("give one password source: --password-file, --random-password or %s", webPasswordEnv)
	}
	if sources == 1 && f.noAuth {
		return nil, errors.New("a password and --no-auth do not go together. Remove --no-auth, or remove the password")
	}

	switch {
	case f.passwordFile != "":
		pw, err := readPasswordFile(f.passwordFile)
		if err != nil {
			return nil, err
		}
		a.password = pw
	case envSet:
		a.password = envPassword
	case f.randomPassword:
		pw, err := randomPassword()
		if err != nil {
			return nil, err
		}
		a.password = pw
	}

	loopback := isLoopbackHost(f.host)
	a.loopback = loopback
	if len(f.allowHosts) > 0 {
		if err := checkAllowHosts(f, a.password != ""); err != nil {
			return nil, err
		}
	}
	// The Host check is for loopback binds only. A network bind answers on
	// names and addresses this process cannot list, and its password is
	// what keeps strangers out.
	if loopback {
		a.hosts = append([]string{"localhost"}, f.allowHosts...)
	}

	switch {
	case a.password != "":
		if f.randomPassword {
			a.announce = randomPasswordText(f, a)
		}
	case f.noAuth:
		fmt.Fprintf(w, "\nWarning: tuios-web does not ask for a password. %s can open a shell as %s.\n\n",
			whoCanReach(f.host), currentAccount())
	case loopback:
		fmt.Fprintln(w, "Note: other users on this machine can connect. Use --random-password to stop this.")
	default:
		printNoAuthMenu(w, f)
		return nil, fmt.Errorf("%w on %s: pass --random-password, --password-file or --no-auth", errNoWebAuth, f.host)
	}
	return a, nil
}

// checkAllowHosts refuses --allow-host where it cannot work or would open a
// hole. A reverse proxy in front of a loopback server carries every request
// from the network, so --allow-host needs a password or --no-auth.
func checkAllowHosts(f webAccessFlags, hasPassword bool) error {
	if !isLoopbackHost(f.host) {
		return fmt.Errorf("the allowed host names work only with a loopback --host, such as localhost. %s is not loopback. Remove --allow-host", f.host)
	}
	for _, h := range f.allowHosts {
		if h == "" {
			return errors.New("an --allow-host value is empty. Give a host name, such as term.example.com")
		}
		if _, _, err := net.SplitHostPort(h); err == nil {
			return fmt.Errorf("the allowed host %s has a port. Give it to --allow-host with no port", h)
		}
		if strings.ContainsAny(h, "/:@") && net.ParseIP(strings.Trim(h, "[]")) == nil {
			return fmt.Errorf("the allowed host %s is not a host name. Give --allow-host a host name only, such as term.example.com", h)
		}
	}
	if !hasPassword && !f.noAuth {
		return errors.New("a reverse proxy lets the network in, so --allow-host needs a password. Add --random-password or --password-file, or add --no-auth")
	}
	return nil
}

// apply puts the decision on a sip config.
func (a *webAccess) apply(cfg *sip.Config) {
	if a.password != "" {
		cfg.BasicUsername = a.user
		cfg.BasicPassword = a.password
		// sip refuses Basic Auth over plain HTTP unless this is set. On a
		// loopback bind the password never crosses a network, so plain HTTP
		// is fine there. A network bind in plain HTTP already needs
		// --insecure, which sets this too.
		if a.loopback {
			cfg.AllowInsecureNoTLS = true
		}
	}
	if a.hosts != nil {
		cfg.ConnectMiddleware = append(cfg.ConnectMiddleware, hostCheckMiddleware(a.hosts))
	}
}

// hostCheckMiddleware refuses a session handshake whose Host header is not on
// the list. Loopback addresses always pass. It runs on the WebSocket and the
// WebTransport handshake, which are the only ways to a session. The page and
// its static files carry nothing a rebinding page could use.
func hostCheckMiddleware(allowed []string) sip.ConnectMiddleware {
	return func(next sip.ConnectHandler) sip.ConnectHandler {
		return func(r *http.Request) error {
			if !hostAllowed(r.Host, allowed) {
				return &sip.ConnectError{
					Status: http.StatusForbidden,
					Body:   "tuios-web does not answer to this host name. Start it with --allow-host to add the name.",
				}
			}
			return next(r)
		}
	}
}

// hostAllowed reports whether a Host header names this machine or a name on
// the list. The port is ignored.
func hostAllowed(hostHeader string, allowed []string) bool {
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return slices.ContainsFunc(allowed, func(a string) bool {
		return strings.EqualFold(strings.TrimSuffix(strings.Trim(a, "[]"), "."), host)
	})
}

// readPasswordFile reads the password from the first line of path. A file
// that other users can read is refused: the password in it is not secret.
//
// The checks run on the open file, not on the path, so the file cannot be
// swapped between the check and the read. The file must belong to the
// current user and must not be readable or writable by anyone else. Windows
// has no such mode bits, so there it is not checked.
func readPasswordFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the operator's own configuration
	if err != nil {
		return "", fmt.Errorf("cannot read the password file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("cannot read the password file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("the password file %s is not a regular file", path)
	}
	if err := checkPasswordFileMode(path, info, os.Getuid()); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", fmt.Errorf("cannot read the password file: %w", err)
	}
	pw, _, _ := strings.Cut(string(data), "\n")
	pw = strings.TrimRight(pw, "\r")
	if pw == "" {
		return "", fmt.Errorf("the password file %s is empty. Write a password on its first line", path)
	}
	return pw, nil
}

// randomPassword makes a password of 144 random bits.
func randomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot make a random password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// announcePassword prints a generated password. runWebServer calls it once
// the TLS check has passed, so a refused start prints no password.
func (a *webAccess) announcePassword(w io.Writer) {
	if a.announce != "" {
		_, _ = io.WriteString(w, a.announce)
	}
}

// randomPasswordText shows the generated password, and a URL that carries it
// so the first visit needs no typing.
func randomPasswordText(f webAccessFlags, a *webAccess) string {
	scheme := "http"
	if f.tls {
		scheme = "https"
	}
	host := f.host
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	u := url.URL{Scheme: scheme, User: url.UserPassword(a.user, a.password), Host: net.JoinHostPort(host, f.port), Path: "/"}
	return fmt.Sprintf("\nUser: %s\nPassword: %s\nOpen: %s\n\nThe password changes each time tuios-web starts.\n\n", a.user, a.password, u.String())
}

// printNoAuthMenu answers a network bind with no password. It is printed
// rather than carried in the error because fang reflows an error into one
// paragraph, which leaves no command to copy.
func printNoAuthMenu(w io.Writer, f webAccessFlags) {
	fmt.Fprintf(w, `
  %s is not this machine. Every browser that opens tuios-web gets a shell
  on this machine as %s. TLS encrypts the traffic, but it does not check who
  connects. Set a password:

  1. Make a new password each time tuios-web starts. tuios-web prints it.

       tuios-web --host %s --port %s%s --random-password

  2. Keep the password in a file that only you can read.

       (umask 077; head -c 18 /dev/urandom | base64 > ~/.config/tuios/web-password)
       tuios-web --host %s --port %s%s --password-file ~/.config/tuios/web-password

  3. Give the password in the %s environment variable.

  4. Keep tuios-web on this machine and connect through SSH.

       ssh -L %s:localhost:%s <this-machine>

  5. Let anyone who reaches the port in. Only on a network you trust.

       tuios-web --host %s --port %s%s --no-auth

`,
		f.host, currentAccount(),
		f.host, f.port, f.tlsArgs,
		f.host, f.port, f.tlsArgs,
		webPasswordEnv,
		f.port, f.port,
		f.host, f.port, f.tlsArgs)
}

// whoCanReach names who can reach a bind address.
func whoCanReach(host string) string {
	if isLoopbackHost(host) {
		return "Anyone on this machine"
	}
	return "Anyone who reaches this port"
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

// authFlagsForMenu is the password part of the command line, repeated in the
// TLS refusal so a command copied from it keeps the password.
func authFlagsForMenu(f webAccessFlags) string {
	switch {
	case f.passwordFile != "":
		return " --password-file " + f.passwordFile
	case f.randomPassword:
		return " --random-password"
	case f.noAuth:
		return " --no-auth"
	}
	return ""
}
