package crag

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
)

// HTTPEvaluator calls an external service to evaluate (query, context) relevance.
// Request: {"query":"...","context":"..."}
// Response: {"score":0.85,"verdict":"correct"}
type HTTPEvaluator struct {
    Endpoint   string
    Client     *httpx.Client
    CorrectTh  float64
    IncorrectTh float64
}

type evalReq struct {
    Query   string `json:"query"`
    Context string `json:"context"`
}
type evalResp struct {
    Score   float64 `json:"score"`
    Verdict string  `json:"verdict"`
}

func (h *HTTPEvaluator) Evaluate(ctx context.Context, query string, contextText string) (float64, Verdict, error) {
    if h.Client == nil { h.Client = httpx.NewFromConfig(nil) }
    bs, _ := json.Marshal(evalReq{Query: query, Context: contextText})
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.Endpoint, bytes.NewReader(bs))
    req.Header.Set("Content-Type", "application/json")
    resp, err := h.Client.Do(req)
    if err != nil {
        return 0, VerdictAmbiguous, err
    }
    defer resp.Body.Close()
    var er evalResp
    if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
        return 0, VerdictAmbiguous, err
    }
    v := VerdictAmbiguous
    switch er.Verdict {
    case "correct":
        v = VerdictCorrect
    case "incorrect":
        v = VerdictIncorrect
    }
    return er.Score, v, nil
}
