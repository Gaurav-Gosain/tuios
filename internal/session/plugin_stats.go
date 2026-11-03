package session

import (
	"encoding/json"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// pluginStat is one row of plugin.stats: what a plugin costs now and since
// the daemon started, so a slow or heavy plugin can be seen.
type pluginStat struct {
	PluginID string `json:"plugin_id"`
	Enabled  bool   `json:"enabled"`
	// Processes counts the processes in the plugin's process sessions now,
	// the reapers included.
	Processes int `json:"processes"`
	// RSS adds up their resident memory in bytes. Known is false where the
	// kernel does not say, as on macOS.
	RSS      int64 `json:"rss_bytes"`
	RSSKnown bool  `json:"rss_known"`
	herdrplugin.RunStats
}

// herdrPluginStats is plugin.stats. It reads the process table once per
// call, so it costs nothing until someone asks.
func (d *Daemon) herdrPluginStats(_ *connState, _ json.RawMessage) (any, *herdrError) {
	runs, sessions := d.plugins.runner.Stats()
	var out []pluginStat
	for _, e := range d.plugins.entries(false) {
		st := pluginStat{PluginID: e.ID, Enabled: e.Enabled, RunStats: runs[e.ID], RSSKnown: true}
		for _, sid := range sessions[e.ID] {
			for _, pid := range procinfo.SessionMembers(sid) {
				st.Processes++
				rss, ok := procinfo.RSS(pid)
				st.RSS += rss
				st.RSSKnown = st.RSSKnown && ok
			}
		}
		if st.Processes == 0 {
			st.RSSKnown = true
		}
		if st.Enabled || st.Runs > 0 || st.Processes > 0 {
			out = append(out, st)
		}
	}
	slices.SortFunc(out, func(a, b pluginStat) int {
		if a.PluginID < b.PluginID {
			return -1
		}
		return 1
	})
	if out == nil {
		out = []pluginStat{}
	}
	return map[string]any{"type": "plugin_stats", "plugins": out}, nil
}
