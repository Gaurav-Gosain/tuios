package agentproto

import (
	"context"
	"maps"
	"math"
	"strconv"
	"strings"

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
	// ReportActivity reports one activity. It changes no state: the state
	// part it carries applies only to a pane already working.
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

// metaFeed sends metadata on its own goroutine, so a slow daemon never holds
// the pane. Only the newest values wait: a burst of updates becomes one call,
// and each call sends only the keys that changed since the last one sent.
type metaFeed struct {
	r    MetaReporter
	next chan map[string]string
	done chan struct{}
}

func newMetaFeed(r MetaReporter) *metaFeed {
	f := &metaFeed{r: r, next: make(chan map[string]string, 1), done: make(chan struct{})}
	go f.run()
	return f
}

// push hands the feed the pane's metadata as it stands now.
func (f *metaFeed) push(all map[string]string) {
	snap := maps.Clone(all)
	for {
		select {
		case f.next <- snap:
			return
		default:
		}
		// Drop the older values waiting, if they are still there.
		select {
		case <-f.next:
		default:
		}
	}
}

func (f *metaFeed) run() {
	sent := map[string]string{}
	for {
		select {
		case <-f.done:
			return
		case all := <-f.next:
			diff := map[string]string{}
			for k, v := range all {
				if sent[k] != v {
					diff[k] = v
				}
			}
			if len(diff) == 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
			err := f.r.SetMeta(ctx, diff)
			cancel()
			if err == nil {
				maps.Copy(sent, diff)
			}
		}
	}
}

func (f *metaFeed) stop() { close(f.done) }
