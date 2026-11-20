# Sketch

tuios draws eight Mermaid diagram types itself: flowchart, sequence, state, class, ER, gantt, pie and mindmap. This plugin brings the other 25, such as timelines, git graphs and sankey diagrams.

```sh
tuios plugins install sketch
```

The plugin holds no grant and runs nothing on its own. It is the `sketch` program. `tuios mermaid` runs it for a diagram of another type, with the flags both take: `--format`, `--out`, `--cols`, `--diagram` and `--theme`. A TML panel, the agents thread and quick look draw such a diagram with it too, once per change of the diagram.

An install by the name `sketch` takes the program of the tuios release. An install from this folder or from GitHub builds `bin/sketch` from the `sketch` module of the repository, so it needs Go. Run `tuios plugins build tuios.sketch` to build it again.

sketch is a port of pele (MIT). Its licence is in the archive of the release.
