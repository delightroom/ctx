package preview

import (
	"strings"
	"testing"

	"github.com/delightroom/ctx/internal/protocol"
)

func TestBuildExtractsCurrentRequestActivityAndRecentTurns(t *testing.T) {
	digest := protocol.Digest{
		Manifest: protocol.Manifest{Truncated: true},
		Events: []protocol.Event{
			{Role: "user", Kind: "message", Text: "Investigate the queue"},
			{Role: "assistant", Kind: "message", Text: "I found the worker."},
			{Role: "assistant", Kind: "tool_call", ToolName: "Bash"},
			{Role: "tool", Kind: "tool_result", Text: "done"},
			{Role: "user", Kind: "message", Text: "Patch it"},
			{Role: "assistant", Kind: "tool_call", ToolName: "Edit"},
			{Role: "assistant", Kind: "tool_call", ToolName: "Bash"},
			{Role: "assistant", Kind: "message", Text: "The tests pass."},
			{Role: "user", Kind: "message", Text: "Create the PR"},
		},
	}

	result := Build(digest)

	if result.CurrentRequest != "Create the PR" {
		t.Fatalf("current request = %q", result.CurrentRequest)
	}
	if result.EventCount != 9 || result.UserTurns != 3 || result.AgentTurns != 2 ||
		result.ToolCalls != 3 || !result.Truncated {
		t.Fatalf("activity = %+v", result)
	}
	if strings.Join(result.Tools, ",") != "Bash,Edit" {
		t.Fatalf("tools = %v", result.Tools)
	}
	if len(result.Recent) != 4 ||
		result.Recent[0].Text != "I found the worker." ||
		result.Recent[3].Text != "Create the PR" {
		t.Fatalf("recent = %+v", result.Recent)
	}
}

func TestBuildSanitizesAndRedactsDisplayedText(t *testing.T) {
	result := Build(protocol.Digest{Events: []protocol.Event{{
		Role: "user",
		Kind: "message",
		Text: "deploy\n\033[31msecret=very-sensitive-value\tsoon",
	}}})

	if strings.Contains(result.CurrentRequest, "\n") ||
		strings.Contains(result.CurrentRequest, "\033") ||
		strings.Contains(result.CurrentRequest, "very-sensitive-value") {
		t.Fatalf("unsafe current request = %q", result.CurrentRequest)
	}
	if result.CurrentRequest != "deploy secret=[REDACTED] soon" {
		t.Fatalf("current request = %q", result.CurrentRequest)
	}
}

func TestBuildIgnoresToolResultsMasqueradingAsUserTurns(t *testing.T) {
	result := Build(protocol.Digest{Events: []protocol.Event{
		{Role: "user", Kind: "message", Text: "Run the checks"},
		{Role: "user", Kind: "tool_result", Text: "all green"},
	}})

	if result.UserTurns != 1 || result.CurrentRequest != "Run the checks" ||
		len(result.Recent) != 1 {
		t.Fatalf("summary = %+v", result)
	}
}

func TestAccumulatorBoundsRecentTurnsAndTools(t *testing.T) {
	var accumulator Accumulator
	for index := range 100 {
		accumulator.Add(protocol.Event{
			Role:     "assistant",
			Kind:     "tool_call",
			ToolName: "tool-" + string(rune('a'+index%26)),
		})
		accumulator.Add(protocol.Event{
			Role: "user",
			Kind: "message",
			Text: "request",
		})
	}

	result := accumulator.Summary(false)
	if len(result.Tools) != toolLimit || len(result.Recent) != recentLimit {
		t.Fatalf("unbounded summary: tools=%d recent=%d", len(result.Tools), len(result.Recent))
	}
	if result.EventCount != 200 || result.ToolCalls != 100 || result.UserTurns != 100 {
		t.Fatalf("counts = %+v", result)
	}
}

func TestBuildPagePaginatesTranscriptNewestFirst(t *testing.T) {
	var events []protocol.Event
	for index := range 14 {
		events = append(events, protocol.Event{
			Role: "user",
			Kind: "message",
			Text: "message-" + string(rune('a'+index)),
		})
	}

	newest := BuildPage(protocol.Digest{Events: events}, 1, 6)
	if newest.TranscriptPage != 1 || newest.TranscriptPages != 3 ||
		newest.TranscriptCount != 14 || len(newest.Entries) != 6 {
		t.Fatalf("newest page = %+v", newest)
	}
	if newest.Entries[0].Text != "message-i" ||
		newest.Entries[5].Text != "message-n" {
		t.Fatalf("newest entries = %+v", newest.Entries)
	}

	oldest := BuildPage(protocol.Digest{Events: events}, 99, 6)
	if oldest.TranscriptPage != 3 || len(oldest.Entries) != 2 ||
		oldest.Entries[0].Text != "message-a" ||
		oldest.Entries[1].Text != "message-b" {
		t.Fatalf("oldest page = %+v", oldest)
	}
}

func TestEffectivePageSizeStaysBounded(t *testing.T) {
	if got := EffectivePageSize(0); got != DefaultPageSize {
		t.Fatalf("zero page size = %d, want %d", got, DefaultPageSize)
	}
	if got := EffectivePageSize(1_000); got != maxPageSize {
		t.Fatalf("large page size = %d, want %d", got, maxPageSize)
	}
}

func TestTranscriptEntriesHideToolPayloadsAndResults(t *testing.T) {
	result := BuildPage(protocol.Digest{Events: []protocol.Event{
		{
			Role:     "assistant",
			Kind:     "tool_call",
			ToolName: "Bash",
			Text:     `{"command":"echo api_key=very-sensitive-value"}`,
		},
		{
			Role: "tool",
			Kind: "tool_result",
			Text: "api_key=another-sensitive-value",
		},
	}}, 1, 6)

	if len(result.Entries) != 2 ||
		result.Entries[0].Text != "Called Bash" ||
		result.Entries[1].Text != "Result returned" {
		t.Fatalf("entries = %+v", result.Entries)
	}
	combined := result.Entries[0].Text + result.Entries[1].Text
	if strings.Contains(combined, "sensitive") || strings.Contains(combined, "echo") {
		t.Fatalf("tool payload leaked into transcript: %q", combined)
	}
}

func TestPageBoundsHandlesEmptyAndInvalidRequests(t *testing.T) {
	if page, pages, start, end := PageBounds(0, 1, 6); page != 0 || pages != 0 || start != 0 || end != 0 {
		t.Fatalf("empty bounds = %d/%d [%d:%d]", page, pages, start, end)
	}
	if page, pages, start, end := PageBounds(7, 0, 0); page != 1 || pages != 2 || start != 1 || end != 7 {
		t.Fatalf("default bounds = %d/%d [%d:%d]", page, pages, start, end)
	}
}
