# Bash completion for tuios

_tuios_sessions() {
    tuios session list 2>/dev/null
}

_tuios_pty_ids() {
    tuios pty list 2>/dev/null | grep -oE '^[0-9]+'
}

_tuios() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "${COMP_WORDS[1]}" in
        session)
            case "${COMP_WORDS[2]}" in
                attach|delete)
                    COMPREPLY=($(compgen -W "$(_tuios_sessions)" -- "$cur"))
                    return
                    ;;
                rename)
                    COMPREPLY=($(compgen -W "$(_tuios_sessions)" -- "$cur"))
                    return
                    ;;
                list)
                    return
                    ;;
                *)
                    COMPREPLY=($(compgen -W "attach list rename delete --help -h" -- "$cur"))
                    return
                    ;;
            esac
            ;;
        pty)
            case "${COMP_WORDS[2]}" in
                kill)
                    COMPREPLY=($(compgen -W "$(_tuios_pty_ids)" -- "$cur"))
                    return
                    ;;
                list)
                    return
                    ;;
                *)
                    COMPREPLY=($(compgen -W "list kill --help -h" -- "$cur"))
                    return
                    ;;
            esac
            ;;
        serve)
            return
            ;;
        *)
            COMPREPLY=($(compgen -W "serve session pty --help -h --version -v" -- "$cur"))
            return
            ;;
    esac
}

complete -F _tuios tuios
