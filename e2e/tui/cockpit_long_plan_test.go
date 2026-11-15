package tuie2e

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// longPlan is a plan the way Claude Code writes one: Markdown, with the lines
// of a paragraph broken at the agent's own width, and long enough that the
// card shows it a window at a time.
func longPlan() string {
	var b strings.Builder
	b.WriteString("# Cache the fib table\n\n## Context\n\nThe table is built\nfresh on each call.\nKeep it in `fibCache` for the\nlife of the process.\n\n## Steps\n\n")
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "%d. Change `file%d.go` so the table holds row %d.\n", i, i, i)
	}
	b.WriteString("\n## Done when\n\nEvery test passes. PLAN-END-MARK\n")
	return b.String()
}

// planShown is the card's "Lines A-B of N".
var planShown = regexp.MustCompile(`(?i)lines (\d+)-(\d+) of (\d+)`)

func planWindow(s tuitest.Screen) (first, last, total int) {
	m := planShown.FindStringSubmatch(s.Text())
	if m == nil {
		return 0, 0, 0
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	n, _ := strconv.Atoi(m[3])
	return a, b, n
}

// TestCockpitLongPlanReadsByPageAsMarkdown holds a 70 line plan and reads it
// in the agents view. The plan is drawn as Markdown: no "#" or backtick
// shows, and a paragraph the agent broke at its own width is one line. A
// page key moves the plan a page, not the list, so the end is a few presses
// away; g and G go to its start and end; Z gives the card the whole focus.
// The plan, read to its end, is approved with the hook's allow.
//
// Negative control: on main, pgdown moves the list cursor and the plan never
// reaches its end, and the card shows "## Context" raw.
func TestCockpitLongPlanReadsByPageAsMarkdown(t *testing.T) {
	base, term := approvalsClient(t)
	input, err := json.Marshal(map[string]any{"plan": longPlan(), "planFilePath": "/tmp/plan.md"})
	if err != nil {
		t.Fatal(err)
	}
	hook := startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-long-plan","permission_mode":"plan","tool_name":"ExitPlanMode","tool_input":`+string(input)+`}`)
	dir := artifactDir(t)

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, n := planWindow(s)
		return screenHas(s, "Approve the plan?", "Context") && n > 0
	}, uiTimeout); err != nil {
		t.Fatalf("the agents view never showed the plan: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "long-plan-top")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "long-plan-top")
	text := term.Screen().Text()
	for _, raw := range []string{"## Context", "# Cache", "`fibCache`"} {
		if strings.Contains(text, raw) {
			t.Errorf("ASSERTION: the plan shows raw Markdown %q:\n%s", raw, term.Snapshot())
		}
	}
	if !strings.Contains(text, "The table is built fresh on each call.") {
		t.Errorf("ASSERTION: a paragraph broken at the agent's width is not one line:\n%s", term.Snapshot())
	}
	first, last, total := planWindow(term.Screen())
	window := last - first + 1
	if total < 60 {
		t.Fatalf("the plan is %d lines, want the whole of it counted:\n%s", total, term.Snapshot())
	}
	pause(t, "the view takes no answer key in the moment after a card is shown", answerSettle)

	// pgdown reads the plan a page at a time: the end is within the pages
	// the plan has, and the list cursor stays on the plan.
	pages := (total+window-2)/max(window-1, 1) + 1
	reached := false
	for range pages {
		if err := term.SendKeys(tuitest.PageDown); err != nil {
			t.Fatalf("pgdown: %v", err)
		}
		if term.WaitForText("PLAN-END-MARK", time.Second) == nil {
			reached = true
			break
		}
	}
	if !reached {
		t.Fatalf("ASSERTION: %d presses of pgdown did not reach the end of a %d line plan in a %d line window:\n%s", pages, total, window, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "Approve the plan?") {
		t.Fatalf("ASSERTION: pgdown left the plan:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "long-plan-end")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "long-plan-end")

	// g goes back to the start, and G to the end in one press.
	if err := term.SendKeys("g"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		a, _, _ := planWindow(s)
		return a == 1 && !strings.Contains(s.Text(), "PLAN-END-MARK")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: g did not go to the plan's start: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("G"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("PLAN-END-MARK", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: G did not go to the plan's end: %v\n%s", err, term.Snapshot())
	}

	// Z gives the card the whole focus: more of the plan shows at once.
	if err := term.SendKeys("g", "Z"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		a, b, _ := planWindow(s)
		return a == 1 && b-a+1 > window
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: Z did not show more of the plan than the %d line window: %v\n%s", window, err, term.Snapshot())
	}
	saveFrame(t, term, "long-plan-zoomed")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "long-plan-zoomed")
	if err := term.SendKeys("Z"); err != nil {
		t.Fatal(err)
	}

	// Read to its end, the plan is approved.
	if err := term.SendKeys("a"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-hook.exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-hook.exited:
			if err != nil {
				t.Fatalf("the hook failed: %v", err)
			}
		case <-time.After(uiTimeout):
			t.Fatalf("the plan was never approved:\n%s", term.Snapshot())
		}
	}
	if out := hook.stdout.String(); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("the hook printed %q, want Claude Code's allow", out)
	}
	alive(t, term, "after approving a long plan")
}
