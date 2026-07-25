package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLocator(t *testing.T) {
	tests := []struct {
		input string
		host  string
		feed  string
	}{
		{"dev-laptop/payments", "dev-laptop", "payments"},
		{"ctx://dev-laptop/payments", "dev-laptop", "payments"},
		{"https://dev-laptop.example.ts.net:8443/payments", "https://dev-laptop.example.ts.net:8443", "payments"},
	}
	for _, test := range tests {
		locator, err := ParseLocator(test.input)
		if err != nil {
			t.Fatalf("ParseLocator(%q): %v", test.input, err)
		}
		if locator.Host != test.host || locator.Feed != test.feed {
			t.Fatalf("ParseLocator(%q) = %+v", test.input, locator)
		}
	}
}

func TestParseLocatorRejectsMissingFeed(t *testing.T) {
	if _, err := ParseLocator("dev-laptop"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseLocatorRejectsUnsupportedScheme(t *testing.T) {
	if _, err := ParseLocator("ftp://dev-laptop/payments"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDigestRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Length", "262145")
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, _, err := New(time.Second).Digest(context.Background(), server.URL, "feed", "")
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("Digest error = %v", err)
	}
}

func TestDigestRejectsTrailingJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"manifest":{},"events":[]} {}`))
	}))
	defer server.Close()

	_, _, err := New(time.Second).Digest(context.Background(), server.URL, "feed", "")
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("Digest error = %v", err)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	_, _, err := New(time.Second).Digest(context.Background(), redirect.URL, "feed", "")
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("Digest error = %v", err)
	}
	if targetRequests.Load() != 0 {
		t.Fatal("client followed a redirect away from the discovered origin")
	}
}
