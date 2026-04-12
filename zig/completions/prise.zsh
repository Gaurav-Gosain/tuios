#compdef tuios

_tuios_sessions() {
    local sessions
    sessions=(${(f)"$(tuios session list 2>/dev/null)"})
    _describe 'session' sessions
}

_tuios_pty_ids() {
    local ids
    ids=(${(f)"$(tuios pty list 2>/dev/null | grep -oE '^[0-9]+')"})
    _describe 'pty id' ids
}

_tuios() {
    local -a commands
    local curcontext="$curcontext" state line

    _arguments -C \
        '(-h --help)'{-h,--help}'[Show help message]' \
        '(-v --version)'{-v,--version}'[Show version]' \
        '1: :->command' \
        '*:: :->args'

    case $state in
        command)
            commands=(
                'serve:Start the server in the foreground'
                'session:Manage sessions'
                'pty:Manage PTYs'
            )
            _describe 'command' commands
            ;;
        args)
            case $words[1] in
                session)
                    if (( CURRENT == 2 )); then
                        local -a session_commands
                        session_commands=(
                            'attach:Attach to a session'
                            'list:List all sessions'
                            'rename:Rename a session'
                            'delete:Delete a session'
                        )
                        _describe 'session command' session_commands
                    else
                        case $words[2] in
                            attach|delete|rename)
                                _tuios_sessions
                                ;;
                        esac
                    fi
                    ;;
                pty)
                    if (( CURRENT == 2 )); then
                        local -a pty_commands
                        pty_commands=(
                            'list:List all PTYs'
                            'kill:Kill a PTY by ID'
                        )
                        _describe 'pty command' pty_commands
                    else
                        case $words[2] in
                            kill)
                                _tuios_pty_ids
                                ;;
                        esac
                    fi
                    ;;
            esac
            ;;
    esac
}

_tuios "$@"
