package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// agentMarkStates are the states that draw a mark, in the order the docs list
// them.
var agentMarkStates = []string{"working", "needs_input", "idle", "done", "errored", "unknown"}

// inGlyphMode runs f with ASCII-only rendering set to on, and puts it back
// after.
func inGlyphMode(t *testing.T, on bool, f func()) {
	t.Helper()
	prev := overlay.UseASCII()
	overlay.SetASCII(on)
	defer overlay.SetASCII(prev)
	f()
}

// TestAgentMarksAreOneCharacterPerMeaning holds the symbol set to the rule the
// set was chosen by: every state is one shape, every shape is one state, and
// the Inbox's and the mail's own marks never reuse a state's. It runs in both
// glyph modes, because the ASCII fallback is where the collisions were: the
// Inbox drew finished as working's "*", and a mail notice as needs-you's "!".
func TestAgentMarksAreOneCharacterPerMeaning(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		inGlyphMode(t, ascii, func() {
			seen := map[string]string{}
			claim := func(mark, meaning string) {
				t.Helper()
				if mark == "" {
					t.Errorf("ascii=%v: %s draws no mark", ascii, meaning)
					return
				}
				if prev, ok := seen[mark]; ok && prev != meaning {
					t.Errorf("ascii=%v: %q means both %s and %s", ascii, mark, prev, meaning)
				}
				seen[mark] = meaning
			}
			for _, s := range agentMarkStates {
				mark := agentStateIndicator(s)
				if w := ansi.StringWidth(mark); w != 1 {
					t.Errorf("ascii=%v: %s mark %q is %d cells, want 1", ascii, s, mark, w)
				}
				claim(mark, s)
			}
			claim(sidebarMailGlyph(), "mail")
			claim(inboxKindGlyph(session.AttentionResume), "resume")
			claim(inboxKindGlyph(session.AttentionOutbox), "outbox")
			claim(agentMailLinkGlyph(), "mail over a link")

			// The Inbox kinds that stand for a state wear that state's mark.
			for kind, state := range map[string]string{
				session.AttentionApproval: "needs_input",
				session.AttentionQuestion: "needs_input",
				session.AttentionAsk:      "needs_input",
				session.AttentionErrored:  "errored",
				session.AttentionFinished: "done",
				session.AttentionMail:     "mail",
			} {
				want := sidebarMailGlyph()
				if state != "mail" {
					want = agentStateIndicator(state)
				}
				if got := inboxKindGlyph(kind); got != want {
					t.Errorf("ascii=%v: Inbox %s wears %q, want %q", ascii, kind, got, want)
				}
			}
		})
	}
}

// TestReadFinishedPaneLooksTheSameEverywhere pins the unread rule on every
// surface that draws a state. A finished pane the person has looked at wore
// idle's circle on the rail and the filled square on the title bar, the
// palette, the session switcher and the aggregate view.
func TestReadFinishedPaneLooksTheSameEverywhere(t *testing.T) {
	pal := theme.UI()
	unread, _ := agentMark("done", false, pal)
	read, readInk := agentMark("done", true, pal)
	if unread != agentStateIndicator("done") {
		t.Errorf("an unread finished pane draws %q, want done's %q", unread, agentStateIndicator("done"))
	}
	if read != agentStateIndicator("idle") {
		t.Errorf("a read finished pane draws %q, want idle's %q", read, agentStateIndicator("idle"))
	}
	if readInk != pal.FgMute {
		t.Errorf("a read finished pane is inked %v, want the muted ink", readInk)
	}

	if got := sessionPaletteLabel("Window: ", "api", "done", true); !strings.Contains(got, read) || strings.Contains(got, unread) {
		t.Errorf("palette row for a read finished pane = %q, want the %q mark", got, read)
	}

	win := newTestWindow(t, "mark-title-0001", 60, 20)
	win.CustomName = "api"
	win.AgentState = "done"
	m := newTestOS(win)
	m.markAgentSeen(win.ID)
	title := windowTitleText(win, m.windowMarkState(win), 1, 40, &config.Global)
	if !strings.HasPrefix(title, read+" ") {
		t.Errorf("title bar of a read finished pane = %q, want it to lead with %q", title, read)
	}
	if strings.Contains(title, unread) {
		t.Errorf("title bar of a read finished pane still draws done's %q: %q", unread, title)
	}
}

// TestAgentAlertWearsTheStateMark checks the dock draws an agent alert with
// the state's own mark rather than a Nerd Font severity icon, which showed a
// different shape for the same state and tofu without a patched font.
func TestAgentAlertWearsTheStateMark(t *testing.T) {
	for _, tc := range []struct {
		state, sev, nerd string
	}{
		{"needs_input", "warning", config.NotificationGlyphWarning},
		{"errored", "error", config.NotificationGlyphError},
		{"done", "success", config.NotificationGlyphSuccess},
	} {
		m := notifTestOS(t, 160)
		m.showAgentNotification("api "+tc.state, tc.sev, tc.state, m.Settings.NotificationDuration, NotifTarget{WindowID: "x"})
		block, ok := m.renderNotificationBlock(160, 0)
		if !ok {
			t.Fatalf("%s: no block", tc.state)
		}
		plain := ansi.Strip(block.Text)
		if !strings.Contains(plain, agentStateIndicator(tc.state)+"  api") {
			t.Errorf("%s: dock block %q does not lead with the state's mark %q", tc.state, plain, agentStateIndicator(tc.state))
		}
		if strings.Contains(plain, tc.nerd) {
			t.Errorf("%s: dock block still draws the Nerd Font icon: %q", tc.state, plain)
		}
	}

	// A message that is not about an agent keeps its severity icon.
	m := notifTestOS(t, 160)
	m.ShowNotification("Layout saved", "success", m.Settings.NotificationDuration)
	block, _ := m.renderNotificationBlock(160, 0)
	if !strings.Contains(ansi.Strip(block.Text), config.NotificationGlyphSuccess) {
		t.Errorf("a plain message lost its severity icon: %q", ansi.Strip(block.Text))
	}
}

// TestInboxBurstMarkState is which state's mark a burst of Inbox alerts wears.
func TestInboxBurstMarkState(t *testing.T) {
	approval := session.AttentionItem{Kind: session.AttentionApproval}
	mail := session.AttentionItem{Kind: session.AttentionMail}
	for _, tc := range []struct {
		items []session.AttentionItem
		sev   string
		want  string
	}{
		{[]session.AttentionItem{approval}, "warning", "needs_input"},
		{[]session.AttentionItem{mail}, "warning", ""},
		{[]session.AttentionItem{mail, approval}, "warning", "needs_input"},
		{[]session.AttentionItem{{Kind: session.AttentionErrored}}, "error", "errored"},
		{[]session.AttentionItem{{Kind: session.AttentionFinished}}, "success", "done"},
	} {
		if got := inboxAlertMarkState(tc.items, tc.sev); got != tc.want {
			t.Errorf("inboxAlertMarkState(%v, %s) = %q, want %q", tc.items, tc.sev, got, tc.want)
		}
	}
}

// TestMailRowsSayTheKindInWords covers the mail list rows: one mark for all
// mail, the kind in words, and a notice with no sender named as a notice
// rather than as "someone".
func TestMailRowsSayTheKindInWords(t *testing.T) {
	for _, tc := range []struct {
		th   agentMailThread
		want string
	}{
		{agentMailThread{Kind: "message", From: "docs", To: "api"}, "docs → api"},
		{agentMailThread{Kind: "ask", From: "docs", To: "api"}, "docs asks api"},
		{agentMailThread{Kind: "notice", From: "someone", To: "all", Anonymous: true}, "Notice to all"},
		{agentMailThread{Kind: "notice", From: "lead", To: "all"}, "Notice from lead to all"},
	} {
		if got := agentMailWho(tc.th); got != tc.want {
			t.Errorf("agentMailWho(%+v) = %q, want %q", tc.th, got, tc.want)
		}
	}
}
