package main

import (
    "encoding/json"
    "log"
    "net/http"
    "os"
)

type evalReq struct { Query string `json:"query"`; Context string `json:"context"` }
type evalResp struct { Score float64 `json:"score"`; Verdict string `json:"verdict"` }

func handleEval(w http.ResponseWriter, r *http.Request) {
    var req evalReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, err.Error(), 400); return }
    resp := evalResp{Score: 0.95, Verdict: "correct"}
    if req.Context == "" { resp = evalResp{Score: 0.2, Verdict: "incorrect"} }
    _ = json.NewEncoder(w).Encode(resp)
}

func main() {
    addr := ":8081"
    if v := os.Getenv("EVAL_ADDR"); v != "" { addr = v }
    http.HandleFunc("/eval", handleEval)
    log.Printf("Evaluator mock listening on %s", addr)
    log.Fatal(http.ListenAndServe(addr, nil))
}
