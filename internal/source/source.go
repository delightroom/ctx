package source

import (
	"bufio"
	"bytes"
	"context"
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

	"github.com/delightroom/ctx/internal/preview"
	"github.com/delightroom/ctx/internal/protocol"
	"github.com/delightroom/ctx/internal/redact"
)

const (
	maxEventBytes           = 8 * 1024
	maxDigestBytes          = 96 * 1024
	recentConversationLimit = 8
)

var errUnrecognizedSession = errors.New("unrecognized session")

type Snapshotter interface {
	Snapshot() (protocol.Digest, error)
	Path() string
}

type File struct {
	path  string
	agent string
}

type Session struct {
	SourceAgent string    `json:"source_agent"`
	SessionID   string    `json:"session_id"`
	Project     string    `json:"project"`
	CWD         string    `json:"cwd"`
	Path        string    `json:"path"`
	ModifiedAt  time.Time `json:"modified_at"`
}

func Open(path string) (*File, error) {
	agent, err := Detect(path)
	if err != nil {
		return nil, err
	}
	return &File{path: path, agent: agent}, nil
}

func Discover(cwd string) (*File, error) {
	sessions, err := List(cwd)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no Claude or Codex session found for %s; pass --source explicitly", cwd)
	}
	return &File{path: sessions[0].Path, agent: sessions[0].SourceAgent}, nil
}

// List returns hostable Claude Code and Codex sessions for cwd, newest first.
func List(cwd string) ([]Session, error) {
	return listSessions(cwd, false)
}

// ListAll returns hostable Claude Code and Codex sessions across all workspaces,
// newest first.
func ListAll() ([]Session, error) {
	return listSessions("", true)
}

func listSessions(cwd string, all bool) ([]Session, error) {
	var candidates []candidate
	var providerErrors []error

	var claude []candidate
	var err error
	if all {
		claude, err = allClaudeCandidates()
	} else {
		claude, err = claudeCandidates(cwd)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		providerErrors = append(providerErrors, fmt.Errorf("scan Claude sessions: %w", err))
	} else {
		candidates = append(candidates, claude...)
	}

	var codex []candidate
	if all {
		codex, err = allCodexCandidates()
	} else {
		codex, err = codexCandidates(cwd)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		providerErrors = append(providerErrors, fmt.Errorf("scan Codex sessions: %w", err))
	} else {
		candidates = append(candidates, codex...)
	}

	if len(candidates) == 0 {
		if len(providerErrors) > 0 {
			return nil, errors.Join(providerErrors...)
		}
		return nil, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modified.Equal(candidates[j].modified) {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].modified.After(candidates[j].modified)
	})

	sessions := make([]Session, 0, len(candidates))
	for _, candidate := range candidates {
		session, inspectErr := inspectSession(candidate)
		if inspectErr != nil {
			if errors.Is(inspectErr, errUnrecognizedSession) {
				continue
			}
			if !errors.Is(inspectErr, fs.ErrNotExist) {
				providerErrors = append(providerErrors, inspectErr)
			}
			continue
		}
		if session.CWD == "" && !all {
			session.CWD = cwd
			session.Project = filepath.Base(filepath.Clean(cwd))
		}
		sessions = append(sessions, session)
	}
	if len(sessions) == 0 && len(providerErrors) > 0 {
		return nil, errors.Join(providerErrors...)
	}
	return sessions, nil
}

func (f *File) Path() string { return f.path }

func (f *File) Snapshot() (protocol.Digest, error) {
	return f.SnapshotContext(context.Background())
}

// SnapshotContext is the cancellable form for callers that may stop a long parse.
func (f *File) SnapshotContext(ctx context.Context) (protocol.Digest, error) {
	if err := contextError(ctx); err != nil {
		return protocol.Digest{}, err
	}
	handle, err := os.Open(f.path)
	if err != nil {
		return protocol.Digest{}, err
	}
	defer handle.Close()

	var digest protocol.Digest
	switch f.agent {
	case "claude-code":
		digest, err = parseClaude(ctx, handle)
	case "codex-cli":
		digest, err = parseCodex(ctx, handle)
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
		if err := contextError(ctx); err != nil {
			return protocol.Digest{}, err
		}
		digest.Events[index].Text = redact.String(truncate(digest.Events[index].Text, maxEventBytes))
	}
	digest.Events, digest.Manifest.Truncated = limitEvents(digest.Events, maxDigestBytes)
	digest.Manifest.EventCount = len(digest.Events)
	digest.Manifest.Revision = revision(digest)
	return digest, nil
}

// PreviewContext extracts the small set of display fields in a streaming pass.
// Unlike SnapshotContext, memory use does not grow with the session history.
func (f *File) PreviewContext(ctx context.Context) (preview.Summary, error) {
	return f.PreviewPageContext(ctx, 1)
}

// PreviewPageContext returns one newest-first transcript page while retaining
// only the summary, the newest page, and the requested page in memory.
func (f *File) PreviewPageContext(ctx context.Context, requestedPage int) (preview.Summary, error) {
	return f.PreviewPageContextSized(ctx, requestedPage, preview.DefaultPageSize)
}

// PreviewPageContextSized returns a caller-sized transcript page while
// retaining only bounded display entries in memory.
func (f *File) PreviewPageContextSized(
	ctx context.Context,
	requestedPage int,
	requestedPageSize int,
) (preview.Summary, error) {
	if err := contextError(ctx); err != nil {
		return preview.Summary{}, err
	}
	handle, err := os.Open(f.path)
	if err != nil {
		return preview.Summary{}, err
	}
	defer handle.Close()

	var accumulator preview.Accumulator
	total := 0
	pageSize := preview.EffectivePageSize(requestedPageSize)
	newest := make([]preview.Entry, 0, pageSize)
	newestStart := 0
	err = f.scanPreviewEvents(ctx, handle, func(event protocol.Event) bool {
		accumulator.Add(event)
		entry, ok := preview.EntryFromEvent(event)
		if !ok {
			return true
		}
		total++
		if len(newest) == pageSize {
			newest[newestStart] = entry
			newestStart = (newestStart + 1) % pageSize
		} else {
			newest = append(newest, entry)
		}
		return true
	})
	if err != nil {
		return preview.Summary{}, err
	}

	page, pages, start, end := preview.PageBounds(
		total,
		requestedPage,
		pageSize,
	)
	entries := newest
	if len(newest) == pageSize && newestStart > 0 {
		entries = make([]preview.Entry, 0, pageSize)
		entries = append(entries, newest[newestStart:]...)
		entries = append(entries, newest[:newestStart]...)
	}
	if page > 1 {
		if _, err := handle.Seek(0, io.SeekStart); err != nil {
			return preview.Summary{}, err
		}
		entries = make([]preview.Entry, 0, end-start)
		index := 0
		err = f.scanPreviewEvents(ctx, handle, func(event protocol.Event) bool {
			entry, ok := preview.EntryFromEvent(event)
			if !ok {
				return true
			}
			if index >= start && index < end {
				entries = append(entries, entry)
			}
			index++
			return index < end
		})
	}
	if err != nil {
		return preview.Summary{}, err
	}
	return preview.AttachPage(
		accumulator.Summary(false),
		entries,
		total,
		page,
		pages,
	), nil
}

func (f *File) scanPreviewEvents(
	ctx context.Context,
	reader io.Reader,
	consume func(protocol.Event) bool,
) error {
	switch f.agent {
	case "claude-code":
		_, err := scanClaude(ctx, reader, consume)
		return err
	case "codex-cli":
		_, err := scanCodex(ctx, reader, consume)
		return err
	default:
		return fmt.Errorf("unsupported source agent %q", f.agent)
	}
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
	root, err := claudeProjectsRoot()
	if err != nil {
		return nil, err
	}
	project := filepath.Join(root, nonAlphanumeric.ReplaceAllString(cwd, "-"))
	return candidatesInDirectory(project, "claude-code")
}

func allClaudeCandidates() ([]candidate, error) {
	root, err := claudeProjectsRoot()
	if err != nil {
		return nil, err
	}
	projects, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var result []candidate
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		candidates, readErr := candidatesInDirectory(filepath.Join(root, project.Name()), "claude-code")
		if readErr == nil {
			result = append(result, candidates...)
		}
	}
	return result, nil
}

func claudeProjectsRoot() (string, error) {
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root != "" {
		return filepath.Join(root, "projects"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

func candidatesInDirectory(directory, agent string) ([]candidate, error) {
	entries, err := os.ReadDir(directory)
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
				path:     filepath.Join(directory, entry.Name()),
				agent:    agent,
				modified: info.ModTime(),
			})
		}
	}
	return result, nil
}

func codexCandidates(cwd string) ([]candidate, error) {
	candidates, err := allCodexCandidates()
	if err != nil {
		return nil, err
	}
	var result []candidate
	for _, candidate := range candidates {
		matches, matchErr := codexSessionCWD(candidate.path, cwd)
		if matchErr == nil && matches {
			result = append(result, candidate)
		}
	}
	return result, nil
}

func allCodexCandidates() ([]candidate, error) {
	root, err := codexSessionsRoot()
	if err != nil {
		return nil, err
	}
	var result []candidate
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr == nil {
			result = append(result, candidate{path: path, agent: "codex-cli", modified: info.ModTime()})
		}
		return nil
	})
	return result, err
}

func codexSessionsRoot() (string, error) {
	root := os.Getenv("CODEX_HOME")
	if root != "" {
		return filepath.Join(root, "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

func inspectSession(candidate candidate) (Session, error) {
	session := Session{
		SourceAgent: candidate.agent,
		Path:        candidate.path,
		ModifiedAt:  candidate.modified,
	}
	handle, err := os.Open(candidate.path)
	if err != nil {
		return Session{}, fmt.Errorf("inspect session %s: %w", candidate.path, err)
	}
	defer handle.Close()

	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	recognized := false
	for scanner.Scan() {
		var envelope struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
			Payload   struct {
				ID  string `json:"id"`
				CWD string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil {
			continue
		}
		switch candidate.agent {
		case "claude-code":
			if envelope.SessionID != "" || envelope.Type == "assistant" || envelope.Type == "user" {
				recognized = true
			}
			if envelope.SessionID != "" {
				session.SessionID = envelope.SessionID
			}
			if envelope.CWD != "" {
				session.CWD = envelope.CWD
			}
		case "codex-cli":
			if envelope.Type == "session_meta" {
				recognized = true
				session.SessionID = envelope.Payload.ID
				session.CWD = envelope.Payload.CWD
			}
		}
		if session.SessionID != "" && session.CWD != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return Session{}, fmt.Errorf("inspect session %s: %w", candidate.path, err)
	}
	if !recognized {
		return Session{}, fmt.Errorf("%w: %s", errUnrecognizedSession, candidate.path)
	}
	if session.SessionID == "" {
		session.SessionID = strings.TrimSuffix(filepath.Base(candidate.path), filepath.Ext(candidate.path))
	}
	if session.CWD != "" {
		session.Project = filepath.Base(filepath.Clean(session.CWD))
	}
	return session, nil
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

func parseClaude(ctx context.Context, reader io.Reader) (protocol.Digest, error) {
	var digest protocol.Digest
	manifest, err := scanClaude(ctx, reader, func(event protocol.Event) bool {
		digest.Events = append(digest.Events, event)
		return true
	})
	digest.Manifest = manifest
	return digest, err
}

func scanClaude(
	ctx context.Context,
	reader io.Reader,
	consume func(protocol.Event) bool,
) (protocol.Manifest, error) {
	var manifest protocol.Manifest
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		if err := contextError(ctx); err != nil {
			return protocol.Manifest{}, err
		}
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
			manifest.SessionID = envelope.SessionID
		}
		if envelope.CWD != "" {
			manifest.HostCWD = envelope.CWD
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
		for _, event := range claudeContent(message.Role, envelope.Timestamp, line, message.Content) {
			if !consume(event) {
				return manifest, nil
			}
		}
	}
	return manifest, scanner.Err()
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

func parseCodex(ctx context.Context, reader io.Reader) (protocol.Digest, error) {
	var digest protocol.Digest
	manifest, err := scanCodex(ctx, reader, func(event protocol.Event) bool {
		digest.Events = append(digest.Events, event)
		return true
	})
	digest.Manifest = manifest
	return digest, err
}

func scanCodex(
	ctx context.Context,
	reader io.Reader,
	consume func(protocol.Event) bool,
) (protocol.Manifest, error) {
	var manifest protocol.Manifest
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		if err := contextError(ctx); err != nil {
			return protocol.Manifest{}, err
		}
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
				manifest.SessionID = metadata.ID
				manifest.HostCWD = metadata.CWD
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
					if !consume(protocol.Event{
						ID: fmt.Sprintf("%d-%d", line, index), Timestamp: envelope.Timestamp,
						Role: role, Kind: "message", Text: text,
					}) {
						return manifest, nil
					}
				}
			}
		case "function_call", "custom_tool_call":
			if !consume(protocol.Event{
				ID: fmt.Sprintf("%d-0", line), Timestamp: envelope.Timestamp, Role: "assistant",
				Kind: "tool_call", ToolName: stringValue(item["name"]),
				ToolCallID: stringValue(item["call_id"]), Text: contentText(item["arguments"]),
			}) {
				return manifest, nil
			}
		case "function_call_output", "custom_tool_call_output":
			if !consume(protocol.Event{
				ID: fmt.Sprintf("%d-0", line), Timestamp: envelope.Timestamp, Role: "tool",
				Kind: "tool_result", ToolCallID: stringValue(item["call_id"]),
				Text: contentText(item["output"]),
			}) {
				return manifest, nil
			}
		}
	}
	return manifest, scanner.Err()
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
		size += eventBudgetCost(event)
	}
	if size <= limit {
		return events, false
	}

	markerCost := eventBudgetCost(omissionEvent(len(events)))
	remaining := max(0, limit-markerCost)
	selected := make([]bool, len(events))
	selectEvent := func(index int) bool {
		cost := eventBudgetCost(events[index])
		if selected[index] || cost > remaining {
			return false
		}
		selected[index] = true
		remaining -= cost
		return true
	}

	for index := 0; index < min(4, len(events)); index++ {
		selectEvent(index)
	}

	conversation := 0
	for index := len(events) - 1; index >= 0 && conversation < recentConversationLimit; index-- {
		event := events[index]
		if event.Kind != "message" || event.Role != "user" && event.Role != "assistant" {
			continue
		}
		if selectEvent(index) {
			conversation++
		}
	}

	for index := len(events) - 1; index >= 0; index-- {
		selectEvent(index)
	}

	omitted := 0
	for _, keep := range selected {
		if !keep {
			omitted++
		}
	}

	result := make([]protocol.Event, 0, len(events)-omitted+1)
	seenOmitted := false
	markerAdded := false
	for index, keep := range selected {
		if !keep {
			seenOmitted = true
			continue
		}
		if seenOmitted && !markerAdded {
			result = append(result, omissionEvent(omitted))
			markerAdded = true
		}
		result = append(result, events[index])
	}
	if !markerAdded {
		result = append(result, omissionEvent(omitted))
	}
	return result, true
}

func eventBudgetCost(event protocol.Event) int {
	encoded, err := json.Marshal(event)
	if err != nil {
		return maxDigestBytes
	}
	return len(encoded) + 1
}

func omissionEvent(count int) protocol.Event {
	return protocol.Event{
		ID:   "omitted",
		Role: "system",
		Kind: "omission",
		Text: fmt.Sprintf("[%d events omitted to fit the context budget]", count),
	}
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

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
