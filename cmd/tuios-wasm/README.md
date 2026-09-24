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
```

`build.sh` writes:

| File | What it is |
|---|---|
| `tuios.wasm.br`, `tuios.wasm.gz` | the program, compressed. The raw file is over Cloudflare's 25 MiB limit, so only these ship |
| `wasm_exec.js` | Go's loader, from the toolchain that built the wasm |
| `webterm.js`, `webterm.css`, `xterm.css` | the renderer, sip's xterm.js bundle |
| `fonts/*.woff2` | JetBrains Mono Nerd Font, regular and bold |
| `manifest.json` | the tuios commit, the sizes and the sha256 of every file |
| `index.html` | a demo page with a five-step track, for trying a change |

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

## Events

Every event is `{type, data, windowId?, state?}`. `windowId` is set when the
event is about one pane. `state` is the snapshot after the change, and is set
on every event that comes from a state change: all but `key`, `action`,
`notification` and the `shell.*` events.

Events come in a fixed order for one key press: `key` first, then any
`action`, then the state changes it caused.

| Type | Data | When |
|---|---|---|
| `ready` | `{}` | the program started. Carries `state` |
| `key` | `{key, mode}` | a key press, before tuios handles it. `key` is Bubble Tea's name for it (`"ctrl+b"`, `"n"`, `"enter"`), `mode` is `"window"` or `"terminal"` |
| `action` | `{name, mode}` | the exact keybinding action a key, a prefix chord, a menu row or `command("action")` ran, such as `"prefix_new_window"` or `"toggle_tiling"`. Also sent for an action Learn mode turns off |
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
| `workspace` | `{from, to}` | the current workspace, 1 to 9 |
| `tiling` | `{from, to}` | tiling on (`true`) or off |
| `layout` | `{from, to}` | the layout mode: `"bsp"`, `"master-stack"` or `"scrolling"` |
| `theme` | `{from, to}` | the theme id, such as `"catppuccin_mocha"` |
| `setting` | `{name, from, to}` | a look changed. `name` is `"glyphs"` (the glyph set, `"default"` for the shipped one) or `"borderStyle"` (such as `"rounded"`) |
| `overlay.open` | `{name}` | an overlay opened. Names below |
| `overlay.close` | `{name}` | an overlay closed |
| `notification` | `{message, level}` | a message in the dock. `level` is `"info"`, `"success"`, `"warning"` or `"error"`. Learn mode's notes are `"info"` |
| `agent` | `{id, from, to, message, kind, harness}` | a pane's agent state: `""`, `"working"`, `"needs_input"`, `"idle"`, `"done"`, `"errored"` or `"unknown"`. `kind` is `"approval"` or `"question"` for `needs_input` |
| `tape.start` | `{name?}` | a tape started playing |
| `tape.finish` | `{}` | the tape finished |
| `shell.start` | `{command, line, cwd}` | the fake shell started a command. `command` is the first word |
| `shell.command` | `{command, line, cwd, exitCode}` | the command finished. `exitCode` is a number: 0 for success, 127 for a command the shell does not know |
| `shell.cwd` | `{cwd}` | the shell changed directory |
| `exit` | `{error?}` | the program ended. Learn mode never quits, so this only follows a crash |

Overlay names: `help`, `whichkey`, `commandPalette`, `launcher`, `settings`,
`themePicker`, `keybinds`, `copyMode`, `search` (copy mode or scrollback
search), `scrollback`, `quitMenu`, `workspaceSwitcher`, `layoutPicker`,
`sidebar`, `logs`, `screensaver`.

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
    { id, title, workspace, x, y, width, height, minimized, zoomed, floating, agent, agentMessage }
  ],                         // x, y, width, height are in cells
  tape: false,               // a tape is playing
  glyphs: "default",         // the glyph set
  borderStyle: "rounded",
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
| `type` | `text` | type into the focused pane, as if typed |
| `action` | `name` | run a keybinding action by name, as its key would. The page then sees the same `action` event |
| `agent` | `state`, `message?`, `kind?` | set the focused pane's agent state, without waiting for the fake agent |
| `tape` | `script`, `name?` | play a tape script |
| `celebrate` | `"big"?`, `"still"?`, `"x,y"?` or a window id | confetti drawn by tuios in terminal cells. `still` is a static sparkle for reduced motion |
| `reset` | | no windows, workspace 1, tiling on in bsp, window mode, the starting theme, no overlay |

## Learn mode

The browser build runs in Learn mode (`app.OSOptions.LearnMode`):

- Nothing quits. The quit keys, the quit menu, Ctrl+C in window mode and any
  other quit show a note in the dock instead, and every pane stays open.
- An action that needs a daemon, a real process, the network or the host's
  files shows a note instead of an error. `tuios.unavailable()` lists them.
  Sessions (switcher, detach, new session), screenshots, tape recording and
  the tape manager, the rail's file actions, agent mail and host clipboard
  paste are the ones turned off.

## The fake shell

Every command it runs is drawn green as it is typed, and nothing drawn green
reports "command not found". `help` lists what is there. Worth knowing for a
lesson:

- `~/projects/hello` is a tiny Go project and a git repository with four
  commits and a change in the working tree. `git status`, `git log`,
  `git diff`, `git add`, `git commit -m`, `git restore` work, and so do
  `go run .` and `go test` (which fails until the change is restored).
- `less`, `more`, `view` and `vim` open a read-only viewer: `j`, `k`, space,
  `g`, `G`, `/` to search, `q` or `:q` to quit.
- `claude` is a scripted agent in the style of Claude Code. It reads two
  files, thinks for about three seconds, then asks to edit `style.css` with
  three options. `y`, `1`, `2` or Enter approve, `n`, `3` or Esc turn it down,
  arrows move the choice. It reports `working`, `needs_input` (kind
  `approval`) and `done` through the same in-process path a local pane's
  set-agent-state takes, so the rail, the title glyph and the `agent` event
  all follow it. Its screen also matches the claude-code harness rules.
- `tuios tape play demo.tape` plays a tape that opens two windows and runs
  commands in them. `tuios tape list` lists the tapes.
- `top`, `rain`, `neofetch`, `tree`, `fortune`, `cowsay` and `colors` are
  there for fun. The launcher (Alt+Space) lists the full-screen ones.
