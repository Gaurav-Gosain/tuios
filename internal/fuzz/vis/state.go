package vis

import (
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
)

const (
	tapeRows = 7
	tapeCols = 80
	tapeCap  = tapeRows * tapeCols

	ledgerCap = 5

	funnelHead = 6
	funnelTail = 22
)

// Phase is what the engine is doing, derived from the events rather than
// reported: the first Shrink call means minimisation started, Done means it
// stopped. Nothing on screen claims a phase the engine did not enter.
type Phase int

const (
	PhaseGenerating Phase = iota
	PhaseShrinking
	PhaseDone
)

// Attempt is one shrink candidate as it was tested.
type Attempt struct {
	Pass     string
	Size     int
	Accepted bool
}

type state struct {
	mu sync.Mutex

	rules   []fuzz.RuleInfo
	classes []Class

	seed  uint64
	steps int

	started  time.Time
	finished time.Time

	actions int
	checks  int

	tape          [tapeCap]uint8
	head          int
	violationCell int

	ledger  [ledgerCap]fuzz.Action
	ledgerN int

	mix []int

	ruleBroken []bool
	ruleIndex  map[string]int
	failedRule string
	failedInfo fuzz.RuleInfo
	failedStep int
	detail     string
	violated   bool

	phase      Phase
	initialLen int
	bestLen    int
	headRuns   [funnelHead]Attempt
	tailRuns   [funnelTail]Attempt
	runsSeen   int

	done   bool
	result fuzz.Result

	gen        uint64
	sinceBatch int

	wake chan struct{}
}

func (s *state) nudge() {
	if s.wake == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func newState(rules []fuzz.RuleInfo, classes []Class) *state {
	s := &state{
		rules:         rules,
		classes:       classes,
		mix:           make([]int, len(classes)),
		ruleBroken:    make([]bool, len(rules)),
		ruleIndex:     make(map[string]int, len(rules)),
		violationCell: -1,
		failedStep:    -1,
	}
	for i, r := range rules {
		if _, ok := s.ruleIndex[r.Name]; !ok {
			s.ruleIndex[r.Name] = i
		}
	}
	return s
}

func (s *state) Start(seed uint64, steps int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seed, s.steps = seed, steps
	s.started = time.Now()
	s.gen++
	s.nudge()
}

func (s *state) Step(i int, a fuzz.Action, vs []fuzz.Violation) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.classOf(a.Kind)
	s.tape[s.head] = uint8(c) + 1
	cell := s.head
	s.head = (s.head + 1) % tapeCap
	s.actions++
	s.sinceBatch++
	s.mix[c]++

	copy(s.ledger[1:], s.ledger[:ledgerCap-1])
	s.ledger[0] = a
	if s.ledgerN < ledgerCap {
		s.ledgerN++
	}

	if len(vs) > 0 && !s.violated {
		s.violated = true
		s.failedRule = vs[0].Rule
		s.detail = vs[0].Detail
		s.failedStep = i
		s.violationCell = cell
		if idx, ok := s.ruleIndex[vs[0].Rule]; ok {
			s.ruleBroken[idx] = true
			s.failedInfo = s.rules[idx]
		} else {
			s.failedInfo = fuzz.RuleInfo{Name: vs[0].Rule, Family: "unregistered"}
		}
	}
	s.gen++
	s.nudge()
}

func (s *state) Rule(_ int, rule string, ok bool) {
	s.mu.Lock()
	s.checks++
	if !ok {
		if idx, found := s.ruleIndex[rule]; found {
			s.ruleBroken[idx] = true
		}
	}
	s.mu.Unlock()
}

func (s *state) Shrink(pass string, size int, accepted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == PhaseGenerating {
		s.phase = PhaseShrinking
		s.initialLen = s.failedStep + 1
		if s.initialLen <= 0 {
			s.initialLen = s.actions
		}
		s.bestLen = s.initialLen
	}
	a := Attempt{Pass: pass, Size: size, Accepted: accepted}
	if s.runsSeen < funnelHead {
		s.headRuns[s.runsSeen] = a
	} else {
		copy(s.tailRuns[:], s.tailRuns[1:])
		s.tailRuns[funnelTail-1] = a
	}
	s.runsSeen++
	if accepted && size < s.bestLen {
		s.bestLen = size
	}
	s.gen++
	s.nudge()
}

func (s *state) Done(r fuzz.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	s.phase = PhaseDone
	s.result = r
	s.finished = time.Now()
	s.gen++
	s.nudge()
}

func (s *state) classOf(k fuzz.Kind) int {
	for i, c := range s.classes {
		if c.Holds(k) {
			return i
		}
	}
	return len(s.classes) - 1
}

// Snapshot is a consistent copy of the state for one frame. It is a value so
// the renderer can work on it with no lock held, which is what keeps a slow
// terminal from ever showing up as engine latency.
type Snapshot struct {
	Rules   []fuzz.RuleInfo
	Classes []Class

	Seed    uint64
	Steps   int
	Elapsed time.Duration

	Actions int
	Checks  int
	Rate    float64

	Tape          [tapeCap]uint8
	Head          int
	ViolationCell int

	Ledger  [ledgerCap]fuzz.Action
	LedgerN int
	Mix     []int

	Broken     []bool
	Violated   bool
	FailedRule fuzz.RuleInfo
	FailedStep int
	Detail     string

	Phase      Phase
	InitialLen int
	BestLen    int
	Runs       []Attempt
	RunsSeen   int
	Elided     int

	Done   bool
	Result fuzz.Result
}

func (s *state) snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	end := s.finished
	if end.IsZero() {
		end = time.Now()
	}
	elapsed := time.Duration(0)
	if !s.started.IsZero() {
		elapsed = end.Sub(s.started)
	}

	snap := Snapshot{
		Rules: s.rules, Classes: s.classes,
		Seed: s.seed, Steps: s.steps, Elapsed: elapsed,
		Actions: s.actions, Checks: s.checks,
		Tape: s.tape, Head: s.head, ViolationCell: s.violationCell,
		Ledger: s.ledger, LedgerN: s.ledgerN,
		Violated: s.violated, FailedRule: s.failedInfo, FailedStep: s.failedStep,
		Detail: s.detail,
		Phase:  s.phase, InitialLen: s.initialLen, BestLen: s.bestLen,
		RunsSeen: s.runsSeen,
		Done:     s.done, Result: s.result,
	}
	if secs := elapsed.Seconds(); secs > 0 {
		snap.Rate = float64(s.actions) / secs
	}
	snap.Mix = append([]int(nil), s.mix...)
	snap.Broken = append([]bool(nil), s.ruleBroken...)

	head := min(s.runsSeen, funnelHead)
	snap.Runs = append(snap.Runs, s.headRuns[:head]...)
	if s.runsSeen > funnelHead {
		tail := min(s.runsSeen-funnelHead, funnelTail)
		snap.Elided = s.runsSeen - funnelHead - tail
		snap.Runs = append(snap.Runs, s.tailRuns[funnelTail-tail:]...)
	}
	return snap
}

func (s *state) takeFrame(lastGen uint64) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen, s.gen != lastGen
}

func (s *state) batchReady(n int, lastPhase Phase) (Phase, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != lastPhase || s.done {
		s.sinceBatch = 0
		return s.phase, true
	}
	if s.sinceBatch >= n {
		s.sinceBatch = 0
		return s.phase, true
	}
	return s.phase, false
}
