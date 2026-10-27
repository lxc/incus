package cfg

// Section holds QEMU configuration sections.
type Section struct {
	Name    string            `json:"name"`
	Comment string            `json:"comment"`
	Entries map[string]string `json:"entries"`
}
