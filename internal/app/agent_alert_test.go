package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// alertOS builds a client with two panes and a policy, with nothing focused so
// suppress_focused never hides an alert the test meant to see.
func alertOS(t *testing.T, agent config.AgentAlertsConfig) *OS {
	t.Helper()
	m := &OS{
		Settings: config.Global,
		Width:    120, Height: 40,
		FocusedWindow:    -1,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		UserConfig:       &config.UserConfig{},
		HookManager:      hooks.NewManager(),
	}
	m.UserConfig.Notifications.Agent = agent
	for _, id := range []string{"w-1", "w-2"} {
		w := &terminal.Window{ID: id, CustomName: id, Workspace: 1}
		m.Windows = append(m.Windows, w)
	}
	return m
}

// hostCapture stands in for the terminal on the far end of the render stream.
type hostCapture struct{ b strings.Builder }

func (h *hostCapture) Write(p []byte) (int, error) { return h.b.Write(p) }

// captureHost points the client's raw host writes at a buffer.
func captureHost(t *testing.T, m *OS) *hostCapture {
	t.Helper()
	h := &hostCapture{}
	m.KittyPassthrough = NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: h})
	return h
}

// recordHook installs a runner that collects the contexts the alert fired with.
func recordHook(m *OS) *hookRecorder {
	r := &hookRecorder{}
	m.HookManager.ClearAll()
	m.HookManager.SetRunner(func(_ string, ctx hooks.Context) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.fired = append(r.fired, ctx)
	})
	m.HookManager.Register(hooks.AfterAgentState, "true")
	return r
}

func zeroSettle() config.AgentAlertsConfig {
	settle := 0
	return config.AgentAlertsConfig{SettleSeconds: &settle}
}

func TestAgentAlertFiresOnConfiguredTransitionsOnly(t *testing.T) {
	m := alertOS(t, zeroSettle())
	host := captureHost(t, m)

	// The defaults: the agent stopped, in each of the three ways it can.
	for _, state := range []string{"needs_input", "errored", "done"} {
		m.Notifications = nil
		host.b.Reset()
		m.Windows[0].AgentState = "working"
		m.noteAgentState(m.Windows[0], state)
		if len(m.Notifications) != 1 {
			t.Errorf("%s raised %d dock messages, want 1", state, len(m.Notifications))
		}
		if !strings.Contains(host.b.String(), "\x1b]9;") {
			t.Errorf("%s wrote no in-band notification: %q", state, host.b.String())
		}
	}

	// The ones that report progress rather than a stop.
	for _, state := range []string{"working", "idle"} {
		m.Notifications = nil
		host.b.Reset()
		m.Windows[0].AgentState = "done"
		m.noteAgentState(m.Windows[0], state)
		if len(m.Notifications) != 0 {
			t.Errorf("%s raised %v, want silence", state, m.Notifications)
		}
		if host.b.Len() != 0 {
			t.Errorf("%s wrote %q to the terminal, want nothing", state, host.b.String())
		}
	}
}

func TestAgentAlertMasterSwitchSilencesEverySink(t *testing.T) {
	off := false
	cfg := zeroSettle()
	cfg.Enabled = &off
	m := alertOS(t, cfg)
	host := captureHost(t, m)
	r := recordHook(m)

	m.noteAgentState(m.Windows[0], "needs_input")
	m.HookManager.Wait()

	if len(m.Notifications) != 0 || host.b.Len() != 0 || len(r.fired) != 0 {
		t.Fatalf("enabled=false left %d dock, %q host, %d hooks",
			len(m.Notifications), host.b.String(), len(r.fired))
	}
}

func TestAgentAlertPerStateToggleSilencesOneState(t *testing.T) {
	off := false
	cfg := zeroSettle()
	cfg.States.NeedsInput = &off
	m := alertOS(t, cfg)

	m.noteAgentState(m.Windows[0], "needs_input")
	if len(m.Notifications) != 0 {
		t.Fatalf("needs_input=false still raised %v", m.Notifications)
	}
	m.Windows[1].AgentState = "working"
	m.noteAgentState(m.Windows[1], "errored")
	if len(m.Notifications) != 1 {
		t.Fatal("turning one state off silenced the others too")
	}
}

func TestAgentAlertPerSinkTogglesAreIndependent(t *testing.T) {
	on, off := true, false
	cfg := zeroSettle()
	cfg.Notify, cfg.Dock, cfg.Sound = &off, &off, &on
	cfg.SoundMode = string(config.AgentSoundBell)
	m := alertOS(t, cfg)
	host := captureHost(t, m)

	m.noteAgentState(m.Windows[0], "needs_input")

	if len(m.Notifications) != 0 {
		t.Errorf("dock=false still drew %v", m.Notifications)
	}
	if got := host.b.String(); !strings.Contains(got, "\x07") || strings.Contains(got, "\x1b]9;") {
		t.Errorf("sound-only wrote %q, want a bell and no notification", got)
	}
}

// TestAgentAlertAudioModeWritesNoBell is the difference between the two sound
// modes, and it matters because the bell is not free: a terminal configured to
// flash on BEL would flash alongside a cue the user asked to hear instead of it.
func TestAgentAlertAudioModeWritesNoBell(t *testing.T) {
	on, off := true, false
	cfg := zeroSettle()
	cfg.Notify, cfg.Dock, cfg.Sound = &off, &off, &on
	cfg.SoundMode = string(config.AgentSoundAudio)
	m := alertOS(t, cfg)
	host := captureHost(t, m)

	m.noteAgentState(m.Windows[0], "needs_input")

	if got := host.b.String(); strings.Contains(got, "\x07") {
		t.Errorf("audio mode wrote a bell as well: %q", got)
	}
}

// TestAgentAlertSoundOffMakesNoNoiseEitherWay keeps the master switch master.
func TestAgentAlertSoundOffMakesNoNoiseEitherWay(t *testing.T) {
	for _, mode := range config.AgentSoundModeNames {
		t.Run(mode, func(t *testing.T) {
			off := false
			cfg := zeroSettle()
			cfg.Sound, cfg.SoundMode = &off, mode
			policy := config.ResolveAgentAlerts(&cfg)
			if policy.PlaysAudio() || policy.PlaysBell() {
				t.Errorf("sound=false with mode %q plays audio=%v bell=%v",
					mode, policy.PlaysAudio(), policy.PlaysBell())
			}
		})
	}
}

// TestAgentAlertFlappingProducesOneAlert is the anti-flicker property: a pane
// that leaves the state before its settle window closes says nothing, and one
// that stays says it once.
func TestAgentAlertFlappingProducesOneAlert(t *testing.T) {
	settle := 2
	m := alertOS(t, config.AgentAlertsConfig{SettleSeconds: &settle})
	host := captureHost(t, m)
	start := time.Now()

	// Flip in and straight back out, four times.
	for range 4 {
		m.noteAgentState(m.Windows[0], "needs_input")
		m.noteAgentState(m.Windows[0], "working")
	}
	m.flushDueAgentAlerts(start.Add(10 * time.Second))
	if len(m.Notifications) != 0 || host.b.Len() != 0 {
		t.Fatalf("a flap that resolved itself alerted: %v / %q", m.Notifications, host.b.String())
	}

	// Now one that sticks.
	m.noteAgentState(m.Windows[0], "needs_input")
	if len(m.Notifications) != 0 {
		t.Fatal("the settle window did not hold the alert back")
	}
	m.flushDueAgentAlerts(start.Add(20 * time.Second))
	if len(m.Notifications) != 1 {
		t.Fatalf("a state that stuck alerted %d times, want 1", len(m.Notifications))
	}

	// And the parked entry is gone, so the tick can go back to sleep.
	if len(m.pendingAgentAlerts) != 0 {
		t.Fatalf("%d alerts still parked after the flush", len(m.pendingAgentAlerts))
	}
}

func TestAgentAlertSettledAlertDropsWhenThePaneCloses(t *testing.T) {
	settle := 2
	m := alertOS(t, config.AgentAlertsConfig{SettleSeconds: &settle})

	m.noteAgentState(m.Windows[0], "needs_input")
	m.Windows = m.Windows[1:] // the pane exited while the alert waited
	m.flushDueAgentAlerts(time.Now().Add(10 * time.Second))

	if len(m.Notifications) != 0 {
		t.Fatalf("alerted about a pane that no longer exists: %v", m.Notifications)
	}
}

func TestAgentAlertSuppressesTheFocusedPane(t *testing.T) {
	m := alertOS(t, zeroSettle())
	m.FocusedWindow = 0

	m.noteAgentState(m.Windows[0], "needs_input")
	if len(m.Notifications) != 0 {
		t.Fatalf("the pane the user is looking at announced itself: %v", m.Notifications)
	}

	off := false
	m.UserConfig.Notifications.Agent.SuppressFocused = &off
	m.Windows[0].AgentState = "working"
	m.noteAgentState(m.Windows[0], "needs_input")
	if len(m.Notifications) != 1 {
		t.Fatal("suppress_focused=false did not restore the alert")
	}
}

func TestAgentAlertQuietHoursSilenceEverything(t *testing.T) {
	cfg := zeroSettle()
	// A window covering the whole day, so the test does not depend on the clock.
	cfg.QuietHours = "00:00-23:59"
	m := alertOS(t, cfg)
	host := captureHost(t, m)

	m.noteAgentState(m.Windows[0], "needs_input")
	if len(m.Notifications) != 0 || host.b.Len() != 0 {
		t.Fatalf("quiet hours let an alert through: %v / %q", m.Notifications, host.b.String())
	}
}

// TestAgentAlertHookCarriesTheDocumentedContract is the fire site the hooks
// coverage test refers to, and pins the payload a user's script reads.
func TestAgentAlertHookCarriesTheDocumentedContract(t *testing.T) {
	m := alertOS(t, zeroSettle())
	m.SessionName = "main"
	r := recordHook(m)

	w := m.Windows[0]
	w.AgentState = "working"
	w.AgentHarness = "claude"
	w.AgentMessage = "awaiting approval"
	m.noteAgentState(w, "needs_input")

	ctx := r.only(t, m, hooks.AfterAgentState)
	if ctx.AgentState != "needs_input" || ctx.PrevAgentState != "working" {
		t.Errorf("states = %q from %q, want needs_input from working", ctx.AgentState, ctx.PrevAgentState)
	}
	if ctx.AgentHarness != "claude" || ctx.AgentMessage != "awaiting approval" {
		t.Errorf("harness/message = %q/%q", ctx.AgentHarness, ctx.AgentMessage)
	}
	if ctx.WindowID != "w-1" || ctx.SessionID != "main" {
		t.Errorf("pane/session = %q/%q", ctx.WindowID, ctx.SessionID)
	}
}

// TestAgentAlertHookCannotStallTheClient pins the property the render loop
// depends on: the alert path returns without waiting on the command.
func TestAgentAlertHookCannotStallTheClient(t *testing.T) {
	m := alertOS(t, zeroSettle())
	m.HookManager.ClearAll()
	m.HookManager.Register(hooks.AfterAgentState, "sleep 30")

	done := make(chan struct{})
	go func() {
		m.noteAgentState(m.Windows[0], "needs_input")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a slow alert command blocked the update goroutine")
	}
}

// TestAgentAlertParkedAlertKeepsTheTickAwake pins the idle gate's side of the
// settle window: nothing parked means nothing to wake for.
func TestAgentAlertParkedAlertKeepsTheTickAwake(t *testing.T) {
	settle := 2
	m := alertOS(t, config.AgentAlertsConfig{SettleSeconds: &settle})
	if m.tickNeedsWork() {
		t.Fatal("an idle client with nothing parked already wants work")
	}
	m.noteAgentState(m.Windows[0], "needs_input")
	if !m.tickNeedsWork() {
		t.Fatal("a parked alert would never fire: the tick goes back to sleep")
	}
	m.flushDueAgentAlerts(time.Now().Add(10 * time.Second))
	m.Notifications = nil
	if m.tickNeedsWork() {
		t.Fatal("the tick stayed awake after the last alert drained")
	}
}
