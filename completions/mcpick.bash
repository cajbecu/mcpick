# bash completion for mcpick
# install: cp completions/mcpick.bash ~/.local/share/bash-completion/completions/mcpick

_mcpick() {
    local cur prev commands flags agents
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    commands="run list doctor measure export serve import move profile login logout agents restore help"
    flags="--uid --file --agent --profile --select --addr --home --timeout --redact --yes --trust --trust-catalog --json --last --all --none --help --version -y -h -V"
    agents="claude codex copilot pi muse opencode gemini antigravity grok devin"

    # Everything after `run` belongs to the agent, not to mcpick.
    local i
    for (( i=1; i < COMP_CWORD; i++ )); do
        if [[ "${COMP_WORDS[i]}" == run ]]; then
            return 0
        fi
    done

    case "$prev" in
        --agent)
            COMPREPLY=( $(compgen -W "$agents" -- "$cur") )
            return 0 ;;
        --file)
            COMPREPLY=( $(compgen -f -- "$cur") )
            return 0 ;;
        --home)
            COMPREPLY=( $(compgen -d -- "$cur") )
            return 0 ;;
        --select)
            # A comma-separated list: complete the name after the last comma.
            local done=""
            [[ "$cur" == *,* ]] && done="${cur%,*},"
            COMPREPLY=( $(compgen -P "$done" -W "$(mcpick __complete servers 2>/dev/null)" -- "${cur##*,}") )
            return 0 ;;
        --profile|--uid|--addr|--timeout)
            return 0 ;;
        move)
            COMPREPLY=( $(compgen -W "$(mcpick __complete servers 2>/dev/null)" -- "$cur") )
            return 0 ;;
        profile)
            COMPREPLY=( $(compgen -W "list save delete rename" -- "$cur") )
            return 0 ;;
    esac
    if [[ $COMP_CWORD -ge 3 && "${COMP_WORDS[COMP_CWORD-2]}" == move ]]; then
        COMPREPLY=( $(compgen -W "project local user" -- "$cur") )
        return 0
    fi

    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
    else
        COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
    fi
}

complete -F _mcpick mcpick
