# sketch

`tuios.dev/sketch` draws Mermaid diagrams as SVG, in pure Go.

It is a port of [pele](https://github.com/obsidianmd/pele), the Mermaid
renderer by [kepano](https://github.com/kepano) of
[Obsidian](https://obsidian.md), under the MIT licence. The layout, the
shapes, the safety rules and the test corpus are pele's work. The port
follows pele v1.6.0, commit `458322a5b8abb1ae57ddf127c32017ab9fb14a92`.
[LICENSE](LICENSE) has the notice of pele and of the port.
[raster/fonts/NOTICE](raster/fonts/NOTICE) has the licences of the fonts that
sketch/raster draws text in: Arimo (SIL Open Font License 1.1) and Go Mono.

## Status

It draws flowcharts, sequence diagrams, state diagrams, class diagrams, ER
diagrams, pie charts, mindmaps and gantt charts. Each other diagram type gives
an error with the code `unsupported-diagram` until its port is done.

The output is the same as the output of pele, byte for byte. The test suite
compares each diagram with an SVG that pele drew from the same source.

| Type | Goldens that match |
|---|---:|
| flowchart | 114/114 |
| sequence | 38/38 |
| state | 22/22 |
| class | 38/38 |
| er | 25/25 |
| pie | 2/2 |
| mindmap | 13/13 |
| gantt | 11/11 |

## Use

```go
svg, err := sketch.Render("flowchart LR\n  A --> B\n", sketch.RenderOptions{})
if err != nil {
	// err is an *sketch.Error with a code: syntax, semantic, limit or unsupported-diagram.
}
os.WriteFile("out.svg", []byte(svg.Data), 0o644)
```

## Text

`tuios.dev/sketch/text` draws flowcharts and sequence diagrams with box-drawing
characters, for a terminal that shows no pictures. It takes the layout that
sketch works out for the SVG and maps it to cells. The labels stay real text.

```go
d, _ := sketch.Parse(src, sketch.ParseOptions{})
lines, err := text.Draw(d, text.Options{Cols: 100})
```

`text.ErrTooWide` says the drawing does not fit the columns. The package first
asks pele for a narrower layout, which turns a flowchart that runs across to
run down. A drawing past `text.MaxCells` gives `text.ErrTooLarge`. Every
character a terminal acts on, such as ESC in a label, is written as U+FFFD.

## Layout of the code

Each Go file is the port of one pele file. The header of the file names it.
The Go files keep the names, the functions and the comments of pele, so that a
change upstream maps to a change here.

| Folder | pele folder |
|---|---|
| `.` | `src/core.ts`, `src/detect.ts`, `src/preprocess.ts` |
| `internal/types` | `src/types.ts`, `src/errors.ts` |
| `internal/text` | `src/text` |
| `internal/svg` | `src/svg` |
| `internal/layout` | `src/layout` |
| `internal/util` | `src/util` |
| `internal/diagrams/...` | `src/diagrams/...` |
| `internal/js` | none: JavaScript semantics the port needs |

`scripts/upstream.sh` lists the pele commits after the pin and the files that
each commit changes.

## Limits

The limits of pele apply. The source can have 50,000 characters. A flowchart can
have 5,000 edges. The SVG can have 4,000,000 characters. A host can set lower
limits in `RenderOptions`.

## Tests

```sh
go test ./...
```

`TestGolden` draws the 522 sources of the pele documentation corpus. It shows
how many match for each type. It fails when a diagram of a ported type does not
match.
