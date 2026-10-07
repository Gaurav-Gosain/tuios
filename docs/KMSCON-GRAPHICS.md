# Images on kmscon

tuios runs well on kmscon, the KMS/DRM console. kitty graphics and sixel
images do not show there, because kmscon draws neither. This document records
what was found, the options, and what tuios does now.

## Result

tuios draws a pane's sixel image as block glyphs when the host terminal has no
graphics protocol. Each image cell becomes one glyph with a foreground and a
background colour. It is on by default, and only on a host that answers for
neither sixel nor kitty graphics. A host with either gets the real picture and
is never sent glyphs.

The default, `auto`, picks the glyphs from `TERM`:

| `TERM` | Glyphs | Why |
| --- | --- | --- |
| `kmscon` | Octants (2x4) | kmscon's built-in Unifont has them. |
| `linux` | Half blocks (1x2) | Console fonts hold 256 or 512 glyphs: the CP437 block set and nothing finer. |
| anything else | Quadrants (2x2) | In the Basic Multilingual Plane; every font with block elements has them. |

![The glyph sets: octant, sextant, quadrant and half in 24-bit colour, octant at 256 colours, and half blocks at 16 colours](images/kmscon-glyph-sets.png)

![auto with TERM=kmscon, TERM=linux and TERM=xterm-256color](images/kmscon-auto.png)

These are the host's frames from the e2e tests, drawn by `internal/shot`. The
16-colour frame is drawn in the xterm palette; the Linux console shows the VGA
colours.

At 16 colours the glyphs are always half blocks, dithered to the 16 colours.
`appearance.image_symbols` names a set outright, or `off`. See
[CONFIGURATION.md](CONFIGURATION.md#images-on-a-terminal-without-graphics).

## What kmscon supports

- **No image protocol.** kmscon has no sixel, no kitty graphics and no iTerm2
  images. The open request is
  [kmscon/kmscon#138](https://github.com/kmscon/kmscon/issues/138). A
  maintainer said there that libtsm "only supports a table of cells", and
  that sixel or kitty images need a rewrite of most of libtsm and a large part
  of the renderer. [#301](https://github.com/kmscon/kmscon/issues/301) says
  the same.
- **Where the project lives.** The maintained repository is now
  [kmscon/kmscon](https://github.com/kmscon/kmscon), with libtsm at
  kmscon/libtsm. Aetf/kmscon points there. The latest release is kmscon
  10.0.4 with libtsm 4.8.0 (2026-09-25), which is also the version on this
  machine.
- **No sixel work upstream.** No branch, pull request or issue in kmscon or
  libtsm adds image storage to the cell grid.
- **Renderers.** Two remain: bbulk (software, drm2d) and gltex (OpenGL ES,
  drm3d, `--hwaccel`). The mouse pointer recently got a `draw_pointer` hook in
  both, and a hardware cursor plane. An image layer would need the same kind
  of hook, plus image storage per cell in libtsm.
- **Glyphs.** kmscon draws every character from its font engine (freetype,
  pango, psf, unifont, 8x16). It does not draw block elements itself. Its
  embedded Unifont data covers U+1FB00..U+1FBFF and U+1CD00..U+1CDFF, and
  code points past U+FFFF work since early 2026. `--font-engine=unifont`
  therefore draws sextants and octants.
- **Colour.** kmscon 10 sets `TERM=kmscon` and `COLORTERM=truecolor`. The
  terminfo entry says 256 colours. tuios reads `COLORTERM` and draws in 24-bit
  colour.

## How other console programs draw pixels

| Program | How it draws | Works beside kmscon? |
| --- | --- | --- |
| fbi, fim, fbv | Write to `/dev/fbN`. fbi needs the real VT, not a pty. | No. See below. |
| fbcon, simpledrm | The kernel's own console and its DRM fbdev emulation. | Not while kmscon is DRM master. |
| mpv `--vo=drm` | Takes DRM master and scans out its own buffers; `--drm-draw-plane` picks a plane. | Only after kmscon releases master: `kmscon-launch-gui` sends `OSC setBackground`, and keyboard input then breaks ([#406](https://github.com/kmscon/kmscon/issues/406)). mpv `--vo=tct` (text) or `--vo=sixel` are the in-terminal options. |
| w3m-img | `w3mimgdisplay` has an fb backend on `/dev/fb0` (`$FRAMEBUFFER`). | No. Same as fbi. |
| chafa | Symbols when the terminal has no graphics: half, quad, sextant, octant (since 1.16), braille and more. | Yes. It is the model for what tuios now does. |
| yaft | A framebuffer terminal with experimental sixel, drawing into `/dev/fb0` itself. | It replaces kmscon; it does not run beside it. |

## Can a second process draw while kmscon holds DRM master?

No. In Linux 6.17, `SETCRTC`, `SETPLANE`, `PAGE_FLIP`, `DIRTYFB`, `ATOMIC` and
`CREATE_LEASE` all need DRM master. `CREATE_DUMB` and `ADDFB2` do not, so a
second process can make a buffer but cannot put it on screen. The fbdev
emulation calls `drm_master_internal_acquire()` and returns `-EBUSY` while
another master exists, so writes to `/dev/fb0` land in a buffer that is not
being scanned out. kmscon still has an fbdev backend (`no-drm`); there it owns
the framebuffer and repaints damaged cells over anything written beside it. A
DRM lease would work, but only the master can create one, and kmscon has no
lease support.

## Options

| Option | Without root or DRM master? | Cost | Verdict |
| --- | --- | --- | --- |
| (a) Draw to `/dev/fb0` or a DRM plane at cell positions, redraw after each flip | No | Needs master, a lease kmscon does not offer, or kmscon in the background. With the fbdev backend it races kmscon's repaints. Device access from a multiplexer is a security boundary of its own. | Not practical. |
| (b) Sixel or kitty graphics in kmscon | Yes, once merged | Image storage per cell in libtsm, a draw hook in bbulk and gltex, scrolling and erase semantics. Months of work; the maintainers will not do it themselves. | The right long-term fix, and worth contributing, but not something tuios can ship. |
| (c) tuios draws images as Unicode block glyphs | Yes | A font with the glyphs. Two colours per cell. | **Built.** Works on kmscon, plain SSH clients and any terminal without graphics. |
| (d) tuios as its own DRM console | No | Seat management (logind or seatd), VT switching, input, font rendering, a renderer: a second kmscon. | Assessed only. Too large for the gain. |

**Recommendation:** (c) now, as tuios's own fallback for any host without
graphics. (b) is the path to real pixels on kmscon; it starts with a libtsm
design for image cells and is best proposed on #138 before any code.

## What tuios does

`internal/mosaic` turns a picture into cells. A cell is split into sub-cells:
2x4 for octants, 2x3 for sextants, 2x2 for quadrants and 1x2 for half blocks.
Each sub-cell is the average of the pixels under it, taken in linear light.
The sub-cells are then split into the two groups whose means in OKLab leave
the least error, by trying every split (128 for an octant). The glyph is the
shape of the foreground group. A transparent sub-cell keeps the pane's own
background. At 256 colours the sub-cells are dithered with a 4x4 Bayer matrix
and snapped to the xterm palette (entries 16 to 255), so a gradient between
two palette entries shows as a pattern of both.

The sixel passthrough has a fourth mode, `symbols`, beside `sixel`, `kitty` and
the placeholder box. The image model does not change: the emulator marks the
cells an image covers, and the frame scan finds the marks. In `symbols` mode
the scan writes each marked cell as its glyph. So the picture moves with the
pane, scrolls into the scrollback, is cut by popups and other panes, dims under
a modal, and goes when its cells are cleared or overwritten, like any text.
Nothing is written to the host after the frame.

The glyphs are drawn once per image, on the pane's PTY reader, and kept with
the image. They count toward the pane's 16 MiB image budget. A frame only
looks them up.

In this mode a pane is told it can draw sixel (DA1 attribute 4). Programs that
choose between sixel and their own text output, such as chafa, timg, lsix and
yazi, then send sixel. A client that attaches later with real graphics shows
the same image as a picture. In daemon mode the client tells the daemon in its
hello (`symbol_images`) and in the graphics update an SSH client sends after
its DA1 answer. An older daemon ignores the field and keeps telling the panes
no sixel.

`appearance.image_symbols` takes `auto`, `octant`, `sextant`, `quadrant`,
`half` or `off`. `auto` reads the host's `TERM` (the local one, or the one an
SSH client sent). `off` is the old behaviour: a box, and no sixel in DA1. A
change in the settings page applies on the next frame, except to images that
arrived while it was `off`: those were never decoded and keep the box.

`tuios screenshot` and the e2e frames draw sextants and octants themselves
(`internal/shot`), as kitty and Ghostty do, since the embedded font has none.

### Not done

- **Kitty graphics images.** A pane on such a host is still told there are no
  kitty graphics, so kitty-only programs use their own fallback. Drawing them
  as glyphs means answering the query, decoding PNG and raw transmissions, and
  turning placements into marked cells. That is the next step if it is wanted.
- **Glyph detection.** A terminal cannot be asked whether its font has a
  glyph. `auto` goes by `TERM`, and the setting overrides it.

### The 16-colour floor

At 16 colours (`TERM=linux`, or any host tuios draws for in 16 colours) the
picture is always drawn as half blocks, top and bottom of a cell, whatever the
setting says. Each half is dithered to the 16 colours with a 4x4 Bayer matrix
and sent as an ANSI index, so the terminal paints its own palette; the choice is
made against the Linux console's default (VGA) palette. Only the eight dark
colours are used as a background: the Linux console gives bright backgrounds to
blink. A cell whose two colours are both bright gives the one that loses least
to its nearest dark colour.

Below a measured fidelity the box is shown instead. Fidelity
(`mosaic.Fidelity`) is the correlation between the picture's lightness and the
cells' lightness, each averaged over 2x2-cell blocks so a dither pattern counts
as the shade it makes. The threshold is `mosaic.MinFidelity = 0.5`.

Measured on ten pictures that ship with this machine's packages (CUPS and
gutenprint test photos, wallpapers, glmark2 textures, a logo), each at full
contrast and at 30, 15, 8 and 4 per cent, 60x20 cells:

| | lowest | typical |
| --- | --- | --- |
| Half blocks, 16 colours, dithered | 0.62 (fine contour lines, full contrast) | 0.85 to 0.99 |
| The same without the dither | 0.23 (contour lines, 4 %) | 0.3 to 0.5 at 8 and 4 % |
| Half blocks, 24-bit colour | 0.96 | 0.99 to 1.00 |
| Random noise, any mode | 0.51 to 0.53 | |

Without the dither, low-contrast pictures fall to flat bands with no shape
left; 0.5 sits under every dithered result and over those. With the dither, no
measured picture fell under it, so the box is a safety net. It is shown by the
e2e control that raises the threshold (below).

chafa in a pane, through tuios's octants (left) and with
`image_symbols = "off"`, its own text output (right):

![chafa through tuios's octants, and chafa's own text](images/kmscon-chafa.png)

## Measured

On the development machine, with `GOMAXPROCS=4`, `nice -n 19` and 8 cores:

- **Quality**, mean error per channel out of 255 against the source picture
  averaged over the same sub-cells, in `TestImageSymbolsOnAHostWithoutGraphics`:
  octant 4.75, sextant 5.03, quadrant 4.87, half 4.56 in truecolor. On cells
  with an edge in them, the glyphs leave 16.4 (octant), 11.2 (sextant), 10.3
  (quadrant) and 8.2 (half) against 32 for a flat colour. At 256 colours the
  palette dominates: 25.9, and 20.1 for the cell means (21.9 without
  dithering).
- **Drawing an image**, `BenchmarkEncode`, 80x24 cells of 10x20 pixels:
  2.2 ms (half), 2.5 ms (quadrant), 3.3 ms (sextant), 6.5 ms (octant);
  3.5 to 9.6 ms with dithering. This runs once per image, off the UI.
- **Text frames.** `BenchmarkKeystrokeFrame`, run interleaved 8 times against
  the branch base: +0.4% geomean, not significant (p > 0.2 everywhere). The
  allocation counts of `BenchmarkKeystrokeFrame`, `BenchmarkClientFrame` and
  `BenchmarkRenderTerminalReal` are unchanged. A frame without image cells
  takes no new path.
- **A frame with an image.** `BenchmarkSymbolImageFrame`, a keystroke frame
  over a 60x16-cell image: about 3.5 ms with the box, 3.7 ms with quadrants,
  3.9 ms with octants. The extra time and the 378 extra allocations are the
  renderer writing more colours, the same as a pane of coloured text.

## Trying it on a real kmscon

The e2e tests stand in for kmscon with a host that answers no graphics and
sets `TERM=kmscon` and `COLORTERM=truecolor`. A real run needs a free VT and
DRM master, so it is a manual step. On a machine where VT 6 is free:

```sh
sudo kmscon --vt=6 --switchvt --font-engine=unifont
# log in, then
tuios
chafa -s 60x20 picture.png    # in a pane
```

`sudo systemctl start kmsconvt@tty6.service` does the same through systemd,
with the options in `/etc/kmscon/kmscon.conf` (`font-engine=unifont`).
