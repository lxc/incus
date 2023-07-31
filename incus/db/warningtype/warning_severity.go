//go:build linux && cgo && !agent

package warningtype

// Severity represents the warning severity.
type Severity int

const (
	// SeverityLow represents the low Severity.
	SeverityLow Severity = 1
	// SeverityModerate represents the moderate Severity.
	SeverityModerate Severity = 2
	// SeverityHigh represents the high Severity.
	SeverityHigh Severity = 3
)

// Severities associates a severity code to its name.
var Severities = map[Severity]string{
	SeverityLow:      "low",
	SeverityModerate: "moderate",
	SeverityHigh:     "high",
}

// SeverityTypes associates a warning severity to its type code.
var SeverityTypes = map[string]Severity{}

func init() {
	for code, name := range Severities {
		SeverityTypes[name] = code
	}
}
