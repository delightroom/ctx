package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	ctxclient "github.com/delightroom/ctx/internal/client"
	"github.com/delightroom/ctx/internal/protocol"
	"github.com/delightroom/ctx/internal/render"
	"github.com/delightroom/ctx/internal/server"
	"github.com/delightroom/ctx/internal/source"
	"github.com/delightroom/ctx/internal/tailnet"
)

var Version = "dev"

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		if canPrompt(stdin, stdout) {
			if err := launch(stdin, stdout, stderr); err != nil {
				if errors.Is(err, errCancelled) {
					return 0
				}
				fmt.Fprintf(stderr, "ctx: %v\n", err)
				return 1
			}
			return 0
		}
		usage(stdout)
		return 0
	}

	var err error
	switch args[0] {
	case "host":
		err = host(args[1:], stdout, stderr)
	case "ls":
		err = list(args[1:], stdout, stderr)
	case "tail":
		err = tail(args[1:], stdin, stdout, stderr)
	case "continue":
		err = continueWork(args[1:], stdin, stdout, stderr)
	case "doctor":
		err = doctor(args[1:], stdout, stderr)
	case "update":
		err = update(args[1:], stdin, stdout, stderr)
	case "completion":
		err = completion(args[1:], stdout)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, Version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		if errors.Is(err, errCancelled) {
			return 0
		}
		fmt.Fprintf(stderr, "ctx: %v\n", err)
		return 1
	}
	return 0
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, `ctx — tailnet-native AI context sharing

Usage:
  ctx
  ctx host [--name NAME] [--source SESSION.jsonl]
  ctx host ls [--all] [--json]
  ctx ls [HOST]
  ctx tail [-f] [HOST/FEED]
  ctx continue [--with claude|codex] [HOST/FEED]
  ctx doctor
  ctx update
  ctx completion bash|zsh|fish

Commands:
  ctx        Open the interactive context launcher
  host       Publish the current Claude or Codex session
  host ls    List local Claude and Codex sessions available to host
  ls         List feeds from one host or discover tailnet hosts
  tail       Print a feed; -f follows new revisions
  continue   Start a new local agent from a pinned neutral digest
  doctor     Diagnose installation, Tailscale, and agent sessions
  update     Update ctx using its original installation method
  completion Generate shell completion setup`)
}

func host(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "ls" {
		return hostList(args[1:], stdout, stderr)
	}

	flags := flag.NewFlagSet("host", flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("name", "", "feed name (defaults to project name)")
	sourcePath := flags.String("source", "", "explicit Claude or Codex JSONL session")
	interval := flags.Duration("interval", 5*time.Second, "snapshot refresh interval")
	noTailscale := flags.Bool("no-tailscale", false, "serve on localhost without Tailscale (development)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("host takes flags only")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	var snapshotter *source.File
	if *sourcePath != "" {
		snapshotter, err = source.Open(*sourcePath)
	} else {
		snapshotter, err = source.Discover(cwd)
	}
	if err != nil {
		return err
	}
	initial, err := snapshotter.Snapshot()
	if err != nil {
		return fmt.Errorf("read session: %w", err)
	}
	if *name == "" {
		*name = initial.Manifest.Project
	}
	*name = feedName(*name)
	if *name == "" {
		return errors.New("feed name is empty after normalization")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()

	node := "localhost"
	owner := currentUser()
	publicBase := "http://" + listener.Addr().String()
	if !*noTailscale {
		status, statusErr := tailnet.ReadStatus(ctx)
		if statusErr != nil {
			return statusErr
		}
		node = tailnet.ShortName(status.Self)
		publicBase = fmt.Sprintf("https://%s:%d", status.Self.DNSName, tailnet.ServePort)
	}

	logger := log.New(stderr, "ctx host: ", log.LstdFlags)
	service := server.New(server.Config{
		Name: *name, Node: node, Owner: owner, PublicURL: publicBase,
		Interval: *interval, Logger: logger,
	}, snapshotter)
	if err := service.Refresh(); err != nil {
		return err
	}
	go service.RunRefreshLoop(ctx)

	httpServer := &http.Server{
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	fmt.Fprintf(stdout, "Hosting: ctx://%s/%s\n", node, *name)
	fmt.Fprintf(stdout, "Source: %s\n", displayAgent(initial.Manifest.SourceAgent))
	fmt.Fprintf(stdout, "Session: %s\n", snapshotter.Path())
	fmt.Fprintf(stdout, "Revision: %s\n", initial.Manifest.Revision)
	fmt.Fprintf(stdout, "Digest URL: %s/v1/feeds/%s/digest\n", publicBase, *name)
	fmt.Fprintln(stdout, "Press Ctrl-C to stop.")

	if *noTailscale {
		select {
		case <-ctx.Done():
		case err := <-serverErrors:
			return err
		}
	} else {
		serveErrors := make(chan error, 1)
		go func() {
			serveErrors <- tailnet.Serve(ctx, listener.Addr().String(), stdout, stderr)
		}()
		select {
		case <-ctx.Done():
		case err := <-serverErrors:
			stop()
			return err
		case err := <-serveErrors:
			if err != nil {
				stop()
				return err
			}
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func hostList(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("host ls", flag.ContinueOnError)
	flags.SetOutput(stderr)
	all := flags.Bool("all", false, "include sessions from every workspace")
	asJSON := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("host ls takes flags only")
	}

	var sessions []source.Session
	var err error
	scope := "this workspace"
	if *all {
		sessions, err = source.ListAll()
		scope = "local session stores"
	} else {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return cwdErr
		}
		sessions, err = source.List(cwd)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		if sessions == nil {
			sessions = []source.Session{}
		}
		return json.NewEncoder(stdout).Encode(sessions)
	}
	if len(sessions) == 0 {
		fmt.Fprintf(stdout, "No Claude or Codex sessions found in %s.\n", scope)
		return nil
	}

	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "AGENT\tPROJECT\tUPDATED\tSESSION\tSOURCE")
	for _, session := range sessions {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			displayAgent(session.SourceAgent),
			projectName(session.CWD),
			relativeTime(session.ModifiedAt),
			shortSessionID(session.SessionID),
			session.Path,
		)
	}
	return table.Flush()
}

func list(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ls", flag.ContinueOnError)
	flags.SetOutput(stderr)
	timeout := flags.Duration("timeout", 5*time.Second, "timeout per host")
	asJSON := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return errors.New("ls accepts at most one host")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host := ""
	if flags.NArg() == 1 {
		host = flags.Arg(0)
	}
	feeds, err := discoverFeeds(ctx, host, *timeout)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(feeds)
	}
	if len(feeds) == 0 {
		fmt.Fprintln(stdout, "No ctx feeds found.")
		return nil
	}
	fmt.Fprintf(stdout, "%-24s %-24s %-12s %-20s %s\n", "HOST", "FEED", "AGENT", "UPDATED", "REV")
	for _, feed := range feeds {
		fmt.Fprintf(stdout, "%-24s %-24s %-12s %-20s %s\n",
			feed.Node, feed.Name, displayAgent(feed.SourceAgent),
			relativeTime(feed.UpdatedAt), feed.Revision)
	}
	return nil
}

func tail(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("tail", flag.ContinueOnError)
	flags.SetOutput(stderr)
	follow := flags.Bool("f", false, "follow new revisions")
	flags.BoolVar(follow, "follow", false, "follow new revisions")
	interval := flags.Duration("interval", 3*time.Second, "follow polling interval")
	asJSON := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return errors.New("tail accepts at most one HOST/FEED")
	}
	rawLocator := ""
	var err error
	if flags.NArg() == 1 {
		rawLocator = flags.Arg(0)
	} else {
		if !canPrompt(stdin, stdout) {
			return errors.New("tail requires HOST/FEED outside an interactive terminal")
		}
		rawLocator, err = promptForFeed(stdin, stdout, "Tail which context?")
		if err != nil {
			return err
		}
	}
	locator, base, err := resolveLocator(context.Background(), rawLocator)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := ctxclient.New(20 * time.Second)
	digest, revision, err := client.Digest(ctx, base, locator.Feed, "")
	if err != nil {
		return err
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(digest); err != nil {
			return err
		}
	} else {
		render.Digest(stdout, digest, 0)
	}
	if !*follow {
		return nil
	}

	previous := digest
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			next, nextRevision, fetchErr := client.Digest(ctx, base, locator.Feed, revision)
			if errors.Is(fetchErr, ctxclient.ErrNotModified) {
				continue
			}
			if fetchErr != nil {
				fmt.Fprintf(stderr, "ctx tail: retrying after error: %v\n", fetchErr)
				continue
			}
			if *asJSON {
				if err := json.NewEncoder(stdout).Encode(next); err != nil {
					return err
				}
			} else {
				start := commonPrefix(previous.Events, next.Events)
				fmt.Fprintln(stdout)
				render.Digest(stdout, next, start)
			}
			previous, revision = next, nextRevision
		}
	}
}

func continueWork(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("continue", flag.ContinueOnError)
	flags.SetOutput(stderr)
	agent := flags.String("with", "", "agent to launch: claude or codex")
	printOnly := flags.Bool("print", false, "print the continuation prompt instead of launching")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return errors.New("continue accepts at most one HOST/FEED")
	}
	rawLocator := ""
	var err error
	if flags.NArg() == 1 {
		rawLocator = flags.Arg(0)
	} else {
		if !canPrompt(stdin, stdout) {
			return errors.New("continue requires HOST/FEED outside an interactive terminal")
		}
		rawLocator, err = promptForFeed(stdin, stdout, "Continue which context?")
		if err != nil {
			return err
		}
	}
	locator, base, err := resolveLocator(context.Background(), rawLocator)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	digest, _, err := ctxclient.New(30*time.Second).Digest(ctx, base, locator.Feed, "")
	if err != nil {
		return err
	}
	prompt := render.ContinuationPrompt(digest)
	fmt.Fprintf(stderr, "Pinned %s/%s at revision %s\n",
		digest.Manifest.Node, digest.Manifest.Name, digest.Manifest.Revision)
	if *printOnly {
		fmt.Fprintln(stdout, prompt)
		return nil
	}
	selected, err := selectAgent(*agent, digest.Manifest.SourceAgent)
	if err != nil {
		return err
	}
	command := exec.Command(selected, prompt)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func resolveLocator(ctx context.Context, raw string) (ctxclient.Locator, string, error) {
	locator, err := ctxclient.ParseLocator(raw)
	if err != nil {
		return ctxclient.Locator{}, "", err
	}
	base, err := tailnet.ResolveHost(ctx, locator.Host)
	return locator, base, err
}

func selectAgent(requested, sourceAgent string) (string, error) {
	if requested != "" && requested != "claude" && requested != "codex" {
		return "", fmt.Errorf("--with must be claude or codex")
	}
	if requested != "" {
		if _, err := exec.LookPath(requested); err != nil {
			return "", fmt.Errorf("%s is not installed", requested)
		}
		return requested, nil
	}
	preferred := map[string]string{"claude-code": "claude", "codex-cli": "codex"}[sourceAgent]
	if preferred != "" {
		if _, err := exec.LookPath(preferred); err == nil {
			return preferred, nil
		}
	}
	var installed []string
	for _, candidate := range []string{"claude", "codex"} {
		if _, err := exec.LookPath(candidate); err == nil {
			installed = append(installed, candidate)
		}
	}
	if len(installed) == 1 {
		return installed[0], nil
	}
	return "", errors.New("choose a target agent with --with claude or --with codex")
}

func commonPrefix(left, right []protocol.Event) int {
	limit := min(len(left), len(right))
	index := 0
	for index < limit && left[index].ID == right[index].ID && left[index].Text == right[index].Text {
		index++
	}
	return index
}

func feedName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	dash := false
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if valid {
			builder.WriteRune(char)
			dash = false
		} else if !dash && builder.Len() > 0 {
			builder.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func currentUser() string {
	current, err := user.Current()
	if err == nil {
		return current.Username
	}
	return os.Getenv("USER")
}

func displayAgent(value string) string {
	switch value {
	case "claude-code":
		return "Claude"
	case "codex-cli":
		return "Codex"
	default:
		return value
	}
}

func shortSessionID(value string) string {
	const visible = 12
	if len(value) <= visible {
		return value
	}
	return value[:visible]
}

func projectName(cwd string) string {
	if cwd == "" {
		return "-"
	}
	return filepath.Base(filepath.Clean(cwd))
}

func relativeTime(value time.Time) string {
	delta := time.Since(value)
	if delta < time.Minute {
		return fmt.Sprintf("%ds ago", max(0, int(delta.Seconds())))
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	}
	return value.Local().Format("2006-01-02 15:04")
}

func discoverFeeds(ctx context.Context, host string, timeout time.Duration) ([]protocol.FeedSummary, error) {
	var bases []string
	if host != "" {
		base, err := tailnet.ResolveHost(ctx, host)
		if err != nil {
			return nil, err
		}
		bases = []string{base}
	} else {
		var err error
		bases, err = tailnet.OnlinePeerURLs(ctx)
		if err != nil {
			return nil, err
		}
	}

	type result struct {
		feeds []protocol.FeedSummary
	}
	results := make(chan result, len(bases))
	var wait sync.WaitGroup
	for _, base := range bases {
		wait.Add(1)
		go func(base string) {
			defer wait.Done()
			requestCtx, requestCancel := context.WithTimeout(ctx, timeout)
			defer requestCancel()
			feeds, err := ctxclient.New(timeout).List(requestCtx, base)
			if err == nil {
				results <- result{feeds: feeds}
			}
		}(base)
	}
	wait.Wait()
	close(results)

	var feeds []protocol.FeedSummary
	for item := range results {
		feeds = append(feeds, item.feeds...)
	}
	sort.Slice(feeds, func(i, j int) bool {
		if feeds[i].Node == feeds[j].Node {
			return feeds[i].Name < feeds[j].Name
		}
		return feeds[i].Node < feeds[j].Node
	})
	return feeds, nil
}
