package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/delightroom/ctx/internal/source"
	"github.com/delightroom/ctx/internal/tailnet"
	ctxtui "github.com/delightroom/ctx/internal/tui"
	"golang.org/x/sync/singleflight"
)

func tuiCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		tuiUsage(stdout)
		return nil
	}
	if len(args) != 0 {
		return errors.New("tui takes no arguments")
	}
	if !canPrompt(stdin, stdout) {
		return errors.New("tui requires an interactive terminal")
	}
	return runTUI(stdin, stdout, stderr)
}

func tuiUsage(writer io.Writer) {
	fmt.Fprintln(writer, `ctx tui — interactive context dashboard

Usage:
  ctx
  ctx tui

The dashboard discovers local Claude Code and Codex sessions plus ctx feeds
reachable over the tailnet. Selecting an action closes the dashboard before
running the corresponding host, tail, or continue command.

Keys:
  Tab / Shift+Tab     Change focused panel
  Up / Down or j / k  Move selection
  PageUp / PageDown   Move five rows
  /                   Filter the focused panel
  a                   Toggle workspace/all local sessions
  r                   Refresh status and inventories
  Enter               Open available actions
  h                   Host selected local session
  t / f               Tail once / follow shared context
  c                   Continue selected shared context
  ?                   Show keyboard help
  q / Ctrl-C          Quit`)
}

func runTUI(stdin io.Reader, stdout, stderr io.Writer) error {
	return runTUIWith(tuiDependencies{
		runProgram:   ctxtui.Run,
		host:         host,
		tail:         tail,
		continueWork: continueWork,
	}, stdin, stdout, stderr)
}

type tuiDependencies struct {
	runProgram   func(ctxtui.Config, io.Reader, io.Writer) (ctxtui.Result, error)
	host         func([]string, io.Writer, io.Writer) error
	tail         func([]string, io.Reader, io.Writer, io.Writer) error
	continueWork func([]string, io.Reader, io.Writer, io.Writer) error
}

func runTUIWith(dependencies tuiDependencies, stdin io.Reader, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	result, err := dependencies.runProgram(ctxtui.Config{
		Context: context.Background(),
		Version: Version,
		Loader:  &cliTUILoader{cwd: cwd},
	}, stdin, stdout)
	if err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}

	switch result.Action {
	case ctxtui.ActionNone:
		return nil
	case ctxtui.ActionHost:
		return dependencies.host([]string{"--source", result.SourcePath}, stdout, stderr)
	case ctxtui.ActionTail:
		args := []string{result.Locator}
		if result.Follow {
			args = []string{"--follow", result.Locator}
		}
		return dependencies.tail(args, stdin, stdout, stderr)
	case ctxtui.ActionContinue:
		return dependencies.continueWork([]string{result.Locator}, stdin, stdout, stderr)
	default:
		return fmt.Errorf("unsupported TUI action %q", result.Action)
	}
}

type cliTUILoader struct {
	cwd         string
	statusGroup singleflight.Group
	statusRead  func(context.Context) (tailnet.Status, error)
}

func (loader *cliTUILoader) LoadStatus(ctx context.Context) (ctxtui.Status, error) {
	status := ctxtui.Status{Agents: installedAgents()}
	if _, err := exec.LookPath("tailscale"); err != nil {
		status.TailnetError = "Tailscale is not installed"
		return status, errors.New(status.TailnetError)
	}

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tailnetStatus, err := loader.readTailnetStatus(checkCtx)
	if err != nil {
		status.TailnetError = err.Error()
		return status, err
	}
	status.TailnetReady = tailnetStatus.Self.DNSName != ""
	status.TailnetName = tailnet.ShortName(tailnetStatus.Self)
	if !status.TailnetReady {
		status.TailnetError = "Tailscale is installed but not connected"
		return status, errors.New(status.TailnetError)
	}
	return status, nil
}

func (loader *cliTUILoader) readTailnetStatus(ctx context.Context) (tailnet.Status, error) {
	value, err, _ := loader.statusGroup.Do("status", func() (any, error) {
		read := loader.statusRead
		if read == nil {
			read = tailnet.ReadStatus
		}
		return read(ctx)
	})
	if err != nil {
		return tailnet.Status{}, err
	}
	return value.(tailnet.Status), nil
}

func (loader *cliTUILoader) LoadLocal(ctx context.Context, all bool) ([]ctxtui.LocalSession, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	var sessions []source.Session
	var err error
	if all {
		sessions, err = source.ListAll()
	} else {
		sessions, err = source.List(loader.cwd)
	}
	if err != nil {
		return nil, err
	}
	result := make([]ctxtui.LocalSession, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, ctxtui.LocalSession{
			SourceAgent: session.SourceAgent,
			SessionID:   session.SessionID,
			Project:     session.Project,
			CWD:         session.CWD,
			Path:        session.Path,
			ModifiedAt:  session.ModifiedAt,
		})
	}
	return result, nil
}

func (loader *cliTUILoader) LoadShared(ctx context.Context) ([]ctxtui.SharedContext, error) {
	discoveryCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	status, err := loader.readTailnetStatus(discoveryCtx)
	if err != nil {
		return nil, err
	}
	bases := tailnet.OnlinePeerURLsFromStatus(status)
	feeds := discoverFeedBases(discoveryCtx, bases, 2*time.Second)
	contexts := make([]ctxtui.SharedContext, 0, len(feeds))
	for _, feed := range feeds {
		contexts = append(contexts, ctxtui.SharedContext{
			Name:        feed.Name,
			Owner:       feed.Owner,
			Node:        feed.Node,
			SourceAgent: feed.SourceAgent,
			Project:     feed.Project,
			Revision:    feed.Revision,
			UpdatedAt:   feed.UpdatedAt,
		})
	}
	return contexts, nil
}
