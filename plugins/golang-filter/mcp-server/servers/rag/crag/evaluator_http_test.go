package crag

import (
    "encoding/json"
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestHTTPEvaluator_Evaluate(t *testing.T) {
    // mock evaluator service
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req struct{ Query, Context string }
        _ = json.NewDecoder(r.Body).Decode(&req)
        resp := map[string]interface{}{"score": 0.95, "verdict": "correct"}
        if req.Context == "" { resp = map[string]interface{}{"score": 0.2, "verdict": "incorrect"} }
        _ = json.NewEncoder(w).Encode(resp)
    }))
    defer srv.Close()

    ev := &HTTPEvaluator{Endpoint: srv.URL}
    score, verdict, err := ev.Evaluate(context.Background(), "q", "ctx")
    if err != nil { t.Fatalf("eval error: %v", err) }
    if verdict != VerdictCorrect || score <= 0.9 { t.Fatalf("unexpected: score=%v verdict=%v", score, verdict) }
}
