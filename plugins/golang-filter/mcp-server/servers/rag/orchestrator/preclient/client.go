package preclient

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
    prepb "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/proto/precontract/v1"
)

// Client abstracts the Preprocessor invocation.
type Client interface {
	Generate(ctx context.Context, req *prepb.PreprocessRequest) (*prepb.PreprocessResponse, error)
}

// New returns a preprocessor client based on provider.
func New(provider, endpoint string, httpClient *httpx.Client) (Client, error) {
    switch provider {
    case "http":
        return &HTTPClient{Endpoint: endpoint, Client: httpClient}, nil
    default:
        return nil, fmt.Errorf("unknown preprocessor provider: %s", provider)
    }
}

// HTTPClient calls a HTTP endpoint that accepts/returns protobuf-JSON.
type HTTPClient struct {
	Endpoint string
	Client   *httpx.Client
}

func (c *HTTPClient) Generate(ctx context.Context, req *prepb.PreprocessRequest) (*prepb.PreprocessResponse, error) {
	if c.Endpoint == "" || c.Client == nil {
		return nil, fmt.Errorf("http preprocessor misconfigured")
	}
	bs, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(bs))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pre http status %d", resp.StatusCode)
	}
	var out prepb.PreprocessResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
