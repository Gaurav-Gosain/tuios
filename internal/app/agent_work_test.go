package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// These tests pin the client half of the agent work's foundation: the new
// chrome waits for an agent like the rest of the agent chrome, the queue's
// length reaches the rail, and the plan kind reads as something waiting on
// the person.

// TestAgentWorkChromeWaitsForAnAgent: ctrl+b v and ctrl+b O are listed in the
// prefix menu and help, and the fold row on the settings page, only once an
// agent has been seen.
func TestAgentWorkChromeWaitsForAnAgent(t *testing.T) {
	withSidebar(t, true, "right", config.SidebarDefaultWidth)
	m := newTestOS(&terminal.Window{ID: "w-1", Width: 40, Height: 20, Workspace: 1})
	m.Settings = config.Global
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())

	read := func() (menu, help, fold bool) {
		for _, b := range m.prefixMenuBindings() {
			if b.Key == "v" || b.Key == "O" {
				menu = true
			}
		}
		for _, cat := range m.HelpCategories() {
			for _, b := range cat.Bindings {
				if b.Description == config.ActionDescriptions["prefix_review"] {
					help = true
				}
			}
		}
		for _, cat := range m.settingsCategories() {
			for _, it := range cat.Items {
				if it.Path == "appearance.sidebar.agent_rest_fold" {
					fold = true
				}
			}
		}
		return
	}
	if menu, help, fold := read(); menu || help || fold {
		t.Fatalf("with no agent seen: menu %v, help %v, fold row %v", menu, help, fold)
	}
	m.noteAgentState(m.Windows[0], "working")
	if menu, help, fold := read(); !menu || !help || !fold {
		t.Errorf("with an agent seen: menu %v, help %v, fold row %v, want all", menu, help, fold)
	}
}

// TestTheQueueLengthReachesTheRail: the synced count lands on the window, in
// the session tree and on the agents section's entry, and a change to it
// rebuilds the rail.
func TestTheQueueLengthReachesTheRail(t *testing.T) {
	withSidebar(t, true, "right", config.SidebarDefaultWidth)
	w := &terminal.Window{ID: "w-1", Width: 40, Height: 20, Workspace: 1}
	m := newTestOS(w)
	m.Settings = config.Global
	m.updateWindowFromState(w, &session.WindowState{ID: "w-1", AgentState: session.AgentStateWorking, AgentQueued: 2})
	if w.AgentQueued != 2 {
		t.Fatalf("the window holds %d queued, want 2", w.AgentQueued)
	}
	in := m.currentSessionInput()
	if len(in.Windows) != 1 || in.Windows[0].Queued != 2 {
		t.Fatalf("the session input carries %+v", in.Windows)
	}
	node := sessiontree.BuildSession(in)
	if len(node.Children) != 1 || node.Children[0].Queued != 2 {
		t.Fatalf("the tree node carries %+v", node.Children)
	}
	var found bool
	for _, e := range m.sidebarAgents([]sessiontree.Node{node}) {
		if e.WindowID == "w-1" {
			found = e.Queued == 2
		}
	}
	if !found {
		t.Error("the rail's agent entry lost the queue length")
	}
	before := m.sidebarSignature()
	w.AgentQueued = 3
	if m.sidebarSignature() == before {
		t.Error("a change to the queue does not rebuild the rail")
	}
}

// TestThePlanKindWaitsOnThePerson: a plan is grouped under Plans, counts as
// blocking the agent, and alerts as needs_input.
func TestThePlanKindWaitsOnThePerson(t *testing.T) {
	if inboxGroupTitle(session.AttentionPlan) != "Plans" {
		t.Errorf("a plan sits under %q", inboxGroupTitle(session.AttentionPlan))
	}
	if inboxAlertState(session.AttentionPlan) != "needs_input" {
		t.Errorf("a plan alerts as %q", inboxAlertState(session.AttentionPlan))
	}
	m := &OS{}
	m.Inbox.Items = []session.AttentionItem{{ID: "1", Kind: session.AttentionPlan, Session: "work", Window: "w"}}
	if c := m.inboxCounts(""); c.Blocked != 1 || c.Worst != "needs_input" {
		t.Errorf("a plan counts as %+v", c)
	}
	if words := inboxKindWords(m.Inbox.Items[0]); words == session.AttentionPlan {
		t.Error("a plan's alert says its kind rather than words")
	}
}

// TestUnbuiltAgentWorkHooksChangeNothing: until their work lands, the hooks
// answer that they did nothing, so the Inbox and the rail draw and answer as
// they did.
func TestUnbuiltAgentWorkHooksChangeNothing(t *testing.T) {
	m := &OS{}
	// The Inbox's lifecycle keys (inbox_lifecycle.go) are built, and answer
	// on any list, empty or not.
	built := map[string]bool{config.ActionInboxSnooze: true, config.ActionInboxUndo: true, config.ActionInboxShowSnoozed: true}
	for action := range InboxWorkActions {
		if built[action] {
			continue
		}
		if _, handled := m.InboxWorkAction(action); handled {
			t.Errorf("%s says it did something before it is built", action)
		}
	}
	for _, action := range []string{config.ActionAgentUnread, config.ActionAgentSnooze, config.ActionAgentReply, config.ActionAgentReview, config.ActionAgentCancelQueued} {
		if _, handled := m.SidebarAgentAction(action); handled {
			t.Errorf("%s says it did something with no agent row under the cursor", action)
		}
	}
	it := session.AttentionItem{Kind: session.AttentionFinished, Summary: "done"}
	if m.inboxRowExtras(it) != "" || m.sidebarAgentQueuedFigure(sidebarAgentEntry{Queued: 2}) != "" {
		t.Error("a render hook draws something before it is built")
	}
	if _, _, ok := m.inboxDetailExtras(it); ok {
		t.Error("the detail hook claims an item before it is built")
	}
	// Every Inbox action the dispatcher answers is bound by default, and none
	// of them shares a key with an older Inbox action.
	inbox := config.DefaultConfig().Keybindings.Inbox
	for action := range InboxWorkActions {
		if len(inbox[action]) == 0 {
			t.Errorf("%s has no default key", action)
		}
	}
	var old []string
	for action, keys := range inbox {
		if !InboxWorkActions[action] {
			old = append(old, keys...)
		}
	}
	for action := range InboxWorkActions {
		for _, k := range inbox[action] {
			if slices.Contains(old, k) {
				t.Errorf("%s takes %q from an older Inbox action", action, k)
			}
		}
	}
}
