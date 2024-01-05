package idmap

// ByHostID allows for sorting an IdmapSet by host id.
type ByHostID IdmapSet

func (s ByHostID) Len() int {
	return len(s.Idmap)
}

func (s ByHostID) Swap(i, j int) {
	s.Idmap[i], s.Idmap[j] = s.Idmap[j], s.Idmap[i]
}

func (s ByHostID) Less(i, j int) bool {
	return s.Idmap[i].Hostid < s.Idmap[j].Hostid
}
