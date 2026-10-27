package api

// InstanceExecControl represents a message on the instance exec "control" socket.
//
// API extension: instances.
type InstanceExecControl struct {
	Command string            `json:"command" yaml:"command"`
	Args    map[string]string `json:"args" yaml:"args"`
	Signal  int               `json:"signal" yaml:"signal"`
}

// InstanceExecPost represents an instance exec request.
//
// swagger:model
//
// API extension: instances.
type InstanceExecPost struct {
	// Command and its arguments
	// Example: ["bash"]
	Command []string `json:"command" yaml:"command"`

	// Whether to wait for all websockets to be connected before spawning the command
	// Example: true
	WaitForWS bool `json:"wait-for-websocket" yaml:"wait-for-websocket"`

	// Whether the command is to be spawned in interactive mode (singled PTY instead of 3 PIPEs)
	// Example: true
	Interactive bool `json:"interactive" yaml:"interactive"`

	// Additional environment to pass to the command
	// Example: {"FOO": "BAR"}
	Environment map[string]string `json:"environment" yaml:"environment"`

	// Terminal width in characters (for interactive)
	// Example: 80
	Width int `json:"width" yaml:"width"`

	// Terminal height in rows (for interactive)
	// Example: 24
	Height int `json:"height" yaml:"height"`

	// Whether to capture the output for later download (requires non-interactive)
	RecordOutput bool `json:"record-output" yaml:"record-output"`

	// UID of the user to spawn the command as
	// Example: 1000
	User uint32 `json:"user" yaml:"user"`

	// GID of the user to spawn the command as
	// Example: 1000
	Group uint32 `json:"group" yaml:"group"`

	// Current working directory for the command
	// Example: /home/foo/
	Cwd string `json:"cwd" yaml:"cwd"`
}
