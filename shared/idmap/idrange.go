package idmap

type IdRange struct {
	Isuid   bool
	Isgid   bool
	Startid int64
	Endid   int64
}

func (i *IdRange) Contains(id int64) bool {
	return id >= i.Startid && id <= i.Endid
}
