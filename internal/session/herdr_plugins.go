package session

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// herdr's plugin.* socket methods and popup.close, answered by the plugin
// host (plugin_host.go). Each takes herdr's params and answers herdr's
// result shape (src/api/schema/plugins.rs at herdrTargetVersion), so a
// plugin or a tool that drives herdr's plugins drives tuios's the same way.

// herdrPluginMethods are the methods this file answers.
var herdrPluginMethods = map[string]func(d *Daemon, cs *connState, params json.RawMessage) (any, *herdrError){
	"plugin.list":   (*Daemon).herdrPluginList,
	"plugin.link":   (*Daemon).herdrPluginLink,
	"plugin.unlink": (*Daemon).herdrPluginUnlink,
	"plugin.enable": func(d *Daemon, cs *connState, p json.RawMessage) (any, *herdrError) {
		return d.herdrPluginSetEnabled(cs, p, true)
	},
	"plugin.disable": func(d *Daemon, cs *connState, p json.RawMessage) (any, *herdrError) {
		return d.herdrPluginSetEnabled(cs, p, false)
	},
	"plugin.action.list":   (*Daemon).herdrPluginActionList,
	"plugin.action.invoke": (*Daemon).herdrPluginActionInvoke,
	"plugin.log.list":      (*Daemon).herdrPluginLogList,
	"plugin.pane.open":     (*Daemon).herdrPluginPaneOpen,
	"plugin.pane.focus":    (*Daemon).herdrPluginPaneFocus,
	"plugin.pane.close":    (*Daemon).herdrPluginPaneClose,
	"popup.close":          (*Daemon).herdrPopupClose,
}

// herdrPluginCall answers a plugin method. handled is false for any other.
func (d *Daemon) herdrPluginCall(cs *connState, method string, params json.RawMessage) (any, *herdrError, bool) {
	fn, ok := herdrPluginMethods[method]
	if !ok {
		return nil, nil, false
	}
	if d.plugins == nil {
		return nil, herdrErr("unsupported", "this tuios daemon runs no plugins"), true
	}
	out, herr := fn(d, cs, params)
	return out, herr, true
}

func fromPluginErr(e *herdrplugin.Error) *herdrError { return herdrErr(e.Code, e.Msg) }

// herdrPluginTrust refuses a change to which plugins run unless the person
// asks: never from a pane, from a process the daemon started (a plugin's
// own command included), or over a link.
func (d *Daemon) herdrPluginTrust(cs *connState, method string) *herdrError {
	if cs != nil && (cs.viaLink || !d.mayActAsHuman(cs)) {
		return herdrErr("forbidden", method+" is for the person: the caller runs inside a pane or a plugin of this tuios. Nothing was changed. Run tuios plugins from a terminal outside tuios")
	}
	return nil
}

// herdrPluginMayRun refuses a pane that may not start processes. A plugin
// command runs with the person's rights, so starting one needs what
// starting a pane needs: admin.
func (d *Daemon) herdrPluginMayRun(cs *connState) *herdrError {
	if cs != nil && cs.viaLink {
		return herdrErr("forbidden", "plugin commands are not run for another machine")
	}
	if pa := d.paneAuthority(cs); pa != nil && !pa.grants.Has(GrantAdmin) {
		return herdrErr("forbidden", "running a plugin command needs the admin grant, and this pane holds "+pa.grants.String())
	}
	return nil
}

type herdrPluginIn struct {
	PluginID   string              `json:"plugin_id"`
	ActionID   string              `json:"action_id"`
	Path       string              `json:"path"`
	Enabled    *bool               `json:"enabled"`
	Limit      int                 `json:"limit"`
	Context    *pluginContext      `json:"context"`
	Entrypoint string              `json:"entrypoint"`
	Placement  string              `json:"placement"`
	Width      *herdrplugin.Size   `json:"width"`
	Height     *herdrplugin.Size   `json:"height"`
	Workspace  string              `json:"workspace_id"`
	Target     string              `json:"target_pane_id"`
	Direction  string              `json:"direction"`
	Cwd        string              `json:"cwd"`
	Focus      bool                `json:"focus"`
	Env        map[string]string   `json:"env"`
	PaneID     string              `json:"pane_id"`
	Source     *herdrplugin.Source `json:"source"`
}

func decodePluginIn(params json.RawMessage, required ...string) (*herdrPluginIn, *herdrError) {
	in := &herdrPluginIn{}
	if herr := herdrDecode(params, in, required...); herr != nil {
		return nil, herr
	}
	return in, nil
}

// pluginIDParam checks an optional plugin id filter.
func pluginIDParam(id string) (string, *herdrError) {
	if id == "" {
		return "", nil
	}
	n, ok := herdrplugin.NormalizeID(id)
	if !ok {
		return "", herdrErr("invalid_plugin_id", "plugin id must be non-empty, <= 120 characters, and contain only ASCII letters, digits, colon, dot, underscore, or hyphen")
	}
	return n, nil
}

// pluginRecord is a listing entry as herdr's InstalledPluginInfo. An entry
// whose manifest did not load is listed with herdr's manifest unavailable
// warning and runs nothing.
func pluginRecord(e herdrplugin.Entry) herdrplugin.Plugin {
	if e.Plugin != nil {
		p := *e.Plugin
		if e.Err != nil {
			p.Warnings = append(slices.Clone(p.Warnings), "manifest unavailable: "+e.Err.Msg)
		}
		return p
	}
	return herdrplugin.Plugin{
		PluginID: e.ID, ManifestPath: e.Path, PluginRoot: filepath.Dir(e.Path), Enabled: false,
		Source: herdrplugin.Source{Kind: "local"}, Warnings: []string{"manifest unavailable: " + e.Err.Error()},
	}
}

func (d *Daemon) herdrPluginList(_ *connState, params json.RawMessage) (any, *herdrError) {
	in, herr := decodePluginIn(params)
	if herr != nil {
		return nil, herr
	}
	id, herr := pluginIDParam(in.PluginID)
	if herr != nil {
		return nil, herr
	}
	plugins := []herdrplugin.Plugin{}
	for _, e := range d.plugins.entries(true) {
		if id == "" || e.ID == id {
			plugins = append(plugins, pluginRecord(e))
		}
	}
	return map[string]any{"type": "plugin_list", "plugins": plugins}, nil
}

// herdrPluginEntry finds a runnable plugin: loaded, enabled, not shadowed.
func (d *Daemon) herdrPluginEntry(id string) (*herdrplugin.Entry, *herdrError) {
	n, herr := pluginIDParam(id)
	if herr != nil {
		return nil, herr
	}
	e := herdrplugin.Find(d.plugins.entries(true), n)
	switch {
	case e == nil:
		return nil, herdrErr("plugin_not_found", "plugin not found")
	case e.Plugin == nil || e.Err != nil:
		return nil, herdrErr("plugin_manifest_unavailable", "plugin "+n+" manifest is unavailable")
	case !e.Enabled:
		return nil, herdrErr("plugin_disabled", "plugin "+n+" is disabled. Enable it with tuios plugins enable "+n)
	}
	return e, nil
}

// herdrPluginSetEnabled is plugin.enable and plugin.disable: the person's
// change to [plugins] enabled, written to config.toml and applied at once.
func (d *Daemon) herdrPluginSetEnabled(cs *connState, params json.RawMessage, on bool) (any, *herdrError) {
	method := "plugin.disable"
	if on {
		method = "plugin.enable"
	}
	if herr := d.herdrPluginTrust(cs, method); herr != nil {
		return nil, herr
	}
	in, herr := decodePluginIn(params, "plugin_id")
	if herr != nil {
		return nil, herr
	}
	id, herr := pluginIDParam(in.PluginID)
	if herr != nil || id == "" {
		return nil, cmpHerr(herr, herdrErr("invalid_plugin_id", "invalid plugin id"))
	}
	e := herdrplugin.Find(d.plugins.entries(true), id)
	if e == nil {
		return nil, herdrErr("plugin_not_found", "plugin not found")
	}
	if herr := d.writePlugins(func(path string) error {
		_, err := config.SetPluginEnabledInFile(path, id, on)
		return err
	}); herr != nil {
		return nil, herr
	}
	e = herdrplugin.Find(d.plugins.entries(true), id)
	kind := "plugin_disabled"
	if on {
		kind = "plugin_enabled"
	}
	return map[string]any{"type": kind, "plugin": pluginRecord(*e)}, nil
}

func cmpHerr(a, b *herdrError) *herdrError {
	if a != nil {
		return a
	}
	return b
}

// writePlugins edits config.toml with edit and applies its [plugins] table
// as the person's.
func (d *Daemon) writePlugins(edit func(path string) error) *herdrError {
	path := d.configPath
	if path == "" {
		p, err := config.GetConfigPath()
		if err != nil {
			return herdrErr("plugin_registry_save_failed", err.Error())
		}
		path = p
	}
	if err := edit(path); err != nil {
		return herdrErr("plugin_registry_save_failed", err.Error())
	}
	cfg, err := config.PluginsInFile(path)
	if err != nil {
		return herdrErr("plugin_registry_load_failed", err.Error())
	}
	d.plugins.apply(cfg, true)
	log.Printf("[PLUGINS] the person applied [plugins]: enabled %v", cfg.Enabled)
	return nil
}

// herdrPluginLink is plugin.link: list a plugin folder, as [plugins] dirs,
// and enable it unless enabled is false, as herdr does.
func (d *Daemon) herdrPluginLink(cs *connState, params json.RawMessage) (any, *herdrError) {
	if herr := d.herdrPluginTrust(cs, "plugin.link"); herr != nil {
		return nil, herr
	}
	in, herr := decodePluginIn(params, "path")
	if herr != nil {
		return nil, herr
	}
	p, perr := herdrplugin.Load(in.Path)
	if perr != nil {
		return nil, fromPluginErr(perr)
	}
	enable := in.Enabled == nil || *in.Enabled
	dir := p.PluginRoot
	if herr := d.writePlugins(func(path string) error {
		if _, err := config.AddPluginDirInFile(path, dir); err != nil {
			return err
		}
		if enable {
			_, err := config.SetPluginEnabledInFile(path, p.PluginID, true)
			return err
		}
		return nil
	}); herr != nil {
		return nil, herr
	}
	e := herdrplugin.Find(d.plugins.entries(true), p.PluginID)
	if e == nil {
		return nil, herdrErr("plugin_not_found", "plugin not found after link")
	}
	return map[string]any{"type": "plugin_linked", "plugin": pluginRecord(*e)}, nil
}

// herdrPluginUnlink is plugin.unlink: take a linked folder out of [plugins]
// dirs and the plugin out of enabled. A plugin found in the tuios plugins
// folder or in herdr's registry stays listed, disabled.
func (d *Daemon) herdrPluginUnlink(cs *connState, params json.RawMessage) (any, *herdrError) {
	if herr := d.herdrPluginTrust(cs, "plugin.unlink"); herr != nil {
		return nil, herr
	}
	in, herr := decodePluginIn(params, "plugin_id")
	if herr != nil {
		return nil, herr
	}
	id, herr := pluginIDParam(in.PluginID)
	if herr != nil || id == "" {
		return nil, cmpHerr(herr, herdrErr("invalid_plugin_id", "invalid plugin id"))
	}
	removed := false
	var dirs []string
	for _, e := range d.plugins.entries(true) {
		if e.ID == id && e.Origin == herdrplugin.OriginConfig {
			dirs = append(dirs, filepath.Dir(e.Path), e.Path)
		}
	}
	if herr := d.writePlugins(func(path string) error {
		cur, err := config.PluginsInFile(path)
		if err != nil {
			return err
		}
		for _, dir := range cur.Dirs {
			if abs, err := filepath.Abs(dir); err == nil && slices.Contains(dirs, abs) || slices.Contains(dirs, dir) {
				if ok, err := config.RemovePluginDirInFile(path, dir); err != nil {
					return err
				} else if ok {
					removed = true
				}
			}
		}
		ok, err := config.SetPluginEnabledInFile(path, id, false)
		removed = removed || ok
		return err
	}); herr != nil {
		return nil, herr
	}
	d.plugins.forgetPanes(id)
	return map[string]any{"type": "plugin_unlinked", "plugin_id": id, "removed": removed}, nil
}

// herdrActionInfo is herdr's PluginActionInfo.
type herdrActionInfo struct {
	PluginID    string   `json:"plugin_id"`
	ActionID    string   `json:"action_id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Contexts    []string `json:"contexts,omitempty"`
	Command     []string `json:"command"`
	Platforms   []string `json:"platforms,omitempty"`
}

func actionInfo(p *herdrplugin.Plugin, a herdrplugin.Action) herdrActionInfo {
	return herdrActionInfo{PluginID: p.PluginID, ActionID: a.ID, Title: a.Title, Description: a.Description, Contexts: a.Contexts, Command: a.Command, Platforms: p.EffectivePlatforms(a.Platforms)}
}

func (d *Daemon) herdrPluginActionList(_ *connState, params json.RawMessage) (any, *herdrError) {
	in, herr := decodePluginIn(params)
	if herr != nil {
		return nil, herr
	}
	id, herr := pluginIDParam(in.PluginID)
	if herr != nil {
		return nil, herr
	}
	actions := []herdrActionInfo{}
	for _, e := range d.plugins.entries(true) {
		if e.Plugin == nil || e.Err != nil || (id != "" && e.ID != id) {
			continue
		}
		for _, a := range e.Plugin.Actions {
			actions = append(actions, actionInfo(e.Plugin, a))
		}
	}
	sort.SliceStable(actions, func(i, j int) bool {
		return actions[i].PluginID+"."+actions[i].ActionID < actions[j].PluginID+"."+actions[j].ActionID
	})
	return map[string]any{"type": "plugin_action_list", "actions": actions}, nil
}

// findPluginAction finds an action as herdr does: by plugin and action id,
// or by an action id or qualified id that names one action only.
func (d *Daemon) findPluginAction(pluginID, actionID string) (*herdrplugin.Entry, herdrplugin.Action, *herdrError) {
	if pluginID != "" {
		e, herr := d.herdrPluginEntryAny(pluginID)
		if herr != nil {
			return nil, herdrplugin.Action{}, herr
		}
		aid, ok := herdrplugin.NormalizeLocalID(actionID)
		if !ok {
			return nil, herdrplugin.Action{}, herdrErr("invalid_plugin_action_id", "invalid action id")
		}
		a, ok := e.Plugin.Action(aid)
		if !ok {
			return nil, herdrplugin.Action{}, herdrErr("plugin_action_not_found", "plugin action not found")
		}
		return e, a, nil
	}
	actionID = strings.TrimSpace(actionID)
	var found []*herdrplugin.Entry
	var acts []herdrplugin.Action
	entries := d.plugins.entries(true)
	for i := range entries {
		e := &entries[i]
		if e.Plugin == nil || e.Err != nil {
			continue
		}
		for _, a := range e.Plugin.Actions {
			if a.ID == actionID || e.ID+"."+a.ID == actionID {
				found, acts = append(found, e), append(acts, a)
			}
		}
	}
	switch len(found) {
	case 0:
		return nil, herdrplugin.Action{}, herdrErr("plugin_action_not_found", "plugin action not found")
	case 1:
		return found[0], acts[0], nil
	}
	return nil, herdrplugin.Action{}, herdrErr("ambiguous_plugin_action", "plugin action id matches more than one action; include plugin_id")
}

// herdrPluginEntryAny finds a loaded plugin, enabled or not.
func (d *Daemon) herdrPluginEntryAny(id string) (*herdrplugin.Entry, *herdrError) {
	n, herr := pluginIDParam(id)
	if herr != nil {
		return nil, herr
	}
	e := herdrplugin.Find(d.plugins.entries(true), n)
	switch {
	case e == nil:
		return nil, herdrErr("plugin_not_found", "plugin not found")
	case e.Plugin == nil || e.Err != nil:
		return nil, herdrErr("plugin_manifest_unavailable", "plugin "+n+" manifest is unavailable")
	}
	return e, nil
}

func (d *Daemon) herdrPluginActionInvoke(cs *connState, params json.RawMessage) (any, *herdrError) {
	in, herr := decodePluginIn(params, "action_id")
	if herr != nil {
		return nil, herr
	}
	if herr := d.herdrPluginMayRun(cs); herr != nil {
		return nil, herr
	}
	e, a, herr := d.findPluginAction(in.PluginID, in.ActionID)
	if herr != nil {
		return nil, herr
	}
	if !e.Enabled {
		return nil, herdrErr("plugin_disabled", "plugin "+e.ID+" is disabled. Enable it with tuios plugins enable "+e.ID)
	}
	info := actionInfo(e.Plugin, a)
	if perr := e.Plugin.Supported(a.Platforms, "action '"+e.ID+"."+a.ID+"'"); perr != nil {
		return nil, fromPluginErr(perr)
	}
	ctx := d.pluginContextCurrent("plugin.action.invoke")
	if in.Context != nil && in.Context.FocusedPaneID != "" {
		if c, ok := d.pluginContextForPane(in.Context.FocusedPaneID, "plugin.action.invoke"); ok {
			// The caller may name the pane by its tuios window id. The
			// context carries herdr's id for it.
			ctx = c
			in.Context.FocusedPaneID = ""
		}
	}
	ctx = ctx.merge(in.Context)
	log, perr := d.plugins.run(e.Plugin, a.ID, "", a.Command, ctx, "", herdrplugin.DefaultTimeout)
	if perr != nil {
		return nil, fromPluginErr(perr)
	}
	return map[string]any{"type": "plugin_action_invoked", "action": info, "context": ctx, "log": log}, nil
}

func (d *Daemon) herdrPluginLogList(_ *connState, params json.RawMessage) (any, *herdrError) {
	in, herr := decodePluginIn(params)
	if herr != nil {
		return nil, herr
	}
	id, herr := pluginIDParam(in.PluginID)
	if herr != nil {
		return nil, herr
	}
	logs := d.plugins.runner.Logs(id, in.Limit)
	if logs == nil {
		logs = []herdrplugin.LogEntry{}
	}
	return map[string]any{"type": "plugin_log_list", "logs": logs}, nil
}

// pluginPaneProtected are the variables a caller's env may not set for a
// plugin pane, as in herdr.
var pluginPaneProtected = []string{
	"HERDR_SOCKET_PATH", "HERDR_ENV", "HERDR_PLUGIN_ID", "HERDR_PLUGIN_ROOT",
	"HERDR_PLUGIN_CONFIG_DIR", "HERDR_PLUGIN_STATE_DIR", "HERDR_PLUGIN_ENTRYPOINT_ID",
	"HERDR_PLUGIN_CONTEXT_JSON", "HERDR_BIN_PATH",
}

// overlaySize is the popup size of an overlay pane: herdr draws an overlay
// over the active pane's area, and tuios has a popup for it.
const overlaySize = "90%"

// herdrPluginPaneOpen is plugin.pane.open. The pane is an ordinary tuios
// pane that runs the entry's command, placed as the entry asks:
//
//	popup    a popup, sized by width and height
//	overlay  a popup sized 90% by 90%
//	split    a new pane on the target pane's workspace, which a tiling
//	         client places beside it
//	zoomed   split, then zoomed
//	tab      a new pane on the first free workspace
func (d *Daemon) herdrPluginPaneOpen(cs *connState, params json.RawMessage) (any, *herdrError) {
	in, herr := decodePluginIn(params, "plugin_id", "entrypoint")
	if herr != nil {
		return nil, herr
	}
	if herr := d.herdrPluginMayRun(cs); herr != nil {
		return nil, herr
	}
	e, herr := d.herdrPluginEntry(in.PluginID)
	if herr != nil {
		return nil, herr
	}
	entry, ok := herdrplugin.NormalizeLocalID(in.Entrypoint)
	if !ok {
		return nil, herdrErr("invalid_plugin_entrypoint", "invalid entrypoint id")
	}
	pane, ok := e.Plugin.Pane(entry)
	if !ok {
		return nil, herdrErr("plugin_pane_not_found", "plugin pane entrypoint '"+entry+"' not found")
	}
	if perr := e.Plugin.Supported(pane.Platforms, "plugin pane"); perr != nil {
		return nil, fromPluginErr(perr)
	}
	placement := pane.Placement
	if in.Placement != "" {
		if !slices.Contains(herdrplugin.Placements, in.Placement) {
			return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+echoName(in.Placement)+"`")
		}
		placement = in.Placement
	}
	if placement != herdrplugin.PlacementPopup && (in.Width != nil || in.Height != nil) {
		return nil, herdrErr("invalid_params", "width and height are only supported when placement is popup")
	}
	switch placement {
	case herdrplugin.PlacementOverlay, herdrplugin.PlacementPopup:
		if in.Workspace != "" || in.Target != "" || in.Direction != "" {
			return nil, herdrErr("invalid_params", "overlay and popup plugin panes target the active pane")
		}
	case herdrplugin.PlacementSplit, herdrplugin.PlacementZoomed:
		if in.Workspace != "" {
			return nil, herdrErr("invalid_params", "split and zoomed plugin panes target an existing pane; use target_pane_id")
		}
	case herdrplugin.PlacementTab:
		if in.Target != "" || in.Direction != "" {
			return nil, herdrErr("invalid_params", "tab plugin panes support workspace_id but not target_pane_id or direction")
		}
	}

	// Where the pane goes: the target pane's session and workspace, or the
	// focused session's.
	var sess *Session
	workspace := 0
	switch {
	case in.Target != "":
		s, win, ierr := d.herdrFindPane(in.Target)
		if ierr != nil {
			return nil, ierr.herdr()
		}
		sess, workspace = s, max(win.Workspace, 1)
	default:
		s, herr := d.herdrSessionOrFocused(cs, in.Workspace)
		if herr != nil {
			return nil, herr
		}
		sess = s
		st := s.GetState()
		workspace = max(st.CurrentWorkspace, 1)
		if w, ok := findWindowState(st, st.FocusedWindowID); ok && placement != herdrplugin.PlacementTab {
			workspace = max(w.Workspace, 1)
		}
	}
	verb := "new-window"
	popup := placement == herdrplugin.PlacementPopup || placement == herdrplugin.PlacementOverlay
	if popup {
		verb = "popup"
		if !d.hasTUIClient(sess) {
			return nil, herdrErr("no_client", "a popup is drawn on a screen, so it needs an attached client. Attach one with tuios attach "+sess.Name()+", then try again")
		}
	}
	if herr := d.herdrAdmit(cs, verb, sess); herr != nil {
		return nil, herr
	}
	if placement == herdrplugin.PlacementTab {
		workspace = freeWorkspace(sess.GetState())
	}

	dirs := d.plugins.pluginDirs()
	if err := dirs.EnsureUserDirs(e.ID); err != nil {
		return nil, herdrErr("plugin_user_dir_create_failed", err.Error())
	}
	ctx := d.pluginContextFor(sess, sess.GetState().FocusedWindowID, "plugin-pane")
	if in.Target != "" {
		if c, ok := d.pluginContextForPane(in.Target, "plugin-pane"); ok {
			ctx = c
		}
	}
	ctxJSON, _ := json.Marshal(ctx)
	cwd := in.Cwd
	if cwd == "" {
		cwd = e.Plugin.PluginRoot
	}
	var env []string
	keys := make([]string, 0, len(in.Env))
	for k := range in.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(in.Env[k], 0) {
			return nil, herdrErr("invalid_params", "env keys must be non-empty and hold no = or NUL")
		}
		if !slices.Contains(pluginPaneProtected, k) {
			env = append(env, k+"="+in.Env[k])
		}
	}
	env = append(env, "PWD="+cwd)
	env = append(env, d.plugins.baseEnv(e.Plugin, dirs)...)
	env = append(env, "HERDR_PLUGIN_ENTRYPOINT_ID="+pane.ID, "HERDR_PLUGIN_CONTEXT_JSON="+string(ctxJSON))

	argv := slices.Clone(pane.Command)
	if strings.ContainsAny(argv[0], `/\`) && !filepath.IsAbs(argv[0]) {
		// herdr's program_for_cwd: a relative path with a separator is the
		// plugin root's, whatever the pane's cwd.
		argv[0] = filepath.Join(e.Plugin.PluginRoot, argv[0])
	}
	grants, verr := d.launchGrants(cs, nil)
	if verr != nil {
		return nil, herdrFromVerb(verr, "plugin_pane_open_failed")
	}
	focus := in.Focus || popup || placement == herdrplugin.PlacementZoomed
	opts := NewWindowOptions{
		Title: pane.Title, Name: pane.Title, Cwd: cwd, Workspace: workspace, Focus: focus,
		Command: argv, Env: env, Grants: grants,
	}
	if popup {
		opts.Popup = true
		opts.PopupWidth, opts.PopupHeight = overlaySize, overlaySize
		if placement == herdrplugin.PlacementPopup {
			opts.PopupWidth, opts.PopupHeight = "", ""
			if w := cmpSize(in.Width, pane.Width); w != "" {
				opts.PopupWidth = w
			}
			if h := cmpSize(in.Height, pane.Height); h != "" {
				opts.PopupHeight = h
			}
		}
	}
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sess.ID, ptyID)
		d.closeWindowOfPTY(sess, ptyID)
	}
	if _, err := os.Stat(cwd); err != nil {
		return nil, herdrErr("plugin_pane_open_failed", "cannot start the pane in "+echoName(cwd)+": "+err.Error())
	}
	win, err := sess.AddDaemonWindowWith(opts, onExit)
	if err != nil {
		if errors.Is(err, ErrScratchExists) {
			return nil, herdrErr("ui_busy", err.Error())
		}
		return nil, herdrErr("plugin_pane_open_failed", err.Error())
	}
	d.notePaneCreator(cs, win.ID)
	d.plugins.mu.Lock()
	d.plugins.panes[win.ID] = pluginPane{pluginID: e.ID, entrypoint: pane.ID}
	d.plugins.mu.Unlock()
	if win.Unplaced && d.findTUIClient(sess.ID) != nil {
		d.awaitPlacement(sess, win.ID, win.PTYID, newWindowPlaceWait)
	}
	paneID := herdrPaneID(sess.ID, win.ID)
	if placement == herdrplugin.PlacementZoomed {
		if _, herr := d.herdrPaneZoom(cs, &herdrIn{PaneID: paneID, Mode: "on"}); herr != nil {
			log.Printf("[PLUGINS] the pane of %s opened, and zoom failed: %s", e.ID, herr.msg)
		}
	}
	if placement == herdrplugin.PlacementPopup {
		// herdr answers a popup with ok and no pane.
		return herdrAck, nil
	}
	res, herr := d.herdrPaneResult(cs, "pane_info", paneID)
	if herr != nil {
		return nil, herr
	}
	return map[string]any{"type": "plugin_pane_opened", "plugin_pane": map[string]any{"plugin_id": e.ID, "entrypoint": pane.ID, "pane": res.Pane}}, nil
}

func cmpSize(a, b *herdrplugin.Size) string {
	switch {
	case a != nil:
		return string(*a)
	case b != nil:
		return string(*b)
	}
	return ""
}

// freeWorkspace is the first workspace of a session that holds no window,
// herdr's new tab. A session with every workspace in use gets its current
// one.
func freeWorkspace(st *SessionState) int {
	used := map[int]bool{}
	for _, w := range st.Windows {
		used[max(w.Workspace, 1)] = true
	}
	for n := 1; n <= 9; n++ {
		if !used[n] {
			return n
		}
	}
	return max(st.CurrentWorkspace, 1)
}

// pluginPaneOf finds a pane plugin.pane.open made.
func (d *Daemon) pluginPaneOf(id string) (*Session, WindowState, pluginPane, *herdrError) {
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return nil, WindowState{}, pluginPane{}, herdrErr("plugin_pane_not_found", "plugin pane not found")
	}
	d.plugins.mu.Lock()
	rec, ok := d.plugins.panes[win.ID]
	d.plugins.mu.Unlock()
	if !ok {
		return nil, WindowState{}, pluginPane{}, herdrErr("plugin_pane_not_found", "plugin pane not found")
	}
	return sess, win, rec, nil
}

func (d *Daemon) herdrPluginPaneFocus(cs *connState, params json.RawMessage) (any, *herdrError) {
	in, herr := decodePluginIn(params, "pane_id")
	if herr != nil {
		return nil, herr
	}
	sess, win, rec, herr := d.pluginPaneOf(in.PaneID)
	if herr != nil {
		return nil, herr
	}
	res, herr := d.herdrFocusPane(cs, herdrPaneID(sess.ID, win.ID))
	if herr != nil {
		return nil, herr
	}
	return map[string]any{"type": "plugin_pane_focused", "plugin_pane": map[string]any{"plugin_id": rec.pluginID, "entrypoint": rec.entrypoint, "pane": res.Pane}}, nil
}

func (d *Daemon) herdrPluginPaneClose(cs *connState, params json.RawMessage) (any, *herdrError) {
	in, herr := decodePluginIn(params, "pane_id")
	if herr != nil {
		return nil, herr
	}
	sess, win, _, herr := d.pluginPaneOf(in.PaneID)
	if herr != nil {
		return nil, herr
	}
	if _, herr := d.herdrVerb(cs, "close-window", herdrWin(sess, win.ID), "pane_close_failed"); herr != nil {
		return nil, herr
	}
	d.plugins.mu.Lock()
	delete(d.plugins.panes, win.ID)
	d.plugins.mu.Unlock()
	return map[string]any{"type": "plugin_pane_closed", "pane_id": in.PaneID}, nil
}

// herdrPopupClose is popup.close: the popup the caller runs in, or, for a
// caller in no popup, the popup of the focused session.
func (d *Daemon) herdrPopupClose(cs *connState, _ json.RawMessage) (any, *herdrError) {
	var sess *Session
	var win WindowState
	if window, _ := d.placePaneWindow(cs); window != "" {
		if s := d.sessionHoldingWindow(window); s != nil {
			if w, ok := findWindowState(s.GetState(), window); ok && w.Popup && !w.Scratch {
				sess, win = s, w
			}
		}
	}
	if sess == nil {
		s := d.herdrFocusedSession(d.herdrSessions(cs))
		if s != nil {
			for _, w := range s.GetState().Windows {
				if w.Popup && !w.Scratch {
					sess, win = s, w
				}
			}
		}
	}
	if sess == nil {
		return nil, herdrErr("popup_not_found", "no popup is open")
	}
	if _, herr := d.herdrVerb(cs, "close-window", herdrWin(sess, win.ID), "popup_close_failed"); herr != nil {
		return nil, herr
	}
	return herdrAck, nil
}

// forgetPanes drops the pane records of a plugin. The panes keep running.
func (h *pluginHost) forgetPanes(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for w, p := range h.panes {
		if p.pluginID == id {
			delete(h.panes, w)
		}
	}
}
