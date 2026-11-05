package hostsetup

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
)

// The steps, as events and the JSON name them.
const (
	StepProbe   = "probe"
	StepFix     = "fix"
	StepInstall = "install"
	StepService = "service"
	StepStart   = "start"
	StepAdd     = "add"
	StepTest    = "test"
	StepReverse = "both_ways"
)

// The states of a step.
const (
	StateRun    = "run"
	StateOK     = "ok"
	StateFailed = "failed"
	StateSkip   = "skipped"
	StatePerson = "person"
	// StatePlan is a step a dry run would take.
	StatePlan = "plan"
)

// Event is one report from a step.
type Event struct {
	Step  string `json:"step"`
	State string `json:"state"`
	// Text is one plain sentence for the person.
	Text string `json:"text,omitempty"`
	// Detail is what the far machine or ssh said. It is data.
	Detail string `json:"detail,omitempty"`
	// Commands are what the step runs, or with a dry run would run.
	Commands []Command `json:"commands,omitempty"`
	// Done and Total count the bytes of a copy.
	Done  int64 `json:"done,omitempty"`
	Total int64 `json:"total,omitempty"`
}

// Choices are the answers a front end already has, from flags or from the
// person. A nil bool is a question to ask.
type Choices struct {
	// Install is copy, release, pkg, script or none. Empty asks.
	Install string
	// Manager picks the package manager with InstallPkg. Empty takes the
	// first one the machine has.
	Manager string
	// Service starts tuios at boot there.
	Service *bool
	// BothWays adds this machine on the far machine too.
	BothWays *bool
	// Address is this machine's address for the far side. Empty tries the
	// candidates.
	Address string
	// Yes agrees to every step the choices name.
	Yes bool
	// DryRun probes and plans, and changes nothing.
	DryRun bool
	// Keep adds the machine even when its link does not come up.
	Keep bool
	// bulk says the run is one machine of a tailnet run. Then InstallCopy
	// means "the version of this machine", and a machine of another
	// platform gets the release of that version.
	bulk bool
}

// Question is a yes or no a front end asks.
type Question struct {
	// ID is install, start, service, both_ways, keep or a both-ways fix.
	ID string
	// Text is the question, and Note one more line under it.
	Text string
	Note string
	// Default is the answer when the person only presses enter.
	Default bool
	// Commands are what a yes runs.
	Commands []Command
}

// Asker is a front end.
type Asker interface {
	// Event reports progress.
	Event(Event)
	// PickInstall asks which way to install tuios. The list shows every
	// command, so a pick is consent.
	PickInstall(ctx context.Context, opts []InstallOption) (InstallOption, error)
	// Confirm asks a yes or no. A front end that cannot ask returns
	// NeedChoice.
	Confirm(ctx context.Context, q Question) (bool, error)
	// Fix shows a failure. It runs the fix when the fix needs the person
	// and they agree, and says whether to test again.
	Fix(ctx context.Context, f *Failure) (retry bool, err error)
	// Person runs a step that needs the person, such as a package manager
	// that asks for a password. It returns when the command ended.
	Person(ctx context.Context, p *PersonStep) error
}

// NeedChoice is the error of a front end that cannot ask, for a choice that
// has to come from a flag.
type NeedChoice struct {
	// Flag names what to pass, such as "--install copy, release, pkg or script".
	Flag string
	// What says what is missing.
	What string
}

func (e *NeedChoice) Error() string {
	return e.What + " Pass " + e.Flag + "."
}

// NeedPerson is the error of a front end with no terminal for a step that
// needs the person.
type NeedPerson struct{ Step *PersonStep }

func (e *NeedPerson) Error() string {
	if e.Step.URL != "" {
		return e.Step.Why + " Open " + e.Step.URL + ", then run the setup again."
	}
	return e.Step.Why + " Run this command in a terminal, then run the setup again: " + e.Step.Line
}

// Result is what the setup did.
type Result struct {
	OK     bool   `json:"ok"`
	DryRun bool   `json:"dry_run,omitempty"`
	Dest   string `json:"dest"`
	Name   string `json:"name"`
	Probe  *Probe `json:"probe,omitempty"`
	// Options are the install choices, when tuios had to be installed.
	Options []InstallOption `json:"options,omitempty"`
	// Installed is the source used, empty when nothing was installed.
	Installed string `json:"installed,omitempty"`
	// Notes are what the install path said besides its steps.
	Notes []string `json:"notes,omitempty"`
	// Note says what a person should know about the install, such as the
	// release a machine of another platform got in place of a copy.
	Note  string  `json:"note,omitempty"`
	Steps []Event `json:"steps"`
	// Failure is the ssh failure that stopped the setup.
	Failure *Failure `json:"failure,omitempty"`
	// Person is the step that waits for the person.
	Person *PersonStep `json:"person,omitempty"`
	// Host is the report of the link at the end.
	Host *federation.HostReport `json:"host,omitempty"`
	// Reverse is what the both-ways step did.
	Reverse *Reverse `json:"both_ways,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// keep records e as the state of its step: the last event of a step wins,
// in the order the steps first ran. Progress ticks are not kept.
func (r *Result) keep(e Event) {
	if e.State == StateRun && e.Total > 0 {
		return
	}
	for i := range r.Steps {
		if r.Steps[i].Step == e.Step {
			// A plan or a run keeps its commands when the next event of
			// the step has none.
			if len(e.Commands) == 0 {
				e.Commands = r.Steps[i].Commands
			}
			r.Steps[i] = e
			return
		}
	}
	r.Steps = append(r.Steps, e)
}

// budget bounds the steps that talk to the far machine. An install and a
// package manager get much longer.
const (
	stepBudget    = 90 * time.Second
	installBudget = 20 * time.Minute
	// probeTries bounds the tests again after a fix.
	probeTries = 4
)

// Run sets up the machine, asking a for each choice c does not make. It
// always returns a Result, and an error when the setup stopped.
func (s *Setup) Run(ctx context.Context, c Choices, a Asker) (res *Result, err error) {
	res = &Result{Dest: s.opts.Dest, Name: s.name, DryRun: c.DryRun}
	s.res, s.asker = res, a
	defer func() {
		res.OK = err == nil
		if err != nil {
			res.Error = err.Error()
			if f, ok := errors.AsType[*Failure](err); ok {
				res.Failure = f
			}
			if np, ok := errors.AsType[*NeedPerson](err); ok {
				res.Person = np.Step
			}
		}
	}()
	s.open()

	// 2. Probe, and fix what ssh says until it passes.
	for try := 0; ; try++ {
		s.say(StepProbe, StateRun, "Read "+s.name+" over ssh.")
		pctx, cancel := context.WithTimeout(ctx, stepBudget)
		p, perr := probe(pctx, s.runner)
		cancel()
		if perr == nil {
			s.probe = p
			res.Probe = p
			break
		}
		perr = failureOf(perr, s.name, s.host.Addr, s.host.SSHOptions)
		f, ok := errors.AsType[*Failure](perr)
		if !ok {
			s.say(StepProbe, StateFailed, perr.Error())
			return res, perr
		}
		s.sayd(StepProbe, StateFailed, f.Reason, f.SSHSaid)
		if try+1 >= probeTries || c.DryRun {
			return res, f
		}
		var fix []Command
		if f.FixCommand != "" {
			fix = []Command{{Where: "here", Line: f.FixCommand}}
		}
		s.say(StepFix, StatePerson, f.Fix, fix...)
		var retry bool
		var ferr error
		if s.gate != nil && f.Class == FailTailscaleCheck {
			retry, ferr = s.gate.fix(ctx, a, f, &s.round)
		} else {
			retry, ferr = a.Fix(ctx, f)
		}
		if ferr != nil {
			return res, ferr
		}
		if !retry {
			return res, f
		}
		s.say(StepFix, StateOK, "Test again.")
		// The fix may have changed what ssh does (a new key, a known host),
		// so the next test opens a connection of its own.
		s.Close()
		s.open()
	}
	if s.probe.OS == "" {
		return res, errors.New(s.name + " runs " + s.probe.Uname + ", which tuios does not run on")
	}

	// 3. tuios and its daemon.
	state := s.daemonState(ctx)
	p := s.probe
	said := "tuios is not installed on " + s.name + "."
	switch state.Status {
	case federation.StatusUp:
		said = "tuios " + state.DaemonVersion + " runs on " + s.name + "."
	case federation.StatusNoDaemon:
		said = "tuios " + p.TuiosVersion + " is installed on " + s.name + ". Its daemon does not run."
	case federation.StatusIncompatible:
		said = "tuios " + p.TuiosVersion + " on " + s.name + " is too old for this machine."
	}
	s.say(StepProbe, StateOK, p.System+", "+p.Arch+". "+strings.Join(append(p.Managers, p.Service), ", ")+". "+said)

	// 4. Install, when it is missing or too old.
	installed := false
	if s.probe.Tuios == "" || state.Status == federation.StatusIncompatible {
		installed = true
		if err := s.install(ctx, c, a, res); err != nil {
			return res, err
		}
		if !c.DryRun {
			state = s.daemonState(ctx)
		}
	}

	// 5. Start the daemon, at boot when asked.
	if err := s.start(ctx, c, a, state.Status == federation.StatusUp, installed); err != nil {
		return res, err
	}

	// 6. Add it here and test the link.
	if err := s.addAndTest(ctx, c, res); err != nil {
		return res, err
	}

	// 7. Both ways.
	both := c.BothWays
	if both == nil {
		q := Question{ID: "both_ways", Default: true,
			Text: "Add this machine on " + s.name + " too? Then a client on " + s.name + " sees your sessions here.",
			Note: "Your Inbox needs only this direction. It works already."}
		yes, err := a.Confirm(ctx, q)
		if err != nil {
			return res, err
		}
		both = &yes
	}
	if *both {
		rev, err := s.reverse(ctx, c, a)
		res.Reverse = rev
		if err != nil {
			return res, err
		}
	} else {
		s.say(StepReverse, StateSkip, "This machine was not added on "+s.name+".")
	}
	return res, nil
}

// daemonState dials the machine once, the way the link does, and says
// whether a daemon answers.
func (s *Setup) daemonState(ctx context.Context) federation.HostReport {
	if s.probe.Tuios == "" {
		return federation.HostReport{Status: federation.StatusNoBinary}
	}
	h := s.host
	h.Command = s.probe.Tuios
	dctx, cancel := context.WithTimeout(ctx, stepBudget)
	defer cancel()
	r, err := s.testLink(dctx, h)
	if err != nil {
		return federation.HostReport{Status: federation.StatusUnreachable, Reason: err.Error()}
	}
	return r
}

// testLink dials h with the front end's dialer, or one of this process.
func (s *Setup) testLink(ctx context.Context, h federation.Host) (federation.HostReport, error) {
	if s.opts.TestLink != nil {
		return s.opts.TestLink(ctx, h)
	}
	return DialOnce(ctx, h, s.opts.Version)
}

// install picks a source, gets consent, and installs tuios.
func (s *Setup) install(ctx context.Context, c Choices, a Asker, res *Result) error {
	opts := s.installOptions()
	res.Options = opts
	var pick InstallOption
	if c.Install == "" {
		if c.DryRun {
			return &NeedChoice{Flag: "--install copy, release, pkg or script", What: "tuios is not installed on " + s.name + "."}
		}
		var err error
		if pick, err = a.PickInstall(ctx, opts); err != nil {
			return err
		}
	} else {
		for _, o := range opts {
			if o.ID == c.Install && (c.Manager == "" || o.Manager == c.Manager) {
				pick = o
				break
			}
		}
		// In a bulk run a copy means this version: a machine of another
		// platform gets the release of it.
		if pick.ID == InstallCopy && !pick.Available && c.bulk && opts[1].Available {
			pick = opts[1]
			res.Note = s.name + " is " + s.probe.Platform() + ". tuios sent the v" + strings.TrimPrefix(s.opts.Version, "v") + " release, checked with its sha256."
		}
		switch {
		case pick.ID == "":
			return errors.New("--install " + c.Install + " is not a way to install tuios on " + s.name + ". Run with --dry-run to see the choices")
		case !pick.Available:
			return errors.New("--install " + c.Install + " cannot run: " + pick.WhyNot)
		case !c.Yes && !c.DryRun:
			if ok, err := a.Confirm(ctx, Question{ID: "install", Default: true, Text: "Install tuios on " + s.name + "? " + pick.Label, Commands: pick.Commands}); !ok {
				return cmp.Or(err, errStopped)
			}
		}
	}
	res.Installed = pick.ID
	if c.DryRun {
		s.say(StepInstall, StatePlan, pick.Label, pick.Commands...)
		return nil
	}
	ictx, cancel := context.WithTimeout(ctx, installBudget)
	defer cancel()
	s.say(StepInstall, StateRun, pick.Label, pick.Commands...)
	var err error
	switch {
	case pick.ID == InstallCopy || pick.ID == InstallRelease:
		err = s.bringUp(ictx, pick.ID, hostsync.PlanOptions{})
	case pick.NeedsPerson:
		err = a.Person(ictx, s.personSSH(pick.Label, pick.remote))
	default:
		_, err = s.runRemote(ictx, pick.remote)
	}
	// The probe again: the version shown is the one that answers, not the
	// one that was asked for.
	if err == nil {
		s.probe, err = probe(ictx, s.runner)
		res.Probe = s.probe
		err = failureOf(err, s.name, s.host.Addr, s.host.SSHOptions)
	}
	if err == nil && s.probe.Tuios == "" {
		err = errors.New("the install ran, and tuios is still not found on " + s.name + ". The link looks on the PATH, in a login shell and in ~/.local/bin")
	}
	if err != nil {
		s.say(StepInstall, StateFailed, err.Error())
		return err
	}
	s.say(StepInstall, StateOK, "tuios "+s.probe.TuiosVersion+" is installed at "+s.probe.Tuios+".")
	return nil
}

// errStopped is a setup the person stopped at a question.
var errStopped = errors.New("stopped. Nothing was added")

// bringUp runs the sync path of hostsync on the machine: an install from
// source ("copy" or "release", none for ""), then the service and the
// daemon start that opts ask for. hosts sync runs the same path.
func (s *Setup) bringUp(ctx context.Context, source string, opts hostsync.PlanOptions) error {
	t := &hostsync.Target{Name: s.name, Host: s.host, Runner: s.runner, Res: hostsync.Result{Host: s.name, Addr: s.host.Addr}}
	if err := hostsync.ProbeBinary(ctx, t); err != nil {
		return failureOf(err, s.name, s.host.Addr, s.host.SSHOptions)
	}
	if t.Res.Path != "" {
		t.Res.Daemon.State = hostsync.DaemonStopped
		if opts.Start {
			t.Res.Daemon.State = s.daemonFact
		}
	}
	src := &hostsync.Source{Version: t.Res.Before}
	switch source {
	case InstallCopy:
		src = hostsync.NewSource(nil)
		if err := src.LoadBinary(s.opts.Executable); err != nil {
			return err
		}
	case InstallRelease:
		if src = s.opts.Source; src == nil || src.Releases == nil {
			return errors.New("this tuios cannot fetch its release here. Pick another way to install")
		}
	}
	hostsync.Plan(t, src, opts)
	if source == "" {
		t.Install = false
	} else if err := src.Prepare([]string{t.Platform}); err != nil {
		return err
	}
	t.Progress = func(done, total int64) {
		if done == total || done%(1<<20) < 32<<10 {
			s.emit(Event{Step: StepInstall, State: StateRun, Done: done, Total: total, Text: "Sending tuios to " + s.name + "."})
		}
	}
	if !t.Failed {
		hostsync.Apply(t, src)
	}
	// A note of the sync path (a login shell that does not find tuios, a
	// service manager that said something) is kept, and told once.
	s.res.Notes = append(s.res.Notes, t.Res.Notes...)
	for _, n := range t.Res.Notes {
		s.asker.Event(Event{Step: "note", State: StateSkip, Text: n})
	}
	if t.Res.Error != "" {
		return failureOf(errors.New(t.Res.Error), s.name, s.host.Addr, s.host.SSHOptions)
	}
	if t.Res.ServiceInstalled {
		s.say(StepService, StateOK, "tuios starts on "+s.name+" when it boots.")
	}
	return nil
}

// start starts the daemon there, under the service when the person wants
// it at boot. An install the person picked is consent to start what it
// installed. A daemon that is installed and stopped is asked about first.
func (s *Setup) start(ctx context.Context, c Choices, a Asker, running, installed bool) error {
	bin := cmp.Or(s.probe.Tuios, "~/.local/bin/tuios")
	svc := Command{s.name, bin + " daemon install-service"}
	start := Command{s.name, bin + " start-server"}
	can := s.probe.Service != "" && !s.probe.ServiceInstalled
	want := c.Service
	if want == nil && can {
		yes, err := a.Confirm(ctx, Question{ID: "service", Default: true, Text: "Start tuios on " + s.name + " when it boots?", Note: s.probe.Service, Commands: []Command{svc}})
		if err != nil {
			return err
		}
		want = &yes
	}
	service := want != nil && *want && can
	if !service {
		s.say(StepService, StateSkip, "tuios does not start at boot on "+s.name+".")
	}
	if c.DryRun {
		if service {
			s.say(StepService, StatePlan, "Start tuios at boot.", svc)
		} else if !running {
			s.say(StepStart, StatePlan, "Start the daemon.", start)
		}
		return nil
	}
	if !running && !installed && !c.Yes {
		if ok, err := a.Confirm(ctx, Question{ID: "start", Default: true, Text: "Start the tuios daemon on " + s.name + "?", Commands: []Command{start}}); !ok {
			return cmp.Or(err, errStopped)
		}
	}
	if running && !service {
		return nil
	}
	s.daemonFact = hostsync.DaemonStopped
	if running {
		s.daemonFact = hostsync.DaemonRunning
	}
	s.say(StepStart, StateRun, "Start the daemon.", start)
	sctx, cancel := context.WithTimeout(ctx, stepBudget)
	defer cancel()
	if err := s.bringUp(sctx, "", hostsync.PlanOptions{Start: !running, Service: service}); err != nil {
		s.say(StepStart, StateFailed, err.Error())
		return err
	}
	s.say(StepStart, StateOK, "The daemon runs on "+s.name+".")
	return nil
}

// addAndTest writes [hosts.NAME] here, asks the daemon to read it, and tests
// the link. A link that does not come up is removed again, unless the person
// keeps it.
func (s *Setup) addAndTest(ctx context.Context, c Choices, res *Result) error {
	path := s.opts.ConfigPath
	add := Command{"here", "write [hosts." + s.name + "] addr = \"" + s.host.Addr + "\" to " + path}
	if c.DryRun {
		if s.existing == "" {
			s.say(StepAdd, StatePlan, "Add "+s.name+" here.", add)
		}
		s.say(StepTest, StatePlan, "Test the link to "+s.name+".")
		return nil
	}
	if s.existing == "" {
		s.say(StepAdd, StateRun, "Add "+s.name+" here.", add)
		if _, err := config.SetHostInFile(path, s.name, config.HostConfig{Addr: s.host.Addr, SSHOptions: s.host.SSHOptions}); err != nil {
			s.say(StepAdd, StateFailed, err.Error())
			return err
		}
		s.apply()
		s.say(StepAdd, StateOK, s.name+" is added here.")
	}
	tctx, cancel := context.WithTimeout(ctx, stepBudget)
	defer cancel()
	h := s.host
	h.ControlPath = ""
	r, err := s.testLink(tctx, h)
	res.Host = &r
	if err == nil && r.Status == federation.StatusUp {
		s.say(StepTest, StateOK, "The link to "+s.name+" is up.")
		return nil
	}
	if err == nil {
		err = errors.New("the link to " + s.name + " is " + string(r.Status) + ". " + r.Reason)
	}
	s.sayd(StepTest, StateFailed, err.Error(), r.Detail)
	if s.existing == "" && !c.Keep {
		if _, rerr := config.RemoveHostFromFile(path, s.name); rerr == nil {
			s.apply()
			s.say(StepAdd, StateSkip, s.name+" was removed again. Nothing was added here.")
		}
	}
	return err
}

// apply asks the daemon here to read the entry, when one runs.
func (s *Setup) apply() {
	if s.opts.Apply != nil {
		_ = s.opts.Apply(s.name)
	}
}

// ConfiguredHosts reads the [hosts] table of path.
func ConfiguredHosts(path string) (map[string]federation.Host, error) {
	in, err := config.HostsInFile(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]federation.Host, len(in))
	for name, h := range in {
		out[name] = federation.Host{Name: name, Addr: h.Addr}
	}
	return out, nil
}

// emit records e in the result and reports it to the front end.
func (s *Setup) emit(e Event) {
	s.res.keep(e)
	s.asker.Event(e)
}

// say reports a step's state, with the commands it runs.
func (s *Setup) say(step, state, text string, cmds ...Command) {
	s.emit(Event{Step: step, State: state, Text: text, Commands: cmds})
}

// sayd reports a step's state with what the far side said.
func (s *Setup) sayd(step, state, text, detail string) {
	s.emit(Event{Step: step, State: state, Text: text, Detail: detail})
}
