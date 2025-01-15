package cfg

// Entry holds single QEMU configuration Key-Value pairs.
type Entry struct {
	Key   string
	Value string
}

// Section holds QEMU configuration sections.
type Section struct {
	Name    string
	Comment string
	Entries []Entry
}
