# bash completion for mcpick
# install: cp completions/mcpick.bash ~/.local/share/bash-completion/completions/mcpick

_mcpick() {
    local cur prev commands flags targets
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    commands="run list doctor measure export serve import profile login logout targets restore help"
    flags="--uid --file --target --profile --select --addr --home --timeout --redact --json --last --all --none --help --version"
    targets="claude codex copilot pi muse opencode gemini antigravity grok devin"

    # Everything after `run` belongs to the agent, not to mcpick.
    local i
    for (( i=1; i < COMP_CWORD; i++ )); do
        if [[ "${COMP_WORDS[i]}" == run ]]; then
            return 0
        fi
    done

    case "$prev" in
        --target)
            COMPREPLY=( $(compgen -W "$targets" -- "$cur") )
            return 0 ;;
        --file)
            COMPREPLY=( $(compgen -f -- "$cur") )
            return 0 ;;
        --home)
            COMPREPLY=( $(compgen -d -- "$cur") )
            return 0 ;;
        --select|--profile|--uid|--addr|--timeout)
            return 0 ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
    else
        COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
    fi
}

complete -F _mcpick mcpick
