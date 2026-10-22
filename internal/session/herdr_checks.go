package session

import "slices"

// How each herdr socket method is held to what its caller may do.
//
// The JSON verbs are classified in verbScopes and verbCapabilities, and a
// verb missing there is refused. The herdr socket gets the same rule here:
// every method it answers names its check, a method with none is refused
// before its handler runs, and TestHerdrMethodsNameTheirCheck fails until a
// new method is added. The checks of the classes marked "in the handler" run
// there; this table records that they exist. The herdr socket is local only,
// so no class has to think about a link.

// herdrCheck is the check one herdr method gets.
type herdrCheck uint8

const (
	// herdrUnclassified is a method missing from the table: refused.
	herdrUnclassified herdrCheck = iota
	// herdrOpen touches nothing: a pong, or a no-op answer.
	herdrOpen
	// herdrRead reads records of this tuios that belong to no session:
	// installed plugins, their actions, the agent manifests. A pane or a
	// plugin needs the read grant. Checked here.
	herdrRead
	// herdrVerbs works through admitVerb or callVerb, which hold the caller
	// to its pane or plugin grants as the matching tuios verb does.
	herdrVerbs
	// herdrTrust changes which plugins run: only the person
	// (herdrPluginTrust, in the handler).
	herdrTrust
	// herdrAdmin runs a plugin command or reads its log: a pane or a plugin
	// needs admin (herdrPluginMayRun and the log check, in the handler).
	herdrAdmin
	// herdrReport reports for the caller's own pane (herdr_compat.go).
	herdrReport
)

// herdrChecks classifies every method the herdr socket answers.
var herdrChecks = map[string]herdrCheck{
	"ping":                         herdrOpen,
	"session.snapshot":             herdrVerbs,
	"workspace.list":               herdrVerbs,
	"workspace.get":                herdrVerbs,
	"workspace.create":             herdrVerbs,
	"workspace.rename":             herdrVerbs,
	"workspace.close":              herdrVerbs,
	"tab.list":                     herdrVerbs,
	"tab.get":                      herdrVerbs,
	"tab.create":                   herdrVerbs,
	"tab.rename":                   herdrVerbs,
	"tab.focus":                    herdrVerbs,
	"tab.move":                     herdrVerbs,
	"tab.close":                    herdrVerbs,
	"pane.list":                    herdrVerbs,
	"pane.get":                     herdrVerbs,
	"pane.current":                 herdrVerbs,
	"pane.layout":                  herdrVerbs,
	"pane.read":                    herdrVerbs,
	"pane.send_text":               herdrVerbs,
	"pane.send_keys":               herdrVerbs,
	"pane.send_input":              herdrVerbs,
	"pane.rename":                  herdrVerbs,
	"pane.focus":                   herdrVerbs,
	"pane.split":                   herdrVerbs,
	"pane.close":                   herdrVerbs,
	"pane.wait_for_output":         herdrVerbs,
	"agent.list":                   herdrVerbs,
	"agent.get":                    herdrVerbs,
	"agent.read":                   herdrVerbs,
	"agent.send_keys":              herdrVerbs,
	"agent.focus":                  herdrVerbs,
	"agent.prompt":                 herdrVerbs,
	"agent.wait":                   herdrVerbs,
	"worktree.list":                herdrVerbs,
	"worktree.create":              herdrVerbs,
	"worktree.open":                herdrVerbs,
	"worktree.remove":              herdrVerbs,
	"events.wait":                  herdrVerbs,
	"pane.process_info":            herdrVerbs,
	"pane.neighbor":                herdrVerbs,
	"pane.edges":                   herdrVerbs,
	"pane.focus_direction":         herdrVerbs,
	"pane.swap":                    herdrVerbs,
	"pane.zoom":                    herdrVerbs,
	"workspace.focus":              herdrVerbs,
	"agent.start":                  herdrVerbs,
	"pane.resize":                  herdrVerbs,
	"server.reload_config":         herdrOpen,
	"server.agent_manifests":       herdrRead,
	"agent.rename":                 herdrVerbs,
	"pane.move":                    herdrVerbs,
	"workspace.report_metadata":    herdrVerbs,
	"agent.explain":                herdrVerbs,
	"client.window_title.set":      herdrVerbs,
	"client.window_title.clear":    herdrVerbs,
	"release_notes.dismiss":        herdrOpen,
	"product_announcement.dismiss": herdrOpen,
	"plugin.list":                  herdrRead,
	"plugin.link":                  herdrTrust,
	"plugin.unlink":                herdrTrust,
	"plugin.restart":               herdrTrust,
	"plugin.action.list":           herdrRead,
	"plugin.action.invoke":         herdrAdmin,
	"plugin.log.list":              herdrAdmin,
	"plugin.pane.open":             herdrAdmin,
	"plugin.pane.focus":            herdrVerbs,
	"plugin.pane.close":            herdrVerbs,
	"popup.close":                  herdrVerbs,
	"plugin.enable":                herdrTrust,
	"plugin.approve":               herdrTrust,
	"plugin.disable":               herdrTrust,

	"events.subscribe": herdrVerbs,

	"pane.report_agent":          herdrReport,
	"pane.report_agent_session":  herdrReport,
	"pane.release_agent":         herdrReport,
	"pane.report_metadata":       herdrReport,
	"notification.show":          herdrReport,
	"pane.clear_agent_authority": herdrReport,
}

// herdrAllowed refuses a method that is not classified, and a read method
// for a pane or plugin without the read grant.
func (d *Daemon) herdrAllowed(cs *connState, method string) *herdrError {
	switch herdrChecks[method] {
	case herdrUnclassified:
		return herdrErr("forbidden", "tuios has not classified "+echoName(method)+", so it does not answer it")
	case herdrRead:
		if pa := d.paneAuthority(cs); pa != nil && !pa.grants.Has(GrantRead) {
			return herdrErr("forbidden", method+" needs the read grant, and the caller holds "+pa.grants.String())
		}
	}
	return nil
}

// herdrAnswered lists every method the herdr socket answers, for the test.
func herdrAnswered() []string {
	var out []string
	for m := range herdrMethods() {
		out = append(out, m)
	}
	for m := range herdrPluginMethods() {
		out = append(out, m)
	}
	out = append(out, herdrReportMethods...)
	out = append(out, "events.subscribe")
	slices.Sort(out)
	return out
}
