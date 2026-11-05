package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// FilesConfig is the [files] table: what the rail's files section does with a
// file. See FileOpenRule.
type FilesConfig struct {
	// Open maps a name pattern to the program file_open_system starts for a
	// match. The first rule that matches wins. With no match, the system
	// opener runs (xdg-open, open, or the Windows handler).
	Open []FileOpenRule `toml:"open"`
}

// FileOpenRule is one [[files.open]] entry.
//
//	[[files.open]]
//	match = "*.pdf"
//	command = ["zathura", "{path}"]
//
// The command is an argv, never a shell line. {path} in any word becomes the
// path, and with no {path} the path goes last. A name in the listing is
// whatever the program that made the file chose, so a shell line would turn a
// crafted name into code.
type FileOpenRule struct {
	// Match is a glob, as filepath.Match reads it, compared without regard to
	// case. A pattern with no slash is matched against the base name, and one
	// with a slash against the whole path. A trailing slash makes the rule
	// match folders and only folders: "*/" is every folder. A rule without one
	// matches files only.
	Match string `toml:"match"`
	// Command is the argv to start.
	Command []string `toml:"command"`
}

// FileOpenPathToken is the word in a rule's command that the path replaces.
const FileOpenPathToken = "{path}"

// matches reports whether the rule applies to path. A pattern filepath.Match
// cannot read matches nothing; validation names it.
func (r FileOpenRule) matches(path string, isDir bool) bool {
	pattern, forDirs := strings.CutSuffix(strings.TrimSpace(r.Match), "/")
	if pattern == "" || forDirs != isDir {
		return false
	}
	subject := filepath.Base(path)
	if strings.Contains(pattern, "/") {
		subject = filepath.ToSlash(path)
	}
	ok, err := filepath.Match(strings.ToLower(pattern), strings.ToLower(subject))
	return err == nil && ok
}

// argv is the rule's command with the path put in.
func (r FileOpenRule) argv(path string) []string {
	argv := make([]string, len(r.Command))
	placed := false
	for i, word := range r.Command {
		if strings.Contains(word, FileOpenPathToken) {
			word = strings.ReplaceAll(word, FileOpenPathToken, path)
			placed = true
		}
		argv[i] = word
	}
	if !placed {
		argv = append(argv, path)
	}
	return argv
}

// usable reports whether a rule can run at all. Validation warns about the
// others, and OpenArgv skips them.
func (r FileOpenRule) usable() bool {
	if len(r.Command) == 0 || strings.TrimSpace(r.Command[0]) == "" {
		return false
	}
	pattern, _ := strings.CutSuffix(strings.TrimSpace(r.Match), "/")
	_, err := filepath.Match(pattern, "")
	return pattern != "" && err == nil
}

// OpenArgv returns the argv of the first usable rule that matches path, or nil
// when none does.
func (c FilesConfig) OpenArgv(path string, isDir bool) []string {
	for _, r := range c.Open {
		if r.usable() && r.matches(path, isDir) {
			return r.argv(path)
		}
	}
	return nil
}

// validateFilesOpen warns about a [[files.open]] rule that can never run. Such
// a rule is skipped, so a file it was meant for goes to the system opener.
func validateFilesOpen(cfg *UserConfig, result *ValidationResult) {
	for i, r := range cfg.Files.Open {
		key := fmt.Sprintf("open[%d]", i+1)
		pattern, _ := strings.CutSuffix(strings.TrimSpace(r.Match), "/")
		_, badGlob := filepath.Match(pattern, "")
		var problem string
		switch {
		case pattern == "":
			problem = "The rule has no match pattern"
		case badGlob != nil:
			problem = fmt.Sprintf("The match pattern %q is not a valid glob", r.Match)
		case len(r.Command) == 0 || strings.TrimSpace(r.Command[0]) == "":
			problem = "The rule has no command"
		default:
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "files", Key: key,
			Message: problem + ". tuios skips the rule. Fix it, or remove it.",
		})
	}
}
