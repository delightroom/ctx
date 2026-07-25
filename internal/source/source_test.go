package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delightroom/ctx/internal/preview"
	"github.com/delightroom/ctx/internal/protocol"
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

func TestSnapshotContextHonorsCancellation(t *testing.T) {
	path := writeFixture(t, "cancelled.jsonl",
		`{"type":"user","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"user","content":"same"}}`)
	session, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = session.SnapshotContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SnapshotContext error = %v", err)
	}
}

func TestPreviewContextStreamsFullSessionIntoBoundedSummary(t *testing.T) {
	var lines []string
	lines = append(lines,
		`{"type":"user","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"user","content":"first request"}}`,
	)
	for index := range 100 {
		lines = append(lines,
			`{"type":"assistant","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-id","name":"tool-`+
				string(rune('a'+index%26))+`","input":{}}]}}`,
		)
	}
	lines = append(lines,
		`{"type":"user","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"user","content":"current request"}}`,
	)
	path := writeFixture(t, "preview.jsonl", strings.Join(lines, "\n"))
	session, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	summary, err := session.PreviewContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.CurrentRequest != "current request" || summary.EventCount != 102 ||
		summary.ToolCalls != 100 || len(summary.Tools) != 4 || len(summary.Recent) != 2 ||
		summary.TranscriptCount != 102 || len(summary.Entries) != preview.DefaultPageSize {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestPreviewPageContextReadsOlderTranscriptPage(t *testing.T) {
	var lines []string
	for index := range 14 {
		lines = append(lines,
			`{"type":"user","sessionId":"claude-1","cwd":"/work/payments","message":{"role":"user","content":"message-`+
				string(rune('a'+index))+`"}}`,
		)
	}
	path := writeFixture(t, "paged-preview.jsonl", strings.Join(lines, "\n"))
	session, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	page, err := session.PreviewPageContext(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.TranscriptPage != 2 || page.TranscriptPages != 3 ||
		page.TranscriptCount != 14 || len(page.Entries) != preview.DefaultPageSize {
		t.Fatalf("page = %+v", page)
	}
	if page.Entries[0].Text != "message-c" ||
		page.Entries[len(page.Entries)-1].Text != "message-h" {
		t.Fatalf("entries = %+v", page.Entries)
	}
}

func TestLimitEventsPreservesRecentConversationAcrossLargeToolOutput(t *testing.T) {
	events := []protocol.Event{
		{ID: "developer-1", Role: "developer", Kind: "message", Text: "rules"},
		{ID: "developer-2", Role: "developer", Kind: "message", Text: "more rules"},
		{ID: "user-old", Role: "user", Kind: "message", Text: "old request"},
		{ID: "assistant-old", Role: "assistant", Kind: "message", Text: "old reply"},
		{ID: "user-current", Role: "user", Kind: "message", Text: "current request"},
		{ID: "tool-large", Role: "tool", Kind: "tool_result", Text: strings.Repeat("x", 200)},
		{ID: "assistant-current", Role: "assistant", Kind: "message", Text: "current reply"},
		{ID: "tool-latest", Role: "tool", Kind: "tool_result", Text: strings.Repeat("y", 200)},
	}

	limited, truncated := limitEvents(events, 1000)
	if !truncated {
		t.Fatal("events were not truncated")
	}
	var ids []string
	for _, event := range limited {
		ids = append(ids, event.ID)
	}
	joined := strings.Join(ids, ",")
	if !strings.Contains(joined, "user-current") ||
		!strings.Contains(joined, "assistant-current") ||
		!strings.Contains(joined, "omitted") {
		t.Fatalf("limited events = %s", joined)
	}
	if strings.Index(joined, "user-current") > strings.Index(joined, "assistant-current") {
		t.Fatalf("conversation order changed: %s", joined)
	}
}

func TestLimitEventsBudgetsMetadataAndUsesOneOmissionMarker(t *testing.T) {
	events := make([]protocol.Event, 500)
	for index := range events {
		events[index] = protocol.Event{
			ID:         strings.Repeat("i", 40),
			Role:       "assistant",
			Kind:       "tool_call",
			ToolName:   strings.Repeat("tool", 20),
			ToolCallID: strings.Repeat("call", 20),
		}
	}

	const limit = 4 * 1024
	limited, truncated := limitEvents(events, limit)
	if !truncated {
		t.Fatal("metadata-only events were not truncated")
	}
	cost := 0
	omissions := 0
	for _, event := range limited {
		cost += eventBudgetCost(event)
		if event.Kind == "omission" {
			omissions++
		}
	}
	if cost > limit {
		t.Fatalf("limited event cost = %d, want <= %d", cost, limit)
	}
	if omissions != 1 {
		t.Fatalf("omission markers = %d, want 1", omissions)
	}
}

func TestListFindsClaudeAndCodexSessionsForWorkspace(t *testing.T) {
	claudeRoot := filepath.Join(t.TempDir(), ".claude")
	codexRoot := filepath.Join(t.TempDir(), ".codex")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", codexRoot)

	cwd := filepath.Join(t.TempDir(), "payments")
	claudePath := filepath.Join(
		claudeRoot,
		"projects",
		nonAlphanumeric.ReplaceAllString(cwd, "-"),
		"claude-session.jsonl",
	)
	codexPath := filepath.Join(codexRoot, "sessions", "2026", "07", "24", "codex-session.jsonl")
	otherPath := filepath.Join(codexRoot, "sessions", "2026", "07", "23", "other-session.jsonl")

	writeSessionFile(t, claudePath,
		`{"type":"user","sessionId":"claude-1","cwd":"`+cwd+`","message":{"role":"user","content":"hello"}}`)
	writeSessionFile(t, codexPath,
		`{"type":"session_meta","payload":{"id":"codex-1","cwd":"`+cwd+`"}}`)
	writeSessionFile(t, otherPath,
		`{"type":"session_meta","payload":{"id":"codex-other","cwd":"/work/other"}}`)

	now := time.Now()
	if err := os.Chtimes(claudePath, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(codexPath, now, now); err != nil {
		t.Fatal(err)
	}

	sessions, err := List(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want two", sessions)
	}
	if sessions[0].SourceAgent != "codex-cli" || sessions[0].SessionID != "codex-1" {
		t.Fatalf("newest session = %+v", sessions[0])
	}
	if sessions[1].SourceAgent != "claude-code" || sessions[1].SessionID != "claude-1" {
		t.Fatalf("older session = %+v", sessions[1])
	}
	for _, session := range sessions {
		if session.CWD != cwd || session.Project != "payments" {
			t.Fatalf("unexpected workspace metadata: %+v", session)
		}
	}

	all, err := ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all sessions = %+v, want three", all)
	}
}

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	writeSessionFile(t, path, content)
	return path
}

func writeSessionFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
