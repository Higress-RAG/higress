package crag

import "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/logger"

// Helper logging functions for the crag package.
// These delegate to the unified logger.

func logInfof(format string, args ...interface{}) {
	logger.Infof(format, args...)
}

func logWarnf(format string, args ...interface{}) {
	logger.Warnf(format, args...)
}

