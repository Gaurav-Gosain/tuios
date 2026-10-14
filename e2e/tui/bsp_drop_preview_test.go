package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// appearance.drop_preview picks what a BSP drag draws where the pane would
// land (fill, outline, off), and appearance.drop_preview_delay how long the
// pointer rests on one zone before that shows. The board and the gesture are
// the ones in bsp_drop_zone_test.go.
//
// How these could pass wrongly, written down first:
//   - off could hide the highlight by breaking the drop. The off case drops on
//     A's left edge and holds the result to the split.
//   - outline could draw the fill too, or nothing at all. The outline case
//     holds every visible ring cell to the outline and every visible inside
//     cell to the ground it had before the drag.
//   - The delay could be read only as "the zone shows eventually", which an
//     immediate highlight passes too. The delay is long against the frame
//     read right after the move, which must show nothing, and the frame read
//     well past it must show the zone.
//   - The highlight could arrive only with the next event rather than when
//     the delay runs out. Nothing moves between the two frames of the delay
//     case, so a timer that does not ask for a frame leaves the first one up.
//   - A zone left before its delay could still show when the delay runs out,
//     on the zone the pointer is on by then. The left-zone case reads a frame
//     after the first zone's delay ran out and before the second's did.

// previewDelay is long against what one frame read takes, so a frame read
// right after a move cannot be one the delay has already run out for, and
// short enough to wait out.
const previewDelay = 3 * time.Second

// TestDropPreviewOffDrawsNothing carries C to A's left edge with the preview
// off and waits past the default delay: nothing is highlighted. The release
// still splits A.
func TestDropPreviewOffDrawsNothing(t *testing.T) {
	term, base, p := dropBoardWith(t, "drop-preview-off", chromeDepths[2].env, "drop_preview = 'off'")
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	time.Sleep(time.Second)
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2b-mid-drag-later")
	d.checkHighlight(t, nil, false)
	got := d.drop(t, base)
	edgeCases[0].check(t, p, got[p.a.ID], got[p.b.ID], got[p.c.ID])
}

// TestDropPreviewOutlineDrawsOnlyTheFrame is the left-edge drop with the
// outline-only preview: the landing rectangle's outline is drawn, and the
// cells inside it keep their ground.
func TestDropPreviewOutlineDrawsOnlyTheFrame(t *testing.T) {
	term, base, p := dropBoardWith(t, "drop-preview-outline", chromeDepths[2].env, "drop_preview = 'outline'")
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	got := d.drop(t, base)
	edgeCases[0].check(t, p, got[p.a.ID], got[p.b.ID], got[p.c.ID])
	c := got[p.c.ID]
	d.checkPreview(t, &c, false, false)
}

// TestDropPreviewWaitsForItsDelay is the left-edge drop with a long delay.
// Right after the move nothing is highlighted; after the delay, with no
// further input, the landing rectangle is.
func TestDropPreviewWaitsForItsDelay(t *testing.T) {
	term, base, p := dropBoardWith(t, "drop-preview-delay", chromeDepths[2].env, "drop_preview_delay = '"+previewDelay.String()+"'")
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	d.checkHighlight(t, nil, false)

	time.Sleep(previewDelay)
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2b-mid-drag-after-delay")
	got := d.drop(t, base)
	edgeCases[0].check(t, p, got[p.a.ID], got[p.b.ID], got[p.c.ID])
	c := got[p.c.ID]
	d.checkHighlight(t, &c, false)
}

// TestADropZoneLeftBeforeItsDelayNeverShows rests C on A's left edge for half
// the delay, then moves it to A's middle. Once the left edge's delay has run
// out, and before the middle's has, nothing is highlighted; once the middle's
// has, all of A is, and the release swaps.
func TestADropZoneLeftBeforeItsDelayNeverShows(t *testing.T) {
	term, base, p := dropBoardWith(t, "drop-preview-left", chromeDepths[2].env, "drop_preview_delay = '"+previewDelay.String()+"'")
	// carry returns half a second after the pointer reached the edge.
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	edgeEntered := time.Now().Add(-500 * time.Millisecond)
	time.Sleep(previewDelay/2 - 500*time.Millisecond)

	d.x = p.a.X + p.a.Width/2
	for x := p.a.X + 2; x <= d.x; x++ {
		mouseMotion(t, term, x, d.y, tuitest.MouseLeft, 0)
	}
	middleEntered := time.Now()
	d.checkHeld(t)

	// Halfway between the two delays running out.
	time.Sleep(time.Until(edgeEntered.Add(previewDelay + middleEntered.Sub(edgeEntered)/2)))
	if time.Since(middleEntered) >= previewDelay {
		t.Fatalf("the frame between the two delays was read too late, %v after the middle was entered", time.Since(middleEntered))
	}
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2b-left-edge-delay-over")
	d.checkHighlight(t, nil, false)

	time.Sleep(time.Until(middleEntered.Add(previewDelay + time.Second)))
	d.mid = readCells(term.Screen())
	saveArtifact(t, term, d.artifactAt, "2c-middle-delay-over")
	got := d.drop(t, base)
	whole := winRect{ID: p.c.ID, X: p.a.X, Y: p.a.Y, Width: p.a.Width, Height: p.a.Height}
	if got[p.c.ID] != whole {
		t.Errorf("dropped in A's middle, C is %+v, want A's slot %+v", got[p.c.ID], p.a)
	}
	d.checkHighlight(t, &whole, false)
}

// paintShell writes a shell into base that fills its pane with text in many
// colours before it hands over to /bin/sh: plain colours, bold, a cell on a
// ground of its own, faint text, reverse video and a truecolour ink. It
// returns the [appearance] line that makes it the panes' shell.
func paintShell(t *testing.T, base string) string {
	t.Helper()
	line := `printf '\033[7mrev\033[0m \033[31mred\033[0m \033[1;34mbold\033[0m \033[42;30m green \033[0m \033[2mfaint\033[0m \033[38;2;250;180;40mamber\033[0m plain\n'`
	script := "#!/bin/sh\ni=0\nwhile [ $i -lt 40 ]; do\n" + line + "\ni=$((i+1))\ndone\nexec /bin/sh\n"
	path := filepath.Join(base, "paint.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write the paint shell: %v", err)
	}
	return "preferred_shell = '" + path + "'"
}

// TestDropPreviewFillWashesTheZone is the left-edge drop on a pane full of
// coloured text, with the default fill preview. The outline is the border's
// own glyphs in one ink. Inside it every cell is on the tint, and no cell with
// text keeps its colour, whatever ground, weight or reverse video it had.
//
// How it could pass wrongly: a pane of default-coloured blanks changes colour
// under any tint at all, so the fixture is held to having the colours the
// wash has to reach before the drag: four inks, a cell with its own ground and
// a reverse one, all inside the zone.
func TestDropPreviewFillWashesTheZone(t *testing.T) {
	term, base, p := dropBoardPrep(t, "drop-preview-fill", chromeDepths[2].env, func(base string) string {
		return paintShell(t, base)
	})
	d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
	got := d.drop(t, base)
	edgeCases[0].check(t, p, got[p.a.ID], got[p.b.ID], got[p.c.ID])
	c := got[p.c.ID]

	inks := map[tuitest.Color]bool{}
	grounds, reversed := 0, 0
	for y := c.Y + 1; y < c.Y+c.Height-1; y++ {
		for x := c.X + 1; x < c.X+c.Width-1; x++ {
			if !d.visible(x, y) {
				continue
			}
			cell := d.pre[y][x]
			inks[cell.Fg] = true
			if cell.Bg.Kind != tuitest.ColorDefault {
				grounds++
			}
			if cell.Reverse {
				reversed++
			}
		}
	}
	if len(inks) < 4 || grounds == 0 || reversed == 0 {
		t.Fatalf("before the drag the zone %+v holds %d inks, %d cells on a ground and %d reverse ones; the fixture cannot test the wash\n%s",
			c, len(inks), grounds, reversed, d.term.Snapshot())
	}
	d.checkHighlight(t, &c, false)
}

// TestDropPreviewLooks drags a pane over a fish prompt and a coloured ls
// listing, in both looks and both preview styles, and at 16 colours, and keeps
// a picture of each frame mid-drag (TUIOS_E2E_FRAMES). Each frame is held to
// the same checks as the other preview cases.
func TestDropPreviewLooks(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	cases := []struct {
		name, theme, mode string
		depth             int
	}{
		{"dark-fill", "", "fill", 2},
		{"dark-outline", "", "outline", 2},
		{"light-fill", "catppuccin_latte", "fill", 2},
		{"light-outline", "catppuccin_latte", "outline", 2},
		{"dark-16", "", "fill", 0},
		{"light-16", "catppuccin_latte", "fill", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term, base, p := dropBoardPrep(t, "drop-look-"+tc.name, chromeDepths[tc.depth].env, func(base string) string {
				fishLooks(t, base)
				lines := "preferred_shell = '" + fish + "'\ndrop_preview = '" + tc.mode + "'\n"
				if tc.theme != "" {
					lines += "theme = '" + tc.theme + "'\n"
				}
				return lines
			})
			d := carry(t, term, p, p.a.X+1, p.a.Y+p.a.Height/2)
			savePNG(t, term.Screen(), hostPalette(t, tc.theme), d.artifactAt, "drop-preview-"+tc.name)
			got := d.drop(t, base)
			c := got[p.c.ID]
			d.checkPreview(t, &c, tc.depth == 0, tc.mode == "fill")
		})
	}
}

// fishLooks gives fish a busy prompt (segments on grounds of their own, a
// faint clock) and starts it in a folder whose ls listing is in colour.
func fishLooks(t *testing.T, base string) {
	t.Helper()
	dir := filepath.Join(base, "project")
	for _, sub := range []string{"cmd", "docs", "internal", "scripts"} {
		mustMkdir(filepath.Join(dir, sub))
	}
	for name, mode := range map[string]os.FileMode{
		"build.sh": 0o755, "install.sh": 0o755, "go.mod": 0o644, "README.md": 0o644,
		"logo.png": 0o644, "release.tar.gz": 0o644, "LICENSE": 0o644,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Symlink("README.md", filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("link AGENTS.md: %v", err)
	}
	config := `set -g fish_greeting
function fish_prompt
    set_color -b 7aa2f7 1a1b26; printf ' ~/d/tuios '
    set_color -b 414868 c0caf5; printf ' feat/drop-zones '
    set_color -b f7768e 1a1b26; printf ' Go '
    set_color normal; set_color -d; printf ' v1.27.2 12:37 '
    set_color normal; set_color -b e0af68 1a1b26; printf ' gaurav '
    set_color normal; set_color green; printf '\n> '
    set_color normal
end
cd ` + dir + `
echo (set_color -d)'-- tuios: restored from Oct 14 12:36, fresh shell in '$PWD(set_color normal)
echo 'Type '(set_color cyan)'help'(set_color normal)' for instructions on how to use fish'
ls --color=always
ls -l --color=always
`
	path := filepath.Join(base, "XDG_CONFIG_HOME", "fish", "config.fish")
	mustMkdir(filepath.Dir(path))
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatalf("write the fish config: %v", err)
	}
}
