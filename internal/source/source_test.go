package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSnapshot(t *testing.T) {
	path := writeFixture(t, "claude.jsonl", strings.Join([]string{
		`{"type":"user","timestamp":"2026-07-24T01:00:00Z","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"user","content":"debug payments"}}`,
		`{"type":"assistant","timestamp":"2026-07-24T01:00:01Z","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"assistant","content":[{"type":"text","text":"I will inspect it."},{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"echo api_key=super-secret-value"}}]}}`,
		`{"type":"user","timestamp":"2026-07-24T01:00:02Z","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"done"}]}}`,
	}, "\n"))

	session, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := session.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if digest.Manifest.SourceAgent != "claude-code" {
		t.Fatalf("source agent = %q", digest.Manifest.SourceAgent)
	}
	if digest.Manifest.SessionID != "claude-1" || digest.Manifest.Project != "payments" {
		t.Fatalf("unexpected manifest: %+v", digest.Manifest)
	}
	if len(digest.Events) != 4 {
		t.Fatalf("event count = %d, want 4", len(digest.Events))
	}
	if strings.Contains(digest.Events[2].Text, "super-secret-value") {
		t.Fatalf("tool input was not redacted: %s", digest.Events[2].Text)
	}
	if digest.Manifest.Revision == "" {
		t.Fatal("revision is empty")
	}
}

func TestCodexSnapshot(t *testing.T) {
	path := writeFixture(t, "codex.jsonl", strings.Join([]string{
		`{"timestamp":"2026-07-24T01:00:00Z","type":"session_meta","payload":{"id":"codex-1","cwd":"/work/payments"}}`,
		`{"timestamp":"2026-07-24T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"debug payments"}]}}`,
		`{"timestamp":"2026-07-24T01:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"call-1","arguments":"{\"cmd\":\"pwd\"}"}}`,
		`{"timestamp":"2026-07-24T01:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-1","output":"/work/payments"}}`,
	}, "\n"))

	session, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := session.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if digest.Manifest.SourceAgent != "codex-cli" || digest.Manifest.SessionID != "codex-1" {
		t.Fatalf("unexpected manifest: %+v", digest.Manifest)
	}
	if len(digest.Events) != 3 {
		t.Fatalf("event count = %d, want 3", len(digest.Events))
	}
	if digest.Events[1].ToolCallID != "call-1" || digest.Events[2].ToolCallID != "call-1" {
		t.Fatalf("tool linkage not preserved: %+v", digest.Events)
	}
}

func TestRevisionIsStableForSameContent(t *testing.T) {
	path := writeFixture(t, "stable.jsonl",
		`{"type":"user","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"user","content":"same"}}`)
	session, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest.Revision != second.Manifest.Revision {
		t.Fatalf("revision changed: %s != %s", first.Manifest.Revision, second.Manifest.Revision)
	}
}

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
