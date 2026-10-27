//go:build ignore

// contributors-mosaic writes assets/contributors.svg: the contributors'
// avatars laid out so they spell TUIOS, which the README shows as its
// contributors section. scripts/contributors.sh runs it, and the
// contributors workflow runs that every week:
//
//	go run ./scripts/contributors-mosaic.go [owner/name]
//
// The list comes from GitHub's contributors API. People only: an entry is
// kept when its type is "User", its login does not end in "[bot]" and it is
// not in .github/contributors-exclude. GH_TOKEN or GITHUB_TOKEN is used when
// set, for the rate limit.
//
// The avatars are embedded, because GitHub shows the SVG through an <img>,
// which loads nothing from outside the file. The output carries no
// timestamp, so a week with no new contributors rewrites nothing.
//
// The letters are pen strokes, and everyone gets one seat spaced evenly
// along them, most contributions first. The word grows with the list
// instead of having a fixed number of seats, so it is always whole and
// always holds everyone.
package main

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	defaultRepo = "Gaurav-Gosain/tuios"
	excludePath = ".github/contributors-exclude"
	outPath     = "assets/contributors.svg"

	pitch  = 52 // distance between neighbouring avatars along a stroke
	avatar = 44 // avatar diameter
	margin = 8

	// Below this many seats the letters are too sparse to read, so a short
	// list goes round again. Only a young fork gets there.
	minSeats = 45
	// The space between letters, in letter heights.
	letterGap = 0.35
)

type pt struct{ x, y float64 }

// A stroke is one pen line of a letter, in the letter's own units: one unit
// tall, y going down. pts is the line sampled finely enough to measure.
type stroke struct {
	pts    []pt
	closed bool
	// skipStart is for a stroke that starts on another one, like the T's
	// stem under its bar: the seat at the join belongs to the other stroke.
	skipStart bool
}

type glyph struct {
	width   float64
	strokes []stroke
}

var word = []glyph{
	{0.8, []stroke{ // T
		{pts: line(pt{0, 0}, pt{0.8, 0})},
		{pts: line(pt{0.4, 0}, pt{0.4, 1}), skipStart: true},
	}},
	{0.7, []stroke{ // U
		{pts: join(line(pt{0, 0}, pt{0, 0.65}), arc(0.35, 0.65, 0.35, 0.35, 180, 0), line(pt{0.7, 0.65}, pt{0.7, 0}))},
	}},
	{0, []stroke{ // I
		{pts: line(pt{0, 0}, pt{0, 1})},
	}},
	{0.8, []stroke{ // O
		{pts: arc(0.4, 0.5, 0.4, 0.5, -90, 270), closed: true},
	}},
	{0.65, []stroke{ // S, two bowls of 240 degrees
		{pts: join(arc(0.325, 0.25, 0.325, 0.25, -30, -270), arc(0.325, 0.75, 0.325, 0.25, -90, 150))},
	}},
}

func line(a, b pt) []pt {
	const steps = 64
	out := make([]pt, steps+1)
	for i := range out {
		t := float64(i) / steps
		out[i] = pt{a.x + (b.x-a.x)*t, a.y + (b.y-a.y)*t}
	}
	return out
}

// arc samples an ellipse from one angle to another, in degrees, with y
// going down: 90 is the bottom.
func arc(cx, cy, rx, ry, from, to float64) []pt {
	const steps = 128
	out := make([]pt, steps+1)
	for i := range out {
		a := (from + (to-from)*float64(i)/steps) * math.Pi / 180
		out[i] = pt{cx + rx*math.Cos(a), cy + ry*math.Sin(a)}
	}
	return out
}

// join chains lines that meet end to start into one stroke.
func join(parts ...[]pt) []pt {
	out := parts[0]
	for _, p := range parts[1:] {
		out = append(out, p[1:]...)
	}
	return out
}

func length(pts []pt) float64 {
	l := 0.0
	for i := 1; i < len(pts); i++ {
		l += math.Hypot(pts[i].x-pts[i-1].x, pts[i].y-pts[i-1].y)
	}
	return l
}

// at returns the point d along the line.
func at(pts []pt, d float64) pt {
	for i := 1; i < len(pts); i++ {
		seg := math.Hypot(pts[i].x-pts[i-1].x, pts[i].y-pts[i-1].y)
		if d <= seg && seg > 0 {
			t := d / seg
			return pt{pts[i-1].x + (pts[i].x-pts[i-1].x)*t, pts[i-1].y + (pts[i].y-pts[i-1].y)*t}
		}
		d -= seg
	}
	return pts[len(pts)-1]
}

// seats returns n points along the word, in letter units, in reading order,
// and the spacing between them. Every stroke gets the same spacing: a line
// with both ends free holds one seat more than its length divided by the
// spacing, so it ends on a face, and a loop or a stroke that starts on
// another holds exactly that.
func seats(n int) ([][]pt, float64) {
	var lens []float64
	var ends []bool
	total, open := 0.0, 0
	for _, g := range word {
		for _, s := range g.strokes {
			l := length(s.pts)
			lens = append(lens, l)
			total += l
			e := !s.closed && !s.skipStart
			ends = append(ends, e)
			if e {
				open++
			}
		}
	}
	spacing := total / float64(n-open)
	// Largest remainder, so the shares add up to n exactly.
	counts := make([]int, len(lens))
	rems := make([]float64, len(lens))
	used := 0
	for i, l := range lens {
		q := l / spacing
		if ends[i] {
			q++
		}
		counts[i] = max(1, int(q))
		rems[i] = q - float64(int(q))
		used += counts[i]
	}
	for used < n {
		best := 0
		for i := range rems {
			if rems[i] > rems[best] {
				best = i
			}
		}
		counts[best]++
		rems[best] = -1
		used++
	}

	out := make([][]pt, len(word))
	i := 0
	for gi, g := range word {
		for _, s := range g.strokes {
			k, l := counts[i], lens[i]
			i++
			for j := range k {
				var d float64
				switch {
				case s.closed:
					d = float64(j) * l / float64(k)
				case s.skipStart:
					d = float64(j+1) * l / float64(k)
				case k == 1:
					d = l / 2
				default:
					d = float64(j) * l / float64(k-1)
				}
				out[gi] = append(out[gi], at(s.pts, d))
			}
		}
	}
	return out, spacing
}

type contributor struct {
	Login         string `json:"login"`
	ID            int64  `json:"id"`
	Type          string `json:"type"`
	Contributions int    `json:"contributions"`
}

func main() {
	repo := defaultRepo
	if len(os.Args) > 1 {
		repo = os.Args[1]
	}
	client := &http.Client{Timeout: 20 * time.Second}
	people, err := fetchContributors(client, repo)
	if err != nil {
		fail(err)
	}
	if len(people) == 0 {
		fail(fmt.Errorf("%s returned no contributors", repo))
	}
	svg, err := render(client, people)
	if err != nil {
		fail(err)
	}
	if old, err := os.ReadFile(outPath); err == nil && string(old) == svg {
		fmt.Printf("%s already shows these %d contributors\n", outPath, len(people))
		return
	}
	if err := os.WriteFile(outPath, []byte(svg), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s: %d contributors\n", outPath, len(people))
}

// fetchContributors returns the people who contributed to repo, most
// contributions first. The API breaks ties in no documented order, so the
// login is the second key and a rerun draws the same mosaic.
func fetchContributors(client *http.Client, repo string) ([]contributor, error) {
	skip, err := readExclude(excludePath)
	if err != nil {
		return nil, err
	}
	var out []contributor
	for page := 1; ; page++ {
		url := fmt.Sprintf("https://api.github.com/repos/%s/contributors?per_page=100&page=%d", repo, page)
		var batch []contributor
		if err := getJSON(client, url, &batch); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, c := range batch {
			if c.Type != "User" || strings.HasSuffix(c.Login, "[bot]") || skip[strings.ToLower(c.Login)] {
				continue
			}
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b contributor) int {
		if c := cmp.Compare(b.Contributions, a.Contributions); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.Login), strings.ToLower(b.Login))
	})
	return out, nil
}

func getJSON(client *http.Client, url string, v any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	token := cmp.Or(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// readExclude returns the lower-cased logins in the exclude list. GitHub
// logins are case-insensitive. A missing list excludes nobody.
func readExclude(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "#")
		if line = strings.ToLower(strings.TrimSpace(line)); line != "" {
			skip[line] = true
		}
	}
	return skip, nil
}

func render(client *http.Client, people []contributor) (string, error) {
	n := max(len(people), minSeats)
	// The letters are sized so neighbouring seats sit one pitch apart, so
	// the word grows with the list and the avatars never overlap.
	pos, spacing := seats(n)
	scale := pitch / spacing
	gap := max(letterGap*scale, pitch)
	pad := float64(margin) + avatar/2.0

	var placed []pt
	x0 := pad
	for gi, g := range word {
		for _, p := range pos[gi] {
			placed = append(placed, pt{x0 + p.x*scale, pad + p.y*scale})
		}
		x0 += g.width*scale + gap
	}
	w := x0 - gap + pad
	h := scale + 2*pad

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img">`+"\n", w, h, w, h)
	b.WriteString("<title>TUIOS, spelled by its contributors</title>\n")
	b.WriteString(`<style>.ring{fill:none;stroke:#cba6f7;stroke-width:2}</style>` + "\n")

	avatars := map[string]string{}
	for i, c := range placed {
		p := people[i%len(people)]
		data, ok := avatars[p.Login]
		if !ok {
			var err error
			if data, err = fetchAvatar(client, p.ID); err != nil {
				return "", fmt.Errorf("%s: %w", p.Login, err)
			}
			avatars[p.Login] = data
		}
		id := fmt.Sprintf("c%d", i+1)
		fmt.Fprintf(&b, `<g><title>%s</title><clipPath id="%s"><circle cx="%.1f" cy="%.1f" r="%d"/></clipPath>`, html.EscapeString(p.Login), id, c.x, c.y, avatar/2)
		fmt.Fprintf(&b, `<image href="%s" xlink:href="%s" x="%.1f" y="%.1f" width="%d" height="%d" clip-path="url(#%s)"/>`, data, data, c.x-avatar/2, c.y-avatar/2, avatar, avatar, id)
		fmt.Fprintf(&b, `<circle class="ring" cx="%.1f" cy="%.1f" r="%d"/></g>`+"\n", c.x, c.y, avatar/2)
	}
	b.WriteString("</svg>\n")
	return b.String(), nil
}

// fetchAvatar returns the avatar as a JPEG data URI. The mosaic draws 44 px,
// so 64 px re-encoded keeps the SVG small.
func fetchAvatar(client *http.Client, id int64) (string, error) {
	url := fmt.Sprintf("https://avatars.githubusercontent.com/u/%d?s=64&v=4", id)
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	src, _, err := image.Decode(resp.Body)
	if err != nil {
		return "", err
	}
	// JPEG has no alpha, so a transparent avatar goes onto white first.
	flat := image.NewRGBA(src.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, src.Bounds().Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: 80}); err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "contributors-mosaic:", err)
	os.Exit(1)
}
