package logger

import (
	"fmt"

	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

// Logger provides a unified logging interface for the RAG system.
// It gracefully handles both Envoy environment and standalone testing.

// LogLevel represents log severity levels
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

var (
	// CurrentLevel is the current logging level (default: Info)
	CurrentLevel = LevelInfo

	// UseEnvoyAPI controls whether to use Envoy API for logging
	// Set to false in tests to use fmt.Printf
	UseEnvoyAPI = true
)

// Debugf logs a debug message
func Debugf(format string, args ...interface{}) {
	if CurrentLevel > LevelDebug {
		return
	}
	logf(LevelDebug, format, args...)
}

// Infof logs an info message
func Infof(format string, args ...interface{}) {
	if CurrentLevel > LevelInfo {
		return
	}
	logf(LevelInfo, format, args...)
}

// Warnf logs a warning message
func Warnf(format string, args ...interface{}) {
	if CurrentLevel > LevelWarn {
		return
	}
	logf(LevelWarn, format, args...)
}

// Errorf logs an error message
func Errorf(format string, args ...interface{}) {
	logf(LevelError, format, args...)
}

// logf is the internal logging function
func logf(level LogLevel, format string, args ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Silently ignore panics from Envoy API in tests
			// Fallback to fmt.Printf
			fallbackLog(level, format, args...)
		}
	}()

	if UseEnvoyAPI {
		// Try to use Envoy API
		switch level {
		case LevelDebug:
			api.LogDebugf(format, args...)
		case LevelInfo:
			api.LogInfof(format, args...)
		case LevelWarn:
			api.LogWarnf(format, args...)
		case LevelError:
			api.LogErrorf(format, args...)
		}
	} else {
		// Use standard output
		fallbackLog(level, format, args...)
	}
}

// fallbackLog uses fmt.Printf when Envoy API is not available
func fallbackLog(level LogLevel, format string, args ...interface{}) {
	prefix := levelPrefix(level)
	fmt.Printf(prefix+format+"\n", args...)
}

// levelPrefix returns the prefix for each log level
func levelPrefix(level LogLevel) string {
	switch level {
	case LevelDebug:
		return "[DEBUG] "
	case LevelInfo:
		return "[INFO] "
	case LevelWarn:
		return "[WARN] "
	case LevelError:
		return "[ERROR] "
	default:
		return "[LOG] "
	}
}

// SetLevel sets the minimum log level
func SetLevel(level LogLevel) {
	CurrentLevel = level
}

// DisableEnvoyAPI disables Envoy API logging (useful for tests)
func DisableEnvoyAPI() {
	UseEnvoyAPI = false
}

// EnableEnvoyAPI enables Envoy API logging (default)
func EnableEnvoyAPI() {
	UseEnvoyAPI = true
}

// With returns a logger with additional context (placeholder for future enhancement)
type ContextLogger struct {
	context map[string]interface{}
}

// WithContext creates a new logger with context
func WithContext(context map[string]interface{}) *ContextLogger {
	return &ContextLogger{context: context}
}

// Infof logs with context
func (c *ContextLogger) Infof(format string, args ...interface{}) {
	// For now, just delegate to package-level function
	// Future: could prepend context to log message
	Infof(format, args...)
}

// Warnf logs with context
func (c *ContextLogger) Warnf(format string, args ...interface{}) {
	Warnf(format, args...)
}

// Errorf logs with context
func (c *ContextLogger) Errorf(format string, args ...interface{}) {
	Errorf(format, args...)
}
