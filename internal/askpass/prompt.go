// Package askpass reads the prompts that ssh, git and gpg put to a person
// through an askpass program, so tuios can say in plain words what is asked
// and what for.
//
// The prompt text comes from a program inside a pane, so nothing here trusts
// it: it only decides how a dialog words the question. Who asks is told by the
// daemon from the kernel's record of the process, never by this text.
package askpass

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Kinds of prompt. They are wire values: the kind an Inbox item carries.
const (
	// KindPassphrase unlocks a key file.
	KindPassphrase = "passphrase"
	// KindPassword is a password for an account on a host.
	KindPassword = "password"
	// KindPIN is a security key's or a smart card's PIN.
	KindPIN = "pin"
	// KindSecret is any other prompt: the answer is hidden all the same.
	KindSecret = "secret"
	// KindConfirm is a yes or no question, such as ssh's about a host key
	// it has not seen. The answer shows as it is typed.
	KindConfirm = "confirm"
	// KindUsername is a user name, such as git's for an https remote. The
	// answer shows as it is typed.
	KindUsername = "username"
)

// Prompt is what a dialog shows for one askpass prompt.
type Prompt struct {
	// Kind is one of the kinds above.
	Kind string
	// Subject is the key file or the account the prompt names, with the home
	// directory written as ~. Empty when the prompt names none.
	Subject string
	// Text is the prompt as the program wrote it, without its trailing colon
	// and spaces.
	Text string
}

// Read classifies a prompt.
func Read(prompt string) Prompt {
	text := strings.TrimRight(strings.TrimSpace(prompt), ": ")
	lower := strings.ToLower(text)
	p := Prompt{Text: text, Kind: KindSecret}
	switch {
	case strings.Contains(lower, "yes/no"):
		p.Kind = KindConfirm
	case strings.HasPrefix(lower, "username"):
		p.Kind = KindUsername
	case strings.Contains(lower, "passphrase"):
		p.Kind = KindPassphrase
	case strings.Contains(" "+lower+" ", " pin "):
		p.Kind = KindPIN
	case strings.Contains(lower, "password"):
		p.Kind = KindPassword
	}
	p.Subject = Tidy(subject(text, lower))
	return p
}

// subject finds the key file or the account in a prompt: the quoted path of
// "Enter passphrase for key '/home/u/.ssh/id_ed25519'", the account of
// "git@host's password", the target of "Password for 'https://host'".
func subject(text, lower string) string {
	if i := strings.Index(lower, "'s password"); i > 0 {
		return unquote(text[:i])
	}
	i := strings.Index(lower, " for ")
	if i < 0 {
		return ""
	}
	rest := text[i+len(" for "):]
	// ssh names the key's type first: "PIN for ECDSA-SK key /k".
	if j := strings.Index(rest, "key "); j >= 0 && !strings.ContainsAny(rest[:j], "'\"/") {
		rest = rest[j+len("key "):]
	}
	return unquote(rest)
}

// unquote takes the quoted part of s when it has one, else s without the
// punctuation around it.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if q := strings.IndexAny(s, "'\""); q >= 0 {
		if end := strings.IndexByte(s[q+1:], s[q]); end > 0 {
			return s[q+1 : q+1+end]
		}
	}
	return strings.Trim(s, "'\":? ")
}

// Tidy writes the home directory at the start of a path in s as ~.
func Tidy(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 2 {
		return s
	}
	if rest, ok := strings.CutPrefix(s, home); ok && (rest == "" || rest[0] == '/') {
		return "~" + rest
	}
	return s
}

// systemDirs are the folders a package manager installs ssh, git and gpg
// in. A process in a pane cannot write to them.
var systemDirs = []string{
	"/usr/bin", "/bin", "/usr/sbin", "/sbin", "/usr/local/bin", "/opt/homebrew/bin",
	"/usr/lib/git-core", "/usr/libexec/git-core", "/usr/local/libexec/git-core", "/opt/homebrew/libexec/git-core",
}

// KnownProgram reports whether exe, the resolved path of the program that
// asks, is one that asks through an askpass program in the normal course of
// its work: ssh, git or gpg by name, and found where that program lives. A
// copy of any binary named ssh in a folder the pane can write does not count,
// so its prompt shows a warning: it may fish for a password.
//
// A program lives in a system folder, or is the one the daemon's PATH finds
// for its name.
func KnownProgram(exe string) bool {
	exe = strings.TrimSuffix(exe, " (deleted)")
	name := filepath.Base(exe)
	switch name {
	case "ssh", "ssh-add", "ssh-keygen", "scp", "sftp", "git", "git-remote-http", "git-remote-https", "gpg", "gpg2":
	default:
		return false
	}
	if slices.Contains(systemDirs, filepath.Dir(exe)) {
		return true
	}
	found, err := exec.LookPath(name)
	if err != nil {
		return false
	}
	found, err = filepath.EvalSymlinks(found)
	return err == nil && found == exe
}

// credentialFlags are the flags whose value is a password or a token.
var credentialFlags = []string{"--password", "--passwd", "--pass", "--passphrase", "--token", "--secret", "--api-key"}

// MaskCommandLine joins a command line for the dialog with its credentials
// replaced by ***: the password and token in a URL's userinfo, the value of a
// password or token flag, and sshpass's -p. The user name in a URL stays, so
// the person can tell which account asks.
func MaskCommandLine(args []string) string {
	out := make([]string, len(args))
	maskNext := false
	// sshpass's own options come before the command it runs, whose -p is
	// ssh's port.
	sshpassOpts := len(args) > 0 && filepath.Base(args[0]) == "sshpass"
	for i, a := range args {
		if i > 0 && !maskNext && !strings.HasPrefix(a, "-") {
			sshpassOpts = false
		}
		switch {
		case maskNext:
			a, maskNext = "***", false
		case slices.Contains(credentialFlags, a) || (a == "-p" && sshpassOpts):
			maskNext = true
		default:
			for _, f := range credentialFlags {
				if strings.HasPrefix(a, f+"=") {
					a = f + "=***"
				}
			}
		}
		out[i] = maskURLUserinfo(a)
	}
	return strings.Join(out, " ")
}

// maskURLUserinfo replaces the password in every URL's userinfo in s:
// https://user:TOKEN@host gives https://user:***@host. A userinfo with no
// colon is a token in an http or https URL, https://TOKEN@host gives
// https://***@host, and a user name in any other, so ssh://git@host stays.
func maskURLUserinfo(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "://")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		scheme := strings.ToLower(s[strings.LastIndexAny(s[:i], " =:")+1 : i])
		b.WriteString(s[:i+3])
		s = s[i+3:]
		end := strings.IndexAny(s, "/?# ")
		if end < 0 {
			end = len(s)
		}
		if at := strings.LastIndexByte(s[:end], '@'); at >= 0 {
			switch user, _, ok := strings.Cut(s[:at], ":"); {
			case ok:
				b.WriteString(user)
				b.WriteString(":***")
			case scheme == "http" || scheme == "https":
				b.WriteString("***")
			default:
				b.WriteString(user)
			}
			s = s[at:]
		}
	}
}
