package source

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/delightroom/ctx/internal/protocol"
	"github.com/delightroom/ctx/internal/redact"
)

const (
	maxEventBytes  = 8 * 1024
	maxDigestBytes = 96 * 1024
)

type Snapshotter interface {
	Snapshot() (protocol.Digest, error)
	Path() string
}

type File struct {
	path  string
	agent string
}

func Open(path string) (*File, error) {
	agent, err := Detect(path)
	if err != nil {
		return nil, err
	}
	return &File{path: path, agent: agent}, nil
}

func Discover(cwd string) (*File, error) {
	var candidates []candidate

	claude, err := claudeCandidates(cwd)
	if err == nil {
		candidates = append(candidates, claude...)
	}
	codex, err := codexCandidates(cwd)
	if err == nil {
		candidates = append(candidates, codex...)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no Claude or Codex session found for %s; pass --source explicitly", cwd)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modified.After(candidates[j].modified)
	})
	return &File{path: candidates[0].path, agent: candidates[0].agent}, nil
}

func (f *File) Path() string { return f.path }

func (f *File) Snapshot() (protocol.Digest, error) {
	handle, err := os.Open(f.path)
	if err != nil {
		return protocol.Digest{}, err
	}
	defer handle.Close()

	var digest protocol.Digest
	switch f.agent {
	case "claude-code":
		digest, err = parseClaude(handle)
	case "codex-cli":
		digest, err = parseCodex(handle)
	default:
		err = fmt.Errorf("unsupported source agent %q", f.agent)
	}
	if err != nil {
		return protocol.Digest{}, err
	}
	digest.Manifest.ProtocolVersion = protocol.Version
	digest.Manifest.SourceAgent = f.agent
	digest.Manifest.UpdatedAt = time.Now().UTC()
	if digest.Manifest.Project == "" && digest.Manifest.HostCWD != "" {
		digest.Manifest.Project = filepath.Base(digest.Manifest.HostCWD)
	}

	for index := range digest.Events {
		digest.Events[index].Text = redact.String(truncate(digest.Events[index].Text, maxEventBytes))
	}
	digest.Events, digest.Manifest.Truncated = limitEvents(digest.Events, maxDigestBytes)
	digest.Manifest.EventCount = len(digest.Events)
	digest.Manifest.Revision = revision(digest)
	return digest, nil
}

func Detect(path string) (string, error) {
	handle, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer handle.Close()

	scanner := bufio.NewScanner(handle)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		var envelope struct {
			Type      string          `json:"type"`
			SessionID string          `json:"sessionId"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil {
			continue
		}
		if envelope.Type == "session_meta" {
			return "codex-cli", nil
		}
		if envelope.SessionID != "" || envelope.Type == "assistant" || envelope.Type == "user" {
			return "claude-code", nil
		}
	}
	return "", errors.New("could not detect a Claude or Codex JSONL session")
}

type candidate struct {
	path     string
	agent    string
	modified time.Time
}

var nonAlphanumeric = regexp.MustCompile(`[^A-Za-z0-9]`)

func claudeCandidates(cwd string) ([]candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root == "" {
		root = filepath.Join(home, ".claude")
	}
	project := filepath.Join(root, "projects", nonAlphanumeric.ReplaceAllString(cwd, "-"))
	entries, err := os.ReadDir(project)
	if err != nil {
		return nil, err
	}
	var result []candidate
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			result = append(result, candidate{
				path:     filepath.Join(project, entry.Name()),
				agent:    "claude-code",
				modified: info.ModTime(),
			})
		}
	}
	return result, nil
}

func codexCandidates(cwd string) ([]candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		root = filepath.Join(home, ".codex")
	}
	root = filepath.Join(root, "sessions")
	var result []candidate
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		matches, err := codexSessionCWD(path, cwd)
		if err == nil && matches {
			result = append(result, candidate{path: path, agent: "codex-cli", modified: info.ModTime()})
		}
		return nil
	})
	return result, err
}

func codexSessionCWD(path, cwd string) (bool, error) {
	handle, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer handle.Close()
	scanner := bufio.NewScanner(io.LimitReader(handle, 256*1024))
	for scanner.Scan() {
		var envelope struct {
			Type    string `json:"type"`
			Payload struct {
				CWD string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) == nil && envelope.Type == "session_meta" {
			return filepath.Clean(envelope.Payload.CWD) == filepath.Clean(cwd), nil
		}
	}
	return false, scanner.Err()
}

func parseClaude(reader io.Reader) (protocol.Digest, error) {
	var digest protocol.Digest
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var envelope struct {
			Type      string          `json:"type"`
			Timestamp time.Time       `json:"timestamp"`
			SessionID string          `json:"sessionId"`
			CWD       string          `json:"cwd"`
			Message   json.RawMessage `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil {
			continue
		}
		if envelope.SessionID != "" {
			digest.Manifest.SessionID = envelope.SessionID
		}
		if envelope.CWD != "" {
			digest.Manifest.HostCWD = envelope.CWD
		}
		if envelope.Type != "user" && envelope.Type != "assistant" {
			continue
		}
		var message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(envelope.Message, &message) != nil {
			continue
		}
		digest.Events = append(digest.Events, claudeContent(message.Role, envelope.Timestamp, line, message.Content)...)
	}
	return digest, scanner.Err()
}

func claudeContent(role string, timestamp time.Time, line int, content json.RawMessage) []protocol.Event {
	var plain string
	if json.Unmarshal(content, &plain) == nil {
		return []protocol.Event{{
			ID: fmt.Sprintf("%d-0", line), Timestamp: timestamp, Role: role, Kind: "message", Text: plain,
		}}
	}
	var blocks []map[string]any
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var events []protocol.Event
	for index, block := range blocks {
		event := protocol.Event{
			ID: fmt.Sprintf("%d-%d", line, index), Timestamp: timestamp, Role: role,
		}
		switch stringValue(block["type"]) {
		case "text":
			event.Kind = "message"
			event.Text = stringValue(block["text"])
		case "tool_use":
			event.Kind = "tool_call"
			event.ToolName = stringValue(block["name"])
			event.ToolCallID = stringValue(block["id"])
			event.Text = compactJSON(block["input"])
		case "tool_result":
			event.Kind = "tool_result"
			event.ToolCallID = stringValue(block["tool_use_id"])
			event.Text = contentText(block["content"])
		default:
			continue
		}
		if event.Text != "" || event.ToolName != "" {
			events = append(events, event)
		}
	}
	return events
}

func parseCodex(reader io.Reader) (protocol.Digest, error) {
	var digest protocol.Digest
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var envelope struct {
			Timestamp time.Time       `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil {
			continue
		}
		if envelope.Type == "session_meta" {
			var metadata struct {
				ID  string `json:"id"`
				CWD string `json:"cwd"`
			}
			if json.Unmarshal(envelope.Payload, &metadata) == nil {
				digest.Manifest.SessionID = metadata.ID
				digest.Manifest.HostCWD = metadata.CWD
			}
			continue
		}
		if envelope.Type != "response_item" {
			continue
		}
		var item map[string]any
		if json.Unmarshal(envelope.Payload, &item) != nil {
			continue
		}
		kind := stringValue(item["type"])
		switch kind {
		case "message":
			role := stringValue(item["role"])
			blocks, _ := item["content"].([]any)
			for index, rawBlock := range blocks {
				block, _ := rawBlock.(map[string]any)
				text := stringValue(block["text"])
				if text == "" {
					text = stringValue(block["input_text"])
				}
				if text == "" {
					text = stringValue(block["output_text"])
				}
				if text != "" {
					digest.Events = append(digest.Events, protocol.Event{
						ID: fmt.Sprintf("%d-%d", line, index), Timestamp: envelope.Timestamp,
						Role: role, Kind: "message", Text: text,
					})
				}
			}
		case "function_call", "custom_tool_call":
			digest.Events = append(digest.Events, protocol.Event{
				ID: fmt.Sprintf("%d-0", line), Timestamp: envelope.Timestamp, Role: "assistant",
				Kind: "tool_call", ToolName: stringValue(item["name"]),
				ToolCallID: stringValue(item["call_id"]), Text: contentText(item["arguments"]),
			})
		case "function_call_output", "custom_tool_call_output":
			digest.Events = append(digest.Events, protocol.Event{
				ID: fmt.Sprintf("%d-0", line), Timestamp: envelope.Timestamp, Role: "tool",
				Kind: "tool_result", ToolCallID: stringValue(item["call_id"]),
				Text: contentText(item["output"]),
			})
		}
	}
	return digest, scanner.Err()
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func contentText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			if block, ok := item.(map[string]any); ok {
				if text := stringValue(block["text"]); text != "" {
					parts = append(parts, text)
				}
			} else if text, ok := item.(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return compactJSON(value)
	}
}

func compactJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var buffer bytes.Buffer
	if json.Compact(&buffer, data) == nil {
		return buffer.String()
	}
	return string(data)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[truncated]"
}

func limitEvents(events []protocol.Event, limit int) ([]protocol.Event, bool) {
	size := 0
	for _, event := range events {
		size += len(event.Text)
	}
	if size <= limit {
		return events, false
	}

	headCount := min(4, len(events))
	result := append([]protocol.Event(nil), events[:headCount]...)
	remaining := limit
	for _, event := range result {
		remaining -= len(event.Text)
	}
	var tail []protocol.Event
	for index := len(events) - 1; index >= headCount; index-- {
		if len(events[index].Text) > remaining {
			break
		}
		tail = append(tail, events[index])
		remaining -= len(events[index].Text)
	}
	for left, right := 0, len(tail)-1; left < right; left, right = left+1, right-1 {
		tail[left], tail[right] = tail[right], tail[left]
	}
	omitted := len(events) - len(result) - len(tail)
	result = append(result, protocol.Event{
		ID: "omitted", Role: "system", Kind: "omission",
		Text: fmt.Sprintf("[%d earlier events omitted to fit the context budget]", omitted),
	})
	result = append(result, tail...)
	return result, true
}

func revision(digest protocol.Digest) string {
	payload, _ := json.Marshal(struct {
		SourceAgent string           `json:"source_agent"`
		SessionID   string           `json:"session_id"`
		Events      []protocol.Event `json:"events"`
	}{
		SourceAgent: digest.Manifest.SourceAgent,
		SessionID:   digest.Manifest.SessionID,
		Events:      digest.Events,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:12]
}
