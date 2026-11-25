# tuios for Omarchy

The tuios agents widget for the [Omarchy](https://omarchy.org) 4 bar, and
the tuios menu, `tuios-menu`, a bash script with jq.

Install it with tuios:

```sh
tuios integration install omarchy
```

The command also adds a tuios row to the Omarchy menu. See
[docs/OMARCHY.md](../../docs/OMARCHY.md).

`tuios.agents/` is the plugin as install writes it, with the `command` and
`menu` defaults and the `tuios=` line of `tuios-menu` set to full paths. A copy is in
`internal/integration/assets/omarchy`, and a test keeps the two the same.
