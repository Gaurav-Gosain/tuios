package session

import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const (
	pluginEventBurst  = 20
	pluginEventWindow = 10 * time.Second
	pluginDiscoverTTL = 2 * time.Second
)

type pluginHost struct {
	d      *Daemon
	runner *herdrplugin.Runner

	mu         sync.Mutex
	enabled    []string
	dirs       []string
	startedUp  map[string]bool
	roots      map[string]string
	panes      map[string]pluginPane
	burst      map[string]int
	burstStart time.Time
	cache      []herdrplugin.Entry
	cacheAt    time.Time
	running    bool
	following  bool
	cells      map[string]map[string]PluginCell
	uiCount    map[string]int
	uiStart    time.Time
	fresh      *herdrTranslator
}

type pluginPane struct {
	pluginID, entrypoint string
	root                 string
}

// PluginReaper is the argv that runs a plugin command under a reaper, set
// by the tuios binary (herdrplugin.Runner.Reaper). Nil runs commands
// without one, as in tests.
var PluginReaper []string

func newPluginHost(d *Daemon, cfg config.PluginsConfig) *pluginHost {
	runner := herdrplugin.NewRunner()
	runner.Reaper = PluginReaper
	runner.Hold = func(pluginID string) (func(), bool) {
		return d.pipes.tryHold("a command of plugin " + pluginID + " is running")
	}
	return &pluginHost{
		d: d, runner: runner,
		enabled: slices.Clone(cfg.Enabled), dirs: slices.Clone(cfg.Dirs),
		startedUp: map[string]bool{}, roots: map[string]string{}, panes: map[string]pluginPane{}, burst: map[string]int{},
	}
}

func (h *pluginHost) pluginDirs() herdrplugin.Dirs {
	path := h.d.configPath
	if path == "" {
		path, _ = config.GetConfigPath()
	}
	h.mu.Lock()
	dirs := slices.Clone(h.dirs)
	h.mu.Unlock()
	return herdrplugin.DefaultDirs(path, dirs)
}

func (h *pluginHost) entries(fresh bool) []herdrplugin.Entry {
	h.mu.Lock()
	if !fresh && h.cache != nil && time.Since(h.cacheAt) < pluginDiscoverTTL {
		out := h.cache
		h.mu.Unlock()
		return out
	}
	enabled := slices.Clone(h.enabled)
	h.mu.Unlock()
	out := herdrplugin.Discover(h.pluginDirs(), enabled)
	h.mu.Lock()
	h.cache, h.cacheAt = out, time.Now()
	h.mu.Unlock()
	return out
}

func (h *pluginHost) applied() config.PluginsConfig {
	h.mu.Lock()
	defer h.mu.Unlock()
	return config.PluginsConfig{Enabled: slices.Clone(h.enabled), Dirs: slices.Clone(h.dirs)}
}

func (h *pluginHost) waits(cfg config.PluginsConfig) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.ContainsFunc(cfg.Enabled, func(id string) bool { return !slices.Contains(h.enabled, id) }) ||
		slices.ContainsFunc(cfg.Dirs, func(dir string) bool { return !slices.Contains(h.dirs, dir) })
}

func (h *pluginHost) start() {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	follow := len(h.enabled) > 0 && !h.following
	h.following = h.following || follow
	h.mu.Unlock()
	h.runStartups()
	if follow {
		go h.followEvents()
	}
}

func (h *pluginHost) stop() {
	h.runner.StopAll(2 * time.Second)
}

func (h *pluginHost) restart() {
	h.runner.Resume()
	h.mu.Lock()
	running := h.running
	clear(h.startedUp)
	h.mu.Unlock()
	if running {
		h.runStartups()
	}
}

func (h *pluginHost) apply(cfg config.PluginsConfig, byPerson bool) (waits bool) {
	h.mu.Lock()
	old := slices.Clone(h.enabled)
	next := slices.Clone(cfg.Enabled)
	if !byPerson {
		for _, id := range cfg.Enabled {
			if !slices.Contains(old, id) {
				waits = true
			}
		}
		next = slices.DeleteFunc(next, func(id string) bool { return !slices.Contains(old, id) })
	}
	dirs := slices.Clone(cfg.Dirs)
	if !byPerson {
		for _, dir := range dirs {
			if !slices.Contains(h.dirs, dir) {
				waits = true
			}
		}
		dirs = slices.DeleteFunc(dirs, func(dir string) bool { return !slices.Contains(h.dirs, dir) })
	}
	h.enabled = next
	h.dirs = dirs
	h.cache = nil
	running := h.running
	wake := running && len(old) == 0 && len(next) > 0
	follow := wake && !h.following
	h.following = h.following || follow
	if len(next) == 0 {
		h.fresh = nil
	}
	var stopped []string
	for _, id := range old {
		if !slices.Contains(next, id) {
			stopped = append(stopped, id)
			delete(h.startedUp, id)
		}
	}
	h.mu.Unlock()
	for _, id := range stopped {
		log.Printf("[PLUGINS] %s is disabled. Its processes are stopped", id)
		h.runner.StopPlugin(id)
		h.dropCells(id)
	}
	if wake {
		tr := h.d.newHerdrTranslator()
		h.mu.Lock()
		if len(h.enabled) > 0 {
			h.fresh = tr
		}
		h.mu.Unlock()
	}
	if follow {
		go h.followEvents()
	}
	if running {
		h.runStartups()
	}
	return waits
}

func (h *pluginHost) refresh() {
	h.mu.Lock()
	h.cache = nil
	running := h.running
	h.mu.Unlock()
	if running {
		h.runStartups()
	}
}

func (h *pluginHost) stopPlugin(id string) {
	h.mu.Lock()
	delete(h.startedUp, id)
	h.mu.Unlock()
	h.runner.StopPlugin(id)
	h.dropCells(id)
}

func (h *pluginHost) runStartups() {
	entries := h.entries(true)
	defer func() { h.d.notePluginAsks(pluginAsksOf(entries)) }()
	for _, e := range entries {
		if !e.Runnable() {
			continue
		}
		h.mu.Lock()
		done := h.startedUp[e.ID]
		h.startedUp[e.ID] = true
		if !done {
			h.roots[e.ID] = e.Plugin.PluginRoot
		}
		h.mu.Unlock()
		if done || len(e.Plugin.Startup) == 0 {
			continue
		}
		ctx := h.d.pluginContextCurrent("plugin.startup")
		ctx.InvocationSource = "startup"
		for _, s := range e.Plugin.Startup {
			if e.Plugin.Supported(s.Platforms, "startup") != nil {
				continue
			}
			if _, perr := h.run(e.Plugin, "", "startup", s.Command, ctx, "", 0); perr != nil {
				log.Printf("[PLUGINS] startup of %s did not start: %s", e.ID, perr.Msg)
			}
		}
	}
}

func (h *pluginHost) run(p *herdrplugin.Plugin, actionID, event string, argv []string, ctx pluginContext, eventJSON string, timeout time.Duration) (herdrplugin.LogEntry, *herdrplugin.Error) {
	dirs := h.pluginDirs()
	if err := dirs.EnsureUserDirs(p.PluginID); err != nil {
		return herdrplugin.LogEntry{}, &herdrplugin.Error{Code: "plugin_user_dir_create_failed", Msg: err.Error()}
	}
	ctxJSON, _ := jsonx.Marshal(ctx)
	env := h.baseEnv(p, dirs)
	env = append(env, "HERDR_PLUGIN_CONTEXT_JSON="+string(ctxJSON))
	if p.Format == herdrplugin.FormatTuios {
		env = append(env, ctx.nativeEnv(string(ctxJSON), actionID)...)
		if event != "" {
			env = append(env, "TUIOS_PLUGIN_EVENT="+event)
		}
		if eventJSON != "" {
			env = append(env, "TUIOS_PLUGIN_EVENT_JSON="+eventJSON)
		}
	}
	if actionID != "" {
		env = append(env, "HERDR_PLUGIN_ACTION_ID="+actionID)
	}
	if event != "" {
		env = append(env, "HERDR_PLUGIN_EVENT="+event)
	}
	if eventJSON != "" {
		env = append(env, "HERDR_PLUGIN_EVENT_JSON="+eventJSON)
	}
	for _, kv := range [][2]string{
		{"HERDR_WORKSPACE_ID", ctx.WorkspaceID}, {"HERDR_TAB_ID", ctx.TabID}, {"HERDR_PANE_ID", ctx.FocusedPaneID},
		{"HERDR_PLUGIN_CLICKED_URL", ctx.ClickedURL}, {"HERDR_PLUGIN_LINK_HANDLER_ID", ctx.LinkHandlerID},
	} {
		if kv[1] != "" {
			env = append(env, kv[0]+"="+kv[1])
		}
	}
	release, err := herdrplugin.HoldLease(dirs.HerdrConfig, p.PluginRoot)
	if err != nil {
		LogBasic("Warning: plugin %s runs without herdr's install lease: %v", p.PluginID, err)
	}
	return h.runner.Start(herdrplugin.Job{Plugin: p, ActionID: actionID, Event: event, Command: argv, Env: env, Timeout: timeout, Release: release})
}

func (h *pluginHost) baseEnv(p *herdrplugin.Plugin, dirs herdrplugin.Dirs) []string {
	sock := HerdrSocketPath(h.d.manager.SocketPath())
	env := []string{
		"TUIOS_SOCKET=" + h.d.manager.SocketPath(),
		"HERDR_ENV=1",
		"HERDR_SOCKET_PATH=" + sock,
		"HERDR_PLUGIN_ID=" + p.PluginID,
		"HERDR_PLUGIN_ROOT=" + p.PluginRoot,
		"HERDR_PLUGIN_CONFIG_DIR=" + dirs.ConfigDir(p.PluginID),
		"HERDR_PLUGIN_STATE_DIR=" + dirs.StateDir(p.PluginID),
	}
	if p.Format == herdrplugin.FormatTuios {
		env = append(env, "TUIOS_PLUGIN_ID="+p.PluginID, "TUIOS_PLUGIN_TOKEN="+h.d.manager.PluginToken(p.PluginID))
		env = append(env, "TUIOS_PLUGIN_ROOT="+p.PluginRoot,
			"TUIOS_PLUGIN_CONFIG_DIR="+dirs.ConfigDir(p.PluginID),
			"TUIOS_PLUGIN_STATE_DIR="+dirs.StateDir(p.PluginID))
		if exe, err := os.Executable(); err == nil {
			env = append(env, "TUIOS_BIN="+exe)
		}
	}
	if b := h.d.manager.herdrBin.Load(); b != nil && *b != "" {
		env = append(env, "HERDR_BIN_PATH="+*b)
		if strings.EqualFold(strings.TrimSuffix(filepath.Base(*b), ".exe"), "herdr") {
			env = append(env, "PATH="+filepath.Dir(*b)+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}
	return env
}

func (h *pluginHost) followEvents() {
	sub, _, err := h.d.events.subscribeFrom(eventFilter{types: herdrHubTypes}, herdrEventQueue, nil)
	if err != nil {
		log.Printf("[PLUGINS] event hooks are off: %v", err)
		return
	}
	defer h.d.events.unsubscribe(sub)
	var tr *herdrTranslator
	if on, _ := h.hooksOn(); on {
		tr = h.d.newHerdrTranslator()
	}
	for {
		select {
		case <-h.d.ctx.Done():
			return
		case <-sub.stop:
			return
		case ev := <-sub.ch:
			dropped := sub.dropped.Swap(0) > 0
			on, fresh := h.hooksOn()
			if !on {
				tr = nil
				continue
			}
			if fresh != nil {
				tr = fresh
			}
			if dropped {
				log.Printf("[PLUGINS] event hooks fell behind and some events were lost")
				tr = nil
			}
			if tr == nil {
				tr = h.d.newHerdrTranslator()
			}
			for _, e := range tr.translate(ev) {
				h.fireHooks(e)
			}
		}
	}
}

func (h *pluginHost) hooksOn() (on bool, fresh *herdrTranslator) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.enabled) == 0 {
		return false, nil
	}
	fresh, h.fresh = h.fresh, nil
	return true, fresh
}

func (h *pluginHost) fireHooks(e herdrEvent) {
	name := e.sub
	if !slices.Contains(herdrplugin.HookEvents, name) {
		return
	}
	var eventJSON string
	var ctx pluginContext
	built := false
	for _, ent := range h.entries(false) {
		if !ent.Runnable() {
			continue
		}
		for _, hook := range ent.Plugin.Events {
			if hook.On != name || ent.Plugin.Supported(hook.Platforms, name) != nil {
				continue
			}
			if !h.admitEvent(ent.ID) {
				log.Printf("[PLUGINS] %s ran more than %d event hooks in %s. The hook on %s was dropped", ent.ID, pluginEventBurst, pluginEventWindow, name)
				continue
			}
			if !built {
				eventJSON = herdrHookJSON(e)
				ctx = h.d.pluginContextForEvent(e, name)
				built = true
			}
			if _, perr := h.run(ent.Plugin, "", name, hook.Command, ctx, eventJSON, herdrplugin.DefaultTimeout); perr != nil {
				log.Printf("[PLUGINS] the %s hook of %s did not start: %s", name, ent.ID, perr.Msg)
			}
		}
	}
}

func (h *pluginHost) admitEvent(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.burstStart) > pluginEventWindow {
		h.burst, h.burstStart = map[string]int{}, time.Now()
	}
	if h.burst[id] >= pluginEventBurst {
		return false
	}
	h.burst[id]++
	return true
}

func herdrHookJSON(e herdrEvent) string {
	kind := strings.ReplaceAll(e.sub, ".", "_")
	data := *e.Data
	data.Type = kind
	out, _ := jsonx.Marshal(struct {
		Event string          `json:"event"`
		Data  *herdrEventData `json:"data"`
	}{kind, &data})
	return string(out)
}

type pluginContext struct {
	WorkspaceID       string                  `json:"workspace_id,omitempty"`
	WorkspaceLabel    string                  `json:"workspace_label,omitempty"`
	WorkspaceCwd      string                  `json:"workspace_cwd,omitempty"`
	Worktree          *herdrWorkspaceWorktree `json:"worktree,omitempty"`
	TabID             string                  `json:"tab_id,omitempty"`
	TabLabel          string                  `json:"tab_label,omitempty"`
	FocusedPaneID     string                  `json:"focused_pane_id,omitempty"`
	FocusedPaneCwd    string                  `json:"focused_pane_cwd,omitempty"`
	FocusedPaneAgent  string                  `json:"focused_pane_agent,omitempty"`
	FocusedPaneStatus string                  `json:"focused_pane_status,omitempty"`
	SelectedText      string                  `json:"selected_text,omitempty"`
	InvocationSource  string                  `json:"invocation_source,omitempty"`
	CorrelationID     string                  `json:"correlation_id,omitempty"`
	ClickedURL        string                  `json:"clicked_url,omitempty"`
	LinkHandlerID     string                  `json:"link_handler_id,omitempty"`

	sessionName, window string
}

func (c pluginContext) nativeEnv(contextJSON, entry string) []string {
	env := []string{"TUIOS_PLUGIN_CONTEXT_JSON=" + contextJSON}
	for _, kv := range [][2]string{
		{"TUIOS_PLUGIN_ENTRY", entry}, {"TUIOS_PLUGIN_SESSION", c.sessionName},
		{"TUIOS_PLUGIN_PANE", c.window}, {"TUIOS_PLUGIN_CWD", c.FocusedPaneCwd},
	} {
		if kv[1] != "" {
			env = append(env, kv[0]+"="+kv[1])
		}
	}
	return env
}

func (c pluginContext) merge(p *pluginContext) pluginContext {
	if p == nil {
		return c
	}
	pick := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	c.WorkspaceID = pick(p.WorkspaceID, c.WorkspaceID)
	c.WorkspaceLabel = pick(p.WorkspaceLabel, c.WorkspaceLabel)
	c.WorkspaceCwd = pick(p.WorkspaceCwd, c.WorkspaceCwd)
	if p.Worktree != nil {
		c.Worktree = p.Worktree
	}
	c.TabID = pick(p.TabID, c.TabID)
	c.TabLabel = pick(p.TabLabel, c.TabLabel)
	c.FocusedPaneID = pick(p.FocusedPaneID, c.FocusedPaneID)
	c.FocusedPaneCwd = pick(p.FocusedPaneCwd, c.FocusedPaneCwd)
	c.FocusedPaneAgent = pick(p.FocusedPaneAgent, c.FocusedPaneAgent)
	c.FocusedPaneStatus = pick(p.FocusedPaneStatus, c.FocusedPaneStatus)
	c.SelectedText = pick(p.SelectedText, c.SelectedText)
	c.InvocationSource = pick(p.InvocationSource, c.InvocationSource)
	c.CorrelationID = pick(p.CorrelationID, c.CorrelationID)
	c.ClickedURL = pick(p.ClickedURL, c.ClickedURL)
	c.LinkHandlerID = pick(p.LinkHandlerID, c.LinkHandlerID)
	return c
}

func (d *Daemon) pluginContextCurrent(correlation string) pluginContext {
	sess := d.herdrFocusedSession(d.herdrOrderedSessions())
	if sess == nil {
		return pluginContext{InvocationSource: "api", CorrelationID: correlation}
	}
	return d.pluginContextFor(sess, sess.GetState().FocusedWindowID, correlation)
}

func (d *Daemon) pluginContextForPane(paneID, correlation string) (pluginContext, bool) {
	sess, win, ierr := d.herdrFindPane(paneID)
	if ierr != nil {
		return pluginContext{}, false
	}
	return d.pluginContextFor(sess, win.ID, correlation), true
}

func (d *Daemon) pluginContextFor(sess *Session, window, correlation string) pluginContext {
	ctx := pluginContext{InvocationSource: "api", CorrelationID: correlation, sessionName: sess.Name(), window: window}
	v := &herdrView{}
	st := sess.GetState()
	d.addHerdrSession(v, sess, st, 1, false)
	if len(v.workspaces) == 0 {
		return ctx
	}
	ws := v.workspaces[0]
	ctx.WorkspaceID, ctx.WorkspaceLabel, ctx.Worktree, ctx.TabID = ws.WorkspaceID, ws.Label, ws.Worktree, ws.ActiveTabID
	if ws.Worktree != nil {
		ctx.WorkspaceCwd = ws.Worktree.CheckoutPath
	}
	if window != "" {
		if p := v.pane(herdrPaneID(sess.ID, window)); p != nil {
			ctx.FocusedPaneID, ctx.FocusedPaneCwd, ctx.FocusedPaneAgent = p.PaneID, p.Cwd, p.Agent
			if p.Agent != "" {
				ctx.FocusedPaneStatus = p.AgentStatus
			}
			ctx.TabID = p.TabID
			if p.Cwd != "" {
				ctx.WorkspaceCwd = p.Cwd
			}
		}
	}
	if t := v.tab(ctx.TabID); t != nil {
		ctx.TabLabel = t.Label
	}
	return ctx
}

func (d *Daemon) pluginContextForEvent(e herdrEvent, name string) pluginContext {
	data := e.Data
	paneID := data.PaneID
	if paneID == "" && data.Pane != nil {
		paneID = data.Pane.PaneID
	}
	if paneID != "" {
		if ctx, ok := d.pluginContextForPane(paneID, name); ok {
			return ctx
		}
	}
	wsID := data.WorkspaceID
	if wsID == "" && data.Workspace != nil {
		wsID = data.Workspace.WorkspaceID
	}
	if wsID == "" && data.Tab != nil {
		wsID = data.Tab.WorkspaceID
	}
	if wsID != "" {
		if sess, ierr := d.herdrFindSession(wsID); ierr == nil {
			ctx := d.pluginContextFor(sess, sess.GetState().FocusedWindowID, name)
			if data.TabID != "" {
				ctx.TabID = data.TabID
			}
			return ctx
		}
	}
	ctx := pluginContext{InvocationSource: "api", CorrelationID: name, WorkspaceID: wsID, TabID: data.TabID, FocusedPaneID: paneID}
	return ctx
}
