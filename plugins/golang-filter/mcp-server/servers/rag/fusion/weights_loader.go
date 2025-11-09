package fusion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WeightSnapshot represents a set of learned fusion weights.
type WeightSnapshot struct {
	Version string             `json:"version"`
	Weights map[string]float64 `json:"weights"`
	Bias    float64            `json:"bias"`
	Raw     map[string]any     `json:"-"`
	Fetched time.Time          `json:"-"`
}

// WeightsLoader fetches and caches weight snapshots from a URI.
type WeightsLoader struct {
	uri       string
	client    *http.Client
	ttl       time.Duration
	mu        sync.RWMutex
	cached    *WeightSnapshot
	lastError error
}

// NewWeightsLoader creates a loader for the given URI.
func NewWeightsLoader(uri string, ttl time.Duration) (*WeightsLoader, error) {
	if uri == "" {
		return nil, errors.New("weights uri is required")
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &WeightsLoader{
		uri: uri,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		ttl: ttl,
	}, nil
}

// Get returns the latest snapshot, reloading if the cache is stale.
func (l *WeightsLoader) Get(ctx context.Context) (*WeightSnapshot, error) {
	l.mu.RLock()
	cached := l.cached
	if cached != nil && time.Since(cached.Fetched) < l.ttl {
		defer l.mu.RUnlock()
		return cached, nil
	}
	l.mu.RUnlock()

	return l.reload(ctx)
}

func (l *WeightsLoader) reload(ctx context.Context) (*WeightSnapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cached != nil && time.Since(l.cached.Fetched) < l.ttl {
		return l.cached, nil
	}

	snapshot, err := l.loadOnce(ctx)
	if err != nil {
		l.lastError = err
		return nil, err
	}

	l.cached = snapshot
	l.lastError = nil
	return snapshot, nil
}

func (l *WeightsLoader) loadOnce(ctx context.Context) (*WeightSnapshot, error) {
	reader, err := l.open(ctx)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read weights: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("weights document is empty")
	}

	var snapshot WeightSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode weights json: %w", err)
	}
	if snapshot.Weights == nil {
		snapshot.Weights = make(map[string]float64)
	}
	snapshot.Raw = make(map[string]any)
	if err := json.Unmarshal(data, &snapshot.Raw); err != nil {
		// Ignore secondary decode errors, keep best-effort raw copy.
		snapshot.Raw = map[string]any{}
	}
	snapshot.Fetched = time.Now()
	return &snapshot, nil
}

func (l *WeightsLoader) open(ctx context.Context) (io.ReadCloser, error) {
	parsed, err := url.Parse(l.uri)
	if err != nil || parsed.Scheme == "" {
		// Treat as local path
		return os.Open(filepath.Clean(l.uri))
	}

	switch strings.ToLower(parsed.Scheme) {
	case "file":
		path := parsed.Path
		if path == "" {
			return nil, errors.New("file uri missing path")
		}
		return os.Open(filepath.Clean(path))
	case "http", "https":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.uri, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		resp, err := l.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch weights: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("fetch weights: unexpected status %d", resp.StatusCode)
		}
		return resp.Body, nil
	default:
		return nil, fmt.Errorf("unsupported weights uri scheme: %s", parsed.Scheme)
	}
}

// LastError returns the last fetch error if any.
func (l *WeightsLoader) LastError() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.lastError
}
