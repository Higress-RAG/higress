package metrics

import (
    "sync"
    "time"

    "github.com/prometheus/client_golang/prometheus"
)

var (
    once sync.Once

    retrieverLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "rag_retriever_latency_ms",
        Help:    "Latency of retriever calls in milliseconds",
        Buckets: []float64{10, 25, 50, 75, 100, 150, 200, 300, 500, 800, 1200},
    }, []string{"type"})

    retrieverResults = prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "rag_retriever_results",
        Help:    "Number of results returned by a retriever",
        Buckets: []float64{0, 1, 2, 5, 10, 20, 50, 100},
    }, []string{"type"})

    fusionLists = prometheus.NewHistogram(prometheus.HistogramOpts{
        Name:    "rag_fusion_input_lists",
        Help:    "Number of lists fused per query",
        Buckets: []float64{0, 1, 2, 3, 4, 5, 8, 12},
    })

    cragVerdict = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "rag_crag_verdict_total",
        Help: "CRAG verdict count",
    }, []string{"verdict"})

    gatingDecision = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "rag_gating_decision_total",
        Help: "Gating decisions (suppress/force/neutral)",
    }, []string{"decision"})

    vectorPreflightTop1 = prometheus.NewHistogram(prometheus.HistogramOpts{
        Name:    "rag_vector_preflight_top1",
        Help:    "Vector preflight Top1 score distribution",
        Buckets: []float64{0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.85, 0.9, 0.95, 0.99, 1.0},
    })
)

func ensureRegistered() {
    once.Do(func() {
        prometheus.MustRegister(retrieverLatency, retrieverResults, fusionLists, cragVerdict, gatingDecision, vectorPreflightTop1)
    })
}

// ObserveRetriever records latency and result size for a retriever type.
func ObserveRetriever(typ string, start time.Time, results int) {
    ensureRegistered()
    dur := time.Since(start).Milliseconds()
    retrieverLatency.WithLabelValues(typ).Observe(float64(dur))
    retrieverResults.WithLabelValues(typ).Observe(float64(results))
}

// ObserveFusion records how many lists were fused.
func ObserveFusion(n int) {
    ensureRegistered()
    fusionLists.Observe(float64(n))
}

// IncCRAGVerdict increments verdict counter.
func IncCRAGVerdict(v string) {
    ensureRegistered()
    cragVerdict.WithLabelValues(v).Inc()
}

// IncGating records a gating decision.
func IncGating(decision string) {
    ensureRegistered()
    gatingDecision.WithLabelValues(decision).Inc()
}

// ObserveVectorTop1 records the preflight vector top1 score.
func ObserveVectorTop1(score float64) {
    ensureRegistered()
    if score >= 0 { vectorPreflightTop1.Observe(score) }
}

// Collectors exposes all collectors for external registration with a custom registry.
func Collectors() []prometheus.Collector {
    // ensure vectors exist; don't auto-register here to let caller decide
    _ = retrieverLatency
    _ = retrieverResults
    _ = fusionLists
    _ = cragVerdict
    _ = gatingDecision
    _ = vectorPreflightTop1
    return []prometheus.Collector{
        retrieverLatency, retrieverResults, fusionLists, cragVerdict, gatingDecision, vectorPreflightTop1,
    }
}
