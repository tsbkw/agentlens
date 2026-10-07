package models

import (
	"time"
)

// RawTraceEvent represents an unparsed log record ingested from a file or stream.
type RawTraceEvent struct {
	SourcePath string                 `json:"source_path"`
	LineNumber int                    `json:"line_number"`
	Timestamp  time.Time              `json:"timestamp"`
	RawContent string                 `json:"raw_content"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}
