# Sketch

tuios draws eight Mermaid diagram types itself: flowchart, sequence, state, class, ER, gantt, pie and mindmap. This plugin brings the other 25, such as timelines, git graphs and sankey diagrams, to `tuios mermaid`.

```sh
tuios plugins install sketch
```

The plugin holds no grant and runs nothing on its own. It is the `sketch` program of the tuios release. `tuios mermaid` runs it for a diagram of another type, with the flags both take: `--format`, `--out`, `--cols`, `--diagram` and `--theme`. A TML panel draws such a diagram with it too, once per change of the diagram.

sketch is a port of pele (MIT). Its licence is in the archive of the release.
