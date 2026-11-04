package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"

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
	ui.Column(c.Key("reply")).Shrink(0).Margin(0, a.ckSide(24), 16, a.ckSide(24)).Gap(8).Children(func() {
		for _, q := range r.queued[ag.Key()] {
			ui.Row(c.Key("q-"+q.ID)).Gap(8).AlignItems(ui.Center).Padding(6, 10).Radius(rControl).Background(ckGround(pal)).Children(func() {
				ui.Text(c, locales.CockpitQueued()).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.textMuted).SingleLine().Shrink(0)
				oneLine(c, q.Text, fsBody, 0).TextColor(pal.text).Grow(1)
				if sizedButton(c, "cancel", locales.CockpitCancelQueued(), "", false, hControlSm, rControl, pal) {
					a.cancelQueued(ag, q.ID)
				}
			})
		}
		box := ui.Column(c).Radius(rCard).Padding(10, 12, 8, 12).Gap(8).Background(pal.control)
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
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.CockpitTo()).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Shrink(0)
				oneLine(c, ag.Name, fsSmall, wMedium).TextColor(pal.textMuted)
				ui.Spacer(c)
				oneLine(c, locales.CockpitNextTurn(), fsSmall, 0).TextColor(pal.textFaint).Shrink(2)
			})
			if len(d.images) > 0 {
				ui.Row(c).Gap(8).Wrap().Children(func() {
					for i := range d.images {
						a.imageChip(c, pal, d, i)
					}
				})
			}
			in := ui.TextAreaBase(c.Key("text-"+ag.Key()), &d.text).Bind(&r.box).Lines(2, 8).
				FontSize(fsProse).LineHeight(1.5).TextColor(pal.text).Placeholder(locales.CockpitReplyPlaceholder()).ReadOnly(d.sending)
			in.OnPaste(func(text string) bool {
				if text != "" {
					return false
				}
				return a.pasteImageInto(d)
			})
			if in.Changed() {
				a.ck.typedAt = time.Now()
			}
			in.OnTextKey(func(k ui.TextKey) bool {
				switch {
				case k.Key == ui.KeyEnter && k.Mods == ui.Cmd:
					d.text = k.Text()
					a.sendReply(ag, d, true)
					return true
				case k.Key == ui.KeyEnter && k.Mods == 0:
					d.text = k.Text()
					a.sendReply(ag, d, false)
					return true
				case k.Key == ui.KeyUp && k.Mods == 0 && k.Text() == "" && d.last != "":
					// The last message sent, to send again or change.
					k.SetText(d.last)
					return true
				case k.Key == ui.KeyEscape:
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
					keyHint(c, pal, keyName("CmdOrCtrl+Enter"), locales.CockpitHintSendNow())
				})
				if sizedButton(c, "send", locales.CockpitSend(), "↵", true, hControlSm, rControl, pal) {
					a.sendReply(ag, d, false)
				}
			})
		})
	})
}

// keyName is a shortcut as this platform writes it.
func keyName(keys string) string {
	if strings.HasPrefix(keys, "CmdOrCtrl+") {
		if runtime.GOOS == "darwin" {
			return "⌘" + strings.TrimPrefix(keys, "CmdOrCtrl+")
		}
		return "Ctrl+" + strings.TrimPrefix(keys, "CmdOrCtrl+")
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
		_, err := cl.Verb(verb, p, !now)
		a.update(func() {
			d.sending = false
			if err != nil {
				a.toast(err.Error())
				return
			}
			d.last = text
			d.text, d.images = "", nil
			if now {
				a.toast(locales.CockpitSentNow(ag.Name))
			} else {
				a.toast(locales.CockpitQueuedFor(ag.Name))
			}
			a.fetchQueued(ag)
		})
	}()
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
