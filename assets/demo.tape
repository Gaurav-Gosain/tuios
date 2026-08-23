# The README's demo recording. Render it with:
#
#   assets/record-demo.sh
#
# Not with `vhs demo.tape` directly: the script builds the scratch XDG root and
# the config directory this tape assumes, and it has to do that before vhs
# starts, because vhs applies Env from the first frame and its own shell would
# otherwise create the config directory itself. See record-demo.sh.
#
# Three things this file has to be careful about, all learned by watching a take
# go wrong:
#
#   Mode. A pane only receives text in terminal mode, and prefix actions such as
#   rename hand the keyboard back to window management when they finish. Text
#   typed a moment too early is not text, it is keybindings: a stray "tuios "
#   once read as t-u-i-o-s and toggled tiling off mid-recording. Every block of
#   shell input below is preceded by Enter, which enters terminal mode from
#   window management and costs nothing but a blank prompt line if the session
#   was already in it.
#
#   Order. Panes are split while they are still empty, and the top pane keeps
#   the full width of the screen. Splitting a pane that already holds output
#   rewraps it, and a half-drawn logo in the corner of the frame reads as a bug
#   in tuios rather than as a terminal doing what terminals do. Losing height
#   only scrolls, which is why the first split is the horizontal one.
#
#   Reach. Two of tuios's bindings cannot be spelled in a tape file, because vhs
#   has no Alt+Space or Alt+Escape. The launcher is opened from its command
#   palette row instead, which prints the alt+space shortcut on screen anyway.
#
Output "demo.gif"

Require tuios

Set Shell "fish"
Set FontFamily "JetBrainsMono Nerd Font Mono"
Set FontSize 16
Set Width 1280
Set Height 860
Set Padding 10
Set Margin 16
Set MarginFill "#674EFF"
Set BorderRadius 8
Set WindowBar Colorful
Set Theme "TokyoNight"
Set Framerate 22
Set TypingSpeed 35ms

Env XDG_CONFIG_HOME "/tmp/tuios-demo/config"
Env XDG_RUNTIME_DIR "/tmp/tuios-demo/run"
Env XDG_STATE_HOME "/tmp/tuios-demo/state"
Env XDG_DATA_HOME "/tmp/tuios-demo/data"
Env XDG_CACHE_HOME "/tmp/tuios-demo/cache"
# The panes take their shell from here, not from Set Shell above, which only
# decides what vhs itself starts.
Env SHELL "/usr/bin/fish"
# Note for anyone re-recording this: the launcher draws an icon beside each
# application on a terminal that speaks the kitty graphics protocol, and vhs
# does not. It renders a picture the guest sends straight through, which is why
# the fastfetch logo below arrives intact, but not the placements tuios makes
# for itself, so the launcher here is the text-only form. Forcing the capability
# with TUIOS_KITTY_GRAPHICS=1 does not help: it reserves the icon column and
# then leaves it empty, which looks worse than not offering it. Record on a real
# ghostty or kitty if the icons are the point.
# The greeting is four lines of nothing in a pane this size.
Env fish_greeting ""
# direnv narrates every directory change into the top of every pane.
Env DIRENV_LOG_FORMAT ""

# The working directory, off camera. Everything else is prepared by
# record-demo.sh before vhs starts.
Hide
Type "cd /tmp/tuios-demo/work; clear"
Enter
Sleep 1s
Show

Type "tuios"
Enter
Sleep 3s

# Two more panes, split while they are empty so the open animation is the only
# thing moving. Each one grows out of the tile it is about to fill.
#
# Downwards first, then sideways off the new pane, which leaves the top pane the
# full width of the screen for what follows. Splitting it sideways instead would
# halve its width, and a pane that loses width rewraps everything already
# printed in it; losing height only scrolls.
Ctrl+B
Sleep 600ms
Type "-"
Sleep 1600ms
Ctrl+B
Sleep 600ms
Type "|"
Sleep 1600ms

# Fill the full-width pane along the top. The structure is trimmed to what fits
# the half-height pane exactly, so nothing scrolls away while it is on screen.
Ctrl+B
Sleep 500ms
Type "1"
Sleep 800ms
Enter
Sleep 700ms
Type "fastfetch --logo-height 10 -s title:separator:os:kernel:shell:term:cpu:memory:colors"
Enter
Sleep 3s

# Name the third pane, so the rail and the CLI have something to call it.
Ctrl+B
Sleep 500ms
Type "3"
Sleep 800ms
Ctrl+B
Sleep 500ms
Type "r"
Sleep 500ms
Type "claude"
Enter
Sleep 800ms

# A pane reports what it is doing, and the rail picks it up.
Enter
Sleep 700ms
Type "tuios set-agent-state working -w claude -m 'refactoring the tiler'"
Enter
Sleep 2200ms

# The rail itself.
Ctrl+B
Sleep 500ms
Type "e"
Sleep 2s
Type "j"
Sleep 1s
Escape
Sleep 800ms

# The launcher, opened from the palette row that also prints its shortcut. The
# query is a desktop-entry keyword rather than a program name, so the rows are
# real applications with real icons rather than a list of $PATH binaries.
Ctrl+P
Sleep 1s
Type "run a"
Sleep 700ms
Enter
Sleep 1500ms
Type "browser"
Sleep 2s
Escape
Sleep 600ms

# Ricing, live. The picker previews on ↑↓, not on the query, so the arrows are
# what repaint the session; typing alone would reach the last frame with the
# theme still unchanged. Enter commits, which is also what writes it to the
# config copy made above.
#
# The theme is a dark one on purpose. tuios does not paint a background of its
# own, it recolours what the panes draw and its own accents, so a light theme
# against a dark host terminal changes almost nothing on screen: the first take
# of this beat applied catppuccin_latte and looked, on film, like nothing had
# happened at all.
Ctrl+P
Sleep 1s
Type "Theme Picker"
Sleep 800ms
Enter
Sleep 1200ms
Type "gruvbox"
Sleep 900ms
Down
Sleep 1400ms
Down
Sleep 1600ms
Enter
Sleep 3500ms
