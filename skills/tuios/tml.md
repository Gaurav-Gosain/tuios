# TML surfaces: show the person a result, a dashboard or a question

A TML document is a tree of known components with a JSON state. tuios draws it
in a popup or a pane, with the person's theme. Keys and the mouse work in it.
You never write colours, layout code or escapes. Use it for results, test runs,
status boards and yes/no questions.

## Five rules

1. One tree. The root is `<tml version="1" title="...">`. Its first child may
   be `<state>{...JSON...}</state>`.
2. Roles, never hex: `fg`, `dim`, `mute`, `accent`, `success`, `warning`,
   `error`, `info`, `series1` to `series6`. `#ff0000` is refused.
3. Bindings read the state: `{state.passed}`, `{len(state.failures)}`,
   `{pct(state.ok, state.total)}`. No loops, no assignment.
4. Actions run on a key or a click: `set('state.x', v)` and `toggle('state.x')`
   change the state; `emit('name', v)` tells you; `close(v)` ends the surface
   with a value; `copy(v)` copies; `pane('send-text', state.run)` acts on a
   pane, with `state.run` = `{"window": "build", "text": "make\n"}` (also
   `focus` and `new-window`). Expressions have no object literals, so the
   parameters live in the state.
5. Limits: 256 KiB from a pane, 5,000 nodes, depth 32, state 1 MiB, a patch
   64 KiB, 8 open surfaces per pane.

## Components

| Group | Components |
| --- | --- |
| Layout | `row`, `col`, `grid`, `stack`, `spacer`, `scroll`, `split` |
| Text | `text` (with `b`, `i`, `code`, `c role=`, `kbd`), `heading`, `code` |
| Data | `table` (`rows`, `key`, `sort`, `select`, `on-select`, `column field title w align format`), `list`, `kv`, `stat`, `tree` |
| Charts | `bar`, `line`, `area`, `sparkline`, `gauge`, `progress`, `histogram`, `heatmap`, `pie` |
| Containers | `card`, `tabs` with `tab`, `modal`, `toast` |
| Status | `badge`, `status`, `spinner`, `button` (`key`, `kind="primary"`, `on-press`) |

Sizes: `w`, `h` take cells, `50%`, `fill` or `fill:2`; `pad`, `gap`, `align`,
`justify`. A row of buttons wraps when it does not fit.

## Show it and get an answer

```xml
<tml version="1" title="Deploy">
  <state>{"tag": "v0.11.0", "risky": 2}</state>
  <col pad="1" gap="1">
    <text>Deploy <b>{state.tag}</b>? <c role="warning">{state.risky} risky changes</c></text>
    <row gap="2">
      <button key="y" kind="primary" on-press="close('yes')">Deploy</button>
      <button key="n" on-press="close('no')">Cancel</button>
    </row>
  </col>
</tml>
```

```sh
tuios tml validate deploy.tml              # check it first: each problem names the fix
answer=$(tuios tml show deploy.tml --wait) # "yes", "no", or null when dismissed
```

`--wait` exits 0 for the person's answer, 1 when they closed it with no value
(esc), and 2 when a program closed it with `tuios tml close`. Only exit 0 is the
person's answer.

## Own a live surface

```sh
tuios tml show dash.tml --pane --events     # prints tml_opened, then one line per event
tuios tml patch SURFACE '[{"op":"replace","path":"/passed","value":413}]'
tuios tml replace SURFACE next.tml --keep-state
tuios tml get SURFACE
tuios tml close SURFACE '"done"'
```

Each `emit('rerun', state.failures)` arrives as a `tml-event` line with
`tml.name` and `tml.value`. The close is the last line. A patch with a `test`
op that no longer matches is refused: read with `tml get`, then patch again.

## What a surface may do

- Only the pane that opened a surface may patch, replace or close it.
  Opening, changing and closing need the `write` grant.
- A `pane()` action runs with the grants of the pane that opened the surface,
  never the person's. The first `send-text` into a pane, and every
  `new-window`, asks the person (y/n). Name the pane by its name or id, not
  a number. A yes covers the pane, not the text: your grants decide what may
  be typed there.
  A target that holds more than your pane is refused, as your own `send-text`
  would be.
- The window name says who opened it, first: "from api · Deploy". The owner
  is your pane's name when no other pane has it, else "pane 3f2a91c0 (api)",
  or "pane 3f2a91c0" when your pane has no name. The document and your
  program's name cannot change the owner.
- A surface your pane opens does not take the focus. The person clicks it.
- 100 patches a second per surface; past that, `rate_limited`. Put many
  operations in one patch.
- A client attached through another machine shows a placeholder line, not the
  surface.

## Common errors

| Error | Fix |
| --- | --- |
| `unknown_component` `<chart-bar>` | Use the name in `did_you_mean`, here `bar`. |
| `hex_color` | Use a role, such as `role="error"`. |
| `binding_missing` (warning) | The state has no such path. Add it to `<state>`. |
| `missing_attribute` `version` | Write `<tml version="1">`. |
| `bad_action` | The actions are `set`, `toggle`, `emit`, `close`, `copy`, `tab`, `sort`, `filter`, `fold`, `focus`, `open` and `pane`. |
