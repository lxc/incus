package idmap

// Range represents a range of uid or gid.
type Range struct {
	IsUID   bool
	IsGID   bool
	StartID int64
	EndID   int64
}

// Contains checks whether the range contains a particular uid/gid.
func (i *Range) Contains(id int64) bool {
	return id >= i.StartID && id <= i.EndID
}
