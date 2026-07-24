package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/delightroom/ctx/internal/protocol"
)

var ErrNotModified = errors.New("not modified")

type Client struct {
	HTTP *http.Client
}

func New(timeout time.Duration) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout}}
}

func (c *Client) List(ctx context.Context, baseURL string) ([]protocol.FeedSummary, error) {
	var feeds []protocol.FeedSummary
	_, err := c.get(ctx, strings.TrimRight(baseURL, "/")+"/v1/feeds", "", &feeds)
	return feeds, err
}

func (c *Client) Manifest(ctx context.Context, baseURL, feed string) (protocol.Manifest, error) {
	var manifest protocol.Manifest
	path := fmt.Sprintf("%s/v1/feeds/%s/manifest", strings.TrimRight(baseURL, "/"), url.PathEscape(feed))
	_, err := c.get(ctx, path, "", &manifest)
	return manifest, err
}

func (c *Client) Digest(ctx context.Context, baseURL, feed, revision string) (protocol.Digest, string, error) {
	var digest protocol.Digest
	path := fmt.Sprintf("%s/v1/feeds/%s/digest", strings.TrimRight(baseURL, "/"), url.PathEscape(feed))
	etag, err := c.get(ctx, path, revision, &digest)
	return digest, strings.Trim(etag, `"`), err
}

func (c *Client) get(ctx context.Context, endpoint, revision string, target any) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	if revision != "" {
		request.Header.Set("If-None-Match", `"`+revision+`"`)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return response.Header.Get("ETag"), ErrNotModified
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024))
		return "", fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return "", err
	}
	return response.Header.Get("ETag"), nil
}

type Locator struct {
	Host string
	Feed string
}

func ParseLocator(value string) (Locator, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return Locator{}, err
		}
		feed := strings.Trim(parsed.Path, "/")
		if parsed.Host == "" || feed == "" || strings.Contains(feed, "/") {
			return Locator{}, fmt.Errorf("expected locator shaped like ctx://host/feed or https://host/feed")
		}
		if parsed.Scheme == "ctx" {
			return Locator{Host: parsed.Host, Feed: feed}, nil
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return Locator{}, fmt.Errorf("unsupported context locator scheme %q", parsed.Scheme)
		}
		return Locator{Host: parsed.Scheme + "://" + parsed.Host, Feed: feed}, nil
	}
	host, feed, ok := strings.Cut(value, "/")
	if !ok || host == "" || feed == "" || strings.Contains(feed, "/") {
		return Locator{}, fmt.Errorf("expected context locator shaped like host/feed")
	}
	return Locator{Host: host, Feed: feed}, nil
}
