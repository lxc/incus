//go:build linux && cgo && !agent

package warningtype

// Status represents the warning status.
type Status int

const (
	// StatusNew represents the New WarningStatus.
	StatusNew Status = 1
	// StatusAcknowledged represents the Acknowledged WarningStatus.
	StatusAcknowledged Status = 2
	// StatusResolved represents the Resolved WarningStatus.
	StatusResolved Status = 3
)

// Statuses associates a warning code to its name.
var Statuses = map[Status]string{
	StatusNew:          "new",
	StatusAcknowledged: "acknowledged",
	StatusResolved:     "resolved",
}

// StatusTypes associates a warning status to its type code.
var StatusTypes = map[string]Status{}

func init() {
	for code, name := range Statuses {
		StatusTypes[name] = code
	}
}
