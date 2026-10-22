package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// Plugin tokens: a second way for a process to say which native plugin it
// belongs to.
//
// The first way is the kernel's: the session id of the caller's pid, which
// the runner's owner table names (plugin_grants.go). It holds whatever the
// process sends. A process that starts a session of its own leaves it, and
// where the kernel gives no peer pid (Windows, the BSDs) there is no answer.
//
// For those, every process of a native plugin gets TUIOS_PLUGIN_ID and
// TUIOS_PLUGIN_TOKEN, an HMAC of the plugin id under the key the pane
// tokens use (pane_token.go): one daemon start, never written anywhere. The
// tuios CLI presents them with pane-grants. A connection that presents a
// good token is that plugin for as long as it is open: held to its grants
// and never the person.
//
// A token only narrows. A caller the kernel places in another plugin is
// refused, and one the kernel places in a pane stays the pane's. A caller
// that presents nothing is placed as before, so a process that drops the
// variable on purpose is not held by it: as with pane tokens, this scopes
// mistakes and tools talked into misuse, not a program set on escaping.
// That is why the runner still does not start a native plugin's commands
// where the kernel gives no peer pid: the client protocol does not present
// the token yet.

// PluginToken is the token the processes of plugin id are started with, or
// "" when this manager has no key.
func (m *Manager) PluginToken(id string) string {
	if len(m.paneTokenKey) == 0 || id == "" {
		return ""
	}
	mac := hmac.New(sha256.New, m.paneTokenKey)
	mac.Write([]byte("tuios-plugin-token\x00" + id))
	return hex.EncodeToString(mac.Sum(nil)[:paneTokenBytes])
}

// VerifyPluginToken reports whether token is the one plugin id's processes
// were started with. The comparison takes the same time whatever the token.
func (m *Manager) VerifyPluginToken(id, token string) bool {
	want := m.PluginToken(id)
	if want == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(want), []byte(token))
}

// bindPluginToken places the connection in plugin id when token proves it
// and nothing the kernel says disagrees.
func (d *Daemon) bindPluginToken(cs *connState, id, token string) *verbError {
	refuse := func(msg string) *verbError {
		return hintedVerbError(ErrVerbForbidden, msg, &VerbHint{
			Param:  "plugin_token",
			Detail: "Pass the TUIOS_PLUGIN_TOKEN of the plugin named by TUIOS_PLUGIN_ID, from the same process's environment. A token is good for one plugin of one daemon start.",
		})
	}
	switch {
	case cs == nil:
		return nil
	case cs.viaLink || cs.paneOnly:
		return refuse("a plugin token is good only on the machine that runs the plugin")
	case !d.manager.VerifyPluginToken(id, token):
		return refuse("plugin_token does not prove plugin_id")
	}
	if p := d.pluginOfPeer(cs); p != nil {
		if p.PluginID != id {
			return refuse("this process runs in plugin " + p.PluginID + ", not in " + id)
		}
		cs.pluginBound.Store(&id)
		return nil
	}
	if _, win := d.peerPane(cs); win != "" {
		// A plugin's pane: the pane's grants hold it, which are no more
		// than the plugin's.
		return nil
	}
	if prev := cs.pluginBound.Load(); prev != nil && *prev != id {
		return refuse("this connection already presented another plugin's token")
	}
	cs.pluginBound.Store(&id)
	return nil
}

// bindPeerPluginToken places the connection in the plugin whose token the
// peer process was started with, read from its environment at accept. It
// needs nothing from the caller, so a raw socket call and the client
// protocol are held too. Where the environment cannot be read (Windows, the
// BSDs), the CLI presents the token with pane-grants instead.
func (d *Daemon) bindPeerPluginToken(cs *connState) {
	if cs == nil || cs.viaLink || cs.paneOnly || cs.peerPID <= 0 {
		return
	}
	if !cs.peerEnvOK {
		return
	}
	id, ok := cs.peerEnv.Get("TUIOS_PLUGIN_ID")
	if !ok || id == "" {
		return
	}
	tok, _ := cs.peerEnv.Get("TUIOS_PLUGIN_TOKEN")
	if verr := d.bindPluginToken(cs, id, tok); verr != nil {
		LogBasic("Client %s (pid %d) names plugin %s and is not placed in it: %s", cs.clientID, cs.peerPID, id, verr.Message)
	}
}

// boundPluginGrants is what plugin id holds now: none when it is not a
// native plugin that runs.
func (d *Daemon) boundPluginGrants(id string) Grants {
	if d.plugins == nil {
		return 0
	}
	e := herdrplugin.Find(d.plugins.entries(false), id)
	if e == nil || !e.Runnable() || e.Plugin.Format != herdrplugin.FormatTuios {
		return 0
	}
	return grantsFromNames(e.Plugin.Held)
}
