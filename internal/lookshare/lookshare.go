// Package lookshare builds what "Share my look" hands the person: the comment
// for the setups discussion on GitHub, one line about the setup, and where
// the screenshot goes. `tuios look share` and the in-app action both use it,
// so the two give the same comment.
//
// The comment is written for the tuios.dev gallery's parser (tuios-docs,
// lib/gallery). The parser takes the first image and the first fenced code
// block of a comment, and makes a look of the block when it is TOML. It reads
// the title from the block's name key. A comment that holds an HTML comment
// is skipped, and so is one that seems to ask to stay out of the gallery, so
// the text here holds neither. The block's text is cut at 4000 characters, and
// a cut file is no look at all.
package lookshare

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
)

// DiscussionURL is the discussion that takes setups: "Show us your tuios
// setup".
const DiscussionURL = "https://github.com/Gaurav-Gosain/tuios/discussions/849"

// MaxGalleryText is the longest code block the gallery keeps, in characters.
const MaxGalleryText = 4000

// The dock message. It fits one dock line at 80 columns, so nothing of it is
// cut. The full message, with the screenshot path, is kept for prefix+N.
const (
	MsgToast          = "Look copied. Paste it in the browser page."
	MsgToastNoBrowser = "Look copied. Prefix+N shows the steps."
)

// The lines the CLI prints and the full message holds.
const (
	MsgCopied  = "The comment is on your clipboard. Paste it into the discussion."
	MsgShot    = "Drag the screenshot into the comment: %s."
	MsgNoShot  = "Take a screenshot and drag it into the comment."
	MsgOpen    = "Open the discussion: %s"
	MsgTooLong = "The look is longer than the gallery takes. Remove some settings to share it there."
)

// DefaultTitle is the heading of a comment for a look with no name.
const DefaultTitle = "My tuios setup"

// Summary is one sentence about the setup: the theme, the glyph set, the
// borders, the dock and the rail.
func Summary(cfg *config.UserConfig) string {
	get := func(path string) string {
		v, _ := config.GetOptionValue(cfg, path)
		return strings.TrimSpace(v)
	}
	var parts []string
	if id := strings.TrimPrefix(get("appearance.theme"), config.LookAssetPrefix); id != "" {
		parts = append(parts, "the "+id+" theme")
	}
	if id := strings.TrimPrefix(get("appearance.glyphs"), config.LookAssetPrefix); id != "" && id != "default" {
		parts = append(parts, "the "+id+" glyph set")
	}
	switch b := get("appearance.border_style"); b {
	case "", "hidden":
		parts = append(parts, "no borders")
	default:
		parts = append(parts, b+" borders")
	}
	if get("startup.tiled") == "true" {
		parts = append(parts, "tiled panes")
	}
	switch p := get("appearance.dockbar_position"); p {
	case "hidden":
		parts = append(parts, "no dock")
	case "":
	default:
		parts = append(parts, "the dock at the "+p)
	}
	if get("appearance.sidebar.enabled") == "true" {
		if p := get("appearance.sidebar.position"); p != "" && p != "hidden" {
			parts = append(parts, "the rail on the "+p)
		}
	}
	return "My setup uses " + joinAnd(parts) + "."
}

// joinAnd joins items as a list in a sentence: "a, b and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return "the defaults"
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// Comment is the discussion comment: the name as a heading, the summary, and
// the look file in a TOML code block.
func Comment(name, summary string, look []byte) string {
	title := strings.TrimSpace(name)
	if title == "" {
		title = DefaultTitle
	}
	text := strings.TrimRight(string(look), "\n")
	fence := codeFence(text)
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n", title)
	fmt.Fprintf(&b, "%s\n\n", summary)
	fmt.Fprintf(&b, "%stoml\n%s\n%s\n", fence, text, fence)
	return b.String()
}

// codeFence is a run of backticks longer than any run in text, and at least
// three, so a glyph or a title that holds backticks cannot close the block.
func codeFence(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// TooLong reports whether the gallery would cut the look file.
func TooLong(look []byte) bool {
	return len([]rune(strings.TrimRight(string(look), "\n"))) > MaxGalleryText
}

// ShotPath is where the screenshot goes: the Downloads folder when there is
// one, so a file picker finds it, else tuios's own cache folder.
func ShotPath(now time.Time) (string, error) {
	name := "tuios-look-" + now.Format("20060102-150405") + ".png"
	if dir := xdg.UserDirs.Download; dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return filepath.Join(dir, name), nil
		}
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("tuios cannot find a folder for the screenshot: %w", err)
	}
	dir := filepath.Join(cache, "tuios", "share")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// authorTimeout bounds the gh call that finds the login.
const authorTimeout = 5 * time.Second

// Author is the GitHub login gh is signed in with, or "" when gh is not
// installed or not signed in.
func Author() string {
	ctx, cancel := context.WithTimeout(context.Background(), authorTimeout)
	defer cancel()
	login, err := ghpr.Login(ctx)
	if err != nil {
		return ""
	}
	return login
}

// Look exports cfg as a look file. The author is the gh login when none is
// given.
func Look(cfg *config.UserConfig, name, author string) (*config.LookExport, error) {
	if author == "" {
		author = Author()
	}
	return config.ExportLook(cfg, config.LookExportOptions{Name: name, Author: author})
}
