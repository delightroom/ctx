package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/delightroom/ctx/internal/protocol"
	"github.com/delightroom/ctx/internal/source"
	"github.com/delightroom/ctx/internal/tailnet"
)

type launcherAction struct {
	label string
	run   func() error
}

var errCancelled = errors.New("cancelled")

func launch(stdin io.Reader, stdout, stderr io.Writer) error {
	fmt.Fprintf(stdout, "ctx %s\n\n", Version)

	checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, tailnetErr := tailnet.ReadStatus(checkCtx)
	tailnetReady := tailnetErr == nil && status.Self.DNSName != ""
	if tailnetReady {
		fmt.Fprintf(stdout, "✓ Tailscale connected as %s\n", tailnet.ShortName(status.Self))
	} else if _, err := exec.LookPath("tailscale"); err != nil {
		fmt.Fprintln(stdout, "✗ Tailscale is not installed")
	} else {
		fmt.Fprintln(stdout, "✗ Tailscale is not connected")
	}

	agents := installedAgents()
	if len(agents) == 0 {
		fmt.Fprintln(stdout, "– Claude Code and Codex were not found")
	} else {
		fmt.Fprintf(stdout, "✓ %s detected\n", strings.Join(agents, " and "))
	}

	cwd, cwdErr := os.Getwd()
	var localSession *source.File
	if cwdErr == nil {
		localSession, _ = source.Discover(cwd)
	}
	if localSession != nil {
		snapshot, err := localSession.Snapshot()
		if err == nil {
			fmt.Fprintf(stdout, "✓ Current %s session found for %s\n",
				displayAgent(snapshot.Manifest.SourceAgent), snapshot.Manifest.Project)
		}
	}

	var feeds []protocol.FeedSummary
	if tailnetReady {
		fmt.Fprintln(stdout, "– Discovering shared contexts...")
		discoveryCtx, stopDiscovery := context.WithTimeout(context.Background(), 6*time.Second)
		feeds, _ = discoverFeeds(discoveryCtx, "", 2*time.Second)
		stopDiscovery()
	}

	fmt.Fprintln(stdout)
	if len(feeds) == 0 {
		fmt.Fprintln(stdout, "No shared contexts are currently reachable.")
	} else {
		fmt.Fprintln(stdout, "Available contexts")
		fmt.Fprintln(stdout)
		printFeeds(stdout, feeds)
	}
	fmt.Fprintln(stdout)

	var actions []launcherAction
	if len(feeds) > 0 {
		actions = append(actions,
			launcherAction{
				label: "Continue a context",
				run: func() error {
					locator, err := promptFromFeeds(stdin, stdout, "Continue which context?", feeds)
					if err != nil {
						return err
					}
					return continueWork([]string{locator}, stdin, stdout, stderr)
				},
			},
			launcherAction{
				label: "Tail a context",
				run: func() error {
					locator, err := promptFromFeeds(stdin, stdout, "Tail which context?", feeds)
					if err != nil {
						return err
					}
					return tail([]string{locator}, stdin, stdout, stderr)
				},
			},
		)
	}
	if localSession != nil && tailnetReady {
		actions = append(actions, launcherAction{
			label: "Host this session",
			run: func() error {
				return host(nil, stdout, stderr)
			},
		})
	}
	actions = append(actions,
		launcherAction{
			label: "Diagnose setup",
			run: func() error {
				return doctor(nil, stdout, stderr)
			},
		},
		launcherAction{
			label: "Show command help",
			run: func() error {
				usage(stdout)
				return nil
			},
		},
	)

	fmt.Fprintln(stdout, "What do you want to do?")
	for index, action := range actions {
		fmt.Fprintf(stdout, "  %d. %s\n", index+1, action.label)
	}
	selected, err := promptIndex(stdin, stdout, len(actions))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout)
	return actions[selected].run()
}

func promptForFeed(stdin io.Reader, stdout io.Writer, question string) (string, error) {
	fmt.Fprintln(stdout, "Discovering contexts...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	feeds, err := discoverFeeds(ctx, "", 3*time.Second)
	if err != nil {
		return "", err
	}
	if len(feeds) == 0 {
		return "", errors.New("no reachable ctx feeds found")
	}
	return promptFromFeeds(stdin, stdout, question, feeds)
}

func promptFromFeeds(
	stdin io.Reader,
	stdout io.Writer,
	question string,
	feeds []protocol.FeedSummary,
) (string, error) {
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, question)
	for index, feed := range feeds {
		fmt.Fprintf(stdout, "  %d. %s/%s (%s, %s)\n",
			index+1, feed.Node, feed.Name, displayAgent(feed.SourceAgent), relativeTime(feed.UpdatedAt))
	}
	selected, err := promptIndex(stdin, stdout, len(feeds))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ctx://%s/%s", feeds[selected].Node, feeds[selected].Name), nil
}

func promptIndex(stdin io.Reader, stdout io.Writer, count int) (int, error) {
	if count == 0 {
		return 0, errors.New("nothing to select")
	}
	for {
		fmt.Fprintf(stdout, "Select [1-%d, q]: ", count)
		answer, err := readLine(stdin)
		if err != nil {
			return 0, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "q" || answer == "quit" {
			return 0, errCancelled
		}
		selected, err := strconv.Atoi(answer)
		if err == nil && selected >= 1 && selected <= count {
			return selected - 1, nil
		}
		fmt.Fprintln(stdout, "Enter one of the listed numbers, or q to quit.")
	}
}

func readLine(reader io.Reader) (string, error) {
	var result strings.Builder
	var buffer [1]byte
	for {
		count, err := reader.Read(buffer[:])
		if count > 0 {
			switch buffer[0] {
			case '\n':
				return result.String(), nil
			case '\r':
				continue
			default:
				result.WriteByte(buffer[0])
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && result.Len() > 0 {
				return result.String(), nil
			}
			return "", err
		}
	}
}

func canPrompt(stdin io.Reader, stdout io.Writer) bool {
	return isTerminal(stdin) && isTerminal(stdout) && os.Getenv("CI") == ""
}

func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func installedAgents() []string {
	var agents []string
	if _, err := exec.LookPath("claude"); err == nil {
		agents = append(agents, "Claude Code")
	}
	if _, err := exec.LookPath("codex"); err == nil {
		agents = append(agents, "Codex")
	}
	return agents
}

func printFeeds(writer io.Writer, feeds []protocol.FeedSummary) {
	for index, feed := range feeds {
		fmt.Fprintf(writer, "  %d. %-32s %-8s %s\n",
			index+1,
			feed.Node+"/"+feed.Name,
			displayAgent(feed.SourceAgent),
			relativeTime(feed.UpdatedAt),
		)
	}
}
