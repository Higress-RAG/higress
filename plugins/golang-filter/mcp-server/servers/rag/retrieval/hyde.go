package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
)

// HYDEClient encapsulates HYDE seed generation.
type HYDEClient struct {
	httpClient *http.Client
}

// NewHYDEClient creates a HYDE client with sensible defaults.
func NewHYDEClient() *HYDEClient {
	return &HYDEClient{
		httpClient: &http.Client{
			Timeout: time.Second,
		},
	}
}

// GenerateSeeds produces HYDE seed queries according to the provided configuration.
func (h *HYDEClient) GenerateSeeds(ctx context.Context, cfg config.HYDEConfig, query string) ([]string, error) {
	if !cfg.Enable || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "pre":
		// Pre integration handled upstream.
		return nil, nil
	case "http":
		return h.generateHTTP(ctx, cfg, query)
	default:
		return nil, nil
	}
}

func (h *HYDEClient) generateHTTP(ctx context.Context, cfg config.HYDEConfig, query string) ([]string, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("hyde endpoint required")
	}

	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 150 * time.Millisecond
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	requestBody := map[string]string{"query": query}
	payload, _ := json.Marshal(requestBody)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("hyde unexpected status: " + resp.Status)
	}

	var response struct {
		Seeds []string `json:"seeds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	maxSeeds := cfg.MaxSeeds
	if maxSeeds <= 0 {
		maxSeeds = len(response.Seeds)
	}
	if maxSeeds > 0 && len(response.Seeds) > maxSeeds {
		response.Seeds = response.Seeds[:maxSeeds]
	}
	return response.Seeds, nil
}
