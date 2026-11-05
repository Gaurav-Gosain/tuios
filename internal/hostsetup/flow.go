package hostsetup

import (
	"context"
	"errors"
	"strings"
)

// The model a front end draws. The interactive command and the add sheet in
// the app are two drawers on this one model: the steps so far, the question
// that waits, and the words for both. Neither keeps a model or a text table
// of its own, so the two cannot say different things.
//
// The engine runs in its own goroutine and asks through a Bridge. Each
// question becomes a Prompt that the front end shows; a key goes to
// Prompt.Key, which answers the engine when the question is done.

// Line is one step as a front end draws it.
type Line struct {
	Step, State string
	// Label is the step's name (StepLabel), Text what happened.
	Label, Text, Detail string
	Commands            []Command
	Done, Total         int64
}

// Flow is what a front end draws for one machine.
type Flow struct {
	Name  string
	Lines []Line
	// Ask is the question that waits, nil when none does.
	Ask *Prompt
	// Notes are the lines the install path said besides its steps.
	Notes []string
}

// Apply records e: the last event of a step is its line, in the order the
// steps first ran. A note has no line of its own.
func (f *Flow) Apply(e Event) {
	if e.Step == "note" {
		f.Notes = append(f.Notes, e.Text)
		return
	}
	l := Line{Step: e.Step, State: e.State, Label: StepLabel(e.Step, f.Name), Text: e.Text, Detail: e.Detail, Commands: e.Commands, Done: e.Done, Total: e.Total}
	for i := range f.Lines {
		if f.Lines[i].Step == e.Step {
			if len(l.Commands) == 0 {
				l.Commands = f.Lines[i].Commands
			}
			f.Lines[i] = l
			return
		}
	}
	f.Lines = append(f.Lines, l)
}

// StepLabel is the name of a step, for the machine called name. It is the
// one text table of the steps.
func StepLabel(step, name string) string {
	switch step {
	case StepProbe:
		return "Test " + name
	case StepFix:
		return "Fix"
	case StepInstall:
		return "Install"
	case StepService:
		return "Start at boot"
	case StepStart:
		return "Start the daemon"
	case StepAdd:
		return "Add " + name + " here"
	case StepTest:
		return "Test the link"
	case StepReverse:
		return "Both ways"
	}
	return step
}

// Mark is the mark of a step state, the same in every front end.
func Mark(state string) string {
	switch state {
	case StateOK:
		return "✓"
	case StateFailed:
		return "✗"
	case StatePerson:
		return "!"
	case StateSkip:
		return "-"
	case StatePlan:
		return "·"
	}
	return "○"
}

// The kinds of question.
const (
	// AskPick is the install list: Options, with Cursor.
	AskPick = "pick"
	// AskConfirm is a yes or no: Question, with Yes as the answer so far.
	AskConfirm = "confirm"
	// AskFix is an ssh failure and its fix: Failure. Yes runs the fix, or
	// tests again when the fix is the person's to make elsewhere.
	AskFix = "fix"
	// AskPerson is a command the front end runs where the person can answer
	// it: Person. The front end calls Finish when it ended.
	AskPerson = "person"
	// AskPeers is the tailnet checklist: Peers, with Ticked. The front end
	// makes it with PeersPrompt, before the run, and reads Picked.
	AskPeers = "peers"
)

// Prompt is one question of the engine to the person.
type Prompt struct {
	Kind string
	// Machine names the machine in a bulk run, empty for one machine.
	Machine  string
	Question *Question
	Options  []InstallOption
	Failure  *Failure
	Person   *PersonStep
	// Cursor is the picked option, Yes the picked answer, and All says to
	// show every command of the options.
	Cursor int
	Yes    bool
	All    bool
	Peers  []Peer
	Ticked []bool
	// Own is the row of Peers that asked for its own answers with o, -1
	// when none did. The front end opens the install list for that
	// machine, and sets Own back to -1.
	Own int
	// Skip says the person skipped this machine at its fix, in a bulk run.
	Skip  bool
	reply chan error
}

// PeersPrompt is the checklist of a tailnet, with nothing ticked.
func PeersPrompt(peers []Peer) *Prompt {
	p := &Prompt{Kind: AskPeers, Peers: peers, Ticked: make([]bool, len(peers)), Cursor: -1, Own: -1}
	p.move(1)
	return p
}

// Picked are the ticked peers.
func (p *Prompt) Picked() []Peer {
	var out []Peer
	for i, t := range p.Ticked {
		if t {
			out = append(out, p.Peers[i])
		}
	}
	return out
}

// Hints are the keys of the prompt, as pairs of key and word, for a footer.
func (p *Prompt) Hints() [][2]string {
	switch p.Kind {
	case AskPick:
		return [][2]string{{"↑↓", "move"}, {"enter", "pick"}, {"d", "show every command"}, {"ctrl+c", "stop"}}
	case AskFix:
		h := [][2]string{{"t", "test again"}}
		if p.Failure.FixCommand != "" && p.Failure.NeedsPerson {
			h = [][2]string{{"enter", "run it here"}, {"t", "test again"}}
		}
		if p.Machine != "" {
			h = append(h, [2]string{"s", "skip " + p.Machine})
		}
		return append(h, [2]string{"ctrl+c", "stop"})
	case AskPerson:
		return [][2]string{{"t", "test again"}, {"ctrl+c", "stop"}}
	case AskConfirm:
		return [][2]string{{"y", "yes"}, {"n", "no"}, {"enter", "pick"}, {"ctrl+c", "stop"}}
	case AskPeers:
		return [][2]string{{"space", "tick"}, {"a", "all"}, {"o", "own answers"}, {"enter", "go on"}, {"ctrl+c", "stop"}}
	}
	return nil
}

// Key moves the prompt or answers it. It reports whether the question is
// answered, which also wakes the engine. A front end sends the person's
// key by name: "up", "down", "left", "right", "enter", "y", "n", "t", "d",
// "s", "o".
func (p *Prompt) Key(k string) bool {
	switch p.Kind {
	case AskPeers:
		switch k {
		case "up", "k":
			p.move(-1)
		case "down", "j":
			p.move(1)
		case "space", " ":
			if p.Cursor >= 0 {
				p.Ticked[p.Cursor] = !p.Ticked[p.Cursor]
			}
		case "a":
			for i, peer := range p.Peers {
				p.Ticked[i] = peer.CanSetUp
			}
		case "o":
			// The row gets its own answers, and is ticked.
			if p.Cursor >= 0 {
				p.Own, p.Ticked[p.Cursor] = p.Cursor, true
			}
		case "enter":
			return true
		}
	case AskPick:
		switch k {
		case "up", "k":
			p.move(-1)
		case "down", "j":
			p.move(1)
		case "d":
			p.All = !p.All
		case "enter":
			p.Finish(nil)
			return true
		}
	case AskConfirm:
		switch k {
		case "left", "right", "tab", "h", "l":
			p.Yes = !p.Yes
		case "y", "n":
			p.Yes = k == "y"
			p.Finish(nil)
			return true
		case "enter":
			p.Finish(nil)
			return true
		}
	case AskFix:
		switch k {
		case "enter":
			p.Yes = true
			p.Finish(nil)
			return true
		case "t", "s":
			if k == "s" && p.Machine == "" {
				return false
			}
			p.Yes, p.Skip = false, k == "s"
			p.Finish(nil)
			return true
		}
	case AskPerson:
		// The person ran the step elsewhere, or signed in: test again.
		if k == "t" {
			p.Finish(nil)
			return true
		}
	}
	return false
}

// move steps the cursor over the rows that can be picked.
func (p *Prompt) move(d int) {
	n := max(len(p.Options), len(p.Peers))
	for i := p.Cursor + d; i >= 0 && i < n; i += d {
		if (p.Kind == AskPeers && p.Peers[i].CanSetUp) || (p.Kind != AskPeers && p.Options[i].Available) {
			p.Cursor = i
			return
		}
	}
}

// Finish answers the question. err stops the setup, as a cancel does.
func (p *Prompt) Finish(err error) {
	if p.reply != nil {
		p.reply <- err
		p.reply = nil
	}
}

// Bridge is an Asker for a front end with an event loop: each event and each
// question goes to Send, and a question waits for its Prompt to be finished.
// Send must not block for long: a Bubble Tea program's Send fits.
type Bridge struct {
	Send func(msg any)
	// Machine names the machine on each event and prompt in a bulk run.
	Machine string
}

// BridgeEvent is an event of the engine for the front end.
type BridgeEvent struct {
	Machine string
	Event
}

func (b *Bridge) Event(e Event) { b.Send(BridgeEvent{b.Machine, e}) }

// For is the Bridge of one machine of a bulk run: a BulkAsker.
func (b *Bridge) For(name string) Asker { return &Bridge{Send: b.Send, Machine: name} }

// ask sends p and waits for its answer, or for ctx to end.
func (b *Bridge) ask(ctx context.Context, p *Prompt) error {
	p.Machine = b.Machine
	p.reply = make(chan error, 1)
	b.Send(p)
	select {
	case err := <-p.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Bridge) PickInstall(ctx context.Context, opts []InstallOption) (InstallOption, error) {
	p := &Prompt{Kind: AskPick, Options: opts, Cursor: -1}
	p.move(1)
	if p.Cursor < 0 {
		return InstallOption{}, errors.New("no way to install tuios can run there")
	}
	if err := b.ask(ctx, p); err != nil {
		return InstallOption{}, err
	}
	return opts[p.Cursor], nil
}

func (b *Bridge) Confirm(ctx context.Context, q Question) (bool, error) {
	p := &Prompt{Kind: AskConfirm, Question: &q, Yes: q.Default}
	err := b.ask(ctx, p)
	return p.Yes && err == nil, err
}

// Fix shows the failure. A yes on a fix that needs the person runs it as a
// person step first. Either way the engine tests again, unless the person
// skipped the machine.
func (b *Bridge) Fix(ctx context.Context, f *Failure) (bool, error) {
	p := &Prompt{Kind: AskFix, Failure: f}
	if err := b.ask(ctx, p); err != nil || p.Skip {
		return false, err
	}
	if p.Yes && f.NeedsPerson && (f.FixCommand != "" || f.URL != "") {
		step := &PersonStep{Why: f.Fix, Line: f.FixCommand, URL: f.URL}
		if f.FixCommand != "" {
			step.Argv = strings.Fields(f.FixCommand)
		}
		if err := b.Person(ctx, step); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (b *Bridge) Person(ctx context.Context, s *PersonStep) error {
	return b.ask(ctx, &Prompt{Kind: AskPerson, Person: s})
}
