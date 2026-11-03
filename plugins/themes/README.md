# Themes

Two more themes for tuios: **Dusk**, dark and warm, and **Paper**, light and calm.

```sh
tuios plugins install themes
```

Then choose a theme in the theme picker, or set `theme = "dusk"` in `[appearance]`. The plugin runs no program, so it needs no approval beyond the install.

To make your own theme plugin, list each file in `tuios-plugin.toml`:

```toml
[[theme]]
file = "themes/mine.json"
```

A theme file of your own in `~/.config/tuios/themes/` with the same id wins over a plugin's.
