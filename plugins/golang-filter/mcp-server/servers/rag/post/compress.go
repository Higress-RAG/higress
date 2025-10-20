package post

import (
    "strings"
)

// CompressText is a simple, query-agnostic compressor placeholder.
// It trims the text to a target ratio of its length, preserving the beginning.
func CompressText(text string, targetRatio float64) string {
    if targetRatio <= 0 || targetRatio >= 1 {
        return text
    }
    // Simple token-ish split to reduce mid-text noise; keep first N tokens.
    tokens := strings.Fields(text)
    if len(tokens) == 0 { return text }
    keep := int(float64(len(tokens)) * targetRatio)
    if keep <= 0 { keep = 1 }
    if keep >= len(tokens) { return text }
    return strings.Join(tokens[:keep], " ")
}
