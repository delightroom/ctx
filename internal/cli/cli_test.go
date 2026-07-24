package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/delightroom/ctx/internal/protocol"
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

func TestBareInteractiveRunLaunchesTUI(t *testing.T) {
	terminal, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	t.Setenv("CI", "")

	original := launchInteractiveTUI
	called := false
	launchInteractiveTUI = func(io.Reader, io.Writer, io.Writer) error {
		called = true
		return nil
	}
	t.Cleanup(func() {
		launchInteractiveTUI = original
	})

	if code := Run(nil, terminal, terminal, terminal); code != 0 {
		t.Fatalf("Run exit code = %d", code)
	}
	if !called {
		t.Fatal("bare interactive ctx did not launch the TUI")
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
	originalProgram := runTUIProgram
	originalHost := runTUIHost
	originalTail := runTUITail
	originalContinue := runTUIContinue
	t.Cleanup(func() {
		runTUIProgram = originalProgram
		runTUIHost = originalHost
		runTUITail = originalTail
		runTUIContinue = originalContinue
	})

	var selected ctxtui.Result
	runTUIProgram = func(config ctxtui.Config, _ io.Reader, _ io.Writer) (ctxtui.Result, error) {
		if config.Loader == nil || config.Version != Version {
			t.Fatalf("TUI config = %+v", config)
		}
		if _, ok := config.Context.Deadline(); ok {
			t.Fatal("TUI context unexpectedly has a deadline")
		}
		return selected, nil
	}

	var command string
	var commandArgs []string
	runTUIHost = func(args []string, _, _ io.Writer) error {
		command = "host"
		commandArgs = append([]string(nil), args...)
		return nil
	}
	runTUITail = func(args []string, _ io.Reader, _, _ io.Writer) error {
		command = "tail"
		commandArgs = append([]string(nil), args...)
		return nil
	}
	runTUIContinue = func(args []string, _ io.Reader, _, _ io.Writer) error {
		command = "continue"
		commandArgs = append([]string(nil), args...)
		return nil
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
			if err := runTUI(strings.NewReader(""), io.Discard, io.Discard); err != nil {
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
	_, err := (cliTUILoader{}).LoadLocal(ctx, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadLocal error = %v", err)
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
