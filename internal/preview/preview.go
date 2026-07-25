package preview

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/delightroom/ctx/internal/protocol"
	"github.com/delightroom/ctx/internal/redact"
)

const (
	maxTextRunes = 640
	recentLimit  = 4
	toolLimit    = 4
)

type Turn struct {
	Role string
	Text string
}

type Summary struct {
	CurrentRequest string
	Recent         []Turn
	Tools          []string
	EventCount     int
	UserTurns      int
	AgentTurns     int
	ToolCalls      int
	Truncated      bool
}

// Accumulator creates a preview while retaining only the fields the UI can
// display. It is safe to use while streaming an arbitrarily long session.
type Accumulator struct {
	summary Summary
}

func (a *Accumulator) Add(event protocol.Event) {
	a.summary.EventCount++
	switch event.Kind {
	case "tool_call":
		a.summary.ToolCalls++
		tool := Sanitize(event.ToolName)
		if tool == "" {
			return
		}
		for index, existing := range a.summary.Tools {
			if existing == tool {
				copy(a.summary.Tools[1:index+1], a.summary.Tools[:index])
				a.summary.Tools[0] = tool
				return
			}
		}
		if len(a.summary.Tools) < toolLimit {
			a.summary.Tools = append(a.summary.Tools, "")
		}
		copy(a.summary.Tools[1:], a.summary.Tools[:len(a.summary.Tools)-1])
		a.summary.Tools[0] = tool
	case "message":
		text := Sanitize(event.Text)
		if text == "" {
			return
		}
		var turn Turn
		switch event.Role {
		case "user":
			a.summary.UserTurns++
			a.summary.CurrentRequest = text
			turn = Turn{Role: "You", Text: text}
		case "assistant":
			a.summary.AgentTurns++
			turn = Turn{Role: "Agent", Text: text}
		default:
			return
		}
		if len(a.summary.Recent) == recentLimit {
			copy(a.summary.Recent, a.summary.Recent[1:])
			a.summary.Recent[len(a.summary.Recent)-1] = turn
		} else {
			a.summary.Recent = append(a.summary.Recent, turn)
		}
	}
}

func (a *Accumulator) Summary(truncated bool) Summary {
	result := a.summary
	result.Recent = append([]Turn(nil), result.Recent...)
	result.Tools = append([]string(nil), result.Tools...)
	result.Truncated = truncated
	return result
}

// Build creates a small, deterministic description of a normalized digest.
// It deliberately uses only extractive signals and never calls an LLM.
func Build(digest protocol.Digest) Summary {
	var accumulator Accumulator
	for _, event := range digest.Events {
		accumulator.Add(event)
	}
	return accumulator.Summary(digest.Manifest.Truncated)
}

// Sanitize prepares untrusted session-derived text for one-line terminal use.
func Sanitize(value string) string {
	value = redact.String(ansi.Strip(value))
	var builder strings.Builder
	builder.Grow(min(len(value), maxTextRunes))
	space := false
	runes := 0
	truncated := false

	for _, current := range value {
		if !unicode.IsPrint(current) || unicode.IsSpace(current) {
			space = builder.Len() > 0
			continue
		}
		if space {
			builder.WriteByte(' ')
			space = false
		}
		if runes == maxTextRunes {
			truncated = true
			break
		}
		builder.WriteRune(current)
		runes++
	}

	result := strings.TrimSpace(builder.String())
	if truncated && utf8.RuneCountInString(result) == maxTextRunes {
		result += "…"
	}
	return result
}
