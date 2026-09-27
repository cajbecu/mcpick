# fish completion for mcpick
# install: cp completions/mcpick.fish ~/.config/fish/completions/mcpick.fish

# Everything after `run` is the agent's own command line.
function __mcpick_no_run
    not __fish_seen_subcommand_from run
end

# One name per line; names a shell would need to quote are left out.
function __mcpick_servers
    mcpick __complete servers 2>/dev/null
end

complete -c mcpick -f

complete -c mcpick -n __mcpick_no_run -a run -d 'pick, render a config and launch a command'
complete -c mcpick -n __mcpick_no_run -a list -d 'print the merged catalog'
complete -c mcpick -n __mcpick_no_run -a doctor -d 'connect to the selected servers and report'
complete -c mcpick -n __mcpick_no_run -a measure -d 'refresh the context-cost estimates'
complete -c mcpick -n __mcpick_no_run -a export -d 'print the selection in a tool dialect'
complete -c mcpick -n __mcpick_no_run -a serve -d 'run as one MCP server fronting the selection'
complete -c mcpick -n __mcpick_no_run -a import -d 'seed the catalog from ~/.claude.json'
complete -c mcpick -n __mcpick_no_run -a move -d 'move a server to project, local or user'
complete -c mcpick -n '__fish_seen_subcommand_from move' -a '(__mcpick_servers) project local user'
complete -c mcpick -n __mcpick_no_run -a profile -d 'list, save, delete or rename profiles'
complete -c mcpick -n '__fish_seen_subcommand_from profile' -a 'list save delete rename'
complete -c mcpick -n __mcpick_no_run -a login -d 'run the OAuth flow for a remote server'
complete -c mcpick -n __mcpick_no_run -a logout -d 'forget a stored token'
complete -c mcpick -n __mcpick_no_run -a agents -d 'list the agents mcpick can launch'
complete -c mcpick -n __mcpick_no_run -a restore -d 'undo a project file left behind by a crash'

complete -c mcpick -n __mcpick_no_run -l uid -r -d 'session id'
complete -c mcpick -n __mcpick_no_run -l file -r -F -d 'catalog file'
complete -c mcpick -n __mcpick_no_run -l agent -r -d 'agent to render for' \
    -a 'claude codex copilot pi muse opencode gemini antigravity grok devin'
complete -c mcpick -n __mcpick_no_run -l profile -r -d 'named profile (yours, the catalog\'s, or default)'
complete -c mcpick -n __mcpick_no_run -l select -r -a '(__mcpick_servers)' -d 'exact server list'
complete -c mcpick -n __mcpick_no_run -l addr -r -d 'serve over HTTP'
complete -c mcpick -n __mcpick_no_run -l home -r -a '(__fish_complete_directories)' -d 'where mcpick keeps its files'
complete -c mcpick -n __mcpick_no_run -l timeout -r -d 'per-server connect timeout'
complete -c mcpick -n __mcpick_no_run -l redact -d 'move: rewrite secrets as ${VAR} (import does by default)'
complete -c mcpick -n __mcpick_no_run -l yes -d 'import: copy credentials as they are; move: write them into the catalog as they are'
complete -c mcpick -n __mcpick_no_run -l trust -d 'measure, doctor: run and remember untrusted commands'
complete -c mcpick -n __mcpick_no_run -l trust-catalog -d 'measure, doctor: trust this project\'s catalog for measuring'
complete -c mcpick -n __mcpick_no_run -l json -d 'machine-readable output'
complete -c mcpick -n __mcpick_no_run -s y -l last -d 'reuse the saved selection'
complete -c mcpick -n __mcpick_no_run -l all -d 'select everything'
complete -c mcpick -n __mcpick_no_run -l none -d 'select nothing'
complete -c mcpick -n __mcpick_no_run -s h -l help -d 'show help'
complete -c mcpick -n __mcpick_no_run -s V -l version -d 'print version'
