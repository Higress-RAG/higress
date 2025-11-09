package feedback

import (
	"sync"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/crag"
)

// Trend captures verdict trend statistics.
type Trend struct {
	Total                int
	Incorrect            int
	Ambiguous            int
	Confident            int
	ConsecutiveIncorrect int
	ConsecutiveAmbiguous int
	ConsecutiveConfident int
	LastVerdicts         []crag.Verdict
	LastUpdated          time.Time
}

// VerdictRecord stores a single evaluation outcome.
type VerdictRecord struct {
	Timestamp  time.Time
	Verdict    crag.Verdict
	Confidence float64
}

// Manager tracks verdict feedback per key.
type Manager struct {
	mu         sync.RWMutex
	cfg        config.FeedbackConfig
	history    map[string][]VerdictRecord
	lastAdjust map[string]time.Time
	maxPerKey  int
	defaultKey string
}

// NewManager builds a feedback manager with optional configuration.
func NewManager(cfg *config.FeedbackConfig) *Manager {
	manager := &Manager{
		history:    make(map[string][]VerdictRecord),
		lastAdjust: make(map[string]time.Time),
		defaultKey: "_global",
		maxPerKey:  100,
	}
	if cfg != nil {
		manager.cfg = *cfg
		if cfg.Window > 0 {
			manager.maxPerKey = cfg.Window * 5
		}
	}
	return manager
}

// Record stores a verdict for the given key.
func (m *Manager) Record(key string, verdict crag.Verdict, confidence float64) {
	if key == "" {
		key = m.defaultKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	rec := VerdictRecord{
		Timestamp:  time.Now(),
		Verdict:    verdict,
		Confidence: confidence,
	}
	history := append(m.history[key], rec)
	if len(history) > m.maxPerKey {
		history = history[len(history)-m.maxPerKey:]
	}
	m.history[key] = history
}

// GetTrend computes verdict trend for a key.
func (m *Manager) GetTrend(key string, window int) Trend {
	if key == "" {
		key = m.defaultKey
	}
	m.mu.RLock()
	history := append([]VerdictRecord(nil), m.history[key]...)
	m.mu.RUnlock()

	if window <= 0 {
		window = m.cfg.Window
		if window <= 0 {
			window = 5
		}
	}

	if len(history) == 0 {
		return Trend{}
	}

	if len(history) > window {
		history = history[len(history)-window:]
	}

	trend := Trend{
		Total:        len(history),
		LastVerdicts: make([]crag.Verdict, len(history)),
		LastUpdated:  history[len(history)-1].Timestamp,
	}

	consecutiveIncorrect := 0
	consecutiveAmbiguous := 0
	consecutiveConfident := 0

	for i := len(history) - 1; i >= 0; i-- {
		record := history[i]
		trend.LastVerdicts[i] = record.Verdict
		switch record.Verdict {
		case crag.VerdictIncorrect:
			trend.Incorrect++
			consecutiveIncorrect++
			consecutiveAmbiguous = 0
			consecutiveConfident = 0
		case crag.VerdictAmbiguous:
			trend.Ambiguous++
			consecutiveAmbiguous++
			consecutiveIncorrect = 0
			consecutiveConfident = 0
		case crag.VerdictCorrect:
			trend.Confident++
			consecutiveConfident++
			consecutiveIncorrect = 0
			consecutiveAmbiguous = 0
		default:
			consecutiveIncorrect = 0
			consecutiveAmbiguous = 0
			consecutiveConfident = 0
		}
	}

	trend.ConsecutiveIncorrect = consecutiveIncorrect
	trend.ConsecutiveAmbiguous = consecutiveAmbiguous
	trend.ConsecutiveConfident = consecutiveConfident
	return trend
}

// InCooldown returns true if adjustments are still cooling down.
func (m *Manager) InCooldown(key string, cooldown time.Duration) bool {
	if cooldown <= 0 {
		return false
	}
	if key == "" {
		key = m.defaultKey
	}
	m.mu.RLock()
	last := m.lastAdjust[key]
	m.mu.RUnlock()

	if last.IsZero() {
		return false
	}
	return time.Since(last) < cooldown
}

// MarkAdjustment notes that an adjustment has been applied.
func (m *Manager) MarkAdjustment(key string) {
	if key == "" {
		key = m.defaultKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAdjust[key] = time.Now()
}
