package app

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/openedcopy"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const farOpenAskMax = 8

type farOpenState struct {
	next uint64
	asks map[uint64]farOpenAsk
	jobs map[string]farOpenJob
}

type farOpenAsk struct {
	host, path, name string
	dir              bool
	big              bool
}

type farOpenJob struct {
	ask   farOpenAsk
	place openedcopy.Place
	far   string
}

type farOpenPreparedMsg struct {
	Ask   *farOpenAsk
	Text  string
	Note  string
	Level string
	Open  string
	Row   *session.TransferRow
	Job   farOpenJob
}

type farOpenEndedMsg struct {
	Open string
	Note string
}

func (m *OS) farOpenHost() (host string, ok bool) {
	switch {
	case m.filesView.Host != "" && m.AttachedHost != "":
		return "", false
	case m.filesView.Host != "":
		return m.filesView.Host, true
	}
	return m.AttachedHost, true
}

func (m *OS) offerFarOpen(ask farOpenAsk, text string) {
	st := &m.farOpen
	if st.asks == nil {
		st.asks = map[uint64]farOpenAsk{}
	}
	st.next++
	id := st.next
	st.asks[id] = ask
	delete(st.asks, id-farOpenAskMax)
	m.ShowNotificationFrom(text, "info", m.Settings.NotificationWarningDuration, NotifTarget{FarOpen: id})
}

func farOpenOfferText(ask farOpenAsk) string {
	return printableTitle(ask.name) + " is on " + sanitizeLinkText(ask.host) +
		". Click to open a copy here."
}

func isFarOpenAsk(t *NotifTarget) bool { return t != nil && t.FarOpen != 0 }

// FarOpenOfferAt reports whether x, y is on the body of the dock message,
// and that message is an offer to open a copy.
func (m *OS) FarOpenOfferAt(x, y int) bool {
	z := m.notifHit
	if !z.Active || len(m.Notifications) == 0 || y != z.Y || x < z.X0 || x >= z.DismissX0 {
		return false
	}
	top := m.Notifications[len(m.Notifications)-1]
	return isFarOpenAsk(top.Target) && sameDrawnNotification(top, z.Drawn)
}

func (m *OS) acceptFarOpen(id uint64) {
	ask, ok := m.farOpen.asks[id]
	if !ok {
		m.ShowNotification("That offer ended. Press o on the file again.", "info", m.Settings.NotificationDuration)
		return
	}
	delete(m.farOpen.asks, id)
	for _, job := range m.farOpen.jobs {
		if job.ask.host == ask.host && job.ask.path == ask.path {
			m.ShowNotification("The copy of "+printableTitle(ask.name)+" has not ended yet. It opens when it ends.", "info", m.Settings.NotificationDuration)
			return
		}
	}
	var files config.FilesConfig
	if m.UserConfig != nil {
		files = m.UserConfig.Files
	}
	onHost := m.verbOnHostCaller()
	call := m.transferCaller()
	hint := m.farSendHint()
	m.queueSidebarCmd(func() tea.Msg { return prepareFarOpen(ask, files, hint, onHost, call) })
}

type farStat struct {
	Exists bool             `json:"exists"`
	Info   session.FileInfo `json:"info"`
}

type farWalk struct {
	Entries []session.WalkEntry `json:"entries"`
	Bytes   int64               `json:"bytes"`
}

func prepareFarOpen(ask farOpenAsk, files config.FilesConfig, hint string, onHost func(string, string, map[string]any) (json.RawMessage, error), call inboxVerbCall) farOpenPreparedMsg {
	name, host := printableTitle(ask.name), sanitizeLinkText(ask.host)
	note := func(level, text string) farOpenPreparedMsg { return farOpenPreparedMsg{Note: text, Level: level} }
	root, err := openedcopy.Root()
	if err != nil {
		return note("error", "tuios has no cache folder to copy "+name+" into.")
	}
	_, _ = openedcopy.Sweep(root, files.OpenCopyKeep(), time.Now())

	raw, err := onHost(ask.host, "file-stat", map[string]any{"path": ask.path})
	if err != nil {
		return note("error", "tuios could not reach "+name+" on "+host+": "+verbErrText(err)+".")
	}
	var st farStat
	if jsonx.Unmarshal(raw, &st) != nil || !st.Exists {
		return note("warning", name+" is gone from "+host+". tuios did not open it.")
	}
	var size int64
	count := 1
	var far string
	switch st.Info.Kind {
	case "file":
		size = st.Info.Size
		far = openedcopy.FileFingerprint(st.Info.Size, st.Info.MTime)
	case "dir":
		raw, err := onHost(ask.host, "file-walk", map[string]any{"path": ask.path})
		if session.VerbErrCode(err) == session.ErrVerbInvalidParams {
			return note("warning", name+" on "+host+" holds more than "+strconv.Itoa(config.OpenCopyMaxFiles)+
				" files. tuios does not open a copy of it."+hint)
		}
		if err != nil {
			return note("error", "tuios could not list "+name+" on "+host+": "+verbErrText(err)+".")
		}
		var w farWalk
		if err := jsonx.Unmarshal(raw, &w); err != nil {
			return note("error", "tuios could not read the list of "+name+" on "+host+".")
		}
		size, count = w.Bytes, 0
		entries := make([]openedcopy.Entry, len(w.Entries))
		for i, e := range w.Entries {
			entries[i] = openedcopy.Entry{Rel: e.Rel, Size: e.Size, MTime: e.MTime, Dir: e.Dir}
			if !e.Dir {
				count++
			}
		}
		far = openedcopy.TreeFingerprint(entries)
		ask.dir = true
	default:
		return note("warning", name+" on "+host+" is a link. tuios does not copy links. Open the file it names.")
	}

	place, err := openedcopy.PlaceFor(root, ask.host, ask.path, ask.name)
	if err != nil {
		return note("error", "tuios did not open "+name+": "+err.Error()+".")
	}
	if rec, ok := openedcopy.ReadRecord(place.Record); ok {
		if local, err := openedcopy.LocalFingerprint(place.Path, rec.Dir); err == nil {
			switch {
			case rec.Far == far:
				openedcopy.Touch(place.Record)
				return farOpenPreparedMsg{Open: place.Path}
			case local != rec.Local:
				openedcopy.Touch(place.Record)
				return farOpenPreparedMsg{Open: place.Path, Note: name + " changed on " + host +
					", and the copy here has changes too. tuios kept the copy here. Run tuios transfers clear-opened to get a new copy."}
			}
		}
	}
	switch {
	case size > files.OpenCopyMaxBytes() || count > config.OpenCopyMaxFiles:
		return note("warning", name+" on "+host+" is "+farOpenSize(size, count, ask.dir)+". That is over the limit of "+
			units.Bytes(files.OpenCopyMaxBytes())+" for a copy to open."+hint)
	case !ask.big && (size > files.OpenCopyAskBytes() || count > config.OpenCopyAskFiles):
		ask.big = true
		return farOpenPreparedMsg{Ask: &ask, Text: name + " on " + host + " is " + farOpenSize(size, count, ask.dir) +
			". Click to copy it here and open it."}
	}

	_ = os.Remove(place.Record)
	if err := os.RemoveAll(place.Dir); err != nil {
		return note("error", "tuios could not remove the old copy of "+name+": "+err.Error()+".")
	}
	if err := os.MkdirAll(place.Dir, 0o700); err != nil {
		return note("error", "tuios could not make the folder for "+name+": "+err.Error()+".")
	}
	params := map[string]any{
		"src":       map[string]any{"host": ask.host, "path": ask.path},
		"dst":       map[string]any{"path": place.Path},
		"conflict":  "replace",
		"keep_from": false,
	}
	if ask.dir {
		params["conflict"], params["each"] = "merge", "replace"
	}
	raw, err = call("transfer-start", params, transferCallTimeout)
	if err != nil {
		return note("error", "tuios could not start the copy of "+name+": "+verbErrText(err)+".")
	}
	var row session.TransferRow
	if jsonx.Unmarshal(raw, &row) != nil || row.ID == "" {
		return note("error", "tuios could not start the copy of "+name+".")
	}
	return farOpenPreparedMsg{Row: &row, Job: farOpenJob{ask: ask, place: place, far: far}}
}

func (m *OS) farSendHint() string {
	if m.KeybindRegistry != nil {
		if keys := m.KeybindRegistry.Keys(config.TableSidebarFiles, "file_send"); len(keys) > 0 {
			return " Press " + keys[0] + " to send it to a folder here."
		}
	}
	return ""
}

func farOpenSize(size int64, files int, dir bool) string {
	if !dir {
		return units.Bytes(size)
	}
	return countOf(files, "file") + ", " + units.Bytes(size)
}

func verbErrText(err error) string {
	var verr *session.VerbCallError
	if errors.As(err, &verr) && verr.Message != "" {
		return printableTitle(verr.Message)
	}
	return printableTitle(err.Error())
}

func (m *OS) handleFarOpenPrepared(msg farOpenPreparedMsg) tea.Cmd {
	switch {
	case msg.Ask != nil:
		m.offerFarOpen(*msg.Ask, msg.Text)
	case msg.Open != "":
		if msg.Note != "" {
			m.ShowNotification(msg.Note, "warning", m.Settings.NotificationWarningDuration)
		}
		return m.openFarCopy(msg.Open)
	case msg.Row != nil:
		st := &m.farOpen
		if st.jobs == nil {
			st.jobs = map[string]farOpenJob{}
		}
		st.jobs[msg.Row.ID] = msg.Job
		m.ShowNotification("Copying "+printableTitle(msg.Job.ask.name)+" from "+sanitizeLinkText(msg.Job.ask.host)+
			". It opens when the copy ends.", "info", m.Settings.NotificationDuration)
		if i := m.transferRowIndex(msg.Row.ID); i >= 0 && transferEnded(m.transfers.rows[i].State) {
			cmd, _ := m.farOpenCopyEnded(m.transfers.rows[i])
			return cmd
		}
		cmd := m.applyTransferRow(*msg.Row, false)
		m.transfersChanged()
		return tea.Batch(cmd, m.transfersLingerCmd())
	default:
		m.ShowNotification(msg.Note, msg.Level, m.Settings.NotificationDuration)
	}
	return nil
}

func (m *OS) farOpenCopyEnded(row session.TransferRow) (tea.Cmd, bool) {
	job, ok := m.farOpen.jobs[row.ID]
	if !ok {
		return nil, false
	}
	delete(m.farOpen.jobs, row.ID)
	switch row.State {
	case "done":
	case "failed":
		m.ShowNotification(transferFailedText(row)+" Nothing was opened.", "error", m.Settings.NotificationDuration*2)
		return nil, true
	default:
		return nil, true
	}
	return func() tea.Msg {
		local, err := openedcopy.LocalFingerprint(job.place.Path, job.ask.dir)
		if err != nil {
			return farOpenEndedMsg{Note: "The copy of " + printableTitle(job.ask.name) + " is gone. Nothing was opened."}
		}
		_ = openedcopy.WriteRecord(job.place.Record, openedcopy.Record{
			Host: job.ask.host, Path: job.ask.path, Dir: job.ask.dir, Far: job.far, Local: local,
		})
		return farOpenEndedMsg{Open: job.place.Path}
	}, true
}

func (m *OS) handleFarOpenEnded(msg farOpenEndedMsg) tea.Cmd {
	if msg.Open == "" {
		m.ShowNotification(msg.Note, "warning", m.Settings.NotificationDuration)
		return nil
	}
	return m.openFarCopy(msg.Open)
}

func (m *OS) openFarCopy(p string) tea.Cmd {
	var rules config.FilesConfig
	if m.UserConfig != nil {
		rules = m.UserConfig.Files
	}
	return func() tea.Msg {
		msg := startFileOpener(p, rules)
		msg.Copy = true
		return msg
	}
}
