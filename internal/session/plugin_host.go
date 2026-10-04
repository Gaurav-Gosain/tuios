package session

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// The herdr plugin host.
//
// tuios runs herdr plugins (internal/herdrplugin) in the daemon, the side
// that owns the panes and the event stream. What runs:
//
//   - [[startup]] commands, once per enabled plugin when the daemon is up,
//     and when the person enables a plugin while the daemon runs. Disabling
//     the plugin or stopping the daemon kills their process groups.
//   - [[actions]], when a person runs one from the palette or tuios plugins
//     run, or a caller sends plugin.action.invoke.
//   - [[events]], when the herdr event stream (herdr_events.go) carries the
//     event a hook names.
//   - [[panes]], as ordinary tuios panes, when plugin.pane.open asks.
//
// Trust. Only a plugin whose id is in [plugins] enabled runs anything, and
// the list the daemon holds changes only by the person: tuios plugins enable
// and disable, tuios config apply, or a restart. A change to config.toml
// that some other process writes applies at once only where it narrows: a
// plugin taken off the list stops, a plugin put on it waits for the person.
// A folder put on [plugins] dirs waits for the person too, because it can
// hold a plugin with the id of an enabled one. That is how [hosts] and the
// pane grants behave too (daemon_hosts.go).
// Every trust change through the herdr socket (plugin.enable, plugin.disable,
// plugin.link, plugin.unlink) is refused from a pane and from a process the
// daemon started, a plugin's own included (mayActAsHuman).
//
// Authority. A plugin's background command runs outside every pane, as the
// user, with the person's rights over the machine, as it does in herdr. Its
// calls back to tuios come from a child of the daemon, which the grants
// place in no pane, so they hold the default pane grants (paneAuthority) and
// never act as the person. A plugin pane is an ordinary pane and holds what
// any pane started by its caller holds (launchGrants).

// Bounds on event hooks: at most pluginEventBurst runs of one plugin's hooks
// in any pluginEventWindow. Hooks past it are dropped and logged, as a
// stream that falls behind drops events.
const (
	pluginEventBurst  = 20
	pluginEventWindow = 10 * time.Second
	// pluginDiscoverTTL is how long one listing of the plugins is reused.
	// An event storm reads the manifests once, not once per event.
	pluginDiscoverTTL = 2 * time.Second
)

// pluginHost is the daemon's plugin state.
type pluginHost struct {
	d      *Daemon
	runner *herdrplugin.Runner

	mu sync.Mutex
	// enabled is the list the daemon runs, which the person applied.
	enabled []string
	// dirs are [plugins] dirs, which only add places to list.
	dirs []string
	// startedUp are the plugins whose startup commands ran.
	startedUp map[string]bool
	// panes are the plugin panes plugin.pane.open made, by window id.
	panes map[string]pluginPane
	// burst counts each plugin's event hook runs in the current window.
	burst      map[string]int
	burstStart time.Time
	// cache is the last listing, reused for pluginDiscoverTTL.
	cache   []herdrplugin.Entry
	cacheAt time.Time
	running bool
}

// pluginPane is a pane a plugin opened.
type pluginPane struct {
	pluginID, entrypoint string
}

func newPluginHost(d *Daemon, cfg config.PluginsConfig) *pluginHost {
	return &pluginHost{
		d: d, runner: herdrplugin.NewRunner(),
		enabled: slices.Clone(cfg.Enabled), dirs: slices.Clone(cfg.Dirs),
		startedUp: map[string]bool{}, panes: map[string]pluginPane{}, burst: map[string]int{},
	}
}

// pluginDirs are the folders the plugins are read from.
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

// entries lists the plugins, with enabled from the daemon's applied list.
// fresh skips the cache.
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

// applied is the [plugins] table the daemon runs.
func (h *pluginHost) applied() config.PluginsConfig {
	h.mu.Lock()
	defer h.mu.Unlock()
	return config.PluginsConfig{Enabled: slices.Clone(h.enabled), Dirs: slices.Clone(h.dirs)}
}

// waits reports whether cfg, read from config.toml, enables a plugin or
// names a folder that the daemon does not run.
func (h *pluginHost) waits(cfg config.PluginsConfig) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.ContainsFunc(cfg.Enabled, func(id string) bool { return !slices.Contains(h.enabled, id) }) ||
		slices.ContainsFunc(cfg.Dirs, func(dir string) bool { return !slices.Contains(h.dirs, dir) })
}

// start runs the startup commands of the enabled plugins and starts
// following the event stream. It runs once, when the daemon is up.
func (h *pluginHost) start() {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	h.mu.Unlock()
	h.runStartups()
	go h.followEvents()
}

// stop kills every plugin process. It runs on daemon shutdown.
func (h *pluginHost) stop() {
	h.runner.StopAll(2 * time.Second)
}

// apply sets the daemon's [plugins] table. byPerson applies all of it. A
// file change applies only what narrows: a plugin taken off the list stops,
// a new one waits for the person. It reports whether a change waits.
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
	// A new folder waits for the person too. Plugins are enabled by id, and
	// the folders are read first, so a plugin in a new folder with the id of
	// an enabled plugin would run in its place.
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
	}
	if running {
		h.runStartups()
	}
	return waits
}

// runStartups runs the startup commands of every enabled plugin whose
// startup has not run yet, in plugin id order, as herdr does.
func (h *pluginHost) runStartups() {
	for _, e := range h.entries(true) {
		if !e.Runnable() || len(e.Plugin.Startup) == 0 {
			continue
		}
		h.mu.Lock()
		done := h.startedUp[e.ID]
		h.startedUp[e.ID] = true
		h.mu.Unlock()
		if done {
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

// run starts one command of plugin p with herdr's environment.
func (h *pluginHost) run(p *herdrplugin.Plugin, actionID, event string, argv []string, ctx pluginContext, eventJSON string, timeout time.Duration) (herdrplugin.LogEntry, *herdrplugin.Error) {
	dirs := h.pluginDirs()
	if err := dirs.EnsureUserDirs(p.PluginID); err != nil {
		return herdrplugin.LogEntry{}, &herdrplugin.Error{Code: "plugin_user_dir_create_failed", Msg: err.Error()}
	}
	ctxJSON, _ := json.Marshal(ctx)
	env := h.baseEnv(p, dirs)
	env = append(env, "HERDR_PLUGIN_CONTEXT_JSON="+string(ctxJSON))
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
	return h.runner.Start(herdrplugin.Job{Plugin: p, ActionID: actionID, Event: event, Command: argv, Env: env, Timeout: timeout})
}

// baseEnv is what every plugin process and plugin pane gets: herdr's plugin
// variables, the herdr socket, and the herdr link first on PATH, so a
// plugin that runs a bare herdr reaches tuios.
func (h *pluginHost) baseEnv(p *herdrplugin.Plugin, dirs herdrplugin.Dirs) []string {
	sock := HerdrSocketPath(h.d.manager.SocketPath())
	env := []string{
		// TUIOS_SOCKET places a plugin's process, and what it starts, as
		// automation of this daemon and not the person (human_origin.go).
		// Without it a service that a startup command leaves behind, once
		// its parent exits, has no tie to the daemon, and it could enable
		// plugins or answer as the person.
		"TUIOS_SOCKET=" + h.d.manager.SocketPath(),
		"HERDR_ENV=1",
		"HERDR_SOCKET_PATH=" + sock,
		"HERDR_PLUGIN_ID=" + p.PluginID,
		"HERDR_PLUGIN_ROOT=" + p.PluginRoot,
		"HERDR_PLUGIN_CONFIG_DIR=" + dirs.ConfigDir(p.PluginID),
		"HERDR_PLUGIN_STATE_DIR=" + dirs.StateDir(p.PluginID),
	}
	if b := h.d.manager.herdrBin.Load(); b != nil && *b != "" {
		env = append(env, "HERDR_BIN_PATH="+*b)
		if strings.EqualFold(strings.TrimSuffix(filepath.Base(*b), ".exe"), "herdr") {
			env = append(env, "PATH="+filepath.Dir(*b)+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}
	return env
}

// followEvents runs the event hooks of the enabled plugins, for as long as
// the daemon runs. It reads the stream every herdr client reads, through
// the same translator, so a hook sees what events.subscribe would send.
func (h *pluginHost) followEvents() {
	sub, _, err := h.d.events.subscribeFrom(eventFilter{types: herdrHubTypes}, herdrEventQueue, nil)
	if err != nil {
		log.Printf("[PLUGINS] event hooks are off: %v", err)
		return
	}
	defer h.d.events.unsubscribe(sub)
	tr := h.d.newHerdrTranslator()
	for {
		select {
		case <-h.d.ctx.Done():
			return
		case <-sub.stop:
			return
		case ev := <-sub.ch:
			if sub.dropped.Swap(0) > 0 {
				log.Printf("[PLUGINS] event hooks fell behind and some events were lost")
				tr = h.d.newHerdrTranslator()
			}
			for _, e := range tr.translate(ev) {
				h.fireHooks(e)
			}
		}
	}
}

// fireHooks runs every enabled plugin's hooks on one herdr event.
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

// admitEvent reports whether plugin id may run one more event hook now.
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

// herdrHookJSON is HERDR_PLUGIN_EVENT_JSON: herdr's EventEnvelope, the kind
// in snake_case and the same kind as the data's type.
func herdrHookJSON(e herdrEvent) string {
	kind := strings.ReplaceAll(e.sub, ".", "_")
	data := *e.Data
	data.Type = kind
	out, _ := json.Marshal(struct {
		Event string          `json:"event"`
		Data  *herdrEventData `json:"data"`
	}{kind, &data})
	return string(out)
}

// pluginContext is herdr's PluginInvocationContext: what a command is about.
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
}

// merge lays the fields a caller gave over the computed ones, field by
// field, as herdr's merge_plugin_context does.
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

// pluginContextCurrent is the context of the focused pane of the focused
// session.
func (d *Daemon) pluginContextCurrent(correlation string) pluginContext {
	sess := d.herdrFocusedSession(d.herdrOrderedSessions())
	if sess == nil {
		return pluginContext{InvocationSource: "api", CorrelationID: correlation}
	}
	return d.pluginContextFor(sess, sess.GetState().FocusedWindowID, correlation)
}

// pluginContextForPane is the context of one pane, named by its herdr id.
func (d *Daemon) pluginContextForPane(paneID, correlation string) (pluginContext, bool) {
	sess, win, ierr := d.herdrFindPane(paneID)
	if ierr != nil {
		return pluginContext{}, false
	}
	return d.pluginContextFor(sess, win.ID, correlation), true
}

// pluginContextFor builds the context of window in sess, or of the session
// alone when window is "".
func (d *Daemon) pluginContextFor(sess *Session, window, correlation string) pluginContext {
	ctx := pluginContext{InvocationSource: "api", CorrelationID: correlation}
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

// pluginContextForEvent is the context of the object an event is about.
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

// HerdrPaneID is the herdr id of a window, for a client that names a pane
// in a herdr call.
func HerdrPaneID(sessionID, windowID string) string { return herdrPaneID(sessionID, windowID) }
