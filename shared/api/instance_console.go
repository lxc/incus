package api

// InstanceConsoleControl represents a message on the instance console "control" socket.
//
// API extension: instances.
type InstanceConsoleControl struct {
	Command string            `json:"command" yaml:"command"`
	Args    map[string]string `json:"args" yaml:"args"`
}

// InstanceConsolePost represents an instance console request.
//
// swagger:model
//
// API extension: instances.
type InstanceConsolePost struct {
	// Console width in columns (console type only)
	// Example: 80
	Width int `json:"width" yaml:"width"`

	// Console height in rows (console type only)
	// Example: 24
	Height int `json:"height" yaml:"height"`

	// Type of console to attach to (console or vga)
	// Example: console
	//
	// API extension: console_vga_type
	Type string `json:"type" yaml:"type"`
}
