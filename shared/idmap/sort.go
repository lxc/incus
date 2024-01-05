package idmap

type ByHostid []*IdmapEntry

func (s ByHostid) Len() int {
	return len(s)
}

func (s ByHostid) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s ByHostid) Less(i, j int) bool {
	return s[i].Hostid < s[j].Hostid
}
