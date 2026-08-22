package applist

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// executable reports whether the file is one Windows would run from a bare
// name. There are no execute bits, so the extension is the whole test, and
// %PATHEXT% is the list the shell itself consults.
func executable(info fs.FileInfo) bool {
	ext := strings.ToLower(filepath.Ext(info.Name()))
	if ext == "" {
		return false
	}
	for _, want := range pathExt() {
		if ext == want {
			return true
		}
	}
	return false
}

func pathExt() []string {
	raw := os.Getenv("PATHEXT")
	if raw == "" {
		raw = ".COM;.EXE;.BAT;.CMD"
	}
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Argv is the argv that runs e as a pane's own process. CreateProcess only
// starts real executables; a batch file runs through the interpreter cmd
// itself would hand it to.
//
// An entry carrying an Exec is run by it verbatim, the same as on unix. Nothing
// on Windows produces one, since desktop entries are a unix concept, but the
// field is on Entry everywhere and an entry that carries one means it.
func (e Entry) Argv() []string {
	if len(e.Exec) > 0 {
		return e.Exec
	}
	switch strings.ToLower(filepath.Ext(e.Path)) {
	case ".bat", ".cmd":
		return []string{"cmd.exe", "/c", e.Path}
	}
	return []string{e.Path}
}

// CommandLine is e written the way the user would have typed it, for a launcher
// that puts the command on a shell's prompt instead of running it.
//
// The bare name is preferred over the listed path because it resolves to the
// same file: Scan applies the same %PATH% and %PATHEXT% rule the shell does. A
// name carrying a space or a quote is wrapped in double quotes, which is the
// one quoting form both cmd.exe and PowerShell read the same way.
//
// An entry carrying an Exec has no bare name that resolves to it, so the line
// is the argv itself with each element quoted by the same rule and joined by
// spaces.
func (e Entry) CommandLine() string {
	if len(e.Exec) > 0 {
		parts := make([]string, len(e.Exec))
		for i, arg := range e.Exec {
			parts[i] = winQuote(arg)
		}
		return strings.Join(parts, " ")
	}
	if !strings.ContainsAny(e.Name, " \t\"'&|<>^%!") {
		return e.Name
	}
	return `"` + strings.ReplaceAll(e.Path, `"`, `""`) + `"`
}

// winQuote wraps s in double quotes when it holds a character a shell would
// re-read, doubling any quote inside it. Double quotes are the one form
// cmd.exe and PowerShell agree on.
func winQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'&|<>^%!") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
