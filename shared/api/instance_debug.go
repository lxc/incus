package api

// InstanceDebugRepairPost represents an instance repair request.
//
// swagger:model
//
// API extension: instances_debug_repair.
type InstanceDebugRepairPost struct {
	// The desired repair action.
	// Example: rebuild-config-volume
	Action string `json:"action" yaml:"action"`
}
