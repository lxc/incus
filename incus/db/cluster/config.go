//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t config.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e config objects
//go:generate mapper stmt -e config create struct=Config
//go:generate mapper stmt -e config delete
//
//go:generate mapper method -i -e config GetMany
//go:generate mapper method -i -e config Create struct=Config
//go:generate mapper method -i -e config Update struct=Config
//go:generate mapper method -i -e config DeleteMany

// Config is a reference struct representing one configuration entry of another entity.
type Config struct {
	ID          int `db:"primary=yes"`
	ReferenceID int
	Key         string
	Value       string
}

// ConfigFilter specifies potential query parameter fields.
type ConfigFilter struct {
	Key   *string
	Value *string
}
