package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/delightroom/ctx/internal/protocol"
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
		!strings.Contains(stdout.String(), "__fish_seen_subcommand_from host") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
