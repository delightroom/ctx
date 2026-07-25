package preview

import (
	"strings"
	"time"
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

	// DefaultPageSize keeps transcript pages useful in compact terminals while
	// bounding the amount of display text retained by each cached preview.
	DefaultPageSize = 6
	maxPageSize     = 50
)

type Turn struct {
	Role string
	Text string
}

type Entry struct {
	Timestamp time.Time
	Role      string
	Kind      string
	Text      string
}

type Summary struct {
	CurrentRequest  string
	Recent          []Turn
	Tools           []string
	EventCount      int
	UserTurns       int
	AgentTurns      int
	ToolCalls       int
	Truncated       bool
	Entries         []Entry
	TranscriptPage  int
	TranscriptPages int
	TranscriptCount int
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
	return BuildPage(digest, 1, DefaultPageSize)
}

// BuildPage adds one newest-first transcript page to the bounded summary.
func BuildPage(digest protocol.Digest, page, pageSize int) Summary {
	var accumulator Accumulator
	total := 0
	for _, event := range digest.Events {
		accumulator.Add(event)
		if _, ok := EntryFromEvent(event); ok {
			total++
		}
	}
	summary := accumulator.Summary(digest.Manifest.Truncated)
	number, pages, start, end := PageBounds(total, page, pageSize)
	entries := make([]Entry, 0, end-start)
	index := 0
	for _, event := range digest.Events {
		entry, ok := EntryFromEvent(event)
		if !ok {
			continue
		}
		if index >= start && index < end {
			entries = append(entries, entry)
		}
		index++
		if index == end {
			break
		}
	}
	return AttachPage(summary, entries, total, number, pages)
}

// EntryFromEvent converts normalized history into safe transcript metadata.
// Tool payloads and results are deliberately represented without their text.
func EntryFromEvent(event protocol.Event) (Entry, bool) {
	entry := Entry{
		Timestamp: event.Timestamp,
		Kind:      event.Kind,
	}
	switch event.Kind {
	case "message":
		entry.Text = Sanitize(event.Text)
		if entry.Text == "" {
			return Entry{}, false
		}
		switch event.Role {
		case "user":
			entry.Role = "You"
		case "assistant":
			entry.Role = "Agent"
		case "system", "developer":
			entry.Role = "System"
		default:
			entry.Role = "Message"
		}
	case "tool_call":
		entry.Role = "Tool"
		tool := Sanitize(event.ToolName)
		if tool == "" {
			tool = "unknown"
		}
		entry.Text = "Called " + tool
	case "tool_result":
		entry.Role = "Tool"
		entry.Text = "Result returned"
	case "omission":
		entry.Role = "System"
		entry.Text = Sanitize(event.Text)
		if entry.Text == "" {
			entry.Text = "Earlier events omitted"
		}
	default:
		return Entry{}, false
	}
	return entry, true
}

// PageBounds returns a newest-first page and its half-open chronological
// bounds. Page 1 contains the newest entries, ordered oldest-to-newest.
func PageBounds(total, requested, pageSize int) (page, pages, start, end int) {
	pageSize = clampPageSize(pageSize)
	if total <= 0 {
		return 0, 0, 0, 0
	}
	pages = (total + pageSize - 1) / pageSize
	page = requested
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	end = max(0, total-(page-1)*pageSize)
	start = max(0, end-pageSize)
	return page, pages, start, end
}

func AttachPage(
	summary Summary,
	entries []Entry,
	total, page, pages int,
) Summary {
	summary.Entries = append([]Entry(nil), entries...)
	summary.TranscriptCount = total
	summary.TranscriptPage = page
	summary.TranscriptPages = pages
	return summary
}

func clampPageSize(value int) int {
	if value < 1 {
		return DefaultPageSize
	}
	return min(value, maxPageSize)
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
