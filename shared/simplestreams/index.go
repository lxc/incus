package simplestreams

// Stream represents the base structure of index.json.
type Stream struct {
	Index   map[string]StreamIndex `json:"index"`
	Updated string                 `json:"updated,omitempty"`
	Format  string                 `json:"format"`
}

// StreamIndex represents the Index entry inside index.json.
type StreamIndex struct {
	DataType string   `json:"datatype"`
	Path     string   `json:"path"`
	Updated  string   `json:"updated,omitempty"`
	Products []string `json:"products"`
	Format   string   `json:"format,omitempty"`
}
