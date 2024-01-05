package idmap

// IDRange represents a range of uid or gid.
type IDRange struct {
	Isuid   bool
	Isgid   bool
	Startid int64
	Endid   int64
}

// Contains checks whether the range contains a particular uid/gid.
func (i *IDRange) Contains(id int64) bool {
	return id >= i.Startid && id <= i.Endid
}
