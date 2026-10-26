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
   A data attribute takes a state path with no braces: `rows="state.services"`.
   A component that holds nothing takes its text in attributes:
   `<stat label="Passed" value="{state.passed}"/>`.
4. Actions run on a key or a click: `set('state.x', v)` and `toggle('state.x')`
   change the state; `emit('name', v)` tells you; `close(v)` ends the surface
   with a value; `copy(v)` copies; `pane('send-text', state.run)` acts on a
   pane, with `state.run` = `{"window": "build", "text": "make\n"}` (also
   `focus` and `new-window`). Expressions have no object literals, so the
   parameters live in the state.
5. Limits: 256 KiB from a pane, 5,000 nodes, depth 32, state 1 MiB, a patch
   64 KiB, 8 open surfaces per pane.

## Look at it before and after you show it

You cannot see the person's screen. Read the surface as text instead:

```sh
tuios tml render dash.tml                   # drawn at the terminal's width, or 100
tuios tml render dash.tml --width 80 --height 24 --tab log
tuios tml render dash.tml --state '{"runs":12}'
tuios capture-pane -s "$TUIOS_SESSION" -w WINDOW   # an open surface, as drawn
```

`tml render` opens nothing. It exits 1 and prints the problems when the
document is not valid. `capture-pane` on a surface's window gives the surface
at the window's size, with its state and the tab the person has open, and
adds `surface` to the `--json` result. Read the output, fix what is wrong, and
render again. Look for text cut with `…`, empty charts, and rows that wrap.

Make it look good:

- Show data with real components: `line`, `bar`, `sparkline`, `gauge`,
  `table`, `stat`, `kv` and `mermaid`. Do not draw ASCII art in `code`. It
  does not fit a narrow pane and reads as noise.
- Keep text short. One idea per line. A long value in `kv` or a table column
  is cut at the pane's edge.
- Put sections in `tabs`, one subject on each tab. Put the most important
  numbers on the first tab, at the top.
- Give each chart a `title` and a height (`h`).

## Components

<!-- BEGIN generated from tml/catalog.go: go generate ./skills -->
Each line gives a component, its attributes and what it holds.
`*` marks a required attribute. `a|b` lists the accepted values.
`=path` marks a data attribute. It takes a state path such as `state.rows`, with no braces.
Every component also takes as each grow h id if key max-h max-w min-h min-w pad show w.
A field is a component with `name*`. Every field also takes bind=path disabled label on-change required value.

Root:

- `tml` accent on-key state title version*: holds components

Layout:

- `col`, `row` align=start|center|end|stretch gap justify=start|center|end|between|around on-press: holds components
- `grid` cols* gap: holds components
- `scroll` follow=start|end: holds components
- `spacer`
- `split` dir=h|v ratio: holds two components
- `stack`: holds components

Text:

- `code` from lang lines: holds raw text
- `diff` lang layout=unified|split: holds raw text
- `heading` level role: holds text
- `markdown`: holds raw text
- `text` align=left|center|right on-press role wrap=word|char|none: holds text

Inside text:

- `a` href*: holds text
- `b`, `i`, `s`, `u`: holds text
- `c` role*: holds text
- `kbd`: holds raw text

Data:

- `column` align=left|center|right field* format sort title
- `item` k* role v
- `kv`: holds `item`
- `list` as items*=path on-key on-select select=none|single|multi: holds components
- `stat` delta label* on-press role value*
- `table` filter key on-key on-select rows*=path select=none|single|multi sort: holds `column`
- `tree` children items*=path label on-select open select=none|single|multi

Charts. Each also takes graphics=auto|kitty|cells|ascii role title:

- `area` data*=path x y
- `bar` data*=path dir=v|h x y
- `gauge` label max value*
- `heatmap` data*=path v x y
- `histogram` bins values*=path
- `line` axis data*=path x y
- `pie` data*=path label style=pie|donut v
- `sparkline` values*=path

Media:

- `image` alt* graphics=auto|kitty|cells|ascii on-press src*
- `mermaid`: holds raw text

Status:

- `badge` on-press role: holds text
- `progress` label role value*
- `spinner` label
- `status` label state*=working|needs_input|idle|done|errored|unknown

Input:

- `button` disabled key kind=default|primary|danger on-press submit: holds text
- `checkbox`, `toggle` name*
- `form` on-key on-submit: holds components
- `input` max min name* numeric pattern placeholder
- `select` name* options*=path
- `slider` max min name* step
- `textarea` name* placeholder rows

Containers:

- `card` fold meta on-press role title: holds components
- `modal` open* title: holds components
- `tab` id* title*: holds components
- `tabs` on-change value=path: holds `tab`
- `toast` role ttl: holds text
<!-- END generated -->

Sizes: `w` and `h` take cells, `50%`, `fill` or `fill:2`. A row of buttons
wraps when it does not fit.

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
tuios tml render deploy.tml                # read how it looks
answer=$(tuios tml show deploy.tml --wait) # "yes", "no", or null when dismissed
```

`--wait` exits 0 for the person's answer, 1 when they closed it with no value
(esc), and 2 when a program closed it with `tuios tml close`. Only exit 0 is the
person's answer.

## Ask for input with a form

```xml
<tml version="1" title="New issue">
  <state>{"labels": ["bug", "docs", "feature"]}</state>
  <form on-submit="close(form)" pad="1">
    <input name="title" label="Title" required="true" placeholder="One line"/>
    <select name="label" label="Label" options="state.labels" value="bug"/>
    <toggle name="urgent" label="Urgent" value="false"/>
    <row gap="2">
      <button kind="primary" submit="true" key="enter">Create</button>
      <button key="esc" on-press="close(null)">Cancel</button>
    </row>
  </form>
</tml>
```

A `submit="true"` button runs the form's `on-submit`. `form` holds each
field's value under its `name`. With `--wait`, `close(form)` prints
`{"title":"...","label":"bug","urgent":false}`.

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
  `new-window`, asks the person (y/n). Name the pane by its name or id.
  A target that holds more than your pane is refused.
- The window name starts with the owner pane, such as "from api · Deploy".
  The owner is the pane's name, else the agent in it, such as "from claude".
  The document cannot change the owner.
- A surface your pane opens does not take the focus. The person clicks it.
- An `input` with `bind="state.x"` takes typed text, and a `submit` button closes with `form`: `<form on-submit="close(form.note)">`.
- `view.placement` is `popup` or `pane`. esc closes a popup, not a pane:
  `<text if="view.placement == 'popup'">esc close</text>`.
- A document taller than its pane scrolls, with a bar at the right. The
  focused table, list or field takes the keys first. Put what the person
  must see first at the top.
- The person may minimize a surface to the dock and restore it, as a pane
  or a popup. Its state, cursor and scroll place stay.
- The person may move it between a popup and a pane. Each surface you show is also listed in the agents view, on your Artifacts tab, where the person can answer it.
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
