package tuie2e

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuitest"
)

// subagentRowsScenario is what the fake claude replays for the rail: four
// background subagents with a tool call each and a Stop that leaves the main
// agent done, then two of them ending (one done, one stopped), then the
// other two done.
func subagentRowsScenario() string {
	type launch struct{ id, call, desc, typ, tool, input string }
	launches := []launch{
		{saTmux, callT, "Research tmux", "general-purpose", "Bash", `{"command":"go test ./...","description":"Run the tests"}`},
		{saRail, callR, "Audit the rail", "general-purpose", "Read", `{"file_path":"internal/app/render_sidebar.go"}`},
		{saDocs, callD, "Write the docs", "general-purpose", "Edit", `{"file_path":"AGENTS.md","old_string":"a","new_string":"b"}`},
		{saCI, callCI, "Check CI", "Explore", "Bash", `{"command":"gh run list","description":"List the runs"}`},
	}
	lines := []string{
		cc("", `"hook_event_name":"SessionStart","source":"startup"`),
		cc("", `"hook_event_name":"UserPromptSubmit","prompt":"Look at tmux, the rail, the docs and CI in parallel."`),
	}
	var tasks []string
	for _, l := range launches {
		lines = append(lines,
			agentLaunch(l.call, l.desc, l.typ, true),
			subagentStartOf(l.id, l.typ),
			cc("", `"hook_event_name":"PostToolUse","tool_name":"Agent","tool_input":{"description":"`+l.desc+`","prompt":"Do it, then reply done.","subagent_type":"`+l.typ+`","run_in_background":true},"tool_response":{"isAsync":true,"status":"async_launched","agentId":"`+l.id+`","description":"`+l.desc+`","prompt":"Do it, then reply done."},"tool_use_id":"`+l.call+`","duration_ms":3`))
		tasks = append(tasks, task(l.id, l.desc, l.typ))
	}
	for i, l := range launches {
		lines = append(lines, "& "+subagentTool(l.id, "toolu_01t"+strconv.Itoa(i), l.tool, l.input, false))
	}
	lines = append(lines, "join",
		cc("", `"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Handed the work to four agents.","background_tasks":[`+strings.Join(tasks, ",")+`],"session_crons":[]`),
		"wait running",
		subagentTool(saTmux, "toolu_01t0", "Bash", launches[0].input, true),
		subagentStopOf(saTmux, "general-purpose", "tmux keeps every pane in one server.", ""),
		cc("", `"hook_event_name":"UserPromptSubmit","prompt":"Stop the docs agent."`),
		cc("", `"hook_event_name":"PreToolUse","tool_name":"TaskStop","tool_input":{"task_id":"`+saDocs+`"},"tool_use_id":"toolu_01st"`),
		subagentStopOf(saDocs, "general-purpose", "", ""),
		cc("", `"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Stopped the docs agent.","background_tasks":[`+tasks[1]+","+tasks[3]+`],"session_crons":[]`),
		"wait two-ended",
		subagentStopOf(saRail, "general-purpose", "The rail is fine.", ""),
		subagentStopOf(saCI, "Explore", "CI is green.", ""),
		"wait finished",
	)
	return strings.Join(lines, "\n") + "\n"
}

// railOf is the rail's part of each screen line: the text before the rail's
// right border.
func railOf(s tuitest.Screen) []string {
	_, rows := s.Size()
	out := make([]string, 0, rows)
	for y := range rows {
		line := s.Line(y)
		if i := strings.Index(line, "│"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return out
}

// subagentRail is what the agents section of one frame shows.
type subagentRail struct {
	// parents are the agent rows on screen, by name.
	parents []string
	// kids are the subagents' rows, by the start of their description.
	kids []string
	// notes is how many subagent rows have a second line under them.
	notes int
	// more is the "+N more" or "N subagents" row, empty when there is none.
	more string
	// overflow says the section hid rows behind "…+N".
	overflow bool
	lines    []string
}

// subagentDescs are the scenario's descriptions, as far as a 24 column rail
// draws them.
var subagentDescs = []string{"Research", "Audit", "Write", "Check"}

// readSubagentRail parses the agents section of a frame.
func readSubagentRail(s tuitest.Screen) subagentRail {
	var r subagentRail
	lines := railOf(s)
	at := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "agents") })
	if at < 0 {
		return r
	}
	lastKid := false
	for _, l := range lines[at+1:] {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "files") || strings.HasPrefix(l, "───") {
			break
		}
		r.lines = append(r.lines, l)
		switch {
		case strings.Contains(t, "lead") && !strings.HasPrefix(l, "    "):
			r.parents = append(r.parents, "lead")
			lastKid = false
		case strings.Contains(t, "solo") && !strings.HasPrefix(l, "    "):
			r.parents = append(r.parents, "solo")
			lastKid = false
		case strings.HasPrefix(l, "      ") && (strings.HasPrefix(t, "+") || strings.HasSuffix(t, "subagents")):
			r.more = t
			lastKid = false
		case strings.HasPrefix(l, "    ") && len([]rune(t)) > 2 && slices.ContainsFunc(subagentDescs, func(d string) bool {
			return strings.HasPrefix(string([]rune(t)[2:]), d)
		}):
			r.kids = append(r.kids, string([]rune(t)[2:]))
			lastKid = true
		case strings.HasPrefix(l, "      ") && lastKid:
			r.notes++
			lastKid = false
		case strings.Contains(t, "+") && strings.Contains(t, "…"):
			r.overflow = true
		default:
			lastKid = false
		}
	}
	return r
}

// step names the collapse step a frame shows, with total and running the
// subagents the lead has, or "" for a frame without both agent rows.
func (r subagentRail) step(total, running int) string {
	if len(r.parents) != 2 {
		return ""
	}
	h := "short"
	if r.notes > 0 {
		h = "tall"
	}
	switch {
	case len(r.kids) == total && r.more == "":
		return "all-" + h
	case len(r.kids) == running && running < total && r.more == "":
		return "running-" + h
	case r.more != "":
		return "some"
	case len(r.kids) == 0:
		return "none"
	}
	return "other"
}

// TestSubagentRowsOnTheRail drives a stand-in claude, which replays Claude
// Code's hook payloads through the real hook, under a real client with the
// rail at 24 columns, beside a second agent pane at rest, and holds the rail's
// subagent rows to the design:
//
//   - the lead, done with four subagents at work, wears the waiting mark (◊)
//     on its rail row and on its title bar, and each subagent has a row under
//     it, a second line with its tool when the section is tall;
//   - as the terminal shrinks the section steps down: every row tall, every
//     row short, the running ones only (once two have ended), a few rows and
//     "+N more", and no rows with the count on the lead's row. At no height
//     does a subagent row show while either agent row is hidden;
//   - a finished row goes when the daemon prunes it;
//   - ASCII and 16 colours keep the marks apart by shape;
//   - subagent_rows = "count" draws the count alone.
//
// It saves the frame of each step, as text and styled text, and a PNG drawn
// by tuios's own renderer of the tall frame, a "+N more" frame and the ASCII
// 16 colour frame, under TUIOS_E2E_FRAMES.
//
// Negative controls are in NEGATIVE_CONTROLS.md, "Subagent rows".
func TestSubagentRowsOnTheRail(t *testing.T) {
	log := &stateLog{name: "subagent-rows"}
	defer log.save(t)
	// Long enough that the finished rows stay through a whole shrink.
	t.Setenv("TUIOS_SUBAGENT_FADE_SECONDS", "25")
	frames := os.Getenv("TUIOS_E2E_FRAMES")
	base := t.TempDir()
	// The looks as they ship, so the config can be rewritten mid-test without
	// the suite's pins going missing under a live client.
	useShippedLooks(base)
	rail := "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 24\n"
	writeConfig(t, base, rail)
	killDaemon(t, base)
	for _, name := range []string{"e2e-sub", "e2e-solo"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	for _, s := range []struct{ session, name string }{{"e2e-sub", "lead"}, {"e2e-solo", "solo"}} {
		if out, err := tuiosCLI(t, base, "set-window", "-s", s.session, "-w", "0", "--name", s.name); err != nil {
			t.Fatalf("name %s: %v\n%s", s.session, err, out)
		}
	}
	dir := workDirIn(t, base)
	// The second agent sits idle, so it sorts with the lead, after it: a
	// collapse that took a parent's line for a child row would push it off.
	for _, payload := range []string{
		`{"hook_event_name":"SessionStart","session_id":"solo-1","source":"startup"}`,
	} {
		explained, err := subagentHook(base, dir, "e2e-solo", payload)
		log.add("%s", explained)
		if err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(bin, "claude")
	if err := os.WriteFile(claude, []byte(strings.ReplaceAll(fakeClaudeScript, "__TUIOS__", tuiosBin)), 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(base, "scenario.txt")
	if err := os.WriteFile(scenario, []byte(subagentRowsScenario()), 0o644); err != nil {
		t.Fatal(err)
	}
	hookLog := filepath.Join(base, "hooks.log")
	step := func() {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", "\n"); err != nil {
			t.Fatalf("step the fake claude: %v\n%s", err, out)
		}
	}

	const cols, tallRows = 120, 50
	term := attachIn(t, base, "e2e-sub", startOpts{cols: cols, rows: tallRows})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-sub", "-w", "0", claude+" "+scenario+" "+hookLog+"\n"); err != nil {
		t.Fatalf("start the fake claude: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT running")
	waitSubagents(t, base, "e2e-sub", log, "running", func(s subagentsOf) bool {
		return s.Subagents == 4 && s.byID(saCI).Now == "Bash: gh run list" && s.byID(saTmux).Now == "Bash: go test ./..."
	})

	// The lead's row and its title bar wear the waiting mark, and every
	// subagent has a tall row.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := readSubagentRail(s)
		return r.step(4, 4) == "all-tall"
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never drew a tall row per subagent: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	leadRow := slices.IndexFunc(railOf(s), func(l string) bool { return strings.Contains(l, "lead") && strings.Contains(l, "◊") })
	if leadRow < 0 {
		t.Fatalf("the lead's rail row does not wear the waiting mark:\n%s", term.Snapshot())
	}
	titled := false
	_, rows := s.Size()
	for y := range rows {
		line := []rune(s.Line(y))
		if len(line) > 30 && strings.Contains(string(line[26:]), "◊") && strings.Contains(string(line[26:]), "lead") {
			titled = true
		}
	}
	if !titled {
		t.Fatalf("the lead's title bar does not wear the waiting mark:\n%s", term.Snapshot())
	}
	if !strings.Contains(strings.Join(railOf(s), "\n"), "Bash: go test") {
		t.Fatalf("the tall rows do not say the tool:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "subagent-rows-all-tall")
	if frames != "" {
		savePNG(t, term.Screen(), shot.XTermPalette(), frames, "subagent-rows-all-tall")
	}

	// Each collapse step, from a tall terminal down to a short one.
	seen := map[string]bool{}
	// listed is how many subagents the daemon lists for the lead now, so a
	// frame drawn after the finished ones faded is not read as one that left
	// them out.
	listed := func() int { return len(readSubagents(t, base, "e2e-sub").List) }
	shrink := func(total, running int, phase string) {
		t.Helper()
		for h := tallRows; h >= 12; h-- {
			if err := term.Resize(cols, h); err != nil {
				t.Fatalf("resize to %d rows: %v", h, err)
			}
			var r subagentRail
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				_, got := s.Size()
				r = readSubagentRail(s)
				return got == h && len(r.lines) > 0
			}, uiTimeout); err != nil {
				continue
			}
			time.Sleep(150 * time.Millisecond)
			r = readSubagentRail(term.Screen())
			if len(r.kids) > 0 && len(r.parents) != 2 {
				t.Fatalf("at %d rows a subagent row shows while an agent row is hidden:\n%s", h, term.Snapshot())
			}
			if len(r.kids) > 0 && r.overflow {
				t.Fatalf("at %d rows the section hides rows behind its overflow mark to show subagent rows:\n%s", h, term.Snapshot())
			}
			st := r.step(total, running)
			if st == "" || st == "other" || listed() != total {
				continue
			}
			key := phase + "-" + st
			if !seen[key] {
				seen[key] = true
				log.add("%-24s at %d rows: %d subagent rows, more %q, %d second lines\n%s", key, h, len(r.kids), r.more, r.notes, strings.Join(r.lines, "\n"))
				saveFrame(t, term, "subagent-rows-"+key)
				if st == "some" && frames != "" {
					savePNG(t, term.Screen(), shot.XTermPalette(), frames, "subagent-rows-"+key)
				}
			}
		}
		if err := term.Resize(cols, tallRows); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			_, got := s.Size()
			return got == tallRows && len(readSubagentRail(s).parents) == 2
		}, uiTimeout); err != nil {
			t.Fatalf("the rail did not come back at %d rows: %v\n%s", tallRows, err, term.Snapshot())
		}
	}
	shrink(4, 4, "running")
	// The shipped layout gives the agents section a share of the rail, and a
	// share in rows is twice the lines once the rows are tall, so a section
	// that holds every row short holds them tall too. With the agents section
	// alone it takes what is left, which holds every row short before it
	// holds them tall.
	writeConfig(t, base, rail+"sections = \"agents\"\n")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(strings.Join(railOf(s), "\n"), "sessions") && len(readSubagentRail(s).parents) == 2
	}, uiTimeout); err != nil {
		t.Fatalf("the rail kept its sections: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "subagent-rows-agents-only")
	shrink(4, 4, "running")
	writeConfig(t, base, rail)
	for _, want := range []string{"running-all-tall", "running-all-short", "running-some", "running-none"} {
		if !seen[want] {
			t.Fatalf("no height showed %s; seen %v", want, seen)
		}
	}

	// Two end: one done, one stopped. Their rows stay, muted, until the fade,
	// and a short section leaves them out first.
	step()
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT two-ended")
	waitSubagents(t, base, "e2e-sub", log, "two ended", func(s subagentsOf) bool {
		return s.Subagents == 2 && len(s.List) == 4
	})
	shrink(4, 2, "ended")
	if !seen["ended-running-short"] {
		t.Fatalf("no height left the finished rows out and kept the running ones; seen %v", seen)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := readSubagentRail(s)
		return len(r.parents) == 2 && len(r.kids) == 2 && !strings.Contains(strings.Join(r.lines, "\n"), "Research")
	}, 45*time.Second); err != nil {
		t.Fatalf("the finished rows never faded: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "subagent-rows-faded")
	log.add("the finished rows faded")

	// ASCII and 16 colours: the marks still differ by shape.
	// A C locale is what turns ASCII on when nobody chose a glyph set, and
	// TERM=xterm without COLORTERM is a 16 colour terminal. Started without
	// attachIn's window count, which reads the dock of one client alone.
	plain := startIn(t, base, startOpts{cols: cols, rows: tallRows, args: []string{"attach", "e2e-sub"},
		env: []string{"TERM=xterm", "COLORTERM=", "LANG=C", "LC_ALL=C", "LC_CTYPE=C"}})
	if err := plain.WaitFor(func(s tuitest.Screen) bool {
		r := readSubagentRail(s)
		text := strings.Join(r.lines, "\n")
		return len(r.parents) == 2 && len(r.kids) == 2 && strings.Contains(text, "% lead") && strings.Contains(text, "* Audit") && !strings.ContainsAny(text, "◊●")
	}, bootTimeout); err != nil {
		t.Fatalf("the ASCII rail does not draw the waiting mark and the running marks: %v\n%s", err, plain.Snapshot())
	}
	saveFrame(t, plain, "subagent-rows-ascii-16")
	if frames != "" {
		savePNG(t, plain.Screen(), shot.XTermPalette(), frames, "subagent-rows-ascii-16")
	}

	// The count alone, as the rail drew it before.
	writeConfig(t, base, rail+"subagent_rows = \"count\"\n")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := readSubagentRail(s)
		return len(r.parents) == 2 && len(r.kids) == 0 && strings.Contains(strings.Join(r.lines, "\n"), "2 subagents")
	}, uiTimeout); err != nil {
		t.Fatalf("subagent_rows = count still draws rows: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "subagent-rows-count")
	writeConfig(t, base, rail)

	// The last two end, and the lead is done with nothing at work.
	step()
	waitCapture(t, base, "e2e-sub", "0", "FAKE-AT finished")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := readSubagentRail(s)
		return len(r.parents) == 2 && !strings.Contains(strings.Join(railOf(s), "\n"), "◊")
	}, uiTimeout); err != nil {
		t.Fatalf("the waiting mark stayed after the last subagent ended: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "subagent-rows-finished")
	step()
	waitCapture(t, base, "e2e-sub", "0", "FAKE-DONE")
	alive(t, term, "after the subagent rows came and went")
}
