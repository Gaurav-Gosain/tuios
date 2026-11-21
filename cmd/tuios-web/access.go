package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/netutil"
)

const webPasswordEnv = "TUIOS_WEB_PASSWORD"

const minWebPasswordRunes = 12

const defaultWebUser = "tuios"

type webAccessFlags struct {
	host           string
	port           string
	tls            bool
	user           string
	passwordFile   string
	randomPassword bool
	noAuth         bool
	allowHosts     []string
	tlsArgs        string
}

type webAccess struct {
	user       string
	password   string
	allowHosts []string
	loopback   bool
	announce   string
}

var errNoAuthWildcard = errors.New("refusing --no-auth on an address that listens on every interface")

var errNoWebAuth = errors.New("refusing to serve with no password")

func planWebAccess(w io.Writer, f webAccessFlags) (*webAccess, error) {
	a := &webAccess{user: f.user}
	if a.user == "" {
		a.user = defaultWebUser
	}

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

	if f.noAuth && isUnspecifiedHost(f.host) {
		return nil, fmt.Errorf("%w, such as %s. A web page in a browser on this machine can then open a shell. Use --host localhost, or set a password with --random-password or --password-file",
			errNoAuthWildcard, f.host)
	}

	loopback := isLoopbackHost(f.host)
	a.loopback = loopback
	if len(f.allowHosts) > 0 {
		if err := checkAllowHosts(f, a.password != ""); err != nil {
			return nil, err
		}
	}
	a.allowHosts = f.allowHosts

	switch {
	case a.password != "":
		if f.randomPassword {
			a.announce = randomPasswordText(f, a)
		} else if n := utf8.RuneCountInString(a.password); n < minWebPasswordRunes {
			fmt.Fprintf(w, "Warning: the password is %d characters long. Someone on the network can guess a short password. Use --random-password, or a password of at least %d characters.\n",
				n, minWebPasswordRunes)
		}
	case f.noAuth:
		fmt.Fprintf(w, "\nWarning: tuios-web does not ask for a password. %s can open a shell as %s.\n\n",
			whoCanReach(f.host), netutil.CurrentAccount())
	case loopback:
		fmt.Fprintln(w, "Note: other users on this machine can connect. Use --random-password to stop this.")
	default:
		printNoAuthMenu(w, f)
		return nil, fmt.Errorf("%w on %s: pass --random-password, --password-file or --no-auth", errNoWebAuth, f.host)
	}
	return a, nil
}

func checkAllowHosts(f webAccessFlags, hasPassword bool) error {
	if !isLoopbackHost(f.host) {
		return fmt.Errorf("the allowed host names work only with a loopback --host, such as localhost. %s is not loopback. Remove --allow-host", f.host)
	}
	for _, h := range f.allowHosts {
		if h == "" {
			return errors.New("an --allow-host value is empty. Give a host name, such as term.example.com")
		}
		if h == "*" {
			return errors.New("the allowed host * is not a host name. Give --allow-host a host name only, such as term.example.com")
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

func (a *webAccess) apply(cfg *sip.Config) {
	if a.password != "" {
		cfg.BasicUsername = a.user
		cfg.BasicPassword = a.password
		if a.loopback {
			cfg.AllowInsecureNoTLS = true
		}
	}
	cfg.AllowedHosts = a.allowHosts
}

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
	if err := checkPasswordFileMode(f, path, info, os.Getuid()); err != nil {
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

func randomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot make a random password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (a *webAccess) announcePassword(w io.Writer) {
	if a.announce != "" {
		_, _ = io.WriteString(w, a.announce)
	}
}

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

`,
		f.host, netutil.CurrentAccount(),
		f.host, f.port, f.tlsArgs,
		f.host, f.port, f.tlsArgs,
		webPasswordEnv,
		f.port, f.port)
	if !isUnspecifiedHost(f.host) {
		fmt.Fprintf(w, `  5. Let anyone who reaches the port in. Only on a network you trust.

       tuios-web --host %s --port %s%s --no-auth

`, f.host, f.port, f.tlsArgs)
	}
}

func isUnspecifiedHost(host string) bool { return netutil.IsUnspecifiedHost(host) }

func whoCanReach(host string) string {
	if isLoopbackHost(host) {
		return "Anyone on this machine"
	}
	return "Anyone who reaches this port"
}

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
