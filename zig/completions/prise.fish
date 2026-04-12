# Fish completion for tuios

# Disable file completions by default
complete -c tuios -f

# Top-level commands
complete -c tuios -n __fish_use_subcommand -a serve -d 'Start the server in the foreground'
complete -c tuios -n __fish_use_subcommand -a session -d 'Manage sessions'
complete -c tuios -n __fish_use_subcommand -a pty -d 'Manage PTYs'
complete -c tuios -n __fish_use_subcommand -s h -l help -d 'Show help message'
complete -c tuios -n __fish_use_subcommand -s v -l version -d 'Show version'

# Session subcommands
complete -c tuios -n '__fish_seen_subcommand_from session; and not __fish_seen_subcommand_from attach list rename delete' -a attach -d 'Attach to a session'
complete -c tuios -n '__fish_seen_subcommand_from session; and not __fish_seen_subcommand_from attach list rename delete' -a list -d 'List all sessions'
complete -c tuios -n '__fish_seen_subcommand_from session; and not __fish_seen_subcommand_from attach list rename delete' -a rename -d 'Rename a session'
complete -c tuios -n '__fish_seen_subcommand_from session; and not __fish_seen_subcommand_from attach list rename delete' -a delete -d 'Delete a session'
complete -c tuios -n '__fish_seen_subcommand_from session; and not __fish_seen_subcommand_from attach list rename delete' -s h -l help -d 'Show session help'

# PTY subcommands
complete -c tuios -n '__fish_seen_subcommand_from pty; and not __fish_seen_subcommand_from list kill' -a list -d 'List all PTYs'
complete -c tuios -n '__fish_seen_subcommand_from pty; and not __fish_seen_subcommand_from list kill' -a kill -d 'Kill a PTY by ID'
complete -c tuios -n '__fish_seen_subcommand_from pty; and not __fish_seen_subcommand_from list kill' -s h -l help -d 'Show PTY help'

# Dynamic session name completion
function __tuios_sessions
    tuios session list 2>/dev/null | string match -r '^\S+'
end

complete -c tuios -n '__fish_seen_subcommand_from session; and __fish_seen_subcommand_from attach' -a '(__tuios_sessions)' -d Session
complete -c tuios -n '__fish_seen_subcommand_from session; and __fish_seen_subcommand_from delete' -a '(__tuios_sessions)' -d Session
complete -c tuios -n '__fish_seen_subcommand_from session; and __fish_seen_subcommand_from rename' -a '(__tuios_sessions)' -d Session

# Dynamic PTY ID completion
function __tuios_pty_ids
    tuios pty list 2>/dev/null | string match -r '^\d+'
end

complete -c tuios -n '__fish_seen_subcommand_from pty; and __fish_seen_subcommand_from kill' -a '(__tuios_pty_ids)' -d 'PTY ID'
