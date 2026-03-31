package app

import (
	"flag"
	"fmt"
	"io"
)

func runCompletion(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline completion", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: mainline completion [bash|zsh|fish]")
	}

	switch fs.Arg(0) {
	case "bash":
		_, err := io.WriteString(stdout, bashCompletionScript())
		return err
	case "zsh":
		_, err := io.WriteString(stdout, zshCompletionScript())
		return err
	case "fish":
		_, err := io.WriteString(stdout, fishCompletionScript())
		return err
	default:
		return fmt.Errorf("unknown shell %q; expected bash, zsh, or fish", fs.Arg(0))
	}
}

func bashCompletionScript() string {
	return `# bash completion for mainline and mq
_mainline_completions()
{
  local cur prev words cword
  _init_completion || return

  if [[ ${cword} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "submit status run-once publish doctor completion repo" -- "$cur") )
    return
  fi

  if [[ ${words[1]} == "repo" && ${cword} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "init show" -- "$cur") )
    return
  fi

  if [[ ${words[1]} == "completion" && ${cword} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
    return
  fi

  case "${words[1]}" in
    submit)
      COMPREPLY=( $(compgen -W "--repo --branch --worktree" -- "$cur") )
      ;;
    status)
      COMPREPLY=( $(compgen -W "--repo --json --events" -- "$cur") )
      ;;
    run-once|publish|doctor)
      COMPREPLY=( $(compgen -W "--repo --json" -- "$cur") )
      ;;
    repo)
      case "${words[2]}" in
        init)
          COMPREPLY=( $(compgen -W "--repo --protected-branch --remote --main-worktree" -- "$cur") )
          ;;
        show)
          COMPREPLY=( $(compgen -W "--repo --json" -- "$cur") )
          ;;
      esac
      ;;
  esac
}

complete -F _mainline_completions mainline
complete -F _mainline_completions mq
`
}

func zshCompletionScript() string {
	return `#compdef mainline mq

_mainline() {
  local -a commands
  commands=(
    'submit:queue a source worktree'
    'status:show queue and publish status'
    'run-once:run one integration or publish cycle'
    'publish:queue publish of the protected tip'
    'doctor:inspect repo health'
    'completion:emit shell completion script'
    'repo:repository commands'
  )

  if (( CURRENT == 2 )); then
    _describe 'command' commands
    return
  fi

  case "$words[2]" in
    repo)
      if (( CURRENT == 3 )); then
        _describe 'repo command' 'init:initialize repo config' 'show:show repo config'
        return
      fi
      ;;
    completion)
      _values 'shell' bash zsh fish
      return
      ;;
    submit)
      _arguments '--repo[repository path]:path:_files -/' '--branch[branch name]:branch:' '--worktree[source worktree]:path:_files -/'
      return
      ;;
    status)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--events[number of recent events]:count:'
      return
      ;;
    run-once|publish|doctor)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]'
      return
      ;;
  esac
}

_mainline "$@"
`
}

func fishCompletionScript() string {
	return `complete -c mainline -f -n "__fish_use_subcommand" -a "submit status run-once publish doctor completion repo"
complete -c mq -f -n "__fish_use_subcommand" -a "submit status run-once publish doctor completion repo"

complete -c mainline -f -n "__fish_seen_subcommand_from repo; and not __fish_seen_subcommand_from init show" -a "init show"
complete -c mq -f -n "__fish_seen_subcommand_from repo; and not __fish_seen_subcommand_from init show" -a "init show"

complete -c mainline -f -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
complete -c mq -f -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"

complete -c mainline -l repo
complete -c mq -l repo
complete -c mainline -l json
complete -c mq -l json
complete -c mainline -l events
complete -c mq -l events
complete -c mainline -l branch
complete -c mq -l branch
complete -c mainline -l worktree
complete -c mq -l worktree
complete -c mainline -l protected-branch
complete -c mq -l protected-branch
complete -c mainline -l remote
complete -c mq -l remote
complete -c mainline -l main-worktree
complete -c mq -l main-worktree
`
}
