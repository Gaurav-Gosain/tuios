package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
// The daemon runs what it read, so what it reads is held to a narrow shape:
//
//   - The program is never taken from the process. The daemon looks ssh or
//     mosh up on its own PATH. A ./ssh in a project folder, or any binary that
//     calls itself ssh, is not run.
//   - A line is followed only when every -o keyword is in sshAllowConfig, a
//     list of connection options that neither run nor load a program, and
//     no option or value holds a control character. -F, -I, and mosh's
//     --ssh, --client and --server are refused for the same reason.
//   - A line with an option that is not a session (-G, -V, -Q, -O) is not
//     followed, and neither is a line with an option this file does not know.
//   - The client must have been started by the pane's shell, through shells
//     and known wrappers only. ssh run by scp, rsync, git or sftp is a
//     transfer, not a login, and is not followed.
//   - Only a process owned by the user the daemon runs as is read.
//
// What is kept and what is dropped from a line that is followed:
//
//   - The destination and the options that say how to reach it are kept: the
//     port, the identity, the jump host, the login name, the -o options.
//   - The remote command is dropped. The new pane is for a shell.
//   - Options that make the client something other than an interactive session
//     are dropped: -N, -f, -n, -T, -t, -s, -M, and the same things spelt as -o
//     options (RemoteCommand, SessionType, ControlMaster, and so on).
//   - Port forwards (-L, -R, -D, -W, -w) are dropped. The first client already
//     holds those ports, and a second bind fails.
//
// The argv is never given to a shell on this machine. It is exec'd as it is,
// the way every pane command is.
//
// When the remote shell reported its folder with OSC 7 and the host in the
// report is the destination's host, the new pane starts there. The remote
// login shell can be anything (csh, fish, nushell), so the command it is given
// is only `exec sh -c '...'`, and the cd runs in sh. A cd that fails (the
// folder is gone) leaves the person in their home folder rather than ending
// the connection.

// sshFollowWalkLimit and sshFollowWalkDepth bound the walk of the foreground
// group. A wrapper (sshpass, a shell running ssh, autossh) puts the client a
// few levels down at most.
const (
	sshFollowWalkLimit = 32
	sshFollowWalkDepth = 6
)

// sshAncestryLimit bounds the walk up from a candidate to the pane's shell.
const sshAncestryLimit = 64

// lookPath finds a client program on the daemon's own PATH. A variable so the
// parser tests can name a fixed path.
var lookPath = exec.LookPath

// SSHFollowArgv returns the argv that opens a new session to where the ssh or
// mosh client running under shellPID is connected, the environment entries
// the new pane needs on top of an ordinary pane's, and whether there is a
// client to follow.
//
// reportHost and reportDir are the host and folder of one OSC 7 report the
// pane's terminal saw from another machine, both empty when there was none.
// The folder is used only when the host is the client's destination host.
//
// The environment is an ordinary pane's: the daemon's, which is what a split
// of the pane gets before its shell starts. The one thing a shell commonly
// adds that ssh needs is the agent socket (keychain, gpg-agent, an
// ssh-agent started in the pane), so SSH_AUTH_SOCK is taken from the client
// being followed when it names a socket this user owns.
func SSHFollowArgv(shellPID int, reportHost, reportDir string) (argv, env []string, ok bool) {
	login, ok := findRemoteLogin(shellPID)
	if !ok {
		return nil, nil, false
	}
	dir := ""
	if reportHost != "" && login.reportMatches(reportHost) {
		dir = reportDir
	}
	if sock, ok := readEnvVarOf(login.pid, "SSH_AUTH_SOCK"); ok && ownedSocket(sock) {
		env = []string{"SSH_AUTH_SOCK=" + sock}
	}
	return login.argv(dir), env, true
}

// ownedSocket reports whether path is an absolute path to a Unix socket, not
// a link, owned by the user the daemon runs as, in a folder that user owns
// and no one else may write to. Another user could otherwise put a socket of
// their own where the name points.
func ownedSocket(path string) bool {
	if !filepath.IsAbs(path) || hasControl(path) {
		return false
	}
	me := os.Geteuid()
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		return false
	}
	if uid, ok := fileOwner(fi); !ok || uid != me {
		return false
	}
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0o022 != 0 {
		return false
	}
	uid, ok := fileOwner(dir)
	return ok && uid == me
}

// findRemoteLogin finds the ssh or mosh client in the foreground of the pane
// whose shell is shellPID: the group leader first, then the members under it.
// The first client found decides: when it is not one to follow, nothing is.
func findRemoteLogin(shellPID int) (remoteLogin, bool) {
	if shellPID <= 0 {
		return remoteLogin{}, false
	}
	uid := os.Geteuid()
	leader, ok := foregroundPGID(shellPID)
	if !ok || leader <= 0 {
		return remoteLogin{}, false
	}
	// found says a client was seen, followed or not.
	found := false
	try := func(pid int) (remoteLogin, bool) {
		argv := readArgvExact(pid)
		if remoteClientName(argv) == "" {
			return remoteLogin{}, false
		}
		found = true
		if !startedByShell(pid, shellPID, uid) {
			return remoteLogin{}, false
		}
		login, ok := parseRemoteLogin(argv)
		login.pid = pid
		return login, ok
	}
	if login, ok := try(leader); ok || found {
		return login, ok
	}
	walk := foregroundGroup(leader, sshFollowWalkLimit, sshFollowWalkDepth)
	if walk == nil {
		return remoteLogin{}, false
	}
	var login remoteLogin
	var hit bool
	walk(func(info foregroundInfo) bool {
		login, hit = try(info.pid)
		return !hit && !found
	})
	return login, hit
}

// startedByShell reports whether pid is the pane's shell or was started by it
// through shells and known wrappers only, with every process on the way owned
// by uid.
func startedByShell(pid, shellPID, uid int) bool {
	for i := range sshAncestryLimit {
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
		if i > 0 && !sshLaunchers[programName(readArgvExact(pid))] {
			return false
		}
		pid = ppid
	}
	return false
}

// sshLaunchers are the programs that may stand between the pane's shell and
// the client. Anything else that runs ssh (scp, rsync, git, sftp) runs it for
// a transfer.
var sshLaunchers = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true,
	"mksh": true, "fish": true, "tcsh": true, "csh": true,
	"sshpass": true, "autossh": true, "mosh": true,
}

// scriptInterpreters are the programs a client script can run under. mosh is a
// perl script, and a wrapper named ssh can be a shell script.
var scriptInterpreters = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true,
	"perl": true, "python": true, "python3": true,
}

// programName is the name of what a process runs: its argv[0], or for a
// script run by an interpreter, the script. An interpreter running code given
// on its command line (-c, -e, -m) is the interpreter.
func programName(argv []string) string {
	_, name, _ := splitScript(argv)
	return name
}

// splitScript returns the program's arguments after its name, its name, and
// whether it is a script run by an interpreter.
func splitScript(argv []string) (args []string, name string, script bool) {
	if len(argv) == 0 {
		return nil, "", false
	}
	name = strings.TrimPrefix(filepath.Base(argv[0]), "-")
	args = argv[1:]
	if !scriptInterpreters[name] {
		return args, name, false
	}
	// The kernel runs a script as: interpreter, its own options, the
	// script's path, the script's arguments.
	i := 0
	for i < len(args) && i < 2 && strings.HasPrefix(args[i], "-") {
		if strings.ContainsAny(strings.TrimLeft(args[i], "-"), "cem") {
			return args, name, false
		}
		i++
	}
	if i >= len(args) {
		return args, name, false
	}
	return args[i+1:], filepath.Base(args[i]), true
}

// remoteClientName is "ssh", "mosh" or "mosh-client" when argv runs one, and
// "" otherwise.
func remoteClientName(argv []string) string {
	switch name := programName(argv); name {
	case "ssh", "mosh", "mosh-client":
		return name
	}
	return ""
}

// remoteLogin is a parsed ssh or mosh command line.
type remoteLogin struct {
	// bin is the program to run, found on the daemon's PATH.
	bin string
	// mosh says the client is mosh, which takes its remote command after --
	// and execs it rather than handing it to a shell.
	mosh bool
	// opts are the kept options, in their original order.
	opts []string
	// dest is the destination as it was given.
	dest string
	// pid is the client process the line was read from.
	pid int
}

// argv builds the command line for the new pane. dir, when not empty, is the
// remote folder to start in.
func (l remoteLogin) argv(dir string) []string {
	out := append([]string{l.bin}, l.opts...)
	if !l.mosh {
		// The new pane neither becomes a control master nor shares one: the
		// line's control socket is dropped, and one from ~/.ssh/config is
		// not made by a pane the person did not start.
		out = append(out, "-o", "ControlMaster=no")
	}
	dir = safeRemoteDir(dir)
	script := ""
	if dir != "" {
		script = `cd "` + dir + `" 2>/dev/null; exec "$SHELL" -l`
	}
	if script != "" && !l.mosh {
		// A remote command makes ssh skip the terminal unless asked for one,
		// and a RemoteCommand in ~/.ssh/config would refuse to run beside it.
		out = append(out, "-t", "-o", "RemoteCommand=none")
	}
	if strings.HasPrefix(l.dest, "-") {
		out = append(out, "--")
	}
	out = append(out, l.dest)
	if script == "" {
		return out
	}
	if l.mosh {
		// mosh-server execs the command itself, so the shell is named.
		return append(out, "--", "sh", "-c", script)
	}
	// ssh hands the remote command to the remote user's login shell, which
	// only has to run sh with one single-quoted word.
	return append(out, "exec sh -c '"+script+"'")
}

// hostMatches reports whether the host an OSC 7 report named is the
// destination's host. The whole name must match: a report from a further hop
// that shares a first label must not move the new pane.
func (l remoteLogin) hostMatches(reported string) bool {
	host := destHost(l.dest)
	reported = strings.ToLower(strings.TrimSpace(reported))
	return host != "" && host == reported
}

// reportMatches is hostMatches, and then a match against the host name ssh
// resolves the destination to. A shell reports its machine's own name, and a
// destination is often an alias in ~/.ssh/config: "prod" whose HostName is
// box.example.com. The whole resolved name matches, and a report with no dot
// may match its first label, because that is what most machines call
// themselves.
func (l remoteLogin) reportMatches(reported string) bool {
	if l.hostMatches(reported) {
		return true
	}
	if l.mosh {
		return false
	}
	reported = strings.ToLower(strings.TrimSpace(reported))
	resolved := strings.ToLower(resolveSSHHostName(l))
	if reported == "" || resolved == "" {
		return false
	}
	if reported == resolved {
		return true
	}
	// An address has no first label to compare.
	if strings.Trim(resolved, "0123456789.") == "" || strings.Contains(resolved, ":") {
		return false
	}
	first, _, dotted := strings.Cut(resolved, ".")
	return !strings.Contains(reported, ".") && dotted && reported == first
}

// sshResolveTimeout bounds ssh -G, which reads config files and nothing else.
const sshResolveTimeout = 2 * time.Second

// resolveSSHHostName is the HostName ssh -G gives for the line, run with the
// line's kept options only, or "" when it cannot say. A variable so the tests
// can stand in for ssh.
var resolveSSHHostName = func(l remoteLogin) string {
	ctx, cancel := context.WithTimeout(context.Background(), sshResolveTimeout)
	defer cancel()
	args := append([]string{"-G"}, l.opts...)
	if strings.HasPrefix(l.dest, "-") {
		args = append(args, "--")
	}
	cmd := exec.CommandContext(ctx, l.bin, append(args, l.dest)...)
	cmd.Stdin = nil
	// A Match exec in ~/.ssh/config runs a child that can outlive ssh and
	// hold its output open. ssh gets a process group of its own, the whole
	// group is killed at the timeout, and the wait for the pipes is bounded.
	killGroupOnCancel(cmd)
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(out)) {
		if v, ok := strings.CutPrefix(line, "hostname "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
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

// safeRemoteDir returns dir when it is an absolute path that can sit inside
// the double quotes of the cd, inside the single quotes around the sh script,
// in any login shell. It returns "" otherwise, and the new pane then starts in
// the home folder. The folder came from bytes the remote shell printed.
func safeRemoteDir(dir string) string {
	if !strings.HasPrefix(dir, "/") || len(dir) > 4096 {
		return ""
	}
	for _, r := range dir {
		if r < 0x20 || r == 0x7f || strings.ContainsRune("'\"$`\\!", r) {
			return ""
		}
	}
	return dir
}

// parseRemoteLogin reads an ssh or mosh client's command line.
func parseRemoteLogin(argv []string) (remoteLogin, bool) {
	args, name, _ := splitScript(argv)
	var l remoteLogin
	var ok bool
	switch name {
	case "ssh":
		l, ok = parseSSHArgs(args)
	case "mosh":
		l, ok = parseMoshArgs(args, false)
	case "mosh-client":
		l, ok = parseMoshClientArgs(args)
		name = "mosh"
	}
	if !ok {
		return remoteLogin{}, false
	}
	bin, err := lookPath(name)
	if err != nil {
		return remoteLogin{}, false
	}
	l.bin = bin
	return l, true
}

// sshValueOpts are the ssh options that take a value, and sshFlagOpts the ones
// that do not, from ssh(1). An option outside both makes the line unreadable,
// and the pane is then not followed: a guess could replay the wrong thing.
const (
	sshValueOpts = "BbcDEeFIiJLlmOoPpQRSWw"
	sshFlagOpts  = "46AaCfGgKkMNnqsTtVvXxYy"
	// sshDropValue and sshDropFlag are the ones the new pane does not get:
	// forwards, sessions that are not a shell, the control socket (-S), and
	// agent and X11 forwarding (-A, -X, -Y). The user's ~/.ssh/config still
	// applies the last two where the user chose them.
	sshDropValue = "DLRSWw"
	sshDropFlag  = "AfMNnsTtXY"
	// sshRefuse are the ones that make the line not followed: -F and -I can
	// run code on this machine, -E appends to a file the line names, and -G,
	// -V, -Q and -O are not a session.
	sshRefuse = "EFIGVQO"
)

// sshAllowConfig are the -o keywords a followed line may carry, lower case,
// and whether the new pane keeps the option. A keyword outside this table
// makes the line not followed. A refusal list missed ProxyCommand split by a
// newline and XAuthLocation, so the table names what is safe instead: each
// keyword was checked against ssh_config(5) to neither run nor load a program
// named by its value.
//
// The kept ones are what it takes to reach the same host as the same user.
// The new pane drops the rest without refusing the line: forwards, which the
// first client holds; sessions that are not a shell; and the settings that
// choose files, sockets, environment or forwarding (known hosts files, the
// control path, the agent, X11). The user's ~/.ssh/config still applies those
// where the user chose them, and a line read from a process does not get to.
var sshAllowConfig = map[string]bool{
	"port": true, "user": true, "hostname": true,
	"identityfile": true, "identitiesonly": true, "certificatefile": true,
	"serveraliveinterval": true, "serveralivecountmax": true,
	"connecttimeout": true, "connectionattempts": true, "tcpkeepalive": true,
	"stricthostkeychecking": true, "checkhostip": true,
	"compression": true, "addressfamily": true, "batchmode": true,
	"loglevel": true, "escapechar": true, "proxyjump": true,
	"preferredauthentications": true, "pubkeyauthentication": true,
	"passwordauthentication": true, "kbdinteractiveauthentication": true,
	"ciphers": true, "macs": true, "kexalgorithms": true,
	"hostkeyalgorithms": true, "pubkeyacceptedalgorithms": true,

	"remotecommand": false, "sessiontype": false, "stdinnull": false,
	"forkafterauthentication": false, "requesttty": false,
	"localforward": false, "remoteforward": false, "dynamicforward": false,
	"gatewayports": false, "exitonforwardfailure": false,
	"clearallforwardings": false, "tunnel": false, "tunneldevice": false,
	"controlmaster": false, "controlpersist": false, "controlpath": false,
	"userknownhostsfile": false, "globalknownhostsfile": false,
	"hostkeyalias": false, "identityagent": false,
	"sendenv": false, "setenv": false,
	"forwardagent": false, "forwardx11": false, "forwardx11trusted": false,
}

// parseSSHArgs reads ssh's arguments the way ssh does: options, the
// destination, more options, then the remote command, with -- ending the
// options at either place.
func parseSSHArgs(args []string) (remoteLogin, bool) {
	var l remoteLogin
	terminated := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if l.dest != "" && (terminated || a == "--" || !strings.HasPrefix(a, "-")) {
			// The remote command starts here, and it is dropped.
			break
		}
		if hasControl(a) || (i+1 < len(args) && hasControl(args[i+1]) && takesValue(a)) {
			return remoteLogin{}, false
		}
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
				if strings.IndexByte(sshRefuse, c) >= 0 {
					return remoteLogin{}, false
				}
				if strings.IndexByte(sshValueOpts, c) >= 0 {
					val := a[j+1:]
					if j+1 == len(a) {
						if i+1 >= len(args) {
							return remoteLogin{}, false
						}
						i++
						val = args[i]
					}
					keep, refuse := sshValueVerdict(c, val)
					if refuse {
						return remoteLogin{}, false
					}
					if keep {
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
		if a == "" {
			return remoteLogin{}, false
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

// sshValueVerdict says whether an option with a value goes to the new pane,
// and whether it makes the line not followed.
func sshValueVerdict(c byte, val string) (keep, refuse bool) {
	if strings.IndexByte(sshDropValue, c) >= 0 {
		return false, false
	}
	if c == 'J' {
		return true, !validJumpHosts(val)
	}
	if c != 'o' {
		return true, false
	}
	key := sshConfigKeyword(val)
	keep, known := sshAllowConfig[key]
	if !known {
		return false, true
	}
	if key == "proxyjump" && !validJumpHosts(sshConfigValue(val)) {
		return false, true
	}
	return keep, false
}

// sshConfigKeyword is the lower-case keyword of an -o value, read the way ssh
// reads a config line: leading space and a quote are skipped, and the keyword
// ends at =, space or a quote. A value with a control character never gets
// here: ssh splits a line at \r and \n too, so parseSSHArgs refuses it.
func sshConfigKeyword(val string) string {
	v := strings.TrimSpace(val)
	v = strings.TrimLeft(v, `"'`)
	if i := strings.IndexAny(v, "= \t\"'"); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(v)
}

// sshConfigValue is the value of an -o option: what follows the keyword and
// the space or = after it.
func sshConfigValue(val string) string {
	v := strings.TrimLeft(strings.TrimSpace(val), `"'`)
	if i := strings.IndexAny(v, "= \t\"'"); i >= 0 {
		v = v[i:]
	} else {
		return ""
	}
	v = strings.TrimLeft(v, `"' 	=`)
	return strings.TrimRight(v, `"'`)
}

// validJumpHosts reports whether a ProxyJump value is a comma-separated list
// of hops, each [user@]host[:port] or ssh://[user@]host[:port], spelt with
// letters, digits and ._:@[]- only and not starting with -. ssh builds a
// command line from the hops, so anything else is not followed.
func validJumpHosts(v string) bool {
	if v == "" {
		return false
	}
	for hop := range strings.SplitSeq(v, ",") {
		hop = strings.TrimPrefix(hop, "ssh://")
		if hop == "" || hop[0] == '-' {
			return false
		}
		for _, r := range hop {
			ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
				strings.ContainsRune("._:@[]-", r)
			if !ok {
				return false
			}
		}
	}
	return true
}

// moshValueOpts are the mosh options that take a value. moshFlagOpts are the
// ones that do not. moshRefuseOpts name a program mosh runs on this machine,
// so a line with one is not followed.
var (
	moshValueOpts = map[string]bool{
		"predict": true, "port": true, "p": true, "family": true,
		"bind-server": true, "experimental-remote-ip": true,
	}
	moshFlagOpts = map[string]bool{
		"a": true, "n": true, "4": true, "6": true, "o": true,
		"predict-overwrite": true, "no-predict-overwrite": true,
		"ssh-pty": true, "no-ssh-pty": true, "init": true, "no-init": true,
		"local": true,
	}
	moshRefuseOpts = map[string]bool{"ssh": true, "client": true, "server": true}
)

// parseMoshArgs reads mosh's arguments: options, the destination, then the
// remote command after --. strict refuses a line with anything after the
// destination, for a line rebuilt from mosh-client's, where the remote command
// and the destination cannot be told apart.
func parseMoshArgs(args []string, strict bool) (remoteLogin, bool) {
	l := remoteLogin{mosh: true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if l.dest != "" {
				if strict {
					return remoteLogin{}, false
				}
				break
			}
			if i+1 >= len(args) || args[i+1] == "" {
				return remoteLogin{}, false
			}
			l.dest = args[i+1]
			if strings.HasPrefix(l.dest, "-") {
				return remoteLogin{}, false
			}
			if strict && i+2 < len(args) {
				return remoteLogin{}, false
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
			case moshRefuseOpts[name]:
				return remoteLogin{}, false
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
			if strict {
				return remoteLogin{}, false
			}
			break
		}
		if a == "" {
			return remoteLogin{}, false
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
// joined by spaces. Arguments that held spaces cannot be split back apart, so
// the line must read back as options and exactly one destination, with no
// quotes, no remote command, and none of the options whose values could hold
// spaces.
func parseMoshClientArgs(args []string) (remoteLogin, bool) {
	if len(args) == 0 || !strings.HasPrefix(args[0], "-#") {
		return remoteLogin{}, false
	}
	line := strings.TrimSpace(strings.TrimPrefix(args[0], "-#"))
	line = strings.TrimSpace(strings.TrimSuffix(line, "|"))
	if line == "" || strings.ContainsAny(line, `"'\`) {
		return remoteLogin{}, false
	}
	fields := strings.Fields(line)
	for _, f := range fields {
		name, _, _ := strings.Cut(strings.TrimLeft(f, "-"), "=")
		if strings.HasPrefix(f, "-") && (moshRefuseOpts[name] || name == "predict") {
			return remoteLogin{}, false
		}
	}
	return parseMoshArgs(fields, true)
}

// redactSSHArgv is argv with the value of every -o option replaced, for a log
// line. A -o value can carry a password or a token for a proxy.
func redactSSHArgv(argv []string) []string {
	out := make([]string, len(argv))
	copy(out, argv)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "-o" {
			if key, _, ok := strings.Cut(out[i+1], "="); ok {
				out[i+1] = key + "=..."
			} else {
				out[i+1] = "..."
			}
			i++
		}
	}
	return out
}

// sshFollowArgv is SSHFollowArgv for one of the session's windows, named by
// id or name. It is false for a window on another machine: its processes are
// not on this one to read.
func (s *Session) sshFollowArgv(window string) (argv, env []string, ok bool) {
	state := s.GetState()
	idx, err := findWindowStateIndex(state.Windows, window)
	if err != nil {
		return nil, nil, false
	}
	pty := s.GetPTY(state.Windows[idx].PTYID)
	if pty == nil || pty.IsExited() {
		return nil, nil, false
	}
	if _, remote := pty.pty.(*remotePane); remote {
		return nil, nil, false
	}
	host, dir := pty.place.ElsewhereReport()
	return SSHFollowArgv(pty.ShellPID(), host, dir)
}

// hasControl reports whether s holds a control character. ssh reads an -o
// value as a config line, and a line break in it starts a second line.
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// takesValue reports whether an ssh argument is an option cluster that ends in
// an option with its value in the next argument.
func takesValue(a string) bool {
	if len(a) < 2 || a[0] != '-' || a == "--" {
		return false
	}
	for j := 1; j < len(a); j++ {
		if strings.IndexByte(sshValueOpts, a[j]) >= 0 {
			return j+1 == len(a)
		}
	}
	return false
}
