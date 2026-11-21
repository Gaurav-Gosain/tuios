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
	"regexp"
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
	// Title is the dialog's heading.
	Title string
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
	p := Prompt{Text: text, Kind: KindSecret, Title: "Enter a secret"}
	switch {
	case strings.Contains(lower, "passphrase"):
		p.Kind, p.Title = KindPassphrase, "Unlock a key"
	case pinWord.MatchString(lower):
		p.Kind, p.Title = KindPIN, "Enter a PIN"
	case strings.Contains(lower, "password"):
		p.Kind, p.Title = KindPassword, "Enter a password"
	}
	p.Subject = Tidy(subject(text, lower))
	return p
}

var pinWord = regexp.MustCompile(`\bpin\b`)

// subject finds the key file or the account in a prompt: the quoted path of
// "Enter passphrase for key '/home/u/.ssh/id_ed25519'", the account of
// "git@host's password", the target of "Password for 'https://host'".
func subject(text, lower string) string {
	for _, lead := range []string{"passphrase for key", "passphrase for", "pin for key", "pin for", "password for"} {
		if i := strings.Index(lower, lead); i >= 0 {
			rest := text[i+len(lead):]
			// ssh names the key's type first: "PIN for ECDSA-SK key /k".
			if j := strings.Index(strings.ToLower(rest), " key "); j >= 0 && !strings.ContainsAny(rest[:j], "'\"/") {
				rest = rest[j+len(" key "):]
			}
			return unquote(rest)
		}
	}
	if i := strings.Index(lower, "'s password"); i > 0 {
		return unquote(text[:i])
	}
	return ""
}

// unquote takes the quoted part of s when it has one, else s without the
// punctuation around it.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	for _, q := range []string{"'", "\""} {
		if _, rest, ok := strings.Cut(s, q); ok {
			if in, _, ok := strings.Cut(rest, q); ok && in != "" {
				return in
			}
		}
	}
	if i := strings.IndexAny(s, "?\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.Trim(s, "'\":"))
}

// Tidy writes the home directory at the start of a path in s as ~.
func Tidy(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return s
	}
	home = strings.TrimRight(home, "/")
	if s == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(s, home+"/"); ok {
		return "~/" + rest
	}
	return s
}

// knownPrograms are the programs that ask through an askpass program in the
// normal course of their work. A prompt from any other program is shown with
// a warning: it may be a program that fishes for a password.
var knownPrograms = map[string]bool{
	"ssh": true, "ssh-add": true, "ssh-keygen": true, "scp": true, "sftp": true,
	"git": true, "git-remote-http": true, "git-remote-https": true,
	"gpg": true, "gpg2": true, "tuios": true,
}

// KnownProgram reports whether the executable at path is one that asks
// through an askpass program in the normal course of its work.
func KnownProgram(path string) bool {
	base := path
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return knownPrograms[strings.TrimSuffix(base, " (deleted)")]
}
