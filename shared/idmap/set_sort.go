package idmap

// ByHostID allows for sorting an Set by host id.
type ByHostID Set

func (s ByHostID) Len() int {
	return len(s.Entries)
}

func (s ByHostID) Swap(i, j int) {
	s.Entries[i], s.Entries[j] = s.Entries[j], s.Entries[i]
}

func (s ByHostID) Less(i, j int) bool {
	return s.Entries[i].HostID < s.Entries[j].HostID
}
