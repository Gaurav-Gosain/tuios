# Images on kmscon

tuios runs well on kmscon, the KMS/DRM console. kmscon draws neither kitty
graphics nor sixel images
([kmscon/kmscon#138](https://github.com/kmscon/kmscon/issues/138)). This
document says what tuios does instead.

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

At 16 colours the glyphs are always half blocks, dithered to the 16 colours.
`appearance.image_symbols` names a set outright, or `off`. See
[CONFIGURATION.md](CONFIGURATION.md#images-on-a-terminal-without-graphics).

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
cells an image covers, and a pass over the composed frame finds the marks and
writes each marked cell as its glyph. So the picture moves with the pane,
scrolls into the scrollback, is cut by popups and other panes, and goes when
its cells are cleared or overwritten, like any text. The pass runs before the
modal scrim and the spotlight, so they shade the glyphs like text, and it gives
each glyph its pane's `dim_unfocused`. Nothing is written to the host after the
frame.

The glyphs are drawn a part at a time. The part a pane shows when the image
arrives is drawn on the pane's PTY reader. Any other cell is drawn by the frame
pass when it first comes into view. The dither is ordered, so the parts meet
without a seam. The glyphs are kept with the image and count toward the pane's
16 MiB image budget.

Drawing has a budget per pane: a quarter of wall time, with a 100 ms burst. A
pane that sends pictures faster than that, such as a video played as sixel,
gets the image box for the pictures past the budget. When the budget is back,
the frame pass draws the part on screen.

A frame with no colour (`NO_COLOR`, or output that is not a terminal) shows the
box, and its panes are not told sixel.

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
as the shade it makes. It is measured on the first part of the picture drawn.
The threshold is `mosaic.MinFidelity = 0.5`. Dithered half blocks scored 0.62
and above on every picture measured, so the box is a safety net.

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
