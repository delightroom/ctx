package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/delightroom/ctx/internal/protocol"
	"github.com/delightroom/ctx/internal/source"
)

type Config struct {
	Name      string
	Node      string
	Owner     string
	PublicURL string
	Interval  time.Duration
	Logger    *log.Logger
}

type Server struct {
	config      Config
	snapshotter source.Snapshotter
	mu          sync.RWMutex
	digest      protocol.Digest
}

func New(config Config, snapshotter source.Snapshotter) *Server {
	if config.Interval <= 0 {
		config.Interval = 5 * time.Second
	}
	if config.Logger == nil {
		config.Logger = log.Default()
	}
	return &Server{config: config, snapshotter: snapshotter}
}

func (s *Server) Refresh() error {
	digest, err := s.snapshotter.Snapshot()
	if err != nil {
		return err
	}
	digest.Manifest.Name = s.config.Name
	digest.Manifest.Node = s.config.Node
	digest.Manifest.Owner = s.config.Owner

	s.mu.Lock()
	s.digest = digest
	s.mu.Unlock()
	return nil
}

func (s *Server) RunRefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(s.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Refresh(); err != nil {
				s.config.Logger.Printf("snapshot refresh failed: %v", err)
			}
		}
	}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) serveHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		s.logAccess(request)
		writeError(response, http.StatusMethodNotAllowed, "read-only API")
		return
	}

	path := strings.Trim(request.URL.Path, "/")
	switch {
	case path == "v1/feeds":
		s.listFeeds(response, request)
	case path == fmt.Sprintf("v1/feeds/%s/manifest", s.config.Name):
		s.getManifest(response, request)
	case path == fmt.Sprintf("v1/feeds/%s/digest", s.config.Name):
		s.getDigest(response, request)
	default:
		s.logAccess(request)
		writeError(response, http.StatusNotFound, "feed or endpoint not found")
	}
}

func (s *Server) listFeeds(response http.ResponseWriter, request *http.Request) {
	s.logAccess(request)
	digest := s.current()
	writeJSON(response, http.StatusOK, []protocol.FeedSummary{digest.Manifest.Summary()})
}

func (s *Server) getManifest(response http.ResponseWriter, request *http.Request) {
	digest := s.current()
	if unchanged(response, request, digest.Manifest.Revision) {
		return
	}
	s.logAccess(request)
	writeJSON(response, http.StatusOK, digest.Manifest)
}

func (s *Server) getDigest(response http.ResponseWriter, request *http.Request) {
	digest := s.current()
	if unchanged(response, request, digest.Manifest.Revision) {
		return
	}
	s.logAccess(request)
	writeJSON(response, http.StatusOK, digest)
}

func (s *Server) current() protocol.Digest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.digest
}

func (s *Server) logAccess(request *http.Request) {
	identity := request.Header.Get("Tailscale-User-Login")
	if identity == "" {
		identity = request.RemoteAddr
	}
	s.config.Logger.Printf("access identity=%q method=%s path=%s", identity, request.Method, request.URL.Path)
}

func unchanged(response http.ResponseWriter, request *http.Request, revision string) bool {
	etag := `"` + revision + `"`
	response.Header().Set("ETag", etag)
	response.Header().Set("Cache-Control", "private, no-cache")
	if request.Header.Get("If-None-Match") == etag {
		response.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}
