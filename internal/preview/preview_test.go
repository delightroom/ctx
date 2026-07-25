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
