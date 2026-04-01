package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
)

func runCompletion(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" completion", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s completion [--json] [bash|zsh|fish]

Emit a shell completion script for mainline and mq.

Examples:
  mq completion zsh
  mq --json completion bash

Flags:
`, currentCLIProgramName()))
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "output json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: mainline completion [bash|zsh|fish]")
	}

	shell := fs.Arg(0)
	var script string
	switch fs.Arg(0) {
	case "bash":
		script = bashCompletionScript()
	case "zsh":
		script = zshCompletionScript()
	case "fish":
		script = fishCompletionScript()
	default:
		return fmt.Errorf("unknown shell %q; expected bash, zsh, or fish", fs.Arg(0))
	}
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]string{
			"shell":  shell,
			"script": script,
		})
	}
	_, err := io.WriteString(stdout, script)
	return err
}

func bashCompletionScript() string {
	return `# bash completion for mainline and mq
_mainline_completions()
{
  local cur prev words cword
  _init_completion || return

  if [[ ${cword} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "land submit status confidence run-once wait retry cancel publish logs watch events doctor completion version config repo registry" -- "$cur") )
    return
  fi

  if [[ ${words[1]} == "repo" && ${cword} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "init show audit root" -- "$cur") )
    return
  fi

  if [[ ${words[1]} == "registry" && ${cword} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "prune" -- "$cur") )
    return
  fi

  if [[ ${words[1]} == "config" && ${cword} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "edit" -- "$cur") )
    return
  fi

  if [[ ${words[1]} == "completion" && ${cword} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
    return
  fi

  case "${words[1]}" in
    land)
      COMPREPLY=( $(compgen -W "--repo --branch --sha --worktree --requested-by --priority --allow-newer-head --json --timeout --poll-interval" -- "$cur") )
      ;;
    submit)
      COMPREPLY=( $(compgen -W "--repo --branch --sha --worktree --requested-by --priority --allow-newer-head --json --check --check-only --queue-only --wait --for --timeout --poll-interval" -- "$cur") )
      ;;
    status)
      COMPREPLY=( $(compgen -W "--repo --json --events" -- "$cur") )
      ;;
    confidence)
      COMPREPLY=( $(compgen -W "--repo --json --events --soak-summary --cert-report" -- "$cur") )
      ;;
    wait)
      COMPREPLY=( $(compgen -W "--repo --submission --for --json --timeout --poll-interval" -- "$cur") )
      ;;
    retry|cancel)
      COMPREPLY=( $(compgen -W "--repo --submission --publish" -- "$cur") )
      ;;
    logs)
      COMPREPLY=( $(compgen -W "--repo --json --lifecycle --follow --limit --poll-interval --idle-exit" -- "$cur") )
      ;;
    watch)
      COMPREPLY=( $(compgen -W "--repo --json --events --interval --max-cycles" -- "$cur") )
      ;;
    events)
      COMPREPLY=( $(compgen -W "--repo --json --lifecycle --follow --limit --poll-interval --idle-exit" -- "$cur") )
      ;;
    run-once|publish)
      COMPREPLY=( $(compgen -W "--repo" -- "$cur") )
      ;;
    doctor)
      COMPREPLY=( $(compgen -W "--repo --json --fix" -- "$cur") )
      ;;
    config)
      case "${words[2]}" in
        edit)
          COMPREPLY=( $(compgen -W "--repo --editor --print-path" -- "$cur") )
          ;;
      esac
      ;;
    repo)
      case "${words[2]}" in
        init)
          COMPREPLY=( $(compgen -W "--repo --protected-branch --remote --main-worktree" -- "$cur") )
          ;;
        show)
          COMPREPLY=( $(compgen -W "--repo --json" -- "$cur") )
          ;;
        audit)
          COMPREPLY=( $(compgen -W "--repo --json" -- "$cur") )
          ;;
        root)
          COMPREPLY=( $(compgen -W "--repo --json --adopt-root" -- "$cur") )
          ;;
      esac
      ;;
    registry)
      case "${words[2]}" in
        prune)
          COMPREPLY=( $(compgen -W "--json --registry" -- "$cur") )
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
    'land:submit and wait for integrate plus publish'
    'wait:wait on a durable submission id'
    'status:show queue and publish status'
    'confidence:show promotion confidence and evidence'
    'run-once:run one integration or publish cycle'
    'retry:requeue a blocked, failed, or cancelled item'
    'cancel:cancel a queued or failed item'
    'publish:queue publish of the protected tip'
    'logs:show durable queue history'
    'watch:refresh queue status continuously'
    'events:stream durable queue events'
    'doctor:inspect repo health'
    'completion:emit shell completion script'
    'version:show build metadata'
    'config:configuration commands'
    'repo:repository commands'
    'registry:global registry commands'
  )

  if (( CURRENT == 2 )); then
    _describe 'command' commands
    return
  fi

  case "$words[2]" in
    repo)
      if (( CURRENT == 3 )); then
        _describe 'repo command' 'init:initialize repo config' 'show:show repo config' 'audit:list refs not merged into protected main' 'root:inspect the canonical root checkout'
        return
      fi
      ;;
    registry)
      if (( CURRENT == 3 )); then
        _describe 'registry command' 'prune:remove stale global registry entries'
        return
      fi
      ;;
    config)
      if (( CURRENT == 3 )); then
        _describe 'config command' 'edit:open the repo config in an editor'
        return
      fi
      ;;
    completion)
      _values 'shell' bash zsh fish
      return
      ;;
    land)
      _arguments '--repo[source worktree path]:path:_files -/' '--branch[branch to submit]:branch:' '--sha[detached commit to submit]:sha:' '--worktree[source worktree override]:path:_files -/' '--requested-by[submitter identity]:identity:' '--priority[submission priority]:priority:(high normal low)' '--allow-newer-head[allow the queued branch tip to advance before integration if it stays descended from the submitted sha]' '--json[json output]' '--timeout[maximum wait time]:duration:' '--poll-interval[wait interval between worker checks]:duration:'
      return
      ;;
    submit)
      _arguments '--repo[repository path]:path:_files -/' '--branch[branch name]:branch:' '--sha[detached commit to submit]:sha:' '--worktree[source worktree]:path:_files -/' '--requested-by[submitter identity]:identity:' '--priority[submission priority]:priority:(high normal low)' '--allow-newer-head[allow the queued branch tip to advance before integration if it stays descended from the submitted sha]' '--json[json output]' '--check[validate submission without queueing]' '--check-only[validate submission without queueing]' '--queue-only[queue without opportunistically draining]' '--wait[wait for the submission result]' '--for[wait target when used with --wait]:target:(integrated landed)' '--timeout[maximum integration wait time]:duration:' '--poll-interval[wait interval between worker checks]:duration:'
      return
      ;;
    status)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--events[number of recent events]:count:'
      return
      ;;
    confidence)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--events[number of recent events]:count:' '--soak-summary[path to soak summary json]:path:_files' '--cert-report[path to certification report json]:path:_files'
      return
      ;;
    wait)
      _arguments '--repo[repository path]:path:_files -/' '--submission[submission id]:id:' '--for[wait target]:target:(integrated landed)' '--json[json output]' '--timeout[maximum wait time]:duration:' '--poll-interval[wait interval between worker checks]:duration:'
      return
      ;;
    retry|cancel)
      _arguments '--repo[repository path]:path:_files -/' '--submission[integration submission id]:id:' '--publish[publish request id]:id:'
      return
      ;;
    logs)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--lifecycle[emit normalized branch lifecycle events]' '--follow[stream events continuously]' '--limit[number of initial events]:count:' '--poll-interval[poll interval]:duration:' '--idle-exit[exit after an idle follow poll]'
      return
      ;;
    watch)
      _arguments '--repo[repository path]:path:_files -/' '--json[ndjson snapshots]' '--events[number of recent events per snapshot]:count:' '--interval[refresh interval]:duration:' '--max-cycles[maximum refresh cycles]:count:'
      return
      ;;
    events)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--lifecycle[emit normalized branch lifecycle events]' '--follow[stream events continuously]' '--limit[number of initial events]:count:' '--poll-interval[poll interval]:duration:' '--idle-exit[exit after an idle follow poll]'
      return
      ;;
    run-once|publish)
      _arguments '--repo[repository path]:path:_files -/'
      return
      ;;
    doctor)
      _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--fix[apply safe automatic fixes]'
      return
      ;;
    config)
      _arguments '--repo[repository path]:path:_files -/' '--editor[editor binary]:editor:_command_names' '--print-path[print config path before editing]'
      return
      ;;
    repo)
      case "$words[3]" in
        init)
          _arguments '--repo[repository path]:path:_files -/' '--protected-branch[protected branch name]:branch:' '--remote[default remote name]:remote:' '--main-worktree[canonical protected-branch worktree path]:path:_files -/'
          return
          ;;
        show|audit)
          _arguments '--repo[repository path]:path:_files -/' '--json[json output]'
          return
          ;;
        root)
          _arguments '--repo[repository path]:path:_files -/' '--json[json output]' '--adopt-root[set the repository root as the canonical main worktree when it is already clean and on the protected branch]'
          return
          ;;
      esac
      ;;
    registry)
      case "$words[3]" in
        prune)
          _arguments '--json[json output]' '--registry[registry path override]:path:_files -/'
          return
          ;;
      esac
      ;;
  esac
}

_mainline "$@"
`
}

func fishCompletionScript() string {
	return `complete -c mainline -f -n "__fish_use_subcommand" -a "land submit status confidence run-once wait retry cancel publish logs watch events doctor completion version config repo registry"
complete -c mq -f -n "__fish_use_subcommand" -a "land submit status confidence run-once wait retry cancel publish logs watch events doctor completion version config repo registry"

complete -c mainline -f -n "__fish_seen_subcommand_from repo; and not __fish_seen_subcommand_from init show audit root" -a "init show audit root"
complete -c mq -f -n "__fish_seen_subcommand_from repo; and not __fish_seen_subcommand_from init show audit root" -a "init show audit root"
complete -c mainline -f -n "__fish_seen_subcommand_from registry; and not __fish_seen_subcommand_from prune" -a "prune"
complete -c mq -f -n "__fish_seen_subcommand_from registry; and not __fish_seen_subcommand_from prune" -a "prune"
complete -c mainline -f -n "__fish_seen_subcommand_from config; and not __fish_seen_subcommand_from edit" -a "edit"
complete -c mq -f -n "__fish_seen_subcommand_from config; and not __fish_seen_subcommand_from edit" -a "edit"

complete -c mainline -f -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
complete -c mq -f -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"

complete -c mainline -l repo
complete -c mq -l repo
complete -c mainline -n "__fish_seen_subcommand_from status doctor repo show repo audit repo root" -l json
complete -c mq -n "__fish_seen_subcommand_from status doctor repo show repo audit repo root" -l json
complete -c mainline -n "__fish_seen_subcommand_from doctor" -l fix
complete -c mq -n "__fish_seen_subcommand_from doctor" -l fix
complete -c mainline -n "__fish_seen_subcommand_from logs events" -l json
complete -c mq -n "__fish_seen_subcommand_from logs events" -l json
complete -c mainline -n "__fish_seen_subcommand_from logs events" -l lifecycle
complete -c mq -n "__fish_seen_subcommand_from logs events" -l lifecycle
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
complete -c mainline -n "__fish_seen_subcommand_from confidence" -l json
complete -c mq -n "__fish_seen_subcommand_from confidence" -l json
complete -c mainline -n "__fish_seen_subcommand_from confidence" -l events
complete -c mq -n "__fish_seen_subcommand_from confidence" -l events
complete -c mainline -n "__fish_seen_subcommand_from confidence" -l soak-summary
complete -c mq -n "__fish_seen_subcommand_from confidence" -l soak-summary
complete -c mainline -n "__fish_seen_subcommand_from confidence" -l cert-report
complete -c mq -n "__fish_seen_subcommand_from confidence" -l cert-report
complete -c mainline -n "__fish_seen_subcommand_from land" -l branch
complete -c mq -n "__fish_seen_subcommand_from land" -l branch
complete -c mainline -n "__fish_seen_subcommand_from land" -l sha
complete -c mq -n "__fish_seen_subcommand_from land" -l sha
complete -c mainline -n "__fish_seen_subcommand_from land" -l worktree
complete -c mq -n "__fish_seen_subcommand_from land" -l worktree
complete -c mainline -n "__fish_seen_subcommand_from land" -l requested-by
complete -c mq -n "__fish_seen_subcommand_from land" -l requested-by
complete -c mainline -n "__fish_seen_subcommand_from land" -l priority
complete -c mq -n "__fish_seen_subcommand_from land" -l priority
complete -c mainline -n "__fish_seen_subcommand_from land" -l allow-newer-head
complete -c mq -n "__fish_seen_subcommand_from land" -l allow-newer-head
complete -c mainline -n "__fish_seen_subcommand_from land" -l json
complete -c mq -n "__fish_seen_subcommand_from land" -l json
complete -c mainline -n "__fish_seen_subcommand_from land" -l timeout
complete -c mq -n "__fish_seen_subcommand_from land" -l timeout
complete -c mainline -n "__fish_seen_subcommand_from land" -l poll-interval
complete -c mq -n "__fish_seen_subcommand_from land" -l poll-interval
complete -c mainline -n "__fish_seen_subcommand_from submit" -l branch
complete -c mq -n "__fish_seen_subcommand_from submit" -l branch
complete -c mainline -n "__fish_seen_subcommand_from submit" -l sha
complete -c mq -n "__fish_seen_subcommand_from submit" -l sha
complete -c mainline -n "__fish_seen_subcommand_from submit" -l worktree
complete -c mq -n "__fish_seen_subcommand_from submit" -l worktree
complete -c mainline -n "__fish_seen_subcommand_from submit" -l requested-by
complete -c mq -n "__fish_seen_subcommand_from submit" -l requested-by
complete -c mainline -n "__fish_seen_subcommand_from submit" -l priority
complete -c mq -n "__fish_seen_subcommand_from submit" -l priority
complete -c mainline -n "__fish_seen_subcommand_from submit" -l allow-newer-head
complete -c mq -n "__fish_seen_subcommand_from submit" -l allow-newer-head
complete -c mainline -n "__fish_seen_subcommand_from submit" -l json
complete -c mq -n "__fish_seen_subcommand_from submit" -l json
complete -c mainline -n "__fish_seen_subcommand_from submit" -l check
complete -c mq -n "__fish_seen_subcommand_from submit" -l check
complete -c mainline -n "__fish_seen_subcommand_from submit" -l check-only
complete -c mq -n "__fish_seen_subcommand_from submit" -l check-only
complete -c mainline -n "__fish_seen_subcommand_from submit" -l queue-only
complete -c mq -n "__fish_seen_subcommand_from submit" -l queue-only
complete -c mainline -n "__fish_seen_subcommand_from submit" -l wait
complete -c mq -n "__fish_seen_subcommand_from submit" -l wait
complete -c mainline -n "__fish_seen_subcommand_from submit" -l for
complete -c mq -n "__fish_seen_subcommand_from submit" -l for
complete -c mainline -n "__fish_seen_subcommand_from submit" -l timeout
complete -c mq -n "__fish_seen_subcommand_from submit" -l timeout
complete -c mainline -n "__fish_seen_subcommand_from submit" -l poll-interval
complete -c mq -n "__fish_seen_subcommand_from submit" -l poll-interval
complete -c mainline -n "__fish_seen_subcommand_from wait" -l submission
complete -c mq -n "__fish_seen_subcommand_from wait" -l submission
complete -c mainline -n "__fish_seen_subcommand_from wait" -l for
complete -c mq -n "__fish_seen_subcommand_from wait" -l for
complete -c mainline -n "__fish_seen_subcommand_from wait" -l json
complete -c mq -n "__fish_seen_subcommand_from wait" -l json
complete -c mainline -n "__fish_seen_subcommand_from wait" -l timeout
complete -c mq -n "__fish_seen_subcommand_from wait" -l timeout
complete -c mainline -n "__fish_seen_subcommand_from wait" -l poll-interval
complete -c mq -n "__fish_seen_subcommand_from wait" -l poll-interval
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
complete -c mainline -n "__fish_seen_subcommand_from repo root" -l adopt-root
complete -c mq -n "__fish_seen_subcommand_from repo root" -l adopt-root
complete -c mainline -n "__fish_seen_subcommand_from registry prune" -l json
complete -c mq -n "__fish_seen_subcommand_from registry prune" -l json
complete -c mainline -n "__fish_seen_subcommand_from registry prune" -l registry
complete -c mq -n "__fish_seen_subcommand_from registry prune" -l registry
complete -c mainline -n "__fish_seen_subcommand_from config edit" -l editor
complete -c mq -n "__fish_seen_subcommand_from config edit" -l editor
complete -c mainline -n "__fish_seen_subcommand_from config edit" -l print-path
complete -c mq -n "__fish_seen_subcommand_from config edit" -l print-path
`
}
