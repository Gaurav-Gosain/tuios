package hostsetup

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"golang.org/x/crypto/ssh"
)

// Both ways: this machine added on the far machine, so a client there sees
// the sessions here. The far side tests its way back before anything is
// written there, because a NAT or a firewall often blocks that direction,
// and a far entry that cannot connect is worse than none.

// Here is this machine, as the far side needs it.
type Here struct {
	// User is the login here, and Name the name this machine gets there.
	User string `json:"user"`
	Name string `json:"name"`
	// Hostname is this machine's host name, and Tailnet its tailnet name.
	Hostname string `json:"hostname,omitempty"`
	Tailnet  string `json:"tailnet,omitempty"`
}

// ReadHere reads this machine for the both-ways step.
func ReadHere(ctx context.Context) *Here {
	h := &Here{}
	if u, err := user.Current(); err == nil {
		h.User = u.Username
	}
	h.Hostname, _ = os.Hostname()
	h.Name = pickName(h.Hostname, nil)
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ms, _ := federation.TailnetMachines(tctx, federation.TailnetOptions{})
	for _, m := range ms {
		if m.Self {
			h.Tailnet, h.Name = m.DNSName, pickName(m.Name, nil)
		}
	}
	return h
}

// Candidate is one address of this machine the far side tried.
type Candidate struct {
	Addr string `json:"addr"`
	// Source is tailnet, ssh_connection, hostname or flag.
	Source  string `json:"source"`
	OK      bool   `json:"ok"`
	Failure string `json:"failure,omitempty"`
	Said    string `json:"said,omitempty"`
}

// Reverse is what the both-ways step did.
type Reverse struct {
	// Name is this machine's name there, and Addr the address it uses.
	Name       string      `json:"name"`
	Addr       string      `json:"addr,omitempty"`
	Candidates []Candidate `json:"candidates"`
	// Added says the far side keeps the entry, and Status is its link.
	Added  bool   `json:"added"`
	Status string `json:"status,omitempty"`
	// WhyNot says why nothing was added there.
	WhyNot string `json:"why_not,omitempty"`
}

// candidates are this machine's addresses for the far side, in the order
// they are tried, each with the login here.
func (s *Setup) candidates(c Choices, here *Here) []Candidate {
	var out []Candidate
	add := func(addr, source string) {
		if addr == "" {
			return
		}
		if !strings.Contains(addr, "@") && here.User != "" {
			addr = here.User + "@" + addr
		}
		for _, c := range out {
			if c.Addr == addr {
				return
			}
		}
		if federation.SafeRemoteArg(addr) {
			out = append(out, Candidate{Addr: addr, Source: source})
		}
	}
	if c.Address != "" {
		add(c.Address, "flag")
		return out
	}
	if s.probe.Tailscale {
		add(here.Tailnet, "tailnet")
	}
	if ip := net.ParseIP(s.probe.From); ip != nil {
		add(ip.String(), "ssh_connection")
	}
	add(here.Hostname, "hostname")
	return out
}

// The far-side scripts, under the sync quoting rules. $1 is the far tuios
// for the hosts ones.
const (
	// reachBackScript runs ssh on the far side to $1 with BatchMode, and
	// prints its exit code and the last lines it wrote.
	reachBackScript = `o=$("${TUIOS_SSH:-ssh}" -o BatchMode=yes -o ConnectTimeout=5 -T "$1" true </dev/null 2>&1); echo "rc=$?"; echo "$o" | tail -n 3 | while IFS= read -r l; do echo "said=$l"; done`
	// readKeyScript prints the far side's first public key.
	readKeyScript = `for k in id_ed25519 id_ecdsa id_rsa; do if [ -f "$HOME/.ssh/$k.pub" ]; then head -n 1 "$HOME/.ssh/$k.pub"; exit 0; fi; done`
	// farAddScript adds host $2 at $3 there and prints its test, unless
	// host $2 is already there at another address ($4 is "check" then).
	farAddScript = `if [ "$4" = check ]; then "$1" hosts test "$2" --json </dev/null 2>/dev/null; exit 0; fi; "$1" hosts add "$2" "$3" --connect-timeout 5 </dev/null >/dev/null 2>&1; "$1" hosts test "$2" --json </dev/null 2>/dev/null`
	// farRemoveScript removes host $2 there.
	farRemoveScript = `"$1" hosts remove "$2" </dev/null >/dev/null 2>&1`
)

// reverse runs the both-ways step. A way back that does not work is
// reported in the result, not as an error: the link from here works.
func (s *Setup) reverse(ctx context.Context, c Choices, a Asker) (*Reverse, error) {
	here := s.opts.Here
	if here == nil {
		here = ReadHere(ctx)
	}
	bin := s.probe.Tuios
	rev := &Reverse{Name: here.Name, Candidates: s.candidates(c, here)}
	fail := func(why string) (*Reverse, error) {
		rev.WhyNot = why
		s.say(StepReverse, StateFailed, why)
		return rev, nil
	}
	var cmds []Command
	for _, cd := range rev.Candidates {
		cmds = append(cmds, Command{s.name, "ssh -o BatchMode=yes " + cd.Addr + " true"})
	}
	cmds = append(cmds, Command{s.name, "tuios hosts add " + here.Name + " ADDRESS"}, Command{s.name, "tuios hosts test " + here.Name})
	if c.DryRun {
		s.say(StepReverse, StatePlan, s.name+" tests its way back, then adds "+here.Name+".", cmds...)
		return rev, nil
	}
	if len(rev.Candidates) == 0 {
		return fail("tuios found no address of this machine to give " + s.name + ". Pass --address.")
	}
	if !federation.SafeRemoteArg(bin) || !federation.SafeRemoteArg(here.Name) {
		return fail("tuios cannot run the tuios on " + s.name + " by its path.")
	}

	s.say(StepReverse, StateRun, s.name+" tests its way back.")
	var tried []string
	for i := range rev.Candidates {
		cd := &rev.Candidates[i]
		ok, err := s.reachBack(ctx, a, cd)
		if err != nil {
			return rev, err
		}
		if ok {
			rev.Addr = cd.Addr
			break
		}
		tried = append(tried, cd.Addr[strings.LastIndexByte(cd.Addr, '@')+1:])
	}
	if rev.Addr == "" {
		return fail(s.name + " cannot reach this machine. It tried " + strings.Join(tried, ", ") + ". You can still use " + s.name + " from here, and its agents reach your Inbox. On one tailnet, both ways work.")
	}

	actx, cancel := context.WithTimeout(ctx, stepBudget)
	defer cancel()
	var r federation.HostReport
	if out, _, _ := s.runner.Run(actx, farAddScript, []string{bin, rev.Name, rev.Addr, "check"}, nil); decodeFirst(out, &r) == nil && r.Addr != "" && r.Addr != rev.Addr {
		return fail(s.name + " already has a machine called " + rev.Name + ", at " + clean(r.Addr) + ". Nothing was changed there.")
	}
	s.say(StepReverse, StateRun, "Add "+rev.Name+" on "+s.name+".", Command{s.name, bin + " hosts add " + rev.Name + " " + rev.Addr})
	out, _, err := s.runner.Run(actx, farAddScript, []string{bin, rev.Name, rev.Addr, "add"}, nil)
	r = federation.HostReport{}
	if err == nil {
		err = decodeFirst(out, &r)
	}
	rev.Status = string(r.Status)
	if err == nil && r.Status == federation.StatusUp {
		rev.Added = true
		s.say(StepReverse, StateOK, rev.Name+" is added on "+s.name+". Its link is up.")
		return rev, nil
	}
	// Never leave a broken entry there.
	_, _, _ = s.runner.Run(actx, farRemoveScript, []string{bin, rev.Name}, nil)
	return fail("The link from " + s.name + " to this machine did not come up. Nothing was added on " + s.name + ". " + clean(r.Reason))
}

// reachBack tests one address from the far side. A key the far side does
// not know, or a key this machine refuses, gets its fix, with the person's
// consent, and one test again.
func (s *Setup) reachBack(ctx context.Context, a Asker, cd *Candidate) (bool, error) {
	for try := 0; try < 2; try++ {
		tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, stderr, err := s.runner.Run(tctx, reachBackScript, []string{cd.Addr}, nil)
		cancel()
		if err != nil {
			return false, failureOf(hostsync.RemoteRunError("could not run ssh on "+s.name, stderr, err), s.name, s.host.Addr, s.host.SSHOptions)
		}
		f := hostsync.ParseFacts(out)
		cd.Said = clean(strings.Join(f["said"], " "))
		cd.OK = f.Get("rc") == "0"
		at := s.name + " reaches this machine at " + cd.Addr + "."
		if cd.OK {
			cd.Failure = ""
			s.say(StepReverse, StateOK, at)
			return true, nil
		}
		class := federation.ClassifySSHFailure(cd.Said)
		cd.Failure = string(class)
		s.sayd(StepReverse, StateFailed, "Not "+lowerFirst(at), cd.Said)
		if try > 0 {
			return false, nil
		}
		var ok bool
		switch class {
		case federation.FailHostKeyNew:
			// The person checks the key on the far side, where ssh asks.
			err = a.Person(ctx, s.personSSH(s.name+" does not know the host key of this machine. Connect one time from there and check the key.", "ssh "+cd.Addr+" true"))
			ok = err == nil
		case federation.FailAuth:
			ok, err = s.allowFarKey(ctx, a)
		}
		if _, np := errors.AsType[*NeedPerson](err); np || !ok {
			// With no terminal, the fix is the person's. The way back
			// stays closed, and the setup goes on.
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

// allowFarKey adds the far side's public key to authorized_keys here, with
// the person's consent, tagged so removing the machine removes the key. A
// far side with no key makes one first, where the person sees it.
func (s *Setup) allowFarKey(ctx context.Context, a Asker) (bool, error) {
	tctx, cancel := context.WithTimeout(ctx, stepBudget)
	defer cancel()
	out, _, _ := s.runner.Run(tctx, readKeyScript, nil, nil)
	if strings.TrimSpace(out) == "" {
		if err := a.Person(ctx, s.personSSH(s.name+" has no ssh key. Make one there.", "ssh-keygen -t ed25519")); err != nil {
			return false, err
		}
		out, _, _ = s.runner.Run(tctx, readKeyScript, nil, nil)
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(out)))
	path := AuthorizedKeysPath()
	if err != nil || path == "" {
		s.say(StepReverse, StateFailed, "tuios cannot read the public key of "+s.name+".")
		return false, nil
	}
	fp := ssh.FingerprintSHA256(pub)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " " + clean(comment) + " " + KeyTag(s.name)
	yes, err := a.Confirm(ctx, Question{ID: "reverse_authorize", Default: true,
		Text:     "This machine refuses the ssh key of " + s.name + ". Allow it here?",
		Note:     "Key " + fp + " " + clean(comment) + ". tuios hosts remove " + s.name + " takes it out again.",
		Commands: []Command{{"here", "add to ~/.ssh/authorized_keys: " + fp + " " + KeyTag(s.name)}}})
	if err != nil || !yes {
		return false, err
	}
	if err := AppendAuthorizedKey(path, line); err != nil {
		s.say(StepReverse, StateFailed, err.Error())
		return false, nil
	}
	s.say(StepReverse, StateOK, "This machine allows the key "+fp+" "+clean(comment)+" of "+s.name+".")
	return true, nil
}

// KeyTag ends an authorized_keys line that the both-ways step added for the
// machine called name, so removing that machine takes the line out again.
func KeyTag(name string) string { return "tuios-host=" + name }

// AuthorizedKeysPath is this user's authorized_keys file.
func AuthorizedKeysPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "authorized_keys")
}

// AppendAuthorizedKey adds one line to the file. It makes the folder (0700)
// and the file (0600) when they are missing, and leaves the modes of ones
// that exist alone.
func AppendAuthorizedKey(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	prefix := ""
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 && data[len(data)-1] != '\n' { //nolint:gosec // the file the person names
		prefix = "\n"
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // the file the person names
	if err != nil {
		return err
	}
	if _, err := f.WriteString(prefix + line + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// RemoveAuthorizedKeys takes out of the authorized_keys file at path every
// line that the both-ways step added for the machine called name, and
// reports how many. A missing file removes nothing.
func RemoveAuthorizedKeys(path, name string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // this user's own file
	if err != nil {
		return 0, nil
	}
	tag := " " + KeyTag(name)
	var keep []string
	n := 0
	for l := range strings.SplitSeq(string(data), "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), tag) {
			n++
		} else {
			keep = append(keep, l)
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, os.WriteFile(path, []byte(strings.Join(keep, "\n")), 0o600)
}

// decodeFirst decodes the first JSON value in out.
func decodeFirst(out string, v any) error {
	i := strings.IndexByte(out, '{')
	if i < 0 {
		return errors.New("no report")
	}
	return json.NewDecoder(strings.NewReader(out[i:])).Decode(v)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
