package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

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
		return nil
	}
	if prev := cs.pluginBound.Load(); prev != nil && *prev != id {
		return refuse("this connection already presented another plugin's token")
	}
	cs.pluginBound.Store(&id)
	return nil
}

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
