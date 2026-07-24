package protocol

import "time"

const Version = "1"

type Manifest struct {
	ProtocolVersion string    `json:"protocol_version"`
	Name            string    `json:"name"`
	Owner           string    `json:"owner,omitempty"`
	Node            string    `json:"node"`
	SourceAgent     string    `json:"source_agent"`
	SessionID       string    `json:"session_id"`
	Project         string    `json:"project"`
	HostCWD         string    `json:"host_cwd"`
	Revision        string    `json:"revision"`
	UpdatedAt       time.Time `json:"updated_at"`
	EventCount      int       `json:"event_count"`
	Truncated       bool      `json:"truncated"`
}

type Event struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp,omitempty"`
	Role       string    `json:"role"`
	Kind       string    `json:"kind"`
	Text       string    `json:"text,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
}

type Digest struct {
	Manifest Manifest `json:"manifest"`
	Events   []Event  `json:"events"`
}

type FeedSummary struct {
	Name        string    `json:"name"`
	Owner       string    `json:"owner,omitempty"`
	Node        string    `json:"node"`
	SourceAgent string    `json:"source_agent"`
	Project     string    `json:"project"`
	Revision    string    `json:"revision"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (m Manifest) Summary() FeedSummary {
	return FeedSummary{
		Name:        m.Name,
		Owner:       m.Owner,
		Node:        m.Node,
		SourceAgent: m.SourceAgent,
		Project:     m.Project,
		Revision:    m.Revision,
		UpdatedAt:   m.UpdatedAt,
	}
}
