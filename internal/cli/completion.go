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
		fmt.Fprintln(stdout, `complete -W "host ls tail continue doctor update completion version help" ctx`)
	case "zsh":
		fmt.Fprintln(stdout, `#compdef ctx
_arguments '1:command:(host ls tail continue doctor update completion version help)'`)
	case "fish":
		fmt.Fprintln(stdout, `complete -c ctx -f
complete -c ctx -n "__fish_use_subcommand" -a "host ls tail continue doctor update completion version help"`)
	default:
		return fmt.Errorf("unsupported shell %q; expected bash, zsh, or fish", args[0])
	}
	return nil
}
