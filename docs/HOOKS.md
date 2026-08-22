# Hooks

The hooks reference lives on the docs site: https://tuios.gaurav.zip/docs/hooks

Nine events, each running a shell command with `TUIOS_*` environment variables carrying the facts; the site page lists every event and every variable. Hooks are read once at startup from the `[hooks]` table; see the [configuration reference](https://tuios.gaurav.zip/docs/configuration) for the table itself.
