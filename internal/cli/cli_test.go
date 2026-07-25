package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delightroom/ctx/internal/preview"
	"github.com/delightroom/ctx/internal/protocol"
	"github.com/delightroom/ctx/internal/tailnet"
	ctxtui "github.com/delightroom/ctx/internal/tui"
)

func TestFeedName(t *testing.T) {
	if got := feedName(" Payments Debug / July "); got != "payments-debug-july" {
		t.Fatalf("feedName = %q", got)
	}
}

func TestCommonPrefix(t *testing.T) {
	left := []protocol.Event{{ID: "1", Text: "a"}, {ID: "2", Text: "b"}}
	right := []protocol.Event{{ID: "1", Text: "a"}, {ID: "2", Text: "changed"}}
	if got := commonPrefix(left, right); got != 1 {
		t.Fatalf("commonPrefix = %d", got)
	}
}

func TestRunWithoutTerminalPrintsHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run(nil, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ctx — tailnet-native AI context sharing") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDevNullDoesNotLaunchTUI(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	t.Setenv("CI", "")

	if isTerminal(devNull) {
		t.Fatal("/dev/null was incorrectly detected as a terminal")
	}
	if code := Run(nil, devNull, devNull, devNull); code != 0 {
		t.Fatalf("Run exit code = %d", code)
	}
}

func TestExplicitTUIRequiresTerminalAndProvidesHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"tui"}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("Run exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "tui requires an interactive terminal") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"tui", "--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ctx tui — interactive context dashboard") ||
		!strings.Contains(stdout.String(), "Toggle workspace/all local sessions") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestTUIActionHandoff(t *testing.T) {
	var selected ctxtui.Result
	var command string
	var commandArgs []string
	dependencies := tuiDependencies{
		runProgram: func(config ctxtui.Config, _ io.Reader, _ io.Writer) (ctxtui.Result, error) {
			if config.Loader == nil || config.Version != Version {
				t.Fatalf("TUI config = %+v", config)
			}
			if _, ok := config.Context.Deadline(); ok {
				t.Fatal("TUI context unexpectedly has a deadline")
			}
			return selected, nil
		},
		host: func(args []string, _, _ io.Writer) error {
			command = "host"
			commandArgs = append([]string(nil), args...)
			return nil
		},
		tail: func(args []string, _ io.Reader, _, _ io.Writer) error {
			command = "tail"
			commandArgs = append([]string(nil), args...)
			return nil
		},
		continueWork: func(args []string, _ io.Reader, _, _ io.Writer) error {
			command = "continue"
			commandArgs = append([]string(nil), args...)
			return nil
		},
	}

	tests := []struct {
		name       string
		result     ctxtui.Result
		want       string
		wantArgs   []string
		wantCalled bool
	}{
		{
			name:       "host",
			result:     ctxtui.Result{Action: ctxtui.ActionHost, SourcePath: "/tmp/session.jsonl"},
			want:       "host",
			wantArgs:   []string{"--source", "/tmp/session.jsonl"},
			wantCalled: true,
		},
		{
			name:       "tail",
			result:     ctxtui.Result{Action: ctxtui.ActionTail, Locator: "ctx://node/feed"},
			want:       "tail",
			wantArgs:   []string{"ctx://node/feed"},
			wantCalled: true,
		},
		{
			name:       "follow",
			result:     ctxtui.Result{Action: ctxtui.ActionTail, Locator: "ctx://node/feed", Follow: true},
			want:       "tail",
			wantArgs:   []string{"--follow", "ctx://node/feed"},
			wantCalled: true,
		},
		{
			name:       "continue",
			result:     ctxtui.Result{Action: ctxtui.ActionContinue, Locator: "ctx://node/feed"},
			want:       "continue",
			wantArgs:   []string{"ctx://node/feed"},
			wantCalled: true,
		},
		{name: "quit", result: ctxtui.Result{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected = test.result
			command = ""
			commandArgs = nil
			if err := runTUIWith(dependencies, strings.NewReader(""), io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			if test.wantCalled && command != test.want {
				t.Fatalf("command = %q, want %q", command, test.want)
			}
			if !test.wantCalled && command != "" {
				t.Fatalf("unexpected command = %q", command)
			}
			if strings.Join(commandArgs, "\x00") != strings.Join(test.wantArgs, "\x00") {
				t.Fatalf("args = %q, want %q", commandArgs, test.wantArgs)
			}
		})
	}
}

func TestTUILoaderHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&cliTUILoader{}).LoadLocal(ctx, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadLocal error = %v", err)
	}
}

func TestTUILoaderBuildsAndCachesLocalPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	initial := strings.Join([]string{
		`{"type":"user","sessionId":"claude-1","cwd":"/work/ctx","message":{"role":"user","content":"Improve the session peek"}}`,
		`{"type":"assistant","sessionId":"claude-1","cwd":"/work/ctx","message":{"role":"assistant","content":"I will inspect secret=very-sensitive-value"}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(initial+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := ctxtui.LocalSession{Path: path, ModifiedAt: time.Now()}
	loader := &cliTUILoader{}

	first, err := loader.LoadLocalPreview(context.Background(), session, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.CurrentRequest != "Improve the session peek" ||
		len(first.Recent) != 2 ||
		strings.Contains(first.Recent[1].Text, "very-sensitive-value") {
		t.Fatalf("preview = %+v", first)
	}

	replacement := `{"type":"user","sessionId":"claude-1","cwd":"/work/ctx","message":{"role":"user","content":"This should require a new inventory revision"}}`
	if err := os.WriteFile(path, []byte(replacement+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := loader.LoadLocalPreview(context.Background(), session, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.CurrentRequest == first.CurrentRequest ||
		second.CurrentRequest != "This should require a new inventory revision" {
		t.Fatalf("stale preview after source changed: first=%q second=%q",
			first.CurrentRequest, second.CurrentRequest)
	}
}

func TestTUILoaderLoadsSharedPreviewFromDiscoveredOrigin(t *testing.T) {
	const revision = "abc123"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/v1/feeds/payments/digest" {
			http.NotFound(response, request)
			return
		}
		events := make([]protocol.Event, 14)
		for index := range events {
			events[index] = protocol.Event{
				Role: "user",
				Kind: "message",
				Text: fmt.Sprintf("Review the payment retry %d", index),
			}
		}
		response.Header().Set("ETag", `"`+revision+`"`)
		_ = json.NewEncoder(response).Encode(protocol.Digest{
			Manifest: protocol.Manifest{
				ProtocolVersion: protocol.Version,
				Name:            "payments",
				Node:            "provider",
				Revision:        revision,
			},
			Events: events,
		})
	}))
	defer server.Close()

	loader := &cliTUILoader{
		statusRead: func(context.Context) (tailnet.Status, error) {
			t.Fatal("shared preview reran tailscale status")
			return tailnet.Status{}, nil
		},
	}
	summary, err := loader.LoadSharedPreview(context.Background(), ctxtui.SharedContext{
		Name:     "payments",
		Node:     "provider",
		Revision: revision,
		BaseURL:  server.URL,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CurrentRequest != "Review the payment retry 13" ||
		summary.TranscriptPages != 3 || summary.TranscriptPage != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	older, err := loader.LoadSharedPreview(context.Background(), ctxtui.SharedContext{
		Name:     "payments",
		Node:     "provider",
		Revision: revision,
		BaseURL:  server.URL,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if older.TranscriptPage != 2 || older.Entries[0].Text != "Review the payment retry 2" {
		t.Fatalf("older page = %+v", older)
	}
	if requests.Load() != 1 {
		t.Fatalf("digest requests = %d, want one cached request", requests.Load())
	}
}

func TestTUILoaderRejectsChangedSharedPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("ETag", `"new-revision"`)
		_ = json.NewEncoder(response).Encode(protocol.Digest{
			Manifest: protocol.Manifest{
				ProtocolVersion: protocol.Version,
				Name:            "payments",
				Node:            "provider",
				Revision:        "new-revision",
			},
		})
	}))
	defer server.Close()

	_, err := (&cliTUILoader{}).LoadSharedPreview(
		context.Background(),
		ctxtui.SharedContext{
			Name:     "payments",
			Node:     "provider",
			Revision: "discovered-revision",
			BaseURL:  server.URL,
		},
		1,
	)
	if err == nil || !strings.Contains(err.Error(), "changed since discovery") {
		t.Fatalf("LoadSharedPreview error = %v", err)
	}
}

func TestTUILoaderBoundsPreviewCache(t *testing.T) {
	loader := &cliTUILoader{}
	for index := range maxPreviewCacheEntries + 10 {
		key := strconv.Itoa(index)
		_, err := loader.loadPreview(context.Background(), key, func() (preview.Summary, error) {
			return preview.Summary{CurrentRequest: key}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(loader.previews) != maxPreviewCacheEntries ||
		len(loader.previewKeys) != maxPreviewCacheEntries {
		t.Fatalf("cache sizes = %d/%d", len(loader.previews), len(loader.previewKeys))
	}
	if _, exists := loader.previews["0"]; exists {
		t.Fatal("oldest preview was not evicted")
	}
}

func TestAnimationsEnabledCanBeDisabled(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CTX_NO_ANIMATION", "")
	if !animationsEnabled() {
		t.Fatal("animation unexpectedly disabled")
	}
	t.Setenv("CTX_NO_ANIMATION", "1")
	if animationsEnabled() {
		t.Fatal("CTX_NO_ANIMATION did not disable animation")
	}
}

func TestTUILoaderCoalescesConcurrentStatusReads(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	loader := &cliTUILoader{
		statusRead: func(context.Context) (tailnet.Status, error) {
			calls.Add(1)
			started <- struct{}{}
			<-release
			return tailnet.Status{}, nil
		},
	}

	done := make(chan error, 2)
	go func() {
		_, err := loader.readTailnetStatus(context.Background())
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first status read did not start")
	}
	go func() {
		_, err := loader.readTailnetStatus(context.Background())
		done <- err
	}()
	select {
	case <-started:
		t.Fatal("concurrent status read was not coalesced")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("coalesced status read did not finish")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("tailscale status calls = %d, want 1", calls.Load())
	}
}

func TestFeedDiscoveryBoundsConcurrency(t *testing.T) {
	bases := make([]string, maxConcurrentHostProbes*3)
	for index := range bases {
		bases[index] = "https://peer-" + strconv.Itoa(index)
	}

	var active atomic.Int32
	var peak atomic.Int32
	started := make(chan struct{}, len(bases))
	release := make(chan struct{})
	done := make(chan []protocol.FeedSummary, 1)

	go func() {
		done <- probeFeedBases(
			context.Background(),
			bases,
			time.Second,
			func(ctx context.Context, base string) ([]protocol.FeedSummary, error) {
				current := active.Add(1)
				defer active.Add(-1)
				for {
					previous := peak.Load()
					if current <= previous || peak.CompareAndSwap(previous, current) {
						break
					}
				}
				started <- struct{}{}
				select {
				case <-release:
					return []protocol.FeedSummary{{Name: base}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		)
	}()

	for range maxConcurrentHostProbes {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("bounded workers did not start")
		}
	}
	select {
	case <-started:
		t.Fatalf("more than %d peer probes started concurrently", maxConcurrentHostProbes)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	select {
	case feeds := <-done:
		if len(feeds) != len(bases) {
			t.Fatalf("discovered feeds = %d, want %d", len(feeds), len(bases))
		}
		for _, feed := range feeds {
			if feed.BaseURL != feed.Name {
				t.Fatalf("feed origin = %q, want %q", feed.BaseURL, feed.Name)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("bounded peer discovery did not finish")
	}
	if peak.Load() != maxConcurrentHostProbes {
		t.Fatalf("peak concurrency = %d, want %d", peak.Load(), maxConcurrentHostProbes)
	}
}

func TestFeedDiscoveryStopsBeforeProbingCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	feeds := probeFeedBases(
		ctx,
		[]string{"https://peer-1", "https://peer-2"},
		time.Second,
		func(context.Context, string) ([]protocol.FeedSummary, error) {
			calls.Add(1)
			return nil, nil
		},
	)
	if len(feeds) != 0 || calls.Load() != 0 {
		t.Fatalf("cancelled discovery returned %d feeds after %d probes", len(feeds), calls.Load())
	}
}

func TestTailRequiresLocatorOutsideTerminal(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"tail"}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("Run exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "requires HOST/FEED outside an interactive terminal") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestPromptIndexRetriesInvalidSelection(t *testing.T) {
	var stdout bytes.Buffer
	selected, err := promptIndex(strings.NewReader("wrong\n3\n"), &stdout, 3)
	if err != nil {
		t.Fatal(err)
	}
	if selected != 2 {
		t.Fatalf("selected = %d", selected)
	}
	if !strings.Contains(stdout.String(), "Enter one of the listed numbers") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDoctorJSON(t *testing.T) {
	t.Setenv("CTX_INSTALL_METHOD", "test")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"doctor", "--json"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"name":"Installation"`) ||
		!strings.Contains(stdout.String(), `"detail":"dev (test)"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestHostListJSON(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), ".claude"))
	codexRoot := filepath.Join(t.TempDir(), ".codex")
	t.Setenv("CODEX_HOME", codexRoot)

	sessionPath := filepath.Join(codexRoot, "sessions", "2026", "07", "24", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"session_meta","payload":{"id":"codex-list-1","cwd":` +
		strconv.Quote(workspace) + `}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"host", "ls", "--json"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"source_agent":"codex-cli"`) ||
		!strings.Contains(stdout.String(), `"session_id":"codex-list-1"`) ||
		!strings.Contains(stdout.String(), `"cwd":`+strconv.Quote(workspace)) {
		t.Fatalf("stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"host", "ls"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "AGENT") ||
		!strings.Contains(stdout.String(), "Codex") ||
		!strings.Contains(stdout.String(), "codex-list-1") ||
		!strings.Contains(stdout.String(), sessionPath) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestHostListHelpExitsSuccessfully(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"host", "ls", "--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage of host ls:") ||
		strings.Contains(stderr.String(), "ctx: flag: help requested") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCompletion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"completion", "fish"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "complete -c ctx") ||
		!strings.Contains(stdout.String(), "__fish_seen_subcommand_from host") ||
		!strings.Contains(stdout.String(), `"tui host ls tail`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
