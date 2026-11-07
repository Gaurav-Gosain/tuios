package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The reply box: what the person writes to the selected agent, with images
// pasted or dropped. Enter queues the message for the agent's next rest
// (queue-prompt, as the TUI's reply area does), Ctrl+Enter (Cmd+Enter on
// the Mac) types it into the pane now, Shift+Enter starts a new line. The
// messages that wait in the agent's queue show above the box, each with a
// button that drops it (list-queued, cancel-queued).

// ckMaxImages is how many images a message carries, queue-prompt's bound.
const ckMaxImages = 8

// ckImageMax is the most bytes an image may have, queue-prompt's bound.
const ckImageMax = 8 << 20

// ckReply is the reply box's state.
type ckReply struct {
	drafts map[string]*ckDraft // by agent
	box    ui.Handle
	queued map[string][]ckQueued // by agent, as list-queued said
	asked  map[string]bool       // the agents whose queue was read
	// focusNext puts the keys in the box once it is built: the Mail view
	// builds it only when a reply starts.
	focusNext bool
	// insert is text typed while a list had the keys, which the typing
	// guard found to be prose: the box adds it at its end.
	insert string
	// asMail holds, by agent, that the box sends mail to the agent's
	// inbox rather than a prompt typed into its pane.
	asMail map[string]bool
	// texts holds the whole text of each message this app queued, by its
	// queue id, for Send now: list-queued gives only its first line.
	texts map[string]string
}

// ckDraft is a message being written.
type ckDraft struct {
	text    string
	last    string // the last message sent
	images  []ckImage
	sending bool
}

// ckImage is an image in a draft.
type ckImage struct {
	name string
	data []byte
	bm   *ui.Bitmap
	n    int
}

// ckQueued is a message waiting in an agent's queue.
type ckQueued struct {
	ID     string `json:"id"`
	Window string `json:"window"`
	Text   string `json:"preview"`
	By     string `json:"by"`
	State  string `json:"state"`
}

func (a *App) draftFor(ag client.Agent) *ckDraft {
	r := &a.ck.reply
	if r.drafts == nil {
		r.drafts = map[string]*ckDraft{}
	}
	d := r.drafts[ag.Key()]
	if d == nil {
		d = &ckDraft{}
		r.drafts[ag.Key()] = d
	}
	return d
}

// ckFocusReply puts the keys in the reply box.
func (a *App) ckFocusReply() { a.ck.reply.box.Focus() }

// replyBox draws the box for the agent.
func (a *App) replyBox(c *ui.Context, pal *palette, ag client.Agent) {
	if ag.Window == "" || ag.Host != "" {
		return
	}
	r := &a.ck.reply
	d := a.draftFor(ag)
	if r.asked == nil {
		r.asked = map[string]bool{}
	}
	if !r.asked[ag.Key()] {
		r.asked[ag.Key()] = true
		a.fetchQueued(ag)
	}
	ui.Column(c.Key("reply")).Shrink(0).Margin(0, a.ckSide(24), 12, a.ckSide(24)).Gap(8).Children(func() {
		cardWaits := a.ckCardWaits(ag)
		queued := r.queued[ag.Key()]
		if len(queued) == 0 {
			a.replyBoxBody(c, pal, ag, d)
			return
		}
		// The queue scrolls past two and a half rows, so it never pushes
		// the approval card's buttons or the box off the view (QA-079).
		// The half row says there is more.
		ui.Scroll(c.Key("queue")).Shrink(0).MaxHeight(ckQueueMaxH).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				a.queuedRows(c, pal, ag, queued, cardWaits)
			})
		})
		a.replyBoxBody(c, pal, ag, d)
	})
}

// ckQueueMaxH is the most height the queued messages take over the box.
const ckQueueMaxH = 128

// queuedRows draws the messages waiting in the agent's queue.
func (a *App) queuedRows(c *ui.Context, pal *palette, ag client.Agent, queued []ckQueued, cardWaits bool) {
	r := &a.ck.reply
	for _, q := range queued {
		ui.Row(c.Key("q-"+q.ID)).Gap(8).AlignItems(ui.Center).Padding(6, 10).Radius(rControl).Background(ckGround(pal)).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				oneLine(c, q.Text, fsBody, 0).TextColor(pal.text)
				// Why it waits, from the queue's state; a card that
				// waits comes first.
				why := ckQueuedWhy(q.State)
				if cardWaits {
					why = locales.CockpitWaitsForAnswer()
				}
				oneLine(c, why, fsSmall, 0).TextColor(pal.textMuted)
			})
			if full, ok := r.texts[q.ID]; ok && q.State != "delivering" && !cardWaits {
				if sizedButton(c, "now", locales.CockpitSendNowButton(), "", true, hControlSm, rControl, pal) {
					a.sendQueuedNow(ag, q.ID, full)
				}
			}
			if sizedButton(c, "cancel", locales.CockpitCancelQueued(), "", false, hControlSm, rControl, pal) {
				a.cancelQueued(ag, q.ID)
			}
		})
	}
}

// replyBoxBody draws the box a reply is written in, under the queue.
func (a *App) replyBoxBody(c *ui.Context, pal *palette, ag client.Agent, d *ckDraft) {
	r := &a.ck.reply
	// Keyed: the queue comes and goes above the box, and an unkeyed box
	// became another element each time, which took the keyboard focus from
	// the field. A reply typed after Enter queued the last one went nowhere.
	box := ui.Column(c.Key("reply-box")).Radius(rCard).Padding(10, 12, 8, 12).Gap(8).Background(pal.control)
	focused := r.box.FocusWithin(c)
	if focused {
		box.Border(1, pal.accent.Alpha(0.6))
	} else {
		box.Border(1, pal.divider)
	}
	if files := box.DroppedFiles(); len(files) > 0 {
		a.addImageFiles(d, files)
	}
	if box.FileDragOver() {
		box.Background(pal.accent.Alpha(0.08))
	}
	box.Children(func() {
		mail := r.asMail[ag.Key()]
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			// One box, two ways to reach the agent: a prompt typed
			// into its pane, or mail to its inbox.
			// The hints sit on the control's fill, the lightest surface
			// in the dark: text2 keeps them at 4.5:1 there (council
			// round 3, visual S3).
			ui.Text(c, locales.CockpitSendAs()).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Shrink(0)
			// A segmented control, as Settings draws them: the chosen
			// segment raised on a soft track, so that it shows on the
			// light theme's white box too (council round 2, S5).
			ui.Row(c).Gap(2).Padding(2).Radius(rControl).Background(pal.chip).Shrink(0).Children(func() {
				a.sendAsChip(c, pal, ag, false, locales.CockpitSendAsPrompt())
				a.sendAsChip(c, pal, ag, true, locales.CockpitSendAsMail())
			})
			ui.Spacer(c)
			note, tip := locales.CockpitNextTurn(), ""
			if r, ok := a.ckSelected(); ok && r.item != nil &&
				(r.item.Kind == session.AttentionApproval || r.item.Kind == session.AttentionPlan) {
				// At an approval, held or not, what is sent waits for its
				// answer: one note for one queue (Mac round 2, workflow
				// S3). Short, so that it fits beside Send as; for a prompt
				// this app cannot answer the whole sentence is its tooltip
				// (Mac round 1, visual S7).
				note = locales.CockpitWaitsAtPromptShort()
				if !ckHeld(*r.item) && !a.ckCanReject() {
					tip = locales.CockpitWaitsAtPrompt()
				}
			}
			if mail {
				note = locales.CockpitMailInbox(ag.Name)
				if m := a.ck.tabs.mail; m != nil && m.replyTo != 0 {
					note = locales.CockpitMailReplyTo(ag.Name)
				}
			}
			nl := oneLine(c, note, fsSmall, 0).TextColor(pal.textMuted).Shrink(2)
			if tip != "" {
				nl.Tooltip(tip)
			}
		})
		if len(d.images) > 0 {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for i := range d.images {
					a.imageChip(c, pal, d, i)
				}
			})
		}
		insert := r.insert
		if insert != "" && !d.sending {
			d.text += insert
		}
		r.insert = ""
		in := ui.TextAreaBase(c.Key("text-"+ag.Key()), &d.text).Bind(&r.box).Lines(2, 8).
			FontSize(fsProse).LineHeight(1.5).TextColor(pal.text).Placeholder(locales.CockpitReplyPlaceholder()).PlaceholderColor(pal.textMuted).ReadOnly(d.sending)
		if r.focusNext {
			r.focusNext = false
			r.box.Focus()
			if insert != "" {
				n := utf8.RuneCountInString(d.text)
				in.SetTextSelection(n, n)
			}
		}
		in.OnPaste(func(text string) bool {
			if text != "" {
				return false
			}
			return a.pasteImageInto(d)
		})
		in.OnTextKey(func(k ui.TextKey) bool {
			switch {
			case k.Key == ui.KeyTab && k.Mods == ui.Shift:
				// Back to the list, the stop before the box.
				a.ck.fleetFocus.Focus()
				return true
			case k.Key == ui.KeyEnter && k.Mods == ui.Cmd && !mail && strings.TrimSpace(k.Text()) == "" && len(d.images) == 0:
				// An empty box sends nothing: Ctrl+Enter allows, as it
				// does in the list.
				a.ckAllowChord()
				return true
			case k.Key == ui.KeyEnter && k.Mods == ui.Cmd && !mail:
				d.text = k.Text()
				a.sendReply(ag, d, true)
				return true
			case k.Key == ui.KeyEnter && k.Mods == 0 && strings.TrimSpace(k.Text()) == "" && len(d.images) == 0 && !mail:
				// Enter in an empty box opens the agent's pane, as
				// Enter in the list does.
				a.goToAgent(ag)
				return true
			case k.Key == ui.KeyEnter && k.Mods == 0:
				d.text = k.Text()
				a.sendBox(ag, d, mail)
				return true
			case k.Key == ui.KeyUp && k.Mods == 0 && k.Text() == "" && d.last != "":
				// The last message sent, to send again or change.
				k.SetText(d.last)
				return true
			case k.Key == ui.KeyEscape:
				if m := a.ck.tabs.mail; a.ck.view == viewMail && m != nil {
					// In the Mail view, Escape drops the reply.
					m.replyTo = 0
					m.focus.Focus()
					return true
				}
				a.ck.fleetFocus.Focus()
				return true
			}
			return false
		})
		ui.Row(c).Gap(sp3).AlignItems(ui.Center).Children(func() {
			// The hints as key caps, as the hint bar draws them; they
			// leave the row as it narrows, the send button stays.
			ui.Row(c).Gap(sp3).AlignItems(ui.Center).Grow(1).MinWidth(0).Clip().Children(func() {
				keyHint(c, pal, keyName("CmdOrCtrl+V"), locales.CockpitHintImage())
				keyHint(c, pal, "⇧↵", locales.CockpitHintNewLine())
				// Ctrl+↵ sends now only while the box has text: empty, it
				// allows (cockpit_ask.go).
				switch {
				case strings.TrimSpace(d.text) == "" && len(d.images) == 0:
				case a.ckCardWaits(ag):
					keyHint(c, pal, keyName("CmdOrCtrl+Enter"), locales.CockpitHintQueue())
				default:
					keyHint(c, pal, keyName("CmdOrCtrl+Enter"), locales.CockpitHintSendNow())
				}
			})
			// While the confirm step is open, its button owns Enter:
			// Send drops its cap and its accent.
			keys, primary := "↵", true
			if a.ck.question != nil {
				keys, primary = "", false
			} else if !a.ck.reply.box.FocusWithin(c) {
				// Enter goes to the list while the box has no focus:
				// one ↵ cap on screen, on what Enter does (Mac round 1,
				// feel S5).
				keys = ""
			}
			if sizedButton(c, "send", locales.CockpitSend(), keys, primary, hControlSm, rControl, pal) {
				a.sendBox(ag, d, mail)
			}
		})
	})
}

// sendAsChip is one way the box sends: a prompt, or mail.
func (a *App) sendAsChip(c *ui.Context, pal *palette, ag client.Agent, mail bool, label string) {
	r := &a.ck.reply
	on := r.asMail[ag.Key()] == mail
	b := ui.ButtonBase(c.Key(label)).Height(20).Padding(0, 8).Radius(rRow).AlignItems(ui.Center).Shrink(0).Label(label)
	switch {
	case on:
		// The box is the control's fill: in the dark the chosen segment
		// takes the lighter hover fill to stand out of it.
		fill := pal.control
		if pal.dark {
			fill = pal.controlHover
		}
		chosen(b.Background(fill), pal)
	case b.Hovered():
		b.Background(pal.rowHover)
	}
	b.Children(func() {
		col := pal.textMuted
		if on {
			col = pal.text
		}
		ui.Text(c, label).FontSize(fsSmall).FontWeight(wMedium).TextColor(col).SingleLine()
	})
	if b.Clicked() {
		c.Invalidate()
		a.ckSendAs(ag, mail)
		r.box.Focus()
	}
}

// ckSendAs sets how the box sends to an agent.
func (a *App) ckSendAs(ag client.Agent, mail bool) {
	r := &a.ck.reply
	if r.asMail == nil {
		r.asMail = map[string]bool{}
	}
	r.asMail[ag.Key()] = mail
}

// sendBox sends the box's text as the switch says: a prompt queued for
// the agent's rest, or mail to its inbox.
func (a *App) sendBox(ag client.Agent, d *ckDraft, mail bool) {
	if !mail {
		a.sendReply(ag, d, false)
		return
	}
	cl := a.cockpitClient()
	text := strings.TrimSpace(d.text)
	if cl == nil || d.sending || text == "" {
		return
	}
	if len(d.images) > 0 {
		a.toast(locales.CockpitMailNoImages())
		return
	}
	p := map[string]any{"session": ag.Session, "to": ag.Window, "text": text, "from": "human"}
	m := a.ck.tabs.mail
	if m != nil && m.replyTo != 0 && m.replySess == ag.Session {
		p["reply_to"] = m.replyTo
	}
	d.sending = true
	go func() {
		_, err := cl.Verb("send-agent-message", p, true)
		a.update(func() {
			d.sending = false
			if err != nil {
				a.toast(err.Error())
				return
			}
			d.last = text
			d.text = ""
			var id int64
			if m != nil {
				id = m.replyTo
				m.replyTo = 0
			}
			a.mailAnswered(ag.Session, ag.Window, ag.Name)
			a.ckAdvanceAfter(ag)
			if id != 0 {
				a.mailReplied(m, ag.Session, id)
			}
		})
	}()
}

// ckCardWaits reports whether an approval, a plan or a question waits on
// the agent: its pane shows a prompt, and nothing guios types may reach the
// pane's input until it is answered (council round 5, M1).
func (a *App) ckCardWaits(ag client.Agent) bool {
	for _, it := range a.ck.items {
		if it.Session != ag.Session || it.Window != ag.Window || it.Host != ag.Host {
			continue
		}
		switch it.Kind {
		case session.AttentionApproval, session.AttentionPlan, session.AttentionQuestion, session.AttentionAsk:
			return true
		}
	}
	return false
}

// keyName is a shortcut as this platform writes it.
//
// Keys are written one way on every cap: Ctrl+↵, ⇧↵, Esc (council round 5,
// visual S3). Prose spells them out: "Press Ctrl+Enter".
func keyName(keys string) string {
	keys = strings.Replace(keys, "Enter", "↵", 1)
	if rest, ok := strings.CutPrefix(keys, "CmdOrCtrl+"); ok {
		if runtime.GOOS == "darwin" {
			return "⌘" + rest
		}
		return "Ctrl+" + rest
	}
	return keys
}

// imageChip is an image in the draft: its picture, name and size, and a
// button that takes it out.
func (a *App) imageChip(c *ui.Context, pal *palette, d *ckDraft, i int) {
	img := d.images[i]
	ui.Row(c.Key(fmt.Sprint("img", img.n))).Gap(8).AlignItems(ui.Center).Padding(4, 6, 4, 4).Radius(rControl).Background(pal.chip).Children(func() {
		if img.bm != nil {
			ui.Image(c, img.bm).Size(36, 28).Fit(ui.Cover).Radius(rChip)
		}
		ui.Column(c).Gap(1).Children(func() {
			oneLine(c, img.name, fsSmall, 0).TextColor(pal.text).MaxWidth(160)
			info := humanSize(len(img.data))
			if img.bm != nil {
				w, h := img.bm.Size()
				info = fmt.Sprintf("%d × %d · %s", w, h, info)
			}
			ui.Text(c, info).FontSize(fsCaption).TextColor(pal.textFaint).FontFeatures("tnum")
		})
		x := ui.ButtonBase(c.Key("x")).Size(18, 18).Radius(9).Center().Label(locales.CockpitRemoveImage()).Tooltip(locales.CockpitRemoveImage())
		if x.Hovered() {
			x.Background(pal.rowHover)
		}
		x.Children(func() { ui.Icon(c, closeIcon).Size(12, 12).TextColor(pal.textMuted) })
		if x.Clicked() {
			c.Invalidate()
			d.images = append(d.images[:i:i], d.images[i+1:]...)
		}
	})
}

// humanSize is a byte count as people read it.
func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// pasteImageInto takes an image from the clipboard into the draft. It
// reports whether the clipboard held one.
func (a *App) pasteImageInto(d *ckDraft) bool {
	data, err := mygo.Clipboard.Read()
	if err != nil {
		return false
	}
	f, ok := imageToPaste(data)
	if !ok {
		return false
	}
	img, err := data.Read(f)
	if err != nil || len(img) == 0 {
		a.toast(locales.ToastPasteClipboardUnreadable())
		return true
	}
	a.addImage(d, locales.CockpitPastedImage(len(d.images)+1), img)
	return true
}

// addImageFiles takes dropped image files into the draft.
func (a *App) addImageFiles(d *ckDraft, files []string) {
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if !isPasteExt(ext) {
			a.toast(locales.CockpitNotImage(filepath.Base(f)))
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			a.toast(err.Error())
			continue
		}
		a.addImage(d, filepath.Base(f), b)
	}
}

func (a *App) addImage(d *ckDraft, name string, b []byte) {
	if len(d.images) >= ckMaxImages {
		a.toast(locales.CockpitTooManyImages(ckMaxImages))
		return
	}
	if len(b) > ckImageMax {
		a.toast(locales.CockpitImageTooBig(name))
		return
	}
	n := 1
	for _, im := range d.images {
		n = max(n, im.n+1)
	}
	bm, _ := ui.DecodeBitmap(b)
	d.images = append(d.images, ckImage{name: name, data: b, bm: bm, n: n})
}

// sendReply sends the draft: queued for the agent's next rest, or typed
// into the pane now.
func (a *App) sendReply(ag client.Agent, d *ckDraft, now bool) {
	cl := a.cockpitClient()
	text := strings.TrimSpace(d.text)
	if cl == nil || d.sending || text == "" && len(d.images) == 0 {
		return
	}
	if now && len(d.images) > 0 {
		a.toast(locales.CockpitNowNoImages())
		return
	}
	forCard := now && a.ckCardWaits(ag)
	if forCard {
		// The pane shows a permission dialog or a question: an Enter typed
		// into it would pick the dialog's option. The draft is queued, and
		// goes in when the agent rests (council round 5, M1).
		now = false
	}
	var verb string
	var p map[string]any
	if now {
		verb = "send-text"
		p = map[string]any{"session": ag.Session, "window": ag.Window, "text": text, "submit": true}
	} else {
		parts := []map[string]any{}
		if text != "" {
			parts = append(parts, map[string]any{"kind": "text", "text": text})
		}
		for _, im := range d.images {
			parts = append(parts, map[string]any{"kind": "image", "data": base64.StdEncoding.EncodeToString(im.data), "n": im.n})
		}
		verb = "queue-prompt"
		p = map[string]any{"session": ag.Session, "window": ag.Window, "text": text, "parts": parts,
			"client_id": fmt.Sprintf("guios-%d", time.Now().UnixNano())}
	}
	d.sending = true
	go func() {
		raw, err := cl.Verb(verb, p, !now)
		a.update(func() {
			d.sending = false
			if err != nil {
				a.toast(ckQueueError(err, ag.Name))
				return
			}
			var q struct {
				ID string `json:"id"`
			}
			if !now && jsonUnmarshal(raw, &q) == nil && q.ID != "" && len(d.images) == 0 {
				r := &a.ck.reply
				if r.texts == nil {
					r.texts = map[string]string{}
				}
				r.texts[q.ID] = text
			}
			d.last = text
			d.text, d.images = "", nil
			switch {
			case now:
				a.toast(locales.CockpitSentNow(ag.Name))
			case forCard:
				a.toast(locales.CockpitQueuedAnswerFirst(ag.Name))
			default:
				a.toast(locales.CockpitQueuedFor(ag.Name))
			}
			a.fetchQueued(ag)
			a.ckAdvanceAfter(ag)
		})
	}()
}

// ckQueueError is a failed send in words: the daemon's message names the
// window by its id and ends in an error code.
func ckQueueError(err error, name string) string {
	var ve *session.VerbCallError
	if errors.As(err, &ve) && ve.Code == session.ErrVerbQueueFull {
		return locales.CockpitQueueFull(name)
	}
	return err.Error()
}

// fetchQueued reads the agent's queue.
func (a *App) fetchQueued(ag client.Agent) {
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	go func() {
		var out struct {
			Entries []ckQueued `json:"entries"`
		}
		err := cl.VerbInto("list-queued", map[string]any{"session": ag.Session, "window": ag.Window}, false, &out)
		a.update(func() {
			if err != nil {
				return
			}
			r := &a.ck.reply
			if r.queued == nil {
				r.queued = map[string][]ckQueued{}
			}
			r.queued[ag.Key()] = out.Entries
		})
	}()
}

// ckQueuedWhy says why a queued message waits.
func ckQueuedWhy(state string) string {
	switch state {
	case "delivering":
		return locales.CockpitQueuedDelivering()
	case "stalled":
		return locales.CockpitQueuedStalled()
	}
	return locales.CockpitQueuedWaiting()
}

// sendQueuedNow takes a waiting message out of the queue and types it into
// the pane now.
func (a *App) sendQueuedNow(ag client.Agent, id, text string) {
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	if a.ckCardWaits(ag) {
		// Not into a pane at a prompt: the Enter would answer it.
		a.toast(locales.CockpitWaitsForAnswer())
		return
	}
	go func() {
		_, err := cl.Verb("cancel-queued", map[string]any{"session": ag.Session, "window": ag.Window, "id": id}, true)
		if err == nil {
			_, err = cl.Verb("send-text", map[string]any{"session": ag.Session, "window": ag.Window, "text": text, "submit": true}, false)
		}
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			} else {
				delete(a.ck.reply.texts, id)
				a.toast(locales.CockpitSentNow(ag.Name))
			}
			a.fetchQueued(ag)
		})
	}()
}

// cancelQueued drops a waiting message.
func (a *App) cancelQueued(ag client.Agent, id string) {
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	go func() {
		_, err := cl.Verb("cancel-queued", map[string]any{"session": ag.Session, "window": ag.Window, "id": id}, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.fetchQueued(ag)
		})
	}()
}
