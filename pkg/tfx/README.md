# tfx

Terminal text effects as a Go library. Feed it text or a captured cell grid,
pick an effect, and pull frames off it one at a time.

**This is a port of a port.** [TerminalTextEffects][tte] by
[ChrisBuilds][chrisbuilds] is the original: every effect and the whole
animation engine are that project's design. [ttfx][ttfx] by omacom-io
translated it to Rust. This package translates ttfx to Go. All three are MIT.
See [NOTICE](NOTICE) for the full chain and [LICENSE](LICENSE) for the
copyrights, both of which are preserved. If you like these effects, the credit
belongs upstream.

[tte]: https://github.com/ChrisBuilds/terminaltexteffects
[chrisbuilds]: https://github.com/ChrisBuilds
[ttfx]: https://github.com/omacom-io/ttfx

## Using it

```go
terminal := tfx.NewTerminalFromText("hello", tfx.TerminalConfig{Width: 80, Height: 24})
engine := tfx.NewEngine(terminal, tfx.NewRng(1))

effect := tfx.NewDecrypt(tfx.DefaultDecryptConfig())
if err := effect.Build(engine); err != nil {
    return err
}
for effect.Advance(engine) {
    fmt.Print("\x1b[H", engine.Frame())
}
```

`Advance` does not return the frame. Read it with `engine.Frame()` for an ANSI
string, or `engine.FrameRows()` for rows of visuals you can style yourself.
That is the one place this port deliberately differs in shape from ttfx, which
writes to a tty it owns.

To animate a screen rather than a string, hand it a cell grid:

```go
terminal := tfx.NewTerminalFromCells(cells, tfx.TerminalConfig{
    Width:                 cols,
    Height:                rows,
    ExistingColorHandling: tfx.DynamicExistingColors,
})
```

`DynamicExistingColors` makes every character resolve back to the colour it
arrived with, so the screen reassembles as itself rather than in the effect's
own palette.

## What is here

The engine, in the shape ttfx found it:

| Piece | What it is |
| --- | --- |
| `Coord`, `geometry.go` | 1-based grid coordinates, origin bottom left, lines and bezier curves |
| `Color`, `Gradient` | colour ramps and the coordinate mappings effects paint across the canvas |
| `Easing` | the thirty-one standard curves |
| `Waypoint`, `Path`, `Motion` | where a character goes and how fast |
| `Frame`, `Scene`, `Animation` | what a character looks like over time |
| `Event`, `Action` | how a scene or path hands off to the next one |
| `Character` | one cell: its animation, its motion, its handlers |
| `Canvas`, `Terminal` | the grid, the character populations, and the frame painter |
| `Engine` | the stepping loop that ties all of it together |

Four effects, chosen to exercise different parts of that engine and to work
over a whole screen of arbitrary content rather than over a centred banner:

| Effect | What it shows |
| --- | --- |
| `decrypt` | per-character scenes and scene-to-scene chaining, no motion at all |
| `rain` | paths, easing, and a path completion handing off to an animation |
| `waves` | eased scenes released in bands, a sweep with no motion at all |
| `vhstape` | paths driving synced scenes, row groups, and several phases |

## Adding an effect

Write one file. Implement `Build` (set up scenes and paths on every character)
and `Advance` (release a few characters, call `engine.Update()`, say whether
you are done), and call `Register` from an `init`. The four here are 160 to
430 lines each and the engine does the rest.

## Differences from ttfx

* No parity with the Python original, and no Mersenne Twister clone. The same
  effect will not produce the same frames as either upstream. `NewRng(seed)`
  makes a run reproducible within this package, which is what the tests need.
* No command line, no tty writer, no resize handling. The host owns the screen.
* Four effects rather than thirty-five.
* Rounding quirks that change how effects look **are** kept: half-to-even
  rounding on coordinates, floor division on gradient channel steps, and the
  bezier arc-length estimate that stops at t=0.9. Removing them would retune
  every effect by a little, silently.
