package agentproto

import (
	"context"
	"maps"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// What the pane reports about its agent beyond its state: the model, the
// context use, the cost and the plan's progress as agent metadata, and each
// tool call and turn as activity. Both are display only at the daemon, and
// both are optional parts of a Reporter: one that does not implement them is
// sent neither.

// MetaReporter is a Reporter that also writes the pane's agent metadata.
type MetaReporter interface {
	// SetMeta sets metadata keys for the pane. A key missing from tokens is
	// left as it is.
	SetMeta(ctx context.Context, tokens map[string]string) error
}

// Activity is one thing the agent did, in the words of set-agent-state's
// activity parameter.
type Activity struct {
	// Event is prompt, tool, tool_done, tool_failed or turn_end.
	Event string
	// Tool and Target name a tool call and what it acts on.
	Tool, Target string
	// Text is a prompt's or a finished turn's first line.
	Text string
	// OK is whether a finished tool call succeeded, nil when unknown.
	OK *bool
	// State is the state the pane last reported for itself: working during
	// a turn, and the turn's end state for turn_end. The report puts it back
	// only on a pane whose state is none, one that lost its state mid-turn.
	// Empty means working.
	State string
}

// Activity events.
const (
	ActivityPrompt     = "prompt"
	ActivityTool       = "tool"
	ActivityToolDone   = "tool_done"
	ActivityToolFailed = "tool_failed"
	ActivityTurnEnd    = "turn_end"
)

// ActivityReporter is a Reporter that also reports activity.
type ActivityReporter interface {
	// ReportActivity reports one activity. It changes the state of no pane
	// that has one: its state part is a.State if the pane's state is none,
	// so the daemon records the activity and refuses the state part on
	// every pane that kept its state.
	ReportActivity(ctx context.Context, a Activity) error
}

// Metadata keys a protocol pane writes.
const (
	MetaModel   = "model"
	MetaContext = "context"
	MetaCost    = "cost"
	MetaPlan    = "plan"
)

// maxActivityText bounds the text of one activity. The daemon cleans and
// cuts it again; this only keeps a long command from riding every report.
const maxActivityText = 200

// metaFromUsage is the metadata a Usage states, formatted for the rail.
func metaFromUsage(u Usage) map[string]string {
	out := map[string]string{}
	if m := oneLine(u.Model); m != "" {
		out[MetaModel] = m
	}
	if u.ContextSize > 0 && u.ContextUsed >= 0 {
		pct := math.Min(100, float64(u.ContextUsed)*100/float64(u.ContextSize))
		out[MetaContext] = strconv.Itoa(int(math.Round(pct))) + "%"
	}
	if u.HasCost && u.Cost >= 0 && !math.IsNaN(u.Cost) && !math.IsInf(u.Cost, 0) {
		amount := strconv.FormatFloat(u.Cost, 'f', 2, 64)
		switch cur := strings.ToUpper(strings.TrimSpace(u.Currency)); cur {
		case "", "USD":
			out[MetaCost] = "$" + amount
		default:
			out[MetaCost] = amount + " " + cur
		}
	}
	return out
}

// planProgress is a plan as done of total, "" for an empty plan.
func planProgress(p Plan) string {
	if len(p.Entries) == 0 {
		return ""
	}
	done := 0
	for _, e := range p.Entries {
		if e.Status == "completed" {
			done++
		}
	}
	return strconv.Itoa(done) + "/" + strconv.Itoa(len(p.Entries))
}

// activityTool is a tool call as an activity's tool and target: the kind in
// the words the hooks use for the same thing (Bash, Edit, Read), and the
// command, the files or the title.
func activityTool(t Tool) (tool, target string) {
	switch t.Kind {
	case "execute":
		tool = "Bash"
	case "edit":
		tool = "Edit"
	case "read":
		tool = "Read"
	case "delete":
		tool = "Delete"
	case "move":
		tool = "Move"
	case "search":
		tool = "Search"
	case "fetch":
		tool = "Fetch"
	case "think":
		tool = "Think"
	default:
		tool = "Tool"
	}
	switch {
	case t.Kind == "execute" && t.Input["command"] != "":
		target = t.Input["command"]
	case len(t.Diffs) > 0:
		paths := make([]string, 0, len(t.Diffs))
		for _, d := range t.Diffs {
			paths = append(paths, d.Path)
		}
		target = strings.Join(paths, ", ")
	default:
		target = t.Title
	}
	return tool, clip(target)
}

// clip is s on one line, cut to maxActivityText cells.
func clip(s string) string {
	return ansi.Truncate(oneLine(clean(s)), maxActivityText, "...")
}

// feed sends metadata and activity on a goroutine of its own, so a slow
// daemon never holds the pane's input and render loop.
//
// Activity waits in order, at most maxQueuedActivity of it: when the daemon
// falls that far behind, the oldest is dropped. Metadata waits as the pane's
// values as they stand now, so a burst of updates becomes one call, and each
// call sends only the keys that changed since the last one the daemon took.
//
// What was sent is forgotten when a call fails and when resync is called, and
// then every key goes again. A failed call is tried again after
// feedRetryAfter, up to feedRetries times in a row, and the Session calls resync at each turn start, so values
// the daemon lost (a restart, or a clear to none) come back without waiting
// for the agent to change them.
type feed struct {
	meta MetaReporter
	act  ActivityReporter
	wake chan struct{}
	done chan struct{}
	// exited is closed when run returns.
	exited chan struct{}

	mu   sync.Mutex
	want map[string]string
	acts []Activity
	// forget says the next pass sends every key, not only the changed ones.
	forget bool
	// retry is set while a failed metadata call waits to be tried again,
	// retryAfter later.
	retry      *time.Timer
	retryAfter time.Duration
}

// maxQueuedActivity bounds the activity waiting for a slow daemon.
const maxQueuedActivity = 64

// feedRetryAfter is how long a failed metadata call waits before it is tried
// again, and feedRetries how many times in a row. After that the values go
// with the next change or the next turn.
const (
	feedRetryAfter = 5 * time.Second
	feedRetries    = 3
)

// newFeed starts a feed for whichever of meta and act is not nil. It returns
// nil when both are. retryAfter is feedRetryAfter when zero.
func newFeed(meta MetaReporter, act ActivityReporter, retryAfter time.Duration) *feed {
	if meta == nil && act == nil {
		return nil
	}
	if retryAfter <= 0 {
		retryAfter = feedRetryAfter
	}
	f := &feed{meta: meta, act: act, wake: make(chan struct{}, 1), done: make(chan struct{}), exited: make(chan struct{}), retryAfter: retryAfter}
	go f.run()
	return f
}

func (f *feed) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// setMeta hands the feed the pane's metadata as it stands now.
func (f *feed) setMeta(all map[string]string) {
	if f.meta == nil {
		return
	}
	f.mu.Lock()
	f.want = maps.Clone(all)
	f.mu.Unlock()
	f.poke()
}

// activity queues one activity.
func (f *feed) activity(a Activity) {
	if f.act == nil {
		return
	}
	f.mu.Lock()
	if len(f.acts) >= maxQueuedActivity {
		f.acts = f.acts[1:]
	}
	f.acts = append(f.acts, a)
	f.mu.Unlock()
	f.poke()
}

// resync makes the next pass send every metadata key again.
func (f *feed) resync() {
	f.mu.Lock()
	f.forget = true
	f.mu.Unlock()
	f.poke()
}

func (f *feed) run() {
	defer close(f.exited)
	sent := map[string]string{}
	failures := 0
	for {
		select {
		case <-f.done:
			return
		case <-f.wake:
		}
		f.mu.Lock()
		acts := f.acts
		f.acts = nil
		want := f.want
		if f.forget {
			clear(sent)
			f.forget = false
		}
		f.mu.Unlock()
		for _, a := range acts {
			select {
			case <-f.done:
				return
			default:
			}
			ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
			_ = f.act.ReportActivity(ctx, a)
			cancel()
		}
		diff := map[string]string{}
		for k, v := range want {
			if sent[k] != v {
				diff[k] = v
			}
		}
		if len(diff) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
		err := f.meta.SetMeta(ctx, diff)
		cancel()
		if err == nil {
			maps.Copy(sent, diff)
			failures = 0
			continue
		}
		// The daemon may have taken some of it, or be gone: send it all
		// next time, and try again even if nothing changes.
		clear(sent)
		failures++
		if failures > feedRetries {
			continue
		}
		f.mu.Lock()
		if f.retry == nil {
			f.retry = time.AfterFunc(f.retryAfter, func() {
				f.mu.Lock()
				f.retry = nil
				f.mu.Unlock()
				f.poke()
			})
		}
		f.mu.Unlock()
	}
}

// stop ends the feed. What is still waiting is not sent.
func (f *feed) stop() {
	close(f.done)
	<-f.exited
	f.mu.Lock()
	if f.retry != nil {
		f.retry.Stop()
		f.retry = nil
	}
	f.mu.Unlock()
}
