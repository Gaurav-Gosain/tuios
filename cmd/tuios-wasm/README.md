# tuios in the browser

This is the build behind [tuios.dev/learn](https://tuios.dev/learn):
the real tuios compiled to WebAssembly, running in a browser tab with no
server. Every pane runs a small fake shell (`internal/webshell`), and
`internal/learn` reports what the person does so a lesson can react.

## Build it

```sh
cmd/tuios-wasm/build.sh out/      # build the files the docs site needs
node cmd/tuios-wasm/serve.mjs out/ 8765   # then open http://127.0.0.1:8765
node cmd/tuios-wasm/smoke.mjs out/        # headless Chromium check
node cmd/tuios-wasm/smoke-lessons.mjs out/ frames/ basics  # a track by show me, a frame per step
```

`build.sh` writes:

| File | What it is |
|---|---|
| `tuios.wasm.br`, `tuios.wasm.gz` | the program, compressed. The raw file is over Cloudflare's 25 MiB limit, so only these ship |
| `wasm_exec.js` | Go's loader, from the toolchain that built the wasm |
| `webterm.js`, `webterm.css`, `xterm.css` | the renderer, sip's xterm.js bundle |
| `webterm-vtgl.js` | the vtgl renderer, which the page loads only for `?renderer=vtgl`. Only a sip that ships it as its own file has it |
| `fonts/*.woff2` | JetBrains Mono Nerd Font, regular and bold |
| `lessons.json` | every lesson, the same bytes `tuios.lessons()` returns. A page that lists tracks reads it without loading the wasm |
| `manifest.json` | the tuios commit, the sizes and the sha256 of every file. `lessons` is the sha256 of `lessons.json` |
| `index.html` | a demo page with a five-step track, for trying a change. `?track=basics` runs a track of the lessons instead |

Pass `--raw` to keep `tuios.wasm` as well, which `serve.mjs` falls back to.

Only files under `cmd/tuios-wasm/`, `internal/webshell/` and `internal/learn/`
and the `_js.go` files are browser-only. Everything else is the tuios every
other build runs. `prepare.sh` explains the two dependencies that need a patch
to build for js, and when that can go.

## The page API

The program sets a global `tuios` before it starts, and calls
`window.onTuiosReady(tuios)` if the page defined it. Set
`window.tuiosInitialSize = [cols, rows]` before starting it to size the first
frame.

| Call | What it does |
|---|---|
| `tuios.onOutput(fn)` | `fn(Uint8Array)` receives the bytes for the terminal renderer |
| `tuios.onEvent(fn)` | `fn(event)` receives every event below. Events sent before this is called are held and delivered to it |
| `tuios.input(data)` | keyboard and mouse bytes from the renderer, a string or a `Uint8Array` |
| `tuios.resize(cols, rows)` | the renderer's size in cells |
| `tuios.state()` | the current snapshot, below |
| `tuios.command(name, ...args)` | run a command, below. All arguments are strings |
| `tuios.actions()` | every keybinding action name mapped to its description |
| `tuios.unavailable()` | the actions Learn mode turns off, mapped to the note each shows |
| `tuios.commands()` | the command names `tuios.command` accepts |
| `tuios.lessons()` | every lesson as a JSON string. See [Lessons](#lessons) |
| `tuios.lesson(cmd, ...args)` | run a lesson command, below. Returns `null`, an error message as a string, or for `log` the events |
| `tuios.lessonState()` | the running lesson, as the `lesson.*` events describe it |
| `tuios.stop()` | end the program. The promise `go.run` returned resolves, and no event is sent. Drop every reference to the API and to the `Go` object after it, so the browser can free the memory. A call to the API after it throws an error |

After `tuios.stop()`, the timers that the Go runtime set can still fire.
Each one then throws "Go program has already exited". Clear them when the
promise from `go.run` resolves:

```js
go.run(instance).then(() => {
  for (const id of go._scheduledTimeouts.values()) clearTimeout(id);
  go._scheduledTimeouts.clear();
});
```

Do not call `tuios.stop()` from an `onOutput` or `onEvent` callback. The
call that sent the output or the event then throws a `TypeError`.

## Events

Every event is `{type, data, windowId?, state?}`. `windowId` is set when the
event is about one pane. `state` is the snapshot after the change, and is set
on every event that comes from a state change: all but `key`, `action`,
`notification`, the `shell.*` and the `tml.*` events.

Events come in a fixed order for one key press: `key` first, then any
`action`, then the state changes it caused.

| Type | Data | When |
|---|---|---|
| `ready` | `{}` | the program started. Carries `state` |
| `key` | `{key, mode}` | a key press, before tuios handles it. `key` is Bubble Tea's name for it (`"ctrl+b"`, `"n"`, `"enter"`), `mode` is `"window"` or `"terminal"` |
| `action` | `{name, mode}` | the exact keybinding action a key, a prefix chord, a menu row or `command("action")` ran, such as `"prefix_new_window"` or `"toggle_tiling"`. The keys of the agents view and its peek send theirs too, such as `"inbox_allow"` or `"peek_back"`. Also sent for an action Learn mode turns off |
| `mode` | `{from, to}` | `"window"` or `"terminal"` |
| `prefix` | `{from, to}` | the prefix state: `""`, `"prefix"`, `"workspace"`, `"window"`, `"minimize"`, `"layout"`, `"debug"` or `"tape"`. The leader key gives `to: "prefix"` |
| `window.open` | `{id, title, workspace, count}` | a window opened. `count` is the total over every workspace |
| `window.close` | `{id, title, count}` | a window closed |
| `window.focus` | `{from, to, title}` | focus moved; `to` is `""` when nothing has focus |
| `window.rename` | `{id, from, to}` | a window's title changed |
| `window.minimize` | `{id, minimized}` | minimized or restored |
| `window.zoom` | `{id, zoomed}` | zoomed or unzoomed |
| `window.move` | `{id, x, y, width, height, from}` | a window's place or size changed, in cells. `from` is `{x, y, width, height}` before. Sent once any animation has finished, so a snap or a retile is one event per window, not one per frame. A window that opened or closed is left out |
| `window.float` | `{id, floating}` | the window floats above the tiling, or went back into it |
| `window.workspace` | `{id, from, to}` | the window moved to another workspace. The scratch pane's workspace is 1000 |
| `workspace` | `{from, to}` | the current workspace, 1 to 9 |
| `tiling` | `{from, to}` | tiling on (`true`) or off |
| `layout` | `{from, to}` | the layout mode: `"bsp"`, `"master-stack"` or `"scrolling"` |
| `theme` | `{from, to}` | the theme id, such as `"catppuccin_mocha"` |
| `setting` | `{name, from, to}` | a look changed. `name` is `"glyphs"` (the glyph set, `"default"` for the shipped one), `"borderStyle"` (such as `"rounded"`), `"showkeys"` (the showkeys overlay, `ctrl+b D k`: `"true"` or `"false"`), or the config path of one of the looks below. Values are strings, as `tuios get-config` prints them, and an enum left unset reads as its default |
| `themeEdit` | `{changed}` | the theme editor's draft changed: `changed` is `true` once a colour differs from the saved theme, and `false` again after an undo, a save or a close |
| `overlay.open` | `{name}` | an overlay opened. Names below |
| `overlay.close` | `{name}` | an overlay closed |
| `notification` | `{message, level}` | a message in the dock. `level` is `"info"`, `"success"`, `"warning"` or `"error"`. Learn mode's notes are `"info"` |
| `agent` | `{id, from, to, message, kind, harness}` | a pane's agent state: `""`, `"working"`, `"needs_input"`, `"idle"`, `"done"`, `"errored"` or `"unknown"`. `kind` is `"approval"` or `"question"` for `needs_input` |
| `keys` | `{from, to}` | the keymap changed. Each side is `{preset, leader, lock}`: the preset (`"tuios"`, `"tmux"`, `"screen"` or `"zellij"`), the leader key, and whether lock mode is on |
| `pip` | `{from, to}` | the window picture-in-picture shows changed. `to` is `""` when it is off |
| `agents.select` | `{from, to, title, agent, kind, harness}` | the agents view's cursor moved to another agent. `from` and `to` are window ids, `""` while the view is closed. `title`, `agent` (its state), `kind` and `harness` are the agent's under the cursor now |
| `agents.tab` | `{from, to}` | the agents view's focus tab changed: `"thread"`, `"changes"`, `"checkpoints"`, `"artifacts"` or `"pane"`. `""` while the view is closed |
| `files` | `{from, to}` | the rail's files section changed. Each side is `{dir, cursor}`: the folder it lists, and the path of the row under the rail's cursor while the rail has the keyboard. Each is `""` when there is none |
| `review.move` | `{file, hunk, from}` | the review's cursor moved to another file or hunk. `file` is the path in the repository, `hunk` the index of the hunk the cursor is in, `-1` for none. `from` is `{file, hunk}` before. Opening and closing the review are `overlay.*` events |
| `review.note` | `{action, path?, line?, text?, notes}` | a review note was saved. `action` is `"add"`, `"edit"` or `"remove"`. `notes` is how many notes the review holds now |
| `review.send` | `{notes}` | `S` in the review marked this many notes as sent. Nothing leaves the tab: the dock says what `S` does on a real machine |
| `worktree` | `{repo, branch, path}` | a worktree of the fake repository was made, with `ctrl+b U` |
| `copy.yank` | `{chars}` | a yank in copy mode or the scrollback browser wrote the clipboard. `chars` is its length in characters. It comes after the key's other events |
| `tml.open` | `{surface, windowId, title, owner, target}` | `tuios tml show` opened a TML surface. `target` is `"popup"` or `"pane"`. `owner` names the pane that ran the command, as the window's frame does |
| `tml.input` | `{surface, state}` | the person changed the surface's state: a key, a click or typed text in a field that binds a state path. `state` is the whole state after it |
| `tml.place` | `{surface, windowId, target}` | the person moved the surface to a popup or a pane (`ctrl+b t f`). A `window.float` event follows |
| `tml.close` | `{surface, windowId, title, reason, value}` | the surface closed. `reason` is `"closed"` (a close action in the document, `value` is the answer), or `"dismissed"` (esc, or its window closed, and `value` is `null`) |
| `tape.start` | `{name?}` | a tape started playing |
| `tape.finish` | `{}` | the tape finished |
| `shell.start` | `{command, line, cwd}` | the fake shell started a command. `command` is the first word |
| `shell.command` | `{command, line, cwd, exitCode}` | the command finished. `exitCode` is a number: 0 for success, 127 for a command the shell does not know |
| `shell.cwd` | `{cwd}` | the shell changed directory |
| `exit` | `{error?}` | the program ended. Learn mode never quits, so this only follows a crash. `tuios.stop()` sends no `exit` event |

Overlay names: `help`, `whichkey`, `commandPalette`, `launcher`, `settings`,
`themePicker`, `keybinds`, `copyMode`, `search` (copy mode or scrollback
search), `scrollback`, `quitMenu`, `workspaceSwitcher`, `layoutPicker`,
`sidebar`, `logs`, `screensaver`, `agents` (the agents view), `mail`,
`presets` (the keybind preset picker), `goto` (the go-to picker), `hints`,
`paneLabels`, `spotlight`, `scratch` (the scratch pane is up), and over the
agents view `peek` (the prompt an agent waits on), `reply` (the reply editor),
`newAgent` (the New agent sheet) and `compare` (the attempts of a fan side by
side).
`paneLabels`, `spotlight`, `scratch` (the scratch pane is up), `railFocus`
(the rail has the keyboard, `ctrl+b e`), `fileSearch` (the palette searches
files, `ctrl+b f`), `review` (the review overlay, `ctrl+b v`) and
`worktree` (the new worktree dialog, `ctrl+b U`).
`themePicker`, `themeEditor`, `glyphPicker` (the settings page's glyph set
picker, which previews each set as the cursor moves: a `setting` event for
`glyphs` comes before the picker closes), `keybinds`, `copyMode`, `search`
(copy mode or scrollback search), `scrollback`, `quitMenu`, `workspaceSwitcher`,
`layoutPicker`, `sidebar`, `logs`, `screensaver`, `agents` (the agents view),
`mail`, `presets` (the keybind preset picker), `goto` (the go-to picker),
`hints`, `paneLabels`, `spotlight` and `scratch` (the scratch pane is up).

The looks a `setting` event names by config path:
`appearance.window_title_style`, `appearance.window_title_position`,
`appearance.window_title_align`, `appearance.border_focused_color`,
`appearance.border_focused_color_window`,
`appearance.border_focused_color_terminal`,
`appearance.border_unfocused_color`, `appearance.theme_light`,
`appearance.frame_panels`, `appearance.popup_dim`, `appearance.modal_dim`,
`appearance.gap`, `appearance.sidebar.row_style`,
`appearance.sidebar.header_style`, `appearance.dock_pill_caps` and
`appearance.dock_mode_pill`. tuios reads them again after a key, a mouse
press, a command or a tape step, so a change shows in the event that follows
that input.

## Lessons

The lessons are data in `internal/learn/lessons`: `tracks.json` lists the
tracks in order with the hub's groups and the coach's shared lines, and
`tracks/<id>.json` is one track. `lessons.schema.json` describes both. After
an edit, run `go generate ./internal/learn/lessons`. The binary embeds the
archive it writes, `lessons.tar.gz`.
`tuios.lessons()` returns them joined into one document:

```js
{
  version: 1,
  groups: [{ id: "start", title: "Start here" }, ...],
  coach: { greet, finish, hintedLead, reactions: { split: [...] }, nudges: { idle: [...] }, poke },
  tracks: [{
    id, title, blurb, audience, group, minutes,
    surfaces: ["web", "terminal"],   // where the track is ready
    next: ["layouts"], setup: [...], coach: { greet: [...], finish: [...] },
    steps: [{ id, title, note, keys: ["ctrl+b", "|", { text: "ls" }], needs, setup,
              done: { ... },         // the rule, below
              hint, showMe, explainer: { art, body, href, linkText }, learned, try,
              web: { title?, note?, hint? }, terminal: { ... } }]  // words for one surface
  }]
}
```

The coach's lines may hold `{name}` slots the page fills: `{n}` (the
workspace), `{layout}`, `{command}`, `{steps}`, `{clean}` and `{total}`.

A step's `done` is a rule, matched against the events above:

| Rule | Done when |
|---|---|
| `{"on": T, "where": {...}}` | an event of type `T`, with each `where` path holding |
| `{"changed": T, "to": V}` | a `T` event whose `data.to` is `V` |
| `{"opened": N}`, `{"closed": N}` | the overlay `N` opened or closed |
| `{"ran": C}` | the shell ran command `C` and knew it |
| `{"ranLine": RE}` | the shell ran a line that matches |
| `{"agent": S}` | a pane's agent moved to state `S` |
| `{"via": [A, ...], "then": R}` | `R`, caused by one of these actions. An entry may be `{"prefix": "workspace_prefix_move_"}` |
| `{"any": [R, ...]}`, `{"seq": [R, ...]}` | one of them, or each in turn |
| `{"times": N, "of": R}` | `R`, `N` times |
| `{"zoomed": B}`, `{"moved": true}` | a zoom in or out, or a settled move |
| `{"notified": RE}`, `{"setting": N}` | a dock note that matches, or the look `N` changed |

A `where` path is `data.x`, `state.x`, deeper such as `data.to.preset`, or
`window(data.to).agent`: a field of the window in `state.windowList` whose id
is at the inner path. Its value is a literal, or an object of checks that
must all hold: `prefix`, `suffix`, `match` (a pattern), `truthy` (true or
false), `not` (a value it must not be) and `differs` (a path whose value it
must not equal). Patterns are RE2, which JavaScript reads too. A setup entry
may name an `until` rule: the next entry waits for that event, up to 3
seconds.

A step may name `after` rules, events it depends on, such as an agent
asking, with a `waiting` line for the card. Each must have happened since
the lesson started. Until then `lesson.hint` says `waiting`, the hint ladder
does not start, and show me waits for them before it plays the keys.

The lesson engine runs in tuios. The page starts a lesson, draws the
`lesson.*` events, and sends the commands below. It keeps no step logic.

| Command | Arguments | What it does |
|---|---|---|
| `start` | `track`, `from?` | reset the screen, set the track's scene and start it. With `from`, the steps before it are played at speed first, for a resumed lesson |
| `showMe` | | type the rest of the step's keys, and count the step as hinted. A step that waits on its `after` events is played once they came |
| `skip` | | play the step's keys at speed, and move on as skipped after 2.5 seconds if they did not finish it. An explainer is skipped at once |
| `next` | | the person read the explainer step: done |
| `back` | | go back one step and set its scene again |
| `stop` | | end the lesson |
| `log` | `"on"`, `"off"` or `"get"` | keep the events the engine was fed, and return them with `get`. For tests and the key debug panel |

| Event | Data | When |
|---|---|---|
| `lesson.start` | `{track, title, total, from}` | the track's scene is set |
| `lesson.step` | `{track, index, total, id, title, note, keys, needs, hint, waiting, learned, try, explainer?}` | a step's scene is set and its card can show. The words are the web's |
| `lesson.keys` | `{index, pressed, wrong, wrongMode}` | a key lit or cleared the step's keycaps: `pressed` caps are lit |
| `lesson.hint` | `{index, level, altBlocked, noWindow, nudge, waiting}` | the hint ladder moved. `waiting`: the step's `after` events have not all come, so the card shows the step's `waiting` line, not the hint. `level` is 0, 1 (the hint) or 2 (offer show me). `altBlocked`: the next key is an alt chord that never arrived, so something outside tuios may take it. `noWindow`: the step needs a window and there is none. `nudge` is the coach's reason to speak, `""` for none: `idle`, `wrongKeys`, `wrongModeWindow`, `wrongModeTerminal`, `showMe`, `altBlocked` or `noWindow` |
| `lesson.press` | `{chord, down}` | show me holds or lets go of a key, for a keycap to light |
| `lesson.done` | `{index, id, result, results, finished, lastAction, learned, explainer, event?}` | the step is done. `result` is `"clean"`, `"hinted"` or `"skipped"`. `event` is `{type, data}` of the event that finished it, which the coach reacts to |
| `lesson.finish` | `{track, results, seconds}` | the last step is done |

## State

`tuios.state()` and the `state` field of an event:

```js
{
  mode: "window",            // or "terminal"
  workspace: 1,
  workspacesUsed: [1, 2],    // workspaces with a window on them
  tiling: true,
  layout: "bsp",             // "bsp", "master-stack" or "scrolling"
  prefix: "",                // as in the prefix event
  overlays: ["help"],        // the open overlays, sorted
  theme: "catppuccin_mocha",
  focused: "<window id>",    // "" when nothing has focus
  focusedTitle: "~",
  zoomed: false,             // the focused window
  windows: 2,                // on the current workspace, minimized included
  minimized: 0,              // on the current workspace
  totalWindows: 3,           // on every workspace
  windowList: [              // every window, in tuios's order
    { id, title, workspace, x, y, width, height, minimized, zoomed, floating,
      agent, agentMessage, agentKind, harness }
  ],                         // x, y, width, height are in cells
    { id, title, workspace, x, y, width, height, minimized, zoomed, floating, agent, agentMessage, kind, popup }
  ],                         // x, y, width, height are in cells. kind is "tml"
                             // for a TML surface, else "". popup: it floats as a popup
  tape: false,               // a tape is playing
  glyphs: "default",         // the glyph set
  borderStyle: "rounded",
  keys: { preset: "tuios", leader: "ctrl+b", lock: false },
  pip: "",                   // the window picture-in-picture shows
  agentsView: { selected: "<window id>", tab: "thread" }, // "" for both while the agents view is closed
  files: { dir: "/home/guest/projects/hello", cursor: "" },  // as in the files event
  review: { file: "greet.go", hunk: 0 },  // null while the review is closed
  cols: 120, rows: 36
}
```

## Commands

`tuios.command(name, ...args)` runs on the program's own loop, so it never
races a key. An unknown command or a bad argument does nothing.

| Command | Arguments | What it does |
|---|---|---|
| `notify` | `text`, `level?` | show a message in the dock |
| `newWindow` | `title?`, `program?`, `args...` | open a window. With a program (`"top"`, `"claude"`), the pane runs it instead of the shell |
| `closeWindow` | `id?` | close a window, the focused one by default |
| `tiling` | `"on"`, `"off"` or `"toggle"` | turn tiling on or off |
| `layout` | `"bsp"`, `"master-stack"` or `"scrolling"` | set the layout mode |
| `cascade` | | tiling off, windows spread in an overlapping diagonal. Use it before a tiling step, so turning tiling on visibly changes the screen |
| `workspace` | `n` | switch to workspace n |
| `mode` | `"window"` or `"terminal"` | set the mode |
| `theme` | `id` | set the theme |
| `themeJSON` | `id`, `json` | load a theme file's JSON and set it, for a look that carries its own theme. The theme gets the id `look-<id>` in this tab only. tuios checks it as `tuios look apply` does. A theme that fails the check, such as one with a bad id, a control character or a missing base, does nothing. A `theme` event follows |
| `set` | `path`, `value` | set an option by its `tuios set-config` path, such as `set("appearance.glyphs", "ascii")`. It changes this tab's config and nothing else. It takes the look and input options only, and every key `tuios look keys` lists. A list, such as `dock.left`, is a JSON array: `set("dock.left", '["mode","workspaces"]')`. A dock list or `appearance.sidebar.sections` with an item a look leaves out, such as `custom/clock`, does nothing. `startup.tiled` and `startup.layout` change the tiling of the tab at once. An option that runs a program, names a file or writes the clipboard does nothing. A `setting` event follows for the glyph set, the border style and the looks listed under the events, and a `theme` event for the theme |
| `type` | `text` | type into the focused pane, as if typed |
| `action` | `name` | run a keybinding action by name, as its key would. The page then sees the same `action` event |
| `agent` | `state`, `message?`, `kind?` | set the focused pane's agent state, without waiting for the fake agent |
| `tape` | `script`, `name?` | play a tape script |
| `celebrate` | `"big"?`, `"still"?`, `"x,y"?` or a window id | confetti drawn by tuios in terminal cells. `still` is a static sparkle for reduced motion |
| `reset` | | no windows, workspace 1, tiling on in bsp, window mode, the starting theme, the shipped keys, the rail closed, no overlay, beam or picture-in-picture. The fake files and repository stay as they are |

## Learn mode

The browser build runs in Learn mode (`app.OSOptions.LearnMode`), with the
shipped defaults: the screen a person sees after installing tuios. The dock
and the window titles are at the top, and the rail is on the right. A lesson
starts with `reset`, which closes the rail.

- Nothing quits. The quit keys, the quit menu, Ctrl+C in window mode and any
  other quit show a note in the dock instead, and every pane stays open.
- The keybind preset picker (`U` in window mode) changes the keys of the tab
  at once. Nothing is written, and `reset` brings back the shipped keys.
- The rail's files section lists the fake shell's files and follows `cd` in
  the focused pane. Its tree, `ctrl+b f` (file search) and walking into
  folders work. `enter` on a file opens it in the `less` viewer in a new
  pane, and `shift+enter` in the `vim` viewer. On a real machine `enter`
  copies the path and `shift+enter` opens the editor, and the dock says so.
- `ctrl+b v` reviews the focused pane's change in the fake repository:
  files, hunks and notes work as on a real machine. `S` marks the notes as
  sent, and the dock says that a real machine sends them to the pane's agent.
- `ctrl+b U` in a pane under `~/projects/hello` makes a worktree on a new
  branch under `~/.local/share/tuios/worktrees/hello/`, and opens a pane in
  it. A real machine makes a session there instead. The dock names it.
- An action that needs a daemon, a real process, the network or the host's
  files shows a note instead of an error. `tuios.unavailable()` lists them.
  Sessions (switcher, detach, new session), screenshots, tape recording and
  the tape manager, the rail's file changes (new, rename, delete, copy,
  paste), agent mail and host clipboard paste are the ones turned off.
- The agents view (`ctrl+b i`) works with the pretend agents, with no daemon.
  `internal/learn/fleet.go` answers the verbs the view calls from the agents'
  own records: the thread with its edits, checkpoints, the peek, the answer
  keys, a reply, and the New agent sheet, which starts agents in new panes.
  More than one attempt starts a fan: its attempts are grouped under one row,
  and `c` compares them. The judge (`j`) picks the attempt that changed the
  most lines, and a keep (`k` twice) closes the panes of the others. claude's
  approvals are held for the view, as with the tuios hooks installed, so
  `a`, `A`, `r` and `R` answer them from the view. A question is answered
  from the peek (`space`, then its number).
  the tape manager, the rail's file actions, agent mail and host clipboard
  paste are the ones turned off.
- The go-to picker (`Ctrl+B G`) works. It lists the tab's one session,
  `local`, with its workspaces, its panes and any agent.

## The fake shell

Every command it runs is drawn green as it is typed, and nothing drawn green
reports "command not found". `help` lists what is there. Worth knowing for a
lesson:

- `~/projects/hello` is a tiny Go project and a git repository with four
  commits and a change in the working tree. `git status`, `git log`,
  `git diff`, `git add`, `git commit -m`, `git restore`, `git branch` and
  `git worktree list` work, and so do `go run .` and `go test` (which fails
  until the change is restored). Every worktree made with `ctrl+b U` is a
  checkout of its own, on its own branch.
- `tuios ship` lists the steps from the pane's worktree to a merged change.
  `tuios ship commit -m MSG` and `tuios ship merge` work on the fake
  repository. The merge refuses a main checkout with uncommitted changes, as
  on a real machine, and the demo repository has one. `tuios ship push` and
  `tuios ship pr` show what they would send, and what a real machine prints
  after you say yes. `tuios ship status` shows the pull request `pr` opened.
- Every pane announces its folder with OSC 7 when it starts, where a real
  tuios reads the folder of the pane's process.
- `less`, `more`, `view` and `vim` open a read-only viewer: `j`, `k`, space,
  `g`, `G`, `/` to search, `q` or `:q` to quit.
- `claude`, `codex` and `gemini` are scripted agents in the style of Claude
  Code, Codex and Gemini CLI. Each one names its pane after its task and
  reports `working`, `needs_input` and `done` through the same in-process
  path a local pane's set-agent-state takes, so the rail, the title glyph,
  the `agent` event and the agents view all follow it. With no task, each
  one runs its own script:
  - `claude` (harness `claude-code`) reads two files, thinks for about three
    seconds, then asks to edit `style.css` with three options (kind
    `approval`). `y`, `1`, `2` or Enter approve, `n`, `3` or Esc turn it
    down, arrows move the choice. Its screen matches the claude-code harness
    rules.
  - `gemini` (harness `gemini-cli`) reads `style.css`, then asks which
    colors the dark theme uses (kind `question`, options 1 to 3), and writes
    `theme.css`.
  - `codex` (harness `codex`) reads the Go project, adds a test and runs
    `go vet`, and is done in about four seconds. It asks nothing.

  With a task of the person's own (`claude add a footer`), the agent writes
  a short plan for it to a notes file and is done. At the end of a turn the
  agent waits at its input line: the next line typed there is a new turn,
  and ctrl+d or `/exit` goes back to the shell.
- `tuios tape play demo.tape` plays a tape that opens two windows and runs
  commands in them. `tuios tape list` lists the tapes.
- `tuios theme share NAME` prints the real share line of a shipped theme.
- Other `tuios` commands that need a daemon or the network print what they
  print on a real machine, under a line that says so: `ls`, `attach`, `new`,
  `move-window`, `note set`, `note get`, `restart-server --dry-run`, `hosts`,
  `task add`, `task ls`, `task claim`, `look list`, `list-agents`,
  `send-agent-message`, `fan`, `worktree`, `list-verbs` and `list-hooks`.
- `cat .config/tuios/config.toml` shows the theme, the border style, the glyph
  set and every look above that changed in the tab.
- `tuios tml show FILE` opens a TML surface in tuios, as a popup or with
  `--pane` as a pane, with no daemon: the tab holds it
  (`internal/learn/surfaces.go`). As on a real machine, a surface opened
  from a pane leaves the keys in that pane. A click, or `ctrl+b` and the
  window's number, moves them to it. `--wait` blocks until it closes and prints the answer as JSON. Its
  exit status is 0 for an answer and 1 for esc. `tuios tml render FILE`,
  `tuios tml validate FILE` and `tml --check FILE` draw and check a document
  with the real libraries. `~/deploy.tml` is a form and `~/status.tml` a
  dashboard with a table, a chart and a diagram.
- `tuios mermaid FILE` and `sketch FILE` draw a Mermaid diagram as text at
  the pane's width (`~/flow.mmd`). `tplot FILE --kind bar` draws a chart
  from CSV (`~/sales.csv`) in the theme's colours. Each draws again when the
  pane changes width.
- `top`, `rain`, `fastfetch`, `tree`, `fortune`, `cowsay` and `colors` are
  there for fun. `neofetch` is another name for `fastfetch`. The launcher (Alt+Space) lists the full-screen ones.
