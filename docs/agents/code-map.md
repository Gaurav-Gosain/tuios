# Code organization

The tree of packages and what each one holds.

```
tuios/
├── cmd/tuios/              # CLI entry point (main.go with cobra commands)
├── cmd/tuios-web/          # Web terminal server binary (separate for security)
├── cmd/tuios-wasm/         # Browser build for the Learn tuios tour (js/wasm)
├── cmd/sketch-wasm/        # tuios.dev/sketch for the browser: the Mermaid playground at tuios.dev/sketch
├── cmd/tuios-fuzz/         # The property fuzzer, drawn while it runs; kept out of the shipped binary
├── internal/
│   ├── app/                # Core window manager, OS model, rendering
│   │   ├── os.go           # Central state (OS struct), window lifecycle
│   │   ├── render.go       # View generation, layer composition
│   │   ├── update.go       # Bubble Tea Update() handler
│   │   ├── stylecache.go   # LRU style caching (40-60% allocation reduction)
│   │   ├── workspace.go    # Multi-workspace support (1-9)
│   │   └── animations.go   # Visual transitions
│   ├── config/             # Configuration and keybindings
│   │   ├── userconfig.go   # TOML config loading, defaults
│   │   ├── registry.go     # Keybind action lookup
│   │   └── validation.go   # Config validation
│   ├── input/              # Input handling and modal routing
│   │   ├── handler.go      # Main input coordinator
│   │   ├── keyboard.go     # Key event dispatch
│   │   ├── mouse.go        # Mouse interactions
│   │   ├── actions.go      # Action handlers (the prefix handlers are in prefix_actions.go)
│   │   └── copymode_*.go   # Vim-style copy mode (50+ motions)
│   ├── terminal/           # Terminal window management
│   │   ├── window.go       # Window struct, PTY lifecycle
│   │   └── window_unix.go / window_nounix.go  # Platform-specific window and PTY glue
│   ├── ptyspawn/           # The one path every PTY-backed process is spawned through (spawn_unix.go, spawn_windows.go)
│   ├── session/            # The daemon: sessions, PTYs (session.go), wire protocol, JSON verbs
│   ├── childproc/          # Starts the daemon's helper programs (git, ssh, gh, hooks) with no console window
│   ├── serverscope/        # Names the daemon a process belongs to, for named servers
│   ├── sockpath/           # The daemon's socket folder and the longest socket path
│   ├── procinfo/           # What the kernel says about a process: socket peer, start time, parent
│   ├── restartplan/        # What a daemon restart stops and brings back (restart-server --json)
│   ├── guibridge/          # A client with no screen that streams layout and pane bytes to the GPU client (tuios gui-bridge)
│   ├── transcriptview/     # Decodes an agent's transcript into a conversation, for agent-transcript only
│   ├── federation/         # The link layer between this daemon and the daemons on other machines
│   ├── worktree/           # Git worktrees: detect, create, and remove without losing uncommitted work
│   ├── gitstate/           # Branch and upstream drift for the sidebar
│   ├── dirwatch/           # Tells the rail's files section when the listed folder's entries change
│   ├── review/             # Reading a worktree's diff, and the notes left on it
│   ├── diffview/           # Drawing diff lines: chroma highlighting, split layout, changed words
│   ├── markdown/           # Reads the Markdown agents write into blocks and styled runs; draws nothing
│   ├── mermaid/            # Finds Mermaid diagrams, themes them, draws them as kitty graphics or text through sketch
│   ├── tmldraw/            # Draws a TML document into text the way an attached client draws a surface, with loom
│   ├── ghpr/               # Reads and opens GitHub pull requests through the person's own gh
│   ├── capture/            # Turns a screenshot request and config into what shot renders
│   ├── lookshare/          # Builds what "Share my look" hands the person (tuios look share)
│   ├── release/            # Finds published releases and verifies a downloaded binary (tuios update)
│   ├── netutil/            # Small network helpers the servers share
│   ├── memtrim/            # Gives the heap a burst left behind back to the OS once the process settles
│   ├── harness/            # tuios policy over tuios.dev/harness: prompt answers, resume, launch flags, permission ladder, title label
│   ├── herdrplugin/        # herdr plugins: manifest, discovery, the command runner (the host is internal/session/plugin_host.go)
│   ├── herdrcli/           # herdr's command line, answered by tuios
│   ├── integration/        # Wires harness hooks, plugins and MCP entries (tuios integration)
│   ├── mcp/                # The MCP server behind tuios mcp
│   ├── risk/               # Marks an approval risky by the shipped and configured rules
│   ├── pushnotify/         # Sends Inbox items to a phone through ntfy, Pushover or a webhook
│   ├── agentproto/         # Headless agents over ACP (through tuios.dev/acp) and the Codex app-server: the pane program of start-agent --protocol
│   ├── learn/              # Learn tuios: tour model, event contract, page commands, lessons/ (the tracks as data and the engine)
│   ├── webshell/           # In-memory pty and fake shell for the browser build
│   ├── tutor/              # tuios tutor: Learn mode in this terminal, in a sandbox, with the coach card
│   ├── hooks/              # Shell hooks on window/session/agent events
│   ├── tmuxcompat/         # The opt-in tmux shim (tuios tmux-shim) and its pane holder; see docs/TMUX_SHIM.md
│   ├── shimlink/           # The tmux and herdr links that run tuios as another program
│   ├── stopevent/          # The Windows event tuios kill-server sets to stop a daemon
│   ├── scrollback/         # OSC 133 scrollback browser
│   ├── hints/              # Finds the things on screen a person may copy and labels them, for hints mode
│   ├── pastebuf/           # Paste buffers of recent yanks, newest first, after tmux's
│   ├── overlay/            # Panel and dialog primitives for chrome
│   ├── composer/           # The editor a person writes to an agent in: multiline, undo, paste blocks
│   ├── textedit/           # The editing rules every text field shares
│   ├── explore/            # The full-screen browser behind keybinds browse, config browse and help -i
│   ├── sessiontree/        # Sidebar session tree model
│   ├── tape/               # Tape scripting automation
│   │   ├── lexer.go        # Tokenizer
│   │   ├── parser.go       # AST generation
│   │   ├── executor.go     # Command execution
│   │   └── player.go       # Playback engine
│   ├── server/             # SSH server (Wish v2)
│   ├── served/             # The model a server (SSH, web) hands to one remote client
│   ├── theme/              # Color theming
│   ├── layout/             # Window tiling algorithms
│   ├── pool/               # Memory pooling
│   ├── refreshrate/        # Finds the displays' refresh rate, for max_fps = "auto"
│   ├── crashexit/          # Puts the terminal back and exits when a goroutine tuios started panics
│   ├── invisible/          # Finds characters that draw nothing but change how text reads
│   ├── tarpack/            # Reads the gzipped archives a package embeds (see generated-files.md)
│   ├── lint/               # Source checks that run as tests (see source-checks.md)
│   └── ui/                 # Animation system
│                           # (plus cliflags, debuglog, fang, listnav, sound,
│                           #  transcript, guestenv, perf, fuzz, testutil,
│                           #  atomicfile, lazyre, linediff, oneline, pathroot,
│                           #  plural, sortx, units, winacl, winjob, winpath)
├── acp/                    # tuios.dev/acp, a Go module of its own: the Agent Client Protocol client, versions 1 and 2; the JSON-RPC peer, negotiation, typed updates
├── harness/                # tuios.dev/harness, a Go module of its own: agent CLI detection; the manifest format, 26 bundled manifests, Identify, Classify and Explain
├── loom/                   # tuios.dev/loom, a Go module of its own: draws TML into cells; flex layout, retained tree, palettes per depth, keys and mouse
├── nitpick/                # tuios.dev/nitpick, a Go module of its own: go/analysis hygiene rules with a measured precision; cmd/nitpick checks changed lines
├── plot/                   # tuios.dev/plot, a Go module of its own: eleven chart kinds (bar, line, area, heatmap, pie, world map and more) as cells with theme roles
├── progstatus/             # tuios.dev/progstatus, a Go module of its own: OSC 7501 parser, record store, encoder
├── shot/                   # tuios.dev/shot, a Go module of its own: a cell grid to PNG, SVG, HTML, ANSI or text; mosaic turns pictures into cells
├── sketch/                 # tuios.dev/sketch, a Go module of its own: Mermaid diagrams to SVG in pure Go, a port of pele; raster/ and text/ outputs
├── swatch/                 # tuios.dev/swatch, a Go module of its own: desktop theme palettes (Omarchy), followed live
├── tml/                    # tuios.dev/tml, a Go module of its own: the TML markup for surfaces (codecs, validator, expressions, JSON Patch); draws nothing
├── vt/                     # tuios.dev/vt, a Go module of its own: the terminal emulator, pure Go, plus libghostty-vt behind -tags ghostty
│   ├── emulator.go         # The pure Go Emulator and its write path
│   ├── parser.go           # Parser state machine
│   ├── screen.go           # Screen buffer management
│   ├── scrollback.go       # History ring (10,000 lines unless configured)
│   ├── dnd/                # The kitty drag and drop protocol (OSC 72) on the wire: parse, join chunks, write
│   ├── snapshot/           # Capture, pack, apply and replay a pane's state (the daemon's wire snapshot)
│   └── vtgen/              # Grammar-based terminal input generator for the fuzzers
├── pkg/                    # Embeddable facade (tuios), applist, fuzzy
├── docs/                   # Documentation
│   ├── ARCHITECTURE.md     # Technical architecture diagrams
│   ├── KEYBINDINGS.md      # Complete keybinding reference
│   ├── CONFIGURATION.md    # Config options
│   └── CLI_REFERENCE.md    # CLI flags and commands
├── examples/               # Tape script examples, and dock components under examples/dock/
├── plugins/                # First-party plugins; the binary embeds only template/, which tuios plugins new copies
├── scripts/                # check.sh, ci-changes.sh, library-pins.sh, binary-size.sh, rebase.sh, install.sh
├── skills/                 # The tuios skill (skills/tuios/SKILL.md and its topics), embedded and printed by tuios --skill [topic]
├── integrations/           # Harness integrations, such as the claude-code agent-state shim
├── e2e/                    # End-to-end tests; e2e/tui is its own Go module
├── clienttests/            # Playwright tests for the web client
└── nix/                    # Nix packaging
```
