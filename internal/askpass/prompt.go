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
	"path/filepath"
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

// KnownProgram reports whether the executable at path is one that asks
// through an askpass program in the normal course of its work. A prompt from
// any other program is shown with a warning: it may fish for a password.
func KnownProgram(path string) bool {
	switch strings.TrimSuffix(filepath.Base(path), " (deleted)") {
	case "ssh", "ssh-add", "ssh-keygen", "scp", "sftp", "git", "git-remote-http", "git-remote-https", "gpg", "gpg2", "tuios":
		return true
	}
	return false
}
