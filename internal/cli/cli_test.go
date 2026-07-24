package cli

import (
	"bytes"
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

func TestCompletion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"completion", "fish"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "complete -c ctx") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
