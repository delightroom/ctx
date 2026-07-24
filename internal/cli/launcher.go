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

	"github.com/charmbracelet/x/term"
	"github.com/delightroom/ctx/internal/protocol"
)

var errCancelled = errors.New("cancelled")

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
	return term.IsTerminal(file.Fd())
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
