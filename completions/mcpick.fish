# fish completion for mcpick
# install: cp completions/mcpick.fish ~/.config/fish/completions/mcpick.fish

# Everything after `run` is the agent's own command line.
function __mcpick_no_run
    not __fish_seen_subcommand_from run
end

function __mcpick_servers
    mcpick list 2>/dev/null | awk '{print $1}'
end

complete -c mcpick -f

complete -c mcpick -n __mcpick_no_run -a run -d 'pick, render a config and launch a command'
complete -c mcpick -n __mcpick_no_run -a list -d 'print the merged catalog'
complete -c mcpick -n __mcpick_no_run -a doctor -d 'connect to the selected servers and report'
complete -c mcpick -n __mcpick_no_run -a measure -d 'refresh the context-cost estimates'
complete -c mcpick -n __mcpick_no_run -a export -d 'print the selection in a tool dialect'
complete -c mcpick -n __mcpick_no_run -a serve -d 'run as one MCP server fronting the selection'
complete -c mcpick -n __mcpick_no_run -a import -d 'seed the catalog from ~/.claude.json'
complete -c mcpick -n __mcpick_no_run -a profile -d 'list, save or delete profiles'
complete -c mcpick -n '__fish_seen_subcommand_from profile' -a 'list save delete'
complete -c mcpick -n __mcpick_no_run -a login -d 'run the OAuth flow for a remote server'
complete -c mcpick -n __mcpick_no_run -a logout -d 'forget a stored token'
complete -c mcpick -n __mcpick_no_run -a targets -d 'list the agents mcpick can launch'
complete -c mcpick -n __mcpick_no_run -a restore -d 'undo a project file left behind by a crash'

complete -c mcpick -n __mcpick_no_run -l uid -r -d 'session id'
complete -c mcpick -n __mcpick_no_run -l file -r -F -d 'catalog file'
complete -c mcpick -n __mcpick_no_run -l target -r -d 'agent dialect' \
    -a 'claude codex copilot pi muse opencode gemini antigravity grok devin'
complete -c mcpick -n __mcpick_no_run -l profile -r -d 'named profile from the catalog'
complete -c mcpick -n __mcpick_no_run -l select -r -a '(__mcpick_servers)' -d 'exact server list'
complete -c mcpick -n __mcpick_no_run -l addr -r -d 'serve over HTTP'
complete -c mcpick -n __mcpick_no_run -l home -r -a '(__fish_complete_directories)' -d 'where mcpick keeps its files'
complete -c mcpick -n __mcpick_no_run -l timeout -r -d 'per-server connect timeout'
complete -c mcpick -n __mcpick_no_run -l redact -d 'rewrite secrets as ${VAR} on import'
complete -c mcpick -n __mcpick_no_run -l json -d 'machine-readable output'
complete -c mcpick -n __mcpick_no_run -s y -l last -d 'reuse the saved selection'
complete -c mcpick -n __mcpick_no_run -l all -d 'select everything'
complete -c mcpick -n __mcpick_no_run -l none -d 'select nothing'
complete -c mcpick -n __mcpick_no_run -s h -l help -d 'show help'
complete -c mcpick -n __mcpick_no_run -l version -d 'print version'
