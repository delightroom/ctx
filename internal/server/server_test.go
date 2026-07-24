package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/delightroom/ctx/internal/protocol"
)

type fixedSnapshotter struct {
	digest protocol.Digest
}

func (f fixedSnapshotter) Snapshot() (protocol.Digest, error) { return f.digest, nil }
func (f fixedSnapshotter) Path() string                       { return "fixture.jsonl" }

func TestReadOnlyAPIAndETag(t *testing.T) {
	var logs bytes.Buffer
	service := New(Config{
		Name: "payments", Node: "dev-laptop", Owner: "developer",
		Logger: log.New(&logs, "", 0),
	}, fixedSnapshotter{digest: protocol.Digest{
		Manifest: protocol.Manifest{
			ProtocolVersion: protocol.Version,
			SourceAgent:     "claude-code",
			SessionID:       "session-1",
			Project:         "payments",
			Revision:        "abc123",
			UpdatedAt:       time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
		},
		Events: []protocol.Event{{ID: "1", Role: "user", Kind: "message", Text: "debug it"}},
	}})
	if err := service.Refresh(); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/v1/feeds/payments/digest")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("ETag") != `"abc123"` {
		t.Fatalf("status=%d etag=%q", response.StatusCode, response.Header.Get("ETag"))
	}
	var digest protocol.Digest
	if err := json.NewDecoder(response.Body).Decode(&digest); err != nil {
		t.Fatal(err)
	}
	if digest.Manifest.Node != "dev-laptop" || digest.Manifest.Name != "payments" {
		t.Fatalf("unexpected manifest: %+v", digest.Manifest)
	}

	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/v1/feeds/payments/digest", nil)
	request.Header.Set("If-None-Match", `"abc123"`)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotModified {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}

	request, _ = http.NewRequest(http.MethodPost, httpServer.URL+"/v1/feeds", nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", response.StatusCode)
	}
}
