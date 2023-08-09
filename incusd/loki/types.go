package loki

import (
	"time"
)

// PushRequest models a log stream push.
type PushRequest struct {
	Streams []*Stream `json:"streams"`
}

// LabelSet is a key/value pair mapping of labels.
type LabelSet map[string]string

// Stream represents a log stream. It includes a set of log entries and their labels.
type Stream struct {
	Labels  LabelSet `json:"stream"`
	Entries []Entry  `json:"values"`
}

// Entry represents a log entry. It includes a log message and the time it occurred at.
type Entry struct {
	Timestamp time.Time
	Line      string
}
