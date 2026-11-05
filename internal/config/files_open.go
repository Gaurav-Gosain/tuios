package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// FilesConfig is the [files] table: what the rail's files section does with a
// file. See FileOpenRule.
type FilesConfig struct {
	// Open maps a name pattern to the program file_open_system starts for a
	// match. The first rule that matches wins. With no match, the system
	// opener runs (xdg-open, open, or the Windows handler).
	Open []FileOpenRule `toml:"open"`

	// OpenCopyAskMB is the size in MiB above which o on a file of another
	// machine asks before it copies the file here. 0 or absent is 200.
	OpenCopyAskMB int `toml:"open_copy_ask_mb"`
	// OpenCopyMaxMB is the size in MiB above which o refuses to copy the
	// file. 0 or absent is 4096.
	OpenCopyMaxMB int `toml:"open_copy_max_mb"`
	// OpenCopyKeepDays is how many days a copy that was not opened again
	// stays. 0 or absent is 7, and a negative number keeps every copy until
	// tuios transfers clear-opened.
	OpenCopyKeepDays int `toml:"open_copy_keep_days"`
}

// The defaults of the open-a-copy limits, and the counts of files a folder
// copy asks above and refuses above. The refusal is the most a folder walk
// answers in one reply.
const (
	DefaultOpenCopyAskMB    = 200
	DefaultOpenCopyMaxMB    = 4096
	DefaultOpenCopyKeepDays = 7
	OpenCopyAskFiles        = 1000
	OpenCopyMaxFiles        = 20000
)

// OpenCopyAskBytes is the size above which o on a far file asks first.
func (c FilesConfig) OpenCopyAskBytes() int64 {
	return mib(c.OpenCopyAskMB, DefaultOpenCopyAskMB)
}

// OpenCopyMaxBytes is the size above which o on a far file refuses.
func (c FilesConfig) OpenCopyMaxBytes() int64 {
	return mib(c.OpenCopyMaxMB, DefaultOpenCopyMaxMB)
}

// OpenCopyKeep is how long a copy that was not opened again stays. Zero
// keeps every copy.
func (c FilesConfig) OpenCopyKeep() time.Duration {
	switch {
	case c.OpenCopyKeepDays < 0:
		return 0
	case c.OpenCopyKeepDays == 0:
		return DefaultOpenCopyKeepDays * 24 * time.Hour
	}
	return time.Duration(c.OpenCopyKeepDays) * 24 * time.Hour
}

func mib(n, def int) int64 {
	if n <= 0 {
		n = def
	}
	return int64(n) << 20
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
