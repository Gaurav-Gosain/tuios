# loom

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/loom.svg)](https://pkg.go.dev/tuios.dev/loom)

`tuios.dev/loom` draws [TML](https://pkg.go.dev/tuios.dev/tml) documents into
terminal cells. You give it a document and a size. It gives you a cell
buffer, which renders to ANSI text or to plain text, and whose cells a host
can read to composite.

```go
doc, _, err := tml.Check(src, nil)
s, err := loom.New(doc, &loom.Options{Palette: loom.DefaultPalette(loom.Color256)})
buf := s.Render(80, 24)
fmt.Print(buf.Render()) // ANSI; buf.String() gives plain text
```

## Command line

`tml` shows a TML document in the terminal, drawn by loom.

```sh
go install tuios.dev/loom/cmd/tml@latest
```

```sh
tml dashboard.tml                       # full screen, with keys and the mouse
tml --print --width 100 report.tml      # draw once, as text
tml --check report.tml                  # print each problem in the document
answer=$(tml confirm.tml)               # the value of close(), as JSON
```

- In a terminal the document opens full screen. Press `q` or `esc` to quit
  when the document does not use the key, or `ctrl+c` at any time.
- A `close` action quits and prints its value as JSON. A `copy` action copies
  with OSC 52. `emit`, `open`, `pane` and links need a host such as tuios, so
  they do nothing here.
- When the output is not a terminal, or with `--print`, `tml` draws the
  document once and writes it as text.

## What it draws

| Group | Components |
| --- | --- |
| Layout | `row`, `col`, `grid`, `stack`, `spacer`, `scroll`, `split` |
| Text | `text` with `b`, `i`, `u`, `s`, `code`, `c`, `kbd` and `a`, `heading`, `code` (plain) |
| Data | `table` (sort, select, scroll), `list`, `kv`, `stat`, `tree` |
| Charts | `bar`, `line`, `area`, `sparkline`, `gauge`, `progress`, `histogram`, `heatmap`, `pie`, through [tuios.dev/plot](https://pkg.go.dev/tuios.dev/plot) |
| Containers | `card`, `tabs`, `modal` (in place), `toast` |
| Status | `badge`, `status`, `spinner`, `button` |
| Forms | `input` and `textarea` take typed text, with a caret and the readline keys. `submit` buttons and `enter` in an input run the form's `on-submit`. The other fields show their label and value. |

`markdown`, `diff` and `mermaid` show their source. `image` shows its alt text.
Nothing is left out without a mark.

Columns and areas get an axis when they have the room. A line with more than
one series shows a legend at the right of its title. A one-row sparkline
with a title shows its last value after the trend.

## Layout

Layout works like flex boxes for cells. `w` and `h` take cells, a percentage,
`fill` or `fill:N`. `min-w`, `max-w`, `min-h`, `max-h`, `pad`, `gap`, `grow`,
`align` and `justify` work as their names say. When a column is too short,
tables, lists, text, cards and tabs give rows back first. A box never draws
outside its parent.

A row that holds a button wraps when its children do not fit side by side:
whole buttons go to the next line instead of a cut label. A spacer goes with
the child after it, so a hint at the right stays at the right on its line.

A binding can read `view.width`, `view.height` and `view.band`. The band is
`narrow` below 72 cells, `medium` below 120 and `wide` from 120. Use it to
change the layout with the width, such as `if="view.band == 'wide'"`.

## Colour

Every colour is a role, such as `success`, `mute` or `series2`. You pass a
`Palette` at the depth of the terminal: `TrueColor`, `Color256`, `Color16` or
`ASCII`. `DefaultPalette` gives the tuios palette at each depth, and
`NewPalette` builds one from your theme.

At 16 colours there are no grounds. The cursor row and a primary button use
reverse video. In ASCII loom also draws its own marks in ASCII.

## Updates

The surface keeps its tree between frames. A state patch marks only the
boxes whose bindings read the changed paths. The next `Render` evaluates
those boxes again and repaints only the area that changed. `Damage` returns
the rectangles repainted since its last call, including renders that input
caused. A `Render` with nothing changed does no work.

On a 200-row table at 200 by 55 cells, a full render takes about 0.6 ms. The
redraw after a patch to one binding takes about 15 µs. The tests hold both
to a budget.

## Input

`Key` and `Mouse` take input. Tabs, sorting, the cursor, folding, scrolling,
`set` and `toggle` run at once, with no round trip to the owner. `emit`,
`close`, `copy`, `open` and `pane` come back to your program as `Effects`.

A button's `key` works from anywhere on the surface, except while a text
field has the focus. `tab` and `shift+tab` move the focus. The focused table
or list takes the arrow keys, `enter` and `s` (sort by the next column).

A text field takes the focus from `tab` or a click, never by default. It then
takes every key that types or edits: printable keys, `space`, `backspace`,
`delete`, `left`, `right`, `home`, `end`, `ctrl+a`, `ctrl+e`, `ctrl+b`,
`ctrl+f`, `ctrl+u`, `ctrl+k`, `ctrl+w` and `alt+backspace`. `Paste` puts text
at the caret. `enter` submits the form in an input and starts a line in a
textarea. `esc` leaves the field. A field with `bind` writes that state path;
one without keeps its text in the view, where `form.NAME` reads it.

## Links and time

A click on an `a` link comes back as a `link` Effect, so the host can ask the
person before it opens anything. loom writes no OSC 8 link unless the host
sets `Options.Hyperlinks`.

A binding that calls `ago()` changes with the clock. `NeedsClock` says
whether a surface has one, and the host then calls `Tick` about once a
minute. `Tick` evaluates only those boxes. A surface without `ago()` needs
no timer.

## Limits

- loom draws any tree, valid or not, at any size up to 4096 by 4096 cells,
  without a panic. Layout measures each box once per width in a pass, so
  deep nesting costs a bounded number of measures.
- Every number that sizes something, such as `bins` or `rows`, is clamped
  to its range, and NaN and infinities take the default. A document cannot
  make loom allocate without bound.
- It drops control characters at the cell.
- The store comes from `tml.StoreFor`, so a patch cannot grow `each`,
  `list` or the rows past the document limits.
- One frame has a budget of 1,000,000 expression steps. Each box gets an
  equal share: 1,000,000 divided by the most boxes the document can build
  (its components, plus its each limit when it repeats anything), and at
  least 100 steps. The share depends only on the document, so a box shows the
  same values in a full render and when it is built again alone, and the
  bindings of a frame never spend more than the budget. A table's column
  sampling and its rows in view each get one more share.

The package imports `tuios.dev/tml`, `tuios.dev/plot`, ultraviolet and
`charmbracelet/x/ansi`. It imports no tuios package.
