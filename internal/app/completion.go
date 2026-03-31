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
    COMPREPLY=( $(compgen -W "submit status run-once retry cancel publish logs watch events doctor completion repo" -- "$cur") )
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
    retry|cancel)
      COMPREPLY=( $(compgen -W "--repo --submission --publish" -- "$cur") )
      ;;
    logs)
      COMPREPLY=( $(compgen -W "--repo --json --follow --limit --poll-interval --idle-exit" -- "$cur") )
      ;;
    watch)
      COMPREPLY=( $(compgen -W "--repo --json --events --interval --max-cycles" -- "$cur") )
      ;;
    events)
      COMPREPLY=( $(compgen -W "--repo --json --follow --limit --poll-interval --idle-exit" -- "$cur") )
      ;;
    run-once|publish)
      COMPREPLY=( $(compgen -W "--repo" -- "$cur") )
      ;;
    doctor)
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
    'retry:requeue a blocked, failed, or cancelled item'
    'cancel:cancel a queued or failed item'
    'publish:queue publish of the protected tip'
    'logs:show durable queue history'
    'watch:refresh queue status continuously'
    'events:stream durable queue events'
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
    retry|cancel)
      _arguments '--repo[repository path]:path:_files -/' '--submission[integration submission id]:id:' '--publish[publish request id]:id:'
      return
      ;;
    logs)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--follow[stream events continuously]' '--limit[number of initial events]:count:' '--poll-interval[poll interval]:duration:' '--idle-exit[exit after an idle follow poll]'
      return
      ;;
    watch)
      _arguments '--repo[repository path]:path:_files -/' '--json[ndjson snapshots]' '--events[number of recent events per snapshot]:count:' '--interval[refresh interval]:duration:' '--max-cycles[maximum refresh cycles]:count:'
      return
      ;;
    events)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--follow[stream events continuously]' '--limit[number of initial events]:count:' '--poll-interval[poll interval]:duration:' '--idle-exit[exit after an idle follow poll]'
      return
      ;;
    run-once|publish)
      _arguments '--repo[repository path]:path:_files -/'
      return
      ;;
    doctor)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]'
      return
      ;;
  esac
}

_mainline "$@"
`
}

func fishCompletionScript() string {
	return `complete -c mainline -f -n "__fish_use_subcommand" -a "submit status run-once retry cancel publish logs watch events doctor completion repo"
complete -c mq -f -n "__fish_use_subcommand" -a "submit status run-once retry cancel publish logs watch events doctor completion repo"

complete -c mainline -f -n "__fish_seen_subcommand_from repo; and not __fish_seen_subcommand_from init show" -a "init show"
complete -c mq -f -n "__fish_seen_subcommand_from repo; and not __fish_seen_subcommand_from init show" -a "init show"

complete -c mainline -f -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
complete -c mq -f -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"

complete -c mainline -l repo
complete -c mq -l repo
complete -c mainline -n "__fish_seen_subcommand_from status doctor repo show" -l json
complete -c mq -n "__fish_seen_subcommand_from status doctor repo show" -l json
complete -c mainline -n "__fish_seen_subcommand_from logs events" -l json
complete -c mq -n "__fish_seen_subcommand_from logs events" -l json
complete -c mainline -n "__fish_seen_subcommand_from logs events" -l follow
complete -c mq -n "__fish_seen_subcommand_from logs events" -l follow
complete -c mainline -n "__fish_seen_subcommand_from logs events" -l limit
complete -c mq -n "__fish_seen_subcommand_from logs events" -l limit
complete -c mainline -n "__fish_seen_subcommand_from logs events" -l poll-interval
complete -c mq -n "__fish_seen_subcommand_from logs events" -l poll-interval
complete -c mainline -n "__fish_seen_subcommand_from logs events" -l idle-exit
complete -c mq -n "__fish_seen_subcommand_from logs events" -l idle-exit
complete -c mainline -n "__fish_seen_subcommand_from watch" -l json
complete -c mq -n "__fish_seen_subcommand_from watch" -l json
complete -c mainline -n "__fish_seen_subcommand_from watch" -l events
complete -c mq -n "__fish_seen_subcommand_from watch" -l events
complete -c mainline -n "__fish_seen_subcommand_from watch" -l interval
complete -c mq -n "__fish_seen_subcommand_from watch" -l interval
complete -c mainline -n "__fish_seen_subcommand_from watch" -l max-cycles
complete -c mq -n "__fish_seen_subcommand_from watch" -l max-cycles
complete -c mainline -n "__fish_seen_subcommand_from status" -l events
complete -c mq -n "__fish_seen_subcommand_from status" -l events
complete -c mainline -n "__fish_seen_subcommand_from submit" -l branch
complete -c mq -n "__fish_seen_subcommand_from submit" -l branch
complete -c mainline -n "__fish_seen_subcommand_from submit" -l worktree
complete -c mq -n "__fish_seen_subcommand_from submit" -l worktree
complete -c mainline -n "__fish_seen_subcommand_from retry cancel" -l submission
complete -c mq -n "__fish_seen_subcommand_from retry cancel" -l submission
complete -c mainline -n "__fish_seen_subcommand_from retry cancel" -l publish
complete -c mq -n "__fish_seen_subcommand_from retry cancel" -l publish
complete -c mainline -n "__fish_seen_subcommand_from repo init" -l protected-branch
complete -c mq -n "__fish_seen_subcommand_from repo init" -l protected-branch
complete -c mainline -n "__fish_seen_subcommand_from repo init" -l remote
complete -c mq -n "__fish_seen_subcommand_from repo init" -l remote
complete -c mainline -n "__fish_seen_subcommand_from repo init" -l main-worktree
complete -c mq -n "__fish_seen_subcommand_from repo init" -l main-worktree
`
}
