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
	// MetaContextTokens and MetaContextWindow are the context's tokens and
	// its window, as integration names them for the status line feed.
	MetaContextTokens = "context_tokens"
	MetaContextWindow = "context_window"
)

const maxActivityText = 200

func metaFromUsage(u Usage) map[string]string {
	out := map[string]string{}
	if m := oneLine(u.Model); m != "" {
		out[MetaModel] = m
	}
	if u.ContextSize > 0 && u.ContextUsed >= 0 {
		pct := math.Min(100, float64(u.ContextUsed)*100/float64(u.ContextSize))
		out[MetaContext] = strconv.Itoa(int(math.Round(pct))) + "%"
		out[MetaContextTokens] = strconv.FormatInt(u.ContextUsed, 10)
		out[MetaContextWindow] = strconv.FormatInt(u.ContextSize, 10)
	}
	if u.ClearCost {
		out[MetaCost] = ""
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

func clip(s string) string {
	return ansi.Truncate(oneLine(clean(s)), maxActivityText, "...")
}

type feed struct {
	meta   MetaReporter
	act    ActivityReporter
	wake   chan struct{}
	done   chan struct{}
	exited chan struct{}

	mu         sync.Mutex
	want       map[string]string
	acts       []Activity
	forget     bool
	retry      *time.Timer
	retryAfter time.Duration
}

const maxQueuedActivity = 64

const (
	feedRetryAfter = 5 * time.Second
	feedRetries    = 3
)

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

func (f *feed) setMeta(all map[string]string) {
	if f.meta == nil {
		return
	}
	f.mu.Lock()
	f.want = maps.Clone(all)
	f.mu.Unlock()
	f.poke()
}

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
