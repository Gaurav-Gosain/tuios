# tuios shell integration for zsh: the prompt marks.
#
# The marks are OSC 133: A where the prompt starts, B where the input starts,
# C when a command runs and D with its exit status when it finishes. They
# print nothing on the screen. The setup waits for the first prompt, after
# .zshrc, so it can see which marks the user's own setup already sends and
# leave those to it.

[[ -o interactive ]] || 'builtin' 'return' 0
(( ${+_tuios_marks} )) && 'builtin' 'return' 0
'builtin' 'typeset' -g _tuios_marks=

_tuios_init() {
    'builtin' 'emulate' -L zsh -o no_aliases
    # The hooks after this one in this first round, such as a prompt theme's.
    'builtin' 'local' -i after=$(( ${#precmd_functions} - ${precmd_functions[(I)_tuios_init]} ))
    precmd_functions=(${precmd_functions:#_tuios_init})
    'builtin' 'zmodload' zsh/parameter 2>/dev/null
    'builtin' 'local' f m text="$PS1"
    for f in $precmd_functions $preexec_functions precmd preexec; do
        [[ $f == _tuios_(init|precmd|preexec) ]] && continue
        text+=$'\n'"${functions[$f]-}"
    done
    for m in A B C D; do
        [[ $text == *"133;$m"* ]] || _tuios_marks+=$m
    done
    [[ -n $_tuios_marks ]] || 'builtin' 'return' 0
    [[ $_tuios_marks == *C* ]] && preexec_functions+=(_tuios_preexec)
    precmd_functions+=(_tuios_precmd)
    # This prompt's hooks were fixed before _tuios_precmd joined them, so it
    # runs now.
    _tuios_precmd
    # A hook after this one may set PS1 again for this first prompt and drop
    # the B. The line editor then sends it, once, where the input starts.
    if [[ $_tuios_marks == *B* ]] && (( after > 0 )) && 'builtin' 'zmodload' zsh/zle 2>/dev/null; then
        'builtin' 'autoload' -Uz add-zle-hook-widget
        'builtin' 'typeset' -gi _tuios_first_line=1
        add-zle-hook-widget line-init _tuios_line_init 2>/dev/null
    fi
}

_tuios_line_init() {
    (( _tuios_first_line )) || 'builtin' 'return' 0
    _tuios_first_line=0
    [[ $PS1 == *'133;B'* ]] || 'builtin' 'print' -rn -- $'\e]133;B\a'
}

# _tuios_precmd sends D with the status of the command that finished and A
# where the prompt starts, and puts B at the end of PS1 when a prompt theme
# has set it again. It keeps itself the last hook, so it runs after a theme
# that sets PS1 in its own precmd.
_tuios_precmd() {
    'builtin' 'local' st=$? pct=0
    [[ -o prompt_percent ]] && pct=1
    'builtin' 'emulate' -L zsh -o no_aliases
    [[ $_tuios_marks == *D* ]] && 'builtin' 'print' -rn -- $'\e]133;D;'"$st"$'\a'
    [[ $_tuios_marks == *A* ]] && 'builtin' 'print' -rn -- $'\e]133;A\a'
    if [[ $_tuios_marks == *B* && $pct == 1 && $PS1 != *'133;B'* ]]; then
        PS1+=$'%{\e]133;B\a%}'
    fi
    if [[ ${precmd_functions[-1]-} != _tuios_precmd ]]; then
        precmd_functions=(${precmd_functions:#_tuios_precmd} _tuios_precmd)
    fi
    'builtin' 'return' 0
}

_tuios_preexec() {
    'builtin' 'print' -rn -- $'\e]133;C\a'
}

precmd_functions+=(_tuios_init)
