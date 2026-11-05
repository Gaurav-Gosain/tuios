package app

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/openedcopy"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// file_open_system on a row of a listing of another machine: the opener runs
// here, and the file is not here. So o offers to copy it here first, with the
// transfer engine that send and tuios cp use, into a place of its own under
// the user cache (internal/openedcopy), and then opens the copy with the same
// [[files.open]] rules.
//
// The offer is a message in the dock with a target: a click on it, or the
// jump key, takes it, and esc is not now. A large file or folder asks again
// with its size, and one past the hard limit is refused.
//
// The copy's place is the same for the same far path, so opening the file
// again finds the copy. Its record says what the far file looked like when it
// was copied: unchanged, the copy opens at once. Changed, it is copied again,
// unless the copy here changed too, which is not replaced.
//
// The copy is not sent back. The success message says changes stay here.

// farOpenAskMax bounds the offers kept for a click. An offer older than the
// last few has left the dock.
const farOpenAskMax = 8

// farOpenState is the offers in the dock and the copies that open when they
// end.
type farOpenState struct {
	next uint64
	asks map[uint64]farOpenAsk
	// jobs maps a copy's transfer id to what opens when it ends.
	jobs map[string]farOpenJob
}

// farOpenAsk is one far row o was pressed on.
type farOpenAsk struct {
	host, path, name string
	dir              bool
	// big is set once the person said yes to the size, so the copy does
	// not ask about it again.
	big bool
}

// farOpenJob is a copy that opens when it ends.
type farOpenJob struct {
	ask   farOpenAsk
	place openedcopy.Place
	far   string
}

// farOpenPreparedMsg is what the look at the far file decided. Exactly one
// of Note, Ask, Open and Row is set.
type farOpenPreparedMsg struct {
	Ask   *farOpenAsk
	Text  string
	Note  string
	Level string
	// Open is a copy here that is current: it opens at once.
	Open string
	// Row is the copy that started, and Job what opens when it ends.
	Row *session.TransferRow
	Job farOpenJob
}

// farOpenEndedMsg is the record of a copy that ended, written off the loop.
type farOpenEndedMsg struct {
	Open string
	Note string
}

// farOpenHost is the machine of the files listing, or "" for this one.
// ok is false for a listing this client cannot copy from: a host reached
// through another host.
func (m *OS) farOpenHost() (host string, ok bool) {
	switch {
	case m.filesView.Host != "" && m.AttachedHost != "":
		return "", false
	case m.filesView.Host != "":
		return m.filesView.Host, true
	}
	return m.AttachedHost, true
}

// offerFarOpen puts the offer in the dock.
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

// farOpenOfferText is the first offer: the file is far, and a click copies
// it here.
func farOpenOfferText(ask farOpenAsk) string {
	return printableTitle(ask.name) + " is on " + sanitizeLinkText(ask.host) +
		". Click to open a copy here."
}

// isFarOpenAsk reports whether a target is an offer to open a copy. A click
// on it takes the offer even when the dock cut its text: the person asked for
// it a moment ago with o.
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

// acceptFarOpen takes the offer: it looks at the far file off the loop and
// decides what to do. The command is parked for the click or key handler
// that runs this.
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

// farStat is what file-stat says of the far path.
type farStat struct {
	Exists bool             `json:"exists"`
	Info   session.FileInfo `json:"info"`
}

// farWalk is what file-walk says of a far folder.
type farWalk struct {
	Entries []session.WalkEntry `json:"entries"`
	Bytes   int64               `json:"bytes"`
}

// prepareFarOpen looks at the far file, checks the limits and the copy that
// is here, and starts the copy when one is needed.
func prepareFarOpen(ask farOpenAsk, files config.FilesConfig, hint string, onHost func(string, string, map[string]any) (json.RawMessage, error), call inboxVerbCall) farOpenPreparedMsg {
	name, host := printableTitle(ask.name), sanitizeLinkText(ask.host)
	note := func(level, text string) farOpenPreparedMsg { return farOpenPreparedMsg{Note: text, Level: level} }
	root, err := openedcopy.Root()
	if err != nil {
		return note("error", "tuios has no cache folder to copy "+name+" into.")
	}
	// The cheap cadence of the cleanup: each offer taken, and the daemon's
	// start. Nothing runs while nobody opens a far file.
	_, _ = openedcopy.Sweep(root, files.OpenCopyKeep(), time.Now())

	raw, err := onHost(ask.host, "file-stat", map[string]any{"path": ask.path})
	if err != nil {
		return note("error", "tuios could not reach "+name+" on "+host+": "+verbErrText(err)+".")
	}
	var st farStat
	if json.Unmarshal(raw, &st) != nil || !st.Exists {
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
		var verr *session.VerbCallError
		if errors.As(err, &verr) && verr.Code == session.ErrVerbInvalidParams {
			return note("warning", name+" on "+host+" holds more than "+strconv.Itoa(config.OpenCopyMaxFiles)+
				" files. tuios does not open a copy of it."+hint)
		}
		if err != nil {
			return note("error", "tuios could not list "+name+" on "+host+": "+verbErrText(err)+".")
		}
		var w farWalk
		if err := json.Unmarshal(raw, &w); err != nil {
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
		// A link is not followed: what it names can be anywhere on that
		// machine, outside the folder on screen.
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
				// Changed there and changed here. The copy here is the
				// person's work, so it is not replaced.
				openedcopy.Touch(place.Record)
				return farOpenPreparedMsg{Open: place.Path, Note: name + " changed on " + host +
					", and the copy here has changes too. tuios kept the copy here. Run tuios transfers clear-opened to get a new copy."}
			}
		}
	}
	// A current copy opened above whatever its size. A new one is held to
	// the limits.
	switch {
	case size > files.OpenCopyMaxBytes() || count > config.OpenCopyMaxFiles:
		return note("warning", name+" on "+host+" is "+farOpenSize(size, count, ask.dir)+". That is over the limit of "+
			units.Bytes(files.OpenCopyMaxBytes())+" for a copy to open."+hint)
	case !ask.big && (size > files.OpenCopyAskBytes() || count > config.OpenCopyAskFiles):
		ask.big = true
		return farOpenPreparedMsg{Ask: &ask, Text: name + " on " + host + " is " + farOpenSize(size, count, ask.dir) +
			". Click to copy it here and open it."}
	}

	// A copy that is out of date goes, so a file gone from the far folder
	// is gone from the copy too.
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
	if json.Unmarshal(raw, &row) != nil || row.ID == "" {
		return note("error", "tuios could not start the copy of "+name+".")
	}
	return farOpenPreparedMsg{Row: &row, Job: farOpenJob{ask: ask, place: place, far: far}}
}

// farSendHint is the next step for a file too big to open a copy of: send,
// as it is bound.
func (m *OS) farSendHint() string {
	if m.KeybindRegistry != nil {
		if keys := m.KeybindRegistry.GetSidebarFilesKeys("file_send"); len(keys) > 0 {
			return " Press " + keys[0] + " to send it to a folder here."
		}
	}
	return ""
}

// farOpenSize says how big a file or folder is.
func farOpenSize(size int64, files int, dir bool) string {
	if !dir {
		return units.Bytes(size)
	}
	return countOf(files, "file") + ", " + units.Bytes(size)
}

// verbErrText is the reason a verb call failed, as the daemon put it.
func verbErrText(err error) string {
	var verr *session.VerbCallError
	if errors.As(err, &verr) && verr.Message != "" {
		return printableTitle(verr.Message)
	}
	return printableTitle(err.Error())
}

// handleFarOpenPrepared does what the look decided.
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
		// The copy can end before this answer is read. Its end event then
		// found no job, so the row in the mirror is what says it ended.
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

// farOpenCopyEnded opens a copy o started, once it ended. It reports false
// for a copy that is not one of them.
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
		// Without a record the copy is copied again next time, which is
		// slower and still right.
		_ = openedcopy.WriteRecord(job.place.Record, openedcopy.Record{
			Host: job.ask.host, Path: job.ask.path, Dir: job.ask.dir, Far: job.far, Local: local,
		})
		return farOpenEndedMsg{Open: job.place.Path}
	}, true
}

// handleFarOpenEnded opens the copy that ended.
func (m *OS) handleFarOpenEnded(msg farOpenEndedMsg) tea.Cmd {
	if msg.Open == "" {
		m.ShowNotification(msg.Note, "warning", m.Settings.NotificationDuration)
		return nil
	}
	return m.openFarCopy(msg.Open)
}

// openFarCopy runs the opener on a copy here. The success message says it
// is a copy.
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
