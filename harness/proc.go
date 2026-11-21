package harness

import (
	"path"
	"strings"
)

// ProcInfo is one process reduced to the three descriptions detection matches
// against. It is the shared shape so the manifest registry and the built-in name
// list read a process the same way; two matchers disagreeing about what counts as
// evidence is how a careful rule ends up bypassed by a careless one.
type ProcInfo struct {
	// Comm is the process name the kernel reports. It is truncated (15 bytes on
	// Linux, 16 on darwin) and a program may rewrite it.
	Comm string
	// Argv is the full command line.
	Argv []string
	// Exe is the resolved executable path, empty when it cannot be read.
	Exe string
}

// CommBase is Comm reduced to a lowercased base name.
func (p ProcInfo) CommBase() string { return BaseName(p.Comm) }

// ExeBase is the base name of the resolved executable.
func (p ProcInfo) ExeBase() string { return BaseName(p.Exe) }

// Argv0Base is the base name of argv[0].
func (p ProcInfo) Argv0Base() string {
	if len(p.Argv) == 0 {
		return ""
	}
	return BaseName(p.Argv[0])
}

var interpreters = map[string]struct{}{
	"node": {}, "nodejs": {}, "deno": {}, "bun": {},
	"python": {}, "python2": {}, "python3": {}, "uv": {}, "uvx": {},
	"npx": {}, "pnpm": {}, "yarn": {}, "bunx": {},
	"sh": {}, "bash": {}, "zsh": {}, "fish": {}, "env": {},
}

var versionedInterpreters = []string{"python", "node", "nodejs"}

// IsInterpreter reports whether a base name is an interpreter or launcher. A
// versioned name counts only when what follows the family is a version and
// nothing else, so python3.14 and node22 count and python3-config and
// node-gyp do not.
func IsInterpreter(base string) bool {
	if _, ok := interpreters[base]; ok {
		return true
	}
	for _, family := range versionedInterpreters {
		if v, ok := strings.CutPrefix(base, family); ok && isVersion(v) {
			return true
		}
	}
	return false
}

func isVersion(s string) bool {
	s = strings.TrimSuffix(s, "t")
	if s == "" {
		return false
	}
	for part := range strings.SplitSeq(s, ".") {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	return true
}

var launchers = map[string]struct{}{
	"timeout": {}, "sudo": {}, "doas": {}, "nohup": {}, "script": {},
	"stdbuf": {}, "unbuffer": {}, "chrt": {}, "ionice": {}, "taskset": {},
	"npm": {}, "npm exec": {}, "corepack": {}, "pipx": {},
	"mise": {}, "asdf": {}, "volta": {}, "rtx": {},
	"nix": {}, "nix-shell": {}, "direnv": {},
}

// IsLauncher reports whether a base name is a resident launcher.
func IsLauncher(base string) bool {
	_, ok := launchers[base]
	return ok
}

// Wraps reports whether this process stands in for another one, so the process
// group behind it is worth reading: an interpreter, a shell, or a launcher, by
// either its name or its executable.
func (p ProcInfo) Wraps() bool {
	commBase, exeBase := p.CommBase(), p.ExeBase()
	return IsInterpreter(commBase) || IsLauncher(commBase) ||
		IsInterpreter(exeBase) || IsLauncher(exeBase)
}

var runnerSubcommands = map[string]struct{}{
	"run": {}, "exec": {}, "dlx": {}, "x": {}, "tool": {}, "--": {},
}

// RunToken is the single argv token that names what an interpreter is running,
// or "" when the process is not an interpreter at all.
//
// It is deliberately one token rather than a scan of the command line. An agent's
// name appearing anywhere in an argument is not evidence: "python3 -m pytest
// tests/aider/test_x.py" is a test run inside aider's own repository, and
// "tail -f ~/dev/opencode/main.go" is an editor. Both were identified as those
// agents by a scan, and mislabelling an unrelated pane is worse than missing a
// real agent. The token an interpreter was asked to execute is the one place the
// name means what it says.
func (p ProcInfo) RunToken() string {
	commBase, exeBase := p.CommBase(), p.ExeBase()
	if !(commBase == "" || IsInterpreter(commBase)) && !(exeBase != "" && IsInterpreter(exeBase)) {
		return ""
	}
	if len(p.Argv) < 2 {
		return ""
	}
	for _, tok := range p.Argv[1:] {
		tok = strings.TrimRight(tok, "\x00")
		if tok == "" || (strings.HasPrefix(tok, "-") && tok != "--") {
			continue
		}
		if _, skip := runnerSubcommands[strings.ToLower(tok)]; skip {
			continue
		}
		return tok
	}
	return ""
}

// BaseName reduces a comm value or an argv token to the name used for matching:
// no directory, no trailing NUL, no login-shell "-" prefix, no script extension,
// lowercased.
func BaseName(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "\x00"))
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(path.Base(slashed(s)), "-")
	lower := strings.ToLower(s)
	for _, ext := range scriptExtensions {
		if before, ok := strings.CutSuffix(lower, ext); ok {
			return before
		}
	}
	return lower
}

func slashed(s string) string { return strings.ReplaceAll(s, `\`, "/") }

var scriptExtensions = []string{".js", ".mjs", ".cjs", ".ts", ".py"}

func segments(s string) []string {
	s = strings.TrimRight(s, "\x00")
	if s == "" {
		return nil
	}
	parts := strings.Split(strings.ToLower(slashed(s)), "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

var packageMarkers = map[string]struct{}{
	"node_modules": {}, "site-packages": {}, "dist-packages": {},
}

func isPackageRoot(segs []string, i int) bool {
	if _, ok := packageMarkers[segs[i]]; ok {
		return true
	}
	return i >= 2 && segs[i-2] == ".bun" && segs[i-1] == "install" && segs[i] == "cache"
}

func packageSegments(have, want []string) bool {
	if len(want) == 0 || len(have) < len(want) {
		return false
	}
	scoped := strings.HasPrefix(want[0], "@")
	for i := 0; i+len(want) <= len(have); i++ {
		if !runEqual(have[i:i+len(want)], want) {
			continue
		}
		switch {
		case len(have) == 1:
			return true
		case scoped:
			return true
		case i >= 1 && isPackageRoot(have, i-1):
			return true
		case i >= 2 && strings.HasPrefix(have[i-1], "@") && isPackageRoot(have, i-2):
			return true
		}
	}
	return false
}

// PackageNamed reports whether token, the script an interpreter runs, sits under
// a package called name: node_modules/name/cli.js, site-packages/name/main.py.
// It is packageSegments for a single bare name, exported for the name list.
func PackageNamed(token, name string) bool {
	want := segments(name)
	if len(want) != 1 {
		return false
	}
	return packageSegments(segments(token), want)
}

func runEqual(have, want []string) bool {
	for j := range want {
		if !segmentEqual(have[j], want[j], j == len(want)-1) {
			return false
		}
	}
	return true
}

func segmentEqual(have, want string, last bool) bool {
	if have == want {
		return true
	}
	if !last {
		return false
	}
	rest, ok := strings.CutPrefix(have, want)
	return ok && (strings.HasPrefix(rest, "@") || strings.HasPrefix(rest, "=="))
}

func matchExeGlob(pattern, p string) bool {
	pat := segments(pattern)
	segs := segments(p)
	if strings.HasPrefix(pattern, "/") {
		return matchSegments(pat, segs)
	}
	for i := 0; i <= len(segs); i++ {
		if matchSegments(pat, segs[i:]) {
			return true
		}
	}
	return false
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
