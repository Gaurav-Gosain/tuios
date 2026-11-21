package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

const (
	sshFollowWalkLimit = 32
	sshFollowWalkDepth = 6
)

const sshAncestryLimit = 64

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
//
// why, when a client is there and is not followed, says why in a line for
// the person. It is empty when the pane runs no client.
func SSHFollowArgv(shellPID int, reportHost, reportDir string) (argv, env []string, why string, ok bool) {
	login, ok, _, why := findRemoteClient(shellPID)
	if !ok {
		return nil, nil, why, false
	}
	dir := ""
	if reportHost != "" && login.reportMatches(reportHost) {
		dir = reportDir
	}
	if sock, ok := readEnvVarOf(login.pid, "SSH_AUTH_SOCK"); ok {
		if resolved, owned := ownedSocket(sock); owned {
			env = []string{"SSH_AUTH_SOCK=" + resolved}
		}
	}
	return login.argv(dir), env, "", true
}

func ownedSocket(path string) (string, bool) {
	if !filepath.IsAbs(path) || hasControl(path) {
		return "", false
	}
	path = filepath.Clean(path)
	folder, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", false
	}
	resolved := filepath.Join(folder, filepath.Base(path))
	me := os.Geteuid()
	fi, err := os.Lstat(resolved)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		return "", false
	}
	if uid, ok := procinfo.FileOwner(fi); !ok || uid != me {
		return "", false
	}
	for dir := folder; ; dir = filepath.Dir(dir) {
		if !safeFolder(dir, me) {
			return "", false
		}
		if dir == filepath.Dir(dir) {
			return resolved, true
		}
	}
}

func stillOwnedSocket(path string) bool {
	_, ok := ownedSocket(path)
	return ok
}

func safeFolder(dir string, me int) bool {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	uid, ok := procinfo.FileOwner(fi)
	if !ok || (uid != me && uid != 0) {
		return false
	}
	return fi.Mode().Perm()&0o022 == 0 || fi.Mode()&os.ModeSticky != 0
}

func findRemoteClient(shellPID int) (login remoteLogin, ok, seen bool, why string) {
	if shellPID <= 0 {
		return remoteLogin{}, false, false, ""
	}
	leader, found := foregroundPGID(shellPID)
	if !found || leader <= 0 {
		return remoteLogin{}, false, false, ""
	}
	try := func(pid int) (remoteLogin, bool) {
		argv := readArgvExact(pid)
		if remoteClientName(argv) == "" {
			return remoteLogin{}, false
		}
		seen = true
		var l remoteLogin
		l, why = checkRemoteClient(pid, shellPID, argv)
		return l, why == ""
	}
	if login, ok = try(leader); ok || seen {
		return login, ok, seen, why
	}
	walk := foregroundGroup(leader, sshFollowWalkLimit, sshFollowWalkDepth)
	if walk == nil {
		return remoteLogin{}, false, false, ""
	}
	walk(func(info foregroundInfo) bool {
		login, ok = try(info.pid)
		return !ok && !seen
	})
	return login, ok, seen, why
}

const (
	sshWhyStart    = "tuios cannot read how the ssh in this pane started. The new pane runs a local shell."
	sshWhyTransfer = "The ssh in this pane is part of a file transfer. The new pane runs a local shell."
	sshWhyOptions  = "tuios does not copy an option of the ssh in this pane. The new pane runs a local shell."
	sshWhyCommand  = "The ssh in this pane runs a remote command. The new pane runs a local shell."
	sshWhyNoLogin  = "The ssh in this pane does not open a login shell. The new pane runs a local shell."
)

func checkRemoteClient(pid, shellPID int, argv []string) (remoteLogin, string) {
	wrapped, why := clientStart(pid, shellPID, os.Geteuid())
	if why != "" {
		return remoteLogin{}, why
	}
	login, ok := parseRemoteLogin(argv)
	if !ok {
		return remoteLogin{}, sshWhyOptions
	}
	if wrapped && login.command {
		return remoteLogin{}, sshWhyCommand
	}
	if wrapped && (login.notLogin || login.tunnel || login.forward) {
		return remoteLogin{}, sshWhyNoLogin
	}
	login.pid = pid
	return login, ""
}

func clientStart(pid, shellPID, uid int) (wrapped bool, why string) {
	for i := range sshAncestryLimit {
		if pid <= 1 {
			break
		}
		ppid, owner, ok := readParentAndOwner(pid)
		if !ok || owner != uid {
			break
		}
		if pid == shellPID {
			return wrapped, ""
		}
		if i > 0 {
			name := programName(readArgvExact(pid))
			if sshTransfers[name] || strings.HasPrefix(name, "git-") {
				return false, sshWhyTransfer
			}
			wrapped = wrapped || !sshLaunchers[name]
		}
		pid = ppid
	}
	return false, sshWhyStart
}

var sshLaunchers = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true,
	"mksh": true, "fish": true, "tcsh": true, "csh": true,
	"sshpass": true, "autossh": true, "mosh": true,
}

var sshTransfers = map[string]bool{
	"scp": true, "sftp": true, "rsync": true, "git": true, "sshfs": true,
}

var scriptInterpreters = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true,
	"perl": true, "python": true, "python3": true,
}

func programName(argv []string) string {
	_, name, _ := splitScript(argv)
	return name
}

func splitScript(argv []string) (args []string, name string, script bool) {
	if len(argv) == 0 {
		return nil, "", false
	}
	name = strings.TrimPrefix(filepath.Base(argv[0]), "-")
	args = argv[1:]
	if !scriptInterpreters[name] {
		return args, name, false
	}
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

func remoteClientName(argv []string) string {
	switch name := programName(argv); name {
	case "ssh", "mosh", "mosh-client":
		return name
	}
	return ""
}

type remoteLogin struct {
	bin      string
	mosh     bool
	opts     []string
	dest     string
	pid      int
	tunnel   bool
	tty      bool
	forward  bool
	command  bool
	notLogin bool
}

func (l remoteLogin) argv(dir string) []string {
	out := append([]string{l.bin}, l.opts...)
	if !l.mosh {
		out = append(out, "-o", "ControlMaster=no")
	}
	dir = safeRemoteDir(dir)
	script := ""
	if dir != "" {
		script = `cd "` + dir + `" 2>/dev/null; exec "$SHELL" -l`
	}
	if script != "" && !l.mosh {
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
		return append(out, "--", "sh", "-c", script)
	}
	return append(out, "exec sh -c '"+script+"'")
}

func (l remoteLogin) hostMatches(reported string) bool {
	host := destHost(l.dest)
	reported = strings.ToLower(strings.TrimSpace(reported))
	return host != "" && host == reported
}

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
	if strings.Trim(resolved, "0123456789.") == "" || strings.Contains(resolved, ":") {
		return false
	}
	first, _, dotted := strings.Cut(resolved, ".")
	return !strings.Contains(reported, ".") && dotted && reported == first
}

const sshResolveTimeout = 2 * time.Second

var resolveSSHHostName = func(l remoteLogin) string {
	ctx, cancel := context.WithTimeout(context.Background(), sshResolveTimeout)
	defer cancel()
	args := append([]string{"-G"}, l.opts...)
	if strings.HasPrefix(l.dest, "-") {
		args = append(args, "--")
	}
	cmd := childproc.CommandContext(ctx, l.bin, append(args, l.dest)...)
	cmd.Stdin = nil
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

func safeRemoteDir(dir string) string {
	if !strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, "//") || len(dir) > 4096 {
		return ""
	}
	for _, r := range dir {
		if r < 0x20 || r == 0x7f || strings.ContainsRune("'\"$`\\!", r) {
			return ""
		}
	}
	return dir
}

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

const (
	sshValueOpts = "BbcDEeFIiJLlmOoPpQRSWw"
	sshFlagOpts  = "46AaCfGgKkMNnqsTtVvXxYy"
	sshDropValue = "DLRSWw"
	sshDropFlag  = "AfMNnsTtXY"
	sshRefuse    = "EFIGVQO"
)

var sshTunnelConfig = map[string]string{
	"sessiontype": "none", "stdinnull": "yes", "forkafterauthentication": "yes",
}

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

func parseSSHArgs(args []string) (remoteLogin, bool) {
	var l remoteLogin
	terminated := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if l.dest != "" && (terminated || a == "--" || !strings.HasPrefix(a, "-")) {
			l.command = l.command || a != "--" || i+1 < len(args)
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
					if c == 'W' {
						l.forward = true
					}
					if c == 'o' {
						key, v := sshConfigKeyword(val), sshConfigValue(val)
						if want, ok := sshTunnelConfig[key]; ok && strings.EqualFold(v, want) {
							l.forward = true
						}
						l.command = l.command || key == "remotecommand" && !strings.EqualFold(v, "none")
						l.notLogin = l.notLogin || key == "sessiontype" && !strings.EqualFold(v, "default") ||
							key == "requesttty" && strings.EqualFold(v, "no")
					}
					if keep {
						l.opts = append(l.opts, "-"+string(c), val)
					}
					break
				}
				if strings.IndexByte(sshFlagOpts, c) < 0 {
					return remoteLogin{}, false
				}
				switch c {
				case 'N':
					l.tunnel = true
				case 's', 'T':
					l.notLogin = true
				case 'f', 'n':
					l.forward = true
				case 't':
					l.tty = true
				}
				if strings.IndexByte(sshDropFlag, c) < 0 {
					l.opts = append(l.opts, "-"+string(c))
				}
			}
			continue
		}
		if l.dest != "" {
			l.command = true
			break
		}
		if a == "" {
			return remoteLogin{}, false
		}
		l.dest = a
		if terminated {
			l.command = i+1 < len(args)
			break
		}
	}
	if l.dest == "" {
		return remoteLogin{}, false
	}
	l.tunnel = l.tunnel || (l.forward && !l.tty)
	return l, true
}

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

func sshConfigKeyword(val string) string {
	v := strings.TrimSpace(val)
	v = strings.TrimLeft(v, `"'`)
	if i := strings.IndexAny(v, "= \t\"'"); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(v)
}

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

func parseMoshArgs(args []string, strict bool) (remoteLogin, bool) {
	l := remoteLogin{mosh: true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if l.dest != "" {
				if strict {
					return remoteLogin{}, false
				}
				l.command = i+1 < len(args)
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
			l.command = i+2 < len(args)
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
			l.command = true
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

func longMoshName(name string) string {
	if name == "p" {
		return "port"
	}
	return name
}

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

func (s *Session) sshFollowArgv(window string) (argv, env []string, why string, ok bool) {
	state := s.GetState()
	idx, err := findWindowStateIndex(state.Windows, window)
	if err != nil {
		return nil, nil, "", false
	}
	pty := s.GetPTY(state.Windows[idx].PTYID)
	if pty == nil || pty.IsExited() {
		return nil, nil, "", false
	}
	if _, remote := pty.pty.(*remotePane); remote {
		return nil, nil, "", false
	}
	host, dir := pty.place.ElsewhereReport()
	return SSHFollowArgv(pty.ShellPID(), host, dir)
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

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
