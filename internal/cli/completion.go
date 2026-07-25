package cli

import (
	"errors"
	"fmt"
	"io"
)

func completion(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("completion requires bash, zsh, or fish")
	}
	switch args[0] {
	case "bash":
		fmt.Fprintln(stdout, `_ctx_complete() {
  local current="${COMP_WORDS[COMP_CWORD]}"
  if [[ "${COMP_WORDS[1]}" == "host" && "${COMP_WORDS[2]}" == "ls" ]]; then
    COMPREPLY=($(compgen -W "--all --json --help" -- "$current"))
  elif [[ "${COMP_WORDS[1]}" == "host" ]]; then
    COMPREPLY=($(compgen -W "ls --name --source --interval --no-tailscale --help" -- "$current"))
  elif [[ "${COMP_WORDS[1]}" == "tui" ]]; then
    COMPREPLY=($(compgen -W "--help" -- "$current"))
  else
    COMPREPLY=($(compgen -W "tui host ls tail continue doctor update completion version help" -- "$current"))
  fi
}
complete -F _ctx_complete ctx`)
	case "zsh":
		fmt.Fprintln(stdout, `#compdef ctx
_arguments \
  '1:command:(tui host ls tail continue doctor update completion version help)' \
  '2:argument:(ls --help)'`)
	case "fish":
		fmt.Fprintln(stdout, `complete -c ctx -f
complete -c ctx -n "__fish_use_subcommand" -a "tui host ls tail continue doctor update completion version help"
complete -c ctx -n "__fish_seen_subcommand_from tui" -l help -d "Show dashboard keyboard controls"
complete -c ctx -n "__fish_seen_subcommand_from host; and not __fish_seen_subcommand_from ls" -a "ls"
complete -c ctx -n "__fish_seen_subcommand_from host; and __fish_seen_subcommand_from ls" -l all -d "Include every workspace"
complete -c ctx -n "__fish_seen_subcommand_from host; and __fish_seen_subcommand_from ls" -l json -d "Emit JSON"`)
	default:
		return fmt.Errorf("unsupported shell %q; expected bash, zsh, or fish", args[0])
	}
	return nil
}
