package session

import (
	"cmp"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"tuios.dev/tml"
)

// What a plugin shows: dock cells and notices.
//
// The daemon holds a plugin's cells, so a hook can set one and exit, and no
// process stays behind to keep it drawn. Clients get the whole list when it
// changes and when they attach. With no cell set the map is nil, and nothing
// here runs: no timer, no goroutine and no message. A cell costs one
// broadcast and one frame per change, and setting the same text costs
// neither. The cells go when the plugin stops, and a new daemon starts with
// none: a plugin sets them again from its startup or its next hook.
//
// Both verbs need the ui grant (checkPluginGrants) and a caller placed in a
// native plugin. A cell must be one the manifest names, which bounds the
// count. A click on a cell runs an action of that plugin, the same call the
// palette makes.

const (
	// maxCellBytes bounds a cell's text. The client cuts it to the cell's
	// width.
	maxCellBytes = 512
	// maxNoticeRunes bounds a notice.
	maxNoticeRunes = 200
	// pluginUIBurst is how many cell changes and notices one plugin may send
	// in a second.
	pluginUIBurst = 8
)

// PluginCell is one dock cell of a plugin.
type PluginCell struct {
	Plugin string `json:"plugin"`
	ID     string `json:"id"`
	Side   string `json:"side"`
	// Text is what the plugin set. The client cleans it before it draws it.
	Text string `json:"text"`
	// Action is the plugin's action a click runs, "" for none.
	Action string `json:"action,omitempty"`
}

// PluginUIPayload is the body of MsgPluginUI: the whole list of cells, or,
// with Notice set, one message for the person.
type PluginUIPayload struct {
	Cells  []PluginCell `json:"cells,omitempty"`
	Notice string       `json:"notice,omitempty"`
	Level  string       `json:"level,omitempty"`
}

// pluginUICaller finds the plugin behind a call to a plugin's verb.
func (d *Daemon) pluginUICaller(cs *connState, verb string) (*herdrplugin.Plugin, *verbError) {
	pa := d.pluginAuthority(cs)
	if pa == nil || pa.plugin == "" {
		return nil, hintedVerbError(ErrVerbForbidden, verb+" is for a plugin's process, and this caller is not one", &VerbHint{
			Detail: "Run it from a command of a tuios plugin. Nothing was done.",
		})
	}
	e := herdrplugin.Find(d.plugins.entries(false), pa.plugin)
	if e == nil || e.Plugin == nil {
		return nil, hintedVerbError(ErrVerbForbidden, "the plugin "+echoName(pa.plugin)+" is not listed any more", &VerbHint{
			Detail: "Run tuios plugins list to see the plugins. Nothing was done.",
		})
	}
	return e.Plugin, nil
}

// admitUI reports whether plugin id may change what it shows now.
func (h *pluginHost) admitUI(id string) *verbError {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.uiStart) > time.Second {
		h.uiCount, h.uiStart = nil, time.Now()
	}
	if h.uiCount[id] >= pluginUIBurst {
		return hintedVerbError(ErrVerbRateLimited, "a plugin changes what it shows at most 8 times a second", &VerbHint{
			Detail: "Set a cell when its value changes, not on a timer. Nothing was done.",
		})
	}
	if h.uiCount == nil {
		h.uiCount = map[string]int{}
	}
	h.uiCount[id]++
	return nil
}

// verbPluginCell sets or clears one dock cell of the calling plugin.
func (d *Daemon) verbPluginCell(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Action string `json:"action"`
		Clear  bool   `json:"clear"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	plugin, verr := d.pluginUICaller(cs, "plugin-cell")
	if verr != nil {
		return nil, verr
	}
	i := slices.IndexFunc(plugin.Cells, func(c herdrplugin.Cell) bool { return c.ID == p.ID })
	if i < 0 {
		return nil, invalidParam("id", "the manifest of "+plugin.PluginID+" has no [[cell]] with the id "+echoName(p.ID))
	}
	switch {
	case p.Clear:
		p.Text, p.Action = "", ""
	case p.Text == "":
		return nil, invalidParam("text", "text is empty. To remove the cell, run tuios plugin-cell clear "+p.ID)
	case len(p.Text) > maxCellBytes || strings.ContainsAny(p.Text, "\r\n"):
		return nil, invalidParam("text", "text must be one line of at most 512 bytes")
	}
	if p.Action != "" && !slices.ContainsFunc(plugin.Actions, func(a herdrplugin.Action) bool { return a.ID == p.Action }) {
		return nil, invalidParam("action", "the manifest of "+plugin.PluginID+" has no action with the id "+echoName(p.Action))
	}
	if verr := d.plugins.admitUI(plugin.PluginID); verr != nil {
		return nil, verr
	}
	cell := PluginCell{Plugin: plugin.PluginID, ID: p.ID, Side: plugin.Cells[i].Side, Text: p.Text, Action: p.Action}
	if d.plugins.setCell(cell) {
		d.pushPluginCells()
	}
	return map[string]any{"type": "plugin_cell", "id": p.ID}, nil
}

// verbPluginNotify shows the person one message, under the plugin's name.
func (d *Daemon) verbPluginNotify(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Text  string `json:"text"`
		Level string `json:"level"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	levels := []string{"info", "warning", "error"}
	level := cmp.Or(p.Level, "info")
	if !slices.Contains(levels, level) {
		return nil, invalidParam("level", "unknown level "+echoName(p.Level), levels...)
	}
	plugin, verr := d.pluginUICaller(cs, "plugin-notify")
	if verr != nil {
		return nil, verr
	}
	text := strings.Join(strings.Fields(tml.Sanitize(p.Text)), " ")
	if text == "" {
		return nil, invalidParam("text", "text is empty")
	}
	if r := []rune(text); len(r) > maxNoticeRunes {
		text = string(r[:maxNoticeRunes-1]) + "…"
	}
	if verr := d.plugins.admitUI(plugin.PluginID); verr != nil {
		return nil, verr
	}
	name := cmp.Or(herdrplugin.ShownName(plugin.Name), plugin.PluginID)
	d.broadcastPluginUI(&PluginUIPayload{Notice: name + ": " + text, Level: level})
	return map[string]any{"type": "plugin_notified"}, nil
}

// setCell stores cell, and removes it when its text is empty. It reports a
// change.
func (h *pluginHost) setCell(cell PluginCell) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	old, had := h.cells[cell.Plugin][cell.ID]
	if cell.Text == "" {
		delete(h.cells[cell.Plugin], cell.ID)
		return had
	}
	if had && old == cell {
		return false
	}
	if h.cells == nil {
		h.cells = map[string]map[string]PluginCell{}
	}
	if h.cells[cell.Plugin] == nil {
		h.cells[cell.Plugin] = map[string]PluginCell{}
	}
	h.cells[cell.Plugin][cell.ID] = cell
	return true
}

// dropCells removes every cell of a plugin that stopped.
func (h *pluginHost) dropCells(id string) {
	h.mu.Lock()
	n := len(h.cells[id])
	delete(h.cells, id)
	h.mu.Unlock()
	if n > 0 {
		h.d.pushPluginCells()
	}
}

// cellList is every cell, in a stable order.
func (h *pluginHost) cellList() []PluginCell {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []PluginCell
	for _, cells := range h.cells {
		for _, c := range cells {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b PluginCell) int {
		return cmp.Or(cmp.Compare(a.Plugin, b.Plugin), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// pushPluginCells sends the attached clients the whole list.
func (d *Daemon) pushPluginCells() {
	d.broadcastPluginUI(&PluginUIPayload{Cells: d.plugins.cellList()})
}

func (d *Daemon) broadcastPluginUI(p *PluginUIPayload) {
	msg, err := NewMessage(MsgPluginUI, p)
	if err != nil {
		log.Printf("[PLUGINS] a cell could not be encoded: %v", err)
		return
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		if pluginAsksReader(cs) {
			d.queueBroadcast(cs, msg, "broadcastPluginUI")
		}
	}
}

// sendPluginCells sends a client that just attached the cells, if any.
func (d *Daemon) sendPluginCells(cs *connState) {
	if d.plugins == nil {
		return
	}
	cells := d.plugins.cellList()
	if len(cells) == 0 || !pluginAsksReader(cs) {
		return
	}
	if msg, err := NewMessage(MsgPluginUI, &PluginUIPayload{Cells: cells}); err == nil {
		d.queueBroadcast(cs, msg, "sendPluginCells")
	}
}
