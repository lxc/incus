package logging

import (
	"encoding/json"
	"time"
)

// batch holds pending log streams waiting to be sent to Loki, and it's used
// to reduce the number of push requests to Loki aggregating multiple log streams
// and entries in a single batch request.
type batch struct {
	streams   map[string]*Stream
	bytes     int
	createdAt time.Time
}

func newBatch(entries ...entry) *batch {
	b := &batch{
		streams:   map[string]*Stream{},
		bytes:     0,
		createdAt: time.Now(),
	}

	// Add entries to the batch
	for _, entry := range entries {
		b.add(entry)
	}

	return b
}

// add an entry to the batch.
func (b *batch) add(entry entry) {
	b.bytes += len(entry.Line)

	// Append the entry to an already existing stream (if any)
	labels := entry.labels.String()

	stream, ok := b.streams[labels]
	if ok {
		stream.Entries = append(stream.Entries, entry.Entry)
		return
	}

	// Add the entry as a new stream
	b.streams[labels] = &Stream{
		Labels:  entry.labels,
		Entries: []Entry{entry.Entry},
	}
}

// sizeBytesAfter returns the size of the batch after the input entry
// will be added to the batch itself.
func (b *batch) sizeBytesAfter(entry entry) int {
	return b.bytes + len(entry.Line)
}

// age of the batch since its creation.
func (b *batch) age() time.Duration {
	return time.Since(b.createdAt)
}

// encode the batch as push request, and returns the encoded bytes and the number of encoded
// entries.
func (b *batch) encode() ([]byte, int, error) {
	req, entriesCount := b.createPushRequest()

	buf, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}

	return buf, entriesCount, nil
}

// creates push request and returns it, together with number of entries.
func (b *batch) createPushRequest() (*PushRequest, int) {
	req := PushRequest{
		Streams: make([]*Stream, 0, len(b.streams)),
	}

	entriesCount := 0

	for _, stream := range b.streams {
		req.Streams = append(req.Streams, stream)
		entriesCount += len(stream.Entries)
	}

	return &req, entriesCount
}

// empty returns true if streams is empty.
func (b *batch) empty() bool {
	return len(b.streams) == 0
}
