package main

import (
    "encoding/json"
    "log"
    "net/http"
    "os"
)

type inCand struct { ID string `json:"id"`; Text string `json:"text"` }
type rerankReq struct { Query string `json:"query"`; Candidates []inCand `json:"candidates"`; TopN int `json:"top_n"` }
type outItem struct { ID string `json:"id"`; Score float64 `json:"score"` }
type rerankResp struct { Ranking []outItem `json:"ranking"` }

func handleRerank(w http.ResponseWriter, r *http.Request) {
    var req rerankReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, err.Error(), 400); return }
    out := rerankResp{}
    // simple logic: length of text as score; descending
    for _, c := range req.Candidates { out.Ranking = append(out.Ranking, outItem{ID: c.ID, Score: float64(len(c.Text))}) }
    // sort here is skipped for brevity; in real use, implement proper ranking
    _ = json.NewEncoder(w).Encode(out)
}

func main() {
    addr := ":8082"
    if v := os.Getenv("RERANK_ADDR"); v != "" { addr = v }
    http.HandleFunc("/rerank", handleRerank)
    log.Printf("Reranker mock listening on %s", addr)
    log.Fatal(http.ListenAndServe(addr, nil))
}
