# tuios shell integration for bash.
#
# tuios starts an interactive bash with --rcfile set to this file, which bash
# then reads in place of ~/.bashrc. The file reads ~/.bashrc, as bash would
# have (or the login files, below), and then makes bash mark its prompts and commands with OSC 133: A
# where the prompt starts, B where the input starts, C when a command runs and
# D with its exit status when it finishes. It prints nothing but those marks,
# and it sends no mark that the user's own setup already sends.
#
# Turn it off with [shell] integration = "off" in the tuios config.

# With [shell] login = true, tuios sets TUIOS_BASH_LOGIN: read the files a
# login bash reads, in its order, in place of ~/.bashrc, as bash -l would. A
# login bash reads no rc file, so --rcfile is how the marks stay on. Most
# ~/.bash_profile files read ~/.bashrc themselves.
if [[ -n ${TUIOS_BASH_LOGIN-} ]]; then
    builtin unset TUIOS_BASH_LOGIN
    if [[ -r /etc/profile ]]; then
        builtin source /etc/profile
    fi
    if [[ -r ~/.bash_profile ]]; then
        builtin source ~/.bash_profile
    elif [[ -r ~/.bash_login ]]; then
        builtin source ~/.bash_login
    elif [[ -r ~/.profile ]]; then
        builtin source ~/.profile
    fi
elif [[ -r ~/.bashrc ]]; then
    builtin source ~/.bashrc
fi

# PS0, which sends C, and the ${var@a} below need bash 4.4. An older bash
# would mark its prompts and never its commands, which is worse than no marks:
# the output of every command would sit inside an open prompt.
if [[ $- == *i* && -z ${__tuios_marks+x} ]] &&
    ((BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4))); then
    __tuios_init() {
        # The options go back as they were when the function returns, so a
        # user who runs with set -u or set -e keeps it.
        builtin local -
        builtin set +eu -f
        # What the user's setup sends before the prompt: the prompt strings,
        # PROMPT_COMMAND, and the functions it and the bash-preexec precmd
        # hooks call. Reading the functions takes a subshell, so it runs only
        # when there are some. The preexec hooks are left out: bash-preexec
        # runs them from a DEBUG trap that another setup can displace, and a
        # hook whose body names the C mark may never fire. WezTerm's
        # /etc/profile.d script is one: a login bash read its A, B and D, and
        # no C, and run refused the pane. C is read from PS0 alone, below.
        builtin local text="${PS1-}"$'\n'"${PS0-}"$'\n'"${PROMPT_COMMAND[*]-}"
        local -a names=()
        builtin local word
        for word in ${PROMPT_COMMAND[*]//[;&|()]/ } ${precmd_functions[*]-}; do
            if builtin declare -F -- "$word" >/dev/null 2>&1; then
                names+=("$word")
            fi
        done
        if ((${#names[@]})); then
            text+=$'\n'"$(builtin declare -f -- "${names[@]}")"
        fi
        __tuios_marks=
        builtin local mark
        for mark in A B C D; do
            if [[ $mark == C ]]; then
                # Only PS0 is known to send C. A hook that sends it too sends
                # a second C at the same place, which the daemon takes as the
                # same command.
                if [[ ${PS0-} != *'133;C'* ]]; then
                    __tuios_marks+=$mark
                fi
            elif [[ $text != *"133;$mark"* ]]; then
                __tuios_marks+=$mark
            fi
        done
        if [[ -z $__tuios_marks || ${PROMPT_COMMAND@a} == *r* ]]; then
            __tuios_marks=
            return 0
        fi
        if [[ $__tuios_marks == *C* ]]; then
            # bash prints PS0 after it reads a command line and before the
            # command runs, and prints nothing for an empty line.
            PS0+='\e]133;C\a'
        fi
        if ((BASH_VERSINFO[0] > 5 || (BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 1))); then
            # bash 5.1 runs each element of a PROMPT_COMMAND array with the
            # command's own status in $?, so the mark goes last and still
            # reads it, after anything that sets PS1.
            if [[ ${PROMPT_COMMAND@a} == *a* ]]; then
                PROMPT_COMMAND+=(__tuios_precmd)
            elif [[ -n ${PROMPT_COMMAND-} ]]; then
                PROMPT_COMMAND=("$PROMPT_COMMAND" __tuios_precmd)
            else
                PROMPT_COMMAND=(__tuios_precmd)
            fi
        else
            # Older bash runs one PROMPT_COMMAND string. The status is saved
            # first and given back to the user's commands.
            if [[ -n ${PROMPT_COMMAND-} ]]; then
                PROMPT_COMMAND=$'__tuios_status=$?\n__tuios_restore\n'"$PROMPT_COMMAND"$'\n__tuios_precmd'
            else
                PROMPT_COMMAND=$'__tuios_status=$?\n__tuios_precmd'
            fi
        fi
        return 0
    }

    # __tuios_restore gives $? back to the user's PROMPT_COMMAND on bash
    # before 5.1. Under set -e a failed status would end the shell, so it
    # gives back 0 there.
    __tuios_restore() {
        if [[ $- != *e* ]]; then
            return "${__tuios_status:-0}"
        fi
        return 0
    }

    # __tuios_precmd runs last before each prompt. It sends D with the status
    # of the command that finished and A where the prompt starts, and puts B
    # and C back when a prompt theme has set PS1 or PS0 again.
    #
    # A carries redraw=last (the kitty extension): on SIGWINCH bash repaints
    # only the last line of its prompt. Without it a terminal that clears a
    # marked prompt on resize, as libghostty-vt does, clears every prompt row
    # and bash repaints one of them, so a narrowed pane shows the prompt twice.
    __tuios_precmd() {
        builtin local status=$? marks=${__tuios_marks-}
        if [[ -n ${__tuios_status-} ]]; then
            status=$__tuios_status
        fi
        if [[ $marks == *D* ]]; then
            builtin printf '\e]133;D;%s\a' "$status"
        fi
        if [[ $marks == *A* ]]; then
            builtin printf '\e]133;A;redraw=last\a'
        fi
        if [[ $marks == *B* && ${PS1-} != *'133;B'* ]]; then
            PS1+='\[\e]133;B\a\]'
        fi
        if [[ $marks == *C* && ${PS0-} != *'133;C'* ]]; then
            PS0+='\e]133;C\a'
        fi
        return 0
    }

    __tuios_init
    builtin unset -f __tuios_init
fi
