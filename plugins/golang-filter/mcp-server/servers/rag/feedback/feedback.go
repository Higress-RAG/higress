package feedback

import (
	"sync"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/crag"
	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

// VerdictRecord represents a single CRAG evaluation result
type VerdictRecord struct {
	Timestamp   time.Time
	Query       string
	QueryType   string
	Verdict     crag.Verdict
	Score       float64
	ProfileUsed string
	WebEnabled  bool
	TopK        int
}

// FeedbackManager manages CRAG feedback and adaptive strategies
type FeedbackManager struct {
	mu              sync.RWMutex
	history         []VerdictRecord
	maxHistorySize  int
	windowSize      int // Number of recent records to consider for adaptation
	incorrectThresh int // Consecutive incorrect verdicts to trigger web search
	ambiguousThresh int // Consecutive ambiguous verdicts to trigger TopK increase
}

// NewFeedbackManager creates a new feedback manager
func NewFeedbackManager() *FeedbackManager {
	return &FeedbackManager{
		history:         make([]VerdictRecord, 0, 100),
		maxHistorySize:  100,
		windowSize:      10,
		incorrectThresh: 3,
		ambiguousThresh: 5,
	}
}

// RecordVerdict records a CRAG evaluation result
func (fm *FeedbackManager) RecordVerdict(record VerdictRecord) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	fm.history = append(fm.history, record)

	// Trim history if it exceeds max size
	if len(fm.history) > fm.maxHistorySize {
		fm.history = fm.history[len(fm.history)-fm.maxHistorySize:]
	}

	api.LogInfof("feedback: recorded verdict=%s score=%.2f query_type=%s",
		record.Verdict.String(), record.Score, record.QueryType)
}

// AdaptProfile adapts retrieval profile based on feedback history
func (fm *FeedbackManager) AdaptProfile(profile config.RetrievalProfile, queryType string) config.RetrievalProfile {
	fm.mu.RLock()
	defer fm.mu.RUnlock()

	if len(fm.history) < 3 {
		// Not enough history, return unchanged
		return profile
	}

	// Get recent history
	recentCount := fm.windowSize
	if len(fm.history) < recentCount {
		recentCount = len(fm.history)
	}
	recent := fm.history[len(fm.history)-recentCount:]

	// Count verdicts in recent window
	incorrectCount := 0
	ambiguousCount := 0
	correctCount := 0
	consecutiveIncorrect := 0
	consecutiveAmbiguous := 0

	for i := len(recent) - 1; i >= 0; i-- {
		record := recent[i]
		switch record.Verdict {
		case crag.VerdictIncorrect:
			incorrectCount++
			consecutiveIncorrect++
			consecutiveAmbiguous = 0
		case crag.VerdictAmbiguous:
			ambiguousCount++
			consecutiveAmbiguous++
			consecutiveIncorrect = 0
		case crag.VerdictCorrect:
			correctCount++
			consecutiveIncorrect = 0
			consecutiveAmbiguous = 0
		}
	}

	adaptations := []string{}

	// Strategy 1: Enable web search after consecutive incorrect verdicts
	if consecutiveIncorrect >= fm.incorrectThresh && !profile.UseWeb {
		profile.UseWeb = true
		if !contains(profile.Retrievers, "web") {
			profile.Retrievers = append(profile.Retrievers, "web")
		}
		adaptations = append(adaptations, "enabled_web_search")
		api.LogInfof("feedback: enabled web search due to %d consecutive incorrect verdicts", consecutiveIncorrect)
	}

	// Strategy 2: Increase TopK after consecutive ambiguous verdicts
	if consecutiveAmbiguous >= fm.ambiguousThresh {
		oldTopK := profile.TopK
		profile.TopK = int(float64(profile.TopK) * 1.5)
		if profile.TopK > 30 {
			profile.TopK = 30 // Cap at 30
		}
		if profile.TopK != oldTopK {
			adaptations = append(adaptations, "increased_topk")
			api.LogInfof("feedback: increased TopK from %d to %d due to %d consecutive ambiguous verdicts",
				oldTopK, profile.TopK, consecutiveAmbiguous)
		}
	}

	// Strategy 3: Enable BM25 if incorrect rate is high but not extreme
	incorrectRate := float64(incorrectCount) / float64(recentCount)
	if incorrectRate > 0.3 && incorrectRate < 0.7 && !contains(profile.Retrievers, "bm25") {
		profile.Retrievers = append(profile.Retrievers, "bm25")
		adaptations = append(adaptations, "enabled_bm25")
		api.LogInfof("feedback: enabled BM25 due to incorrect rate %.2f", incorrectRate)
	}

	// Strategy 4: Lower threshold if ambiguous rate is high
	ambiguousRate := float64(ambiguousCount) / float64(recentCount)
	if ambiguousRate > 0.5 && profile.Threshold > 0.3 {
		oldThreshold := profile.Threshold
		profile.Threshold *= 0.8
		if profile.Threshold < 0.3 {
			profile.Threshold = 0.3 // Floor at 0.3
		}
		if profile.Threshold != oldThreshold {
			adaptations = append(adaptations, "lowered_threshold")
			api.LogInfof("feedback: lowered threshold from %.2f to %.2f due to ambiguous rate %.2f",
				oldThreshold, profile.Threshold, ambiguousRate)
		}
	}

	// Strategy 5: Query-type specific adaptations
	if queryType != "" {
		typeRecords := filterByQueryType(recent, queryType)
		if len(typeRecords) >= 3 {
			typeIncorrectRate := float64(countVerdict(typeRecords, crag.VerdictIncorrect)) / float64(len(typeRecords))
			if typeIncorrectRate > 0.6 {
				// This query type consistently fails, need stronger retrieval
				if !profile.UseWeb {
					profile.UseWeb = true
					if !contains(profile.Retrievers, "web") {
						profile.Retrievers = append(profile.Retrievers, "web")
					}
					adaptations = append(adaptations, "type_specific_web")
					api.LogInfof("feedback: enabled web search for query type %s (fail rate %.2f)", queryType, typeIncorrectRate)
				}
			}
		}
	}

	if len(adaptations) > 0 {
		api.LogInfof("feedback: applied adaptations: %v", adaptations)
	}

	return profile
}

// GetStatistics returns feedback statistics for monitoring
func (fm *FeedbackManager) GetStatistics() map[string]interface{} {
	fm.mu.RLock()
	defer fm.mu.RUnlock()

	if len(fm.history) == 0 {
		return map[string]interface{}{
			"total_count": 0,
		}
	}

	correctCount := 0
	ambiguousCount := 0
	incorrectCount := 0

	for _, record := range fm.history {
		switch record.Verdict {
		case crag.VerdictCorrect:
			correctCount++
		case crag.VerdictAmbiguous:
			ambiguousCount++
		case crag.VerdictIncorrect:
			incorrectCount++
		}
	}

	total := len(fm.history)
	return map[string]interface{}{
		"total_count":       total,
		"correct_count":     correctCount,
		"ambiguous_count":   ambiguousCount,
		"incorrect_count":   incorrectCount,
		"correct_rate":      float64(correctCount) / float64(total),
		"ambiguous_rate":    float64(ambiguousCount) / float64(total),
		"incorrect_rate":    float64(incorrectCount) / float64(total),
		"window_size":       fm.windowSize,
		"max_history_size":  fm.maxHistorySize,
	}
}

// Reset clears feedback history
func (fm *FeedbackManager) Reset() {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	fm.history = make([]VerdictRecord, 0, 100)
	api.LogInfo("feedback: history reset")
}

// Helper functions

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func filterByQueryType(records []VerdictRecord, queryType string) []VerdictRecord {
	result := make([]VerdictRecord, 0)
	for _, r := range records {
		if r.QueryType == queryType {
			result = append(result, r)
		}
	}
	return result
}

func countVerdict(records []VerdictRecord, verdict crag.Verdict) int {
	count := 0
	for _, r := range records {
		if r.Verdict == verdict {
			count++
		}
	}
	return count
}

