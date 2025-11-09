package crag

import "github.com/envoyproxy/envoy/contrib/golang/common/go/api"

// Helper logging functions that only log when api is available.
// These are used throughout the crag package to avoid panics in unit tests.

func logInfof(format string, args ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Silently ignore logging errors in tests
		}
	}()
	api.LogInfof(format, args...)
}

func logWarnf(format string, args ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Silently ignore logging errors in tests
		}
	}()
	api.LogWarnf(format, args...)
}

