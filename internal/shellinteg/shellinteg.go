// Package shellinteg turns on OSC 133 prompt marks in the bash and zsh shells
// tuios starts in its panes, the way Ghostty and kitty do, without editing a
// file of the user's.
//
// The marks are what tuios run, wait-for command-finished, capture-pane
// --last-command, the scrollback browser and the reflow of an open prompt all
// read. A shell sends none of them on its own, and fish 4 and nushell send
// them by themselves, so only bash and zsh are set up here:
//
//   - zsh starts with ZDOTDIR naming a folder of tuios's. Its .zshenv puts
//     ZDOTDIR back, reads the user's .zshenv, and loads the marks. zsh then
//     reads the user's .zprofile, .zshrc and .zlogin from the restored
//     ZDOTDIR, in the order it always does.
//   - bash starts with --rcfile naming a file of tuios's, which reads
//     ~/.bashrc and then adds the marks. tuios starts bash as an interactive
//     shell that is not a login shell, which is the shell that reads
//     ~/.bashrc, so --rcfile changes nothing else about the startup.
//     BASH_ENV, ENV and HISTFILE stay as they are. With [shell] login on,
//     the file reads the login files in place of ~/.bashrc (see Login).
//
// Each script looks at what the user's own setup sends first and sends only
// the marks it does not, so a prompt that marks itself is not marked twice.
//
// The files are written once to a folder under the XDG state home, named by
// the hash of their content, and checked against it the first time a
// process uses them. A new tuios with different scripts writes a new folder,
// so a daemon that still runs an older tuios keeps the files it reads.
package shellinteg

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/adrg/xdg"
)

//go:embed scripts/tuios.bash
var bashScript []byte

//go:embed scripts/zshenv.zsh
var zshEnvScript []byte

//go:embed scripts/tuios.zsh
var zshScript []byte

// BashLoginEnv tells tuios's bash rc file to read the files a login bash
// reads, in place of ~/.bashrc. It unsets it.
const BashLoginEnv = "TUIOS_BASH_LOGIN"

// ZDOTDIREnv carries the user's ZDOTDIR past the shim, which sets it back.
// Its absence means the user had none.
const ZDOTDIREnv = "TUIOS_ZSH_ZDOTDIR"

// file is one script and where it goes under the folder.
type file struct {
	rel  string
	data []byte
}

var files = []file{
	{"bash/tuios.bash", bashScript},
	{"zsh/.zshenv", zshEnvScript},
	{"zsh/tuios.zsh", zshScript},
}

// Shell names the shell a program path runs, for the shells tuios sets up:
// "bash", "zsh", or "" for any other. It goes by the name alone. /bin/sh is
// not bash here even where it links to bash, because bash started as sh runs
// in POSIX mode and reads none of this.
func Shell(path string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	switch name := strings.TrimPrefix(filepath.Base(path), "-"); name {
	case "bash", "zsh":
		return name
	}
	return ""
}

// MarksItself reports whether the shell at path sends its prompt marks by
// itself, with nothing for tuios to add: fish 4 and newer, and nushell.
func MarksItself(path string) bool {
	switch strings.TrimPrefix(filepath.Base(path), "-") {
	case "fish", "nu":
		return true
	}
	return false
}

// Setup returns the arguments to start shell with and its environment, built
// on env, so that the shell marks its prompts. ok is false for a shell this
// package does not set up, or when the files cannot be written, and the shell
// then starts as it is. env is not changed.
func Setup(shell string, env []string) (args, out []string, ok bool) {
	kind := Shell(shell)
	if kind == "" {
		return nil, nil, false
	}
	dir, err := Dir()
	if err != nil {
		return nil, nil, false
	}
	switch kind {
	case "bash":
		return []string{"--rcfile", filepath.Join(dir, "bash", "tuios.bash")}, slices.Clone(env), true
	case "zsh":
		out = make([]string, 0, len(env)+2)
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "ZDOTDIR":
				out = append(out, ZDOTDIREnv+"="+v)
			case ZDOTDIREnv:
				// One from outside would put a ZDOTDIR back that the user
				// never had.
			default:
				out = append(out, kv)
			}
		}
		return nil, append(out, "ZDOTDIR="+filepath.Join(dir, "zsh")), true
	}
	return nil, nil, false
}

var (
	installMu sync.Mutex
	// installed is the folder the files were checked in by this process,
	// "" before the first check.
	installed string
)

// version is the hash of every script, which names the folder they go in.
var version = func() string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00", f.rel, len(f.data))
		h.Write(f.data)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}()

// Root is the folder that holds a folder of scripts per version.
func Root() string {
	return filepath.Join(xdg.StateHome, "tuios", "shell-integration")
}

// Dir returns the folder that holds this build's scripts, and writes them the
// first time a process asks or when they are gone. A file that is there with
// other content, from a crash part way or a hand edit, is written again.
// After the first check a call costs one stat.
func Dir() (string, error) {
	installMu.Lock()
	defer installMu.Unlock()
	dir := filepath.Join(Root(), version)
	if installed == dir {
		if _, err := os.Stat(filepath.Join(dir, files[len(files)-1].rel)); err == nil {
			return dir, nil
		}
	}
	if err := install(dir); err != nil {
		installed = ""
		return "", err
	}
	installed = dir
	return dir, nil
}

// install writes every file that is not there with the right content. A file
// is written to a temporary name and renamed, so a shell never reads half of
// one.
func install(dir string) error {
	for _, f := range files {
		path := filepath.Join(dir, f.rel)
		if got, err := os.ReadFile(path); err == nil && bytes.Equal(got, f.data) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return errfmt.Errorf("failed to make the shell integration folder: %w", err)
		}
		if err := atomicfile.Write(path, f.data, 0o600, false); err != nil {
			return errfmt.Errorf("failed to write the shell integration: %w", err)
		}
	}
	return nil
}

// Login makes cmd, which starts a shell with what Setup gave it or as it is,
// start a login shell, for [shell] login (issue #1768), so the shell reads
// the user's login files (.zprofile, .bash_profile, .profile) as a terminal
// on macOS does.
//
//   - zsh, fish and nushell take -l. A login zsh reads .zprofile and .zlogin
//     from the ZDOTDIR the shim gives back.
//   - bash with tuios's --rcfile keeps it: a login bash reads no rc file at
//     all, so it would lose the marks. The rc file reads the login files in
//     place of ~/.bashrc when BashLoginEnv is set, as VS Code does. A bash
//     without the rc file takes -l.
//   - Any other shell gets a leading - in argv[0], the login convention
//     every Unix shell reads: sh, dash, ksh, tcsh.
//
// On Windows a shell has no login mode, and cmd is left as it is.
func Login(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" || len(cmd.Args) == 0 {
		return
	}
	name := strings.TrimPrefix(filepath.Base(cmd.Path), "-")
	switch name {
	case "zsh", "fish", "nu":
		cmd.Args = slices.Insert(cmd.Args, 1, "-l")
	case "bash":
		if slices.Contains(cmd.Args[1:], "--rcfile") {
			cmd.Env = append(slices.Clone(cmd.Env), BashLoginEnv+"=1")
			return
		}
		cmd.Args = slices.Insert(cmd.Args, 1, "-l")
	default:
		cmd.Args[0] = "-" + name
	}
}
