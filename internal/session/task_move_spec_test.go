//go:build spec

package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// The differential test of the task card lifecycle against its Lean model
// (spec/tasks). It makes random cards and move streams from a seed, runs each
// stream through TaskState.apply and through the model's executable, and
// fails on the first step where the two differ, with the seed and the
// shortest stream it could find that still shows the difference.
//
//	(cd spec/tasks && lake build)
//	go test -tags spec -run TestTaskMoveMatchesLeanModel ./internal/session/
//
// The seam is this file alone: it builds a cardMove and a TaskState from the
// model's JSON, so nothing in the shipped binary changes for it.

var (
	specSeed    = flag.Uint64("spec.seed", 0, "seed of the random streams; 0 picks one from the clock")
	specStreams = flag.Int("spec.n", 100000, "number of random streams")
	specBin     = flag.String("spec.bin", "", "the Lean model's executable; empty is spec/tasks/.lake/build/bin/taskspec")
)

// The model's JSON. Every field is always present, as the model writes it.
type (
	specBlock struct {
		Reason string `json:"reason"`
		By     string `json:"by"`
		At     int64  `json:"at"`
	}
	specWork struct {
		Host    string `json:"host"`
		Session string `json:"session"`
		Window  string `json:"window"`
		Dir     string `json:"dir"`
	}
	specVerify struct {
		State      string  `json:"state"`
		Exit       *int    `json:"exit"`
		Note       string  `json:"note"`
		Tail       string  `json:"tail"`
		StartedAt  int64   `json:"started_at"`
		FinishedAt int64   `json:"finished_at"`
		Held       *string `json:"held"`
	}
	specAttachment struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		MediaType string `json:"media_type"`
		Size      int64  `json:"size"`
		Hash      string `json:"hash"`
		URL       string `json:"url"`
		Note      string `json:"note"`
		By        string `json:"by"`
		At        int64  `json:"at"`
	}
	specEvent struct {
		Action string `json:"action"`
		By     string `json:"by"`
		At     int64  `json:"at"`
		Stage  string `json:"stage"`
		Holder string `json:"holder"`
		Reason string `json:"reason"`
	}
	specCard struct {
		Stage         string           `json:"stage"`
		StageAt       int64            `json:"stage_at"`
		Holder        string           `json:"holder"`
		HeldAt        int64            `json:"held_at"`
		HolderName    string           `json:"holder_name"`
		HolderHarness string           `json:"holder_harness"`
		Dispatched    bool             `json:"dispatched"`
		Blocked       *specBlock       `json:"blocked"`
		Attempts      int              `json:"attempts"`
		Note          string           `json:"note"`
		Work          *specWork        `json:"work"`
		Verify        *specVerify      `json:"verify"`
		Attachments   []specAttachment `json:"attachments"`
		History       []specEvent      `json:"history"`
	}
	specMove struct {
		Kind        string           `json:"kind"`
		By          string           `json:"by"`
		Reason      string           `json:"reason"`
		Window      string           `json:"window"`
		Dispatched  bool             `json:"dispatched"`
		Names       bool             `json:"names"`
		Name        string           `json:"name"`
		Harness     string           `json:"harness"`
		Refresh     bool             `json:"refresh"`
		Stage       *string          `json:"stage"`
		Work        *specWork        `json:"work"`
		VerifyRun   bool             `json:"verify_run"`
		AutoFrom    []string         `json:"auto_from"`
		AutoTo      *string          `json:"auto_to"`
		Token       int64            `json:"token"`
		Verify      *specVerify      `json:"verify"`
		Note        string           `json:"note"`
		Attachments []specAttachment `json:"attachments"`
		Now         int64            `json:"now"`
	}
	cardStream struct {
		Card  specCard   `json:"card"`
		Moves []specMove `json:"moves"`
	}
	cardStep struct {
		Action  string   `json:"action"`
		Holder  string   `json:"holder"`
		Refusal *string  `json:"refusal"`
		Card    specCard `json:"card"`
	}
	specAnswer struct {
		Steps []cardStep `json:"steps"`
		Error string     `json:"error"`
	}
)

var specKinds = map[string]cardMoveKind{
	"claim": moveClaim, "release": moveRelease, "block": moveBlock, "unblock": moveUnblock,
	"block_errored": moveBlockErrored, "unblock_errored": moveUnblockErrored, "hand_in": moveHandIn,
	"finish": moveFinish, "stage": moveStage, "auto": moveAuto, "verified": moveVerified,
	"attach": moveAttach, "detach": moveDetach,
}

func (w *specWork) task() *TaskWork {
	if w == nil {
		return nil
	}
	return &TaskWork{Host: w.Host, Session: w.Session, Window: w.Window, Dir: w.Dir}
}

func (v *specVerify) task() *TaskVerify {
	if v == nil {
		return nil
	}
	out := &TaskVerify{State: v.State, Note: v.Note, Tail: v.Tail, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt}
	if v.Held != nil {
		out.Held = *v.Held
	}
	if v.Exit != nil {
		e := *v.Exit
		out.Exit = &e
	}
	return out
}

func leanAttachments(in []specAttachment) []TaskAttachment {
	var out []TaskAttachment
	for _, a := range in {
		out = append(out, TaskAttachment(a))
	}
	return out
}

func (c specCard) task() TaskState {
	tk := TaskState{ID: "t1", Stage: c.Stage, StageAt: c.StageAt, Holder: c.Holder, HeldAt: c.HeldAt,
		HolderName: c.HolderName, HolderHarness: c.HolderHarness, Dispatched: c.Dispatched,
		Attempts: c.Attempts, Note: c.Note, Work: c.Work.task(), Verify: c.Verify.task(),
		Attachments: leanAttachments(c.Attachments)}
	if c.Blocked != nil {
		tk.Blocked = &TaskBlock{Reason: c.Blocked.Reason, By: c.Blocked.By, At: c.Blocked.At}
	}
	for _, e := range c.History {
		tk.History = append(tk.History, TaskEvent(e))
	}
	return tk
}

// leanCardOf is the card's lifecycle fields as the model writes them.
func leanCardOf(tk *TaskState) specCard {
	c := specCard{Stage: tk.Stage, StageAt: tk.StageAt, Holder: tk.Holder, HeldAt: tk.HeldAt,
		HolderName: tk.HolderName, HolderHarness: tk.HolderHarness, Dispatched: tk.Dispatched,
		Attempts: tk.Attempts, Note: tk.Note, Attachments: []specAttachment{}, History: []specEvent{}}
	if b := tk.Blocked; b != nil {
		c.Blocked = &specBlock{Reason: b.Reason, By: b.By, At: b.At}
	}
	if w := tk.Work; w != nil {
		c.Work = &specWork{Host: w.Host, Session: w.Session, Window: w.Window, Dir: w.Dir}
	}
	if v := tk.Verify; v != nil {
		c.Verify = &specVerify{State: v.State, Exit: v.Exit, Note: v.Note, Tail: v.Tail, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt}
		if v.Held != "" {
			held := v.Held
			c.Verify.Held = &held
		}
	}
	for _, a := range tk.Attachments {
		c.Attachments = append(c.Attachments, specAttachment(a))
	}
	for _, e := range tk.History {
		c.History = append(c.History, specEvent(e))
	}
	return c
}

func (m specMove) card() cardMove {
	out := cardMove{kind: specKinds[m.Kind], by: m.By, reason: m.Reason, window: m.Window,
		dispatched: m.Dispatched, names: m.Names, name: m.Name, harness: m.Harness, refresh: m.Refresh,
		work: m.Work.task(), verifyRun: m.VerifyRun, token: m.Token, verify: m.Verify.task(),
		note: m.Note, attachments: leanAttachments(m.Attachments)}
	if m.Stage != nil {
		out.stage = *m.Stage
	}
	if m.Kind == "auto" {
		out.auto = taskMove{from: m.AutoFrom}
		if m.AutoTo != nil {
			out.auto.to = *m.AutoTo
		}
	}
	return out
}

// leanRefusal names the refusal of a claim the way the model does.
func leanRefusal(verr *verbError) *string {
	if verr == nil {
		return nil
	}
	name := "done"
	switch {
	case verr.Code == ErrVerbTaskHeld:
		name = "held"
	case verr.Code == ErrVerbTaskDone:
		name = "done"
	case verr.Hint != nil && verr.Hint.Param == "window":
		name = "no_window"
	case verr.Hint != nil && verr.Hint.Command != "":
		name = "blocked"
	}
	return &name
}

// leanRunGo runs a stream through TaskState.apply.
func leanRunGo(s cardStream) []cardStep {
	tk := s.Card.task()
	out := make([]cardStep, 0, len(s.Moves))
	for _, m := range s.Moves {
		action, holder, verr := tk.apply(m.card(), m.Now)
		out = append(out, cardStep{Action: action, Holder: holder, Refusal: leanRefusal(verr), Card: leanCardOf(&tk)})
	}
	return out
}

// The random streams. The pools are small, so names, windows and times
// collide often, which is where the rules branch.
var (
	specWindows  = []string{"", "w1", "w2"}
	specBys      = []string{taskByDaemon, "person", "w1", taskByDispatch}
	specReasons  = []string{"", taskBlockErrored, taskBlockErrored + ": exit 1", "waiting on the key", taskReleaseRestart, "a.txt", "b.txt"}
	specNames    = []string{"a.txt", "b.txt", "PR #1"}
	specStates   = []string{VerifyRunning, VerifyPassed, VerifyFailed}
	specKindList = []string{"claim", "release", "block", "unblock", "block_errored", "unblock_errored", "hand_in", "finish", "stage", "auto", "verified", "attach", "detach"}
)

func leanPick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

func leanGen(r *rand.Rand) cardStream {
	t := int64(1_700_000_000_000_000_000) + r.Int64N(1_000_000)
	times := []int64{0, t}
	pickTime := func() int64 { return leanPick(r, times) }
	str := func(xs ...string) string { return leanPick(r, xs) }
	stage := func() *string { s := leanPick(r, TaskStages); return &s }
	work := func() *specWork {
		if r.IntN(2) == 0 {
			return nil
		}
		return &specWork{Host: str("", "build"), Session: str("", "s1"), Window: leanPick(r, specWindows), Dir: str("", "/r")}
	}
	verify := func() *specVerify {
		if r.IntN(3) == 0 {
			return nil
		}
		v := &specVerify{State: leanPick(r, specStates), Note: str("", "timed out"), Tail: str("", "ok"), StartedAt: pickTime(), FinishedAt: pickTime()}
		if r.IntN(2) == 0 {
			e := r.IntN(2)
			v.Exit = &e
		}
		if r.IntN(4) == 0 {
			v.Held = stage()
		}
		return v
	}
	attachments := func(n int) []specAttachment {
		out := []specAttachment{}
		for range n {
			out = append(out, specAttachment{Name: leanPick(r, specNames), Kind: str("file", "link"), Size: r.Int64N(3), Note: str("", "n"), By: leanPick(r, specBys), At: pickTime()})
		}
		return out
	}

	c := specCard{Stage: leanPick(r, TaskStages), StageAt: pickTime(), Holder: leanPick(r, specWindows), HeldAt: pickTime(),
		HolderName: str("", "n"), HolderHarness: str("", "claude"), Dispatched: r.IntN(2) == 0,
		Attempts: r.IntN(3), Note: str("", "x"), Work: work(), Verify: verify(),
		Attachments: attachments(r.IntN(3)), History: []specEvent{}}
	if r.IntN(2) == 0 {
		c.Blocked = &specBlock{Reason: leanPick(r, specReasons), By: leanPick(r, specBys), At: pickTime()}
	}
	if c.Verify != nil {
		times = append(times, c.Verify.StartedAt)
	}
	nh := r.IntN(3)
	if r.IntN(20) == 0 {
		nh = taskMaxHistory - 3 + r.IntN(5)
	}
	for i := range nh {
		c.History = append(c.History, specEvent{Action: TaskActionStaged, By: "person", At: int64(i), Stage: leanPick(r, TaskStages)})
	}

	moves := make([]specMove, 1+r.IntN(8))
	for i := range moves {
		t += r.Int64N(3)
		m := specMove{Kind: leanPick(r, specKindList), By: leanPick(r, specBys), Reason: leanPick(r, specReasons),
			Window: leanPick(r, specWindows), Dispatched: r.IntN(2) == 0, Names: r.IntN(2) == 0,
			Name: str("", "far"), Harness: str("", "codex"), Refresh: r.IntN(2) == 0,
			AutoFrom: []string{}, Attachments: []specAttachment{}, Now: t}
		switch m.Kind {
		case "hand_in":
			s := str(TaskStageVerify, TaskStageReview, TaskStageVerify, leanPick(r, TaskStages))
			m.Stage, m.Work = &s, work()
		case "stage":
			m.Stage, m.Work, m.VerifyRun = stage(), work(), r.IntN(2) == 0
		case "auto":
			for _, s := range TaskStages {
				if r.IntN(3) == 0 {
					m.AutoFrom = append(m.AutoFrom, s)
				}
			}
			m.AutoTo = stage()
		case "verified":
			s := str(TaskStageTodo, TaskStageReview, TaskStageVerify, leanPick(r, TaskStages))
			m.Stage, m.Token, m.Verify, m.Note = &s, pickTime(), verify(), str("", "exit 1")
		case "block_errored":
			m.Note = str("", "the agent said no")
		case "attach":
			m.Attachments = attachments(1 + r.IntN(3))
		case "detach":
			m.Reason = str(leanPick(r, specNames), "c.txt")
		}
		moves[i] = m
		times = append(times, t)
	}
	return cardStream{Card: c, Moves: moves}
}

// leanModel is one running copy of the model's executable.
type leanModel struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	stderr *bytes.Buffer
	waited sync.Once
}

func startLeanModel(t testing.TB, bin string) *leanModel {
	t.Helper()
	cmd := exec.Command(bin)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Kept for the error of a model that stops: one stopped after a
	// difference only reports a broken pipe.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the Lean model %s: %v. Build it with: cd spec/tasks && lake build", bin, err)
	}
	m := &leanModel{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20), stderr: &stderr}
	t.Cleanup(func() { _ = in.Close(); m.wait() })
	return m
}

// wait waits for the model to exit, once.
func (m *leanModel) wait() { m.waited.Do(func() { _ = m.cmd.Wait() }) }

func (m *leanModel) read() (specAnswer, error) {
	line, err := m.out.ReadBytes('\n')
	if err != nil {
		_ = m.cmd.Process.Kill()
		m.wait()
		return specAnswer{}, fmt.Errorf("read the Lean model: %w: %s", err, m.stderr.Bytes())
	}
	var a specAnswer
	if err := json.Unmarshal(line, &a); err != nil {
		return specAnswer{}, fmt.Errorf("decode the Lean model's line %q: %w", line, err)
	}
	if a.Error != "" {
		return a, fmt.Errorf("the Lean model refused a stream: %s", a.Error)
	}
	return a, nil
}

func leanEncode(s cardStream) []byte {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

// run asks the model for one stream.
func (m *leanModel) run(s cardStream) ([]cardStep, error) {
	if _, err := m.in.Write(leanEncode(s)); err != nil {
		return nil, err
	}
	a, err := m.read()
	return a.Steps, err
}

// leanFirstDiff is the index of the first step where Go and the model differ, or
// -1, with both steps as JSON.
func leanFirstDiff(goSteps, leanSteps []cardStep) (int, string, string) {
	for i := range max(len(goSteps), len(leanSteps)) {
		var g, l []byte
		if i < len(goSteps) {
			g, _ = json.Marshal(goSteps[i])
		}
		if i < len(leanSteps) {
			l, _ = json.Marshal(leanSteps[i])
		}
		if !bytes.Equal(g, l) {
			return i, string(g), string(l)
		}
	}
	return -1, "", ""
}

// leanShrink cuts the stream to the first difference and then drops every move
// it can while the two still differ.
func leanShrink(t *testing.T, m *leanModel, s cardStream, at int) cardStream {
	differs := func(s cardStream) bool {
		l, err := m.run(s)
		if err != nil {
			t.Fatal(err)
		}
		i, _, _ := leanFirstDiff(leanRunGo(s), l)
		return i >= 0
	}
	s.Moves = s.Moves[:at+1]
	for changed := true; changed; {
		changed = false
		for i := len(s.Moves) - 1; i >= 0 && len(s.Moves) > 1; i-- {
			try := cardStream{Card: s.Card, Moves: append(append([]specMove{}, s.Moves[:i]...), s.Moves[i+1:]...)}
			if differs(try) {
				s, changed = try, true
			}
		}
	}
	return s
}

func TestTaskMoveMatchesLeanModel(t *testing.T) {
	bin := *specBin
	if bin == "" {
		// The repository root is two folders up from the package. The spec
		// job of CI runs this test, so the unit test jobs need not run for
		// a change to the model.
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		bin = filepath.Join(filepath.Dir(filepath.Dir(wd)), "spec", "tasks", ".lake", "build", "bin", "taskspec")
	}
	seed := *specSeed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	t.Logf("seed %d, %d streams (rerun with -spec.seed=%d)", seed, *specStreams, seed)
	start := time.Now()

	// Each worker owns one model process and the streams i with i%workers
	// == w. A writer feeds the process while the worker reads its answers,
	// so the pipe never waits on a round trip.
	workers := min(runtime.GOMAXPROCS(0), 8)
	type failure struct {
		index      int
		stream     cardStream
		step       int
		goS, leanS string
	}
	var (
		mu    sync.Mutex
		first *failure
		wg    sync.WaitGroup
		// seen counts, for each move kind, the moves that changed the
		// card, the ones that changed nothing and the refused claims.
		seen = map[string]*[3]int{}
	)
	count := func(s cardStream, steps []cardStep) {
		mu.Lock()
		defer mu.Unlock()
		for i, st := range steps {
			c := seen[s.Moves[i].Kind]
			if c == nil {
				c = &[3]int{}
				seen[s.Moves[i].Kind] = c
			}
			switch {
			case st.Refusal != nil:
				c[2]++
			case st.Action == "":
				c[1]++
			default:
				c[0]++
			}
		}
	}
	streamOf := func(i int) cardStream {
		return leanGen(rand.New(rand.NewPCG(seed, uint64(i))))
	}
	for w := range workers {
		model := startLeanModel(t, bin)
		wg.Add(2)
		go func() {
			defer wg.Done()
			bw := bufio.NewWriterSize(model.in, 1<<20)
			for i := w; i < *specStreams; i += workers {
				if _, err := bw.Write(leanEncode(streamOf(i))); err != nil {
					return
				}
			}
			_ = bw.Flush()
		}()
		go func() {
			defer wg.Done()
			for i := w; i < *specStreams; i += workers {
				a, err := model.read()
				if err != nil {
					t.Error(err)
					_ = model.cmd.Process.Kill()
					return
				}
				s := streamOf(i)
				goSteps := leanRunGo(s)
				count(s, goSteps)
				if at, g, l := leanFirstDiff(goSteps, a.Steps); at >= 0 {
					mu.Lock()
					if first == nil || i < first.index {
						first = &failure{i, s, at, g, l}
					}
					mu.Unlock()
					// Stop the model so the writer is not left blocked on
					// a pipe no one reads.
					_ = model.cmd.Process.Kill()
					return
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("%d streams in %s", *specStreams, time.Since(start).Round(time.Millisecond))
	for _, k := range specKindList {
		if c := seen[k]; c != nil {
			t.Logf("%-16s changed %7d  unchanged %7d  refused %7d", k, c[0], c[1], c[2])
		}
	}
	if first == nil {
		return
	}
	model := startLeanModel(t, bin)
	short := leanShrink(t, model, first.stream, first.step)
	l, err := model.run(short)
	if err != nil {
		t.Fatal(err)
	}
	at, g, lean := leanFirstDiff(leanRunGo(short), l)
	t.Fatalf("TaskState.apply and the Lean model differ: seed %d, stream %d, step %d of the shortest stream (%d moves).\n"+
		"stream: %s\ngo:   %s\nlean: %s", seed, first.index, at, len(short.Moves), bytes.TrimSpace(leanEncode(short)), g, lean)
}
