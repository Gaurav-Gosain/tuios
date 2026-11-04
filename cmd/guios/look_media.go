package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/preview"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Films show a frame at a time, as the TUI's quick look does: no playback,
// which would cost a core and a timer for what the desktop's player does
// better. The frame comes from AVFoundation on macOS and from ffmpeg
// elsewhere, one read at a time: a drag on the bar or a held key asks for
// the latest place only, read when the last read ends. J and K move 5 % of
// the film, h and l (or the arrows) a second, g and G to the start and the
// end, and t shows a strip of seven frames to jump to.
//
// Sound files show their cover, their tags and what they hold.

// filmPx is the long side a film's frame is read at.
const filmPx = 1600

// stripFrames is the filmstrip's frames, and stripPx their long side.
const (
	stripFrames = 7
	stripPx     = 320
)

// lookFilm is a film's state.
type lookFilm struct {
	facts   *preview.Facts
	err     string
	frame   *ui.Bitmap
	at      int64 // the frame shown, in ms
	want    int64 // the frame asked for
	reading bool
	failed  string // why the last frame could not be read

	strip        bool
	stripPics    [stripFrames]*ui.Bitmap
	stripReading bool
	stripRead    bool

	dragging bool
}

// startFilm reads the film's facts, then its frame 5 % in.
func (a *App) startFilm(l *fileLook) {
	if l.film != nil && (l.film.facts != nil || l.film.err != "") {
		return
	}
	fm := l.film
	if fm == nil {
		fm = &lookFilm{want: -1}
		l.film = fm
	}
	ctx, path := l.ctx, l.path
	go func() {
		f, err := preview.MediaFacts(ctx, path)
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fm.err = mediaMessage(err)
				return
			}
			fm.facts = &f
			l.mark("facts")
			if fm.want < 0 {
				fm.want = f.DurationMs / 20
			}
			a.filmRead(l)
		})
	}()
}

// mediaMessage is what the viewer says of a film or sound it cannot read.
func mediaMessage(err error) string {
	if nt, ok := err.(preview.ErrNoTool); ok {
		return locales.LookNoFfmpeg(string(nt))
	}
	return lookErrText(err)
}

// filmSeek moves to ms, read once the read under way ends.
func (a *App) filmSeek(l *fileLook, ms int64) {
	fm := l.film
	if fm == nil || fm.facts == nil {
		return
	}
	fm.want = min(max(ms, 0), max(fm.facts.DurationMs-1, 0))
	a.filmRead(l)
}

// filmRead reads the frame asked for, unless a read is under way: its end
// reads again when the ask moved meanwhile.
func (a *App) filmRead(l *fileLook) {
	fm := l.film
	if fm.reading || fm.want == fm.at && fm.frame != nil {
		return
	}
	fm.reading = true
	at, ctx, path := fm.want, l.ctx, l.path
	go func() {
		img, err := preview.Frame(ctx, path, at, filmPx)
		var bm *ui.Bitmap
		if err == nil {
			bm = ui.NewBitmap(img)
		}
		a.update(func() {
			fm.reading = false
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fm.failed = mediaMessage(err)
				if _, ok := err.(preview.ErrNoTool); !ok {
					fm.failed = locales.LookNoFrame()
				}
			} else {
				fm.failed = ""
				fm.frame, fm.at = bm, at
				l.mark("frame")
			}
			if fm.want != at {
				a.filmRead(l)
			}
		})
	}()
}

// readStrip reads the filmstrip's frames, one after the other, after the
// main frame.
func (a *App) readStrip(l *fileLook) {
	fm := l.film
	if fm == nil || fm.facts == nil || fm.stripReading || fm.stripRead {
		return
	}
	fm.stripReading = true
	dur, ctx, path := fm.facts.DurationMs, l.ctx, l.path
	go func() {
		for i := range stripFrames {
			at := dur * int64(2*i+1) / (2 * stripFrames)
			img, err := preview.Frame(ctx, path, at, stripPx)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				continue
			}
			bm := ui.NewBitmap(img)
			a.update(func() { fm.stripPics[i] = bm })
		}
		a.update(func() { fm.stripReading, fm.stripRead = false, true })
	}()
}

// filmView is the frame, the bar, and the strip when it shows.
func (a *App) filmView(c *ui.Context, pal *palette, l *fileLook) {
	fm := l.film
	switch {
	case fm == nil || fm.facts == nil && fm.err == "":
		lookWaiting(c, pal, l)
		return
	case fm.err != "":
		lookFailed(c, pal, fm.err)
		return
	}
	f := fm.facts
	ui.Column(c.Key("film")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		stage := ui.Box(c.Key("frame")).Grow(1).MinHeight(0).Clip().Background(ui.RGBA(0, 0, 0, 0.92))
		stage.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputScroll {
				return false
			}
			// The wheel moves 1 % of the film a notch.
			d := ev.DY
			if !ev.Precise {
				d *= 10
			}
			a.filmSeek(l, fm.want+int64(d/10*float32(f.DurationMs)/100))
			return true
		})
		if bm := fm.frame; bm != nil {
			// Fitted whole in the stage, letterboxed, by hand: an Image
			// child kept its own size and the stage cut it to its middle.
			stage.Draw(func(p *ui.Painter, r ui.Rect) {
				p.Clip(r, 0, func() { p.Image(bm, r, ui.Contain) })
			})
		}
		stage.Children(func() {
			switch {
			case fm.frame != nil:
			case fm.failed != "":
				ui.Text(c, fm.failed).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.7)).Absolute().Left(0).Right(0).Top(0).Bottom(0).TextAlign(ui.Center)
			}
			if fm.reading && fm.frame == nil {
				ui.Box(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Center().Children(func() { ui.Spinner(c) })
			}
		})
		if fm.strip {
			a.filmStrip(c, pal, l)
		}
		a.filmBar(c, pal, l)
		facts := []string{}
		if f.W > 0 {
			facts = append(facts, itoa(f.W)+" × "+itoa(f.H))
		}
		if f.VideoCodec != "" {
			facts = append(facts, codecName(f.VideoCodec))
		}
		if f.FPS > 0 {
			facts = append(facts, trimFloat(f.FPS)+" fps")
		}
		if f.AudioCodec != "" {
			facts = append(facts, locales.LookSound(codecName(f.AudioCodec)))
		} else {
			facts = append(facts, locales.LookNoSound())
		}
		ui.Row(c.Key("film-facts")).Height(28).Shrink(0).Padding(0, 14).AlignItems(ui.Center).Children(func() {
			ui.Text(c, strings.Join(facts, " · ")).FontSize(12).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0)
		})
	})
}

// filmBar is the bar to move through the film: a click or a drag goes there.
func (a *App) filmBar(c *ui.Context, pal *palette, l *fileLook) {
	fm := l.film
	dur := max(fm.facts.DurationMs, 1)
	ui.Row(c.Key("film-bar")).Height(40).Shrink(0).Padding(0, 10).Gap(10).AlignItems(ui.Center).Children(func() {
		iconButton(c, "film-back", chevronLeftIcon, locales.LookBack5(), pal, func() { a.filmSeek(l, fm.want-dur/20) })
		iconButton(c, "film-on", chevronIcon, locales.LookOn5(), pal, func() { a.filmSeek(l, fm.want+dur/20) })
		ui.Text(c, filmClock(fm.want)).FontSize(12).FontFeatures("tnum").TextColor(pal.text).SingleLine().Shrink(0)
		bar := ui.Box(c.Key("track")).Grow(1).Height(24).Cursor(ui.CursorPointer)
		bw := bar.Bounds().W
		seekTo := func(x float32) {
			if bw > 0 {
				a.filmSeek(l, int64(min(max(x/bw, 0), 1)*float32(dur)))
			}
		}
		if dx, _, ok := bar.Dragged(); ok {
			_ = dx
			x, _, _ := bar.PointerPosition()
			seekTo(x)
		}
		if bar.Clicked() {
			x, _, _ := bar.PointerPosition()
			seekTo(x)
		}
		hx, _, over := bar.PointerPosition()
		pos := float32(fm.want) / float32(dur)
		shown := float32(fm.at) / float32(dur)
		bar.Draw(func(p *ui.Painter, r ui.Rect) {
			y := r.Y + r.H/2
			p.Fill(ui.Rect{X: r.X, Y: y - 2, W: r.W, H: 4}, pal.chip.Alpha(1), 2)
			p.Fill(ui.Rect{X: r.X, Y: y - 2, W: r.W * shown, H: 4}, pal.textMuted, 2)
			if over {
				p.Fill(ui.Rect{X: r.X + hx - 0.5, Y: y - 6, W: 1, H: 12}, pal.textFaint, 0)
			}
			p.Fill(ui.Rect{X: r.X + r.W*pos - 6, Y: y - 6, W: 12, H: 12}, pal.text, 6)
		})
		if over {
			bar.Tooltip(filmClock(int64(min(max(hx/max(bw, 1), 0), 1) * float32(dur))))
		}
		ui.Text(c, filmClock(dur)).FontSize(12).FontFeatures("tnum").TextColor(pal.textMuted).SingleLine().Shrink(0)
		strip := locales.LookShowStrip()
		if fm.strip {
			strip = locales.LookHideStrip()
		}
		iconButton(c, "film-strip", stripIcon, strip, pal, func() {
			fm.strip = !fm.strip
			if fm.strip {
				a.readStrip(l)
			}
		})
	})
}

// filmStrip is seven frames across the film; a click goes to one.
func (a *App) filmStrip(c *ui.Context, pal *palette, l *fileLook) {
	fm := l.film
	dur := fm.facts.DurationMs
	ui.Row(c.Key("strip")).Height(72).Shrink(0).Padding(8, 10, 0, 10).Gap(6).Children(func() {
		for i := range stripFrames {
			at := dur * int64(2*i+1) / (2 * stripFrames)
			cell := ui.ButtonBase(c.Key(i)).Grow(1).Basis(0).MinWidth(0).Radius(6).Clip().Background(ui.RGBA(0, 0, 0, 0.85)).
				Tooltip(filmClock(at)).Label(locales.LookGoToTime(filmClock(at))).Cursor(ui.CursorPointer)
			near := fm.at >= at-dur/(2*stripFrames) && fm.at < at+dur/(2*stripFrames)
			if near {
				cell.Border(2, pal.accent)
			}
			cell.Children(func() {
				if bm := fm.stripPics[i]; bm != nil {
					ui.Image(c, bm).Fill().Fit(ui.Cover)
				}
			})
			if cell.Clicked() {
				a.filmSeek(l, at)
			}
		}
	})
}

// codecName is a codec as people know it.
func codecName(c string) string {
	switch strings.ToLower(c) {
	case "h264", "avc1":
		return "H.264"
	case "hevc", "h265", "hvc1":
		return "HEVC"
	case "av1", "av01":
		return "AV1"
	case "vp9", "vp09":
		return "VP9"
	case "vp8":
		return "VP8"
	case "aac", "mp4a":
		return "AAC"
	case "mp3", ".mp3":
		return "MP3"
	case "opus":
		return "Opus"
	case "flac":
		return "FLAC"
	case "vorbis":
		return "Vorbis"
	case "pcm", "lpcm":
		return "PCM"
	}
	if strings.HasPrefix(c, "pcm_") {
		return "PCM"
	}
	return c
}

// trimFloat writes f with at most two decimals and none it does not need.
func trimFloat(f float64) string {
	n := int64(f*100 + 0.5)
	s := itoa(int(n / 100))
	if r := n % 100; r != 0 {
		if r%10 == 0 {
			s += "." + itoa(int(r/10))
		} else if r < 10 {
			s += ".0" + itoa(int(r))
		} else {
			s += "." + itoa(int(r))
		}
	}
	return s
}

var stripIcon = icon(`<rect x="2" y="4" width="12" height="8" rx="1.2"/><path d="M6 4v8M10 4v8"/>`)

// --- sound ---

// lookAudio is a sound file's state.
type lookAudio struct {
	facts *preview.Facts
	cover *ui.Bitmap
	err   string
}

// startAudio reads the file's facts and its cover.
func (a *App) startAudio(l *fileLook) {
	if l.audio != nil {
		return
	}
	au := &lookAudio{}
	l.audio = au
	ctx, path := l.ctx, l.path
	go func() {
		f, err := preview.MediaFacts(ctx, path)
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				au.err = mediaMessage(err)
				return
			}
			au.facts = &f
			l.mark("facts")
		})
		if err != nil || !f.Cover {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		img, err := preview.Frame(cctx, path, 0, 800)
		if err != nil {
			return
		}
		bm := ui.NewBitmap(img)
		a.update(func() { au.cover = bm })
	}()
}

// audioView is the cover, the title and what the file holds.
func (a *App) audioView(c *ui.Context, pal *palette, l *fileLook) {
	au := l.audio
	switch {
	case au == nil || au.facts == nil && au.err == "":
		lookWaiting(c, pal, l)
		return
	case au.err != "":
		lookFailed(c, pal, au.err)
		return
	}
	f := au.facts
	tag := func(k string) string { return strings.TrimSpace(f.Tags[k]) }
	title := tag("title")
	if title == "" {
		title = l.name
	}
	by := []string{}
	if v := tag("artist"); v != "" {
		by = append(by, v)
	}
	if v := tag("album"); v != "" {
		by = append(by, v)
	}
	ui.Scroll(c.Key("audio")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(28, 24).Gap(16).AlignItems(ui.Center).Children(func() {
			// The facts under the cover, as wide as it and a little more.
			art := ui.Box(c.Key("cover")).Size(220, 220).Radius(12).Clip().Background(pal.chip).Center()
			raised(art, pal)
			art.Children(func() {
				if au.cover != nil {
					ui.Image(c, au.cover).Fill().Fit(ui.Cover)
				} else {
					ui.Icon(c, noteIcon).Size(64, 64).TextColor(pal.textFaint)
				}
			})
			ui.Column(c).Gap(4).AlignItems(ui.Center).MaxWidth(420).Children(func() {
				ui.Text(c, title).FontSize(15).FontWeight(600).TextColor(pal.text).TextAlign(ui.Center).MaxLines(2).Ellipsis("…")
				if len(by) > 0 {
					ui.Text(c, strings.Join(by, " · ")).FontSize(13).TextColor(pal.textMuted).TextAlign(ui.Center).MaxLines(2).Ellipsis("…")
				}
			})
			rows := [][2]string{}
			add := func(k, v string) {
				if v != "" {
					rows = append(rows, [2]string{k, v})
				}
			}
			if f.DurationMs > 0 {
				add(locales.LookFactLength(), filmClock(f.DurationMs))
			}
			add(locales.LookFactCodec(), codecName(f.AudioCodec))
			if f.SampleRate > 0 {
				add(locales.LookFactSampleRate(), trimFloat(float64(f.SampleRate)/1000)+" kHz")
			}
			switch f.Channels {
			case 0:
			case 1:
				add(locales.LookFactChannels(), locales.LookMono())
			case 2:
				add(locales.LookFactChannels(), locales.LookStereo())
			default:
				add(locales.LookFactChannels(), itoa(f.Channels))
			}
			if f.BitRate > 0 {
				add(locales.LookFactBitRate(), itoa(int(f.BitRate/1000))+" kb/s")
			}
			add(locales.LookFactDate(), tag("date"))
			add(locales.LookFactTrack(), tag("track"))
			add(locales.LookFactGenre(), tag("genre"))
			add(locales.LookFactAlbumArtist(), tag("album_artist"))
			factsTable(c, pal, rows, 420)
		})
	})
}

// factsTable is rows of a name and a value, the names in the quiet ink.
func factsTable(c *ui.Context, pal *palette, rows [][2]string, width float32) {
	ui.Column(c.Key("facts")).AlignSelf(ui.Stretch).MaxWidth(width).Gap(0).Children(func() {
		for i, r := range rows {
			ui.Row(c.Key(i)).Height(28).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r[0]).FontSize(12).TextColor(pal.textFaint).Width(110).Shrink(0).SingleLine()
				ui.Text(c, r[1]).FontSize(12).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).Tooltip(r[1])
			})
		}
	})
}

var noteIcon = icon(`<path d="M6 12.2V3.6l7-1.4v8.6"/><circle cx="4.4" cy="12.2" r="1.6"/><circle cx="11.4" cy="10.8" r="1.6"/>`)
