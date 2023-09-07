package idmap

// IdmapEntry is a single idmap entry (line).
type IdmapEntry struct {
	Isuid    bool
	Isgid    bool
	Hostid   int64 // id as seen on the host - i.e. 100000
	Nsid     int64 // id as seen in the ns - i.e. 0
	Maprange int64
}

// IdmapSet is a list of IdmapEntry with some functions on it.
type IdmapSet struct {
	Idmap []IdmapEntry
}
