package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// tuios cp: copy files and folders between this machine and its hosts.
//
// The command is a client of the daemon's copies (transfer-start). It reads
// the paths with the host rule of -s HOST:SESSION, decides what the person
// wants done with files that are there, starts one copy per source, and
// follows them to their end (transfer_follow.go). The copies run in the
// daemon, so a closed terminal does not stop them, and Ctrl+C asks whether
// to keep them running or cancel them.

// cpOptions are the flags of tuios cp.
type cpOptions struct {
	move            bool
	recursive       bool
	conflict        string
	skip            bool
	force           bool
	keepBoth        bool
	contents        bool
	json            bool
	quiet           bool
	detach          bool
	noPerms         bool
	noTimes         bool
	noCompress      bool
	bwlimit         string
	timeout         time.Duration
	cancelOnTimeout bool
}

func newCpCommand() *cobra.Command {
	var o cpOptions
	cmd := &cobra.Command{
		Use:   "cp SRC... DST",
		Short: "Copy files and folders between this machine and its hosts",
		Long: `Copy files and folders between this machine and the machines in 'tuios hosts'.

A path is HOST:PATH for a path on a host, or a plain path for this machine.
The word before the first colon is a host when it is a host name or "local",
as in -s HOST:SESSION. Write a path with a colon in it on this machine as
./a:b, or local:a:b. A relative path on a host is under its home folder, so
build:logs is ~/logs on build. HOST: alone is the host's home folder.

The rules are the ones cp has:

  - A DST that is a folder, or ends in /, gets each SRC inside it.
  - A DST that does not exist, with one SRC, is the new name.
  - Several SRCs need DST to be a folder that exists.
  - A folder copies with all it holds. -r is accepted and changes nothing.
    --contents copies what is inside the folder instead of the folder.
  - A folder into a folder that exists merges into it, file by file.

A file that is there with the same bytes (sha256) is not copied again, so a
second run of the same copy is a quick check. A file that is there and
differs follows --conflict:

  ask        ask for each file (the default on a terminal)
  skip       leave the file there as it is (the default otherwise, -n)
  replace    replace it (-f)
  keep-both  keep it, and name the copy "name (from HOST).ext" (-b)

The copy runs in the daemon. It keeps permission bits and modification times,
checks every file with sha256 before it puts it in place, waits for a host
that goes away, and goes on from where it stopped. Ctrl+C asks whether to
cancel the copy or keep it running; a second Ctrl+C cancels it. Follow a copy
that keeps running with 'tuios transfers'.

Exit codes:

  0    every file was copied, or was there with the same bytes
  1    the copy failed (the message says why and what to do)
  2    the command line is wrong
  3    done, but some items were not copied: files skipped by the
       default --conflict, links, pipes and devices, or files made at the
       destination during the copy
  4    refused: the pane's grants or the host's link policy do not allow it
  5    a host was not reachable for --timeout
  6    a file did not match its original twice, or changed during the copy
  130  cancelled with Ctrl+C

In a pane, the copy carries the pane's name, and the pane's grants decide
whether it may copy: admin may copy anything, files only inside the pane's
folder and the hosts' home folders.`,
		Example: `  # A file to a host's home folder
  tuios cp ./build.tar build:

  # A folder from a host to here
  tuios cp build:~/logs ./

  # From one host to another
  tuios cp build:/var/log/app.log gpu:/tmp/

  # Move: remove the original after the check
  tuios cp -m ./dist build:~/www/

  # For a script: JSON events, and say what to do with files that differ
  tuios cp --json --conflict replace ./a.bin build:~/

  # Start the copy and return
  tuios cp --detach ./big.iso build:~/iso/`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCp(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.move, "move", "m", false, "Remove each original after its copy is checked")
	f.BoolVarP(&o.recursive, "recursive", "r", false, "Accepted for habit: a folder always copies with all it holds")
	f.StringVar(&o.conflict, "conflict", "", "What to do with a file that is there and differs: ask, skip, replace or keep-both")
	f.BoolVarP(&o.skip, "no-clobber", "n", false, "The same as --conflict skip")
	f.BoolVarP(&o.force, "force", "f", false, "The same as --conflict replace")
	f.BoolVarP(&o.keepBoth, "keep-both", "b", false, "The same as --conflict keep-both")
	f.BoolVar(&o.contents, "contents", false, "Copy what is inside a SRC folder, not the folder itself")
	f.BoolVar(&o.json, "json", false, "Print the copy's events as JSON lines on stdout")
	f.BoolVarP(&o.quiet, "quiet", "q", false, "Print only errors")
	f.BoolVar(&o.detach, "detach", false, "Start the copy, print its id, and return")
	f.BoolVar(&o.noPerms, "no-perms", false, "Do not copy permission bits")
	f.BoolVar(&o.noTimes, "no-times", false, "Do not copy modification times")
	f.BoolVar(&o.noCompress, "no-compress", false, "Do not compress the bytes on a link")
	f.StringVar(&o.bwlimit, "bwlimit", "", "At most this many bytes a second, such as 500K or 5M")
	f.DurationVar(&o.timeout, "timeout", 0, "Exit 5 when a host stays unreachable this long (the copy goes on)")
	f.BoolVar(&o.cancelOnTimeout, "cancel-on-timeout", false, "With --timeout, cancel the copy too")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &diagnosticError{What: err.Error(), Fix: "run 'tuios cp --help' to see the flags.", Status: exitCopyUsage}
	})
	return cmd
}

// usageError is a wrong command line, exit 2.
func usageError(what, fix string) error {
	return &diagnosticError{What: what, Fix: fix, Status: exitCopyUsage}
}

// cpPath is one path on the command line.
type cpPath struct {
	ep session.Endpoint
	// slash is a path written with a / at its end: a folder.
	slash bool
	arg   string
}

// configuredHostNames is the [hosts] names on this machine.
func configuredHostNames() []string {
	path, err := config.GetConfigPath()
	if err != nil {
		return nil
	}
	hosts, err := config.HostsInFile(path)
	if err != nil {
		return nil
	}
	var names []string
	for name, h := range hosts {
		if h.Addr != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// parseCpPath reads one path. A path on this machine is made absolute here,
// against this command's own folder, because the daemon has another.
func parseCpPath(arg string, hosts []string) (cpPath, error) {
	out := cpPath{arg: arg, slash: strings.HasSuffix(arg, "/") || (runtime.GOOS == "windows" && strings.HasSuffix(arg, `\`))}
	local := func(p string) (cpPath, error) {
		if p == "" {
			return cpPath{}, usageError(fmt.Sprintf("%q names no path on this machine.", arg), "write a path after local:, such as local:./notes.txt.")
		}
		if p == "~" || strings.HasPrefix(p, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return cpPath{}, err
			}
			p = filepath.Join(home, p[min(len(p), 2):])
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return cpPath{}, err
		}
		out.ep = session.Endpoint{Path: abs}
		return out, nil
	}
	if arg == "" {
		return cpPath{}, usageError("an empty path was given.", "name a file or a folder.")
	}
	if strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, ".") || strings.HasPrefix(arg, "~") {
		return local(arg)
	}
	if runtime.GOOS == "windows" && len(arg) >= 2 && arg[1] == ':' && (len(arg) == 2 || arg[2] == '\\' || arg[2] == '/') {
		return local(arg)
	}
	host, rest, ok := strings.Cut(arg, ":")
	if !ok || !federation.IsHostName(host) {
		return local(arg)
	}
	if host == federation.LocalHostName {
		return local(rest)
	}
	if !slices.Contains(hosts, host) {
		what := fmt.Sprintf("%s names the host %q, and no host has that name.", arg, host)
		fix := "run 'tuios hosts' to see the hosts, or write a path on this machine as ./" + arg + "."
		if near := closestName(host, hosts); near != "" {
			fix = fmt.Sprintf("did you mean %s:%s? Run 'tuios hosts' to see the hosts, or write a path on this machine as ./%s.", near, rest, arg)
		}
		return cpPath{}, &diagnosticError{What: what, Fix: fix, Status: exitCopyUsage, Err: &session.VerbCallError{Code: session.ErrVerbUnknownHost, Message: what}}
	}
	if strings.ContainsRune(rest, 0) {
		return cpPath{}, usageError("a path cannot hold a NUL byte.", "write the path again.")
	}
	switch {
	case rest == "" || rest == "~":
		rest = "~"
		out.slash = true
	case strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "~/"):
	default:
		// The scp rule: a relative path on a host is under its home.
		rest = "~/" + rest
	}
	out.ep = session.Endpoint{Host: host, Path: rest}
	return out, nil
}

// closestName is the name in names nearest to s by edit distance, when it is
// close enough to be a typo.
func closestName(s string, names []string) string {
	best, bestD := "", min(len(s)/3+1, 3)+1
	for _, n := range names {
		if d := editDistance(s, n); d < bestD {
			best, bestD = n, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// parseRate reads --bwlimit: a number of bytes a second, with K, M or G for
// 1024, 1024^2 and 1024^3, and an optional B or /s.
func parseRate(s string) (int64, error) {
	t := strings.TrimSpace(strings.ToUpper(s))
	t = strings.TrimSuffix(t, "/S")
	t = strings.TrimSuffix(t, "B")
	t = strings.TrimSuffix(t, "I")
	mult := 1.0
	if t != "" {
		switch t[len(t)-1] {
		case 'K':
			mult = 1 << 10
		case 'M':
			mult = 1 << 20
		case 'G':
			mult = 1 << 30
		}
		if mult > 1 {
			t = t[:len(t)-1]
		}
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%q is not a rate such as 500K or 5M", s)
	}
	return int64(v * mult), nil
}

// conflictPolicy is the policy the flags name, and whether one was named.
func (o cpOptions) conflictPolicy() (string, bool, error) {
	var named []string
	if o.conflict != "" {
		switch o.conflict {
		case "ask", "skip", "replace", "keep-both":
		default:
			return "", false, usageError(fmt.Sprintf("--conflict %s is not a policy.", o.conflict), "use ask, skip, replace or keep-both.")
		}
		named = append(named, o.conflict)
	}
	if o.skip {
		named = append(named, "skip")
	}
	if o.force {
		named = append(named, "replace")
	}
	if o.keepBoth {
		named = append(named, "keep-both")
	}
	slices.Sort(named)
	named = slices.Compact(named)
	if len(named) > 1 {
		return "", false, usageError("the flags name more than one conflict policy: "+strings.Join(named, ", ")+".", "give one of --conflict, -n, -f and -b.")
	}
	if len(named) == 1 {
		return named[0], true, nil
	}
	return "", false, nil
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func runCp(cmd *cobra.Command, args []string, o cpOptions) error {
	if len(args) < 2 {
		return usageError("tuios cp needs a source and a destination.", "run 'tuios cp SRC... DST', for example 'tuios cp ./notes.txt build:'.")
	}
	policy, explicit, err := o.conflictPolicy()
	if err != nil {
		return err
	}
	interactive := isTerminal(os.Stdin) && isTerminal(os.Stderr)
	if !explicit {
		policy = "skip"
		if interactive && !o.json {
			policy = "ask"
		}
	}
	if policy == "ask" && !interactive {
		return usageError("--conflict ask needs a terminal to ask on.", "pass --conflict skip, replace or keep-both.")
	}
	if o.cancelOnTimeout && o.timeout <= 0 {
		return usageError("--cancel-on-timeout needs --timeout.", "pass --timeout too, such as --timeout 10m.")
	}
	var rate int64
	if o.bwlimit != "" {
		if rate, err = parseRate(o.bwlimit); err != nil {
			return usageError(err.Error()+".", "write the rate as bytes a second, such as 500K or 5M.")
		}
	}
	hosts := configuredHostNames()
	paths := make([]cpPath, 0, len(args))
	for _, a := range args {
		p, err := parseCpPath(a, hosts)
		if err != nil {
			return err
		}
		paths = append(paths, p)
	}
	srcs, dst := paths[:len(paths)-1], paths[len(paths)-1]
	for _, s := range srcs {
		if s.ep.Host == "" {
			if _, err := os.Stat(s.ep.Path); err != nil {
				return &diagnosticError{What: fmt.Sprintf("%s does not exist on this machine.", s.arg), Fix: "check the path, or name a host as HOST:PATH.", Status: exitCopyFailed}
			}
		}
	}

	out := outTerminal
	switch {
	case o.json:
		out = outJSON
	case o.quiet:
		out = outQuiet
	case !isTerminal(os.Stderr):
		out = outPlain
	}

	if !session.IsDaemonRunning() {
		if o.json {
			if err := spawnDaemon(); err != nil {
				return err
			}
		} else if err := ensureDaemon(); err != nil {
			return err
		}
	}
	ctl, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = ctl.Close() }()

	fl := newFollower(ctl, out, explicit)
	if !o.detach {
		fl.subscribe()
	}
	if policy == "ask" {
		fl.ask = func(r copyRow) bool { return askConflicts(fl, r) }
	}

	place := "auto"
	if len(srcs) > 1 {
		place = "into"
	}
	if o.contents {
		place = "contents"
	}
	dstPath := dst.ep.Path
	if dst.slash && place == "auto" {
		place = "into"
	}
	code := 0
	var started []copyRow
	for _, s := range srcs {
		params := map[string]any{
			"src":      s.ep,
			"dst":      session.Endpoint{Host: dst.ep.Host, Path: dstPath},
			"move":     o.move,
			"conflict": "merge",
			"each":     policy,
			"place":    place,
		}
		if o.noPerms {
			params["perms"] = false
		}
		if o.noTimes {
			params["times"] = false
		}
		if o.noCompress {
			params["compress"] = false
		}
		if rate > 0 {
			params["rate_limit"] = rate
		}
		raw, err := ctl.Call("transfer-start", params)
		if err != nil {
			c := exitForCode(errCode(err))
			code = worseExit(code, c)
			if o.json {
				fl.emit(map[string]any{"event": "failed", "src": s.ep, "code": errCode(err), "message": err.Error(), "exit": c})
			} else {
				reportCommandError(explainCopyStart(s, dst, err))
			}
			continue
		}
		var r copyRow
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		started = append(started, r)
		fl.add(r)
	}
	if len(started) == 0 {
		return &statusError{code: code}
	}
	if o.detach {
		for _, r := range started {
			if o.json {
				continue
			}
			_, _ = fmt.Fprintf(os.Stdout, "%s\n", r.ID)
		}
		if !o.json && !o.quiet {
			_, _ = fmt.Fprintln(os.Stderr, "The copy runs in the daemon. Follow it with: tuios transfers wait "+started[0].ID)
		}
		return exitStatusError(code)
	}

	stop := make(chan struct{})
	result := make(chan int, 1)
	go func() {
		fl.run(stop)
		result <- fl.summary()
	}()

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	var timeoutC <-chan time.Time
	var timer *time.Ticker
	if o.timeout > 0 {
		timer = time.NewTicker(time.Second)
		defer timer.Stop()
		timeoutC = timer.C
	}
	waitingSince := map[string]time.Time{}
	for {
		select {
		case c := <-result:
			return exitStatusError(worseExit(code, c))
		case sig := <-sigs:
			if sig == syscall.SIGTERM || !interactive || o.json {
				cancelCopies(ctl, fl)
				continue
			}
			fl.paused.Store(true)
			fl.clear()
			if keepOrCancel(sigs) {
				_, _ = fmt.Fprintln(os.Stderr, "The copy goes on in the daemon. Follow it with: tuios transfers")
				return exitStatusError(code)
			}
			fl.paused.Store(false)
			cancelCopies(ctl, fl)
		case <-timeoutC:
			fl.mu.Lock()
			late := false
			for _, id := range fl.ids {
				r := fl.rows[id]
				if r.State != "waiting" {
					delete(waitingSince, id)
					continue
				}
				if waitingSince[id].IsZero() {
					waitingSince[id] = time.Now()
				}
				if time.Since(waitingSince[id]) >= o.timeout {
					late = true
				}
			}
			fl.mu.Unlock()
			if late {
				close(stop)
				<-result
				fl.draw(true)
				if o.cancelOnTimeout {
					for _, id := range fl.ids {
						_, _ = ctl.Call("transfer-cancel", map[string]any{"id": id})
					}
					_, _ = fmt.Fprintf(os.Stderr, "A host was not reachable for %v, so the copy was cancelled.\n", o.timeout)
				} else {
					_, _ = fmt.Fprintf(os.Stderr, "A host was not reachable for %v. The copy waits for it in the daemon. Follow it with: tuios transfers\n", o.timeout)
				}
				if o.json {
					fl.emit(map[string]any{"event": "timeout", "exit": exitCopyNoHost})
				}
				return &statusError{code: exitCopyNoHost}
			}
		}
	}
}

func exitStatusError(code int) error {
	if code == 0 {
		return nil
	}
	return &statusError{code: code}
}

// cancelCopies cancels every copy the command started. The follower then
// sees them end.
func cancelCopies(ctl *session.VerbClient, fl *follower) {
	fl.mu.Lock()
	ids := append([]string(nil), fl.ids...)
	fl.mu.Unlock()
	for _, id := range ids {
		_, _ = ctl.Call("transfer-cancel", map[string]any{"id": id})
	}
}

// readKey reads one key from the terminal, raw, so the person does not have
// to press Enter. A second Ctrl+C reads as c.
func readKey() byte {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		return b[0]
	}
	defer func() { _ = term.Restore(fd, old) }()
	var b [1]byte
	if _, err := os.Stdin.Read(b[:]); err != nil {
		return 'k'
	}
	if b[0] == 3 {
		return 'c'
	}
	return b[0]
}

// keepOrCancel asks the person what Ctrl+C means. It reports true for keep.
func keepOrCancel(sigs <-chan os.Signal) bool {
	_, _ = fmt.Fprint(os.Stderr, "Cancel the copy, or keep it running? [c/k] ")
	keys := make(chan byte, 1)
	go func() { keys <- readKey() }()
	for {
		select {
		case k := <-keys:
			_, _ = fmt.Fprintln(os.Stderr)
			switch k {
			case 'k', 'K':
				return true
			case 'c', 'C':
				return false
			}
			_, _ = fmt.Fprint(os.Stderr, "Press c to cancel the copy or k to keep it running. [c/k] ")
			go func() { keys <- readKey() }()
		case <-sigs:
			_, _ = fmt.Fprintln(os.Stderr)
			return false
		}
	}
}

// askConflicts asks the person about each file a copy waits on, and sends
// the answers. It reports whether it answered any.
func askConflicts(fl *follower, r copyRow) bool {
	answered := false
	fl.paused.Store(true)
	defer func() { fl.paused.Store(false) }()
	for _, c := range r.Conflicts {
		_, _ = fmt.Fprintf(os.Stderr, "%s is there on %s. %s\n", c.Path, machineName(r.Dst.Host), compareText(c))
		_, _ = fmt.Fprint(os.Stderr, "Replace it, keep both, or skip? [r/k/s, or R/K/S for all files] ")
		var choice string
		var all bool
		for choice == "" {
			k := readKey()
			switch k {
			case 'r', 'R':
				choice, all = "replace", k == 'R'
			case 'k', 'K':
				choice, all = "keep-both", k == 'K'
			case 's', 'S':
				choice, all = "skip", k == 'S'
			case 'c':
				// Ctrl+C: the copy is cancelled.
				_, _ = fmt.Fprintln(os.Stderr)
				_, _ = fl.ctl.Call("transfer-cancel", map[string]any{"id": r.ID})
				return true
			}
		}
		_, _ = fmt.Fprintln(os.Stderr, choice)
		params := map[string]any{"id": r.ID, "choice": choice}
		if all {
			params["all"] = true
		} else {
			params["rel"] = c.Rel
		}
		if _, err := fl.ctl.Call("transfer-answer", params); err != nil {
			reportCommandError(err)
			return answered
		}
		answered = true
		if all {
			break
		}
	}
	return answered
}

func machineName(host string) string {
	if host == "" {
		return "this machine"
	}
	return host
}

// compareText says how the file there differs from the new one.
func compareText(c session.ConflictItem) string {
	var parts []string
	switch {
	case c.SrcMTime > c.DstMTime && c.DstMTime > 0:
		parts = append(parts, "The new one is newer")
	case c.SrcMTime < c.DstMTime && c.SrcMTime > 0:
		parts = append(parts, "The one there is newer")
	}
	switch d := c.SrcSize - c.DstSize; {
	case d > 0:
		parts = append(parts, "the new one is "+humanBytes(d)+" larger")
	case d < 0:
		parts = append(parts, "the new one is "+humanBytes(-d)+" smaller")
	default:
		parts = append(parts, "they are the same size with other bytes")
	}
	s := strings.Join(parts, ", and ")
	return capitalize(s) + "."
}

// explainCopyStart says why a copy could not start.
func explainCopyStart(src, dst cpPath, err error) error {
	call, ok := errors.AsType[*session.VerbCallError](err)
	if !ok {
		return err
	}
	status := exitForCode(call.Code)
	switch call.Code {
	case session.ErrVerbForbidden:
		return &diagnosticError{
			What:   fmt.Sprintf("The copy of %s to %s was refused.", src.arg, dst.arg),
			Cause:  call.Message,
			Fix:    "this is the decision of the person who set the grants or the link policy. Ask them, or copy to a place that is allowed.",
			Status: status, Err: err,
		}
	case session.ErrVerbBusy:
		return &diagnosticError{
			What:   fmt.Sprintf("Another copy writes %s now.", dst.arg),
			Fix:    "wait for it with 'tuios transfers wait', or cancel it with 'tuios transfers cancel ID'.",
			Status: status, Err: err,
		}
	case session.ErrVerbUnknownHost:
		return &diagnosticError{What: call.Message, Fix: "run 'tuios hosts' to see the hosts.", Status: status, Err: err}
	}
	return &diagnosticError{What: fmt.Sprintf("The copy of %s to %s did not start: %s", src.arg, dst.arg, call.Message), Status: status, Err: err}
}
