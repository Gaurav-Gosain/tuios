package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

// A change to which plugins run, asked for from inside a pane.
//
// tuios plugins install, update, uninstall, approve, enable, disable, link,
// unlink and build change what runs with the person's rights, so a process
// inside a pane does not get to make one by asking (herdrPluginTrust). But
// someone who works entirely inside tuios has no terminal outside it, so the
// CLI in a pane asks the daemon first, with plugin-confirm, and the daemon
// asks the person where they are: in the dialog a live upgrade from a pane
// uses (upgrade_confirm.go), on every attached client of this machine.
//
// The ask is an AttentionItem with Plugin set, so it has the upgrade ask's
// proof that a person answered: answer-ask takes it only with the nonce of a
// client attached right now, from a process outside every pane, never over a
// link, and only marked as a key pressed in the dialog. The client takes
// only a key the person pressed there (internal/app/upgrade_confirm.go).
//
// What the dialog shows is what the daemon reads itself: the manifest in the
// folder the CLI names, or the plugin the daemon lists under the id, with
// what it runs and its grants in the words of the approval screen. The
// source of a git install is the one thing the CLI says that the daemon
// cannot check, since the daemon never fetches: the dialog shows it with the
// folder the manifest was read from.
//
// A Yes gives the CLI a token. The trust verbs of that change take it, for
// that plugin, from the same pane, for pluginConfirmLife. No, Esc, the wait
// running out and no client that could answer refuse it. A plugin that asks
// for admin is refused without a dialog: the approval screen has the person
// type admin, which a Yes in a dialog is not. A plugin, and a call over a
// link, are refused without a dialog, and a caller outside every pane needs
// none.

// pluginConfirmWait is how long a pane's change waits for the person.
const pluginConfirmWait = 2 * time.Minute

// PluginConfirmWait is pluginConfirmWait, for the CLI's call timeout.
const PluginConfirmWait = pluginConfirmWait

// pluginConfirmLife is how long a Yes lets its change run.
const pluginConfirmLife = 10 * time.Minute

// pluginChanges are the changes plugin-confirm asks about, with the trust
// verbs a Yes lets run. A change with no verb runs in the CLI alone.
var pluginChanges = map[string][]string{
	"install":   {"plugin.restart", "plugin.enable"},
	"update":    {"plugin.restart", "plugin.enable"},
	"uninstall": {"plugin.disable"},
	"approve":   {"plugin.approve"},
	"deny":      {"plugin.approve"},
	"enable":    {"plugin.enable"},
	"disable":   {"plugin.disable"},
	"link":      {"plugin.link"},
	"unlink":    {"plugin.unlink"},
	"build":     nil,
}

// PluginTrustAsk is what the dialog shows: the change, and the plugin as the
// daemon reads it.
type PluginTrustAsk struct {
	// Change is one of pluginChanges.
	Change string `json:"change"`
	// Folder is the folder the manifest was read from.
	Folder string `json:"folder,omitempty"`
	PluginApprovalAsk
}

// pluginConfirmGrant is one Yes: the pane it was asked from, the plugin, and
// the verbs it lets run until it ends.
type pluginConfirmGrant struct {
	window, pluginID, root, change string
	until                          time.Time
	// pin is what the dialog showed, for a change that lets the plugin
	// run (plugin_confirm_pin.go). It is nil for one that stops it.
	pin *pluginPin
}

// pinnedChanges are the changes whose Yes holds to what the dialog showed.
var pinnedChanges = map[string]bool{"install": true, "update": true, "approve": true, "enable": true, "link": true}

// pluginConfirms are the grants that have not ended.
type pluginConfirms struct {
	mu     sync.Mutex
	grants map[string]pluginConfirmGrant
}

func (p *pluginConfirms) put(g pluginConfirmGrant) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.grants == nil {
		p.grants = map[string]pluginConfirmGrant{}
	}
	now := time.Now()
	for k, old := range p.grants {
		if now.After(old.until) {
			delete(p.grants, k)
		}
	}
	p.grants[token] = g
	return token, nil
}

func (p *pluginConfirms) get(token string) (pluginConfirmGrant, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, ok := p.grants[token]
	if !ok || time.Now().After(g.until) {
		return pluginConfirmGrant{}, false
	}
	return g, true
}

// pluginConfirmTimeout is pluginConfirmWait, or a test's shorter one.
func pluginConfirmTimeout() time.Duration {
	if handoffTestOn() {
		if d := testknob.Millis("plugin_confirm_ms"); d > 0 {
			return d
		}
	}
	return pluginConfirmWait
}

// pluginConfirmed reports whether token is a Yes that lets the pane on cs run
// method on the plugin params names. why, when set, is the refusal to show:
// the plugin changed after the Yes.
func (d *Daemon) pluginConfirmed(cs *connState, method string, params json.RawMessage) (ok bool, why string) {
	var in struct {
		Token    string `json:"confirm_token"`
		PluginID string `json:"plugin_id"`
		Path     string `json:"path"`
		Allow    *bool  `json:"allow"`
	}
	if cs == nil || cs.viaLink || d.callerIsPlugin(cs) || jsonx.Unmarshal(params, &in) != nil || in.Token == "" {
		return false, ""
	}
	g, found := d.pluginConfirm.get(in.Token)
	if !found || !slices.Contains(pluginChanges[g.change], method) {
		return false, ""
	}
	if _, window := d.peerPane(cs); window == "" || window != g.window {
		return false, ""
	}
	if method == "plugin.approve" && (in.Allow == nil || *in.Allow != (g.change == "approve")) {
		// A Yes to deny a plugin does not allow it, nor the other way.
		return false, ""
	}
	root := ""
	switch {
	case method == "plugin.link":
		if in.Path == "" || in.Path != g.root {
			return false, ""
		}
		root = in.Path
	case in.PluginID == "" || in.PluginID != g.pluginID:
		return false, ""
	default:
		if e := herdrplugin.Find(d.plugins.entries(true), in.PluginID); e != nil && e.Plugin != nil {
			root = e.Plugin.PluginRoot
		}
	}
	if g.pin == nil {
		return true, ""
	}
	what := "tuios cannot find its folder"
	if root != "" {
		what = g.pin.changedIn(root)
	}
	if what == "" {
		return true, ""
	}
	LogBasic("Plugin %s of %s from pane %s refused: it changed after the Yes: %s", g.change, g.pluginID, g.window, what)
	return false, method + " is refused: the plugin " + g.pluginID + " changed after the person said yes, so " + what + ". Nothing was changed. Run the command again, so the person sees the plugin as it is now"
}

// verbPluginConfirm asks the person whether the pane on cs may make a
// change to which plugins run, and gives a token when they say yes.
func (d *Daemon) verbPluginConfirm(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Change   string `json:"change"`
		PluginID string `json:"plugin_id"`
		Path     string `json:"path"`
		Source   string `json:"source"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if _, ok := pluginChanges[p.Change]; !ok {
		return nil, invalidParam("change", "change must be one of install, update, uninstall, approve, deny, enable, disable, link, unlink or build")
	}
	if p.PluginID == "" && p.Path == "" {
		return nil, invalidParam("plugin_id", "name the plugin with plugin_id, or its folder with path")
	}
	refuse := func(why string) *verbError {
		LogBasic("Plugin %s from a pane refused: %s", p.Change, why)
		return hintedVerbError(ErrVerbForbidden, "tuios plugins "+p.Change+" is for the person, and from a pane it needs their Yes in a dialog at an attached client. Nothing was changed. "+why+".", &VerbHint{
			Detail: "The dialog opens on every attached client and shows the plugin, where it comes from, what it runs and its grants. From a terminal outside tuios, the command needs no dialog.",
		})
	}
	switch {
	case cs == nil:
		return nil, newVerbError(ErrVerbForbidden, "plugin-confirm needs a connection")
	case cs.viaLink:
		return nil, refuse("A change to the plugins of a machine is asked on that machine")
	case d.callerIsPlugin(cs):
		return nil, refuse("A plugin cannot change which plugins run, and no dialog is shown for one. Run the command from a terminal outside tuios")
	case d.mayActAsHuman(cs):
		// The person, outside every pane, needs no dialog.
		return map[string]any{"type": "plugin_confirmed", "needed": false}, nil
	}
	ask, why := d.pluginTrustAsk(p.Change, p.PluginID, p.Path, p.Source)
	if why != "" {
		return nil, refuse(why)
	}
	if slices.Contains(ask.Grants, "admin") && (p.Change == "install" || p.Change == "update" || p.Change == "approve" || p.Change == "enable" || p.Change == "link") {
		return nil, refuse("The plugin " + ask.ID + " asks for the admin grant, which the person gives only by typing admin. Run the command from a terminal outside tuios")
	}
	_, window := d.peerPane(cs)
	if window == "" {
		return nil, refuse("tuios cannot tell which pane asks. Run the command from a terminal outside tuios")
	}
	var pin *pluginPin
	if pinnedChanges[p.Change] {
		// Read before the dialog opens, so the Yes holds to what it shows.
		got, err := pinPlugin(ask.Folder)
		if err != nil {
			return nil, refuse("tuios cannot read the plugin folder to show it (" + strings.TrimSuffix(err.Error(), ".") + "). Run the command from a terminal outside tuios")
		}
		pin = &got
	}
	if why := d.pluginDialogRefusal(); why != "" {
		return nil, refuse(why)
	}
	if why := d.confirmPluginTrust(window, ask); why != "" {
		return nil, refuse(why)
	}
	token, err := d.pluginConfirm.put(pluginConfirmGrant{window: window, pluginID: ask.ID, root: ask.Folder, change: p.Change, until: time.Now().Add(pluginConfirmLife), pin: pin})
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not make a token: "+err.Error())
	}
	LogBasic("Plugin %s of %s from pane %s: the person said yes", p.Change, ask.ID, window)
	return map[string]any{"type": "plugin_confirmed", "needed": true, "token": token, "plugin_id": ask.ID}, nil
}

// pluginTrustAsk reads the plugin the change is about: the manifest in path,
// or the plugin listed under id. why says what is wrong when it cannot.
func (d *Daemon) pluginTrustAsk(change, id, path, source string) (PluginTrustAsk, string) {
	ask := PluginTrustAsk{Change: change}
	if path != "" {
		p, perr := herdrplugin.Load(path)
		if perr != nil {
			return ask, "The plugin in " + path + " cannot be read: " + strings.TrimSuffix(perr.Msg, ".")
		}
		if id != "" && id != p.PluginID {
			return ask, "The folder holds the plugin " + p.PluginID + ", not " + id
		}
		ask.Folder = p.PluginRoot
		src := "folder " + p.PluginRoot
		if source != "" && askLinePrintable(source) {
			src = source
		}
		ask.PluginApprovalAsk = PluginApprovalAsk{ID: p.PluginID, Name: p.Name, Version: p.Version, Source: src,
			Trust: p.Trust, Grants: slices.Clone(p.Grants), Runs: herdrplugin.RunsLines(p)}
		return ask, ""
	}
	e := herdrplugin.Find(d.plugins.entries(true), id)
	if e == nil {
		return ask, "No plugin has the id " + id + ". Run tuios plugins list to see the ids"
	}
	ask.PluginApprovalAsk = pluginAskOf(*e)
	if e.Plugin != nil {
		ask.Folder = e.Plugin.PluginRoot
	}
	return ask, ""
}

// pluginDialogRefusal says why no dialog could be shown for a pane's plugin
// change right now, and "" when one can.
func (d *Daemon) pluginDialogRefusal() string {
	if d.dialogClients(func(cs *connState) bool { return cs.pluginDialog }) == 0 {
		return "No client is attached to show the dialog. Run the command from a terminal outside tuios, or attach a client and run it again"
	}
	if !d.agentsEnabled() {
		return "The agent features are off, so no client shows the dialog. Run the command from a terminal outside tuios"
	}
	return ""
}

// pluginChangeWords is the dialog's question for each change.
var pluginChangeWords = map[string]string{
	"install":   "Install the plugin %s?",
	"update":    "Update the plugin %s?",
	"uninstall": "Uninstall the plugin %s?",
	"approve":   "Allow the plugin %s?",
	"deny":      "Deny the plugin %s?",
	"enable":    "Enable the plugin %s?",
	"disable":   "Disable the plugin %s?",
	"link":      "Link the plugin %s?",
	"unlink":    "Unlink the plugin %s?",
	"build":     "Build the plugin %s?",
}

// PluginChangeQuestion is the dialog's first line for a change.
func PluginChangeQuestion(change, name string) string {
	return fmt.Sprintf(pluginChangeWords[change], name)
}

// confirmPluginTrust asks the person in the dialog. It returns "" when they
// said yes, and otherwise why not, as a sentence that says what to do next.
func (d *Daemon) confirmPluginTrust(window string, ask PluginTrustAsk) string {
	name := herdrplugin.ShownName(ask.Name)
	if name == "" {
		name = ask.ID
	}
	item := AttentionItem{
		Name:    "a process inside a pane",
		Summary: PluginChangeQuestion(ask.Change, name),
		Options: []string{upgradeAnswerNo, upgradeAnswerYes},
		Plugin:  &ask,
	}
	for _, sess := range d.manager.AllSessions() {
		if item.Session == "" {
			item.Session = sess.Name()
		}
		st := sess.GetState()
		if idx, err := findWindowStateIndex(st.Windows, window); err == nil {
			w := st.Windows[idx]
			item.Session, item.Window, item.Name = sess.Name(), w.ID, windowLabelOf(w)
			item.Harness, item.Workspace = w.AgentHarness, w.Workspace
			break
		}
	}
	hold, err := d.attention.openAsk(item, true)
	if err != nil {
		return fmt.Sprintf("The dialog could not be opened (%v). Run the command again", err)
	}
	timer := time.NewTimer(pluginConfirmTimeout())
	defer timer.Stop()
	var out askOutcome
	select {
	case out = <-hold.done:
	case <-timer.C:
		out, _ = d.attention.closeAsk(hold, approvalEndTimeout)
	case <-d.ctx.Done():
		out, _ = d.attention.closeAsk(hold, askEndShutdown)
	}
	switch {
	case out.Reason == AskAnswered && out.Answer == upgradeAnswerYes:
		return ""
	case out.Reason == AskAnswered:
		return "The person answered No. Do not ask again unless they ask for the change"
	case out.Reason == approvalEndTimeout:
		return fmt.Sprintf("Nobody answered the dialog within %d seconds. Run the command again, and ask the person to choose Yes in the dialog", int(pluginConfirmTimeout().Round(time.Second)/time.Second))
	case out.Reason == askEndShutdown:
		return "The daemon is shutting down"
	}
	return fmt.Sprintf("The dialog closed without an answer (%s). Run the command again, and ask the person to choose Yes in the dialog", out.Reason)
}
