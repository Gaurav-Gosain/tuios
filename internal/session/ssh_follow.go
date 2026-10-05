package session

import (
	"os"
	"path/filepath"
	"strings"
)

// Following a pane into ssh.
//
// A person ssh'd into a machine who splits the pane usually wants the new pane
// on that machine too (discussion #468, and tmux-ssh-split before it). The
// daemon can see what the pane runs: the foreground process group of the
// pane's terminal, and the members of that group under it. When one of them is
// an ssh or mosh client, its argument vector says where it connected and how,
// and the new pane runs the same client with the same destination and options.
//
// What is kept and what is dropped:
//
//   - The destination and the options that say how to reach it are kept: the
//     port, the identity, the jump host, the login name, the config file, the
//     -o options, and so on.
//   - The remote command is dropped. The new pane is for a shell.
//   - Options that make the client something other than an interactive session
//     are dropped: -N, -f, -n, -T, -W, -s, -O, -M, -G, -V, -Q, and the same
//     things spelt as -o options (RemoteCommand, SessionType, and so on).
//   - Port forwards (-L, -R, -D, -w) are dropped. The first client already
//     holds those ports, and a second bind fails.
//
// The argv is never given to a shell on this machine. It is exec'd as it is,
// the way every pane command is. Only a process in the pane's own process tree,
// owned by the user the daemon runs as, is read: a process of another user in
// the same group is not something this user started, and its arguments are not
// this user's to replay.
//
// When the remote shell reported its folder with OSC 7 and the host in the
// report matches the destination, the new pane starts there. The cd runs in
// the remote login shell, so the folder is quoted for a POSIX shell, and a cd
// that fails (the folder is gone, or the report came from a further hop) leaves
// the person in their home folder rather than ending the connection.

// sshFollowWalkLimit and sshFollowWalkDepth bound the walk of the foreground
// group. A wrapper (sshpass, a shell running ssh, autossh) puts the client a
// few levels down at most.
const (
	sshFollowWalkLimit = 32
	sshFollowWalkDepth = 6
)

// sshAncestryLimit bounds the walk up from a candidate to the pane's shell.
const sshAncestryLimit = 64

// SSHFollowArgv returns the argv that opens a new session to where the ssh or
// mosh client running under shellPID is connected, and whether there is one.
//
// reportHost and reportDir are the host and folder of the last OSC 7 report the
// pane's terminal saw from another machine, both empty when there was none.
// The folder is used only when the host matches the client's destination.
func SSHFollowArgv(shellPID int, reportHost, reportDir string) ([]string, bool) {
	login, ok := findRemoteLogin(shellPID)
	if !ok {
		return nil, false
	}
	dir := ""
	if reportHost != "" && login.hostMatches(reportHost) {
		dir = reportDir
	}
	return login.argv(dir), true
}

// findRemoteLogin finds the ssh or mosh client in the foreground of the pane
// whose shell is shellPID: the group leader first, then the members under it.
func findRemoteLogin(shellPID int) (remoteLogin, bool) {
	if shellPID <= 0 {
		return remoteLogin{}, false
	}
	uid := os.Geteuid()
	leader, ok := foregroundPGID(shellPID)
	if !ok || leader <= 0 {
		return remoteLogin{}, false
	}
	try := func(info foregroundInfo) (remoteLogin, bool) {
		if !inPaneTree(info.pid, shellPID, uid) {
			return remoteLogin{}, false
		}
		return parseRemoteLogin(info.argv, info.exe)
	}
	lead := readProcessInfo(leader)
	lead.pid = leader
	if login, ok := try(lead); ok {
		return login, true
	}
	walk := foregroundGroup(leader, sshFollowWalkLimit, sshFollowWalkDepth)
	if walk == nil {
		return remoteLogin{}, false
	}
	var found remoteLogin
	var hit bool
	walk(func(info foregroundInfo) bool {
		found, hit = try(info)
		return !hit
	})
	return found, hit
}

// inPaneTree reports whether pid is the pane's shell or one of its
// descendants, and every process on the way is owned by uid.
func inPaneTree(pid, shellPID, uid int) bool {
	for range sshAncestryLimit {
		if pid <= 1 {
			return false
		}
		ppid, owner, ok := readParentAndOwner(pid)
		if !ok || owner != uid {
			return false
		}
		if pid == shellPID {
			return true
		}
		pid = ppid
	}
	return false
}

// remoteLogin is a parsed ssh or mosh command line.
type remoteLogin struct {
	// bin is the program to run.
	bin string
	// mosh says the client is mosh, which takes its remote command after --
	// and execs it rather than handing it to a shell.
	mosh bool
	// opts are the kept options, in their original order.
	opts []string
	// dest is the destination as it was given.
	dest string
}

// argv builds the command line for the new pane. dir, when not empty, is the
// remote folder to start in.
func (l remoteLogin) argv(dir string) []string {
	out := append([]string{l.bin}, l.opts...)
	dir = safeRemoteDir(dir)
	if dir != "" && !l.mosh {
		// A remote command makes ssh skip the terminal unless asked for one.
		out = append(out, "-t")
	}
	if strings.HasPrefix(l.dest, "-") {
		out = append(out, "--")
	}
	out = append(out, l.dest)
	if dir == "" {
		return out
	}
	script := "cd " + remoteShellQuote(dir) + ` 2>/dev/null; exec "$SHELL" -l`
	if l.mosh {
		// mosh-server execs the command itself, so the shell is named.
		return append(out, "--", "sh", "-c", script)
	}
	// ssh hands the remote command to the remote user's shell.
	return append(out, script)
}

// hostMatches reports whether the host an OSC 7 report named is the
// destination this client connected to. A report names the machine by its own
// hostname and a destination is often a longer or shorter form of it, so the
// first labels are compared when the whole names differ.
func (l remoteLogin) hostMatches(reported string) bool {
	host := destHost(l.dest)
	reported = strings.ToLower(strings.TrimSpace(reported))
	if host == "" || reported == "" {
		return false
	}
	if host == reported {
		return true
	}
	first := func(s string) string {
		if i := strings.IndexByte(s, '.'); i > 0 {
			return s[:i]
		}
		return s
	}
	// An address has no first label to compare.
	if isNumericHost(host) || isNumericHost(reported) {
		return false
	}
	return first(host) == first(reported)
}

// destHost is the host part of an ssh destination, lower case: the user, the
// scheme, the port and IPv6 brackets are removed.
func destHost(dest string) string {
	d := strings.TrimPrefix(dest, "ssh://")
	if i := strings.LastIndexByte(d, '@'); i >= 0 {
		d = d[i+1:]
	}
	if strings.HasPrefix(d, "[") {
		if j := strings.IndexByte(d, ']'); j > 0 {
			d = d[1:j]
		}
	} else if strings.Count(d, ":") == 1 {
		d = d[:strings.IndexByte(d, ':')]
	}
	return strings.ToLower(strings.TrimSuffix(d, "/"))
}

func isNumericHost(h string) bool {
	if strings.Contains(h, ":") {
		return true
	}
	return strings.Trim(h, "0123456789.") == ""
}

// safeRemoteDir returns dir when it is an absolute path with no control
// characters or backslashes, and "" otherwise. The folder came from bytes the remote shell
// printed, so it is checked before it goes into a command line.
func safeRemoteDir(dir string) string {
	if !strings.HasPrefix(dir, "/") || len(dir) > 4096 {
		return ""
	}
	for _, r := range dir {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return ""
		}
	}
	return dir
}

// remoteShellQuote quotes s for a POSIX shell, and for fish, as one word. s holds
// no backslash (see safeRemoteDir), the one character the two read
// differently inside single quotes.
func remoteShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// scriptInterpreters are the programs a client script can run under. mosh is a
// perl script, and a wrapper named ssh can be a shell script.
var scriptInterpreters = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true,
	"perl": true, "python": true, "python3": true,
}

// parseRemoteLogin reads an ssh or mosh client's command line. exe is the
// resolved executable, used as the program to run when it is the client
// itself, so the new pane runs the same binary whatever PATH the daemon has.
func parseRemoteLogin(argv []string, exe string) (remoteLogin, bool) {
	if len(argv) == 0 {
		return remoteLogin{}, false
	}
	bin := argv[0]
	args := argv[1:]
	name := filepath.Base(bin)
	if scriptInterpreters[name] {
		// The kernel runs a script as: interpreter, its own options, the
		// script's path, the script's arguments.
		i := 0
		for i < len(args) && i < 2 && strings.HasPrefix(args[i], "-") {
			// -c, -e and -m run code given on the command line, not a
			// script, so what follows is not a program path.
			if strings.ContainsAny(strings.TrimLeft(args[i], "-"), "cem") {
				return remoteLogin{}, false
			}
			i++
		}
		if i >= len(args) {
			return remoteLogin{}, false
		}
		bin, args, exe = args[i], args[i+1:], ""
		name = filepath.Base(bin)
	}
	switch name {
	case "ssh":
		if exe != "" && filepath.Base(exe) == "ssh" {
			bin = exe
		}
		return parseSSHArgs(bin, args)
	case "mosh":
		return parseMoshArgs(bin, args)
	case "mosh-client":
		return parseMoshClientArgs(exe, args)
	}
	return remoteLogin{}, false
}

// sshValueOpts are the ssh options that take a value, and sshFlagOpts the ones
// that do not, from ssh(1). An option outside both makes the line unreadable,
// and the pane is then not followed: a guess could replay the wrong thing.
const (
	sshValueOpts = "BbcDEeFIiJLlmOoPpQRSWw"
	sshFlagOpts  = "46AaCfGgKkMNnqsTtVvXxYy"
	// sshDropValue and sshDropFlag are the ones the new pane does not get.
	sshDropValue = "DLORQWw"
	sshDropFlag  = "fGMNnsTtV"
)

// sshDropConfig are the -o keywords the new pane does not get, lower case.
var sshDropConfig = map[string]bool{
	"remotecommand": true, "sessiontype": true, "stdinnull": true,
	"forkafterauthentication": true, "requesttty": true,
	"localforward": true, "remoteforward": true, "dynamicforward": true,
	"tunnel": true, "tunneldevice": true,
}

// parseSSHArgs reads ssh's arguments the way ssh does: options, the
// destination, more options, then the remote command, with -- ending the
// options at either place.
func parseSSHArgs(bin string, args []string) (remoteLogin, bool) {
	l := remoteLogin{bin: bin}
	terminated := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !terminated && a == "--" {
			if l.dest != "" {
				break
			}
			terminated = true
			continue
		}
		if !terminated && len(a) > 1 && a[0] == '-' {
			for j := 1; j < len(a); j++ {
				c := a[j]
				if strings.IndexByte(sshValueOpts, c) >= 0 {
					val := a[j+1:]
					if val == "" {
						if i+1 >= len(args) {
							return remoteLogin{}, false
						}
						i++
						val = args[i]
					}
					if keepSSHValue(c, val) {
						l.opts = append(l.opts, "-"+string(c), val)
					}
					break
				}
				if strings.IndexByte(sshFlagOpts, c) < 0 {
					return remoteLogin{}, false
				}
				if strings.IndexByte(sshDropFlag, c) < 0 {
					l.opts = append(l.opts, "-"+string(c))
				}
			}
			continue
		}
		if l.dest != "" {
			// The remote command starts here.
			break
		}
		l.dest = a
		if terminated {
			break
		}
	}
	if l.dest == "" {
		return remoteLogin{}, false
	}
	return l, true
}

// keepSSHValue says whether an option with a value goes to the new pane.
func keepSSHValue(c byte, val string) bool {
	if strings.IndexByte(sshDropValue, c) >= 0 {
		return false
	}
	if c == 'o' {
		key := val
		if i := strings.IndexAny(key, "= \t"); i >= 0 {
			key = key[:i]
		}
		return !sshDropConfig[strings.ToLower(key)]
	}
	return true
}

// moshValueOpts are the mosh options that take a value. moshFlagOpts are the
// ones that do not.
var (
	moshValueOpts = map[string]bool{
		"client": true, "server": true, "ssh": true, "predict": true,
		"port": true, "p": true, "family": true, "bind-server": true,
		"experimental-remote-ip": true,
	}
	moshFlagOpts = map[string]bool{
		"a": true, "n": true, "4": true, "6": true, "o": true,
		"predict-overwrite": true, "no-predict-overwrite": true,
		"ssh-pty": true, "no-ssh-pty": true, "init": true, "no-init": true,
		"local": true,
	}
)

// parseMoshArgs reads mosh's arguments: options, the destination, then the
// remote command after --.
func parseMoshArgs(bin string, args []string) (remoteLogin, bool) {
	l := remoteLogin{bin: bin, mosh: true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if l.dest != "" {
				break
			}
			if i+1 < len(args) {
				l.dest = args[i+1]
			}
			break
		}
		if len(a) > 1 && a[0] == '-' {
			name := strings.TrimLeft(a, "-")
			val, hasVal := "", false
			if k, v, ok := strings.Cut(name, "="); ok {
				name, val, hasVal = k, v, true
			}
			switch {
			case moshValueOpts[name]:
				if !hasVal {
					if i+1 >= len(args) {
						return remoteLogin{}, false
					}
					i++
					val = args[i]
				}
				l.opts = append(l.opts, "--"+longMoshName(name)+"="+val)
			case moshFlagOpts[name] && !hasVal:
				l.opts = append(l.opts, a)
			default:
				return remoteLogin{}, false
			}
			continue
		}
		if l.dest != "" {
			break
		}
		l.dest = a
	}
	if l.dest == "" {
		return remoteLogin{}, false
	}
	return l, true
}

// longMoshName spells a short value option the long way, so it can carry its
// value after =.
func longMoshName(name string) string {
	if name == "p" {
		return "port"
	}
	return name
}

// parseMoshClientArgs reads the line mosh leaves behind once it has connected:
// it execs mosh-client with "-# ARGS |", where ARGS are its own arguments
// joined by spaces. Arguments that held spaces cannot be split back apart, and
// a line with quotes in it is not followed.
func parseMoshClientArgs(exe string, args []string) (remoteLogin, bool) {
	if len(args) == 0 || !strings.HasPrefix(args[0], "-#") {
		return remoteLogin{}, false
	}
	line := strings.TrimSpace(strings.TrimPrefix(args[0], "-#"))
	line = strings.TrimSpace(strings.TrimSuffix(line, "|"))
	if line == "" || strings.ContainsAny(line, `"'\`) {
		return remoteLogin{}, false
	}
	bin := "mosh"
	if exe != "" {
		if cand := filepath.Join(filepath.Dir(exe), "mosh"); fileExists(cand) {
			bin = cand
		}
	}
	return parseMoshArgs(bin, strings.Fields(line))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sshFollowArgv is SSHFollowArgv for one of the session's windows, named by
// id or name. It is false for a window on another machine: its processes are
// not on this one to read.
func (s *Session) sshFollowArgv(window string) ([]string, bool) {
	state := s.GetState()
	idx, err := findWindowStateIndex(state.Windows, window)
	if err != nil {
		return nil, false
	}
	pty := s.GetPTY(state.Windows[idx].PTYID)
	if pty == nil || pty.IsExited() {
		return nil, false
	}
	if _, remote := pty.pty.(*remotePane); remote {
		return nil, false
	}
	host := pty.place.Elsewhere()
	dir := ""
	if host != "" {
		dir = pty.place.ElsewhereDir()
	}
	return SSHFollowArgv(pty.ShellPID(), host, dir)
}
