package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/mygo/ui/markdown"
	"github.com/Gaurav-Gosain/mygo/ui/markdown/parser"

	"github.com/Gaurav-Gosain/tuios/internal/guios/preview"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Markdown draws with MyGo's ui/markdown, the parser and view that pass
// CommonMark and GitHub's extensions: tables, task lists, footnotes, the
// formatting tags (<kbd>, <sub>, <sup>, <br>) and images from the
// document's folder. guios adds GitHub's alerts (> [!NOTE] and the four
// others) and the type and colours of its design. Images from the web do
// not load: a document is the agent's or a stranger's as often as the
// person's, and a fetch tells its author someone read it.
//
// A text too large to edit (editMax) shows read only through a line index
// (preview.TextFile), with / to find, n and N for the next and the
// previous match, : to go to a line and w to wrap.

// mdMax bounds a Markdown file drawn whole.
const mdMax = 4 << 20

// lookMD is a Markdown file's state.
type lookMD struct {
	src    string
	err    string
	opts   markdown.Options
	scroll ui.ScrollState
	viewH  float32
	dark   bool
	// inner are the options of the views inside alerts.
	inner markdown.Options
}

func (a *App) startMD(l *fileLook) {
	if l.md != nil {
		return
	}
	m := &lookMD{}
	l.md = m
	ctx, p, html := l.ctx, l.path, l.kind == preview.HTML
	go func() {
		data, err := readCapped(p, mdMax)
		// A page is laid out as Markdown on this goroutine.
		var htmlSrc string
		if html && err == nil {
			htmlSrc = preview.HTMLToMarkdown(strings.ToValidUTF8(string(data), "\uFFFD"))
		}
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				m.err = lookErrText(err)
				return
			}
			m.src = strings.ToValidUTF8(string(data), "\uFFFD")
			if html {
				m.src = htmlSrc
			}
			l.mark("read")
		})
	}()
}

// mdTheme is the Markdown theme of the design: prose at 14 on 1.6, GitHub's
// headings with a rule under the first two levels, code in the terminal's
// font on its own ground.
func (a *App) mdTheme(c *ui.Context, pal *palette) *markdown.Theme {
	t := markdown.DefaultTheme(c.Theme())
	t.FontSize = 14
	t.LineHeight = 1.6
	t.CodeFont = a.termFamily()
	t.Text, t.Muted, t.Link, t.Strong = pal.text, pal.textMuted, pal.accent, pal.text
	t.Code, t.CodeBackground = pal.text, pal.chip
	t.CodeSize = 0.86
	t.BlockSpacing = 0.9
	t.QuoteBar, t.QuoteText = pal.divider.Alpha(1).Mix(pal.textFaint, 0.5), pal.textMuted
	t.Rule = pal.divider
	t.CodeBlockBackground, t.CodeBlockBorder = ckCodeBg(pal), ui.Transparent
	t.CodeBlockRadius = 8
	t.CodeColors = a.codeTheme(c, pal)
	t.TableBorder, t.TableHeader = pal.divider.Mix(pal.textFaint, 0.25), pal.chip
	t.Checkbox = pal.accent
	sizes := [6]float32{2, 1.5, 1.25, 1.05, 0.95, 0.9}
	for i := range t.Headings {
		h := &t.Headings[i]
		h.Size, h.Weight = sizes[i], 600
		h.Top, h.Bottom = 1.5, 0.6
		h.Rule = i < 2
		if i == 5 {
			h.Color = pal.textMuted
		}
	}
	return t
}

func (a *App) mdView(c *ui.Context, pal *palette, l *fileLook) {
	m := l.md
	switch {
	case m == nil || m.src == "" && m.err == "":
		if m != nil && m.src == "" && m.err == "" && !l.busy && l.size == 0 {
			lookFailed(c, pal, locales.LookEmpty())
			return
		}
		lookWaiting(c, pal, l)
		return
	case m.err != "":
		lookFailed(c, pal, m.err)
		return
	}
	if m.opts.Theme == nil || m.dark != pal.dark {
		m.dark = pal.dark
		th := a.mdTheme(c, pal)
		dir := filepath.Dir(l.path)
		load, link := docImages(dir), func(url string) { a.mdLink(l, url) }
		m.opts = markdown.Options{Theme: th, LoadImage: load, OnLink: link}
		m.inner = markdown.Options{Theme: th, LoadImage: load, OnLink: link}
		m.opts.Block = func(c *ui.Context, d *parser.Document, i int) bool { return a.mdAlert(c, pal, m, d, i) }
		m.opts.HTML = func(c *ui.Context, html string) { a.mdHTML(c, pal, m, html) }
	}
	sc := ui.Scroll(c.Key("md")).TrackScroll(&m.scroll).Grow(1).MinHeight(0)
	if b := sc.Bounds(); b.H > 0 {
		m.viewH = b.H
	}
	// A table fits the panel: a column wraps its cells past about a third
	// of the text's width, in steps of 2 ems so that the layout is kept
	// while the panel holds its size (council round 1, visual 9). A table
	// still too wide scrolls sideways inside the panel.
	if b := sc.Bounds(); b.W > 0 && m.opts.Theme != nil {
		ems := min(max(float32(int((min(b.W, 760+56)-56)/14/2.2/2))*2, 8), 24)
		if m.opts.Theme.TableMaxColumn != ems {
			m.opts.Theme.TableMaxColumn, m.inner.Theme.TableMaxColumn = ems, ems
		}
	}
	sc.Children(func() {
		ui.Column(c).Padding(24, 28, 40, 28).AlignItems(ui.Center).Children(func() {
			ui.Column(c).MaxWidth(760).AlignSelf(ui.Stretch).Children(func() {
				markdown.View(c.Key("doc"), m.src, &m.opts)
			})
		})
	})
}

// mdLink opens a link: a web page in the browser, a heading in place, a
// file of the document's folder in the viewer.
func (a *App) mdLink(l *fileLook, url string) {
	switch {
	case strings.HasPrefix(url, "#"):
		if l.md != nil {
			l.md.opts.Anchor = url
			l.md.opts.AnchorVersion++
		}
	case strings.Contains(url, "://") || strings.HasPrefix(url, "mailto:"):
		if ui.SafeToOpen(url) {
			mygo.Shell.OpenExternal(url)
		}
	default:
		p := url
		if i := strings.IndexAny(p, "#?"); i >= 0 {
			p = p[:i]
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(l.path), filepath.FromSlash(p))
		}
		if _, err := os.Stat(p); err == nil {
			openPath(p)
		}
	}
}

// docImages loads a document's images from its folder or the repository
// it is in, never from the web, nor from a path that leaves both.
func docImages(dir string) func(url string) (ui.ImageSource, error) {
	root := dir
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			root = d
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return func(url string) (ui.ImageSource, error) {
		if strings.Contains(url, "://") || strings.HasPrefix(url, "data:") || strings.HasPrefix(url, "//") {
			return nil, errors.New("web images do not load")
		}
		p := strings.TrimPrefix(url, "file:")
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, filepath.FromSlash(p))
		}
		p = filepath.Clean(p)
		if rel, err := filepath.Rel(root, p); err != nil || strings.HasPrefix(rel, "..") {
			return nil, errors.New("the image is outside the document's folder")
		}
		if strings.EqualFold(filepath.Ext(p), ".svg") {
			data, err := readCapped(p, 4<<20)
			if err != nil {
				return nil, err
			}
			return ui.ParseSVG(data)
		}
		pic, err := preview.DecodeImage(context.Background(), p, 2048)
		if err != nil {
			return nil, err
		}
		if pic.Img != nil {
			return packBitmap(pic.Img)
		}
		return bitmapOf(pic)
	}
}

// alertKinds are GitHub's alerts, by their marker.
// alertTitle is an alert's title, by its marker.
func alertTitle(kind string) string {
	switch kind {
	case "TIP":
		return locales.LookAlertTip()
	case "IMPORTANT":
		return locales.LookAlertImportant()
	case "WARNING":
		return locales.LookAlertWarning()
	case "CAUTION":
		return locales.LookAlertCaution()
	}
	return locales.LookAlertNote()
}

var alertMark = regexp.MustCompile(`^\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)

// mdAlert draws a quote that starts with an alert's marker as the alert: a
// bar and a title in its colour, and the rest of the quote under them.
func (a *App) mdAlert(c *ui.Context, pal *palette, m *lookMD, d *parser.Document, i int) bool {
	b := d.Block(i)
	if b.Kind != parser.Quote {
		return false
	}
	src := d.Source()[b.Start:b.End]
	lines := strings.Split(src, "\n")
	var body []string
	kind := ""
	for k, ln := range lines {
		ln = strings.TrimPrefix(strings.TrimLeft(ln, " "), ">")
		ln = strings.TrimPrefix(ln, " ")
		if k == 0 {
			mt := alertMark.FindStringSubmatch(ln)
			if mt == nil {
				return false
			}
			kind = mt[1]
			continue
		}
		body = append(body, ln)
	}
	col := alertColor(pal, kind)
	ui.Row(c.Key("alert")).AlignItems(ui.Stretch).Margin(4, 0).Children(func() {
		ui.Box(c).Width(3).Shrink(0).Radius(1.5).Background(col)
		ui.Column(c).Grow(1).MinWidth(0).Padding(2, 0, 2, 14).Gap(4).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, alertIcon(kind)).Size(15, 15).TextColor(col)
				ui.Text(c, alertTitle(kind)).FontSize(14).FontWeight(wSemibold).TextColor(col)
			})
			markdown.View(c.Key("body"), strings.Join(body, "\n"), &m.inner)
		})
	})
	return true
}

// alertColor is an alert's colour: the accent for a note, the roles of the
// design for the others.
func alertColor(pal *palette, kind string) ui.Color {
	switch kind {
	case "TIP":
		return pal.done.Mix(ui.Hex("#2da44e"), 0.85)
	case "IMPORTANT":
		return pal.accent.Mix(ui.Hex("#8250df"), 0.8)
	case "WARNING":
		return pal.attention
	case "CAUTION":
		return pal.danger
	}
	return pal.accent
}

var (
	alertNote      = icon(`<circle cx="8" cy="8" r="6"/><path d="M8 7.2v3.6M8 5.2v.1"/>`)
	alertTip       = icon(`<path d="M5.6 10.4a4 4 0 1 1 4.8 0c-.5.4-.8 1-.8 1.6H6.4c0-.6-.3-1.2-.8-1.6zM6.4 14h3.2"/>`)
	alertImportant = icon(`<path d="M2.5 3.5h11v7.5H8l-3 2.5V11H2.5z"/><path d="M8 5.5v2.5M8 9.6v.1"/>`)
	alertWarning   = icon(`<path d="M8 2.2l6.2 11H1.8z"/><path d="M8 6.5v3M8 11.4v.1"/>`)
	alertCaution   = icon(`<path d="M5.5 1.8h5l3.7 3.7v5l-3.7 3.7h-5l-3.7-3.7v-5z"/><path d="M8 5v3.4M8 10.6v.1"/>`)
)

func alertIcon(kind string) *ui.SVG {
	switch kind {
	case "TIP":
		return alertTip
	case "IMPORTANT":
		return alertImportant
	case "WARNING":
		return alertWarning
	case "CAUTION":
		return alertCaution
	}
	return alertNote
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlImg     = regexp.MustCompile(`(?i)<img\s[^>]*?src\s*=\s*["']([^"']+)["'][^>]*>`)
	htmlAlt     = regexp.MustCompile(`(?i)\balt\s*=\s*["']([^"']*)["']`)
	htmlWidth   = regexp.MustCompile(`(?i)\bwidth\s*=\s*["']?(\d+)`)
	htmlCenter  = regexp.MustCompile(`(?i)<(p|div|h[1-6])[^>]*\balign\s*=\s*["']?center`)
	htmlTag     = regexp.MustCompile(`(?s)<[^>]+>`)
	htmlDrop    = regexp.MustCompile(`(?is)<(script|style)\b.*?</(script|style)>`)
)

// mdHTML draws an HTML block as READMEs use them: pictures, centred
// headers, and the text of the rest with its tags dropped. Scripts and
// styles go with their content; nothing runs.
func (a *App) mdHTML(c *ui.Context, pal *palette, m *lookMD, html string) {
	html = htmlDrop.ReplaceAllString(htmlComment.ReplaceAllString(html, ""), "")
	center := htmlCenter.MatchString(html)
	align := ui.Start
	if center {
		align = ui.Center
	}
	imgs := htmlImg.FindAllStringSubmatchIndex(html, -1)
	ui.Column(c.Key("html")).Gap(8).AlignItems(align).Children(func() {
		if len(imgs) > 0 {
			ui.Row(c.Key("imgs")).Gap(6).Wrap().Justify(align).Children(func() {
				for k, loc := range imgs {
					tag := html[loc[0]:loc[1]]
					src := html[loc[2]:loc[3]]
					alt := ""
					if mt := htmlAlt.FindStringSubmatch(tag); mt != nil {
						alt = mt[1]
					}
					w := float32(0)
					if mt := htmlWidth.FindStringSubmatch(tag); mt != nil {
						if n, err := strconv.Atoi(mt[1]); err == nil {
							w = float32(n)
						}
					}
					a.mdPicture(c.Key(k), pal, m, src, alt, w)
				}
			})
		}
		text := strings.TrimSpace(htmlTag.ReplaceAllString(html, " "))
		text = strings.Join(strings.Fields(text), " ")
		if text != "" {
			t := ui.Text(c, text).FontSize(14).TextColor(pal.text).LineHeight(1.6)
			if center {
				t.TextAlign(ui.Center)
			}
		}
	})
}

// mdPicture is an <img> of an HTML block, loaded as Markdown's images are.
func (a *App) mdPicture(c *ui.Context, pal *palette, m *lookMD, src, alt string, w float32) {
	pics := mdPics(m)
	p, ok := pics[src]
	if !ok {
		p = &mdPic{}
		pics[src] = p
		load := m.opts.LoadImage
		go func() {
			img, err := load(src)
			a.update(func() { p.img, p.err, p.done = img, err, true })
		}()
	}
	if p.img == nil {
		label := alt
		if label == "" {
			label = filepath.Base(src)
		}
		ui.Text(c, "["+label+"]").FontSize(fsSmall).TextColor(pal.textFaint).Padding(2, 6).Background(pal.chip).Radius(rChip).SingleLine()
		return
	}
	el := ui.Image(c, p.img)
	if iw, ih := ui.ImageSize(p.img); iw > 0 && ih > 0 {
		if w == 0 {
			w = iw
		}
		el.Width(w).AspectRatio(iw / ih).MaxWidthPercent(100)
	}
}

type mdPic struct {
	img  ui.ImageSource
	err  error
	done bool
}

// mdPicsOf keeps each document's HTML pictures.
var mdPicsOf = map[*lookMD]map[string]*mdPic{}

func mdPics(m *lookMD) map[string]*mdPic {
	if mdPicsOf[m] == nil {
		if len(mdPicsOf) > 8 {
			clear(mdPicsOf)
		}
		mdPicsOf[m] = map[string]*mdPic{}
	}
	return mdPicsOf[m]
}

// --- large texts ---

const (
	promptNone = iota
	promptFind
	promptGoTo
)

// lookBig is a text too large to edit, read through its line index.
type lookBig struct {
	tf        *preview.TextFile
	err       string
	list      ui.ListState
	wrap      bool
	prompt    int
	query     string
	find      string // the last search
	hit       int64  // the line of the last match, -1 for none
	searching bool
	note      string
	shown     [2]int
	cancel    context.CancelFunc
}

func (b *lookBig) rows() int { return max(b.shown[1]-b.shown[0], 1) }

func (b *lookBig) scrollBy(n int) {
	b.list.ScrollTo(max(b.shown[0]+n, 0), ui.Start)
}

func (a *App) startBig(l *fileLook) {
	if l.big != nil {
		return
	}
	b := &lookBig{hit: -1}
	l.big = b
	tf, err := preview.OpenText(l.path)
	if err != nil {
		b.err = lookErrText(err)
		return
	}
	b.tf = tf
	ctx := l.ctx
	go func() {
		_ = tf.Index(ctx, func() { a.update(func() { l.mark("first lines") }) })
		a.update(func() { l.mark("indexed") })
	}()
}

func (a *App) bigView(c *ui.Context, pal *palette, l *fileLook) {
	if l.big == nil {
		a.startBig(l)
	}
	b := l.big
	if b.err != "" {
		lookFailed(c, pal, b.err)
		return
	}
	n, done := b.tf.Count()
	if !done && n == 0 {
		lookWaiting(c, pal, l)
		return
	}
	mono := a.termFamily()
	digits := len(strconv.FormatInt(max(n, 1), 10))
	gutter := float32(digits)*7.6 + 18
	q := b.find
	fold := q == strings.ToLower(q)
	ui.Column(c.Key("big")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		if b.prompt != promptNone {
			a.bigPrompt(c, pal, l)
		}
		rows := int(n)
		ui.List(c.Key("lines"), &b.list, rows, func(i int) {
			line, ok := b.tf.Line(int64(i), func() { a.update(func() {}) })
			ui.Row(c).AlignItems(ui.Start).Children(func() {
				num := ui.Text(c, strconv.Itoa(i+1)).Font(mono).FontSize(fsSmall).TextColor(pal.textFaint).Width(gutter).Shrink(0).TextAlign(ui.End).Padding(0, 12, 0, 0).SingleLine()
				if int64(i) == b.hit {
					num.TextColor(pal.accent)
				}
				if !ok {
					return
				}
				spans := bigSpans(line, q, fold, pal, mono)
				t := ui.RichText(c, spans...).MinWidth(0).Grow(1)
				// The line of the match wraps, so the match shows.
				if !b.wrap && int64(i) != b.hit {
					t.SingleLine()
				}
			})
		}).Grow(1).MinHeight(0).Padding(8, 12, 8, 0)
		b.shown[0], b.shown[1] = b.list.Visible()
		if b.note != "" || !done {
			ui.Row(c).Height(26).Shrink(0).Padding(0, 14).AlignItems(ui.Center).Children(func() {
				msg := b.note
				if !done {
					msg = locales.LookCountingSoFar(groupDigits(int(n)))
				}
				ui.Text(c, msg).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…")
			})
		}
	})
}

// bigSpans is a line with the matches of q marked.
func bigSpans(line, q string, fold bool, pal *palette, mono string) []ui.Span {
	if q == "" {
		return []ui.Span{{Text: line, Font: mono, Size: 12.5, Color: pal.text}}
	}
	hay := line
	if fold {
		hay = strings.ToLower(line)
	}
	var out []ui.Span
	for {
		i := strings.Index(hay, q)
		if i < 0 {
			break
		}
		if i > 0 {
			out = append(out, ui.Span{Text: line[:i], Font: mono, Size: 12.5, Color: pal.text})
		}
		out = append(out, ui.Span{Text: line[i : i+len(q)], Font: mono, Size: 12.5, Color: pal.text, Background: pal.attention.Alpha(0.35), Radius: 2})
		line, hay = line[i+len(q):], hay[i+len(q):]
	}
	if line != "" || len(out) == 0 {
		out = append(out, ui.Span{Text: line, Font: mono, Size: 12.5, Color: pal.text})
	}
	return out
}

// bigPrompt is the field to find text or to go to a line.
func (a *App) bigPrompt(c *ui.Context, pal *palette, l *fileLook) {
	b := l.big
	label, hint := locales.LookFind(), locales.LookFindHint()
	if b.prompt == promptGoTo {
		label, hint = locales.LookGoToLine(), locales.LookGoToLineHint()
	}
	ui.Row(c.Key("prompt")).Height(40).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.textMuted).SingleLine().Shrink(0)
		in := ui.TextInput(c.Key("q"), &b.query).Grow(1).Height(26).FontSize(fsBody).Placeholder(hint).AutoFocus()
		if in.Submitted() {
			switch b.prompt {
			case promptFind:
				b.find = b.query
				b.hit = int64(b.shown[0]) - 1
				a.bigFind(l, false)
			case promptGoTo:
				if n, err := strconv.ParseInt(strings.TrimSpace(b.query), 10, 64); err == nil && n > 0 {
					b.hit = n - 1
					b.list.ScrollTo(int(n-1), ui.Center)
				}
			}
			b.prompt = promptNone
			a.focusViewer()
		}
		if in.Shortcut(0, ui.KeyEscape) {
			b.prompt = promptNone
			a.focusViewer()
		}
		if b.searching {
			ui.Spinner(c).Size(14, 14)
		}
	})
}

// lookFind opens the find field of a large text.
func (a *App) lookFind(l *fileLook) {
	if l.current() == viewText && l.big != nil {
		l.big.prompt, l.big.query = promptFind, l.big.find
	}
}

// lookFindNext goes to the next match, or the previous one.
func (a *App) lookFindNext(l *fileLook, back bool) {
	if l.current() == viewText && l.big != nil && l.big.find != "" {
		a.bigFind(l, back)
	}
}

// bigFind looks for the search from the last match, on a goroutine; a newer
// search stops it.
func (a *App) bigFind(l *fileLook, back bool) {
	b := l.big
	if b.cancel != nil {
		b.cancel()
	}
	ctx, cancel := context.WithCancel(l.ctx)
	b.cancel, b.searching, b.note = cancel, true, ""
	from := b.hit + 1
	if back {
		from = max(b.hit, 0)
	}
	q, tf := b.find, b.tf
	go func() {
		line, err := tf.Find(ctx, q, from, back)
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			b.searching = false
			switch {
			case errors.Is(err, preview.ErrNotFound):
				b.note = locales.LookNoMoreMatches(q)
			case err != nil:
				b.note = lookErrText(err)
			default:
				b.hit = line
				b.list.ScrollTo(int(line), ui.Center)
			}
		})
	}()
}
