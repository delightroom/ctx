package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	ctxclient "github.com/delightroom/ctx/internal/client"
	"github.com/delightroom/ctx/internal/preview"
	"github.com/delightroom/ctx/internal/protocol"
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

Resting on a row loads a deterministic, redacted session peek without calling
an LLM. Set CTX_NO_ANIMATION=1 to disable the opening animation.

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
		Context:   context.Background(),
		Version:   Version,
		Loader:    &cliTUILoader{cwd: cwd},
		ShowIntro: animationsEnabled(),
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
	previewMu   sync.Mutex
	previews    map[string]preview.Summary
	previewKeys []string
}

const maxPreviewCacheEntries = 64

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
			BaseURL:     feed.BaseURL,
		})
	}
	return contexts, nil
}

func (loader *cliTUILoader) LoadLocalPreview(
	ctx context.Context,
	session ctxtui.LocalSession,
) (preview.Summary, error) {
	info, err := os.Stat(session.Path)
	if err != nil {
		return preview.Summary{}, err
	}
	key := fmt.Sprintf(
		"local:%s:%d:%d",
		session.Path,
		info.ModTime().UnixNano(),
		info.Size(),
	)
	return loader.loadPreview(ctx, key, func() (preview.Summary, error) {
		file, err := source.Open(session.Path)
		if err != nil {
			return preview.Summary{}, err
		}
		return file.PreviewContext(ctx)
	})
}

func (loader *cliTUILoader) LoadSharedPreview(
	ctx context.Context,
	shared ctxtui.SharedContext,
) (preview.Summary, error) {
	key := "shared:" + shared.BaseURL + ":" + shared.Locator() + ":" + shared.Revision
	return loader.loadPreview(ctx, key, func() (preview.Summary, error) {
		if shared.BaseURL == "" {
			return preview.Summary{}, errors.New("shared context origin is unavailable; press r to refresh")
		}
		if shared.Revision == "" {
			return preview.Summary{}, errors.New("shared context revision is unavailable; press r to refresh")
		}
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		digest, revision, err := ctxclient.New(5*time.Second).Digest(
			requestCtx,
			shared.BaseURL,
			shared.Name,
			"",
		)
		if err != nil {
			return preview.Summary{}, err
		}
		if digest.Manifest.ProtocolVersion != protocol.Version {
			return preview.Summary{}, fmt.Errorf(
				"unsupported protocol version %q",
				digest.Manifest.ProtocolVersion,
			)
		}
		if digest.Manifest.Name != shared.Name || digest.Manifest.Node != shared.Node {
			return preview.Summary{}, errors.New("shared context identity changed; press r to refresh")
		}
		if revision == "" || digest.Manifest.Revision != revision {
			return preview.Summary{}, errors.New("shared context returned an inconsistent revision")
		}
		if revision != shared.Revision {
			return preview.Summary{}, errors.New("shared context changed since discovery; press r to refresh")
		}
		return preview.Build(digest), nil
	})
}

func (loader *cliTUILoader) loadPreview(
	ctx context.Context,
	key string,
	load func() (preview.Summary, error),
) (preview.Summary, error) {
	if err := ctx.Err(); err != nil {
		return preview.Summary{}, err
	}

	loader.previewMu.Lock()
	cached, ok := loader.previews[key]
	loader.previewMu.Unlock()
	if ok {
		return cached, nil
	}

	result, err := load()
	if err != nil {
		return preview.Summary{}, err
	}
	if err := ctx.Err(); err != nil {
		return preview.Summary{}, err
	}
	loader.previewMu.Lock()
	if loader.previews == nil {
		loader.previews = make(map[string]preview.Summary)
	}
	if _, exists := loader.previews[key]; !exists {
		if len(loader.previewKeys) == maxPreviewCacheEntries {
			delete(loader.previews, loader.previewKeys[0])
			copy(loader.previewKeys, loader.previewKeys[1:])
			loader.previewKeys = loader.previewKeys[:len(loader.previewKeys)-1]
		}
		loader.previewKeys = append(loader.previewKeys, key)
	}
	loader.previews[key] = result
	loader.previewMu.Unlock()
	return result, nil
}

func animationsEnabled() bool {
	return os.Getenv("CTX_NO_ANIMATION") == "" && os.Getenv("TERM") != "dumb"
}
