package instance

// InstanceAction indicates the type of action being performed.
type InstanceAction string

// InstanceAction types.
const (
	Stop     InstanceAction = "stop"
	Start    InstanceAction = "start"
	Restart  InstanceAction = "restart"
	Freeze   InstanceAction = "freeze"
	Unfreeze InstanceAction = "unfreeze"
)
