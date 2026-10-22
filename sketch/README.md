# sketch

`tuios.dev/sketch` draws Mermaid diagrams as SVG, in pure Go.

It is a port of [pele](https://github.com/obsidianmd/pele), the Mermaid
renderer by [kepano](https://github.com/kepano) of
[Obsidian](https://obsidian.md), under the MIT licence. The layout, the
shapes, the safety rules and the test corpus are pele's work. The port
follows pele v1.8.0, commit `8fad23bd51783e4df1204e177a7fb0d2a30dcd13`.
[LICENSE](LICENSE) has the notice of pele and of the port.
[raster/fonts/NOTICE](raster/fonts/NOTICE) has the licences of the fonts that
sketch/raster draws text in: Arimo (SIL Open Font License 1.1) and Go Mono.

## Status

It draws flowcharts, sequence diagrams, state diagrams, class diagrams, ER
diagrams, pie charts, mindmaps, gantt charts, XY charts, timelines, quadrant
charts, user journeys, kanban boards, git graphs, sankey diagrams, block
diagrams, architecture diagrams, requirement diagrams, C4 diagrams, packets,
radar charts, treemaps, info diagrams, tree views, ishikawa diagrams, Cynefin
diagrams, event models, venn diagrams, wardley maps, railroad diagrams,
swimlane diagrams, use case diagrams and agent flows: every type that pele
draws. A type that pele does not draw gives an error with the code
`unsupported-diagram`.

The output matches the output of pele, except links. The test suite compares
each diagram with an SVG that pele drew from the same source, byte for byte.

Links go through Go's `net/url`, not the WHATWG URL parser of a browser, so
some links differ from pele's. sketch writes no link for `http:host` or
`https:/host`, where a browser reads `http://host/`. It writes an
international host as `%CF%89`, where pele writes `xn--bya`. It reads U+2028
and U+2029 as part of a line, so a scheme after one is not seen as at the
start of a line. It folds `ſ` and `K` to s and k, so it blocks `javaſcript:`,
which pele keeps as a relative link. It writes `(`, `)`, a soft hyphen or `;`
in the user part in another form, and `#` in a fragment as `%23`. The last gate is
the same scheme allowlist as pele's: no link sketch writes leads to a scheme
other than http, https, mailto or tel.

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
| xychart | 8/8 |
| timeline | 14/14 |
| quadrantChart | 3/3 |
| journey | 1/1 |
| kanban | 3/3 |
| gitGraph | 35/35 |
| sankey | 8/8 |
| block | 30/30 |
| architecture | 6/6 |
| requirement | 9/9 |
| c4 | 6/6 |
| packet | 4/4 |
| radar | 3/3 |
| treemap | 8/8 |
| info | 2/2 |
| treeView | 13/13 |
| ishikawa | 1/1 |
| cynefin | 3/3 |
| eventmodeling | 12/12 |
| venn | 8/8 |
| wardley | 19/19 |
| railroad | 14/14 |
| swimlane | 13/13 |
| usecase | 24/24 |
| agentflow | 12/12 |

`testdata/extra` holds more cases that pele drew, which its corpus does not
cover, such as Chinese and Japanese labels that wrap. They match too.
The cases of `math-typeset.json` are drawn with a fixed math resolver, the
same one in `gen.mjs` and in `golden_test.go`.

## Use

```go
svg, err := sketch.Render("flowchart LR\n  A --> B\n", sketch.RenderOptions{})
if err != nil {
	// err is an *sketch.Error with a code: syntax, semantic, limit or unsupported-diagram.
}
os.WriteFile("out.svg", []byte(svg.Data), 0o644)
```

`svg.Type` is pele's name for the diagram type, such as `flowchart`.
`svg.MermaidType` is the name Mermaid gives it, such as `flowchart-v2` for
`graph TD`. A `Diagram` and an `Error` carry both names too. A parse error
names the diagram type even when the parser of the type does not, as pele
v1.8.0 does.

## Math

A label of a flowchart, sequence, swimlane, block, class, ER, kanban, mindmap,
requirement, state or use case diagram can hold TeX between `$$` marks.
`RenderOptions.Math` is a `MathResolver`: it gets the TeX and the font size
and gives SVG or HTML markup with its size in pixels. sketch has no typesetter.
With no resolver, or when the resolver has nothing for an expression, the label
shows the `$$...$$` source as text. Diagram titles always show text. The
markup goes into the SVG as it is, as pele writes it: give only markup you
trust. `sketch/text` draws the source.

The resolver gets the TeX as the diagram's author wrote it, with entities
decoded. Treat it as untrusted input. Escape it in any markup you build from
it, and do not enable commands that make links or run scripts, such as KaTeX's
`trust: true` or MathJax without its `ui/safe` extension.

## Text

`tuios.dev/sketch/text` draws flowcharts, sequence diagrams, state diagrams,
class diagrams, ER diagrams, pie charts, mindmaps, gantt charts, timelines, XY
charts, quadrant charts, user journeys, kanban boards, git graphs, sankey
diagrams, block diagrams, architecture diagrams, requirement diagrams, C4
diagrams, packet diagrams, radar charts, treemaps, info diagrams, tree views,
Ishikawa diagrams, Cynefin diagrams, event models, Venn diagrams, Wardley
maps, railroad diagrams, swimlane diagrams, use case diagrams and agent flows
with box-drawing characters, for a terminal that shows no pictures. For a
graph it takes the layout that sketch works out for the SVG and maps it to
cells. The labels stay real text. A pie chart is a bar for each slice, with
its value. pele leaves a slice under 1% out of the pie, and shows values only
with `showData`. A mindmap is a tree. A gantt chart is bars on a date axis. A
timeline runs across, and runs down when it does not fit the columns. So does
a user journey. An XY chart draws its bars and lines with tuios.dev/plot, and
turns its categories into rows when they do not fit across. It writes point
labels and bar values where they cover nothing, and leaves out a label that
has no room. A quadrant chart is a box in four parts, with its points as dots.
A kanban board puts its columns in more rows when they do not fit. A git graph
runs down, one row for each commit, with the oldest commit at the top. A
sankey diagram puts its nodes in columns of boxes joined by lines. When two
lines would join, when they cross more than three times, or when the columns
do not fit, it is a table of flows: source, target and value. Block,
architecture, requirement and C4 diagrams are drawn as a flowchart is. A block
arrow shows an arrow character. A service without a title shows its icon name.
A packet diagram is rows of bit fields. A radar chart is a table with one bar
for each curve on each axis. A treemap is nested boxes, or an indented tree
when the boxes are too small. An info diagram is its line of text. A tree view
is drawn as the `tree` command draws a folder. An Ishikawa diagram is a spine
with its causes as bones, or an indented tree when it is narrow. A Cynefin
diagram is a grid of its four domains. An event model is swimlanes in time
order, or a list of frames when it is narrow. A Venn diagram is a table of its
sets and their overlaps. A Wardley map is a grid of visibility by evolution,
or a table when it is narrow. A railroad diagram is its tracks in cells, or
its rules as EBNF text when they do not fit. Use case diagrams and agent flows
are drawn as a flowchart is. A swimlane diagram is a flowchart with its lanes
as bands.

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

`go test -tags differential ./internal/js/` needs node on the path. It checks
the port of V8's `Math.log` and `Math.log10` against node on 1,086,347 inputs,
and `10 ** k` for every whole `k` from -400 to 400. They match bit for bit.
